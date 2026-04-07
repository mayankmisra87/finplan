package engine

import (
	"math"
	"sort"
	"time"
)

// ─────────────────────────────────────────────
// Date helpers
// ─────────────────────────────────────────────

const dateFmt = "2006-01-02"

// parseDate parses a YYYY-MM-DD string into a time.Time (UTC, start of day).
func parseDate(s string) time.Time {
	t, err := time.Parse(dateFmt, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// formatDate formats a time.Time as YYYY-MM-DD.
func formatDate(t time.Time) string {
	return t.Format(dateFmt)
}

// firstOfMonth returns the first day of the month for a given time.
func firstOfMonth(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// todayFirstOfMonth returns today's date snapped to the first of the month.
func todayFirstOfMonth() time.Time {
	return firstOfMonth(time.Now().UTC())
}

// addMonths adds n months to t (first-of-month arithmetic).
func addMonths(t time.Time, n int) time.Time {
	return t.AddDate(0, n, 0)
}

// FormatStartTime expands "R" and "L" date tokens into real dates.
// Mirrors constants.js formatStartTime.
// It modifies the goal's StartDate and EndDate in place.
func FormatStartTime(g *Goal, dob string, retirementAge, lifeExpectancy int) {
	dobDate := parseDate(dob)

	resolve := func(token string) string {
		switch token {
		case "R":
			d := dobDate.AddDate(retirementAge, 0, 0)
			return formatDate(d)
		case "L":
			d := dobDate.AddDate(lifeExpectancy, 0, 0)
			return formatDate(d)
		default:
			return token
		}
	}

	g.StartDate = resolve(g.OriginalStartDate)
	g.EndDate = resolve(g.OriginalEndDate)
}

// FormatIncomeStartTime is the same as FormatStartTime but for Income structs.
func FormatIncomeStartTime(i *Income, dob string, retirementAge, lifeExpectancy int) {
	dobDate := parseDate(dob)
	resolve := func(token string) string {
		switch token {
		case "R":
			return formatDate(dobDate.AddDate(retirementAge, 0, 0))
		case "L":
			return formatDate(dobDate.AddDate(lifeExpectancy, 0, 0))
		default:
			return token
		}
	}
	i.StartDate = resolve(i.OriginalStartDate)
	i.EndDate = resolve(i.OriginalEndDate)
}

// ─────────────────────────────────────────────
// Financial math helpers
// ─────────────────────────────────────────────

// fv computes the future value of a series of payments.
// Mirrors the FV() function used throughout the JS codebase.
//   rate: periodic interest rate (e.g. 0.10 for 10%)
//   nper: number of periods
//   pmt:  payment per period
//   pv:   present value
func fv(rate, nper, pmt, pv float64) float64 {
	pow := math.Pow(1+rate, nper)
	var result float64
	if rate != 0 {
		result = (pmt*(1+rate)*(1-pow)/rate) - pv*pow
	} else {
		result = -(pv + pmt*nper)
	}
	return -result
}

// loanEMI computes the fixed monthly EMI for a loan.
// amount: principal, tenure: years, interest: annual % rate
func loanEMI(amount float64, tenure int, interest float64) float64 {
	if interest == 0 {
		return amount / float64(tenure*12)
	}
	r := interest / 1200
	n := float64(tenure * 12)
	pvif := math.Pow(1+r, n)
	return r * amount * pvif / (pvif - 1)
}

// roundAmount rounds to nearest 1000 for large amounts, 100 for small.
func roundAmount(v float64) float64 {
	if v > 10000 {
		return math.Round(v/1000) * 1000
	}
	return math.Round(v/100) * 100
}

// blendedGrowthRate returns the weighted blended growth rate for an allocation.
func blendedGrowthRate(equity, debt, liquid float64) float64 {
	return equity*Projections["equity"] +
		debt*Projections["debt"] +
		liquid*Projections["liquid"]
}

// ─────────────────────────────────────────────
// Growth map builders
// ─────────────────────────────────────────────

// buildGrowthMap converts []GrowthParam into a year→rate map
// covering startYear to endYear (exclusive).
func buildGrowthMap(params []GrowthParam, startYear, endYear int) map[int]float64 {
	m := make(map[int]float64, endYear-startYear)
	i := 0
	cg := params[0]
	for y := startYear; y < endYear; y++ {
		rangeEnd := parseDate(cg.Range[1]).Year()
		if y > rangeEnd && i+1 < len(params) {
			i++
			cg = params[i]
		}
		m[y] = cg.Value
	}
	return m
}

// buildRetirementExpense computes the monthly expense at retirement
// by applying expense growth from today to the retirement date.
func buildRetirementExpense(monthlyExpense float64, expenseParams []GrowthParam, currentYear, retirementYear int) float64 {
	expense := monthlyExpense
	i := 0
	cg := expenseParams[0]
	for y := currentYear; y < retirementYear; y++ {
		rangeEnd := parseDate(cg.Range[1]).Year()
		if y > rangeEnd && i+1 < len(expenseParams) {
			i++
			cg = expenseParams[i]
		}
		expense *= (1 + cg.Value/100)
	}
	return expense
}

// buildLoanEMIMap expands a list of loans into a date→totalEMI map.
func buildLoanEMIMap(loans []Loan) map[string]float64 {
	m := make(map[string]float64)
	for _, l := range loans {
		sd := firstOfMonth(parseDate(l.StartDate))
		ed := firstOfMonth(parseDate(l.EndDate))
		for !sd.Before(ed) == false {
			k := formatDate(sd)
			m[k] += l.Amount
			sd = addMonths(sd, 1)
		}
	}
	return m
}

// ─────────────────────────────────────────────
// Sorting helpers
// ─────────────────────────────────────────────

// sortGoals sorts goals for the waterfall:
// primary: start_date ascending
// secondary: target_amount ascending
// tertiary: priority ascending (lower number = higher priority)
func sortGoals(goals []Goal) {
	sort.Slice(goals, func(i, j int) bool {
		if goals[i].StartDate != goals[j].StartDate {
			return goals[i].StartDate < goals[j].StartDate
		}
		if goals[i].Priority != goals[j].Priority {
			p1, p2 := goals[i].Priority, goals[j].Priority
			if p1 == 0 { p1 = 99 }
			if p2 == 0 { p2 = 99 }
			return p1 < p2
		}
		return goals[i].TargetAmount < goals[j].TargetAmount
	})
}

// effectiveGoalTarget returns the amount that actually needs to be
// sourced (loan goals only need the down payment portion).
func effectiveGoalTarget(g Goal) float64 {
	if g.IsLoan {
		return g.DownPayment * g.TargetAmount / 100
	}
	return g.TargetAmount
}

// loanPrincipal returns the loan principal for a loan-backed goal.
func loanPrincipal(g Goal) float64 {
	if g.IsLoan {
		return (100 - g.DownPayment) * g.TargetAmount / 100
	}
	return 0
}

// yearsUntil returns the number of years from today until goalDate.
func yearsUntil(goalDate time.Time) float64 {
	today := todayFirstOfMonth()
	return goalDate.Sub(today).Hours() / 24 / 365.25
}

// computeRetirementSIP returns the minimum monthly SIP required to fund
// the retirement corpus, net of two already-committed sources:
//
//  1. The portfolio lumpsum ring-fenced for retirement (grown by lumpsumFV
//     to its future value at retirement).
//  2. The combined PPF + EPF maturity value (principal + accrued interest)
//     at the retirement date.
//
// It uses the standard annuity-due formula solved for the periodic payment:
//
//	FV = PMT × (1+r) × [(1+r)^n − 1] / r
//	PMT = FV × r / [(1+r) × ((1+r)^n − 1)]
//
// where r = annualGrowthRate/1200 (monthly rate) and n = monthsToRetirement.
// Returns 0 if the corpus is already fully covered or if retirement is now.
func ComputeRetirementSIP(
	retirementCorpus float64,
	retirementLumpsum float64,
	lumpsumFV float64,
	ppfEPFAtRetirement float64,
	monthsToRetirement int,
	annualGrowthRate float64,
) float64 {
	if monthsToRetirement <= 0 {
		return 0
	}
	remaining := retirementCorpus - retirementLumpsum*lumpsumFV - ppfEPFAtRetirement
	if remaining <= 0 {
		return 0
	}
	r := annualGrowthRate / 1200
	n := float64(monthsToRetirement)
	if r == 0 {
		return math.Ceil(remaining / n)
	}
	p := math.Pow(1+r, n)
	// annuity-due: PMT = FV × r / [(1+r) × (p − 1)]
	sip := remaining * r / ((1 + r) * (p - 1))
	if sip < 0 {
		return 0
	}
	return math.Ceil(sip)
}
