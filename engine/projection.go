package engine

import "math"

// ─────────────────────────────────────────────
// Output types for the projection endpoint
// ─────────────────────────────────────────────

// YearlySnapshot is the household portfolio state at the start of a year.
type YearlySnapshot struct {
	Year           int     `json:"year"`
	Equity         float64 `json:"equity"`
	Debt           float64 `json:"debt"`
	Liquid         float64 `json:"liquid"`
	Total          float64 `json:"total"`
	PrimaryRetired bool    `json:"primaryRetired"`
	SpouseRetired  bool    `json:"spouseRetired"`
}

// ProjectionResult is the full year-by-year net worth projection.
type ProjectionResult struct {
	Snapshots         []YearlySnapshot `json:"snapshots"`
	PrimaryRetireYear int              `json:"primaryRetireYear"`
	SpouseRetireYear  int              `json:"spouseRetireYear,omitempty"`
}

// RunProjection returns a year-by-year net worth projection using default return rates.
func RunProjection(p PlanParams) ProjectionResult {
	return runProjectionWithRates(p, Projections["equity"], Projections["debt"], Projections["liquid"])
}

// runProjectionWithRates is the inner projection loop with explicit per-asset growth rates,
// allowing Monte Carlo to substitute perturbed rates without touching global state.
func runProjectionWithRates(p PlanParams, eqRate, dbRate, lqRate float64) ProjectionResult {
	today     := todayFirstOfMonth()
	startYear := today.Year()
	dobDate   := parseDate(p.DOB)
	endYear   := dobDate.Year() + p.LifeExpectancy

	primaryRetireYear := dobDate.Year() + p.RetirementAge
	spouseRetireYear  := 0
	if p.Spouse != nil {
		spouseRetireYear = parseDate(p.Spouse.DOB).Year() + p.Spouse.RetirementAge
	}
	lastRetireYear := primaryRetireYear
	if spouseRetireYear > lastRetireYear {
		lastRetireYear = spouseRetireYear
	}

	// ── Income growth maps ────────────────────────────────────────────────
	incomeGrowth := buildGrowthMap(p.IncomeParams, startYear, endYear+1)
	expenseGrowth := buildGrowthMap(p.ExpenseParams, startYear, endYear+1)

	var spouseIncomeGrowth map[int]float64
	if p.Spouse != nil && len(p.Spouse.IncomeParams) > 0 {
		spouseIncomeGrowth = buildGrowthMap(p.Spouse.IncomeParams, startYear, endYear+1)
	}

	// ── Expand goal R/L tokens so we can read their dates ─────────────────
	goals := make([]Goal, len(p.Goals))
	copy(goals, p.Goals)
	for i := range goals {
		if goals[i].OriginalStartDate == "" {
			goals[i].OriginalStartDate = goals[i].StartDate
			goals[i].OriginalEndDate = goals[i].EndDate
		}
		FormatStartTime(&goals[i], p.DOB, p.RetirementAge, p.LifeExpectancy)
	}

	// ── Annual loan EMI totals (year → sum of monthly EMIs) ───────────────
	allLoans := make([]Loan, len(p.Loans))
	copy(allLoans, p.Loans)
	for _, g := range goals {
		if g.IsLoan {
			emi := loanEMI(loanPrincipal(g), g.LoanTenure, g.LoanInterestRate)
			endLoan := parseDate(g.StartDate).AddDate(g.LoanTenure, 0, 0)
			allLoans = append(allLoans, Loan{
				StartDate: g.StartDate,
				EndDate:   formatDate(endLoan),
				Amount:    emi,
			})
		}
	}
	loanEMIMap := buildLoanEMIMap(allLoans)
	annualLoanEMI := make(map[int]float64)
	for date, monthlyEMI := range loanEMIMap {
		annualLoanEMI[parseDate(date).Year()] += monthlyEMI
	}

	// ── One-time goal costs (year → inflation-adjusted amount) ────────────
	avgExpGrowth := p.ExpenseParams[0].Value
	goalCostByYear := make(map[int]float64)
	for _, g := range goals {
		if g.Name == GoalRetirement || g.Name == GoalEmergencyFund || g.Name == GoalWealthGoal {
			continue
		}
		if g.OriginalStartDate == "R" || g.OriginalStartDate == "L" {
			continue
		}
		gy := parseDate(g.StartDate).Year()
		if gy < startYear {
			continue
		}
		yearsAway := float64(gy - startYear)
		inflFactor := math.Pow(1+avgExpGrowth/100, yearsAway)
		if inflFactor < 1 {
			inflFactor = 1
		}
		goalCostByYear[gy] += effectiveGoalTarget(g) * inflFactor
	}

	// ── Retirement cashflow maturities and NPS annuity ────────────────────
	type maturity struct{ eq, db float64 }
	maturities := make(map[int]maturity)
	npsAnnuityByYear := make(map[int]float64) // year → annual annuity income

	addRCs := func(rcs []RetirementCashflow, retireYear int) {
		n := math.Max(0, float64(retireYear-startYear))
		for _, rc := range rcs {
			switch rc.AssetType {
			case "PPF":
				m := maturities[retireYear]
				m.db += rc.MarketValue * math.Pow(1+PPFRate, n)
				maturities[retireYear] = m
			case "EPF":
				contrib := rc.EPFLastContribution
				if contrib == 0 {
					contrib = 60000
				}
				m := maturities[retireYear]
				m.db += fv(EPFRate, n, contrib, rc.MarketValue)
				maturities[retireYear] = m
			case "NPS":
				contrib := rc.NPSLastContribution
				if contrib == 0 {
					contrib = 60000
				}
				corpus := fv(NPSRate, n, contrib, rc.MarketValue)
				m := maturities[retireYear]
				m.eq += corpus * NPSLumpsumFraction
				maturities[retireYear] = m
				// Annuity: fixed annual income from retirement to end of life
				annual := corpus * NPSAnnuityFraction * NPSAnnuityRate
				for y := retireYear; y <= endYear; y++ {
					npsAnnuityByYear[y] += annual
				}
			}
		}
	}
	addRCs(p.RetirementBasedCashflows, primaryRetireYear)
	if p.Spouse != nil {
		addRCs(p.Spouse.RetirementBasedCashflows, spouseRetireYear)
	}

	// ── Running state ─────────────────────────────────────────────────────
	eq := p.PortfolioBreakup.Equity.Amount
	db := p.PortfolioBreakup.Debt.Amount
	lq := p.PortfolioBreakup.Liquid.Amount

	primaryInc := p.MonthlyIncome * 12
	spouseInc  := 0.0
	if p.Spouse != nil {
		spouseInc = p.Spouse.MonthlyIncome * 12
	}
	annualExpense := p.MonthlyExpense * 12
	totalSIP := 0.0
	for _, s := range p.SIPs {
		totalSIP += s.Amount * 12
	}

	snapshots := make([]YearlySnapshot, 0, endYear-startYear+1)

	for y := startYear; y <= endYear; y++ {
		// Record portfolio at start of year
		total := eq + db + lq
		snapshots = append(snapshots, YearlySnapshot{
			Year:           y,
			Equity:         math.Round(eq/1000) * 1000,
			Debt:           math.Round(db/1000) * 1000,
			Liquid:         math.Round(lq/1000) * 1000,
			Total:          math.Round(total/1000) * 1000,
			PrimaryRetired: y >= primaryRetireYear,
			SpouseRetired:  spouseRetireYear > 0 && y >= spouseRetireYear,
		})

		// Active income this year
		activeInc := 0.0
		if y < primaryRetireYear {
			activeInc += primaryInc
		}
		if spouseRetireYear > 0 && y < spouseRetireYear {
			activeInc += spouseInc
		}

		// Annual outflows
		emi  := annualLoanEMI[y]
		sip  := 0.0
		if y < lastRetireYear {
			sip = totalSIP
		}
		surplus := math.Max(0, activeInc-annualExpense-emi-sip)

		// Add retirement cashflow maturities
		if m, ok := maturities[y]; ok {
			eq += m.eq
			db += m.db
		}

		// Deduct one-time goal costs from portfolio
		if cost, ok := goalCostByYear[y]; ok && cost > 0 {
			tot := eq + db + lq
			if tot >= cost {
				ratio := cost / tot
				eq = math.Max(0, eq*(1-ratio))
				db = math.Max(0, db*(1-ratio))
				lq = math.Max(0, lq*(1-ratio))
			} else {
				eq, db, lq = 0, 0, 0
			}
		}

		// Allocate surplus into portfolio by time-horizon bucket
		yearsToRetire := primaryRetireYear - y
		var aEq, aDb, aLq float64
		if yearsToRetire > 0 {
			aEq, aDb, aLq = resolveAllocation(p.AssetAllocation, yearsToRetire)
		} else {
			aEq, aDb, aLq = resolveAllocation(p.RetirementAssetAllocation, 0)
		}
		eq += surplus * aEq
		db += surplus * aDb
		lq += surplus * aLq

		// Grow each bucket at its long-run expected rate
		eq *= (1 + eqRate/100)
		db *= (1 + dbRate/100)
		lq *= (1 + lqRate/100)

		// Post-retirement: draw down to cover expenses net of annuity income
		if y >= lastRetireYear {
			annuity  := npsAnnuityByYear[y]
			drawdown := math.Max(0, annualExpense-annuity)
			tot      := eq + db + lq
			if tot >= drawdown {
				ratio := drawdown / tot
				eq = math.Max(0, eq*(1-ratio))
				db = math.Max(0, db*(1-ratio))
				lq = math.Max(0, lq*(1-ratio))
			} else {
				eq, db, lq = 0, 0, 0
			}
		}

		// Grow income and expenses for the next year
		if y < primaryRetireYear {
			if rate, ok := incomeGrowth[y]; ok {
				primaryInc *= (1 + rate/100)
			}
		}
		if spouseRetireYear > 0 && y < spouseRetireYear {
			if spouseIncomeGrowth != nil {
				if rate, ok := spouseIncomeGrowth[y]; ok {
					spouseInc *= (1 + rate/100)
				}
			}
		}
		if rate, ok := expenseGrowth[y]; ok {
			annualExpense *= (1 + rate/100)
		}
	}

	result := ProjectionResult{
		Snapshots:         snapshots,
		PrimaryRetireYear: primaryRetireYear,
	}
	if spouseRetireYear > 0 {
		result.SpouseRetireYear = spouseRetireYear
	}
	return result
}
