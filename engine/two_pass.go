package engine

import "time"

// twoPassLumpsumAllocation determines the optimal amount of the
// existing portfolio lumpsum to withhold from near-term goals,
// allowing it to compound toward retirement instead.
//
// The logic:
//  Pass 1 — Simulate each non-retirement goal using savings only
//            (no lumpsum). Determine which goals savings can fully cover.
//  Pass 2 — For goals that savings cannot cover, allocate the minimum
//            lumpsum required to make them COMFORTABLE. Whatever
//            lumpsum remains after closing all addressable gaps is
//            preserved for the retirement goal.
//
// Returns: per-goal lumpsum allocation map (goalID → amount)
// and the remaining lumpsum to reserve for retirement.
func twoPassLumpsumAllocation(
	goals []Goal,
	goalsAllocation AllGoalsAllocation,
	totalLumpsum float64,
	monthlySurplus float64, // approximate starting surplus
	incomeGrowth map[int]float64,
	portfolioParams []PortfolioParam,
	windfalls []windfall,
	loanEmis map[string]float64,
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

		// Pass 1: estimate what savings alone can achieve
		// Approximate by summing monthly surplus × growth FV factor
		// over the months until the goal. This is a heuristic — the
		// full savings simulation happens in FinancialSolution.
		savingsAchievable := estimateSavingsCoverage(
			monthlySurplus, incomeGrowth, loanEmis,
			alloc, today, goalDate, portfolioParams, windfalls,
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

// estimateSavingsCoverage is a lightweight approximation of how much
// a savings stream (growing with income) will produce toward a goal
// by its target date, taking into account known loan EMI deductions
// and existing cashflows (FD maturities, windfalls).
//
// This is intentionally approximate — the full month-by-month simulation
// happens in FinancialSolution. Here we just need to know: can savings
// alone plausibly cover this goal?
func estimateSavingsCoverage(
	monthlySurplus float64,
	incomeGrowth map[int]float64,
	loanEmis map[string]float64,
	alloc GoalAllocation,
	startDate, goalDate time.Time,
	portfolioParams []PortfolioParam,
	windfalls []windfall,
) float64 {

	total := 0.0
	current := startDate
	surplus := monthlySurplus
	currentYear := startDate.Year()

	for current.Before(goalDate) {
		yr := current.Year()
		if yr != currentYear {
			if rate, ok := incomeGrowth[currentYear]; ok {
				surplus *= (1 + rate/100)
			}
			currentYear = yr
		}

		stringDate := formatDate(current)
		s := surplus
		if emi, ok := loanEmis[stringDate]; ok {
			s -= emi
		}

		fvMultiplier := 1.0
		if ya, ok := alloc[yr]; ok {
			fvMultiplier = ya.FV
		}

		if s > 0 {
			total += s * fvMultiplier
		}

		// Add windfalls and maturing FDs that arrive before goal date
		for _, w := range windfalls {
			if w.StartDate == stringDate {
				total += w.Amount * fvMultiplier
			}
		}
		for _, pp := range portfolioParams {
			if pp.MaturityDate == stringDate {
				total += pp.MaturityAmount * fvMultiplier
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
