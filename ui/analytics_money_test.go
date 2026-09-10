package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	"finam-terminal/analytics"
	"finam-terminal/api"
	"finam-terminal/models"

	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/marketdata"
	"github.com/gdamore/tcell/v2"
)

// moneyBundle is a two-year history with a deposit, a withdrawal, income and
// every kind of cost.
func moneyBundle() *api.HistoryBundle {
	base := time.Now()
	tx := func(id, category string, days int, amount float64) models.Transaction {
		return models.Transaction{
			ID: id, Category: category, Amount: amount, Currency: "RUB",
			Timestamp: base.AddDate(0, 0, -days),
		}
	}
	return &api.HistoryBundle{
		Transactions: []models.Transaction{
			tx("d1", "DEPOSIT", 700, 100000),
			tx("d2", "DEPOSIT", 40, 50000),
			tx("w1", "WITHDRAW", 20, -30000),
			tx("i1", "INCOME", 10, 3400),
			tx("c1", "COMMISSION", 9, -150),
			tx("t1", "TAX", 8, -250),
			tx("l1", "LOAN", 7, -37.8),
			tx("f1", "FINE", 6, -10),
		},
		Trades: []models.Trade{
			{ID: "1", Symbol: "SBER@MISX", Side: "Buy", Quantity: "10", Price: "100", Currency: "RUB", Timestamp: base.AddDate(0, 0, -5)},
			{ID: "2", Symbol: "SBER@MISX", Side: "Sell", Quantity: "10", Price: "120", Currency: "RUB", Timestamp: base.AddDate(0, 0, -4)},
		},
		Boundary: base.AddDate(-2, 0, 0),
		Complete: true,
	}
}

// moneyApp opens the Money screen with a ready cache.
func moneyApp(t *testing.T, bundle *api.HistoryBundle, bars func(from time.Time) []models.Bar) (*App, func(*tcell.EventKey) *tcell.EventKey) {
	t.Helper()

	mock := &mockClient{
		LoadHistoryFunc: func(context.Context, api.HistoryRequest) (*api.HistoryBundle, error) {
			return bundle, nil
		},
	}
	if bars != nil {
		mock.GetBarsFunc = func(_ string, _ string, _ marketdata.TimeFrame, from, _ time.Time) ([]models.Bar, error) {
			return bars(from), nil
		}
	}

	app := NewApp(mock, []models.AccountInfo{{
		ID:             "acc1",
		Equity:         "160000",
		FirstTradeDate: time.Now().AddDate(-2, 0, 0),
		Cash:           []models.CashBalance{{Currency: "RUB", Amount: 1000}},
	}})
	setupInputHandlers(app)
	app.portfolioView.TabbedView.SetTab(TabAnalytics)
	t.Cleanup(app.Stop)

	capture := app.app.GetInputCapture()
	capture(tcell.NewEventKey(tcell.KeyRune, '3', tcell.ModNone))
	waitHistory(t, app, mock, 1)
	updateAnalyticsHistoryScreens(app)

	return app, capture
}

// TestMoneyScreen_PeriodColumn shows the four totals and the group table.
func TestMoneyScreen_PeriodColumn(t *testing.T) {
	app, _ := moneyApp(t, moneyBundle(), nil)

	text := app.analyticsView().MoneyPeriod.GetText(true)
	for _, want := range []string{"Издержки", "Выплаты", "Чистый ввод", "Комиссии", "Налоги", "Штрафы"} {
		if !strings.Contains(text, want) {
			t.Errorf("period column %q does not mention %q", text, want)
		}
	}
	// Commission + tax + loan interest + fine = 447.80.
	if !strings.Contains(text, "447") {
		t.Errorf("period column %q does not show the total costs", text)
	}
	// The caveat has to be there: the period result carries no unrealised part.
	if !strings.Contains(text, "без нереализованной") {
		t.Errorf("period column %q does not say what the result leaves out", text)
	}
}

// TestMoneyScreen_SinceOpenColumn shows the whole-life figures.
func TestMoneyScreen_SinceOpenColumn(t *testing.T) {
	app, _ := moneyApp(t, moneyBundle(), nil)

	text := app.analyticsView().MoneySinceOpen.GetText(true)
	for _, want := range []string{"Чистый ввод", "Результат", "Доходность", "XIRR"} {
		if !strings.Contains(text, want) {
			t.Errorf("since-open column %q does not mention %q", text, want)
		}
	}
	// Deposits 150000 less a 30000 withdrawal.
	if !strings.Contains(text, "120 000") {
		t.Errorf("since-open column %q does not show the net deposit", text)
	}
}

// TestMoneyScreen_TradeTransactionsExcluded: a transaction mirroring a deal is
// reported in its own group and never in the money totals.
func TestMoneyScreen_TradeTransactionsExcluded(t *testing.T) {
	bundle := moneyBundle()
	purchase := models.Transaction{
		ID: "p1", Category: "OTHERS", Amount: -1000, Currency: "RUB",
		Timestamp: time.Now().AddDate(0, 0, -3),
		Trade:     &models.TransactionTrade{Size: 10, Price: 100},
	}
	bundle.Transactions = append(bundle.Transactions, purchase)

	app, _ := moneyApp(t, bundle, nil)

	text := app.analyticsView().MoneyPeriod.GetText(true)
	if !strings.Contains(text, "Сделки") {
		t.Errorf("period column %q does not report the trades group", text)
	}
	// The net deposit must not have moved: a purchase is not a withdrawal.
	if !strings.Contains(text, "20 000") {
		t.Errorf("period column %q: the net deposit changed when a purchase was added", text)
	}
}

// TestMoneyScreen_XIRRUnavailableSaysWhy: "Н/Д" alone is the kind of output
// that gets a bug report, so every refusal carries its reason.
//
// Both reachable reasons are covered. An account funded before the history
// begins has no flows to solve against; a history that only reaches back a
// week has no horizon to annualise over.
func TestMoneyScreen_XIRRUnavailableSaysWhy(t *testing.T) {
	base := time.Now()

	t.Run("no flows", func(t *testing.T) {
		app, _ := moneyApp(t, &api.HistoryBundle{
			Transactions: []models.Transaction{
				{ID: "c1", Category: "COMMISSION", Amount: -10, Currency: "RUB", Timestamp: base.AddDate(0, 0, -100)},
			},
			Boundary: base.AddDate(-2, 0, 0),
			Complete: true,
		}, nil)

		text := app.analyticsView().MoneySinceOpen.GetText(true)
		if !strings.Contains(text, "Н/Д") {
			t.Errorf("since-open column %q does not report XIRR as unavailable", text)
		}
		if !strings.Contains(text, analytics.XIRRNoSignChange.Label()) {
			t.Errorf("since-open column %q does not explain why XIRR is unavailable", text)
		}
	})

	t.Run("short horizon", func(t *testing.T) {
		app, _ := moneyApp(t, &api.HistoryBundle{
			Transactions: []models.Transaction{
				{ID: "d1", Category: "DEPOSIT", Amount: 100000, Currency: "RUB", Timestamp: base.AddDate(0, 0, -5)},
			},
			// A pass that only reached back a week: the horizon starts there.
			Boundary: base.AddDate(0, 0, -7),
			Complete: false,
		}, nil)

		text := app.analyticsView().MoneySinceOpen.GetText(true)
		if !strings.Contains(text, analytics.XIRRShortHorizon.Label()) {
			t.Errorf("since-open column %q does not name the short horizon", text)
		}
	})
}

// TestMoneyScreen_Benchmark shows the index beside the account's own rate.
func TestMoneyScreen_Benchmark(t *testing.T) {
	start := time.Now().AddDate(-2, 0, 0)
	app, _ := moneyApp(t, moneyBundle(), func(from time.Time) []models.Bar {
		if from.Before(start.AddDate(0, 1, 0)) {
			return []models.Bar{{Timestamp: from.Add(24 * time.Hour), Close: 2000}}
		}
		return []models.Bar{{Timestamp: time.Now().Add(-24 * time.Hour), Close: 2420}}
	})

	text := app.analyticsView().MoneySinceOpen.GetText(true)
	if !strings.Contains(text, "IMOEX") {
		t.Errorf("since-open column %q does not mention the index", text)
	}
	if !strings.Contains(text, "п.п.") {
		t.Errorf("since-open column %q does not show the difference in percentage points", text)
	}
	// The caveat about dividends belongs beside the comparison.
	if !strings.Contains(text, "без дивидендов") {
		t.Errorf("since-open column %q does not say the index excludes dividends", text)
	}
}

// TestMoneyScreen_BenchmarkMissing says so rather than leaving a blank line.
func TestMoneyScreen_BenchmarkMissing(t *testing.T) {
	app, _ := moneyApp(t, moneyBundle(), func(time.Time) []models.Bar { return nil })

	text := app.analyticsView().MoneySinceOpen.GetText(true)
	if !strings.Contains(text, "IMOEX: нет данных") {
		t.Errorf("since-open column %q does not report the missing index data", text)
	}
}

// TestMoneyScreen_ForeignCurrencyReported: money the result cannot include is
// named rather than silently dropped.
func TestMoneyScreen_ForeignCurrencyReported(t *testing.T) {
	bundle := moneyBundle()
	bundle.Transactions = append(bundle.Transactions, models.Transaction{
		ID: "usd", Category: "DEPOSIT", Amount: 500, Currency: "USD",
		Timestamp: time.Now().AddDate(0, 0, -30),
	})

	app, _ := moneyApp(t, bundle, nil)

	text := app.analyticsView().MoneySinceOpen.GetText(true)
	if !strings.Contains(text, "USD") {
		t.Errorf("since-open column %q does not report the excluded foreign flows", text)
	}
}

// TestMoneyScreen_EmptyHistory renders a hint rather than a wall of zeroes.
func TestMoneyScreen_EmptyHistory(t *testing.T) {
	app := NewApp(&mockClient{}, []models.AccountInfo{{ID: "acc1"}})
	app.portfolioView.TabbedView.SetTab(TabAnalytics)
	t.Cleanup(app.Stop)

	updateAnalyticsHistoryScreens(app)

	if text := app.analyticsView().MoneyPeriod.GetText(true); !strings.Contains(text, "История") {
		t.Errorf("period column %q does not explain that history is not loaded", text)
	}
}

// TestOverview_SinceOpenBlock puts the same figures on the overview once the
// history is there.
func TestOverview_SinceOpenBlock(t *testing.T) {
	app, capture := moneyApp(t, moneyBundle(), nil)

	capture(tcell.NewEventKey(tcell.KeyRune, '1', tcell.ModNone))
	updateAnalyticsOverview(app)

	// Title and body together: the block is named by its frame now.
	text := panelText(app.analyticsView().SinceOpen)
	if !strings.Contains(text, "с открытия") {
		t.Errorf("overview %q does not carry the since-open block", text)
	}
	if !strings.Contains(text, "120 000") {
		t.Errorf("overview %q does not show the net deposit", text)
	}
}

// TestOverview_SinceOpenHintBeforeHistory tells the user where to get the
// numbers instead of showing an empty block.
func TestOverview_SinceOpenHintBeforeHistory(t *testing.T) {
	app := NewApp(&mockClient{}, []models.AccountInfo{{ID: "acc1", Equity: "1000"}})
	app.portfolioView.TabbedView.SetTab(TabAnalytics)
	t.Cleanup(app.Stop)

	updateAnalyticsOverview(app)

	text := app.analyticsView().SinceOpen.GetText(true)
	if !strings.Contains(text, "Сделки") || !strings.Contains(text, "Деньги") {
		t.Errorf("overview %q does not point at the screens that load the history", text)
	}
}
