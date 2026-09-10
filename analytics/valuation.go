package analytics

import (
	"math"

	"finam-terminal/models"
)

// ValuationInput is the account's own report and the positions that came with
// it — the GetAccount response the terminal already reads every five seconds.
// Nothing here costs a request.
type ValuationInput struct {
	Account   models.AccountInfo
	Positions []models.Position
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
	// is held, in which case the day is a real zero.
	Daily           Metric
	DailyUnreported int

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
	// price times size, by magnitude, since a short was opened at a price too.
	// It is refused when any position's cost is unknown (the broker leaves
	// average_price empty for FORTS positions): the broker's figure covers
	// every position, so a percentage over the cost of only some of them would
	// overstate it. Bonds carry PositionValue's caveat — should average_price
	// turn out to be quoted as a percent of par, their cost is understated.
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

	var (
		daily, cost, margin           float64
		dailyReported, marginReported int
		costKnown                     = len(in.Positions) > 0
	)
	for _, p := range in.Positions {
		if v, ok := ParseNumber(p.DailyPnL); ok {
			daily += v
			dailyReported++
		} else {
			w.DailyUnreported++
		}

		if v, ok := ParseNumber(p.MaintenanceMargin); ok {
			margin += v
			marginReported++
		}

		qty, qtyOK := ParseNumber(p.Quantity)
		price, priceOK := ParseNumber(p.AveragePrice)
		if qtyOK && priceOK && price > 0 {
			cost += math.Abs(qty * price)
		} else {
			costKnown = false
		}
	}

	if dailyReported > 0 || len(in.Positions) == 0 {
		w.Daily = finiteMetric(daily)
	}
	if marginReported > 0 {
		w.FortsMargin = finiteMetric(margin)
	}

	if w.Current.Valid && w.Daily.Valid && w.DailyUnreported == 0 {
		w.Opening = finiteMetric(w.Current.Value - w.Daily.Value)
	}
	if w.Opening.Valid && w.Opening.Value > 0 {
		w.DailyShare = finiteMetric(w.Daily.Value / w.Opening.Value)
	}

	// An overflowed cost would divide the result down to a plausible-looking
	// 0%, so it is refused along with an unknown one.
	if w.Unrealized.Valid && costKnown && cost > 0 && !math.IsInf(cost, 0) {
		w.UnrealizedShare = finiteMetric(w.Unrealized.Value / cost)
	}

	return w
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
