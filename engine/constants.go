package engine

// Goal name constants — match constants.js
const (
	GoalRetirement   = "Retirement"
	GoalWealthGoal   = "Wealth Goal"
	GoalEmergencyFund = "Emergency Fund"
)

// NumMonths maps payout frequency to number of months per period.
// Mirrors constants.js numMonths.
var NumMonths = map[string]int{
	"Yearly":         12,
	"Half Yearly":    6,
	"Quarterly":      3,
	"Monthly":        1,
	"At Maturity Only": 0,
	"Lumpsum":        0,
}

// Projection defines the long-run expected return for each asset class.
// These are the same values as constants.js projections.
// All rates are % per annum.
var Projections = map[string]float64{
	"equity": 12.0,
	"debt":   7.0,
	"liquid": 3.5,
}

// PPFRate is the assumed annual growth rate for PPF holdings (7%).
const PPFRate = 0.07

// EPFRate is the assumed annual growth rate for EPF holdings (8.65%).
const EPFRate = 0.0865

// DefaultLongevityBuffer is the number of extra years added to
// lifeExpectancy when computing the longevity-buffered corpus.
// Improvement #9: makes longevity risk visible.
const DefaultLongevityBuffer = 5
