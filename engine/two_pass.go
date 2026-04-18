package engine

import "time"

// twoPassLumpsumAllocation determines the optimal amount of the
// existing portfolio lumpsum to withhold from near-term goals,
// allowing it to compound toward retirement instead.
//
// The logic:
//
//	Pass 1 — Simulate each non-retirement goal using savings only
//	          (no lumpsum) via simulateSavingsCoverage, which mirrors
//	          FinancialSolution's inner loop exactly.
//	          Determine which goals savings can fully cover.
//	Pass 2 — For goals that savings cannot cover, allocate the minimum
//	          lumpsum required to make them COMFORTABLE. Whatever
//	          lumpsum remains after closing all addressable gaps is
//	          preserved for the retirement goal.
//
// Returns: per-goal lumpsum allocation map (goalID → amount)
// and the remaining lumpsum to reserve for retirement.
func twoPassLumpsumAllocation(
	goals []Goal,
	goalsAllocation AllGoalsAllocation,
	totalLumpsum float64,
	streams []incomeStream,
	monthlyExpense float64,
	expenseGrowth map[int]float64,
	portfolioParams []PortfolioParam,
	windfalls []windfall,
	loanEmis map[string]float64,
	totalSipAmount float64,
	extraIncomes []extraIncomeItem,
	avgExpenseGrowth float64,
) (map[int]float64, float64) {

	today := todayFirstOfMonth()
	todayYear := today.Year()
	lumpsumPerGoal := make(map[int]float64)
	remainingLumpsum := totalLumpsum

	// Filter to non-retirement, non-emergency evaluating goals
	evalGoals := []Goal{}
	for _, g := range goals {
		if g.Name != GoalRetirement && g.Name != GoalEmergencyFund && g.Name != GoalWealthGoal {
			evalGoals = append(evalGoals, g)
		}
	}
	sortGoals(evalGoals)

	for _, g := range evalGoals {
		alloc := goalsAllocation[g.ID]
		goalDate := parseDate(g.EndDate)
		yearsToGoal := float64(goalDate.Year() - todayYear)
		if yearsToGoal < 0 {
			yearsToGoal = 0
		}

		// FIX #2: compounded inflation
		inflationFactor := pow(1+avgExpenseGrowth/100, yearsToGoal)
		if inflationFactor < 1.05 {
			inflationFactor = 1.05
		}
		goalTarget := effectiveGoalTarget(g) * inflationFactor

		// Pass 1: exact simulation of what savings alone can achieve.
		// Mirrors FinancialSolution's savings loop — no lumpsum deducted.
		savingsAchievable := simulateSavingsCoverage(
			streams,
			monthlyExpense,
			expenseGrowth,
			loanEmis, totalSipAmount, extraIncomes,
			alloc, today, goalDate,
			portfolioParams, windfalls,
		)

		if savingsAchievable >= goalTarget {
			// Pass 1: savings alone covers this goal — no lumpsum needed
			lumpsumPerGoal[g.ID] = 0
			continue
		}

		// Pass 2: how much lumpsum is needed to close the gap?
		gap := goalTarget - savingsAchievable
		lumpsumFV := 1.0
		if ya, ok := alloc[todayYear]; ok {
			lumpsumFV = ya.FV
		}
		lumpsumNeeded := 0.0
		if lumpsumFV > 0 {
			lumpsumNeeded = gap / lumpsumFV
		}

		// Allocate only what's available
		allocated := min(lumpsumNeeded, remainingLumpsum)
		lumpsumPerGoal[g.ID] = allocated
		remainingLumpsum -= allocated
	}

	// Remaining lumpsum is preserved for retirement
	return lumpsumPerGoal, remainingLumpsum
}

// simulateSavingsCoverage is an exact month-by-month simulation of how
// much a savings stream will produce toward a goal by its target date,
// with no lumpsum contribution. It mirrors FinancialSolution's inner
// savings loop faithfully:
//   - income and expense grow annually via their respective growth maps
//   - SIP is deducted from surplus while income exceeds expenses
//   - retirement transition zeros income and SIP
//   - extra recurring incomes are included and grown annually
//   - windfalls are credited on their start date
//   - maturing FD amounts and interest payouts are credited on their dates
//     (non-breakable FDs are skipped until their maturity date)
func simulateSavingsCoverage(
	streams []incomeStream,
	monthlyExpense float64,
	expenseGrowth map[int]float64,
	loanEmis map[string]float64,
	totalSipAmount float64,
	extraIncomes []extraIncomeItem,
	alloc GoalAllocation,
	startDate, goalDate time.Time,
	portfolioParams []PortfolioParam,
	windfalls []windfall,
) float64 {

	total := 0.0
	current := startDate
	currentYear := startDate.Year()
	expense := monthlyExpense
	sip := totalSipAmount

	// Copy streams so this simulation doesn't mutate the caller's slice.
	localStreams := make([]incomeStream, len(streams))
	copy(localStreams, streams)

	// Copy extra incomes so annual growth mutations stay local.
	currentExtraIncomes := make([]extraIncomeItem, len(extraIncomes))
	copy(currentExtraIncomes, extraIncomes)

	for current.Before(goalDate) {
		yr := current.Year()
		stringDate := formatDate(current)

		// Year rollover: apply income/expense/extra-income growth.
		if yr != currentYear {
			for j := range localStreams {
				if !localStreams[j].retired {
					if rate, ok := localStreams[j].growthByYear[currentYear]; ok {
						localStreams[j].current *= (1 + rate/100)
					}
				}
			}
			if rate, ok := expenseGrowth[currentYear]; ok {
				expense *= (1 + rate/100)
			}
			for j, e := range currentExtraIncomes {
				if stringDate >= e.StartDate && stringDate < e.EndDate {
					currentExtraIncomes[j].Amount *= (1 + e.Growth/100)
				}
			}
			currentYear = yr
		}

		// Per-stream retirement: each earner stops on their own date.
		for j := range localStreams {
			if !localStreams[j].retired && current.After(localStreams[j].retirementDate) {
				localStreams[j].retired = true
			}
		}
		if allStreamsRetired(localStreams) {
			sip = 0
		}

		currentFV := 1.0
		if ya, ok := alloc[yr]; ok {
			currentFV = ya.FV
		}

		// Monthly saving (mirrors FinancialSolution logic).
		monthlySaving := totalActiveIncome(localStreams) - expense
		if emi, ok := loanEmis[stringDate]; ok {
			monthlySaving -= emi
		}
		if sip > 0 && sip < monthlySaving {
			monthlySaving -= sip
		}
		if monthlySaving > 0 {
			total += monthlySaving * currentFV
		}

		// Extra recurring incomes.
		for _, e := range currentExtraIncomes {
			eStart := parseDate(e.StartDate)
			eEnd := parseDate(e.EndDate)
			if !current.Before(eStart) && !current.After(eEnd) {
				total += e.Amount * currentFV
			}
		}

		// Windfalls.
		for _, w := range windfalls {
			if w.StartDate == stringDate {
				total += w.Amount * currentFV
			}
		}

		// Maturing investments and interest payouts.
		for _, pp := range portfolioParams {
			if !pp.IsBreakable && current.Before(parseDate(pp.MaturityDate)) {
				continue
			}
			if pp.MaturityDate == stringDate {
				total += pp.MaturityAmount * currentFV
			}
			for _, interest := range pp.Interests {
				if interest.Date == stringDate {
					total += interest.Amount * currentFV
				}
			}
		}

		current = addMonths(current, 1)
	}

	return total
}

// pow is a local alias to avoid importing math in this file
func pow(base, exp float64) float64 {
	if exp == 0 {
		return 1
	}
	result := 1.0
	for i := 0; i < int(exp); i++ {
		result *= base
	}
	// Handle fractional exponent approximately (good enough for 6% inflation)
	frac := exp - float64(int(exp))
	if frac > 0 {
		// linear interpolation for the fractional year
		result *= (1 + base*frac - frac)
	}
	return result
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
