package analytics

import (
	"testing"
	"time"

	"finam-terminal/models"
)

var statsFrom = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
var statsTo = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

// closed builds a closed trade whose exit falls a given number of days into the
// window.
func closed(symbol string, day int, qty, pnl float64, currency string) ClosedTrade {
	exit := statsFrom.AddDate(0, 0, day)
	return ClosedTrade{
		Symbol:     symbol,
		Currency:   currency,
		Qty:        qty,
		EntryPrice: 100,
		ExitPrice:  100 + pnl/qty,
		EntryTime:  exit.Add(-24 * time.Hour),
		ExitTime:   exit,
		PnL:        pnl,
	}
}

// TestStats_Basics computes every headline figure from one small set.
func TestStats_Basics(t *testing.T) {
	trades := []ClosedTrade{
		closed("SBER@MISX", 1, 10, 300, "RUB"),
		closed("GAZP@MISX", 2, 5, -100, "RUB"),
		closed("LKOH@MISX", 3, 1, 500, "RUB"),
		closed("MOEX@MISX", 4, 2, -200, "RUB"),
		closed("MGNT@MISX", 5, 3, 0, "RUB"), // a flat trade
	}

	all := Stats(trades, nil, statsFrom, statsTo, "RUB")
	s, ok := all["RUB"]
	if !ok {
		t.Fatalf("no RUB stats in %v", all)
	}

	if s.ClosedCount != 5 {
		t.Errorf("ClosedCount = %d, want 5", s.ClosedCount)
	}
	if s.Wins != 2 || s.Losses != 2 || s.Zeros != 1 {
		t.Errorf("wins/losses/zeros = %d/%d/%d, want 2/2/1", s.Wins, s.Losses, s.Zeros)
	}
	if !approx(s.GrossProfit, 800) || !approx(s.GrossLoss, -300) {
		t.Errorf("gross = %v / %v, want 800 / -300", s.GrossProfit, s.GrossLoss)
	}
	if !approx(s.Total, 500) {
		t.Errorf("Total = %v, want 500", s.Total)
	}

	// A flat trade is neither a win nor a loss and is left out of the rate.
	if !s.WinRateValid || !approx(s.WinRate, 0.5) {
		t.Errorf("WinRate = %v (valid %v), want 0.5 over the four decided trades", s.WinRate, s.WinRateValid)
	}

	if !s.ProfitFactorValid || s.ProfitFactorInfinite {
		t.Fatalf("profit factor should be a finite number: %+v", s)
	}
	if !approx(s.ProfitFactor, 800.0/300.0) {
		t.Errorf("ProfitFactor = %v, want 800/300", s.ProfitFactor)
	}

	if !approx(s.AverageWin, 400) || !approx(s.AverageLoss, -150) {
		t.Errorf("averages = %v / %v, want 400 / -150", s.AverageWin, s.AverageLoss)
	}
	if !approx(s.Expectancy, 100) {
		t.Errorf("Expectancy = %v, want 100 (500 over 5 closed)", s.Expectancy)
	}

	if !s.BestValid || s.Best.Symbol != "LKOH@MISX" {
		t.Errorf("Best = %+v (valid %v), want LKOH", s.Best, s.BestValid)
	}
	if !s.WorstValid || s.Worst.Symbol != "MOEX@MISX" {
		t.Errorf("Worst = %+v (valid %v), want MOEX", s.Worst, s.WorstValid)
	}
}

// TestStats_ProfitFactorEdges: a run with no losses is "∞", and no trades at
// all is "Н/Д". Neither may reach the screen as a number.
func TestStats_ProfitFactorEdges(t *testing.T) {
	onlyWins := Stats([]ClosedTrade{closed("SBER@MISX", 1, 1, 100, "RUB")}, nil, statsFrom, statsTo, "RUB")["RUB"]
	if !onlyWins.ProfitFactorValid || !onlyWins.ProfitFactorInfinite {
		t.Errorf("a run with no losses: valid=%v infinite=%v, want valid and infinite",
			onlyWins.ProfitFactorValid, onlyWins.ProfitFactorInfinite)
	}

	onlyFlat := Stats([]ClosedTrade{closed("SBER@MISX", 1, 1, 0, "RUB")}, nil, statsFrom, statsTo, "RUB")["RUB"]
	if onlyFlat.ProfitFactorValid {
		t.Error("a run of flat trades reported a profit factor; there is nothing to divide")
	}
	if onlyFlat.WinRateValid {
		t.Error("a run of flat trades reported a win rate; no trade was decided")
	}

	onlyLosses := Stats([]ClosedTrade{closed("SBER@MISX", 1, 1, -100, "RUB")}, nil, statsFrom, statsTo, "RUB")["RUB"]
	if !onlyLosses.ProfitFactorValid || onlyLosses.ProfitFactorInfinite || !approx(onlyLosses.ProfitFactor, 0) {
		t.Errorf("a run with no wins: %+v, want a finite factor of 0", onlyLosses)
	}
}

// TestStats_FiltersByExitTime: the result belongs to the moment the position
// was closed, so the window is applied to the exit.
func TestStats_FiltersByExitTime(t *testing.T) {
	inside := closed("SBER@MISX", 10, 1, 100, "RUB")

	// Entered long before the window, exited inside it: counted.
	early := inside
	early.EntryTime = statsFrom.AddDate(-1, 0, 0)

	after := closed("GAZP@MISX", 200, 1, 999, "RUB") // exit past the window
	before := closed("LKOH@MISX", -10, 1, 999, "RUB")

	s := Stats([]ClosedTrade{early, after, before}, nil, statsFrom, statsTo, "RUB")["RUB"]
	if s.ClosedCount != 1 {
		t.Fatalf("ClosedCount = %d, want 1 — only the trade that exited inside the window", s.ClosedCount)
	}
	if !approx(s.Total, 100) {
		t.Errorf("Total = %v, want 100", s.Total)
	}
}

// TestStats_SeparatesCurrencies never adds two currencies together.
func TestStats_SeparatesCurrencies(t *testing.T) {
	all := Stats([]ClosedTrade{
		closed("SBER@MISX", 1, 1, 100, "RUB"),
		closed("AAPL@XNGS", 2, 1, 50, "USD"),
	}, nil, statsFrom, statsTo, "RUB")

	if len(all) != 2 {
		t.Fatalf("got %d currency groups, want 2: %v", len(all), all)
	}
	if !approx(all["RUB"].Total, 100) || !approx(all["USD"].Total, 50) {
		t.Errorf("totals = RUB %v / USD %v, want 100 / 50", all["RUB"].Total, all["USD"].Total)
	}
	if all["RUB"].Currency != "RUB" || all["USD"].Currency != "USD" {
		t.Error("the currency is not carried on the stats themselves")
	}
}

// TestStats_EmptyCurrencyFallsBackToBase. The API leaves the currency blank on
// at least some trades — the bond fixture carries one and the equities do not —
// so a blank must join the account's base currency rather than forming a
// nameless group of its own.
func TestStats_EmptyCurrencyFallsBackToBase(t *testing.T) {
	all := Stats([]ClosedTrade{
		closed("SBER@MISX", 1, 1, 100, ""),
		closed("GAZP@MISX", 2, 1, 50, "RUB"),
	}, nil, statsFrom, statsTo, "RUB")

	if len(all) != 1 {
		t.Fatalf("got %d currency groups, want 1: %v", len(all), all)
	}
	if !approx(all["RUB"].Total, 150) {
		t.Errorf("RUB total = %v, want 150", all["RUB"].Total)
	}
}

// TestStats_Turnover sums both directions of every raw trade in the window, and
// counts the raw trades separately from the closed pairs.
func TestStats_Turnover(t *testing.T) {
	raw := []struct {
		day   int
		qty   string
		price string
	}{
		{1, "10", "100"},
		{2, "5", "200"},
		{200, "1", "9999"}, // outside the window
	}
	trades := make([]tradeInput, 0, len(raw))
	for i, r := range raw {
		trades = append(trades, tradeInput{
			id: string(rune('a' + i)), symbol: "SBER@MISX", side: "Buy",
			at: statsFrom.AddDate(0, 0, r.day), qty: r.qty, price: r.price, currency: "RUB",
		})
	}

	s := Stats(nil, buildTrades(trades), statsFrom, statsTo, "RUB")["RUB"]
	if s.RawCount != 2 {
		t.Errorf("RawCount = %d, want 2 — the trade outside the window does not count", s.RawCount)
	}
	if !approx(s.Turnover, 10*100+5*200) {
		t.Errorf("Turnover = %v, want 2000", s.Turnover)
	}
}

// TestStats_TurnoverSkipsUnreadable counts what it cannot parse instead of
// treating it as zero.
func TestStats_TurnoverSkipsUnreadable(t *testing.T) {
	s := Stats(nil, buildTrades([]tradeInput{
		{id: "a", symbol: "SBER@MISX", side: "Buy", at: statsFrom.AddDate(0, 0, 1), qty: "N/A", price: "100", currency: "RUB"},
		{id: "b", symbol: "SBER@MISX", side: "Sell", at: statsFrom.AddDate(0, 0, 2), qty: "10", price: "", currency: "RUB"},
		{id: "c", symbol: "SBER@MISX", side: "Buy", at: statsFrom.AddDate(0, 0, 3), qty: "2", price: "50", currency: "RUB"},
	}), statsFrom, statsTo, "RUB")["RUB"]

	if s.Skipped != 2 {
		t.Errorf("Skipped = %d, want 2", s.Skipped)
	}
	if !approx(s.Turnover, 100) {
		t.Errorf("Turnover = %v, want 100 from the one readable trade", s.Turnover)
	}
}

// TestStats_Empty answers an empty map rather than nil, and nothing inside is
// NaN.
func TestStats_Empty(t *testing.T) {
	all := Stats(nil, nil, statsFrom, statsTo, "RUB")
	if all == nil {
		t.Fatal("Stats returned nil")
	}
	if len(all) != 0 {
		t.Errorf("got %v, want no groups for no trades", all)
	}
}

// TestPerInstrument builds the table rows, sorted by result.
func TestPerInstrument(t *testing.T) {
	fifo := MatchFIFO(buildTrades([]tradeInput{
		{id: "1", symbol: "SBER@MISX", side: "Buy", at: statsFrom.AddDate(0, 0, 1), qty: "20", price: "100", currency: "RUB", name: "Сбербанк"},
		{id: "2", symbol: "SBER@MISX", side: "Sell", at: statsFrom.AddDate(0, 0, 2), qty: "10", price: "120", currency: "RUB", name: "Сбербанк"},
		{id: "3", symbol: "GAZP@MISX", side: "Buy", at: statsFrom.AddDate(0, 0, 3), qty: "5", price: "200", currency: "RUB", name: "Газпром"},
		{id: "4", symbol: "GAZP@MISX", side: "Sell", at: statsFrom.AddDate(0, 0, 4), qty: "5", price: "180", currency: "RUB", name: "Газпром"},
	}))

	rows := PerInstrument(fifo, statsFrom, statsTo)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(rows), rows)
	}

	// Best result first.
	if rows[0].Symbol != "SBER@MISX" {
		t.Errorf("rows[0] = %s, want SBER (the profitable one) first", rows[0].Symbol)
	}
	if !approx(rows[0].PnL, 200) {
		t.Errorf("SBER PnL = %v, want 200", rows[0].PnL)
	}
	if rows[0].ClosedCount != 1 {
		t.Errorf("SBER closed count = %d, want 1", rows[0].ClosedCount)
	}
	if !approx(rows[0].OpenQty, 10) {
		t.Errorf("SBER open quantity = %v, want the 10 still held", rows[0].OpenQty)
	}
	if rows[0].Name != "Сбербанк" {
		t.Errorf("SBER name = %q, want the instrument name from the trade", rows[0].Name)
	}
	if !rows[0].WinRateValid || !approx(rows[0].WinRate, 1) {
		t.Errorf("SBER win rate = %v (valid %v), want 1", rows[0].WinRate, rows[0].WinRateValid)
	}

	if !approx(rows[1].PnL, -100) || !approx(rows[1].OpenQty, 0) {
		t.Errorf("GAZP row = %+v, want -100 and a flat position", rows[1])
	}
}

// TestPerInstrument_HoldingWithNoClosures still gets a row: an open position
// with nothing realised in the period is information, not an empty line.
func TestPerInstrument_HoldingWithNoClosures(t *testing.T) {
	fifo := MatchFIFO(buildTrades([]tradeInput{
		{id: "1", symbol: "SBER@MISX", side: "Buy", at: statsFrom.AddDate(0, 0, 1), qty: "10", price: "100", currency: "RUB"},
	}))

	rows := PerInstrument(fifo, statsFrom, statsTo)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].ClosedCount != 0 || !approx(rows[0].PnL, 0) {
		t.Errorf("row = %+v, want no closures and no result", rows[0])
	}
	if !approx(rows[0].OpenQty, 10) {
		t.Errorf("OpenQty = %v, want 10", rows[0].OpenQty)
	}
	if rows[0].WinRateValid {
		t.Error("WinRateValid = true with no decided trades")
	}
}

// TestPerInstrument_Unmatched carries the count of sales with no entry price
// onto the row, so the screen can mark that instrument's result as partial.
func TestPerInstrument_Unmatched(t *testing.T) {
	fifo := MatchFIFO(buildTrades([]tradeInput{
		{id: "1", symbol: "LKOH@MISX", side: "Sell", at: statsFrom.AddDate(0, 0, 1), qty: "7", price: "5000", currency: "RUB"},
	}))

	rows := PerInstrument(fifo, statsFrom, statsTo)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if !approx(rows[0].Unmatched, 7) {
		t.Errorf("Unmatched = %v, want 7", rows[0].Unmatched)
	}
}

// TestPerInstrument_StableOrder: equal results order by symbol, so the table
// does not shuffle between redraws.
func TestPerInstrument_StableOrder(t *testing.T) {
	fifo := MatchFIFO(buildTrades([]tradeInput{
		{id: "1", symbol: "ZZZZ@MISX", side: "Buy", at: statsFrom.AddDate(0, 0, 1), qty: "1", price: "100", currency: "RUB"},
		{id: "2", symbol: "AAAA@MISX", side: "Buy", at: statsFrom.AddDate(0, 0, 2), qty: "1", price: "100", currency: "RUB"},
	}))

	rows := PerInstrument(fifo, statsFrom, statsTo)
	if len(rows) != 2 || rows[0].Symbol != "AAAA@MISX" {
		t.Errorf("rows = %+v, want alphabetical order when results tie", rows)
	}
}

// TestPerInstrument_Empty answers an empty slice.
func TestPerInstrument_Empty(t *testing.T) {
	if rows := PerInstrument(MatchFIFO(nil), statsFrom, statsTo); len(rows) != 0 {
		t.Errorf("rows = %+v, want none", rows)
	}
}

// tradeInput describes one raw trade for the table-driven tests above.
type tradeInput struct {
	id, symbol, side, qty, price, currency, name string
	at                                           time.Time
}

func buildTrades(in []tradeInput) []models.Trade {
	out := make([]models.Trade, 0, len(in))
	for _, t := range in {
		out = append(out, models.Trade{
			ID:        t.id,
			Symbol:    t.symbol,
			Name:      t.name,
			Side:      t.side,
			Quantity:  t.qty,
			Price:     t.price,
			Currency:  t.currency,
			Timestamp: t.at,
		})
	}
	return out
}
