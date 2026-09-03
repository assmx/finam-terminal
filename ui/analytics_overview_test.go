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

func structureText(app *App) string {
	return app.portfolioView.TabbedView.Analytics.Structure.GetText(false)
}

func riskText(app *App) string {
	return app.portfolioView.TabbedView.Analytics.Risk.GetText(false)
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
	for _, want := range []string{"Эквити", "Использование маржи", "Запас до маржин-колла", "Плечо", "Концентрация"} {
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
	if text := riskText(app); !strings.Contains(text, "Ошибка при загрузке данных от брокера") {
		t.Errorf("risk column %q does not show the broker error", text)
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

	if got := mock.GetUsageMetricsCalls.Load(); got != 0 {
		t.Errorf("GetUsageMetrics called %d times during redraws, want 0", got)
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

// TestShareBar covers the clamps. A share is normally 0..1, but the bar is the
// last thing between a bad number and a row that spills across the column, so
// it defends itself.
func TestShareBar(t *testing.T) {
	tests := []struct {
		name  string
		share float64
		want  string
	}{
		{"empty", 0, "░░░░░░░░░░"},
		{"half", 0.5, "█████░░░░░"},
		{"full", 1, "██████████"},
		{"over one is clamped", 1.5, "██████████"},
		{"negative is clamped", -0.3, "░░░░░░░░░░"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shareBar(tt.share); got != tt.want {
				t.Errorf("shareBar(%v) = %q, want %q", tt.share, got, tt.want)
			}
		})
	}
}
