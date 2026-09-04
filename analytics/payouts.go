package analytics

import (
	"sort"
	"strings"
	"time"

	"finam-terminal/models"
)

// Payout kinds, as the table shows them.
const (
	PayoutDividend     = "Дивиденд"
	PayoutCoupon       = "Купон"
	PayoutAmortization = "Амортизация"
	PayoutOffer        = "Оферта"
)

// Payout horizons for the two summary lines.
const (
	payoutNear = 30 * 24 * time.Hour
	payoutFar  = 90 * 24 * time.Hour
)

// Payout is one expected payment on a position currently held.
//
// Amounts are before tax: the broker withholds on payment and the API says
// nothing about the rate that will apply, so subtracting a guess would be worse
// than saying what the gross figure is.
type Payout struct {
	Symbol   string
	Ticker   string
	Name     string
	Kind     string
	Date     string
	When     time.Time
	PerUnit  float64
	Quantity float64
	Currency string

	// Amount is PerUnit × Quantity. AmountValid is false for an offer, which
	// is a date the holder may act on rather than a payment they will receive.
	Amount      float64
	AmountValid bool
}

// PayoutTotals summarises the list.
type PayoutTotals struct {
	// In30 and In90 are the expected money per currency over the next 30 and
	// 90 days. Currencies are never added together.
	In30 map[string]float64
	In90 map[string]float64

	// Skipped counts calendar entries that looked like payouts but could not
	// be used — no date, or an amount that would not parse.
	Skipped int
}

// ExpectedPayouts builds the forecast from the positions held and the calendars
// already loaded for them.
//
// Only long positions appear: a short owes its payouts rather than receiving
// them, and listing it as income would have the sign exactly backwards.
//
// The calendars are looked up under the position's full symbol and its ticker,
// because the caller keys them by whatever it asked the API with.
func ExpectedPayouts(
	positions []models.Position,
	dividends map[string][]models.Dividend,
	events map[string][]models.BondEvent,
	now time.Time,
) ([]Payout, PayoutTotals) {
	totals := PayoutTotals{
		In30: make(map[string]float64),
		In90: make(map[string]float64),
	}
	var out []Payout

	today := dayStart(now)
	near := today.Add(payoutNear)
	far := today.Add(payoutFar)

	for _, p := range positions {
		qty, ok := ParseNumber(p.Quantity)
		if !ok || qty <= 0 {
			continue
		}

		for _, d := range lookupCalendar(dividends, p) {
			payout, usable, skipped := dividendPayout(d, p, qty, today)
			totals.Skipped += skipped
			if usable {
				out = append(out, payout)
			}
		}

		for _, e := range lookupCalendar(events, p) {
			payout, usable, skipped := eventPayout(e, p, qty, today)
			totals.Skipped += skipped
			if usable {
				out = append(out, payout)
			}
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].When.Equal(out[j].When) {
			return out[i].Ticker < out[j].Ticker
		}
		return out[i].When.Before(out[j].When)
	})

	for _, p := range out {
		if !p.AmountValid {
			continue
		}
		if !p.When.After(far) {
			totals.In90[p.Currency] += p.Amount
		}
		if !p.When.After(near) {
			totals.In30[p.Currency] += p.Amount
		}
	}

	return out, totals
}

// dividendPayout converts one calendar entry, reporting whether it is usable
// and whether it should be counted as skipped.
func dividendPayout(d models.Dividend, p models.Position, qty float64, today time.Time) (Payout, bool, int) {
	// The undated check comes first, and the order matters: a zero time is
	// before every date, so testing for the past first would file an undated
	// record as "already paid" and drop it without counting it.
	if d.When.IsZero() {
		return Payout{}, false, 1
	}
	if !d.IsFuture || d.When.Before(today) {
		// Already paid: not a forecast and not a problem.
		return Payout{}, false, 0
	}

	perUnit, ok := ParseNumber(d.Amount)
	if !ok {
		return Payout{}, false, 1
	}

	return Payout{
		Symbol:      p.Symbol,
		Ticker:      p.Ticker,
		Name:        p.Name,
		Kind:        PayoutDividend,
		Date:        d.Date,
		When:        d.When,
		PerUnit:     perUnit,
		Quantity:    qty,
		Currency:    d.Currency,
		Amount:      perUnit * qty,
		AmountValid: true,
	}, true, 0
}

// eventPayout does the same for a bond event.
//
// An offer with no value is expected rather than malformed — it is a date the
// holder may act on, not a payment — so it is listed without an amount and
// without being counted as skipped.
func eventPayout(e models.BondEvent, p models.Position, qty float64, today time.Time) (Payout, bool, int) {
	// Undated first, for the reason spelled out in dividendPayout.
	if e.When.IsZero() {
		return Payout{}, false, 1
	}
	if !e.IsFuture || e.When.Before(today) {
		return Payout{}, false, 0
	}

	kind := payoutKind(e.Kind)
	if kind == "" {
		return Payout{}, false, 0
	}

	payout := Payout{
		Symbol:   p.Symbol,
		Ticker:   p.Ticker,
		Name:     p.Name,
		Kind:     kind,
		Date:     e.Date,
		When:     e.When,
		Quantity: qty,
		Currency: e.Currency,
	}

	perUnit, ok := ParseNumber(e.Value)
	switch {
	case ok:
		payout.PerUnit = perUnit
		payout.Amount = perUnit * qty
		payout.AmountValid = true
	case kind == PayoutOffer:
		// Expected: an offer carries a date, not a sum.
	default:
		return Payout{}, false, 1
	}

	return payout, true, 0
}

// payoutKind maps a bond event kind to its display name. An unknown kind is not
// guessed at and simply does not appear.
func payoutKind(kind string) string {
	switch kind {
	case models.BondEventCoupon:
		return PayoutCoupon
	case models.BondEventAmortization:
		return PayoutAmortization
	case models.BondEventOffer:
		return PayoutOffer
	default:
		return ""
	}
}

// lookupCalendar finds a position's calendar under its full symbol or its
// ticker.
func lookupCalendar[T any](calendars map[string][]T, p models.Position) []T {
	if len(calendars) == 0 {
		return nil
	}
	if v, ok := calendars[p.Symbol]; ok {
		return v
	}
	if ticker := strings.TrimSpace(p.Ticker); ticker != "" {
		if v, ok := calendars[ticker]; ok {
			return v
		}
	}
	return nil
}

// dayStart is midnight of the given instant's day, in its own location.
//
// Calendar dates arrive as UTC midnights, so comparing them against a wall
// clock would drop a payout dated today the moment the day started.
func dayStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}
