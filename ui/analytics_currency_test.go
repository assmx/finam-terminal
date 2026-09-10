package ui

import (
	"strings"
	"testing"
	"time"

	"finam-terminal/analytics"
	"finam-terminal/models"
)

// currencyText is the Валюты panel, title and body.
func currencyText(app *App) string {
	return panelText(app.portfolioView.TabbedView.Analytics.Currencies)
}

// currencyMock answers the three free currency reads from tables, on top of
// the instrument types every overview test uses.
func currencyMock(currencies map[string]models.InstrumentCurrency, units map[string]models.UnitValue, faces map[string]string) *mockClient {
	m := typeMock()
	m.GetInstrumentCurrencyFunc = func(symbol string) (models.InstrumentCurrency, bool) {
		c, ok := currencies[symbol]
		return c, ok
	}
	m.GetUnitValueFunc = func(symbol string) (models.UnitValue, bool) {
		u, ok := units[symbol]
		return u, ok
	}
	m.BondFaceCurrencyCachedFunc = func(symbol string) (string, bool) {
		f, ok := faces[symbol]
		return f, ok
	}
	return m
}

func setRates(app *App, rates map[string]models.FXRate) {
	app.dataMutex.Lock()
	app.analytics.fx.rates = rates
	app.dataMutex.Unlock()
}

func setPositions(app *App, positions ...models.Position) {
	app.dataMutex.Lock()
	app.positions["acc1"] = positions
	app.dataMutex.Unlock()
}

var rubInstruments = map[string]models.InstrumentCurrency{
	"SBER@MISX": {Quote: "RUB"},
	"GAZP@MISX": {Quote: "RUB"},
}

// TestCurrencies_RoubleOnlyAccount: an account holding only roubles shows one
// row, the whole portfolio.
func TestCurrencies_RoubleOnlyAccount(t *testing.T) {
	app := overviewApp(currencyMock(rubInstruments, nil, nil), mcTestAccount())
	seedPositions(app)

	updateAnalyticsOverview(app)

	text := currencyText(app)
	if !strings.Contains(text, "Валюты") {
		t.Errorf("panel %q is not titled Валюты", text)
	}
	if line := lineWith(text, "RUB"); !strings.Contains(line, "100.0%") {
		t.Errorf("RUB row = %q, want the whole portfolio", line)
	}
	for _, bad := range []string{"нет курса", "USD", "неизвестна"} {
		if strings.Contains(text, bad) {
			t.Errorf("a rouble-only panel says %q:\n%s", bad, text)
		}
	}
}

// TestCurrencies_ConvertsAndDetails: a foreign holding is converted into the
// base, the base currency comes first, and a grey line under the rows gives
// the amount in its own money, the rate and the rate's time.
func TestCurrencies_ConvertsAndDetails(t *testing.T) {
	account := mcTestAccount()
	account.Cash = append(account.Cash, models.CashBalance{Currency: "USD", Amount: 10})
	app := overviewApp(currencyMock(map[string]models.InstrumentCurrency{"AMZN@XNGS": {Quote: "USD"}}, nil, nil), account)
	setPositions(app, models.Position{Symbol: "AMZN@XNGS", Ticker: "AMZN", Quantity: "1", CurrentPrice: "100"})
	setRates(app, map[string]models.FXRate{"USD": {Currency: "USD", Rate: 84, At: time.Now().Add(-time.Minute)}})

	updateAnalyticsOverview(app)

	text := currencyText(app)
	rub, usd := strings.Index(text, "RUB"), strings.Index(text, "USD")
	if rub < 0 || usd < 0 || rub > usd {
		t.Fatalf("panel %q does not put RUB before USD", text)
	}
	// (100 + 10) USD × 84 = 9 240 RUB.
	if line := lineWith(text, "USD "); !strings.Contains(line, "9 240") {
		t.Errorf("USD row = %q, want 9 240 in roubles", line)
	}
	if !strings.Contains(text, "USD 110.00 × 84.0000") {
		t.Errorf("panel %q lacks the detail line with the dollar amount and the rate", text)
	}
	if strings.Contains(text, "курс на") {
		t.Errorf("a fresh rate is marked stale:\n%s", text)
	}
}

// TestCurrencies_StaleRateMarked: a rate from the weekend says so instead of
// passing for today's.
func TestCurrencies_StaleRateMarked(t *testing.T) {
	account := mcTestAccount()
	account.Cash = append(account.Cash, models.CashBalance{Currency: "CNY", Amount: 1000})
	app := overviewApp(currencyMock(rubInstruments, nil, nil), account)
	seedPositions(app)
	at := time.Now().Add(-48 * time.Hour)
	setRates(app, map[string]models.FXRate{"CNY": {Currency: "CNY", Rate: 12.5, At: at}})

	updateAnalyticsOverview(app)

	want := "курс на " + at.Local().Format("02.01 15:04")
	if !strings.Contains(currencyText(app), want) {
		t.Errorf("panel %q does not say %q", currencyText(app), want)
	}
}

// TestCurrencies_NoRateShownInOwnMoney: a currency without a rate is listed in
// its own money, in yellow, and stays out of the shares.
func TestCurrencies_NoRateShownInOwnMoney(t *testing.T) {
	app := overviewApp(currencyMock(map[string]models.InstrumentCurrency{
		"SBER@MISX": {Quote: "RUB"},
		"0700@XHKG": {Quote: "HKD"},
	}, nil, nil), mcTestAccount())
	setPositions(app,
		models.Position{Symbol: "SBER@MISX", Ticker: "SBER", Quantity: "100", CurrentPrice: "280"},
		models.Position{Symbol: "0700@XHKG", Ticker: "0700", Quantity: "100", CurrentPrice: "400"},
	)

	updateAnalyticsOverview(app)

	if line := lineWith(currencyText(app), "HKD"); !strings.Contains(line, "40 000.00") || !strings.Contains(line, "нет курса") || !strings.Contains(line, "[yellow]") {
		t.Errorf("HKD line = %q, want 40 000.00 in yellow with нет курса", line)
	}
	if !strings.Contains(structureText(app), "в других валютах: 1") {
		t.Errorf("structure panel does not count the unrated position:\n%s", structureText(app))
	}
}

// TestCurrencies_UnresolvedBond: a bond whose foreign face is still being
// looked up has its own row with a share, and a note that its currency is not
// settled yet.
func TestCurrencies_UnresolvedBond(t *testing.T) {
	app := overviewApp(currencyMock(
		map[string]models.InstrumentCurrency{"RU000A10A851@MISX": {Quote: "RUB", FaceValue: 200000}},
		map[string]models.UnitValue{"RU000A10A851@MISX": {Currency: "RUB", Value: 16633960.33}},
		nil,
	), mcTestAccount())
	setPositions(app, models.Position{Symbol: "RU000A10A851@MISX", Ticker: "RU000A10A851", Quantity: "1", CurrentPrice: "97.25"})

	updateAnalyticsOverview(app)

	text := currencyText(app)
	if line := lineWith(text, "номинал в валюте"); !strings.Contains(line, "%") {
		t.Errorf("unresolved row = %q, want it with a share", line)
	}
	if !strings.Contains(text, "валюта номинала уточняется") {
		t.Errorf("panel %q does not say the face currency is being looked up", text)
	}
	// Valued at the broker's per-piece figure, not 85 times lower.
	if !strings.Contains(structureText(app), "16 753 960") {
		t.Errorf("structure total does not carry the bond at its per-piece value:\n%s", structureText(app))
	}
}

// TestCurrencies_ResolvedBondGoesToItsCurrency: once a calendar has named the
// dollar face, the replacement bond is a dollar holding valued through its face,
// and the unresolved row is gone.
func TestCurrencies_ResolvedBondGoesToItsCurrency(t *testing.T) {
	app := overviewApp(currencyMock(
		map[string]models.InstrumentCurrency{"RU000A10A851@MISX": {Quote: "RUB", FaceValue: 200000}},
		map[string]models.UnitValue{"RU000A10A851@MISX": {Currency: "RUB", Value: 16633960.33}},
		map[string]string{"RU000A10A851@MISX": "USD"},
	), mcTestAccount())
	setPositions(app, models.Position{Symbol: "RU000A10A851@MISX", Ticker: "RU000A10A851", Quantity: "1", CurrentPrice: "97.25"})
	setRates(app, map[string]models.FXRate{"USD": {Currency: "USD", Rate: 84, At: time.Now()}})

	updateAnalyticsOverview(app)

	text := currencyText(app)
	if strings.Contains(text, unresolvedFaceLabel) {
		t.Errorf("a bond whose face currency is known still shows as unresolved:\n%s", text)
	}
	// 97.25% of 200 000 USD at 84.
	if line := lineWith(text, "USD "); !strings.Contains(line, "16 338 000") {
		t.Errorf("USD row = %q, want 16 338 000", line)
	}
	if !strings.Contains(text, "USD 194 500.00 × 84.0000") {
		t.Errorf("panel %q lacks the bond's dollar amount", text)
	}
}

// TestCurrencies_CountersSaySoOutLoud: a currency the broker did not name and a
// bond face nothing could check are counted where the currencies are shown.
func TestCurrencies_CountersSaySoOutLoud(t *testing.T) {
	app := overviewApp(currencyMock(map[string]models.InstrumentCurrency{
		"B@MISX": {Quote: "RUB", FaceValue: 1000},
	}, nil, nil), mcTestAccount())
	setPositions(app,
		models.Position{Symbol: "X@MISX", Ticker: "X", Quantity: "1", CurrentPrice: "100"},
		models.Position{Symbol: "B@MISX", Ticker: "B", Quantity: "1", CurrentPrice: "100"},
	)

	updateAnalyticsOverview(app)

	text := currencyText(app)
	for _, want := range []string{"валюта неизвестна: 1", "валюта номинала не проверена: 1"} {
		if !strings.Contains(text, want) {
			t.Errorf("panel %q does not say %q", text, want)
		}
	}
}

// TestOverview_RiskKeepsLoanNotBalance: a foreign balance is shown once, in the
// currency panel; a foreign loan stays where the risk is.
func TestOverview_RiskKeepsLoanNotBalance(t *testing.T) {
	account := mcTestAccount()
	account.Cash = append(account.Cash,
		models.CashBalance{Currency: "USD", Amount: 500},
		models.CashBalance{Currency: "EUR", Amount: -20},
	)
	app := overviewApp(currencyMock(rubInstruments, nil, nil), account)
	seedPositions(app)

	updateAnalyticsOverview(app)

	risk := riskText(app)
	if strings.Contains(risk, "остаток") {
		t.Errorf("the risk panel still lists a balance:\n%s", risk)
	}
	if !strings.Contains(risk, "заём EUR") {
		t.Errorf("the risk panel lost the loan:\n%s", risk)
	}
	if line := lineWith(currencyText(app), "USD"); !strings.Contains(line, "500.00") {
		t.Errorf("USD line = %q, want the 500.00 balance in the currency panel", line)
	}
}

// TestCurrencies_RedrawsCostNothing is the budget promise for the new panel:
// twenty redraws with a foreign currency, a stale rate and an unresolved bond
// on screen ask for no rate, no calendar and no quote.
func TestCurrencies_RedrawsCostNothing(t *testing.T) {
	account := mcTestAccount()
	account.Cash = append(account.Cash, models.CashBalance{Currency: "USD", Amount: 5})
	mock := currencyMock(
		map[string]models.InstrumentCurrency{"RU000A10A851@MISX": {Quote: "RUB", FaceValue: 200000}, "0700@XHKG": {Quote: "HKD"}},
		map[string]models.UnitValue{"RU000A10A851@MISX": {Currency: "RUB", Value: 16633960.33}},
		nil,
	)
	app := overviewApp(mock, account)
	setPositions(app,
		models.Position{Symbol: "RU000A10A851@MISX", Quantity: "1", CurrentPrice: "97.25"},
		models.Position{Symbol: "0700@XHKG", Quantity: "1", CurrentPrice: "400"},
	)
	setRates(app, map[string]models.FXRate{"USD": {Rate: 84, At: time.Now().Add(-72 * time.Hour)}})

	for range 20 {
		updateAnalyticsOverview(app)
	}

	for name, n := range map[string]int64{
		"GetFXRates":          mock.GetFXRatesCalls.Load(),
		"GetBondFaceCurrency": mock.GetBondFaceCurrencyCalls.Load(),
		"GetBondEvents":       mock.GetBondEventsCalls.Load(),
		"GetQuotes":           mock.GetQuotesCalls.Load(),
	} {
		if n != 0 {
			t.Errorf("%s called %d times during redraws, want 0", name, n)
		}
	}
}

// TestRateTimeLabel: a rate from today shows its time, one from another day its
// date too, a stale one says it is old, and a rate without a time shows none.
func TestRateTimeLabel(t *testing.T) {
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.Local)
	tests := []struct {
		name string
		at   time.Time
		want string
	}{
		{"today", time.Date(2026, 9, 12, 9, 55, 0, 0, time.Local), "[gray]09:55[-]"},
		{"last night, not yet stale", time.Date(2026, 9, 11, 23, 49, 0, 0, time.Local), "[gray]11.09 23:49[-]"},
		{"stale", time.Date(2026, 9, 10, 18, 59, 0, 0, time.Local), "[yellow]курс на 10.09 18:59[-]"},
		{"no time", time.Time{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rateTimeLabel(tt.at, now); got != tt.want {
				t.Errorf("rateTimeLabel = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestRenderCurrencies_Empty: nothing held is said, not drawn as a zero row.
func TestRenderCurrencies_Empty(t *testing.T) {
	got := renderCurrencies(analytics.Allocation{}, time.Now(), 40)
	if !strings.Contains(got, "нет") {
		t.Errorf("renderCurrencies of nothing = %q, want it to say there is nothing", got)
	}
}
