package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	"finam-terminal/api"
	"finam-terminal/models"

	"github.com/gdamore/tcell/v2"
)

// tradesBundle is a small history with two instruments: one closed at a
// profit, one at a loss, and a lot still open.
func tradesBundle() *api.HistoryBundle {
	base := time.Now().Add(-10 * 24 * time.Hour)
	tr := func(id, symbol, name, side string, hours int, qty, price string) models.Trade {
		return models.Trade{
			ID: id, Symbol: symbol, Name: name, Side: side,
			Quantity: qty, Price: price, Currency: "RUB",
			Timestamp: base.Add(time.Duration(hours) * time.Hour),
		}
	}
	return &api.HistoryBundle{
		Trades: []models.Trade{
			tr("1", "SBER@MISX", "Сбербанк", "Buy", 0, "20", "100"),
			tr("2", "SBER@MISX", "Сбербанк", "Sell", 1, "10", "120"),
			tr("3", "GAZP@MISX", "Газпром", "Buy", 2, "5", "200"),
			tr("4", "GAZP@MISX", "Газпром", "Sell", 3, "5", "180"),
		},
		Boundary: base.Add(-24 * time.Hour),
		Complete: true,
	}
}

// tradesApp puts an app on the Trades sub-screen with a ready cache, so the
// renderer can be exercised without a load.
func tradesApp(t *testing.T, bundle *api.HistoryBundle) (*App, func(*tcell.EventKey) *tcell.EventKey, *mockClient) {
	t.Helper()

	mock := &mockClient{
		LoadHistoryFunc: func(_ context.Context, req api.HistoryRequest) (*api.HistoryBundle, error) {
			return bundle, nil
		},
	}
	app := NewApp(mock, []models.AccountInfo{{
		ID: "acc1", FirstTradeDate: time.Now().AddDate(-1, 0, 0),
	}})
	setupInputHandlers(app)
	app.portfolioView.TabbedView.SetTab(TabAnalytics)
	t.Cleanup(app.Stop)

	capture := app.app.GetInputCapture()
	capture(tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModNone))
	waitHistory(t, app, mock, 1)

	updateAnalyticsHistoryScreens(app)
	return app, capture, mock
}

// TestTradesScreen_Stats renders the headline block from the cache.
func TestTradesScreen_Stats(t *testing.T) {
	app, _, _ := tradesApp(t, tradesBundle())

	text := app.analyticsView().TradeStats.GetText(true)
	for _, want := range []string{"закрытых", "прибыльных", "профит-фактор", "оборот", "RUB"} {
		if !strings.Contains(strings.ToLower(text), strings.ToLower(want)) {
			t.Errorf("stats block %q does not mention %q", text, want)
		}
	}

	// Two closed trades, one up 200 and one down 100: a net of 100.
	if !strings.Contains(text, "100") {
		t.Errorf("stats block %q does not show the net result", text)
	}
}

// TestTradesScreen_Table renders one row per instrument, with the header
// pinned above them.
func TestTradesScreen_Table(t *testing.T) {
	app, _, _ := tradesApp(t, tradesBundle())

	table := app.analyticsView().TradesTable
	if got := table.GetRowCount(); got != 3 {
		t.Fatalf("table has %d rows, want a header and two instruments", got)
	}

	header := table.GetCell(0, 0).Text
	if !strings.Contains(header, "Тикер") {
		t.Errorf("header cell = %q, want the ticker column", header)
	}

	// Best result first.
	if got := table.GetCell(1, 0).Text; got != "SBER" {
		t.Errorf("first row ticker = %q, want SBER", got)
	}
	if got := table.GetCell(2, 0).Text; got != "GAZP" {
		t.Errorf("second row ticker = %q, want GAZP", got)
	}

	// The name column carries the human-readable name from the trades.
	if got := table.GetCell(1, 1).Text; !strings.Contains(got, "Сбербанк") {
		t.Errorf("first row name = %q, want Сбербанк", got)
	}
}

// TestTradesScreen_ColumnsExpandOnEveryCell is the lesson the Index tab
// learned: tview sizes columns from the rows currently visible, so expansion
// living only on the header collapses the table once the header scrolls away.
func TestTradesScreen_ColumnsExpandOnEveryCell(t *testing.T) {
	app, _, _ := tradesApp(t, tradesBundle())

	table := app.analyticsView().TradesTable
	for row := range table.GetRowCount() {
		for col := range table.GetColumnCount() {
			cell := table.GetCell(row, col)
			if cell == nil {
				t.Fatalf("cell (%d,%d) is missing", row, col)
			}
			if cell.Expansion != tradeColumnExpansion[col] {
				t.Errorf("cell (%d,%d) expansion = %d, want %d",
					row, col, cell.Expansion, tradeColumnExpansion[col])
			}
		}
	}
}

// TestTradesScreen_OpenPositionShown: an instrument still held reports the
// position alongside the realised result.
func TestTradesScreen_OpenPositionShown(t *testing.T) {
	app, _, _ := tradesApp(t, tradesBundle())

	table := app.analyticsView().TradesTable
	var sberRow int
	for row := 1; row < table.GetRowCount(); row++ {
		if table.GetCell(row, 0).Text == "SBER" {
			sberRow = row
		}
	}
	if sberRow == 0 {
		t.Fatal("SBER is missing from the table")
	}

	row := ""
	for col := range table.GetColumnCount() {
		row += table.GetCell(sberRow, col).Text + " "
	}
	if !strings.Contains(row, "10") {
		t.Errorf("SBER row %q does not show the 10 shares still held", row)
	}
}

// TestTradesScreen_EmptyHistory says so rather than drawing an empty table
// with no explanation.
func TestTradesScreen_EmptyHistory(t *testing.T) {
	app, _, _ := tradesApp(t, &api.HistoryBundle{Complete: true, Boundary: time.Now().AddDate(-1, 0, 0)})

	if got := app.analyticsView().TradesTable.GetRowCount(); got > 1 {
		t.Errorf("table has %d rows for an account with no trades, want at most a header", got)
	}
	if text := app.analyticsView().TradeStats.GetText(true); !strings.Contains(text, "нет") {
		t.Errorf("stats block %q does not say there are no trades", text)
	}
}

// TestTradesScreen_PeriodFilters: shortening the window drops the trades that
// closed outside it, without a request.
func TestTradesScreen_PeriodFilters(t *testing.T) {
	base := time.Now()
	bundle := &api.HistoryBundle{
		Trades: []models.Trade{
			{ID: "1", Symbol: "SBER@MISX", Side: "Buy", Quantity: "1", Price: "100", Currency: "RUB", Timestamp: base.AddDate(0, 0, -200)},
			{ID: "2", Symbol: "SBER@MISX", Side: "Sell", Quantity: "1", Price: "120", Currency: "RUB", Timestamp: base.AddDate(0, 0, -199)},
			{ID: "3", Symbol: "GAZP@MISX", Side: "Buy", Quantity: "1", Price: "200", Currency: "RUB", Timestamp: base.AddDate(0, 0, -5)},
			{ID: "4", Symbol: "GAZP@MISX", Side: "Sell", Quantity: "1", Price: "180", Currency: "RUB", Timestamp: base.AddDate(0, 0, -4)},
		},
		Complete: true,
		Boundary: base.AddDate(-1, 0, 0),
	}

	app, capture, mock := tradesApp(t, bundle)

	// The default window is three months, so only the recent pair counts.
	if got := app.analyticsView().TradesTable.GetRowCount(); got != 2 {
		t.Errorf("table has %d rows under the default window, want a header and GAZP", got)
	}

	// Step to a year, which takes both in.
	capture(tcell.NewEventKey(tcell.KeyRune, 'P', tcell.ModNone))
	if got := app.analyticsView().TradesTable.GetRowCount(); got != 3 {
		t.Errorf("table has %d rows over a year, want a header and both instruments", got)
	}
	if got := mock.LoadHistoryCalls.Load(); got != 1 {
		t.Errorf("LoadHistory called %d times while changing the period, want 1", got)
	}
}

// TestTradesScreen_UnmatchedIsMarked warns that an instrument's result is
// partial rather than quietly understating it.
func TestTradesScreen_UnmatchedIsMarked(t *testing.T) {
	base := time.Now().Add(-5 * 24 * time.Hour)
	app, _, _ := tradesApp(t, &api.HistoryBundle{
		Trades: []models.Trade{
			{ID: "1", Symbol: "LKOH@MISX", Side: "Sell", Quantity: "7", Price: "5000", Currency: "RUB", Timestamp: base},
		},
		Complete: true,
		Boundary: base.Add(-24 * time.Hour),
	})

	text := app.analyticsView().TradeStats.GetText(true)
	if !strings.Contains(text, "без цены входа") {
		t.Errorf("stats block %q does not warn about sales with no entry price", text)
	}
}

// TestTradesScreen_EnterOpensProfile from the highlighted row.
func TestTradesScreen_EnterOpensProfile(t *testing.T) {
	app, _, _ := tradesApp(t, tradesBundle())

	table := app.analyticsView().TradesTable
	table.Select(1, 0)

	if got := app.selectedTradeSymbol(); got != "SBER@MISX" {
		t.Fatalf("selected symbol = %q, want SBER@MISX", got)
	}

	table.Select(2, 0)
	if got := app.selectedTradeSymbol(); got != "GAZP@MISX" {
		t.Errorf("selected symbol = %q, want GAZP@MISX", got)
	}

	// The header row and a stale selection resolve to nothing rather than to
	// the first instrument.
	table.Select(0, 0)
	if got := app.selectedTradeSymbol(); got != "" {
		t.Errorf("selected symbol on the header row = %q, want empty", got)
	}
	table.Select(99, 0)
	if got := app.selectedTradeSymbol(); got != "" {
		t.Errorf("selected symbol on a stale row = %q, want empty", got)
	}
}

// TestTradesScreen_EnterAndOrderKeys drive the two row actions through the real
// input handler.
func TestTradesScreen_EnterAndOrderKeys(t *testing.T) {
	app, _, _ := tradesApp(t, tradesBundle())

	table := app.analyticsView().TradesTable
	table.Select(1, 0)
	app.app.SetFocus(table)

	capture := table.GetInputCapture()
	if capture == nil {
		t.Fatal("the trades table has no input handler; Enter and A would not reach it")
	}

	if got := capture(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)); got != nil {
		t.Error("Enter was not handled by the trades table")
	}
	if !app.profileOpen {
		t.Error("Enter did not open the instrument profile")
	}
	app.CloseProfile()

	table.Select(1, 0)
	if got := capture(tcell.NewEventKey(tcell.KeyRune, 'A', tcell.ModNone)); got != nil {
		t.Error("A was not handled by the trades table")
	}
	// The pages stack is only populated once the UI is built, so the modal
	// being shown is observed through the instrument it was handed.
	if got := app.orderModal.GetInstrument(); got != "SBER@MISX" {
		t.Errorf("order modal instrument = %q, want the selected row's symbol", got)
	}
}
