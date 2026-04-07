package engine

// ─────────────────────────────────────────────
// Input types
// ─────────────────────────────────────────────

// GrowthParam defines a growth rate value for a date range.
// e.g. { Range: ["2020-01-01","2200-01-01"], Value: 8 }
type GrowthParam struct {
	Range [2]string `json:"range"`
	Value float64   `json:"value"`
}

// AllocationBucket defines the equity/debt/liquid split
// for goals with at least `Years` years remaining.
type AllocationBucket struct {
	Years  int     `json:"years"`
	Equity float64 `json:"equity"`
	Debt   float64 `json:"debt"`
	Liquid float64 `json:"liquid"`
}

// Goal is a single financial goal.
// StartDate / EndDate accept "R" (retirement) or "L" (life expectancy)
// as special values — these are expanded by FormatStartTime before
// being passed into the engine.
type Goal struct {
	ID                int     `json:"id"`
	Name              string  `json:"name"`
	Icon              string  `json:"icon"`
	StartDate         string  `json:"start_date"`
	EndDate           string  `json:"end_date"`
	OriginalStartDate string  `json:"original_start_date"`
	OriginalEndDate   string  `json:"original_end_date"`
	TargetAmount      float64 `json:"target_amount"`
	IsLoan            bool    `json:"is_loan"`
	DownPayment       float64 `json:"down_payment"`   // percentage 0-100
	LoanTenure        int     `json:"loan_tenure"`    // years
	LoanInterestRate  float64 `json:"loan_interest_rate"`
	Priority          int     `json:"priority"` // 1 = highest (new field, used in two-pass)
}

// Income is a recurring extra income stream (rental, freelance, etc.)
type Income struct {
	ID              int     `json:"id"`
	Name            string  `json:"name"`
	Amount          float64 `json:"amount"`
	PayoutFrequency string  `json:"payout_frequency"` // "Monthly","Quarterly","Half Yearly","Yearly","Lumpsum"
	GrowthRate      float64 `json:"growth_rate"`
	StartDate       string  `json:"start_date"` // accepts "R" / "L"
	EndDate         string  `json:"end_date"`
	OriginalStartDate string `json:"original_start_date"`
	OriginalEndDate   string `json:"original_end_date"`
}

// Loan is a standalone loan with known EMI.
type Loan struct {
	StartDate string  `json:"start_date"`
	EndDate   string  `json:"end_date"`
	Amount    float64 `json:"amount"` // monthly EMI
}

// SIP is an existing systematic investment plan.
type SIP struct {
	ID               int     `json:"id"`
	Name             string  `json:"name"`
	Amount           float64 `json:"amount"`
	EquityPercentage float64 `json:"equity_percentage"` // 0–1
	DebtPercentage   float64 `json:"debt_percentage"`
	LiquidPercentage float64 `json:"liquid_percentage"`
}

// Interest is a single interest payout from a fixed-income instrument.
type Interest struct {
	Date   string  `json:"date"`
	Amount float64 `json:"amount"`
}

// PortfolioParam is a maturing fixed-income instrument (FD, bond).
type PortfolioParam struct {
	ID             int        `json:"id"`
	AssetType      string     `json:"asset_type"`
	MaturityDate   string     `json:"maturity_date"`
	MaturityAmount float64    `json:"maturity_amount"`
	IsBreakable    bool       `json:"is_breakable"`
	Interests      []Interest `json:"interests"`
}

// PortfolioBreakup is the current portfolio split by asset class.
type PortfolioBreakup struct {
	Equity struct{ Amount float64 } `json:"Equity"`
	Debt   struct{ Amount float64 } `json:"Debt"`
	Liquid struct{ Amount float64 } `json:"Liquid"`
}

// RetirementCashflow is a long-term retirement asset (PPF, EPF, or NPS)
// that is modelled separately from the liquid portfolio because it matures
// at or after retirement and has special payout rules.
type RetirementCashflow struct {
	ID                  int     `json:"id"`
	AssetType           string  `json:"asset_type"`           // "PPF", "EPF", or "NPS"
	MarketValue         float64 `json:"market_value"`         // current corpus / balance
	EPFLastContribution float64 `json:"epf_last_contribution"` // annual EMployee+employer, EPF only
	NPSLastContribution float64 `json:"nps_last_contribution"` // annual employee+employer, NPS only
}

// PlanParams is the complete input to the financial engine.
// Mirrors the fixFinancials() call signature in financial-timeline.js.
type PlanParams struct {
	DOB                       string             `json:"dob"`
	RetirementAge             int                `json:"retirementAge"`
	LifeExpectancy            int                `json:"lifeExpectancy"`
	MonthlyIncome             float64            `json:"monthlyIncome"`
	MonthlyExpense            float64            `json:"monthlyExpense"`
	IncomeParams              []GrowthParam      `json:"incomeParams"`
	ExpenseParams             []GrowthParam      `json:"expenseParams"`
	Incomes                   []Income           `json:"incomes"`
	Loans                     []Loan             `json:"loans"`
	SIPs                      []SIP              `json:"sips"`
	Goals                     []Goal             `json:"goals"`
	PortfolioParams           []PortfolioParam   `json:"portfolioParams"`
	AssetAllocation           []AllocationBucket `json:"assetAllocation"`
	RetirementAssetAllocation []AllocationBucket `json:"retirementAssetAllocation"`
	CurrentPortfolioValue     float64            `json:"currentPortfolioValue"`
	PortfolioBreakup          PortfolioBreakup   `json:"portfolioBreakup"`
	RetirementBasedCashflows  []RetirementCashflow `json:"retirementBasedCashflows"`
}

// ─────────────────────────────────────────────
// Output types
// ─────────────────────────────────────────────

// GoalTag is the funding status of a goal.
type GoalTag string

const (
	TagComfortable  GoalTag = "COMFORTABLE"
	TagManageable   GoalTag = "MANAGEABLE"
	TagAtRisk       GoalTag = "AT_RISK"
	TagUnaffordable GoalTag = "UNAFFORDABLE"
)

// LumpsumAttachment is the lumpsum contribution to a goal.
type LumpsumAttachment struct {
	Amount   float64 `json:"amount"`
	Expected float64 `json:"expected"`
}

// SavingsAttachment is the savings contribution to a goal.
type SavingsAttachment struct {
	Amount   float64 `json:"amount"`
	Expected float64 `json:"expected"`
	Start    string  `json:"start"`
	End      string  `json:"end"`
}

// FutureIncomeAttachment is a windfall or extra income contribution.
type FutureIncomeAttachment struct {
	Name           string  `json:"name"`
	StartDate      string  `json:"start_date"`
	Amount         float64 `json:"amount"`
	ExpectedAmount float64 `json:"expected_amount"`
}

// GoalAttachments describes how a goal was funded.
type GoalAttachments struct {
	Lumpsum            LumpsumAttachment        `json:"lumpsum"`
	Savings            SavingsAttachment        `json:"savings"`
	FutureIncomes      []FutureIncomeAttachment `json:"futureIncomes"`
	MaturedInvestments []FutureIncomeAttachment `json:"maturedInvestments"`
}

// GoalResult is the engine output for a single goal.
type GoalResult struct {
	ID              int             `json:"id"`
	Name            string          `json:"name"`
	Icon            string          `json:"icon"`
	StartDate       string          `json:"start_date"`
	Tag             GoalTag         `json:"tag"`
	TaxTotal        float64         `json:"taxTotal"`        // inflation-adjusted target
	OverallTotal    float64         `json:"overallTotal"`    // including loan amount
	ProjectedAmount float64         `json:"projected_amount"`
	ShortfallAmount float64         `json:"shortfall_amount"`
	ShortfallPct    float64         `json:"shortfall_pct"`
	Attachments     GoalAttachments `json:"attachments"`
}

// AllocationSplit is an equity/debt/liquid percentage split.
type AllocationSplit struct {
	Equity float64 `json:"equity"`
	Debt   float64 `json:"debt"`
	Liquid float64 `json:"liquid"`
	Total  float64 `json:"total,omitempty"`
}

// SIPSchedule is the recommended SIP for a given year.
// FIX #4: we now return a schedule rather than a single value.
type SIPSchedule struct {
	Year   int     `json:"year"`
	Amount float64 `json:"amount"`
}

// PlanResult is the complete output of the financial engine.
type PlanResult struct {
	Goals                  []GoalResult    `json:"goals"`
	IdealAssetAllocation   AllocationSplit `json:"idealAssetAllocation"`
	CurrentAssetAllocation AllocationSplit `json:"currentAssetAllocation"`
	RecommendedSIPSchedule []SIPSchedule  `json:"recommendedSipSchedule"`
	RetirementExpense      float64         `json:"retirementExpense"`
	RetirementSIP          float64         `json:"retirementSip"` // minimum monthly SIP to fund the retirement corpus
	IsFlatLine             bool            `json:"isFlatLine"`   // true only if portfolio genuinely hits zero
	FlatLineDate           string          `json:"flatLineDate"` // date portfolio was exhausted
}

// ─────────────────────────────────────────────
// Monte Carlo types
// ─────────────────────────────────────────────

// GoalProbability reports the success probability of a goal.
type GoalProbability struct {
	ID                  int     `json:"id"`
	Name                string  `json:"name"`
	SuccessProbability  float64 `json:"successProbability"`  // 0–1
	MedianProjected     float64 `json:"medianProjected"`
	P10Projected        float64 `json:"p10Projected"` // pessimistic
	P90Projected        float64 `json:"p90Projected"` // optimistic
	DeterministicTag    GoalTag `json:"deterministicTag"`
}

// MonteCarloResult wraps the deterministic plan with probability bands.
type MonteCarloResult struct {
	Deterministic     PlanResult        `json:"deterministic"`
	GoalProbabilities []GoalProbability `json:"goalProbabilities"`
	Runs              int               `json:"runs"`
}
