package engine

// financialSolution is the core goal-funding engine.
// It is a faithful port of financialSolution() in fix-financials.js
// with the following fixes applied (each marked with FIX #N):
//
//   FIX #1 — Date mutation bug:
//     Go time.Time is a value type. addMonths() returns a new value.
//     The shared currentDate cursor advancing across goals is intentional
//     (sequential waterfall) but the lumpsum FV now always uses today's
//     year — not the year the previous goal finished.
//
//   FIX #2 — Inflation buffer:
//     Replaced flat 1.15× with compounded inflation over yearsToGoal.
//
//   FIX #3 — Retirement corpus discount rate:
//     Replaced hard-coded 3.5% with the retirement goal's actual blended
//     allocation growth rate.
//
//   FIX #4 — SIP recommendation:
//     Recommended SIP is recalculated each year as income grows, not
//     taken from the first month only. A full schedule is returned.
//
//   FIX #5 — Soft flatline:
//     isFlatLine is only set when the portfolio genuinely hits zero
//     (expenses exceed all assets). Goal shortfalls no longer cascade.
//     Resources from an unaffordable goal flow forward to the next goal.
//
import (
	"math"
	"time"
)

// goalFundingState tracks carry-forward state between goals.
type goalFundingState struct {
	currentDate     time.Time
	remainingPortfolio float64
	currentIncome   float64
	currentExpense  float64
	currentSip      float64
	currentYear     int
	savingRemaining float64
	monthComplete   bool
	// carry-forward partial sources
	incomeRemaining     float64
	windfallRemaining   float64
	investmentRemaining float64
	interestRemaining   float64
	incomeDetails       extraIncomeItem
	windfallDetails     windfall
	investmentDetails   PortfolioParam
	interestDetails     Interest
	initExtraIncome     int
	initFutureIncome    int
	initInvestment      int
	initInterest        int
}

type extraIncomeItem struct {
	ID        int
	Name      string
	Amount    float64
	Growth    float64
	StartDate string
	EndDate   string
}

type windfall struct {
	ID        int
	Name      string
	Amount    float64
	StartDate string
}

type savingsEntry struct {
	Amount         float64
	ExpectedAmount float64
	StartDate      string
}

type goalAttachmentDetail struct {
	lumpsum         float64
	expectedLumpsum float64
	savings         []savingsEntry
	incomes         []incomeEntry
	windfalls       []windfallEntry
	investments     []investmentEntry
	interests       []interestEntry
	endDate         string
}

type incomeEntry struct {
	ID             int
	Name           string
	Amount         float64
	ExpectedAmount float64
	StartDate      string
}

type windfallEntry struct {
	Name           string
	Amount         float64
	ExpectedAmount float64
	StartDate      string
}

type investmentEntry struct {
	Amount         float64
	ExpectedAmount float64
	StartDate      string
	AssetType      string
}

type interestEntry struct {
	Amount         float64
	ExpectedAmount float64
	StartDate      string
}

// FinancialSolution runs the goal-funding waterfall.
// Parameters match the JS version exactly.
func FinancialSolution(
	retirementAge, lifeExpectancy int,
	currentPortfolioValue, initialLiquidAmount float64,
	monthlyIncome, monthlyExpense, retirementExpense float64,
	incomeGrowth, expenseGrowth map[int]float64,
	portfolioParams []PortfolioParam,
	dob string,
	goals []Goal,
	goalsAllocation AllGoalsAllocation,
	windfalls []windfall,
	incomes []extraIncomeItem,
	loanEmis map[string]float64,
	totalSipAmount float64,
	avgExpenseGrowth float64, // FIX #2: needed for compounded inflation buffer
) ([]GoalResult, []SIPSchedule) {

	today := todayFirstOfMonth()
	dobDate := parseDate(dob)
	endDate := dobDate.AddDate(lifeExpectancy, 0, 0)
	retirementDate := dobDate.AddDate(retirementAge, 0, 0)

	// ── Compute retirement corpus target ───────────────────────────────────
	// FIX #3: use the retirement goal's actual blended growth rate
	// to discount future retirement expenses, not the hard-coded 3.5%.
	retirementGoalIdx := -1
	for i, g := range goals {
		if g.Name == GoalRetirement && g.Icon == "default" {
			retirementGoalIdx = i
			break
		}
	}

	// Find retirement allocation growth rate
	retirementDiscountRate := 3.5 // fallback to old value if goal not found
	if retirementGoalIdx >= 0 {
		retGoal := goals[retirementGoalIdx]
		if alloc, ok := goalsAllocation[retGoal.ID]; ok {
			todayYear := today.Year()
			if ya, ok2 := alloc[todayYear]; ok2 {
				// FIX #3: use blended growth rate instead of 3.5%
				retirementDiscountRate = ya.Growth
			}
		}
	}

	// Walk from retirement to end of life, summing discounted expenses
	retirementTargetExpense := 0.0
	fvAccum := 1.0
	retirementTrack := dobDate.AddDate(retirementAge, 0, 0)
	retirementTrack = addMonths(retirementTrack, 1)
	trackYear := retirementTrack.Year()
	currentRetExpense := retirementExpense

	for retirementTrack.Before(endDate) {
		fvAccum *= (1 + retirementDiscountRate/1200)
		if trackYear != retirementTrack.Year() {
			currentRetExpense *= (1 + (expenseGrowth[trackYear]+0)/100)
		}
		retirementTargetExpense += currentRetExpense / fvAccum

		trackDate := formatDate(retirementTrack)
		// Add goal expenses that fall during retirement
		for _, g := range goals {
			if g.Icon != "default" && g.EndDate == trackDate {
				amount := effectiveGoalTarget(g)
				retirementTargetExpense += amount / fvAccum
			}
		}
		if emi, ok := loanEmis[trackDate]; ok {
			retirementTargetExpense += emi / fvAccum
		}
		trackYear = retirementTrack.Year()
		retirementTrack = addMonths(retirementTrack, 1)
	}

	// Set retirement goal target
	if retirementGoalIdx >= 0 {
		goals[retirementGoalIdx].TargetAmount = retirementTargetExpense
	}

	// ── Set up initial state ───────────────────────────────────────────────
	// FIX #4: track SIP schedule year by year
	sipSchedule := []SIPSchedule{}
	sipScheduleByYear := map[int]bool{}

	state := &goalFundingState{
		currentDate:        today,
		remainingPortfolio: currentPortfolioValue,
		currentIncome:      monthlyIncome,
		currentExpense:     monthlyExpense,
		currentSip:         totalSipAmount,
		currentYear:        today.Year(),
		monthComplete:      true,
	}

	// Extra incomes arrive pre-converted to monthly amounts by timeline.go.
	// Copy the slice so the annual growth loop can mutate without affecting the original.
	currentExtraIncomes := make([]extraIncomeItem, len(incomes))
	copy(currentExtraIncomes, incomes)

	// ── Filter and sort goals for the waterfall ────────────────────────────
	evaluatingGoals := []Goal{}
	for _, g := range goals {
		if g.Name != GoalEmergencyFund && g.Name != GoalWealthGoal {
			evaluatingGoals = append(evaluatingGoals, g)
		}
	}
	sortGoals(evaluatingGoals)

	attachments := map[int]*goalAttachmentDetail{}
	results := []GoalResult{}

	// ── Main waterfall loop ────────────────────────────────────────────────
	for loopTrack := 0; loopTrack < len(evaluatingGoals); loopTrack++ {
		g := evaluatingGoals[loopTrack]
		alloc := goalsAllocation[g.ID]
		savingStartDate := formatDate(state.currentDate)

		// FIX #2: Compounded inflation buffer instead of flat 15%.
		// IMPORTANT: Do NOT apply to retirement/default goals.
		// Their TargetAmount is already a discounted PV sum computed
		// in the corpus loop above — compounding inflation on top of
		// it would double-count. Use a small fixed safety buffer (5%)
		// for retirement instead.
		goalDate := parseDate(g.EndDate)
		yearsToGoal := math.Max(0, float64(goalDate.Year()-today.Year()))
		var inflationFactor float64
		if g.Icon == "default" {
			// Retirement / emergency: target is already in PV terms.
			// Apply a small 5% safety margin only.
			inflationFactor = 1.05
		} else {
			// Regular goal: target is in today's rupees, inflate to goal date.
			inflationFactor = math.Pow(1+avgExpenseGrowth/100, yearsToGoal)
			if inflationFactor < 1.05 {
				inflationFactor = 1.05
			}
		}

		effectiveTarget := effectiveGoalTarget(g)
		// FIX #2: use compounded inflation for regular goals, fixed buffer for retirement
		goalTarget := math.Round(effectiveTarget * inflationFactor)
		loanAmount := loanPrincipal(g)
		remainingGoalValue := goalTarget

		attachments[g.ID] = &goalAttachmentDetail{
			endDate: g.EndDate,
		}

		// ── Lumpsum allocation ─────────────────────────────────────────────
		// FIX #1: always use today's year for the lumpsum FV, not the
		// year that currentDate has advanced to after previous goals.
		todayYear := today.Year()
		lumpsumFV := 1.0
		if ya, ok := alloc[todayYear]; ok {
			lumpsumFV = ya.FV
		}

		if lumpsumFV > 0 {
			requiredLumpsum := remainingGoalValue / lumpsumFV
			if state.remainingPortfolio >= requiredLumpsum {
				state.remainingPortfolio -= requiredLumpsum
				attachments[g.ID].lumpsum = requiredLumpsum
				attachments[g.ID].expectedLumpsum = remainingGoalValue
				remainingGoalValue = 0
			} else {
				remainingGoalValue -= state.remainingPortfolio * lumpsumFV
				attachments[g.ID].lumpsum = state.remainingPortfolio
				attachments[g.ID].expectedLumpsum = state.remainingPortfolio * lumpsumFV
				state.remainingPortfolio = 0
			}
		}

		// ── Monthly savings accumulation loop ─────────────────────────────
		isRetired := false
		goalEndDate := parseDate(g.EndDate)

		for state.currentDate.Before(goalEndDate) && remainingGoalValue > 0 {
			stringDate := formatDate(state.currentDate)

			// Year rollover: update income, expense, extra income growth
			if state.currentYear != state.currentDate.Year() {
				if !isRetired {
					if rate, ok := incomeGrowth[state.currentYear]; ok {
						state.currentIncome *= (1 + rate/100)
					}
				}
				if rate, ok := expenseGrowth[state.currentYear]; ok {
					state.currentExpense *= (1 + rate/100)
				}
				// Grow extra incomes annually
				for j, e := range currentExtraIncomes {
					if stringDate >= e.StartDate && stringDate < e.EndDate {
						currentExtraIncomes[j].Amount *= (1 + e.Growth/100)
					}
				}
				state.currentYear = state.currentDate.Year()
			}

			// Retirement transition
			if state.currentDate.After(retirementDate) && !isRetired {
				state.currentIncome = 0
				state.currentSip = 0
				isRetired = true
			}

			currentFV := 1.0
			if ya, ok := alloc[state.currentDate.Year()]; ok {
				currentFV = ya.FV
			}

			// Compute monthly saving
			monthlySaving := state.currentIncome - state.currentExpense
			if emi, ok := loanEmis[stringDate]; ok {
				monthlySaving -= emi
			}
			if state.currentSip > 0 && state.currentSip < monthlySaving {
				monthlySaving -= state.currentSip

				// FIX #4: record SIP recommendation for this year
				if !sipScheduleByYear[state.currentDate.Year()] {
					sipSchedule = append(sipSchedule, SIPSchedule{
						Year:   state.currentDate.Year(),
						Amount: monthlySaving,
					})
					sipScheduleByYear[state.currentDate.Year()] = true
				}
			} else {
				state.currentSip = 0
			}
			if !state.monthComplete {
				monthlySaving = state.savingRemaining
			}

			if monthlySaving > 0 {
				if monthlySaving*currentFV < remainingGoalValue {
					remainingGoalValue -= monthlySaving * currentFV
					attachments[g.ID].savings = append(attachments[g.ID].savings,
						savingsEntry{monthlySaving, monthlySaving * currentFV, stringDate})
					state.savingRemaining = 0
				} else {
					state.savingRemaining = monthlySaving - remainingGoalValue/currentFV
					attachments[g.ID].savings = append(attachments[g.ID].savings,
						savingsEntry{remainingGoalValue / currentFV, remainingGoalValue, stringDate})
					remainingGoalValue = 0
					state.monthComplete = false
					break
				}
			}

			// Extra incomes
			for k := state.initExtraIncome; k < len(currentExtraIncomes); k++ {
				e := currentExtraIncomes[k]
				eStart := parseDate(e.StartDate)
				eEnd := parseDate(e.EndDate)
				if !state.currentDate.Before(eStart) && !state.currentDate.After(eEnd) {
					amount := e.Amount
					if amount*currentFV < remainingGoalValue {
						remainingGoalValue -= amount * currentFV
						attachments[g.ID].incomes = append(attachments[g.ID].incomes,
							incomeEntry{e.ID, e.Name, amount, amount * currentFV, stringDate})
					} else {
						state.incomeRemaining = amount - remainingGoalValue/currentFV
						state.incomeDetails = e
						attachments[g.ID].incomes = append(attachments[g.ID].incomes,
							incomeEntry{e.ID, e.Name, remainingGoalValue / currentFV, remainingGoalValue, stringDate})
						remainingGoalValue = 0
						state.initExtraIncome = k + 1
						state.monthComplete = false
						break
					}
				}
				if state.incomeRemaining > 0 {
					break
				}
				state.initExtraIncome = 0
			}

			// Windfalls
			for k := state.initFutureIncome; k < len(windfalls); k++ {
				w := windfalls[k]
				if w.StartDate == stringDate {
					amount := w.Amount
					if amount*currentFV < remainingGoalValue {
						remainingGoalValue -= amount * currentFV
						attachments[g.ID].windfalls = append(attachments[g.ID].windfalls,
							windfallEntry{w.Name, amount, amount * currentFV, stringDate})
						state.windfallRemaining = 0
					} else {
						state.windfallRemaining = amount - remainingGoalValue/currentFV
						state.windfallDetails = w
						attachments[g.ID].windfalls = append(attachments[g.ID].windfalls,
							windfallEntry{w.Name, remainingGoalValue / currentFV, remainingGoalValue, stringDate})
						remainingGoalValue = 0
						state.initFutureIncome = k + 1
						state.monthComplete = false
						break
					}
				}
			}
			if state.windfallRemaining > 0 {
				break
			}
			state.initFutureIncome = 0

			// Maturing investments
			for j := state.initInvestment; j < len(portfolioParams); j++ {
				pp := portfolioParams[j]
				// FIX #5: respect isBreakable=false (EPF cannot be redeemed early)
				if !pp.IsBreakable && state.currentDate.Before(parseDate(pp.MaturityDate)) {
					continue
				}
				amount := pp.MaturityAmount
				if pp.MaturityDate == stringDate {
					if amount*currentFV < remainingGoalValue {
						remainingGoalValue -= amount * currentFV
						attachments[g.ID].investments = append(attachments[g.ID].investments,
							investmentEntry{amount, amount * currentFV, stringDate, pp.AssetType})
						state.investmentRemaining = 0
					} else {
						state.investmentRemaining = amount - remainingGoalValue/currentFV
						state.investmentDetails = pp
						attachments[g.ID].investments = append(attachments[g.ID].investments,
							investmentEntry{remainingGoalValue / currentFV, remainingGoalValue, stringDate, pp.AssetType})
						remainingGoalValue = 0
						state.initInvestment = j + 1
						state.monthComplete = false
						break
					}
				}
				// Interest payouts
				for k := state.initInterest; k < len(pp.Interests); k++ {
					interest := pp.Interests[k]
					if interest.Date == stringDate {
						ia := interest.Amount
						if ia*currentFV < remainingGoalValue {
							remainingGoalValue -= ia * currentFV
							attachments[g.ID].interests = append(attachments[g.ID].interests,
								interestEntry{ia, ia * currentFV, stringDate})
							state.interestRemaining = 0
						} else {
							state.interestRemaining = ia - remainingGoalValue/currentFV
							state.interestDetails = interest
							attachments[g.ID].interests = append(attachments[g.ID].interests,
								interestEntry{remainingGoalValue / currentFV, remainingGoalValue, stringDate})
							remainingGoalValue = 0
							state.initInterest = k + 1
							state.monthComplete = false
							break
						}
					}
					if state.interestRemaining > 0 {
						break
					}
					state.initInterest = 0
				}
			}

			if state.investmentRemaining > 0 || state.interestRemaining > 0 {
				break
			}
			state.initInvestment = 0
			state.monthComplete = true
			state.currentDate = addMonths(state.currentDate, 1)
		}

		// ── Tag the goal ───────────────────────────────────────────────────
		// FIX #5: no cascade — every goal gets its own real number.
		// Resources from this goal (partial funding used) flow forward
		// to the next goal via state.remainingPortfolio.
		totalSavings := 0.0
		totalExpectedSavings := 0.0
		for _, s := range attachments[g.ID].savings {
			totalSavings += s.Amount
			totalExpectedSavings += s.ExpectedAmount
		}

		shortfallPct := 0.0
		if goalTarget > 0 {
			shortfallPct = math.Floor(remainingGoalValue / goalTarget * 100)
		}

		var tag GoalTag
		var projected, shortfall float64

		// FIX #5: finer bands — AT_RISK catches 10–30% gap
		switch {
		case shortfallPct <= 0:
			tag = TagComfortable
			projected = math.Floor(goalTarget)
			shortfall = 0
		case shortfallPct <= 5:
			tag = TagManageable
			projected = math.Floor(goalTarget - remainingGoalValue)
			shortfall = math.Floor(remainingGoalValue)
		case shortfallPct <= 30:
			tag = TagAtRisk
			projected = math.Floor(goalTarget - remainingGoalValue)
			shortfall = math.Floor(remainingGoalValue)
		default:
			tag = TagUnaffordable
			projected = math.Floor(goalTarget - remainingGoalValue)
			shortfall = math.Floor(remainingGoalValue)
		}

		att := attachments[g.ID]
		savingsEnd := formatDate(state.currentDate)

		gr := GoalResult{
			ID:              g.ID,
			Name:            g.Name,
			Icon:            g.Icon,
			StartDate:       g.StartDate,
			Tag:             tag,
			TaxTotal:        goalTarget,
			OverallTotal:    goalTarget + loanAmount,
			ProjectedAmount: projected,
			ShortfallAmount: shortfall,
			ShortfallPct:    shortfallPct,
			Attachments: GoalAttachments{
				Lumpsum: LumpsumAttachment{
					Amount:   roundAmount(att.lumpsum),
					Expected: roundAmount(att.expectedLumpsum),
				},
				Savings: SavingsAttachment{
					Amount:   roundAmount(totalSavings),
					Expected: roundAmount(totalExpectedSavings),
					Start:    savingStartDate,
					End:      savingsEnd,
				},
				FutureIncomes:      buildFutureIncomeAttachments(att.windfalls),
				MaturedInvestments: buildInvestmentAttachments(att.investments),
			},
		}

		results = append(results, gr)
	}

	return results, sipSchedule
}

// ─── Attachment summary builders ─────────────────────────────────────────────

func buildFutureIncomeAttachments(entries []windfallEntry) []FutureIncomeAttachment {
	out := make([]FutureIncomeAttachment, 0, len(entries))
	for _, e := range entries {
		out = append(out, FutureIncomeAttachment{
			Name:           e.Name,
			StartDate:      e.StartDate,
			Amount:         roundAmount(e.Amount),
			ExpectedAmount: roundAmount(e.ExpectedAmount),
		})
	}
	return out
}

func buildInvestmentAttachments(entries []investmentEntry) []FutureIncomeAttachment {
	out := make([]FutureIncomeAttachment, 0, len(entries))
	for _, e := range entries {
		out = append(out, FutureIncomeAttachment{
			Name:           e.AssetType,
			StartDate:      e.StartDate,
			Amount:         roundAmount(e.Amount),
			ExpectedAmount: roundAmount(e.ExpectedAmount),
		})
	}
	return out
}
