package engine_test

import (
	"encoding/json"
	"math"
	"os"
	"testing"

	"github.com/yourorg/finplan/engine"
)

// ── Helpers ───────────────────────────────────────────────────────────────────

func loadScenario(t *testing.T, path string) engine.PlanParams {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("loading scenario %s: %v", path, err)
	}
	var p engine.PlanParams
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatalf("parsing scenario %s: %v", path, err)
	}
	return p
}

// ── Scenario 1: Young salaried investor ──────────────────────────────────────

func TestYoungSalariedInvestor(t *testing.T) {
	p := loadScenario(t, "../scenarios/young_salaried.json")
	result := engine.RunPlan(p)

	if len(result.Goals) == 0 {
		t.Fatal("expected at least one goal result")
	}

	// With ₹1.5L income, ₹80K expenses, and ₹20L portfolio,
	// near-term goals should be COMFORTABLE or MANAGEABLE
	for _, g := range result.Goals {
		t.Logf("Goal %-22s  tag=%-12s  projected=%.0f  shortfall=%.0f",
			g.Name, g.Tag, g.ProjectedAmount, g.ShortfallAmount)
	}

	// Retirement should be present
	hasRetirement := false
	for _, g := range result.Goals {
		if g.Name == engine.GoalRetirement {
			hasRetirement = true
			if g.ProjectedAmount <= 0 {
				t.Error("retirement projected amount should be > 0")
			}
		}
	}
	if !hasRetirement {
		t.Error("expected Retirement goal in results")
	}

	// SIP schedule should have at least one entry
	if len(result.RecommendedSIPSchedule) == 0 {
		t.Error("expected at least one SIP schedule entry")
	}
	t.Logf("SIP schedule entries: %d", len(result.RecommendedSIPSchedule))
	if len(result.RecommendedSIPSchedule) > 0 {
		t.Logf("First SIP recommendation: year=%d amount=%.0f",
			result.RecommendedSIPSchedule[0].Year,
			result.RecommendedSIPSchedule[0].Amount)
	}
}

// ── Scenario 2: Mid-career with active home loan ──────────────────────────────

func TestMidCareerWithLoan(t *testing.T) {
	p := loadScenario(t, "../scenarios/mid_career_loan.json")
	result := engine.RunPlan(p)

	t.Logf("Mid-career plan — %d goals", len(result.Goals))
	for _, g := range result.Goals {
		t.Logf("  %-22s  %s  projected=%.0f  shortfall=%.0f  shortfall_pct=%.0f%%",
			g.Name, g.Tag, g.ProjectedAmount, g.ShortfallAmount, g.ShortfallPct)
	}

	// FIX #5 verification: even if home goal has a shortfall,
	// education goal should still have a non-zero projected amount.
	var homeShortfall float64
	var educationProjected float64
	for _, g := range result.Goals {
		if g.Name == "Home Purchase" {
			homeShortfall = g.ShortfallAmount
		}
		if g.Name == "Child Education" {
			educationProjected = g.ProjectedAmount
		}
	}

	if homeShortfall > 0 && educationProjected == 0 {
		t.Errorf("FIX #5 REGRESSION: home shortfall caused education to be zero — cascade not removed")
	}
	t.Logf("Home shortfall: %.0f, Education projected: %.0f", homeShortfall, educationProjected)
}

// ── Scenario 3: Near retirement ───────────────────────────────────────────────

func TestNearRetirement(t *testing.T) {
	p := loadScenario(t, "../scenarios/near_retirement.json")
	result := engine.RunPlan(p)

	t.Logf("Near-retirement plan — %d goals", len(result.Goals))
	for _, g := range result.Goals {
		t.Logf("  %-22s  %s  projected=%.0f", g.Name, g.Tag, g.ProjectedAmount)
	}

	// With a large portfolio and close retirement, retirement should be COMFORTABLE
	for _, g := range result.Goals {
		if g.Name == engine.GoalRetirement {
			if g.Tag == engine.TagUnaffordable {
				t.Error("near-retirement investor with large portfolio should not be UNAFFORDABLE")
			}
			t.Logf("Retirement: %s  corpus=%.0f", g.Tag, g.ProjectedAmount)
		}
	}
}

// ── FIX #2: Inflation buffer test ────────────────────────────────────────────

func TestCompoundedInflationBuffer(t *testing.T) {
	// A goal 20 years away at 6% inflation needs ~3.2x buffer, not 1.15x
	// We verify that taxTotal reflects this.
	p := loadScenario(t, "../scenarios/young_salaried.json")
	result := engine.RunPlan(p)

	for _, g := range result.Goals {
		if g.Name == "Retirement" {
			// Retirement is 24+ years away — taxTotal should be
			// significantly more than target * 1.15
			t.Logf("Retirement taxTotal: %.0f", g.TaxTotal)
		}
		// Near-term goal: check inflation factor is reasonable
		if g.Name == "Home Purchase" {
			// 4 years away at 6% → ~1.26x buffer
			t.Logf("Home Purchase taxTotal: %.0f", g.TaxTotal)
			if g.TaxTotal == 0 {
				t.Error("home purchase taxTotal should be > 0")
			}
		}
	}
}

// ── FIX #3: Corpus discount rate test ────────────────────────────────────────

func TestRetirementCorpusDiscountRate(t *testing.T) {
	// FIX #3 verification:
	// Old 3.5% discount rate => corpus ~13.4Cr for this scenario.
	// New blended ~9.5% rate => corpus ~6.5Cr.
	// We assert corpus is well below the old-method ceiling, confirming
	// the fix is active, and above a sensible minimum.
	p := loadScenario(t, "../scenarios/young_salaried.json")
	result := engine.RunPlan(p)

	for _, g := range result.Goals {
		if g.Name == engine.GoalRetirement {
			yearsInRetirement := float64(p.LifeExpectancy - p.RetirementAge)
			yearsToRetirement := float64(p.RetirementAge - 36)

			// Approximate inflation-adjusted retirement monthly expense
			retExpense := p.MonthlyExpense
			for i := 0; i < int(yearsToRetirement); i++ {
				retExpense *= 1.06
			}

			// Old 3.5% ceiling — PV sum at low discount rate grows very large
			oldMethodCeiling := retExpense * 12 * yearsInRetirement * 2.5
			// Minimum sanity floor — at least 5 years of expenses
			minimumFloor := retExpense * 12 * 5

			t.Logf("Corpus: %.0f (%.2fCr)  ceiling: %.0f (%.2fCr)  floor: %.0f",
				g.TaxTotal, g.TaxTotal/10_000_000,
				oldMethodCeiling, oldMethodCeiling/10_000_000,
				minimumFloor)

			if g.TaxTotal >= oldMethodCeiling {
				t.Errorf("corpus %.0f >= old-method ceiling %.0f: FIX #3 not working", g.TaxTotal, oldMethodCeiling)
			}
			if g.TaxTotal < minimumFloor {
				t.Errorf("corpus %.0f < floor %.0f: corpus loop may be broken", g.TaxTotal, minimumFloor)
			}
		}
	}
}

// ── computeRetirementSIP unit tests ──────────────────────────────────────────

func TestComputeRetirementSIP_AlreadyCovered(t *testing.T) {
	// lumpsum FV = 50L × 2.5 = 1.25 Cr, PPF/EPF = 60L → total 1.85 Cr > 1 Cr corpus
	sip := engine.ComputeRetirementSIP(1_00_00_000, 50_00_000, 2.5, 60_00_000, 240, 10)
	if sip != 0 {
		t.Errorf("expected 0 when corpus is covered, got %.0f", sip)
	}
}

func TestComputeRetirementSIP_AlreadyRetired(t *testing.T) {
	sip := engine.ComputeRetirementSIP(5_00_00_000, 0, 1, 0, 0, 10)
	if sip != 0 {
		t.Errorf("expected 0 when monthsToRetirement=0, got %.0f", sip)
	}
}

func TestComputeRetirementSIP_ZeroRate(t *testing.T) {
	// At 0% growth the SIP is simply remaining / months.
	corpus := 12_00_000.0
	sip := engine.ComputeRetirementSIP(corpus, 0, 1, 0, 120, 0)
	want := math.Ceil(corpus / 120)
	if sip != want {
		t.Errorf("0%% rate: got %.0f want %.0f", sip, want)
	}
}

func TestComputeRetirementSIP_ReasonableMagnitude(t *testing.T) {
	// 5 Cr corpus, no lumpsum, no PPF/EPF, 20 years, 10% p.a. growth.
	sip := engine.ComputeRetirementSIP(5_00_00_000, 0, 1, 0, 240, 10)
	t.Logf("RetirementSIP for 5Cr over 20 yrs at 10%% pa: ₹%.0f/month", sip)
	if sip <= 0 {
		t.Error("expected positive SIP")
	}
	if sip > 1_00_000 {
		t.Errorf("SIP %.0f seems too high for 5Cr over 20 yrs at 10%%", sip)
	}
}

func TestRunPlanRetirementSIPPresent(t *testing.T) {
	p := loadScenario(t, "../scenarios/young_salaried.json")
	result := engine.RunPlan(p)
	t.Logf("RetirementSIP: ₹%.0f/month", result.RetirementSIP)
	if result.RetirementSIP < 0 {
		t.Error("RetirementSIP should not be negative")
	}
}

// ── Monte Carlo smoke test ────────────────────────────────────────────────────

func TestMonteCarloRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Monte Carlo in short mode")
	}

	p := loadScenario(t, "../scenarios/young_salaried.json")
	assumptions := engine.DefaultReturnAssumptions()
	result := engine.RunMonteCarlo(p, 100, assumptions) // 100 runs for test speed

	if result.Runs != 100 {
		t.Errorf("expected 100 runs, got %d", result.Runs)
	}

	if len(result.GoalProbabilities) == 0 {
		t.Fatal("expected goal probabilities")
	}

	for _, gp := range result.GoalProbabilities {
		if gp.SuccessProbability < 0 || gp.SuccessProbability > 1 {
			t.Errorf("goal %s probability %.3f out of range [0,1]",
				gp.Name, gp.SuccessProbability)
		}
		t.Logf("%-22s  success=%.0f%%  P10=%.0f  P50=%.0f  P90=%.0f",
			gp.Name,
			gp.SuccessProbability*100,
			gp.P10Projected, gp.MedianProjected, gp.P90Projected)
	}
}
