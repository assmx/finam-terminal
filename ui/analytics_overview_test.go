package ui

import (
	"strings"
	"testing"

	"finam-terminal/models"
)

// overviewApp builds an app on the Analytics overview with one account.
func overviewApp(mock *mockClient, account models.AccountInfo) *App {
	app := NewApp(mock, []models.AccountInfo{account})
	app.portfolioView.TabbedView.SetTab(TabAnalytics)
	app.portfolioView.TabbedView.Analytics.SetScreen(AnalyticsOverview)
	return app
}

// structureText and riskText read a whole overview column rather than a single
// panel.
//
// The overview is drawn as five panels sized to their content, but which panel
// a line lands in is a layout decision that may change again; what these tests
// are about is whether the column says the thing at all. Keeping the helpers
// column-shaped keeps the assertions at that level.
func structureText(app *App) string {
	view := app.portfolioView.TabbedView.Analytics
	return panelText(view.Structure) + panelText(view.Sectors)
}

func riskText(app *App) string {
	view := app.portfolioView.TabbedView.Analytics
	return panelText(view.Risk) + panelText(view.Concentration) + panelText(view.SinceOpen)
}

// valuationText reads the headline panel on its own: what it shows is specific
// enough to be asserted row by row.
func valuationText(app *App) string {
	return panelText(app.portfolioView.TabbedView.Analytics.Valuation)
}

// lineWith returns the first line of text that contains marker, so a test can
// ask what a particular row says rather than whether a figure appears anywhere.
func lineWith(text, marker string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, marker) {
			return line
		}
	}
	return ""
}

// panelText is a panel's title and body together. The titles carry the section
// names that used to be headings inside the text, so a test asking whether the
// column names a block has to look at both.
func panelText(panel *analyticsPanel) string {
	return panel.GetTitle() + "\n" + panel.GetText(false) + "\n"
}

func mcTestAccount() models.AccountInfo {
	return models.AccountInfo{
		ID:                "acc1",
		Equity:            "500000",
		PortfolioKind:     "MC",
		HasMarginData:     true,
		AvailableCash:     120000,
		InitialMargin:     100000,
		MaintenanceMargin: 50000,
		Cash:              []models.CashBalance{{Currency: "RUB", Amount: 120000}},
	}
}

func seedPositions(app *App) {
	app.dataMutex.Lock()
	app.positions["acc1"] = []models.Position{
		{Symbol: "SBER@MISX", Ticker: "SBER", Name: "Сбер Банк", Quantity: "100", CurrentPrice: "280"},
		{Symbol: "GAZP@MISX", Ticker: "GAZP", Name: "Газпром", Quantity: "50", CurrentPrice: "160"},
	}
	app.quotes["acc1"] = map[string]*models.Quote{
		"SBER@MISX": {Symbol: "SBER@MISX", Last: "285"},
	}
	app.dataMutex.Unlock()
}

func typeMock() *mockClient {
	return &mockClient{
		GetInstrumentTypeFunc: func(symbol string) string {
			switch symbol {
			case "SBER@MISX", "SBER", "GAZP@MISX", "GAZP":
				return "EQUITIES"
			}
			return ""
		},
	}
}

// TestOverview_RendersStructure checks the left column: type rows with values
// and shares, plus the cash line.
func TestOverview_RendersStructure(t *testing.T) {
	app := overviewApp(typeMock(), mcTestAccount())
	seedPositions(app)

	updateAnalyticsOverview(app)

	text := structureText(app)
	for _, want := range []string{"Акции", "Кэш", "%"} {
		if !strings.Contains(text, want) {
			t.Errorf("structure column %q does not contain %q", text, want)
		}
	}
	// 100*285 + 50*160 = 36500 in equities, plus 120000 cash.
	if !strings.Contains(text, "36 500") {
		t.Errorf("structure column %q does not show the equity total", text)
	}
	if !strings.Contains(text, "120 000") {
		t.Errorf("structure column %q does not show the cash line", text)
	}
}

// TestOverview_RendersRisk checks the right column: the reported margin values
// and the three derived figures.
func TestOverview_RendersRisk(t *testing.T) {
	app := overviewApp(typeMock(), mcTestAccount())
	seedPositions(app)

	updateAnalyticsOverview(app)

	text := riskText(app)
	for _, want := range []string{"Использование маржи", "Запас до маржин-колла", "Плечо", "Концентрация"} {
		if !strings.Contains(text, want) {
			t.Errorf("risk column %q does not contain %q", text, want)
		}
	}
	// 100000 / 500000 = 20%
	if !strings.Contains(text, "20.0%") {
		t.Errorf("risk column %q does not show margin utilisation", text)
	}
	// (500000 - 50000) / 500000 = 90%
	if !strings.Contains(text, "90.0%") {
		t.Errorf("risk column %q does not show the cushion", text)
	}
}

// TestOverview_TopHoldings shows the concentration block.
func TestOverview_TopHoldings(t *testing.T) {
	app := overviewApp(typeMock(), mcTestAccount())
	seedPositions(app)

	updateAnalyticsOverview(app)

	text := riskText(app)
	if !strings.Contains(text, "SBER") {
		t.Errorf("risk column %q does not list the largest holding", text)
	}
	if !strings.Contains(text, "Позиций: 2") {
		t.Errorf("risk column %q does not show the position count", text)
	}
}

// TestOverview_FORTSShowsNotAvailable covers a derivatives account: margin use
// is real, and the two figures FORTS cannot report say so instead of showing
// a zero that would read as a measurement.
func TestOverview_FORTSShowsNotAvailable(t *testing.T) {
	app := overviewApp(typeMock(), models.AccountInfo{
		ID:            "acc1",
		Equity:        "100000",
		PortfolioKind: "FORTS",
		HasMarginData: true,
		AvailableCash: 75000,
		MoneyReserved: 25000,
	})
	seedPositions(app)

	updateAnalyticsOverview(app)

	text := riskText(app)
	if !strings.Contains(text, "25.0%") {
		t.Errorf("risk column %q does not show margin utilisation for FORTS", text)
	}
	if strings.Count(text, "Н/Д") < 2 {
		t.Errorf("risk column %q should mark both the cushion and leverage as Н/Д", text)
	}
}

// TestOverview_MCTShowsNotAvailable covers the portfolio kind that reports no
// numbers at all.
func TestOverview_MCTShowsNotAvailable(t *testing.T) {
	app := overviewApp(typeMock(), models.AccountInfo{
		ID:            "acc1",
		Equity:        "100000",
		PortfolioKind: "MCT",
	})
	seedPositions(app)

	updateAnalyticsOverview(app)

	text := riskText(app)
	if strings.Count(text, "Н/Д") < 3 {
		t.Errorf("risk column %q should mark all three derived figures as Н/Д", text)
	}
}

// TestOverview_ZeroEquityIsNotAvailable is the divide-by-zero case reaching
// the screen.
func TestOverview_ZeroEquityIsNotAvailable(t *testing.T) {
	app := overviewApp(typeMock(), models.AccountInfo{
		ID:            "acc1",
		Equity:        "0",
		PortfolioKind: "MC",
		HasMarginData: true,
	})
	seedPositions(app)

	updateAnalyticsOverview(app)

	text := riskText(app)
	if strings.Count(text, "Н/Д") < 3 {
		t.Errorf("risk column %q should mark all derived figures as Н/Д with no equity", text)
	}
	for _, bad := range []string{"NaN", "Inf", "+Inf"} {
		if strings.Contains(text, bad) {
			t.Errorf("risk column %q contains %q", text, bad)
		}
	}
}

// TestOverview_SectorsUnavailable tells the user the sector line is missing
// because the composition did not load, rather than showing an all-Прочее
// breakdown that looks like real data.
func TestOverview_SectorsUnavailable(t *testing.T) {
	app := overviewApp(typeMock(), mcTestAccount())
	seedPositions(app)

	app.dataMutex.Lock()
	app.indexLoadErr = "boom"
	app.dataMutex.Unlock()

	updateAnalyticsOverview(app)

	if text := structureText(app); !strings.Contains(text, "состав индекса недоступен") {
		t.Errorf("structure column %q does not explain the missing sectors", text)
	}
}

// TestOverview_SectorsFromComposition groups by sector once the composition is
// there.
func TestOverview_SectorsFromComposition(t *testing.T) {
	app := overviewApp(typeMock(), mcTestAccount())
	seedPositions(app)

	app.dataMutex.Lock()
	app.indexLoaded = true
	app.indexConstituents = []models.IndexConstituent{
		{Symbol: "SBER@MISX", Ticker: "SBER", Sector: "Финансы"},
		{Symbol: "GAZP@MISX", Ticker: "GAZP", Sector: "Нефть и газ"},
	}
	app.dataMutex.Unlock()

	updateAnalyticsOverview(app)

	text := structureText(app)
	for _, want := range []string{"Финансы", "Нефть и газ"} {
		if !strings.Contains(text, want) {
			t.Errorf("structure column %q does not show sector %q", text, want)
		}
	}
}

// TestOverview_UnpricedPositionCounted keeps a position nothing can value
// visible instead of silently dropping it.
func TestOverview_UnpricedPositionCounted(t *testing.T) {
	app := overviewApp(typeMock(), mcTestAccount())
	app.dataMutex.Lock()
	app.positions["acc1"] = []models.Position{
		{Symbol: "SBER@MISX", Ticker: "SBER", Quantity: "100", CurrentPrice: "280"},
		{Symbol: "GHOST@MISX", Ticker: "GHOST", Quantity: "5", CurrentPrice: "N/A"},
	}
	app.dataMutex.Unlock()

	updateAnalyticsOverview(app)

	if text := structureText(app); !strings.Contains(text, "без цены: 1") {
		t.Errorf("structure column %q does not report the unpriced position", text)
	}
}

// TestOverview_LoadErrorShowsMessage covers an account the broker refused.
func TestOverview_LoadErrorShowsMessage(t *testing.T) {
	app := overviewApp(typeMock(), models.AccountInfo{ID: "acc1", LoadError: "permission denied"})

	updateAnalyticsOverview(app)

	if text := structureText(app); !strings.Contains(text, "Ошибка при загрузке данных от брокера") {
		t.Errorf("structure column %q does not show the broker error", text)
	}
	if text := valuationText(app); !strings.Contains(text, "Ошибка при загрузке данных от брокера") {
		t.Errorf("the top of the right column %q does not show the broker error", text)
	}
}

// TestOverview_RendersValuation checks the headline block against the broker's
// own terminal: 67 629.18 now and +43.26 today put the start of the day at
// 67 585.92 and the day at +0.06%; −3 041.57 on 70 000 of cost is −4.35%.
func TestOverview_RendersValuation(t *testing.T) {
	app := overviewApp(typeMock(), models.AccountInfo{
		ID:            "acc1",
		Equity:        "67629.18",
		UnrealizedPnL: "-3041.57",
		PortfolioKind: "MC",
		HasMarginData: true,
	})
	app.dataMutex.Lock()
	app.positions["acc1"] = []models.Position{
		{Symbol: "SBER@MISX", Ticker: "SBER", Quantity: "100", AveragePrice: "400", CurrentPrice: "380", DailyPnL: "40.00"},
		{Symbol: "GAZP@MISX", Ticker: "GAZP", Quantity: "200", AveragePrice: "150", CurrentPrice: "140", DailyPnL: "3.26"},
	}
	app.dataMutex.Unlock()

	updateAnalyticsOverview(app)

	text := valuationText(app)
	rows := []struct{ label, figures string }{
		{"На начало дня", "67 585.92"},
		{"Текущая", "67 629.18"},
		{"Прибыль за день", "+43.26"},
		{"Прибыль за день", "+0.06%"},
		{"Прибыль по позициям", "-3 041.57"},
		{"Прибыль по позициям", "-4.35%"},
	}
	for _, r := range rows {
		if line := lineWith(text, r.label); !strings.Contains(line, r.figures) {
			t.Errorf("row %q = %q, want it to show %q", r.label, line, r.figures)
		}
	}
}

// TestOverview_ValuationPartialDay: the broker leaves daily_pnl empty for a
// FORTS position. The day's figure is shown for what is reported and flagged
// as partial; the opening value, which would be wrong by the missing part, is
// Н/Д.
func TestOverview_ValuationPartialDay(t *testing.T) {
	app := overviewApp(typeMock(), mcTestAccount())
	app.dataMutex.Lock()
	app.positions["acc1"] = []models.Position{
		{Symbol: "SBER@MISX", Ticker: "SBER", Quantity: "100", CurrentPrice: "280", DailyPnL: "120"},
		{Symbol: "SiZ6@RTSX", Ticker: "SiZ6", Quantity: "1", CurrentPrice: "90000", DailyPnL: "N/A"},
	}
	app.dataMutex.Unlock()

	updateAnalyticsOverview(app)

	text := valuationText(app)
	if line := lineWith(text, "Прибыль за день"); !strings.Contains(line, "+120.00") {
		t.Errorf("day row = %q, want the reported +120.00", line)
	}
	if !strings.Contains(text, "без дневного P&L: 1") {
		t.Errorf("valuation %q does not say the day's result is partial", text)
	}
	if line := lineWith(text, "На начало дня"); !strings.Contains(line, "Н/Д") {
		t.Errorf("opening row = %q, want Н/Д while the day is partial", line)
	}
}

// TestOverview_CurrentValueShownOnce: the current value moved from the risk
// panel to the valuation panel, and the same number printed in two panels
// three rows apart would be noise.
func TestOverview_CurrentValueShownOnce(t *testing.T) {
	app := overviewApp(typeMock(), mcTestAccount())
	seedPositions(app)

	updateAnalyticsOverview(app)

	right := valuationText(app) + riskText(app)
	if got := strings.Count(right, "500 000.00"); got != 1 {
		t.Errorf("the current value 500 000.00 appears %d times, want once:\n%s", got, right)
	}
}

// TestOverview_RiskShowsFortsMarginOnUnifiedAccount: on a unified (MC) account
// the collateral held for FORTS positions shows nowhere in the account's own
// report, so it is summed from the positions that carry it.
func TestOverview_RiskShowsFortsMarginOnUnifiedAccount(t *testing.T) {
	app := overviewApp(typeMock(), mcTestAccount())
	app.dataMutex.Lock()
	app.positions["acc1"] = []models.Position{
		{Symbol: "SBER@MISX", Ticker: "SBER", Quantity: "100", CurrentPrice: "280", MaintenanceMargin: "N/A"},
		{Symbol: "SiZ6@RTSX", Ticker: "SiZ6", Quantity: "1", CurrentPrice: "90000", MaintenanceMargin: "15000"},
	}
	app.dataMutex.Unlock()

	updateAnalyticsOverview(app)

	if line := lineWith(riskText(app), "ГО FORTS"); !strings.Contains(line, "15 000.00") {
		t.Errorf("FORTS collateral row = %q, want 15 000.00", line)
	}
}

// TestSignedAmount: a change reads as a change only when a gain says "+".
func TestSignedAmount(t *testing.T) {
	tests := []struct {
		in   float64
		want string
	}{
		{43.26, "[green]+43.26[-]"},
		{-3041.57, "[red]-3 041.57[-]"},
		{0, "[white]0.00[-]"},
	}
	for _, tt := range tests {
		if got := signedAmount(tt.in); got != tt.want {
			t.Errorf("signedAmount(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestSignedPercent keeps two decimals: a day's move is often a fraction of a
// percent, and one decimal would round +0.06% up to +0.1%.
func TestSignedPercent(t *testing.T) {
	tests := []struct {
		in   float64
		want string
	}{
		{43.26 / 67585.92, "+0.06%"},
		{-0.04345, "-4.35%"},
		{0, "0.00%"},
	}
	for _, tt := range tests {
		if got := signedPercent(tt.in); got != tt.want {
			t.Errorf("signedPercent(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestOverview_NoFortsMarginWithoutFortsPositions: with nothing reporting it
// the row is left out rather than claiming a zero the broker never sent.
func TestOverview_NoFortsMarginWithoutFortsPositions(t *testing.T) {
	app := overviewApp(typeMock(), mcTestAccount())
	seedPositions(app)

	updateAnalyticsOverview(app)

	if text := riskText(app); strings.Contains(text, "ГО FORTS") {
		t.Errorf("risk column %q shows FORTS collateral for an account with no FORTS positions", text)
	}
}

// TestOverview_EmptyPortfolio must not divide by zero or panic.
func TestOverview_EmptyPortfolio(t *testing.T) {
	app := overviewApp(typeMock(), models.AccountInfo{ID: "acc1", Equity: "0"})

	updateAnalyticsOverview(app)

	text := structureText(app)
	for _, bad := range []string{"NaN", "Inf"} {
		if strings.Contains(text, bad) {
			t.Errorf("structure column %q contains %q", text, bad)
		}
	}
	if !strings.Contains(text, "Н/Д") {
		t.Errorf("structure column %q should say the shares are unavailable", text)
	}
}

// TestOverview_NoAccounts covers the state before the account list arrives.
func TestOverview_NoAccounts(t *testing.T) {
	app := NewApp(typeMock(), nil)
	app.portfolioView.TabbedView.SetTab(TabAnalytics)

	updateAnalyticsOverview(app)

	if text := structureText(app); text == "" {
		t.Error("structure column is empty with no accounts; it should say so")
	}
}

// TestOverview_CostsNoRequests is the headline budget promise of the track:
// the overview redraws from memory, and a redraw issues nothing.
func TestOverview_CostsNoRequests(t *testing.T) {
	mock := typeMock()
	app := overviewApp(mock, mcTestAccount())
	seedPositions(app)

	app.dataMutex.Lock()
	app.indexLoaded = true
	app.indexConstituents = []models.IndexConstituent{{Symbol: "SBER@MISX", Ticker: "SBER", Sector: "Финансы"}}
	app.dataMutex.Unlock()

	for range 20 {
		updateAnalyticsOverview(app)
	}

	if got := mock.GetIndexConstituentsCalls.Load(); got != 0 {
		t.Errorf("GetIndexConstituents called %d times during redraws, want 0", got)
	}
	if got := mock.GetQuotesCalls.Load(); got != 0 {
		t.Errorf("GetQuotes called %d times during redraws, want 0", got)
	}
}

// TestOverview_AccountSwitchChangesData verifies the overview follows the
// selected account.
func TestOverview_AccountSwitchChangesData(t *testing.T) {
	mock := typeMock()
	app := NewApp(mock, []models.AccountInfo{
		mcTestAccount(),
		{ID: "acc2", Equity: "1000000", PortfolioKind: "FORTS", HasMarginData: true, MoneyReserved: 500000},
	})
	app.portfolioView.TabbedView.SetTab(TabAnalytics)
	seedPositions(app)

	updateAnalyticsOverview(app)
	first := riskText(app)

	app.dataMutex.Lock()
	app.selectedIdx = 1
	app.dataMutex.Unlock()

	updateAnalyticsOverview(app)
	second := riskText(app)

	if first == second {
		t.Error("the overview did not change when the account did")
	}
	if !strings.Contains(second, "50.0%") {
		t.Errorf("second account's risk column %q does not show its own utilisation", second)
	}
}

// TestOverview_TickRedrawsWhenTabIsActive wires the redraw into the data path,
// so a five-second tick refreshes the numbers on screen.
func TestOverview_TickRedrawsWhenTabIsActive(t *testing.T) {
	app := overviewApp(typeMock(), mcTestAccount())

	app.applyAccountData("acc1", []models.Position{
		{Symbol: "SBER@MISX", Ticker: "SBER", Quantity: "10", CurrentPrice: "280"},
	}, map[string]*models.Quote{}, &models.AccountInfo{ID: "acc1", Equity: "500000"})

	if text := structureText(app); !strings.Contains(text, "2 800") {
		t.Errorf("structure column %q was not refreshed by the data tick", text)
	}
}

// TestApplyAccountData_CarriesMarginFields is what makes the tick above
// meaningful: the fresh margin numbers must reach the account the overview
// reads, not just equity.
func TestApplyAccountData_CarriesMarginFields(t *testing.T) {
	app := overviewApp(typeMock(), models.AccountInfo{ID: "acc1"})

	app.applyAccountData("acc1", nil, nil, &models.AccountInfo{
		ID:                "acc1",
		Equity:            "500000",
		PortfolioKind:     "MC",
		HasMarginData:     true,
		AvailableCash:     120000,
		InitialMargin:     100000,
		MaintenanceMargin: 50000,
		Cash:              []models.CashBalance{{Currency: "RUB", Amount: 120000}},
	})

	app.dataMutex.RLock()
	got := app.accounts[0]
	app.dataMutex.RUnlock()

	if got.PortfolioKind != "MC" || !got.HasMarginData {
		t.Errorf("portfolio kind did not reach the account: %+v", got)
	}
	if got.InitialMargin != 100000 || got.MaintenanceMargin != 50000 {
		t.Errorf("margins did not reach the account: %+v", got)
	}
	if len(got.Cash) != 1 {
		t.Errorf("cash did not reach the account: %+v", got.Cash)
	}
}

// TestPositionValueHelper proves the Positions column and the overview compute
// a position's value through the same helper, so the two screens cannot
// disagree about what a holding is worth.
func TestPositionValueHelper(t *testing.T) {
	pos := models.Position{Symbol: "SBER@MISX", Quantity: "100", CurrentPrice: "280"}

	withQuote, ok := positionValue(pos, &models.Quote{Last: "285"})
	if !ok || withQuote != 28500 {
		t.Errorf("positionValue with a quote = %v (ok=%v), want 28500", withQuote, ok)
	}

	withoutQuote, ok := positionValue(pos, nil)
	if !ok || withoutQuote != 28000 {
		t.Errorf("positionValue without a quote = %v (ok=%v), want 28000 from the broker price", withoutQuote, ok)
	}

	_, ok = positionValue(models.Position{Quantity: "100", CurrentPrice: "N/A"}, nil)
	if ok {
		t.Error("positionValue should report failure when nothing can price the position")
	}
}

// TestPositionsValueUsesSharedHelper locks the behaviour change that came with
// sharing the formula: a position whose quote has not arrived is valued from
// the broker's own price rather than left as "N/A", and the Positions column
// and the overview therefore always agree.
func TestPositionsValueUsesSharedHelper(t *testing.T) {
	app := NewApp(typeMock(), []models.AccountInfo{{ID: "acc1"}})
	app.dataMutex.Lock()
	app.positions["acc1"] = []models.Position{
		{Symbol: "SBER@MISX", Ticker: "SBER", Quantity: "100", CurrentPrice: "280", LotSize: 1},
	}
	app.quotes["acc1"] = map[string]*models.Quote{}
	app.dataMutex.Unlock()

	updatePositionsTable(app)

	got := app.portfolioView.TabbedView.PositionsTable.GetCell(1, 5).Text
	if got != "28000.00" {
		t.Errorf("Value cell = %q, want 28000.00 from the broker price", got)
	}
}

// TestOverviewEntry_LoadsCompositionWithoutSubscribing checks the sector data
// is fetched once on entry, and that doing so does not drag the whole index
// into the quote subscription — the stream's symbol set follows the Index tab,
// not this one.
func TestOverviewEntry_LoadsCompositionWithoutSubscribing(t *testing.T) {
	var subscribed [][]string
	mock := typeMock()
	mock.GetIndexConstituentsFunc = func(string) ([]models.IndexConstituent, error) {
		return []models.IndexConstituent{
			{Symbol: "SBER@MISX", Ticker: "SBER", Sector: "Финансы"},
			{Symbol: "LKOH@MISX", Ticker: "LKOH", Sector: "Нефть и газ"},
		}, nil
	}
	mock.SetQuoteSymbolsFunc = func(symbols []string) {
		subscribed = append(subscribed, symbols)
	}

	app := NewApp(mock, []models.AccountInfo{mcTestAccount()})
	setupInputHandlers(app)
	app.portfolioView.TabbedView.SetTab(TabAnalytics)
	app.SetAnalyticsScreen(AnalyticsOverview)

	if !waitFor(func() bool { return mock.GetIndexConstituentsCalls.Load() == 1 }) {
		t.Fatalf("GetIndexConstituents called %d times, want 1", mock.GetIndexConstituentsCalls.Load())
	}

	app.SetAnalyticsScreen(AnalyticsOverview)
	if got := mock.GetIndexConstituentsCalls.Load(); got != 1 {
		t.Errorf("GetIndexConstituents called %d times, want 1: the composition is cached", got)
	}

	for _, set := range subscribed {
		for _, s := range set {
			if s == "LKOH@MISX" {
				t.Errorf("the index composition joined the subscription from the Analytics tab: %v", set)
			}
		}
	}
}
