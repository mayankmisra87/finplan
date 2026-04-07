# finplan — Financial Planning Engine (Go POC)

A Go port of the financial planning engine with four Tier 1 fixes applied
and two new modules: two-pass lumpsum allocation and Monte Carlo simulation.

## What changed from the JS version

| # | Fix | File |
|---|-----|------|
| FIX #1 | Date mutation bug — Go `time.Time` is a value type, lumpsum always uses today's FV | `solution.go` |
| FIX #2 | Flat 15% inflation buffer → compounded inflation over years-to-goal | `solution.go` |
| FIX #3 | Retirement corpus discounted at blended allocation rate, not hard-coded 3.5% | `solution.go` |
| FIX #4 | Recommended SIP recalculated annually, returns a full year schedule | `solution.go` |
| FIX #5 | Soft flatline — goal shortfalls no longer cascade; portfolio exhaustion still flags | `solution.go` |
| NEW | Two-pass lumpsum: savings-only pass first, lumpsum fills residual gaps | `two_pass.go` |
| NEW | Monte Carlo: 500+ runs with perturbed returns, probability bands per goal | `monte_carlo.go` |
| NEW | EPF `isBreakable=false` now enforced — EPF cannot fund pre-retirement goals | `solution.go` |

## Quick start

```bash
# Clone this repo and run tests
go test ./engine/... -v

# Run a plan
go run ./cmd -scenario scenarios/young_salaried.json -mode plan

# Run Monte Carlo (500 simulations)
go run ./cmd -scenario scenarios/young_salaried.json -mode monte-carlo -runs 500

# Compare lumpsum strategies
go run ./cmd -scenario scenarios/young_salaried.json -mode compare
```

## Working with Claude Code

Once you push this to GitHub and open it in Claude Code, here are the
prompts to start with:

```
# Verify the port is correct against the JS version
"Run the young_salaried scenario through both the JS engine and Go engine
 with identical inputs and compare the outputs"

# Test a specific fix
"Show me the difference in retirement corpus between FIX #3 applied vs
 the old 3.5% discount rate for the near_retirement scenario"

# Implement the retirement SIP ring-fence
"Add a function computeRetirementSIP() that calculates the minimum monthly
 SIP needed to fund the retirement corpus, given the ring-fenced PPF/EPF
 cashflows. Wire it into RunPlan() and add it to PlanResult"

# Improve the two-pass lumpsum
"The estimateSavingsCoverage function in two_pass.go is a heuristic.
 Replace it with an exact simulation that mirrors FinancialSolution's
 savings loop but skips the lumpsum allocation step"

# Add the tax layer
"Add a TaxProfile struct (IncomeTaxSlab float64, EquityLTCGUsed float64)
 to PlanParams. In FinancialSolution, apply a post-tax haircut to each
 redemption: equity LTCG at 12.5% above ₹1.25L threshold, debt at slab rate"

# Add longevity buffer
"Add a LongevityBuffer int field to PlanParams (default 5 years).
 Run the retirement corpus calculation twice — once to lifeExpectancy,
 once to lifeExpectancy+LongevityBuffer — and include both in PlanResult"

# Stress test the Monte Carlo
"Run the near_retirement scenario with 1000 Monte Carlo runs and report
 how the retirement success probability changes when equity sigma is
 increased from 8% to 15% (sequence-of-returns stress)"
```

## Project structure

```
finplan/
├── engine/
│   ├── types.go           All input/output structs
│   ├── constants.go       Return assumptions, goal name constants
│   ├── helpers.go         Date arithmetic, math helpers, growth map builders
│   ├── allocation.go      Year-by-year asset allocation + FV calculation
│   ├── solution.go        Core goal-funding waterfall (port of fix-financials.js)
│   ├── two_pass.go        Two-pass lumpsum optimisation (new)
│   ├── timeline.go        Top-level orchestrator (port of fixFinancials() in financial-timeline.js)
│   ├── monte_carlo.go     Monte Carlo runner using goroutines
│   └── solution_test.go   Scenario-based tests
├── cmd/
│   └── main.go            CLI: plan / monte-carlo / compare modes
└── scenarios/
    ├── young_salaried.json    Age 36, ₹1.5L income, ₹20L portfolio
    ├── mid_career_loan.json   Age 41, home + education goals, EPF/PPF
    └── near_retirement.json   Age 56, ₹1.5Cr portfolio, 2 years to retirement
```

## Known limitations (next steps)

1. The `perturbParams` function in `monte_carlo.go` adjusts allocation buckets
   but the blended growth rate calculation in `allocation.go` still uses the
   global `Projections` map. Full MC fidelity requires injecting per-run
   return assumptions into `blendedGrowthRate()` — marked with TODO.

2. `estimateSavingsCoverage` in `two_pass.go` is a heuristic approximation.
   For production, replace with an exact simulation pass.

3. Tax on redemptions is not yet modelled. See `TaxProfile` suggestion above.

4. NPS is not yet treated as a retirement cashflow (currently excluded).
   Add it alongside EPF/PPF in `timeline.go`.
