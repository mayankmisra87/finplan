package engine

import (
	"math"
	"sort"
)

// RunPlan is the top-level entry point.
// It mirrors fixFinancials() in financial-timeline.js and orchestrates:
//  1.  PPF / EPF / NPS cashflow setup (NPS: 60% lump sum + 40% annuity)
//  2.  Date expansion (R/L tokens)
//  3.  Loan EMI map construction
//  4.  Future income / windfall map construction
//  5.  Income and expense growth map construction
//  6.  SIP allocation derivation
//  7.  Per-goal FV allocation (OverallFVAllocation)
//  8.  Extra income items construction
//  9.  Two-pass lumpsum optimisation (exact savings simulation)
//  10. FinancialSolution (core waterfall, retirement-first lumpsum)
//  11. Ideal asset allocation calculation
//  12. Minimum retirement SIP (computeRetirementSIP)
func RunPlan(p PlanParams) PlanResult {
	// ── Deep-copy mutable slices ──────────────────────────────────────────
	goals := make([]Goal, len(p.Goals))
	copy(goals, p.Goals)
	portfolioParams := make([]PortfolioParam, len(p.PortfolioParams))
	copy(portfolioParams, p.PortfolioParams)
	incomes := make([]Income, len(p.Incomes))
	copy(incomes, p.Incomes)

	today := todayFirstOfMonth()
	dobDate := parseDate(p.DOB)
	retirementDate := dobDate.AddDate(p.RetirementAge, 0, 0)
	retirementDateStr := formatDate(retirementDate)
	if retirementDate.Before(today) {
		retirementDateStr = formatDate(today)
	}
	n := math.Max(0, float64(parseDate(retirementDateStr).Year()-today.Year()))

	// ── 1. Attach PPF / EPF / NPS as retirement-date cashflows ──────────
	// npsAnnuityItems accumulates the 40% annuity streams from NPS holdings;
	// they are appended to extraIncomeItems in step 8.
	npsAnnuityItems := []extraIncomeItem{}
	lifeExpEndDate := formatDate(dobDate.AddDate(p.LifeExpectancy, 0, 0))

	for _, rc := range p.RetirementBasedCashflows {
		switch rc.AssetType {
		case "PPF":
			interest := rc.MarketValue * (math.Pow(1+PPFRate, n) - 1)
			portfolioParams = append(portfolioParams, PortfolioParam{
				ID:           rc.ID,
				AssetType:    "PPF",
				MaturityDate: retirementDateStr,
				MaturityAmount: rc.MarketValue,
				IsBreakable:  true,
				Interests:    []Interest{{Date: retirementDateStr, Amount: interest}},
			})
		case "EPF":
			contrib := rc.EPFLastContribution
			if contrib == 0 {
				contrib = 60000
			}
			interest := fv(EPFRate, n, contrib, rc.MarketValue) - rc.MarketValue
			portfolioParams = append(portfolioParams, PortfolioParam{
				ID:           rc.ID,
				AssetType:    "EPF",
				MaturityDate: retirementDateStr,
				MaturityAmount: rc.MarketValue,
				IsBreakable:  false, // FIX #5: EPF cannot be redeemed early
				Interests:    []Interest{{Date: retirementDateStr, Amount: interest}},
			})
		case "NPS":
			// Grow the NPS corpus at the Tier-1 equity rate.
			contrib := rc.NPSLastContribution
			if contrib == 0 {
				contrib = 60000 // fallback: ₹60k annual contribution
			}
			npsCorpus := fv(NPSRate, n, contrib, rc.MarketValue)

			// 60% lump sum — tax-free withdrawal, arrives at retirement.
			lumpsum := npsCorpus * NPSLumpsumFraction
			portfolioParams = append(portfolioParams, PortfolioParam{
				ID:             rc.ID,
				AssetType:      "NPS",
				MaturityDate:   retirementDateStr,
				MaturityAmount: lumpsum,
				IsBreakable:    true,
				Interests:      []Interest{},
			})

			// 40% annuity — fixed monthly income from retirement to end of life.
			annuityCorpus := npsCorpus * NPSAnnuityFraction
			monthlyAnnuity := annuityCorpus * NPSAnnuityRate / 12
			npsAnnuityItems = append(npsAnnuityItems, extraIncomeItem{
				ID:        rc.ID,
				Name:      "NPS Annuity",
				Amount:    monthlyAnnuity,
				Growth:    0, // annuity payouts are contractually fixed
				StartDate: retirementDateStr,
				EndDate:   lifeExpEndDate,
			})
		}
	}

	// ── 2. Expand R/L tokens in goals and incomes ─────────────────────────
	for i := range goals {
		if goals[i].OriginalStartDate == "" {
			goals[i].OriginalStartDate = goals[i].StartDate
			goals[i].OriginalEndDate = goals[i].EndDate
		}
		FormatStartTime(&goals[i], p.DOB, p.RetirementAge, p.LifeExpectancy)
	}
	for i := range incomes {
		if incomes[i].OriginalStartDate == "" {
			incomes[i].OriginalStartDate = incomes[i].StartDate
			incomes[i].OriginalEndDate = incomes[i].EndDate
		}
		FormatIncomeStartTime(&incomes[i], p.DOB, p.RetirementAge, p.LifeExpectancy)
	}

	// ── 3. Build loan EMI map ─────────────────────────────────────────────
	allLoans := make([]Loan, len(p.Loans))
	copy(allLoans, p.Loans)
	for _, g := range goals {
		if g.IsLoan {
			emi := loanEMI(loanPrincipal(g), g.LoanTenure, g.LoanInterestRate)
			endDate := parseDate(g.StartDate).AddDate(g.LoanTenure, 0, 0)
			allLoans = append(allLoans, Loan{
				StartDate: g.StartDate,
				EndDate:   formatDate(endDate),
				Amount:    emi,
			})
		}
	}
	loanEmis := buildLoanEMIMap(allLoans)

	// ── 4. Build future incomes / windfalls maps ──────────────────────────
	futureIncomesByDate := make(map[string]float64)
	fi := []Income{} // recurring
	wfList := []windfall{}

	for _, inc := range incomes {
		if inc.PayoutFrequency == "Lumpsum" {
			wfList = append(wfList, windfall{
				ID:        inc.ID,
				Name:      inc.Name,
				Amount:    inc.Amount,
				StartDate: inc.StartDate,
			})
			futureIncomesByDate[inc.StartDate] += inc.Amount
		} else {
			fi = append(fi, inc)
		}
	}

	// Expand recurring incomes into monthly amounts
	for _, inc := range fi {
		months := NumMonths[inc.PayoutFrequency]
		if months == 0 {
			continue
		}
		sd := firstOfMonth(parseDate(inc.StartDate))
		ed := firstOfMonth(parseDate(inc.EndDate))
		curYear := sd.Year()
		curAmount := inc.Amount / float64(months)
		for !sd.Before(ed) == false {
			k := formatDate(sd)
			if curYear != sd.Year() {
				curAmount *= (1 + inc.GrowthRate/100)
				curYear = sd.Year()
			}
			futureIncomesByDate[k] += curAmount
			sd = addMonths(sd, 1)
		}
	}

	// Add FD interest payouts to future income map
	for _, pp := range portfolioParams {
		for _, interest := range pp.Interests {
			futureIncomesByDate[interest.Date] += interest.Amount
		}
	}

	// ── 5. Build growth maps ──────────────────────────────────────────────
	endYear := dobDate.Year() + p.LifeExpectancy
	incomeGrowth := buildGrowthMap(p.IncomeParams, today.Year(), endYear)
	expenseGrowth := buildGrowthMap(p.ExpenseParams, today.Year(), endYear)

	// Compute retirement expense (what monthly expense will be at retirement)
	retirementExpense := buildRetirementExpense(
		p.MonthlyExpense, p.ExpenseParams, today.Year(),
		parseDate(retirementDateStr).Year(),
	)

	// Average expense growth rate (for inflation buffer calculation)
	avgExpenseGrowth := p.ExpenseParams[0].Value // first param as baseline

	// ── 6. Derive SIP allocation ──────────────────────────────────────────
	var sipEquity, sipDebt, sipLiquid float64
	totalSip := 0.0
	for _, s := range p.SIPs {
		totalSip += s.Amount
		sipEquity += s.EquityPercentage
		sipDebt += s.DebtPercentage
		sipLiquid += s.LiquidPercentage
	}
	overallSipAllocation := AllocationSplit{}
	if len(p.SIPs) > 0 {
		n := float64(len(p.SIPs))
		overallSipAllocation = AllocationSplit{
			Equity: sipEquity / n,
			Debt:   sipDebt / n,
			Liquid: sipLiquid / n,
		}
	}
	_ = overallSipAllocation // used in future walkthrough integration

	// ── 7. Compute per-goal FV allocation ────────────────────────────────
	goalsAllocation, retirementAlloc := OverallFVAllocation(
		p.RetirementAssetAllocation,
		p.AssetAllocation,
		goals,
		p.PortfolioBreakup,
	)

	// ── 8. Build extra income items ─────────────────────────────────────
	extraIncomeItems := make([]extraIncomeItem, 0, len(fi))
	for _, inc := range fi {
		months := NumMonths[inc.PayoutFrequency]
		if months == 0 {
			months = 1
		}
		extraIncomeItems = append(extraIncomeItems, extraIncomeItem{
			ID:        inc.ID,
			Name:      inc.Name,
			Amount:    inc.Amount / float64(months),
			Growth:    inc.GrowthRate,
			StartDate: inc.StartDate,
			EndDate:   inc.EndDate,
		})
	}
	// Append NPS annuity streams collected in step 1.
	// These are fixed monthly incomes from retirement to end of life that
	// reduce the net cash the retirement corpus must fund.
	extraIncomeItems = append(extraIncomeItems, npsAnnuityItems...)

	// ── 9. Two-pass lumpsum optimisation ─────────────────────────────────
	// Determine the minimum lumpsum needed for each non-retirement goal so
	// that the remainder is preserved for retirement.  The exact savings
	// simulation (simulateSavingsCoverage) mirrors the FinancialSolution
	// inner loop, so Pass 1 is free of approximation errors.
	lumpsumPerGoal, retirementReserved := twoPassLumpsumAllocation(
		goals,
		goalsAllocation,
		p.CurrentPortfolioValue,
		p.MonthlyIncome,
		p.MonthlyExpense,
		incomeGrowth,
		expenseGrowth,
		portfolioParams,
		wfList,
		loanEmis,
		totalSip,
		extraIncomeItems,
		retirementDate,
		avgExpenseGrowth,
	)

	// ── 10. Run core waterfall ────────────────────────────────────────────
	// lumpsumPerGoal caps each non-retirement goal's lumpsum draw so that
	// retirement's reserved share stays in remainingPortfolio until the
	// retirement goal is processed.
	goalResults, sipSchedule := FinancialSolution(
		p.RetirementAge, p.LifeExpectancy,
		p.CurrentPortfolioValue,
		0, // initialLiquidAmount (windfalls handled separately)
		p.MonthlyIncome, p.MonthlyExpense, retirementExpense,
		incomeGrowth, expenseGrowth,
		portfolioParams,
		p.DOB,
		goals,
		goalsAllocation,
		wfList,
		extraIncomeItems,
		loanEmis,
		totalSip,
		avgExpenseGrowth,
		lumpsumPerGoal,
	)

	// ── 11. Compute ideal asset allocation ───────────────────────────────
	type goalEquityItem struct {
		goalTarget float64
		lumpsum    float64
		equity     float64
		debt       float64
		liquid     float64
	}

	geItems := make([]goalEquityItem, 0, len(goalResults))
	todayYear := today.Year()

	for _, gr := range goalResults {
		// Find the original goal to get its icon
		var icon string
		for _, g := range goals {
			if g.ID == gr.ID {
				icon = g.Icon
				break
			}
		}

		isDefault := icon == "default"
		isEmergency := gr.Name == GoalEmergencyFund

		var eq, dt, lq float64
		if isDefault && !isEmergency {
			eq = retirementAlloc.Equity
			dt = retirementAlloc.Debt
			lq = retirementAlloc.Liquid
		} else {
			if alloc, ok := goalsAllocation[gr.ID]; ok {
				if ya, ok2 := alloc[todayYear]; ok2 {
					eq = ya.Equity
					dt = ya.Debt
					lq = ya.Liquid
				}
			}
		}

		geItems = append(geItems, goalEquityItem{
			goalTarget: gr.TaxTotal,
			lumpsum:    gr.Attachments.Lumpsum.Amount,
			equity:     eq,
			debt:       dt,
			liquid:     lq,
		})
	}

	totalLumpsumAllocated := 0.0
	for _, ge := range geItems {
		totalLumpsumAllocated += ge.lumpsum
	}

	idealAlloc := AllocationSplit{}
	if totalLumpsumAllocated > 0 {
		tE, tD, tL := 0.0, 0.0, 0.0
		for _, ge := range geItems {
			tE += ge.equity * ge.lumpsum
			tD += ge.debt * ge.lumpsum
			tL += ge.liquid * ge.lumpsum
		}
		idealAlloc = AllocationSplit{
			Equity: math.Round(100*tE/totalLumpsumAllocated) / 100,
			Debt:   math.Round(100*tD/totalLumpsumAllocated) / 100,
			Liquid: math.Round(100*tL/totalLumpsumAllocated) / 100,
		}
	}

	// Current allocation
	totPort := p.PortfolioBreakup.Equity.Amount +
		p.PortfolioBreakup.Debt.Amount +
		p.PortfolioBreakup.Liquid.Amount
	currentAlloc := AllocationSplit{Total: totPort}
	if totPort > 0 {
		currentAlloc.Equity = p.PortfolioBreakup.Equity.Amount / totPort
		currentAlloc.Debt = p.PortfolioBreakup.Debt.Amount / totPort
		currentAlloc.Liquid = p.PortfolioBreakup.Liquid.Amount / totPort
	}

	// Sort SIP schedule by year
	sort.Slice(sipSchedule, func(i, j int) bool {
		return sipSchedule[i].Year < sipSchedule[j].Year
	})

	// ── 12. Compute minimum retirement SIP ───────────────────────────────
	// Find the retirement goal corpus from the waterfall result.
	retGoalID := -1
	for _, g := range goals {
		if g.Name == GoalRetirement && g.Icon == "default" {
			retGoalID = g.ID
			break
		}
	}
	retirementCorpus := 0.0
	if retGoalID >= 0 {
		for _, gr := range goalResults {
			if gr.ID == retGoalID {
				retirementCorpus = gr.TaxTotal
				break
			}
		}
	}

	// Sum PPF + EPF + NPS(60%) lump sums at retirement.
	// These are the ring-fenced cashflows added in step 1.
	// NPS MaturityAmount already holds only the 60% lump sum fraction;
	// the 40% annuity is modelled as extra income and is not counted here.
	ppfEPFAtRetirement := 0.0
	for _, pp := range portfolioParams {
		if pp.AssetType == "PPF" || pp.AssetType == "EPF" || pp.AssetType == "NPS" {
			ppfEPFAtRetirement += pp.MaturityAmount
			for _, interest := range pp.Interests {
				ppfEPFAtRetirement += interest.Amount
			}
		}
	}

	// FV multiplier and blended growth rate for the retirement allocation.
	retLumpsumFV := 1.0
	retGrowthRate := 0.0
	if retGoalID >= 0 {
		if alloc, ok := goalsAllocation[retGoalID]; ok {
			if ya, ok2 := alloc[today.Year()]; ok2 {
				retLumpsumFV = ya.FV
				retGrowthRate = ya.Growth
			}
		}
	}

	// Months from today to retirement (clamped to zero if already retired).
	monthsToRetirement := 0
	if retirementDate.After(today) {
		d := retirementDate.Sub(today)
		monthsToRetirement = int(d.Hours() / 24 / 30.4375)
	}

	retirementSIP := ComputeRetirementSIP(
		retirementCorpus,
		retirementReserved,
		retLumpsumFV,
		ppfEPFAtRetirement,
		monthsToRetirement,
		retGrowthRate,
	)

	return PlanResult{
		Goals:                  goalResults,
		IdealAssetAllocation:   idealAlloc,
		CurrentAssetAllocation: currentAlloc,
		RecommendedSIPSchedule: sipSchedule,
		RetirementExpense:      retirementExpense,
		RetirementSIP:          retirementSIP,
	}
}
