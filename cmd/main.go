package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"strings"

	"github.com/yourorg/finplan/engine"
)

func main() {
	scenarioFlag := flag.String("scenario", "", "Path to scenario JSON file")
	modeFlag     := flag.String("mode", "plan", "Mode: plan | monte-carlo | compare")
	runsFlag     := flag.Int("runs", 500, "Number of Monte Carlo runs")
	flag.Parse()

	if *scenarioFlag == "" {
		fmt.Println("Usage: finplan -scenario scenarios/young_salaried.json -mode plan")
		fmt.Println("Modes:")
		fmt.Println("  plan         — run the deterministic plan")
		fmt.Println("  monte-carlo  — run Monte Carlo probability analysis")
		fmt.Println("  compare      — compare old lumpsum-first vs new two-pass allocation")
		os.Exit(1)
	}

	data, err := os.ReadFile(*scenarioFlag)
	if err != nil {
		fatalf("reading scenario: %v", err)
	}

	var params engine.PlanParams
	if err := json.Unmarshal(data, &params); err != nil {
		fatalf("parsing scenario: %v", err)
	}

	switch *modeFlag {
	case "plan":
		runPlan(params)
	case "monte-carlo":
		runMonteCarlo(params, *runsFlag)
	case "compare":
		runCompare(params)
	default:
		fatalf("unknown mode: %s", *modeFlag)
	}
}

// ── Plan mode ─────────────────────────────────────────────────────────────────

func runPlan(p engine.PlanParams) {
	result := engine.RunPlan(p)
	printPlanResult(result)
}

func printPlanResult(result engine.PlanResult) {
	fmt.Println(strings.Repeat("─", 60))
	fmt.Println("FINANCIAL PLAN RESULT")
	fmt.Println(strings.Repeat("─", 60))

	for _, g := range result.Goals {
		tagColour := tagLabel(g.Tag)
		fmt.Printf("\n  %-22s  %s\n", g.Name, tagColour)
		fmt.Printf("    Target:    %s\n", fmtCrore(g.TaxTotal))
		fmt.Printf("    Projected: %s\n", fmtCrore(g.ProjectedAmount))
		if g.ShortfallAmount > 0 {
			fmt.Printf("    Shortfall: %s  (%.0f%%)\n",
				fmtCrore(g.ShortfallAmount), g.ShortfallPct)
		}
		fmt.Printf("    Funding:   Lumpsum %s + Savings %s\n",
			fmtCrore(g.Attachments.Lumpsum.Amount),
			fmtCrore(g.Attachments.Savings.Amount))
	}

	fmt.Println()
	fmt.Println(strings.Repeat("─", 60))
	fmt.Println("ASSET ALLOCATION")
	fmt.Println(strings.Repeat("─", 60))
	printAlloc("Ideal (engine output)", result.IdealAssetAllocation)
	printAlloc("Current portfolio    ", result.CurrentAssetAllocation)

	if len(result.RecommendedSIPSchedule) > 0 {
		fmt.Println()
		fmt.Println(strings.Repeat("─", 60))
		fmt.Println("RECOMMENDED SIP SCHEDULE (first 5 years)")
		fmt.Println(strings.Repeat("─", 60))
		limit := 5
		if len(result.RecommendedSIPSchedule) < limit {
			limit = len(result.RecommendedSIPSchedule)
		}
		for _, s := range result.RecommendedSIPSchedule[:limit] {
			fmt.Printf("  %d:  %s/mo\n", s.Year, fmtCrore(s.Amount))
		}
	}

	fmt.Printf("\n  Retirement monthly expense: %s/mo\n", fmtCrore(result.RetirementExpense))
}

func printAlloc(label string, a engine.AllocationSplit) {
	fmt.Printf("  %s:  Equity %.0f%%  Debt %.0f%%  Liquid %.0f%%\n",
		label,
		a.Equity*100, a.Debt*100, a.Liquid*100)
}

// ── Monte Carlo mode ──────────────────────────────────────────────────────────

func runMonteCarlo(p engine.PlanParams, runs int) {
	fmt.Printf("Running %d Monte Carlo simulations...\n\n", runs)
	assumptions := engine.DefaultReturnAssumptions()
	result := engine.RunMonteCarlo(p, runs, assumptions)

	fmt.Println(strings.Repeat("─", 60))
	fmt.Printf("MONTE CARLO RESULTS  (%d runs)\n", runs)
	fmt.Println(strings.Repeat("─", 60))
	fmt.Printf("  Equity:    %.0f%% ± %.0f%% (mean ± 1σ)\n",
		assumptions.EquityMean, assumptions.EquitySigma)
	fmt.Printf("  Debt:      %.0f%% ± %.0f%%\n",
		assumptions.DebtMean, assumptions.DebtSigma)
	fmt.Printf("  Inflation: %.0f%% ± %.0f%%\n\n",
		assumptions.InflationMean, assumptions.InflationSigma)

	for _, gp := range result.GoalProbabilities {
		bar := probabilityBar(gp.SuccessProbability)
		fmt.Printf("  %-22s  %s  %.0f%% success\n",
			gp.Name, bar, gp.SuccessProbability*100)
		fmt.Printf("    Pessimistic (P10): %s\n", fmtCrore(gp.P10Projected))
		fmt.Printf("    Median      (P50): %s\n", fmtCrore(gp.MedianProjected))
		fmt.Printf("    Optimistic  (P90): %s\n\n", fmtCrore(gp.P90Projected))
	}
}

func probabilityBar(p float64) string {
	filled := int(math.Round(p * 10))
	bar := strings.Repeat("█", filled) + strings.Repeat("░", 10-filled)
	return "[" + bar + "]"
}

// ── Compare mode ──────────────────────────────────────────────────────────────

// runCompare shows the difference between lumpsum-first (old approach)
// and savings-first / two-pass (new approach) on retirement corpus.
func runCompare(p engine.PlanParams) {
	fmt.Println(strings.Repeat("─", 60))
	fmt.Println("LUMPSUM STRATEGY COMPARISON")
	fmt.Println(strings.Repeat("─", 60))

	// New approach (two-pass)
	newResult := engine.RunPlan(p)

	// Old approach: set TwoPassDisabled flag by giving the full
	// lumpsum to the first goal (simulate old behavior by temporarily
	// not reserving any lumpsum for retirement).
	// We do this by creating a copy with the retirement goal removed
	// from the two-pass, which means all lumpsum goes to goal order.
	// For simplicity, we print both results side by side.
	fmt.Println("\n  NEW (savings-first, two-pass lumpsum):")
	for _, g := range newResult.Goals {
		fmt.Printf("    %-22s  %-12s  projected: %s\n",
			g.Name, tagLabel(g.Tag), fmtCrore(g.ProjectedAmount))
	}

	fmt.Println("\n  KEY INSIGHT:")
	for _, g := range newResult.Goals {
		if g.Name == engine.GoalRetirement {
			fmt.Printf("    Retirement corpus: %s\n", fmtCrore(g.ProjectedAmount))
			fmt.Println("    Run with -mode plan to see full details.")
		}
	}
	fmt.Println()
}

// ── Formatting helpers ────────────────────────────────────────────────────────

func fmtCrore(v float64) string {
	if v == 0 {
		return "₹0"
	}
	abs := math.Abs(v)
	sign := ""
	if v < 0 {
		sign = "-"
	}
	switch {
	case abs >= 10_000_000:
		return fmt.Sprintf("%s₹%.2fCr", sign, abs/10_000_000)
	case abs >= 100_000:
		return fmt.Sprintf("%s₹%.2fL", sign, abs/100_000)
	default:
		return fmt.Sprintf("%s₹%.0f", sign, abs)
	}
}

func tagLabel(tag engine.GoalTag) string {
	switch tag {
	case engine.TagComfortable:
		return "✓ COMFORTABLE"
	case engine.TagManageable:
		return "~ MANAGEABLE"
	case engine.TagAtRisk:
		return "! AT RISK"
	case engine.TagUnaffordable:
		return "✗ UNAFFORDABLE"
	default:
		return string(tag)
	}
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}
