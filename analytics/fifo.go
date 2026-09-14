package analytics

import (
	"math"
	"sort"
	"strings"
	"time"

	"finam-terminal/models"
)

// Lot is an open position built by one trade, or what is left of it after
// partial closures.
//
// Price is per unit and already carries this trade's share of any accrued
// interest, so a bond lot values the same way an equity one does.
type Lot struct {
	Qty   float64
	Price float64
	Short bool
	Time  time.Time

	// Name is the instrument's human-readable name, carried from the trade so
	// a table row can be labelled without a second lookup.
	Name string

	// TradeID is the trade that opened the lot, kept so a closed pair can be
	// traced back to its source.
	TradeID string
}

// ClosedTrade is one entry matched against one exit.
//
// A trade that closes several lots produces several of these, because each
// carries its own entry price and entry time; summing their PnL gives the
// result of the closing trade.
//
// PnL is (Exit − Entry) × Qty for a long and the negative of that for a short.
// Prices include accrued interest, which is why a bond round trip at an
// unchanged quote can still show a result.
type ClosedTrade struct {
	Symbol     string
	Name       string
	Currency   string
	Qty        float64
	EntryPrice float64
	ExitPrice  float64
	EntryTime  time.Time
	ExitTime   time.Time
	Short      bool
	PnL        float64
}

// FIFOResult is everything one matching pass produced.
type FIFOResult struct {
	// Closed pairs, in the order their exits happened.
	Closed []ClosedTrade

	// Open lots per symbol, oldest first.
	Open map[string][]Lot

	// Unmatched is the quantity sold with nothing open to sell, per symbol.
	//
	// It is the visible symptom of history that starts mid-position: an
	// account older than the loader's reach shows the sale but not the
	// purchase, and there is no entry price to compute a result against. The
	// screen reports the count rather than inventing one.
	//
	// A genuine opening short is indistinguishable from that and will be
	// counted too. Nothing in the data separates the two — which is also why
	// the mirrored case, a purchase closing a short whose sale is off the
	// edge of history, is not counted at all: an opening long and a
	// truncated cover look exactly alike.
	Unmatched map[string]float64

	// Skipped is how many records could not be read at all — an unparsable
	// price or quantity, a missing symbol, a side that is neither buy nor
	// sell. They are counted rather than treated as zero, because a zero
	// would quietly distort every figure downstream.
	Skipped int
}

// OpenQuantity is the net position the matched history implies for a symbol:
// positive when long, negative when short.
//
// This is what the reconciliation against the broker compares. A disagreement
// means the history is missing something — a truncated window, a share
// transfer, a split — and the screen says so rather than adjusting the number.
func (r FIFOResult) OpenQuantity(symbol string) float64 {
	var qty float64
	for _, lot := range r.Open[symbol] {
		if lot.Short {
			qty -= lot.Qty
		} else {
			qty += lot.Qty
		}
	}
	return qty
}

// MatchFIFO pairs entries with exits, oldest lot first, across every symbol in
// the input.
//
// The input is sorted before matching: the history loader walks an account
// backwards in chunks, so trades do not arrive in time order. Ties are broken
// on the trade id so the same history always produces the same result.
func MatchFIFO(trades []models.Trade) FIFOResult {
	result := FIFOResult{
		Open:      make(map[string][]Lot),
		Unmatched: make(map[string]float64),
	}

	sorted := make([]models.Trade, len(trades))
	copy(sorted, trades)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Timestamp.Equal(sorted[j].Timestamp) {
			return sorted[i].ID < sorted[j].ID
		}
		return sorted[i].Timestamp.Before(sorted[j].Timestamp)
	})

	for _, t := range sorted {
		symbol := strings.TrimSpace(t.Symbol)
		if symbol == "" {
			result.Skipped++
			continue
		}

		short, ok := sideIsShort(t.Side)
		if !ok {
			result.Skipped++
			continue
		}

		qty, ok := ParseNumber(t.Quantity)
		if !ok || qty <= 0 {
			result.Skipped++
			continue
		}

		price, ok := ParseNumber(t.Price)
		if !ok {
			result.Skipped++
			continue
		}

		// The trade's accrued interest is a total for the whole trade; spread
		// over the quantity it becomes part of the per-unit price, on both
		// sides: paid on a purchase, received on a sale.
		if nkd, hasNKD := ParseNumber(t.AccruedInterest); hasNKD && nkd != 0 {
			price += nkd / qty
		}
		if math.IsNaN(price) || math.IsInf(price, 0) {
			result.Skipped++
			continue
		}

		result.apply(symbol, t, short, qty, price)
	}

	return result
}

// apply matches one readable trade against the symbol's open lots.
func (r *FIFOResult) apply(symbol string, t models.Trade, short bool, qty, price float64) {
	lots := r.Open[symbol]

	// Nothing open: the trade opens a position. A sale here is also the
	// symptom Unmatched exists to report.
	if len(lots) == 0 {
		if short {
			r.Unmatched[symbol] += qty
		}
		r.Open[symbol] = append(lots, Lot{Qty: qty, Price: price, Short: short, Time: t.Timestamp, Name: t.Name, TradeID: t.ID})
		return
	}

	// Same direction as what is open: another lot on the pile.
	if lots[0].Short == short {
		r.Open[symbol] = append(lots, Lot{Qty: qty, Price: price, Short: short, Time: t.Timestamp, Name: t.Name, TradeID: t.ID})
		return
	}

	// Opposite direction: close lots oldest first.
	remaining := qty
	for remaining > 0 && len(lots) > 0 {
		lot := lots[0]
		closed := math.Min(remaining, lot.Qty)

		pnl := (price - lot.Price) * closed
		if lot.Short {
			pnl = -pnl
		}

		r.Closed = append(r.Closed, ClosedTrade{
			Symbol:     symbol,
			Name:       t.Name,
			Currency:   t.Currency,
			Qty:        closed,
			EntryPrice: lot.Price,
			ExitPrice:  price,
			EntryTime:  lot.Time,
			ExitTime:   t.Timestamp,
			Short:      lot.Short,
			PnL:        pnl,
		})

		remaining -= closed
		lot.Qty -= closed
		if lot.Qty <= 0 {
			lots = lots[1:]
		} else {
			lots[0] = lot
		}
	}

	// More than was open: the rest opens a position the other way. This is a
	// reversal, not missing history — the lots it consumed prove the entry
	// prices were there.
	if remaining > 0 {
		lots = append(lots, Lot{Qty: remaining, Price: price, Short: short, Time: t.Timestamp, TradeID: t.ID})
	}

	if len(lots) == 0 {
		delete(r.Open, symbol)
		return
	}
	r.Open[symbol] = lots
}

// sideIsShort reads the side string the client produces. Anything else is not
// guessed at.
func sideIsShort(side string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(side)) {
	case "buy":
		return false, true
	case "sell":
		return true, true
	default:
		return false, false
	}
}

// reconcileEpsilon is the largest difference treated as rounding rather than a
// real disagreement. Quantities are whole units in practice; the tolerance
// exists only so a float subtraction cannot manufacture a discrepancy.
const reconcileEpsilon = 1e-6

// Reconcile compares the position the matched history implies against the one
// the broker reports, per symbol.
//
// The result holds only symbols where the two disagree, and the value is
// broker − history: positive means the account holds more than the history
// accounts for. Nothing is adjusted. A disagreement is information — history
// truncated before the first purchase, a share transfer, a split the trades do
// not describe — and quietly correcting the quantity would hide the one signal
// that says the numbers on the screen are incomplete.
//
// A position whose quantity cannot be read is skipped rather than reported as a
// full discrepancy, since an unreadable quantity says nothing about the history.
func Reconcile(result FIFOResult, positions []models.Position) map[string]float64 {
	diffs := make(map[string]float64)

	seen := make(map[string]bool, len(positions))
	for _, p := range positions {
		symbol := strings.TrimSpace(p.Symbol)
		if symbol == "" {
			continue
		}
		seen[symbol] = true

		brokerQty, ok := ParseNumber(p.Quantity)
		if !ok {
			continue
		}
		if diff := brokerQty - result.OpenQuantity(symbol); math.Abs(diff) > reconcileEpsilon {
			diffs[symbol] = diff
		}
	}

	// A symbol the history still holds open but the broker does not report at
	// all is the same kind of disagreement, seen from the other side.
	for symbol := range result.Open {
		if seen[symbol] {
			continue
		}
		if qty := result.OpenQuantity(symbol); math.Abs(qty) > reconcileEpsilon {
			diffs[symbol] = -qty
		}
	}

	return diffs
}
