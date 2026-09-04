package analytics

import (
	"testing"
	"time"

	"finam-terminal/models"
)

var payoutNow = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

func payoutDay(days int) time.Time {
	return time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, days)
}

func holding(symbol, ticker, quantity string) models.Position {
	return models.Position{Symbol: symbol, Ticker: ticker, Name: ticker, Quantity: quantity}
}

func futureDividend(days int, amount, currency string) models.Dividend {
	return models.Dividend{
		Date:     payoutDay(days).Format("2006-01-02"),
		When:     payoutDay(days),
		Amount:   amount,
		Currency: currency,
		IsFuture: true,
	}
}

// futureEvent builds a bond event in roubles; the currency split is exercised
// through the dividend helper, which takes one.
func futureEvent(days int, kind, value string) models.BondEvent {
	return models.BondEvent{
		Date:     payoutDay(days).Format("2006-01-02"),
		When:     payoutDay(days),
		Kind:     kind,
		Value:    value,
		Currency: "RUB",
		IsFuture: true,
	}
}

// TestExpectedPayouts_Dividends multiplies the per-share amount by the shares
// held.
func TestExpectedPayouts_Dividends(t *testing.T) {
	payouts, totals := ExpectedPayouts(
		[]models.Position{holding("SBER@MISX", "SBER", "100")},
		map[string][]models.Dividend{"SBER@MISX": {futureDividend(20, "34.50", "RUB")}},
		nil, payoutNow,
	)

	if len(payouts) != 1 {
		t.Fatalf("got %d payouts, want 1: %+v", len(payouts), payouts)
	}
	p := payouts[0]
	if p.Ticker != "SBER" || p.Kind != PayoutDividend {
		t.Errorf("payout = %+v, want a SBER dividend", p)
	}
	if !approx(p.PerUnit, 34.50) || !approx(p.Quantity, 100) || !approx(p.Amount, 3450) {
		t.Errorf("amounts = %v × %v = %v, want 34.50 × 100 = 3450", p.PerUnit, p.Quantity, p.Amount)
	}
	if !p.AmountValid || p.Currency != "RUB" {
		t.Errorf("payout = %+v, want a valid RUB amount", p)
	}
	if !approx(totals.In30["RUB"], 3450) || !approx(totals.In90["RUB"], 3450) {
		t.Errorf("totals = %v / %v, want 3450 in both windows", totals.In30, totals.In90)
	}
}

// TestExpectedPayouts_BondEvents covers the three bond kinds, including the
// offer that carries no amount.
func TestExpectedPayouts_BondEvents(t *testing.T) {
	payouts, _ := ExpectedPayouts(
		[]models.Position{holding("SU26238@TQOB", "SU26238", "10")},
		nil,
		map[string][]models.BondEvent{"SU26238@TQOB": {
			futureEvent(10, models.BondEventCoupon, "34.90"),
			futureEvent(20, models.BondEventAmortization, "100"),
			futureEvent(30, models.BondEventOffer, ""),
		}},
		payoutNow,
	)

	if len(payouts) != 3 {
		t.Fatalf("got %d payouts, want 3: %+v", len(payouts), payouts)
	}

	byKind := map[string]Payout{}
	for _, p := range payouts {
		byKind[p.Kind] = p
	}

	if c := byKind[PayoutCoupon]; !approx(c.Amount, 349) || !c.AmountValid {
		t.Errorf("coupon = %+v, want 34.90 × 10 = 349", c)
	}
	if a := byKind[PayoutAmortization]; !approx(a.Amount, 1000) || !a.AmountValid {
		t.Errorf("amortization = %+v, want 100 × 10 = 1000", a)
	}

	offer := byKind[PayoutOffer]
	if offer.Kind != PayoutOffer {
		t.Fatalf("the offer is missing: %+v", payouts)
	}
	if offer.AmountValid {
		t.Errorf("offer = %+v, want no amount — an offer is a date, not a payment", offer)
	}
}

// TestExpectedPayouts_ShortsExcluded: a short position owes payouts, it does
// not receive them, so it has no place in an income forecast.
func TestExpectedPayouts_ShortsExcluded(t *testing.T) {
	payouts, _ := ExpectedPayouts(
		[]models.Position{holding("SBER@MISX", "SBER", "-100")},
		map[string][]models.Dividend{"SBER@MISX": {futureDividend(20, "34.50", "RUB")}},
		nil, payoutNow,
	)
	if len(payouts) != 0 {
		t.Errorf("got %+v, want nothing for a short position", payouts)
	}
}

// TestExpectedPayouts_PastAndUndatedSkipped counts what it drops.
func TestExpectedPayouts_PastAndUndatedSkipped(t *testing.T) {
	past := futureDividend(-10, "10", "RUB")

	undated := futureDividend(20, "10", "RUB")
	undated.When = time.Time{}
	undated.Date = ""

	noAmount := futureDividend(30, "", "RUB")
	unreadable := futureDividend(40, "N/A", "RUB")

	alreadyPaid := futureDividend(50, "10", "RUB")
	alreadyPaid.IsFuture = false

	payouts, totals := ExpectedPayouts(
		[]models.Position{holding("SBER@MISX", "SBER", "100")},
		map[string][]models.Dividend{"SBER@MISX": {
			past, undated, noAmount, unreadable, alreadyPaid,
			futureDividend(60, "5", "RUB"),
		}},
		nil, payoutNow,
	)

	if len(payouts) != 1 {
		t.Fatalf("got %d payouts, want only the one usable record: %+v", len(payouts), payouts)
	}
	if !approx(payouts[0].Amount, 500) {
		t.Errorf("Amount = %v, want 500", payouts[0].Amount)
	}
	// The undated one, the two without a readable amount: three records that
	// looked like payouts and could not be used.
	if totals.Skipped != 3 {
		t.Errorf("Skipped = %d, want 3 (undated, empty amount, unreadable amount)", totals.Skipped)
	}
}

// TestExpectedPayouts_TodayCounts: a payout dated today has not happened yet as
// far as the holder is concerned.
func TestExpectedPayouts_TodayCounts(t *testing.T) {
	payouts, _ := ExpectedPayouts(
		[]models.Position{holding("SBER@MISX", "SBER", "10")},
		map[string][]models.Dividend{"SBER@MISX": {futureDividend(0, "1", "RUB")}},
		nil, payoutNow,
	)
	if len(payouts) != 1 {
		t.Errorf("got %d payouts, want the one dated today", len(payouts))
	}
}

// TestExpectedPayouts_SortedByDate is the order the table renders in.
func TestExpectedPayouts_SortedByDate(t *testing.T) {
	payouts, _ := ExpectedPayouts(
		[]models.Position{
			holding("SBER@MISX", "SBER", "10"),
			holding("GAZP@MISX", "GAZP", "10"),
		},
		map[string][]models.Dividend{
			"SBER@MISX": {futureDividend(50, "1", "RUB"), futureDividend(10, "2", "RUB")},
			"GAZP@MISX": {futureDividend(30, "3", "RUB")},
		},
		nil, payoutNow,
	)

	if len(payouts) != 3 {
		t.Fatalf("got %d payouts, want 3", len(payouts))
	}
	for i := 1; i < len(payouts); i++ {
		if payouts[i].When.Before(payouts[i-1].When) {
			t.Errorf("payout %d (%v) comes before %d (%v)", i, payouts[i].When, i-1, payouts[i-1].When)
		}
	}
	if payouts[0].Ticker != "SBER" || payouts[1].Ticker != "GAZP" {
		t.Errorf("order = %s, %s, %s; want the nearest date first",
			payouts[0].Ticker, payouts[1].Ticker, payouts[2].Ticker)
	}
}

// TestExpectedPayouts_Windows: the 30-day total is a subset of the 90-day one,
// and a payout beyond both is listed without entering either.
func TestExpectedPayouts_Windows(t *testing.T) {
	payouts, totals := ExpectedPayouts(
		[]models.Position{holding("SBER@MISX", "SBER", "10")},
		map[string][]models.Dividend{"SBER@MISX": {
			futureDividend(10, "1", "RUB"),  // inside 30
			futureDividend(60, "2", "RUB"),  // inside 90 only
			futureDividend(200, "4", "RUB"), // beyond both
		}},
		nil, payoutNow,
	)

	if len(payouts) != 3 {
		t.Fatalf("got %d payouts, want all three listed", len(payouts))
	}
	if !approx(totals.In30["RUB"], 10) {
		t.Errorf("In30 = %v, want 10", totals.In30["RUB"])
	}
	if !approx(totals.In90["RUB"], 30) {
		t.Errorf("In90 = %v, want 30 (both the near and the mid-range payout)", totals.In90["RUB"])
	}
}

// TestExpectedPayouts_TotalsByCurrency never adds two currencies together.
func TestExpectedPayouts_TotalsByCurrency(t *testing.T) {
	_, totals := ExpectedPayouts(
		[]models.Position{
			holding("SBER@MISX", "SBER", "10"),
			holding("AAPL@XNGS", "AAPL", "10"),
		},
		map[string][]models.Dividend{
			"SBER@MISX": {futureDividend(10, "34.5", "RUB")},
			"AAPL@XNGS": {futureDividend(10, "0.25", "USD")},
		},
		nil, payoutNow,
	)

	if !approx(totals.In30["RUB"], 345) || !approx(totals.In30["USD"], 2.5) {
		t.Errorf("totals = %v, want RUB 345 and USD 2.5", totals.In30)
	}
}

// TestExpectedPayouts_OfferHasNoAmount: an offer is listed for its date and
// contributes nothing to any total.
func TestExpectedPayouts_OfferHasNoAmount(t *testing.T) {
	_, totals := ExpectedPayouts(
		[]models.Position{holding("SU26238@TQOB", "SU26238", "10")},
		nil,
		map[string][]models.BondEvent{"SU26238@TQOB": {
			futureEvent(10, models.BondEventOffer, ""),
		}},
		payoutNow,
	)
	if len(totals.In30) != 0 || len(totals.In90) != 0 {
		t.Errorf("totals = %v / %v, want nothing from an offer", totals.In30, totals.In90)
	}
	if totals.Skipped != 0 {
		t.Errorf("Skipped = %d; an offer without an amount is expected, not malformed", totals.Skipped)
	}
}

// TestExpectedPayouts_UnknownSymbols: a position with no calendar simply has no
// rows, and a calendar for a symbol not held is ignored.
func TestExpectedPayouts_UnknownSymbols(t *testing.T) {
	payouts, _ := ExpectedPayouts(
		[]models.Position{holding("MOEX@MISX", "MOEX", "10")},
		map[string][]models.Dividend{"SBER@MISX": {futureDividend(10, "34.5", "RUB")}},
		nil, payoutNow,
	)
	if len(payouts) != 0 {
		t.Errorf("got %+v, want nothing", payouts)
	}
}

// TestExpectedPayouts_BadQuantity skips a position whose size cannot be read
// rather than guessing at it.
func TestExpectedPayouts_BadQuantity(t *testing.T) {
	payouts, _ := ExpectedPayouts(
		[]models.Position{holding("SBER@MISX", "SBER", "N/A")},
		map[string][]models.Dividend{"SBER@MISX": {futureDividend(10, "34.5", "RUB")}},
		nil, payoutNow,
	)
	if len(payouts) != 0 {
		t.Errorf("got %+v, want nothing for an unreadable position size", payouts)
	}
}

// TestExpectedPayouts_Empty answers usable zero values.
func TestExpectedPayouts_Empty(t *testing.T) {
	payouts, totals := ExpectedPayouts(nil, nil, nil, payoutNow)
	if len(payouts) != 0 {
		t.Errorf("payouts = %+v, want none", payouts)
	}
	if totals.In30 == nil || totals.In90 == nil {
		t.Error("the total maps are nil; the renderer would have to check")
	}
}

// TestExpectedPayouts_MatchesByTicker: the calendars are keyed by whatever the
// caller looked them up with, so a ticker key must find its position too.
func TestExpectedPayouts_MatchesByTicker(t *testing.T) {
	payouts, _ := ExpectedPayouts(
		[]models.Position{holding("SBER@MISX", "SBER", "10")},
		map[string][]models.Dividend{"SBER": {futureDividend(10, "1", "RUB")}},
		nil, payoutNow,
	)
	if len(payouts) != 1 {
		t.Errorf("got %d payouts, want the calendar found under the ticker", len(payouts))
	}
}
