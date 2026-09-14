package ui

import (
	"strings"
	"testing"

	"finam-terminal/models"

	"github.com/gdamore/tcell/v2"
)

// blockedFXRL is the live position of 2026-09-11 as the API layer hands it
// over: sent without a MIC, named after its twin in the bulk list, the broker's
// result zeroed.
func blockedFXRL() models.Position {
	return models.Position{
		Symbol: "FXRL", Ticker: "FXRL", Name: "FinEx Russian RTS Equity MOEX",
		Quantity: "100", AveragePrice: "41.38", CurrentPrice: "21.83",
		DailyPnL: "0.0", UnrealizedPnL: "0.0", Blocked: true,
	}
}

// accountWith is the account …5519: its equity is its cash, nothing else.
func accountWith(cash float64) models.AccountInfo {
	account := models.AccountInfo{ID: "acc1", Equity: "83.05", PortfolioKind: "MC", HasMarginData: true}
	if cash > 0 {
		account.Cash = []models.CashBalance{{Currency: "RUB", Amount: cash}}
	}
	return account
}

// TestPositionsTable_BlockedSaysBlocked: the Value column of a blocked position
// says so instead of an amount the broker does not count; the rest of the row —
// name, prices, the broker's zeroed result — stays as the broker sent it.
func TestPositionsTable_BlockedSaysBlocked(t *testing.T) {
	app := NewApp(&mockClient{}, []models.AccountInfo{{ID: "acc1"}})
	app.positions["acc1"] = []models.Position{
		{Symbol: "SBER@MISX", Ticker: "SBER", Name: "Сбербанк", Quantity: "10", CurrentPrice: "280", LotSize: 1},
		blockedFXRL(),
	}

	updatePositionsTable(app)
	table := app.portfolioView.TabbedView.PositionsTable

	if got := table.GetCell(1, 5).Text; got != "2800.00" {
		t.Errorf("SBER value = %q, want 2800.00", got)
	}
	value := table.GetCell(2, 5)
	if value.Text != "BLOCKED" {
		t.Errorf("FXRL value = %q, want BLOCKED", value.Text)
	}
	if fg, _, _ := value.Style.Decompose(); fg != tcell.ColorYellow {
		t.Errorf("FXRL value colour = %v, want yellow", fg)
	}
	if got := table.GetCell(2, 0).Text; got != "FinEx Russian RTS Equity MOEX" {
		t.Errorf("FXRL name = %q", got)
	}
	if got := table.GetCell(2, 3).Text; got != "21.83" {
		t.Errorf("FXRL current price = %q, want the broker's 21.83", got)
	}
	if got := table.GetCell(2, 6).Text; got != "0.0" {
		t.Errorf("FXRL unrealised = %q, want the broker's 0.0", got)
	}
}

// TestOverview_BlockedLine is …5519 on the overview: the total is the cash —
// the broker's equity — and a line under it says what is left out and what it
// would be worth at the broker's price.
func TestOverview_BlockedLine(t *testing.T) {
	app := overviewApp(typeMock(), accountWith(83.05))
	app.dataMutex.Lock()
	app.positions["acc1"] = []models.Position{blockedFXRL()}
	app.dataMutex.Unlock()

	updateAnalyticsOverview(app)
	text := structureText(app)

	if total := lineWith(text, "Итого"); !strings.Contains(total, "83 RUB") {
		t.Errorf("total line %q, want 83 RUB — the cash, as the broker's equity", total)
	}
	line := lineWith(text, "заблокировано")
	if !strings.Contains(line, "заблокировано: 1") || !strings.Contains(line, "2 183 RUB по цене брокера") {
		t.Errorf("blocked line %q, want the count and 2 183 RUB at the broker's price", line)
	}
	if !strings.Contains(line, "[yellow]") {
		t.Errorf("blocked line %q is not yellow", line)
	}
}

// TestOverview_BlockedLineWithoutBase: an account holding nothing but a blocked
// position has no base, and the line still says why the screen is empty.
func TestOverview_BlockedLineWithoutBase(t *testing.T) {
	app := overviewApp(typeMock(), accountWith(0))
	app.dataMutex.Lock()
	app.positions["acc1"] = []models.Position{blockedFXRL()}
	app.dataMutex.Unlock()

	updateAnalyticsOverview(app)
	text := structureText(app)

	if !strings.Contains(text, "Н/Д") {
		t.Errorf("structure %q, want Н/Д with nothing valued", text)
	}
	if !strings.Contains(text, "заблокировано: 1") {
		t.Errorf("structure %q does not say what is blocked", text)
	}
}

// TestOverview_BlockedLineWithoutValue: with no price to value it at, the line
// keeps the count and names no amount.
func TestOverview_BlockedLineWithoutValue(t *testing.T) {
	p := blockedFXRL()
	p.CurrentPrice = "N/A"
	app := overviewApp(typeMock(), accountWith(83.05))
	app.dataMutex.Lock()
	app.positions["acc1"] = []models.Position{p}
	app.dataMutex.Unlock()

	updateAnalyticsOverview(app)

	line := lineWith(structureText(app), "заблокировано")
	if !strings.Contains(line, "заблокировано: 1") || strings.Contains(line, "по цене брокера") {
		t.Errorf("blocked line %q, want the count alone", line)
	}
}

// TestOverview_BlockedCostsNoRequests: a blocked position on the account does
// not break the overview's budget — twenty redraws, no request of any kind.
func TestOverview_BlockedCostsNoRequests(t *testing.T) {
	mock := typeMock()
	app := overviewApp(mock, mcTestAccount())
	seedPositions(app)
	app.dataMutex.Lock()
	app.positions["acc1"] = append(app.positions["acc1"], blockedFXRL(),
		models.Position{Symbol: "AAPL.SPBZ@_SPBZ", Ticker: "AAPL.SPBZ", Quantity: "3", CurrentPrice: "180", Blocked: true})
	app.dataMutex.Unlock()

	for range 20 {
		updateAnalyticsOverview(app)
	}

	calls := mock.GetQuotesCalls.Load() + mock.GetFXRatesCalls.Load() + mock.GetBondFaceCurrencyCalls.Load() +
		mock.GetDividendsCalls.Load() + mock.GetBondEventsCalls.Load() + mock.GetIndexConstituentsCalls.Load()
	if calls != 0 {
		t.Errorf("%d requests during 20 redraws, want 0", calls)
	}
}

// TestCoveredByStream_IgnoresBlocked: a blocked position never joins the
// subscription (it would silence it), so waiting for it would keep quote
// polling of the whole account on for ever.
func TestCoveredByStream_IgnoresBlocked(t *testing.T) {
	positions := []models.Position{
		{Symbol: "SBER@MISX"},
		{Symbol: "AAPL.SPBZ@_SPBZ", Blocked: true},
	}
	if !coveredByStream(positions, []string{"SBER@MISX"}) {
		t.Error("a blocked position held the stream check back")
	}
}

// TestPayouts_SkipsBlocked: the payout walk asks no calendar for a blocked
// holding — it is in no forecast, so the request would buy nothing.
func TestPayouts_SkipsBlocked(t *testing.T) {
	mock := payoutMock()
	app, _ := payoutApp(t, mock, []models.Position{
		{Symbol: "AAPL.SPBZ@_SPBZ", Ticker: "AAPL.SPBZ", Quantity: "3", Blocked: true},
	})
	waitPayouts(t, app)

	if got := mock.GetDividendsCalls.Load() + mock.GetBondEventsCalls.Load(); got != 0 {
		t.Errorf("%d calendar calls for a blocked holding, want 0", got)
	}
}
