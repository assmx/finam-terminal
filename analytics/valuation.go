package analytics

import (
	"math"
	"strings"

	"finam-terminal/models"
)

// ValuationInput is the account's own report and the positions that came with
// it — the GetAccount response the terminal already reads every five seconds —
// plus the instruments' money facts and the rates the overview keeps. Nothing
// here costs a request.
type ValuationInput struct {
	Account   models.AccountInfo
	Positions []models.Position

	// Instruments and Rates convert each position's own figures into the base
	// currency, as in StructureInput. Without them every position counts in
	// the base currency at 1.
	Instruments map[string]Instrument
	Rates       map[string]models.FXRate
}

// Worth is what the account is worth and what it has made: at the start of the
// day, now, over the day, and on the positions it holds — the block the
// broker's own terminal opens its account summary with.
type Worth struct {
	// Current is the broker's equity.
	Current Metric

	// Daily is the day's result, summed over the positions that report one.
	// The broker leaves daily_pnl empty for FORTS positions, and
	// DailyUnreported counts those so the screen can say the figure is
	// partial. With nothing reported at all Daily is invalid — unless nothing
	// is held, in which case the day is a real zero. A blocked position does
	// not count as held: the broker values it at zero and leaves it out.
	Daily           Metric
	DailyUnreported int

	// DailyNoRate counts positions that did report a day's result but in a
	// currency without a rate — or a bond whose face currency is still being
	// looked up — so it cannot join a sum in the base currency. Like a missing
	// figure it makes the day partial; it is counted apart because the screen
	// says something different about it.
	DailyNoRate int

	// Opening is the value at the start of the day: Current minus Daily. The
	// API reports no such figure, and the subtraction is exact only on a day no
	// money entered or left the account — a deposit, a withdrawal, a commission
	// or a coupon moves equity without moving any position's result. It is
	// refused when the day's result is partial, because it would then be wrong
	// by exactly the part that is missing.
	Opening Metric

	// DailyShare is the day's result over the opening value.
	DailyShare Metric

	// Unrealized is the broker's own unrealised result on open positions.
	//
	// UnrealizedShare measures it against what the positions cost — average
	// price times size, by magnitude, since a short was opened at a price too,
	// through the face for a bond (its average price is a percentage of face,
	// like its current one) and converted into the base currency. It is
	// refused when any position's cost is unknown (the broker leaves
	// average_price empty for FORTS positions) or cannot be converted: the
	// broker's figure covers every position, so a percentage over the cost of
	// only some of them would overstate it.
	Unrealized      Metric
	UnrealizedShare Metric

	// FortsMargin is the collateral held against FORTS positions, summed from
	// the positions that report it. On a unified account this is the only
	// place the derivatives section's collateral shows. With no position
	// reporting it the figure is invalid rather than zero.
	FortsMargin Metric
}

// Valuation computes the block.
func Valuation(in ValuationInput) Worth {
	var w Worth

	if equity, ok := ParseNumber(in.Account.Equity); ok {
		w.Current = Metric{Value: equity, Valid: true}
	}
	if unrealized, ok := ParseNumber(in.Account.UnrealizedPnL); ok {
		w.Unrealized = Metric{Value: unrealized, Valid: true}
	}

	base := strings.ToUpper(baseCurrency(in.Account.Cash))

	var (
		daily, cost, margin           float64
		dailyReported, marginReported int
		held                          int
		costKnown                     = true
	)
	for _, p := range in.Positions {
		// The broker leaves a blocked position out of its equity and out of
		// its unrealised result (…5519, 2026-09-11: equity equals the cash,
		// unrealized 0.0 with FXRL at half its cost), so its day and its cost
		// stay out of these figures, and holding one is holding nothing the
		// broker values.
		if p.Blocked {
			continue
		}
		held++

		conv := pnlConversion(p, lookupInstrument(in.Instruments, p), base, in.Rates)

		if v, ok := ParseNumber(p.DailyPnL); ok {
			if conv.ok {
				daily += v * conv.pnlRate
				dailyReported++
			} else {
				w.DailyNoRate++
			}
		} else {
			w.DailyUnreported++
		}

		// FORTS collateral is roubles, and recalculating it is out of scope.
		if v, ok := ParseNumber(p.MaintenanceMargin); ok {
			margin += v
			marginReported++
		}

		qty, qtyOK := ParseNumber(p.Quantity)
		price, priceOK := ParseNumber(p.AveragePrice)
		if qtyOK && priceOK && price > 0 && conv.ok {
			cost += math.Abs(qty*price*conv.multiplier) * conv.valueRate
		} else {
			costKnown = false
		}
	}

	if dailyReported > 0 || held == 0 {
		w.Daily = finiteMetric(daily)
	}
	if marginReported > 0 {
		w.FortsMargin = finiteMetric(margin)
	}

	if w.Current.Valid && w.Daily.Valid && w.DailyUnreported == 0 && w.DailyNoRate == 0 {
		w.Opening = finiteMetric(w.Current.Value - w.Daily.Value)
	}
	if w.Opening.Valid && w.Opening.Value > 0 {
		w.DailyShare = finiteMetric(w.Daily.Value / w.Opening.Value)
	}

	// An overflowed cost would divide the result down to a plausible-looking
	// 0%, so it is refused along with an unknown one.
	if w.Unrealized.Valid && held > 0 && costKnown && cost > 0 && !math.IsInf(cost, 0) {
		w.UnrealizedShare = finiteMetric(w.Unrealized.Value / cost)
	}

	return w
}

// pnlConv is how one position's own figures reach the base currency.
type pnlConv struct {
	// ok is false when they cannot: no rate, or a bond whose face currency is
	// still being looked up.
	ok bool

	// multiplier turns a price into money: face / 100 for a bond, 1 otherwise.
	multiplier float64

	// valueRate converts a value in the position's currency (a cost) into the
	// base; pnlRate converts the broker's P&L figures, and is 1 when the check
	// finds them already converted.
	valueRate float64
	pnlRate   float64
}

// pnlConversion decides how a position's figures convert.
//
// The broker's P&L follows the price formula — quantity × price move ×
// multiplier, observed to the kopeck on rouble positions — so it is taken to be
// in the position's own currency. That was never observed on a foreign
// position, so where the figures allow a check it is made: an unrealised result
// that matches the formula only with the rate applied is one the broker has
// already converted, and taking the rate again would count it twice.
func pnlConversion(p models.Position, inst Instrument, base string, rates map[string]models.FXRate) pnlConv {
	c := pnlConv{multiplier: 1}
	if inst.FaceValue > 0 {
		c.multiplier = inst.FaceValue / 100
	}

	money := ValuePosition(p, "", inst, base)
	if money.State == FaceUnresolved {
		return c
	}
	rate := RateTo(money.Currency, base, rates)
	if !rate.Valid {
		return c
	}

	c.ok = true
	c.valueRate = rate.Value
	c.pnlRate = rate.Value
	if rate.Value != 1 && pnlAlreadyInBase(p, c.multiplier, rate.Value) {
		c.pnlRate = 1
	}
	return c
}

// PnLCurrency is the currency a position's own P&L figures (daily_pnl,
// unrealized_pnl) are in, by the rule Valuation converts them with: the
// position's value currency, unless the check finds the broker already
// converted them into the base. It is "" for a bond whose face currency is
// still being looked up, where the figures' currency cannot be named yet.
func PnLCurrency(p models.Position, inst Instrument, base string, rates map[string]models.FXRate) string {
	money := ValuePosition(p, "", inst, base)
	if money.State == FaceUnresolved {
		return ""
	}
	conv := pnlConversion(p, inst, base, rates)
	if conv.ok && conv.valueRate != 1 && conv.pnlRate == 1 {
		return strings.ToUpper(base)
	}
	return money.Currency
}

// pnlAlreadyInBase reports whether a position's unrealised result matches the
// price formula converted at rate and not the unconverted one. With no price
// move to compare the two cannot be told apart, and the answer is no.
func pnlAlreadyInBase(p models.Position, multiplier, rate float64) bool {
	reported, ok1 := ParseNumber(p.UnrealizedPnL)
	qty, ok2 := ParseNumber(p.Quantity)
	current, ok3 := ParseNumber(p.CurrentPrice)
	average, ok4 := ParseNumber(p.AveragePrice)
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return false
	}

	formula := qty * (current - average) * multiplier
	if math.Abs(formula) < 0.01 || math.IsInf(formula, 0) {
		return false
	}
	return !pnlMatches(reported, formula) && pnlMatches(reported, formula*rate)
}

// pnlMatches compares a reported figure with a computed one, allowing for the
// rounding of prices and amounts: 2% of the figure or a kopeck, whichever is
// larger.
func pnlMatches(reported, computed float64) bool {
	return math.Abs(reported-computed) <= math.Max(0.02*math.Abs(computed), 0.01)
}

// finiteMetric wraps a computed value, refusing one that left the float range.
// ParseNumber keeps NaN and Inf out on the way in, but a sum or a product of
// finite numbers can still overflow, and the package promises never to hand
// the screen a number that is not one.
func finiteMetric(v float64) Metric {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return Metric{}
	}
	return Metric{Value: v, Valid: true}
}
