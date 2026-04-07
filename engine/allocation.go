package engine

import "time"

// YearAllocation holds the equity/debt/liquid mix and derived values
// for a single calendar year within a goal's allocation schedule.
type YearAllocation struct {
	Year   int
	Equity float64
	Debt   float64
	Liquid float64
	Growth float64 // blended % p.a.
	FV     float64 // future value multiplier: what ₹1 invested this year grows to by goal date
}

// GoalAllocation maps year → YearAllocation for one goal.
type GoalAllocation map[int]YearAllocation

// AllGoalsAllocation maps goalID → GoalAllocation.
type AllGoalsAllocation map[int]GoalAllocation

// resolveAllocation returns the equity/debt/liquid split for a goal
// given how many years remain until its target date.
// Mirrors the inner while loop in overallAllocation (asset-allocation.js).
func resolveAllocation(buckets []AllocationBucket, yearsRemaining int) (equity, debt, liquid float64) {
	// Start from the longest-horizon bucket, step down as years shrink
	i := len(buckets) - 1
	equity = buckets[i].Equity
	debt = buckets[i].Debt
	liquid = buckets[i].Liquid

	for i >= 0 && yearsRemaining < buckets[i].Years {
		equity = buckets[i].Equity
		debt = buckets[i].Debt
		liquid = buckets[i].Liquid
		i--
	}
	return
}

// computeGoalAllocation builds the year-by-year allocation schedule for
// a single goal. If the goal is a retirement/default goal and the
// portfolio has a meaningful existing allocation, the current portfolio
// mix is used (mirrors the special-case in asset-allocation.js).
func computeGoalAllocation(
	buckets []AllocationBucket,
	g Goal,
	portfolioBreakup PortfolioBreakup,
	usePortfolioMix bool,
) GoalAllocation {

	alloc := make(GoalAllocation)
	today := time.Now().Year()
	targetYear := parseDate(g.EndDate).Year()

	// Determine starting allocation
	eq, dt, lq := resolveAllocation(buckets, targetYear-today)

	// Special case: retirement/default goals use current portfolio mix
	// (mirrors asset-allocation.js overallAllocation logic)
	if usePortfolioMix {
		tot := portfolioBreakup.Equity.Amount +
			portfolioBreakup.Debt.Amount +
			portfolioBreakup.Liquid.Amount
		if tot > 0 {
			eq = portfolioBreakup.Equity.Amount / tot
			dt = portfolioBreakup.Debt.Amount / tot
			lq = portfolioBreakup.Liquid.Amount / tot
		}
	}

	// Walk year by year from today to goal year
	i := len(buckets) - 1
	// rewind i to match initial years remaining
	for i >= 0 && targetYear-today < buckets[i].Years {
		i--
	}

	currentYear := today
	for currentYear <= targetYear {
		growth := blendedGrowthRate(eq, dt, lq)
		alloc[currentYear] = YearAllocation{
			Year:   currentYear,
			Equity: eq,
			Debt:   dt,
			Liquid: lq,
			Growth: growth,
		}
		currentYear++

		// Step down to next bucket as goal gets closer
		if i >= 0 && targetYear-currentYear <= buckets[i].Years {
			eq = buckets[i].Equity
			dt = buckets[i].Debt
			lq = buckets[i].Liquid
			i--
		}
	}

	return alloc
}

// computeFV populates the FV field for each year in a GoalAllocation.
// FV walks backwards from the goal year to today.
// FV[goalYear] = 1.0 (by definition)
// FV[y] = FV[y+1] * (1 + growth[y]/100)
// This means: ₹1 invested in year y grows to FV[y] by the goal date.
func computeFV(alloc GoalAllocation, today, targetYear int) {
	fvVal := 1.0
	for y := targetYear; y >= today; y-- {
		if a, ok := alloc[y]; ok {
			a.FV = fvVal
			alloc[y] = a
			fvVal *= (1 + a.Growth/100)
		}
	}
}

// OverallFVAllocation is the main entry point for allocation computation.
// It computes GoalAllocation (with FV) for every goal.
// Returns the per-goal allocation map and the retirement allocation
// (used for lumpsum ideal allocation calculation).
//
// Mirrors overallFVAllocation in asset-allocation.js.
func OverallFVAllocation(
	retirementBuckets []AllocationBucket,
	otherBuckets []AllocationBucket,
	goals []Goal,
	portfolioBreakup PortfolioBreakup,
) (AllGoalsAllocation, YearAllocation) {

	today := time.Now().Year()
	result := make(AllGoalsAllocation)
	var retirementAlloc YearAllocation

	for _, g := range goals {
		isDefault := g.Icon == "default"
		isEmergency := g.Name == GoalEmergencyFund

		// Choose bucket set
		buckets := otherBuckets
		if isDefault {
			buckets = retirementBuckets
		}

		// Use portfolio mix for retirement/default goals (not emergency)
		usePortfolioMix := isDefault && !isEmergency

		alloc := computeGoalAllocation(buckets, g, portfolioBreakup, usePortfolioMix)

		targetYear := parseDate(g.EndDate).Year()
		computeFV(alloc, today, targetYear)

		result[g.ID] = alloc

		// Capture retirement allocation for lumpsum ideal calc
		if isDefault && !isEmergency {
			if a, ok := alloc[today]; ok {
				retirementAlloc = a
			}
		}
	}

	return result, retirementAlloc
}
