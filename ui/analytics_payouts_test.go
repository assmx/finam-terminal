package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"finam-terminal/models"

	"github.com/gdamore/tcell/v2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func payoutDate(days int) time.Time {
	return time.Now().AddDate(0, 0, days)
}

func futureDiv(days int, amount string) models.Dividend {
	d := payoutDate(days)
	return models.Dividend{
		Date: d.Format("2006-01-02"), When: d,
		Amount: amount, Currency: "RUB", IsFuture: true,
	}
}

func futureCoupon(days int, value string) models.BondEvent {
	d := payoutDate(days)
	return models.BondEvent{
		Date: d.Format("2006-01-02"), When: d,
		Kind: models.BondEventCoupon, Value: value, Currency: "RUB", IsFuture: true,
	}
}

// payoutMock answers the type lookup and both calendars.
func payoutMock() *mockClient {
	return &mockClient{
		GetInstrumentTypeFunc: func(symbol string) string {
			switch {
			case strings.HasPrefix(symbol, "SU"):
				return "BONDS"
			case strings.HasPrefix(symbol, "FUT"):
				return "FUTURES"
			default:
				return "EQUITIES"
			}
		},
		GetDividendsFunc: func(string) ([]models.Dividend, error) {
			return []models.Dividend{futureDiv(20, "34.50")}, nil
		},
		GetBondEventsFunc: func(string) ([]models.BondEvent, error) {
			return []models.BondEvent{futureCoupon(40, "34.90")}, nil
		},
	}
}

// payoutApp opens the Payouts screen with the given positions.
func payoutApp(t *testing.T, mock *mockClient, positions []models.Position) (*App, func(*tcell.EventKey) *tcell.EventKey) {
	t.Helper()

	app := NewApp(mock, []models.AccountInfo{{ID: "acc1"}})
	setupInputHandlers(app)
	app.portfolioView.TabbedView.SetTab(TabAnalytics)
	t.Cleanup(app.Stop)

	app.dataMutex.Lock()
	app.positions["acc1"] = positions
	app.dataMutex.Unlock()

	capture := app.app.GetInputCapture()
	capture(tcell.NewEventKey(tcell.KeyRune, '4', tcell.ModNone))

	return app, capture
}

// waitPayouts waits for the payout load to settle.
func waitPayouts(t *testing.T, app *App) {
	t.Helper()

	if !waitFor(func() bool {
		app.dataMutex.RLock()
		defer app.dataMutex.RUnlock()
		data := app.analytics.byAccount["acc1"]
		return data != nil && !data.payoutsLoading && !data.payoutsAt.IsZero()
	}) {
		t.Fatal("the payout load never settled")
	}
	updatePayoutScreen(app)
}

func equityAndBond() []models.Position {
	return []models.Position{
		{Symbol: "SBER@MISX", Ticker: "SBER", Name: "Сбербанк", Quantity: "100"},
		{Symbol: "SU26238@TQOB", Ticker: "SU26238", Name: "ОФЗ 26238", Quantity: "10"},
	}
}

// TestPayouts_LoadsPerInstrumentType asks each position only for the calendar
// its type has: dividends for an equity, events for a bond, nothing at all for
// anything else.
func TestPayouts_LoadsPerInstrumentType(t *testing.T) {
	mock := payoutMock()
	positions := append(equityAndBond(),
		models.Position{Symbol: "FUT-SI@RTSX", Ticker: "FUT-SI", Quantity: "1"})

	app, _ := payoutApp(t, mock, positions)
	waitPayouts(t, app)

	if got := mock.GetDividendsCalls.Load(); got != 1 {
		t.Errorf("GetDividends called %d times, want 1 (the equity only)", got)
	}
	if got := mock.GetBondEventsCalls.Load(); got != 1 {
		t.Errorf("GetBondEvents called %d times, want 1 (the bond only)", got)
	}
	if got := mock.GetSplitsCalls.Load(); got != 0 {
		t.Errorf("GetSplits called %d times, want 0 — splits are not payouts", got)
	}
}

// TestPayouts_SkipsShorts: a short owes its payouts, so it is not asked about.
func TestPayouts_SkipsShorts(t *testing.T) {
	mock := payoutMock()
	app, _ := payoutApp(t, mock, []models.Position{
		{Symbol: "SBER@MISX", Ticker: "SBER", Quantity: "-100"},
	})
	waitPayouts(t, app)

	if got := mock.GetDividendsCalls.Load(); got != 0 {
		t.Errorf("GetDividends called %d times for a short position, want 0", got)
	}
}

// TestPayouts_UnknownTypeIsNotAsked: an instrument missing from the asset
// cache has no known calendar, and guessing would cost a request per refresh.
func TestPayouts_UnknownTypeIsNotAsked(t *testing.T) {
	mock := payoutMock()
	mock.GetInstrumentTypeFunc = func(string) string { return "" }

	app, _ := payoutApp(t, mock, equityAndBond())
	waitPayouts(t, app)

	if got := mock.GetDividendsCalls.Load() + mock.GetBondEventsCalls.Load(); got != 0 {
		t.Errorf("%d calendar calls for instruments of unknown type, want 0", got)
	}
}

// TestPayouts_RendersTableAndTotals.
func TestPayouts_RendersTableAndTotals(t *testing.T) {
	app, _ := payoutApp(t, payoutMock(), equityAndBond())
	waitPayouts(t, app)

	table := app.analyticsView().PayoutTable
	if got := table.GetRowCount(); got != 3 {
		t.Fatalf("table has %d rows, want a header and two payouts", got)
	}
	// Nearest date first: the dividend at 20 days before the coupon at 40.
	if got := table.GetCell(1, 1).Text; got != "SBER" {
		t.Errorf("first row ticker = %q, want SBER", got)
	}

	totals := app.analyticsView().PayoutTotals.GetText(true)
	// 34.50 × 100 inside 30 days; the coupon's 34.90 × 10 joins only the 90-day
	// total.
	if !strings.Contains(totals, "3 450") {
		t.Errorf("totals %q do not show the 30-day sum", totals)
	}
	if !strings.Contains(totals, "3 799") {
		t.Errorf("totals %q do not show the 90-day sum", totals)
	}
	if !strings.Contains(totals, "до налога") {
		t.Errorf("totals %q do not say the amounts are gross", totals)
	}
}

// TestPayouts_ColumnsExpandOnEveryCell, the same tview lesson as elsewhere.
func TestPayouts_ColumnsExpandOnEveryCell(t *testing.T) {
	app, _ := payoutApp(t, payoutMock(), equityAndBond())
	waitPayouts(t, app)

	table := app.analyticsView().PayoutTable
	for row := range table.GetRowCount() {
		for col := range table.GetColumnCount() {
			cell := table.GetCell(row, col)
			if cell == nil {
				t.Fatalf("cell (%d,%d) is missing", row, col)
			}
			if cell.Expansion != payoutColumnExpansion[col] {
				t.Errorf("cell (%d,%d) expansion = %d, want %d",
					row, col, cell.Expansion, payoutColumnExpansion[col])
			}
		}
	}
}

// TestPayouts_ReentryCostsNothing: the cache holds for the session, and the
// calendars behind it hold for a day.
func TestPayouts_ReentryCostsNothing(t *testing.T) {
	mock := payoutMock()
	app, capture := payoutApp(t, mock, equityAndBond())
	waitPayouts(t, app)

	for range 5 {
		capture(tcell.NewEventKey(tcell.KeyRune, '1', tcell.ModNone))
		capture(tcell.NewEventKey(tcell.KeyRune, '4', tcell.ModNone))
	}

	if got := mock.GetDividendsCalls.Load(); got != 1 {
		t.Errorf("GetDividends called %d times across repeat visits, want 1", got)
	}
}

// TestPayouts_SymbolErrorIsTolerated: one instrument failing must not cost the
// rest of the list.
func TestPayouts_SymbolErrorIsTolerated(t *testing.T) {
	mock := payoutMock()
	mock.GetDividendsFunc = func(string) ([]models.Dividend, error) {
		return nil, errors.New("temporary failure")
	}

	app, _ := payoutApp(t, mock, equityAndBond())
	waitPayouts(t, app)

	if got := mock.GetBondEventsCalls.Load(); got != 1 {
		t.Errorf("GetBondEvents called %d times after the equity failed, want 1", got)
	}
	if got := app.analyticsView().PayoutTable.GetRowCount(); got < 2 {
		t.Errorf("table has %d rows, want the bond's coupon still listed", got)
	}

	status := app.analyticsView().PayoutStatus.GetText(true)
	if !strings.Contains(status, "SBER") {
		t.Errorf("status %q does not name the instrument with no data", status)
	}
}

// TestPayouts_RateLimitStopsThePass: a refusal ends the walk instead of
// hammering the remaining positions.
func TestPayouts_RateLimitStopsThePass(t *testing.T) {
	mock := payoutMock()
	mock.GetDividendsFunc = func(string) ([]models.Dividend, error) {
		return nil, status.Error(codes.ResourceExhausted, "quota exceeded")
	}

	positions := []models.Position{
		{Symbol: "SBER@MISX", Ticker: "SBER", Quantity: "100"},
		{Symbol: "GAZP@MISX", Ticker: "GAZP", Quantity: "100"},
		{Symbol: "LKOH@MISX", Ticker: "LKOH", Quantity: "100"},
	}

	app, _ := payoutApp(t, mock, positions)
	waitPayouts(t, app)

	if got := mock.GetDividendsCalls.Load(); got != 1 {
		t.Errorf("GetDividends called %d times after a rate limit, want 1", got)
	}
	if s := app.analyticsView().PayoutStatus.GetText(true); !strings.Contains(s, "лимит") {
		t.Errorf("status %q does not name the rate limit", s)
	}
}

// TestPayouts_NoPositions says so rather than drawing an empty table.
func TestPayouts_NoPositions(t *testing.T) {
	mock := payoutMock()
	app, _ := payoutApp(t, mock, nil)
	waitPayouts(t, app)

	if got := app.analyticsView().PayoutTable.GetRowCount(); got > 1 {
		t.Errorf("table has %d rows with no positions, want at most a header", got)
	}
	if text := app.analyticsView().PayoutTotals.GetText(true); !strings.Contains(text, "нет") {
		t.Errorf("totals %q do not explain the empty list", text)
	}
}

// TestPayouts_PerAccount keeps each account's forecast to itself.
func TestPayouts_PerAccount(t *testing.T) {
	mock := payoutMock()
	app := NewApp(mock, []models.AccountInfo{{ID: "acc1"}, {ID: "acc2"}})
	setupInputHandlers(app)
	app.portfolioView.TabbedView.SetTab(TabAnalytics)
	t.Cleanup(app.Stop)

	app.dataMutex.Lock()
	app.positions["acc1"] = equityAndBond()
	app.positions["acc2"] = []models.Position{{Symbol: "GAZP@MISX", Ticker: "GAZP", Quantity: "50"}}
	app.dataMutex.Unlock()

	capture := app.app.GetInputCapture()
	capture(tcell.NewEventKey(tcell.KeyRune, '4', tcell.ModNone))
	waitPayouts(t, app)

	app.dataMutex.Lock()
	app.selectedIdx = 1
	app.dataMutex.Unlock()
	app.EnterAnalyticsTab()

	if !waitFor(func() bool {
		app.dataMutex.RLock()
		defer app.dataMutex.RUnlock()
		data := app.analytics.byAccount["acc2"]
		return data != nil && !data.payoutsAt.IsZero()
	}) {
		t.Fatal("the second account's payouts were never loaded")
	}

	app.dataMutex.RLock()
	first := app.analytics.byAccount["acc1"]
	second := app.analytics.byAccount["acc2"]
	app.dataMutex.RUnlock()

	if len(first.payouts) == 0 {
		t.Error("the first account's forecast was discarded")
	}
	if len(second.payouts) == 0 {
		t.Error("the second account's forecast was not built")
	}
}

// TestPayouts_EnterAndOrderKeys drive the row actions through the real handler.
func TestPayouts_EnterAndOrderKeys(t *testing.T) {
	app, _ := payoutApp(t, payoutMock(), equityAndBond())
	waitPayouts(t, app)

	table := app.analyticsView().PayoutTable
	table.Select(1, 0)
	app.app.SetFocus(table)

	if got := app.selectedPayoutSymbol(); got != "SBER@MISX" {
		t.Fatalf("selected symbol = %q, want SBER@MISX", got)
	}
	table.Select(0, 0)
	if got := app.selectedPayoutSymbol(); got != "" {
		t.Errorf("selected symbol on the header row = %q, want empty", got)
	}

	table.Select(1, 0)
	capture := table.GetInputCapture()
	if capture == nil {
		t.Fatal("the payout table has no input handler")
	}
	if got := capture(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)); got != nil {
		t.Error("Enter was not handled by the payout table")
	}
	if !app.profileOpen {
		t.Error("Enter did not open the instrument profile")
	}
	app.CloseProfile()

	table.Select(1, 0)
	if got := capture(tcell.NewEventKey(tcell.KeyRune, 'A', tcell.ModNone)); got != nil {
		t.Error("A was not handled by the payout table")
	}
	if got := app.orderModal.GetInstrument(); got != "SBER@MISX" {
		t.Errorf("order modal instrument = %q, want the selected row's symbol", got)
	}
}

// TestPayouts_RefreshReloads: R rebuilds the forecast, and the calendar cache
// behind it keeps the cost at nothing inside a day.
func TestPayouts_RefreshReloads(t *testing.T) {
	mock := payoutMock()
	app, capture := payoutApp(t, mock, equityAndBond())
	waitPayouts(t, app)

	capture(tcell.NewEventKey(tcell.KeyRune, 'R', tcell.ModNone))

	if !waitFor(func() bool { return mock.GetDividendsCalls.Load() >= 2 }) {
		t.Errorf("GetDividends called %d times after R, want a second pass", mock.GetDividendsCalls.Load())
	}
}
