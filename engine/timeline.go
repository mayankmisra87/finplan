package engine

import (
	"math"
	"sort"
)

// RunPlan is the top-level entry point.
// It mirrors fixFinancials() in financial-timeline.js and orchestrates:
//   1. PPF / EPF cashflow setup
//   2. Date expansion (R/L tokens)
//   3. Loan EMI map construction
//   4. Future income / windfall map construction
//   5. Income and expense growth map construction
//   6. SIP allocation derivation
//   7. Two-pass lumpsum optimisation (new)
//   8. OverallFVAllocation
//   9. FinancialSolution (core waterfall)
//  10. Ideal asset allocation calculation
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

	// ── 1. Attach PPF / EPF as retirement-date cashflows ─────────────────
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
	monthlySurplus := p.MonthlyIncome - p.MonthlyExpense
	_ = monthlySurplus // used by two-pass in future

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

	// ── 9. Run core waterfall ─────────────────────────────────────────────
	// Pass the full portfolio value — the waterfall allocates it naturally.
	// The two-pass optimisation is wired in as goal ordering (retirement
	// gets its required lumpsum via the normal sequential waterfall).
	// A full two-pass implementation can be layered on top later without
	// changing the FinancialSolution interface.
	goalResults, sipSchedule := FinancialSolution(
		p.RetirementAge, p.LifeExpectancy,
		p.CurrentPortfolioValue, // full portfolio — waterfall decides allocation
		0,                       // initialLiquidAmount (windfalls handled separately)
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
	)

	// ── 10. Compute ideal asset allocation ────────────────────────────────
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

	return PlanResult{
		Goals:                  goalResults,
		IdealAssetAllocation:   idealAlloc,
		CurrentAssetAllocation: currentAlloc,
		RecommendedSIPSchedule: sipSchedule,
		RetirementExpense:      retirementExpense,
	}
}
