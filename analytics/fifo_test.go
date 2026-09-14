package analytics

import (
	"math"
	"math/rand"
	"testing"
	"time"

	"finam-terminal/models"
)

var fifoBase = time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)

// trade builds a trade at an offset from a fixed base time, so a test reads as
// a sequence rather than a wall of dates.
func trade(id, symbol, side string, minutes int, qty, price string) models.Trade {
	return models.Trade{
		ID:        id,
		Symbol:    symbol,
		Side:      side,
		Quantity:  qty,
		Price:     price,
		Currency:  "RUB",
		Timestamp: fifoBase.Add(time.Duration(minutes) * time.Minute),
	}
}

func withNKD(t models.Trade, nkd string) models.Trade {
	t.AccruedInterest = nkd
	return t
}

func approx(got, want float64) bool { return math.Abs(got-want) < 1e-6 }

// TestMatchFIFO_SimpleLong is the base case: buy then sell the lot whole.
func TestMatchFIFO_SimpleLong(t *testing.T) {
	res := MatchFIFO([]models.Trade{
		trade("1", "SBER@MISX", "Buy", 0, "10", "100"),
		trade("2", "SBER@MISX", "Sell", 60, "10", "120"),
	})

	if len(res.Closed) != 1 {
		t.Fatalf("got %d closed trades, want 1: %+v", len(res.Closed), res.Closed)
	}
	c := res.Closed[0]
	if !approx(c.PnL, 200) {
		t.Errorf("PnL = %v, want 200", c.PnL)
	}
	if c.Short {
		t.Error("Short = true for a long round trip")
	}
	if !approx(c.Qty, 10) || !approx(c.EntryPrice, 100) || !approx(c.ExitPrice, 120) {
		t.Errorf("closed trade = %+v, want 10 @ 100 -> 120", c)
	}
	if !c.EntryTime.Equal(fifoBase) || !c.ExitTime.Equal(fifoBase.Add(time.Hour)) {
		t.Errorf("times = %v -> %v, want %v -> %v", c.EntryTime, c.ExitTime, fifoBase, fifoBase.Add(time.Hour))
	}
	if len(res.Open["SBER@MISX"]) != 0 {
		t.Errorf("open lots remain: %+v", res.Open["SBER@MISX"])
	}
	if res.Unmatched["SBER@MISX"] != 0 {
		t.Errorf("Unmatched = %v, want 0", res.Unmatched["SBER@MISX"])
	}
}

// TestMatchFIFO_OldestLotFirst is the whole point of FIFO: a partial sale
// closes against the earliest purchase, not the cheapest or the latest.
func TestMatchFIFO_OldestLotFirst(t *testing.T) {
	res := MatchFIFO([]models.Trade{
		trade("1", "SBER@MISX", "Buy", 0, "10", "100"),
		trade("2", "SBER@MISX", "Buy", 10, "10", "150"),
		trade("3", "SBER@MISX", "Sell", 20, "10", "200"),
	})

	if len(res.Closed) != 1 {
		t.Fatalf("got %d closed trades, want 1", len(res.Closed))
	}
	if !approx(res.Closed[0].EntryPrice, 100) {
		t.Errorf("EntryPrice = %v, want 100 — the oldest lot closes first", res.Closed[0].EntryPrice)
	}
	if !approx(res.Closed[0].PnL, 1000) {
		t.Errorf("PnL = %v, want 1000", res.Closed[0].PnL)
	}

	open := res.Open["SBER@MISX"]
	if len(open) != 1 || !approx(open[0].Qty, 10) || !approx(open[0].Price, 150) {
		t.Errorf("open lots = %+v, want the 10 @ 150 still held", open)
	}
}

// TestMatchFIFO_PartialClosures splits one lot across several sales and one
// sale across several lots.
func TestMatchFIFO_PartialClosures(t *testing.T) {
	res := MatchFIFO([]models.Trade{
		trade("1", "SBER@MISX", "Buy", 0, "10", "100"),
		trade("2", "SBER@MISX", "Sell", 10, "4", "110"), // 4 of the lot
		trade("3", "SBER@MISX", "Buy", 20, "5", "130"),  // second lot
		trade("4", "SBER@MISX", "Sell", 30, "8", "140"), // 6 of lot 1, 2 of lot 2
	})

	if len(res.Closed) != 3 {
		t.Fatalf("got %d closed trades, want 3: %+v", len(res.Closed), res.Closed)
	}

	var total float64
	for _, c := range res.Closed {
		total += c.PnL
	}
	// 4×(110−100) + 6×(140−100) + 2×(140−130) = 40 + 240 + 20
	if !approx(total, 300) {
		t.Errorf("total PnL = %v, want 300", total)
	}

	open := res.Open["SBER@MISX"]
	if len(open) != 1 || !approx(open[0].Qty, 3) {
		t.Errorf("open lots = %+v, want 3 left of the second lot", open)
	}
}

// TestMatchFIFO_Short opens on the sell side and closes with a buy.
func TestMatchFIFO_Short(t *testing.T) {
	res := MatchFIFO([]models.Trade{
		trade("1", "GAZP@MISX", "Sell", 0, "5", "200"),
		trade("2", "GAZP@MISX", "Buy", 30, "5", "180"),
	})

	if len(res.Closed) != 1 {
		t.Fatalf("got %d closed trades, want 1", len(res.Closed))
	}
	c := res.Closed[0]
	if !c.Short {
		t.Error("Short = false for a short round trip")
	}
	// Sold at 200, bought back at 180: a gain of 20 per share.
	if !approx(c.PnL, 100) {
		t.Errorf("PnL = %v, want 100 — a short profits when the price falls", c.PnL)
	}
	if !approx(c.EntryPrice, 200) || !approx(c.ExitPrice, 180) {
		t.Errorf("prices = %v -> %v, want 200 -> 180", c.EntryPrice, c.ExitPrice)
	}
}

// TestMatchFIFO_Reversal: a sale larger than the open long closes it and opens
// a short with the remainder.
func TestMatchFIFO_Reversal(t *testing.T) {
	res := MatchFIFO([]models.Trade{
		trade("1", "SBER@MISX", "Buy", 0, "10", "100"),
		trade("2", "SBER@MISX", "Sell", 10, "15", "120"),
	})

	if len(res.Closed) != 1 {
		t.Fatalf("got %d closed trades, want 1", len(res.Closed))
	}
	if !approx(res.Closed[0].Qty, 10) || !approx(res.Closed[0].PnL, 200) {
		t.Errorf("closed = %+v, want the 10 long shares at +200", res.Closed[0])
	}
	if res.Unmatched["SBER@MISX"] != 0 {
		t.Errorf("Unmatched = %v; a reversal is not an unmatched sale", res.Unmatched["SBER@MISX"])
	}

	open := res.Open["SBER@MISX"]
	if len(open) != 1 {
		t.Fatalf("open lots = %+v, want one short lot", open)
	}
	if !approx(open[0].Qty, 5) || !open[0].Short || !approx(open[0].Price, 120) {
		t.Errorf("open lot = %+v, want 5 short at 120", open[0])
	}
}

// TestMatchFIFO_SaleWithoutEntry counts a sale with no lot behind it instead of
// inventing an entry price. History that starts mid-position is the normal case
// for an account older than the loader's reach.
func TestMatchFIFO_SaleWithoutEntry(t *testing.T) {
	res := MatchFIFO([]models.Trade{
		trade("1", "LKOH@MISX", "Sell", 0, "7", "5000"),
	})

	if len(res.Closed) != 0 {
		t.Errorf("closed = %+v, want none — there is no entry price to compute against", res.Closed)
	}
	if !approx(res.Unmatched["LKOH@MISX"], 7) {
		t.Errorf("Unmatched = %v, want 7", res.Unmatched["LKOH@MISX"])
	}

	// It still opens a short: a sale is a sale, and the position is real.
	open := res.Open["LKOH@MISX"]
	if len(open) != 1 || !open[0].Short {
		t.Errorf("open = %+v, want a short lot of 7", open)
	}
}

// TestMatchFIFO_UnmatchedOnlyForOpeningTheOtherWay: an unmatched quantity is
// only recorded when the account had nothing open, not on a reversal that
// closed something first.
func TestMatchFIFO_UnmatchedCountsOnlyTheUncovered(t *testing.T) {
	res := MatchFIFO([]models.Trade{
		trade("1", "SBER@MISX", "Buy", 0, "3", "100"),
		trade("2", "SBER@MISX", "Sell", 10, "10", "120"),
	})

	if !approx(res.Closed[0].Qty, 3) {
		t.Errorf("closed qty = %v, want the 3 that were covered", res.Closed[0].Qty)
	}
	if res.Unmatched["SBER@MISX"] != 0 {
		t.Errorf("Unmatched = %v; the uncovered 7 opened a short rather than vanishing",
			res.Unmatched["SBER@MISX"])
	}
}

// TestMatchFIFO_AccruedInterest folds a bond's accrued interest into the cash
// that changed hands: it raises the cost of a purchase and adds to the proceeds
// of a sale.
func TestMatchFIFO_AccruedInterest(t *testing.T) {
	res := MatchFIFO([]models.Trade{
		withNKD(trade("1", "SU26238@TQOB", "Buy", 0, "10", "650"), "20"),
		withNKD(trade("2", "SU26238@TQOB", "Sell", 60, "10", "660"), "30"),
	})

	if len(res.Closed) != 1 {
		t.Fatalf("got %d closed trades, want 1", len(res.Closed))
	}
	// Paid 10×650 + 20 = 6520, received 10×660 + 30 = 6630.
	if !approx(res.Closed[0].PnL, 110) {
		t.Errorf("PnL = %v, want 110 — accrued interest is part of the money that moved",
			res.Closed[0].PnL)
	}
}

// TestMatchFIFO_AccruedInterestProrated spreads a trade's accrued interest over
// the lots it touches, in proportion to the quantity closed.
func TestMatchFIFO_AccruedInterestProrated(t *testing.T) {
	res := MatchFIFO([]models.Trade{
		withNKD(trade("1", "SU26238@TQOB", "Buy", 0, "10", "1000"), "100"),
		trade("2", "SU26238@TQOB", "Sell", 10, "4", "1000"),
	})

	if len(res.Closed) != 1 {
		t.Fatalf("got %d closed trades, want 1", len(res.Closed))
	}
	// 4 of 10 shares carry 40 of the 100 paid in accrued interest, and the
	// prices are equal, so the loss is exactly that 40.
	if !approx(res.Closed[0].PnL, -40) {
		t.Errorf("PnL = %v, want -40 — 4/10 of the 100 accrued interest paid", res.Closed[0].PnL)
	}
}

// TestMatchFIFO_SeparatesSymbols keeps each instrument's queue to itself.
func TestMatchFIFO_SeparatesSymbols(t *testing.T) {
	res := MatchFIFO([]models.Trade{
		trade("1", "SBER@MISX", "Buy", 0, "10", "100"),
		trade("2", "GAZP@MISX", "Sell", 10, "10", "200"),
		trade("3", "SBER@MISX", "Sell", 20, "10", "110"),
	})

	if len(res.Closed) != 1 || res.Closed[0].Symbol != "SBER@MISX" {
		t.Fatalf("closed = %+v, want only the SBER round trip", res.Closed)
	}
	if !approx(res.Unmatched["GAZP@MISX"], 10) {
		t.Errorf("GAZP unmatched = %v, want 10", res.Unmatched["GAZP@MISX"])
	}
}

// TestMatchFIFO_Currency carries the trade currency onto the closed trade, so
// results in different currencies are never added together.
func TestMatchFIFO_Currency(t *testing.T) {
	usd := trade("1", "AAPL@XNGS", "Buy", 0, "1", "100")
	usd.Currency = "USD"
	usdSell := trade("2", "AAPL@XNGS", "Sell", 10, "1", "120")
	usdSell.Currency = "USD"

	res := MatchFIFO([]models.Trade{
		usd, usdSell,
		trade("3", "SBER@MISX", "Buy", 0, "1", "100"),
		trade("4", "SBER@MISX", "Sell", 10, "1", "110"),
	})

	byCurrency := map[string]float64{}
	for _, c := range res.Closed {
		byCurrency[c.Currency] += c.PnL
	}
	if !approx(byCurrency["USD"], 20) || !approx(byCurrency["RUB"], 10) {
		t.Errorf("by currency = %v, want USD 20 and RUB 10", byCurrency)
	}
}

// TestMatchFIFO_SortsByTimeThenID puts the input in order first. The loader
// walks backwards in chunks, so trades do not arrive in time order at all.
func TestMatchFIFO_SortsByTimeThenID(t *testing.T) {
	// Delivered newest first, as a backwards walk produces.
	res := MatchFIFO([]models.Trade{
		trade("3", "SBER@MISX", "Sell", 20, "10", "200"),
		trade("2", "SBER@MISX", "Buy", 10, "10", "150"),
		trade("1", "SBER@MISX", "Buy", 0, "10", "100"),
	})

	if len(res.Closed) != 1 {
		t.Fatalf("got %d closed trades, want 1", len(res.Closed))
	}
	if !approx(res.Closed[0].EntryPrice, 100) {
		t.Errorf("EntryPrice = %v, want 100 — the input must be sorted before matching",
			res.Closed[0].EntryPrice)
	}
}

// TestMatchFIFO_SameTimestampOrderedByID makes the result deterministic when
// two trades share an instant.
func TestMatchFIFO_SameTimestampOrderedByID(t *testing.T) {
	a := trade("b", "SBER@MISX", "Buy", 0, "10", "100")
	b := trade("a", "SBER@MISX", "Buy", 0, "10", "150")
	sell := trade("c", "SBER@MISX", "Sell", 10, "10", "200")

	first := MatchFIFO([]models.Trade{a, b, sell})
	second := MatchFIFO([]models.Trade{b, a, sell})

	if len(first.Closed) != 1 || len(second.Closed) != 1 {
		t.Fatalf("closed counts = %d and %d, want 1 each", len(first.Closed), len(second.Closed))
	}
	if first.Closed[0].EntryPrice != second.Closed[0].EntryPrice {
		t.Errorf("entry prices differ by input order: %v vs %v — ties must break on id",
			first.Closed[0].EntryPrice, second.Closed[0].EntryPrice)
	}
	if !approx(first.Closed[0].EntryPrice, 150) {
		t.Errorf("EntryPrice = %v, want 150 — id \"a\" sorts before \"b\"", first.Closed[0].EntryPrice)
	}
}

// TestMatchFIFO_SkipsUnreadable counts records it cannot parse instead of
// treating them as zero, which would silently distort every figure downstream.
func TestMatchFIFO_SkipsUnreadable(t *testing.T) {
	res := MatchFIFO([]models.Trade{
		trade("1", "SBER@MISX", "Buy", 0, "N/A", "100"),
		trade("2", "SBER@MISX", "Buy", 5, "10", ""),
		trade("3", "", "Buy", 6, "10", "100"),
		trade("4", "SBER@MISX", "Hold", 7, "10", "100"),
		trade("5", "SBER@MISX", "Buy", 10, "10", "100"),
		trade("6", "SBER@MISX", "Sell", 20, "10", "110"),
	})

	if res.Skipped != 4 {
		t.Errorf("Skipped = %d, want 4 (bad quantity, bad price, no symbol, unknown side)", res.Skipped)
	}
	if len(res.Closed) != 1 || !approx(res.Closed[0].PnL, 100) {
		t.Errorf("closed = %+v, want the one readable round trip at +100", res.Closed)
	}
}

// TestMatchFIFO_Empty does nothing gracefully.
func TestMatchFIFO_Empty(t *testing.T) {
	res := MatchFIFO(nil)
	if len(res.Closed) != 0 || len(res.Open) != 0 || len(res.Unmatched) != 0 || res.Skipped != 0 {
		t.Errorf("empty input produced %+v", res)
	}

	// The maps must be usable without a nil check.
	if res.Open["nothing"] != nil {
		t.Error("Open answered a non-nil slice for an unknown symbol")
	}
}

// TestMatchFIFO_OpenQuantity reports the net position per symbol, which is what
// the reconciliation against the broker compares.
func TestMatchFIFO_OpenQuantity(t *testing.T) {
	res := MatchFIFO([]models.Trade{
		trade("1", "SBER@MISX", "Buy", 0, "10", "100"),
		trade("2", "SBER@MISX", "Buy", 10, "5", "110"),
		trade("3", "SBER@MISX", "Sell", 20, "4", "120"),
		trade("4", "GAZP@MISX", "Sell", 30, "8", "200"),
	})

	if got := res.OpenQuantity("SBER@MISX"); !approx(got, 11) {
		t.Errorf("SBER open quantity = %v, want 11", got)
	}
	if got := res.OpenQuantity("GAZP@MISX"); !approx(got, -8) {
		t.Errorf("GAZP open quantity = %v, want -8 for a short", got)
	}
	if got := res.OpenQuantity("NOPE"); got != 0 {
		t.Errorf("unknown symbol open quantity = %v, want 0", got)
	}
}

// TestMatchFIFO_CashInvariant is the property that has to hold whatever the
// sequence: realised result plus the money still tied up in open lots equals
// the net cash the trades moved. If matching ever loses or invents a share,
// this is what catches it.
func TestMatchFIFO_CashInvariant(t *testing.T) {
	rng := rand.New(rand.NewSource(20260904))

	for run := range 200 {
		n := 2 + rng.Intn(12)
		trades := make([]models.Trade, 0, n)
		for i := range n {
			side := "Buy"
			if rng.Intn(2) == 0 {
				side = "Sell"
			}
			qty := 1 + rng.Intn(9)
			price := 50 + rng.Intn(200)
			trades = append(trades, trade(
				string(rune('a'+i)), "SBER@MISX", side, i*10,
				itoa(qty), itoa(price),
			))
		}

		res := MatchFIFO(trades)

		// Cash the trades moved: a purchase costs, a sale pays.
		var netCash float64
		for _, tr := range trades {
			q, _ := ParseNumber(tr.Quantity)
			p, _ := ParseNumber(tr.Price)
			if tr.Side == "Buy" {
				netCash -= q * p
			} else {
				netCash += q * p
			}
		}

		var realised float64
		for _, c := range res.Closed {
			realised += c.PnL
		}

		// Money still tied up in open lots, signed the same way as the cash
		// flow that created them.
		var openCost float64
		for _, lots := range res.Open {
			for _, lot := range lots {
				if lot.Short {
					openCost += lot.Qty * lot.Price
				} else {
					openCost -= lot.Qty * lot.Price
				}
			}
		}

		if !approx(realised+openCost, netCash) {
			t.Fatalf("run %d: realised %v + open cost %v != net cash %v\ntrades: %+v",
				run, realised, openCost, netCash, trades)
		}
	}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}

// TestReconcile compares the position the history implies with the one the
// broker reports. Every disagreement is reported, none is corrected.
func TestReconcile(t *testing.T) {
	res := MatchFIFO([]models.Trade{
		trade("1", "SBER@MISX", "Buy", 0, "10", "100"),
		trade("2", "GAZP@MISX", "Buy", 10, "5", "160"),
		trade("3", "LKOH@MISX", "Buy", 20, "2", "5000"),
	})

	diffs := Reconcile(res, []models.Position{
		{Symbol: "SBER@MISX", Quantity: "10"}, // agrees
		{Symbol: "GAZP@MISX", Quantity: "20"}, // a split the trades do not describe
		{Symbol: "MOEX@MISX", Quantity: "7"},  // held, but bought before the history starts
		{Symbol: "BAD@MISX", Quantity: "N/A"}, // unreadable: says nothing either way
		{Symbol: "", Quantity: "5"},           // no symbol at all
	})

	if _, ok := diffs["SBER@MISX"]; ok {
		t.Errorf("SBER reported as a discrepancy: %v", diffs["SBER@MISX"])
	}
	if !approx(diffs["GAZP@MISX"], 15) {
		t.Errorf("GAZP diff = %v, want +15 (the broker holds more than the history explains)", diffs["GAZP@MISX"])
	}
	if !approx(diffs["MOEX@MISX"], 7) {
		t.Errorf("MOEX diff = %v, want +7", diffs["MOEX@MISX"])
	}
	if _, ok := diffs["BAD@MISX"]; ok {
		t.Error("an unreadable quantity was reported as a discrepancy")
	}
	// LKOH is open in the history but absent from the broker's positions.
	if !approx(diffs["LKOH@MISX"], -2) {
		t.Errorf("LKOH diff = %v, want -2 (the history holds what the broker does not report)", diffs["LKOH@MISX"])
	}
	if len(diffs) != 3 {
		t.Errorf("diffs = %v, want exactly three disagreements", diffs)
	}
}

// TestReconcile_Empty answers an empty map rather than nil, so callers can range
// over it without a check.
func TestReconcile_Empty(t *testing.T) {
	diffs := Reconcile(MatchFIFO(nil), nil)
	if diffs == nil {
		t.Fatal("Reconcile returned nil")
	}
	if len(diffs) != 0 {
		t.Errorf("diffs = %v, want empty", diffs)
	}
}
