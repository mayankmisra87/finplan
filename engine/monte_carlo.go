package engine

import (
	"math"
	"math/rand"
	"sort"
	"sync"
)

// ReturnAssumptions defines the mean and standard deviation for
// each asset class return. Defaults model Indian market history.
type ReturnAssumptions struct {
	EquityMean     float64 // % p.a., default 12.0
	EquitySigma    float64 // % p.a., default 8.0
	DebtMean       float64 // default 7.0
	DebtSigma      float64 // default 2.0
	LiquidMean     float64 // default 3.5
	LiquidSigma    float64 // default 0.5
	InflationMean  float64 // default 6.0
	InflationSigma float64 // default 1.5
}

// DefaultReturnAssumptions returns sensible defaults for Indian markets.
func DefaultReturnAssumptions() ReturnAssumptions {
	return ReturnAssumptions{
		EquityMean:     12.0,
		EquitySigma:    8.0,
		DebtMean:       7.0,
		DebtSigma:      2.0,
		LiquidMean:     3.5,
		LiquidSigma:    0.5,
		InflationMean:  6.0,
		InflationSigma: 1.5,
	}
}

// simRates holds perturbed per-asset return rates for a single simulation run.
type simRates struct {
	equity float64
	debt   float64
	liquid float64
}

// RunMonteCarlo runs the financial plan `runs` times with perturbed return
// assumptions and returns goal probability bands plus a year-by-year net
// worth trajectory at P25/P50/P75.
func RunMonteCarlo(p PlanParams, runs int, assumptions ReturnAssumptions) MonteCarloResult {
	if runs <= 0 {
		runs = 500
	}

	type runResult struct {
		idx       int
		plan      PlanResult
		snapshots []YearlySnapshot
	}

	ch := make(chan runResult, runs)
	var wg sync.WaitGroup

	for i := 0; i < runs; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			perturbed, rates := perturbParamsAndRates(p, assumptions)
			plan := RunPlan(perturbed)
			proj := runProjectionWithRates(perturbed, rates.equity, rates.debt, rates.liquid)
			ch <- runResult{idx, plan, proj.Snapshots}
		}(i)
	}
	go func() { wg.Wait(); close(ch) }()

	planResults  := make([]PlanResult, runs)
	allSnapshots := make([][]YearlySnapshot, runs)
	for r := range ch {
		planResults[r.idx]  = r.plan
		allSnapshots[r.idx] = r.snapshots
	}

	deterministic     := RunPlan(p)
	goalProbabilities := aggregateGoalProbabilities(deterministic, planResults)
	trajectoryBands   := computeTrajectoryBands(allSnapshots)

	return MonteCarloResult{
		Deterministic:     deterministic,
		GoalProbabilities: goalProbabilities,
		TrajectoryBands:   trajectoryBands,
		Runs:              runs,
	}
}

// perturbParamsAndRates samples return assumptions and returns both a modified
// PlanParams (with sampled inflation applied to expenses) and the sampled rates.
func perturbParamsAndRates(p PlanParams, a ReturnAssumptions) (PlanParams, simRates) {
	eq  := math.Max(a.EquityMean+normalSample()*a.EquitySigma, -20.0)
	db  := math.Max(a.DebtMean+normalSample()*a.DebtSigma, 1.0)
	lq  := math.Max(a.LiquidMean+normalSample()*a.LiquidSigma, 1.0)
	inf := math.Max(a.InflationMean+normalSample()*a.InflationSigma, 2.0)

	perturbed := p
	perturbed.ExpenseParams = []GrowthParam{{
		Range: p.ExpenseParams[0].Range,
		Value: inf,
	}}
	return perturbed, simRates{eq, db, lq}
}

// perturbParams is the legacy wrapper kept for backward compatibility.
func perturbParams(p PlanParams, a ReturnAssumptions) PlanParams {
	pp, _ := perturbParamsAndRates(p, a)
	return pp
}

// computeTrajectoryBands transposes per-run snapshots into per-year P25/P50/P75 bands.
func computeTrajectoryBands(allSnapshots [][]YearlySnapshot) []TrajectoryYear {
	if len(allSnapshots) == 0 || len(allSnapshots[0]) == 0 {
		return nil
	}
	n      := len(allSnapshots[0])
	bands  := make([]TrajectoryYear, n)
	totals := make([]float64, 0, len(allSnapshots))

	for j := 0; j < n; j++ {
		totals = totals[:0]
		for _, snaps := range allSnapshots {
			if j < len(snaps) {
				totals = append(totals, snaps[j].Total)
			}
		}
		sort.Float64s(totals)
		bands[j] = TrajectoryYear{
			Year: allSnapshots[0][j].Year,
			P25:  percentile(totals, 25),
			P50:  percentile(totals, 50),
			P75:  percentile(totals, 75),
		}
	}
	return bands
}

// aggregateGoalProbabilities computes success probability and percentile
// outcomes for each goal across all Monte Carlo runs.
func aggregateGoalProbabilities(
	deterministic PlanResult,
	runs []PlanResult,
) []GoalProbability {

	if len(deterministic.Goals) == 0 {
		return nil
	}

	out := make([]GoalProbability, len(deterministic.Goals))

	for i, dg := range deterministic.Goals {
		projectedValues := make([]float64, 0, len(runs))
		successCount := 0

		for _, r := range runs {
			for _, rg := range r.Goals {
				if rg.ID == dg.ID {
					projectedValues = append(projectedValues, rg.ProjectedAmount)
					if rg.Tag == TagComfortable || rg.Tag == TagManageable {
						successCount++
					}
					break
				}
			}
		}

		sort.Float64s(projectedValues)

		p10, p50, p90 := 0.0, 0.0, 0.0
		if len(projectedValues) > 0 {
			p10 = percentile(projectedValues, 10)
			p50 = percentile(projectedValues, 50)
			p90 = percentile(projectedValues, 90)
		}

		successProb := 0.0
		if len(runs) > 0 {
			successProb = float64(successCount) / float64(len(runs))
		}

		out[i] = GoalProbability{
			ID:                 dg.ID,
			Name:               dg.Name,
			SuccessProbability: math.Round(successProb*1000) / 1000,
			MedianProjected:    p50,
			P10Projected:       p10,
			P90Projected:       p90,
			DeterministicTag:   dg.Tag,
		}
	}

	return out
}

// percentile returns the p-th percentile of a sorted slice.
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := (p / 100) * float64(len(sorted)-1)
	lo  := int(math.Floor(idx))
	hi  := int(math.Ceil(idx))
	if lo == hi {
		return sorted[lo]
	}
	return sorted[lo] + (idx-float64(lo))*(sorted[hi]-sorted[lo])
}

// normalSample returns a single standard normal sample using Box-Muller.
func normalSample() float64 {
	u1 := rand.Float64()
	u2 := rand.Float64()
	if u1 == 0 {
		u1 = 1e-10
	}
	return math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
}
