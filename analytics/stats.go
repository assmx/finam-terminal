package analytics

import (
	"math"
	"sort"
	"time"

	"finam-terminal/models"
)

// TradeStats is the headline block of the Trades screen, for one currency.
//
// Results in different currencies are never added together: the Trade API
// carries no exchange rates, so a sum across them would be a made-up number.
// The base currency fills the main panel and the rest appear as their own
// lines.
type TradeStats struct {
	Currency string

	// ClosedCount is matched pairs, RawCount the trades that produced them.
	// They differ, and both are worth showing: one trade can close several
	// lots, and a period can hold openings that closed nothing.
	ClosedCount int
	RawCount    int

	Wins   int
	Losses int
	Zeros  int

	// WinRate is wins over decided trades. Flat trades are excluded from both
	// sides — they say nothing about whether the approach works. Invalid when
	// nothing was decided.
	WinRate      float64
	WinRateValid bool

	GrossProfit float64
	GrossLoss   float64 // negative
	Total       float64

	// ProfitFactor is gross profit over the magnitude of gross loss.
	// Infinite when there were gains and no losses; invalid when there is
	// nothing on either side, since 0/0 is not a ratio of anything.
	ProfitFactor         float64
	ProfitFactorValid    bool
	ProfitFactorInfinite bool

	AverageWin  float64
	AverageLoss float64 // negative

	// Expectancy is the total result spread over every closed trade, flat ones
	// included: it answers "what did an average trade earn", and a flat trade
	// is part of that average.
	Expectancy float64

	Best      ClosedTrade
	BestValid bool

	Worst      ClosedTrade
	WorstValid bool

	// Turnover is price × quantity over every raw trade in the window, both
	// directions.
	Turnover float64

	// Skipped counts raw trades whose price or quantity could not be read.
	Skipped int
}

// Stats summarises the closed trades and the raw trades of one window, grouped
// by currency.
//
// Closed trades are filtered on their exit: a result belongs to the moment the
// position was closed, not to when it was opened, so a position entered years
// ago and sold last week counts in last week's period.
//
// baseCurrency is where trades with no currency of their own are filed. The API
// does not populate the field consistently — the reconnaissance fixture carries
// it on a bond and not on the equities — and a nameless currency group would be
// worse than assuming the account's own.
func Stats(closedTrades []ClosedTrade, rawTrades []models.Trade, from, to time.Time, baseCurrency string) map[string]TradeStats {
	if baseCurrency == "" {
		baseCurrency = defaultBaseCurrency
	}

	grouped := make(map[string]*TradeStats)
	get := func(currency string) *TradeStats {
		if currency == "" {
			currency = baseCurrency
		}
		s, ok := grouped[currency]
		if !ok {
			s = &TradeStats{Currency: currency}
			grouped[currency] = s
		}
		return s
	}

	for _, c := range closedTrades {
		if !InRange(c.ExitTime, from, to) {
			continue
		}
		s := get(c.Currency)
		s.ClosedCount++
		s.Total += c.PnL

		switch {
		case c.PnL > 0:
			s.Wins++
			s.GrossProfit += c.PnL
			if !s.BestValid || c.PnL > s.Best.PnL {
				s.Best, s.BestValid = c, true
			}
		case c.PnL < 0:
			s.Losses++
			s.GrossLoss += c.PnL
			if !s.WorstValid || c.PnL < s.Worst.PnL {
				s.Worst, s.WorstValid = c, true
			}
		default:
			s.Zeros++
		}
	}

	for _, t := range rawTrades {
		if !InRange(t.Timestamp, from, to) {
			continue
		}
		s := get(t.Currency)
		s.RawCount++

		qty, okQty := ParseNumber(t.Quantity)
		price, okPrice := ParseNumber(t.Price)
		if !okQty || !okPrice {
			s.Skipped++
			continue
		}
		s.Turnover += math.Abs(qty * price)
	}

	out := make(map[string]TradeStats, len(grouped))
	for currency, s := range grouped {
		s.finish()
		out[currency] = *s
	}
	return out
}

// finish computes the derived figures once the totals are in.
func (s *TradeStats) finish() {
	if decided := s.Wins + s.Losses; decided > 0 {
		s.WinRate = float64(s.Wins) / float64(decided)
		s.WinRateValid = true
	}

	switch {
	case s.GrossLoss < 0:
		s.ProfitFactor = s.GrossProfit / math.Abs(s.GrossLoss)
		s.ProfitFactorValid = true
	case s.GrossProfit > 0:
		// Gains and no losses. Reported as infinite rather than as a very
		// large number, because the ratio genuinely has no value here.
		s.ProfitFactorValid = true
		s.ProfitFactorInfinite = true
	}

	if s.Wins > 0 {
		s.AverageWin = s.GrossProfit / float64(s.Wins)
	}
	if s.Losses > 0 {
		s.AverageLoss = s.GrossLoss / float64(s.Losses)
	}
	if s.ClosedCount > 0 {
		s.Expectancy = s.Total / float64(s.ClosedCount)
	}
}

// InstrumentRow is one line of the per-instrument table on the Trades screen.
type InstrumentRow struct {
	Symbol   string
	Name     string
	Currency string

	ClosedCount int
	PnL         float64

	WinRate      float64
	WinRateValid bool

	// OpenQty is the position the history implies right now, signed. It is not
	// filtered by the period: what is held is held regardless of the window
	// being looked at.
	OpenQty float64

	// Unmatched is the quantity sold in this instrument with no entry price
	// behind it, so the row can be marked as a partial result.
	Unmatched float64
}

// PerInstrument builds the table rows from a matching pass, filtered to one
// window.
//
// An instrument with an open position but nothing closed in the window still
// gets a row: holding something and realising nothing is information, not an
// empty line.
//
// Rows are ordered by result, best first, with ties broken alphabetically so
// the table does not shuffle between redraws.
func PerInstrument(result FIFOResult, from, to time.Time) []InstrumentRow {
	rows := make(map[string]*InstrumentRow)
	get := func(symbol string) *InstrumentRow {
		r, ok := rows[symbol]
		if !ok {
			r = &InstrumentRow{Symbol: symbol}
			rows[symbol] = r
		}
		return r
	}

	wins := make(map[string]int)
	decided := make(map[string]int)

	for _, c := range result.Closed {
		if !InRange(c.ExitTime, from, to) {
			continue
		}
		r := get(c.Symbol)
		r.ClosedCount++
		r.PnL += c.PnL
		if r.Currency == "" {
			r.Currency = c.Currency
		}
		if r.Name == "" {
			r.Name = c.Name
		}
		switch {
		case c.PnL > 0:
			wins[c.Symbol]++
			decided[c.Symbol]++
		case c.PnL < 0:
			decided[c.Symbol]++
		}
	}

	for symbol, lots := range result.Open {
		r := get(symbol)
		r.OpenQty = result.OpenQuantity(symbol)
		if r.Name == "" && len(lots) > 0 {
			r.Name = lots[0].Name
		}
	}

	for symbol, qty := range result.Unmatched {
		get(symbol).Unmatched = qty
	}

	out := make([]InstrumentRow, 0, len(rows))
	for symbol, r := range rows {
		if n := decided[symbol]; n > 0 {
			r.WinRate = float64(wins[symbol]) / float64(n)
			r.WinRateValid = true
		}
		out = append(out, *r)
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].PnL != out[j].PnL {
			return out[i].PnL > out[j].PnL
		}
		return out[i].Symbol < out[j].Symbol
	})
	return out
}
