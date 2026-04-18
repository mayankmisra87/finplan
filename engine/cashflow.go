package engine

import "math"

// AnnualCashflow records all cash inflows and outflows for a single calendar year.
type AnnualCashflow struct {
	Year          int     `json:"year"`
	PrimaryIncome float64 `json:"primaryIncome"`
	SpouseIncome  float64 `json:"spouseIncome"`
	Expenses      float64 `json:"expenses"`
	LoanEMIs      float64 `json:"loanEMIs"`
	GoalCosts     float64 `json:"goalCosts"`
	ToEquity      float64 `json:"toEquity"`
	ToDebt        float64 `json:"toDebt"`
	ToLiquid      float64 `json:"toLiquid"`
	NPSAnnuity    float64 `json:"npsAnnuity"`
	Drawdown      float64 `json:"drawdown"`
}

// CashflowResult holds the full year-by-year cashflow breakdown for Sankey visualization.
type CashflowResult struct {
	Years             []AnnualCashflow `json:"years"`
	PrimaryRetireYear int              `json:"primaryRetireYear"`
	SpouseRetireYear  int              `json:"spouseRetireYear,omitempty"`
}

// RunCashflow computes annual cash inflows and outflows year-by-year.
func RunCashflow(p PlanParams) CashflowResult {
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

	incomeGrowth  := buildGrowthMap(p.IncomeParams, startYear, endYear+1)
	expenseGrowth := buildGrowthMap(p.ExpenseParams, startYear, endYear+1)
	var spouseIncomeGrowth map[int]float64
	if p.Spouse != nil && len(p.Spouse.IncomeParams) > 0 {
		spouseIncomeGrowth = buildGrowthMap(p.Spouse.IncomeParams, startYear, endYear+1)
	}

	// Expand R/L goal tokens
	goals := make([]Goal, len(p.Goals))
	copy(goals, p.Goals)
	for i := range goals {
		if goals[i].OriginalStartDate == "" {
			goals[i].OriginalStartDate = goals[i].StartDate
			goals[i].OriginalEndDate   = goals[i].EndDate
		}
		FormatStartTime(&goals[i], p.DOB, p.RetirementAge, p.LifeExpectancy)
	}

	// Loan EMI map (year → annual EMI total)
	allLoans := make([]Loan, len(p.Loans))
	copy(allLoans, p.Loans)
	for _, g := range goals {
		if g.IsLoan {
			emi     := loanEMI(loanPrincipal(g), g.LoanTenure, g.LoanInterestRate)
			endLoan := parseDate(g.StartDate).AddDate(g.LoanTenure, 0, 0)
			allLoans = append(allLoans, Loan{
				StartDate: g.StartDate,
				EndDate:   formatDate(endLoan),
				Amount:    emi,
			})
		}
	}
	loanEMIMap    := buildLoanEMIMap(allLoans)
	annualLoanEMI := make(map[int]float64)
	for date, monthlyEMI := range loanEMIMap {
		annualLoanEMI[parseDate(date).Year()] += monthlyEMI
	}

	// Inflation-adjusted one-time goal costs
	avgExpGrowth   := p.ExpenseParams[0].Value
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
		inflFactor := math.Pow(1+avgExpGrowth/100, float64(gy-startYear))
		if inflFactor < 1 {
			inflFactor = 1
		}
		goalCostByYear[gy] += effectiveGoalTarget(g) * inflFactor
	}

	// NPS annuity income per year
	npsAnnuityByYear := make(map[int]float64)
	addNPS := func(rcs []RetirementCashflow, retireYear int) {
		n := math.Max(0, float64(retireYear-startYear))
		for _, rc := range rcs {
			if rc.AssetType != "NPS" {
				continue
			}
			contrib := rc.NPSLastContribution
			if contrib == 0 {
				contrib = 60000
			}
			corpus := fv(NPSRate, n, contrib, rc.MarketValue)
			annual := corpus * NPSAnnuityFraction * NPSAnnuityRate
			for y := retireYear; y <= endYear; y++ {
				npsAnnuityByYear[y] += annual
			}
		}
	}
	addNPS(p.RetirementBasedCashflows, primaryRetireYear)
	if p.Spouse != nil {
		addNPS(p.Spouse.RetirementBasedCashflows, spouseRetireYear)
	}

	// SIP allocations per asset class (annual)
	sipEq, sipDb, sipLq := 0.0, 0.0, 0.0
	for _, s := range p.SIPs {
		annual := s.Amount * 12
		sipEq += annual * s.EquityPercentage
		sipDb += annual * s.DebtPercentage
		sipLq += annual * s.LiquidPercentage
	}
	sipTotal := sipEq + sipDb + sipLq

	primaryInc   := p.MonthlyIncome * 12
	spouseInc    := 0.0
	if p.Spouse != nil {
		spouseInc = p.Spouse.MonthlyIncome * 12
	}
	annualExpense := p.MonthlyExpense * 12

	cashflows := make([]AnnualCashflow, 0, endYear-startYear+1)

	for y := startYear; y <= endYear; y++ {
		cf := AnnualCashflow{
			Year:       y,
			Expenses:   annualExpense,
			LoanEMIs:   annualLoanEMI[y],
			GoalCosts:  goalCostByYear[y],
			NPSAnnuity: npsAnnuityByYear[y],
		}
		if y < primaryRetireYear {
			cf.PrimaryIncome = primaryInc
		}
		if spouseRetireYear > 0 && y < spouseRetireYear {
			cf.SpouseIncome = spouseInc
		}

		totalIncome := cf.PrimaryIncome + cf.SpouseIncome
		if y < lastRetireYear {
			fixedOut := cf.Expenses + cf.LoanEMIs + cf.GoalCosts
			surplus  := math.Max(0, totalIncome-fixedOut-sipTotal)
			yearsToRetire := primaryRetireYear - y
			var aEq, aDb, aLq float64
			if yearsToRetire > 0 {
				aEq, aDb, aLq = resolveAllocation(p.AssetAllocation, yearsToRetire)
			} else {
				aEq, aDb, aLq = resolveAllocation(p.RetirementAssetAllocation, 0)
			}
			cf.ToEquity = sipEq + surplus*aEq
			cf.ToDebt   = sipDb + surplus*aDb
			cf.ToLiquid = sipLq + surplus*aLq
		} else {
			cf.Drawdown = math.Max(0, cf.Expenses-cf.NPSAnnuity)
		}

		cashflows = append(cashflows, cf)

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

	result := CashflowResult{
		Years:             cashflows,
		PrimaryRetireYear: primaryRetireYear,
	}
	if spouseRetireYear > 0 {
		result.SpouseRetireYear = spouseRetireYear
	}
	return result
}
