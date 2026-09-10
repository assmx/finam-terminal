package ui

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"finam-terminal/models"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// fxApp is an app on the Analytics overview for acc1 with the given money and
// positions; the mock describes the instruments.
func fxApp(t *testing.T, mock *mockClient, cash []models.CashBalance, positions ...models.Position) *App {
	t.Helper()
	account := mcTestAccount()
	account.Cash = cash
	app := overviewApp(mock, account)
	t.Cleanup(app.Stop)
	if len(positions) > 0 {
		setPositions(app, positions...)
	}
	return app
}

// ratesMock answers GetFXRates from a table, as the API layer would: only the
// currencies it has a rate for.
func ratesMock(table map[string]float64) *mockClient {
	mock := currencyMock(nil, nil, nil)
	mock.GetFXRatesFunc = func(currencies []string) (map[string]models.FXRate, error) {
		out := make(map[string]models.FXRate)
		for _, c := range currencies {
			if r, ok := table[c]; ok {
				out[c] = models.FXRate{Currency: c, Rate: r, At: time.Now()}
			}
		}
		return out, nil
	}
	return mock
}

// waitFX waits for both loaders to settle.
func waitFX(t *testing.T, app *App) {
	t.Helper()
	if !waitFor(func() bool {
		app.dataMutex.RLock()
		defer app.dataMutex.RUnlock()
		return !app.analytics.fx.loading && !app.analytics.fx.faceLoading
	}) {
		t.Fatal("the currency loaders never settled")
	}
}

func rateOf(app *App, currency string) (models.FXRate, bool) {
	app.dataMutex.RLock()
	defer app.dataMutex.RUnlock()
	r, ok := app.analytics.fx.rates[currency]
	return r, ok
}

// ageAttempts pushes every recorded attempt back past the TTL, the way time
// passing would.
func ageAttempts(app *App) {
	app.dataMutex.Lock()
	defer app.dataMutex.Unlock()
	back := func(m map[string]time.Time) {
		for k, v := range m {
			m[k] = v.Add(-2 * fxRateTTL)
		}
	}
	back(app.analytics.fx.askedAt)
	back(app.analytics.fx.fetchedAt)
	back(app.analytics.fx.faceAskedAt)
}

func overviewStatus(app *App) string {
	updateAnalyticsOverview(app)
	return app.portfolioView.TabbedView.Analytics.OverviewStatus.GetText(false)
}

var rub = []models.CashBalance{{Currency: "RUB", Amount: 1000}}

// TestFX_RoubleAccountAsksNothing: an account with nothing foreign costs no
// rate and no calendar.
func TestFX_RoubleAccountAsksNothing(t *testing.T) {
	mock := currencyMock(rubInstruments, nil, nil)
	app := fxApp(t, mock, rub)
	seedPositions(app)

	for range 5 {
		app.ensureCurrencyData()
	}
	waitFX(t, app)

	if n := mock.GetFXRatesCalls.Load() + mock.GetBondFaceCurrencyCalls.Load(); n != 0 {
		t.Errorf("a rouble account cost %d currency lookups, want 0", n)
	}
}

// TestFX_OneRatePerCurrencyPerTTL: each currency is asked for once, and a
// repeated schedule inside the TTL asks for nothing.
func TestFX_OneRatePerCurrencyPerTTL(t *testing.T) {
	mock := ratesMock(map[string]float64{"USD": 84.26, "CNY": 12.53})
	app := fxApp(t, mock, append(rub, models.CashBalance{Currency: "USD", Amount: 5}, models.CashBalance{Currency: "CNY", Amount: 7}))

	app.ensureCurrencyData()
	waitFX(t, app)
	for range 10 {
		app.ensureCurrencyData()
	}
	waitFX(t, app)

	if n := mock.GetFXRatesCalls.Load(); n != 1 {
		t.Errorf("GetFXRates called %d times inside the TTL, want 1", n)
	}
	for c, want := range map[string]float64{"USD": 84.26, "CNY": 12.53} {
		if n := mock.FXRatesAskedFor(c); n != 1 {
			t.Errorf("%s asked for %d times, want 1", c, n)
		}
		if r, ok := rateOf(app, c); !ok || r.Rate != want {
			t.Errorf("stored %s rate = %+v, %v; want %v", c, r, ok, want)
		}
	}
	if mock.FXRatesAskedFor("RUB") != 0 {
		t.Error("the rouble was asked for")
	}

	// Past the TTL the schedule asks again.
	ageAttempts(app)
	app.ensureCurrencyData()
	waitFX(t, app)
	if n := mock.FXRatesAskedFor("USD"); n != 2 {
		t.Errorf("USD asked for %d times after the TTL, want 2", n)
	}
}

// TestFX_OnlyWhileTheOverviewIsOnScreen: the schedule asks nothing for a
// screen nobody is looking at.
func TestFX_OnlyWhileTheOverviewIsOnScreen(t *testing.T) {
	mock := ratesMock(map[string]float64{"USD": 84})
	app := fxApp(t, mock, append(rub, models.CashBalance{Currency: "USD", Amount: 5}))

	app.analyticsView().SetScreen(AnalyticsTrades)
	app.ensureCurrencyData()
	app.portfolioView.TabbedView.SetTab(TabPositions)
	app.analyticsView().SetScreen(AnalyticsOverview)
	app.ensureCurrencyData()
	waitFX(t, app)

	if n := mock.GetFXRatesCalls.Load(); n != 0 {
		t.Errorf("GetFXRates called %d times off the overview, want 0", n)
	}
}

// TestFX_EnteringTheOverviewAsks: switching to the overview is what starts the
// first load, and the tick keeps it on schedule.
func TestFX_EnteringTheOverviewAsks(t *testing.T) {
	mock := ratesMock(map[string]float64{"USD": 84})
	app := fxApp(t, mock, append(rub, models.CashBalance{Currency: "USD", Amount: 5}))
	app.analyticsView().SetScreen(AnalyticsTrades)

	app.SetAnalyticsScreen(AnalyticsOverview)
	waitFX(t, app)

	if n := mock.GetFXRatesCalls.Load(); n != 1 {
		t.Errorf("entering the overview made %d rate requests, want 1", n)
	}
}

// TestFX_RateLimitLatches: a refusal stops the schedule for the session and
// says so; R still works.
func TestFX_RateLimitLatches(t *testing.T) {
	mock := currencyMock(nil, nil, nil)
	mock.GetFXRatesFunc = func([]string) (map[string]models.FXRate, error) {
		return map[string]models.FXRate{}, status.Error(codes.ResourceExhausted, "Too many requests")
	}
	app := fxApp(t, mock, append(rub, models.CashBalance{Currency: "USD", Amount: 5}))

	app.ensureCurrencyData()
	waitFX(t, app)

	if s := overviewStatus(app); !strings.Contains(s, "лимит API") || !strings.Contains(s, "R — вручную") {
		t.Errorf("status = %q, want the rate-limit message", s)
	}

	ageAttempts(app)
	app.ensureCurrencyData()
	waitFX(t, app)
	if n := mock.GetFXRatesCalls.Load(); n != 1 {
		t.Errorf("the schedule asked %d times after the latch, want 1 (none since)", n)
	}

	app.RefreshAnalytics()
	waitFX(t, app)
	if n := mock.GetFXRatesCalls.Load(); n != 2 {
		t.Errorf("R made %d requests in total, want 2 — R works through the latch", n)
	}
}

// TestFX_OrdinaryFailureWaitsForTheSchedule: a currency that failed is not
// retried at once — only on schedule or by R — and the others keep their rate.
func TestFX_OrdinaryFailureWaitsForTheSchedule(t *testing.T) {
	mock := ratesMock(map[string]float64{"USD": 84}) // CNY fails
	app := fxApp(t, mock, append(rub, models.CashBalance{Currency: "USD", Amount: 5}, models.CashBalance{Currency: "CNY", Amount: 7}))

	app.ensureCurrencyData()
	waitFX(t, app)
	app.ensureCurrencyData()
	waitFX(t, app)

	if n := mock.FXRatesAskedFor("CNY"); n != 1 {
		t.Errorf("CNY asked for %d times right after failing, want 1", n)
	}
	if _, ok := rateOf(app, "USD"); !ok {
		t.Error("USD lost its rate")
	}
	if s := overviewStatus(app); strings.Contains(s, "лимит") {
		t.Errorf("an ordinary failure latched: %q", s)
	}

	// R asks again for the missing rate, and not for the fresh one.
	app.RefreshAnalytics()
	waitFX(t, app)
	if n := mock.FXRatesAskedFor("CNY"); n != 2 {
		t.Errorf("CNY asked for %d times after R, want 2", n)
	}
	if n := mock.FXRatesAskedFor("USD"); n != 1 {
		t.Errorf("USD asked for %d times after R, want 1 — it was fresh", n)
	}
}

// TestFX_AccountSwitchKeepsFreshRates: the rates belong to no account, so a
// second account asks only for what the first did not.
func TestFX_AccountSwitchKeepsFreshRates(t *testing.T) {
	mock := ratesMock(map[string]float64{"USD": 84, "CNY": 12.5})
	second := mcTestAccount()
	second.ID = "acc2"
	second.Cash = append(rub, models.CashBalance{Currency: "USD", Amount: 1}, models.CashBalance{Currency: "CNY", Amount: 1})
	first := mcTestAccount()
	first.Cash = append(rub, models.CashBalance{Currency: "USD", Amount: 5})

	app := NewApp(mock, []models.AccountInfo{first, second})
	t.Cleanup(app.Stop)
	app.portfolioView.TabbedView.SetTab(TabAnalytics)
	app.analyticsView().SetScreen(AnalyticsOverview)

	app.ensureCurrencyData()
	waitFX(t, app)

	app.dataMutex.Lock()
	app.selectedIdx = 1
	app.dataMutex.Unlock()
	app.ensureCurrencyData()
	waitFX(t, app)

	if n := mock.FXRatesAskedFor("USD"); n != 1 {
		t.Errorf("USD asked for %d times across the switch, want 1", n)
	}
	if n := mock.FXRatesAskedFor("CNY"); n != 1 {
		t.Errorf("CNY asked for %d times, want 1", n)
	}
}

// TestFX_StatusWhileLoading says a load is under way, and a result that lands
// after Stop is written nowhere.
func TestFX_StatusWhileLoadingAndStop(t *testing.T) {
	release := make(chan struct{})
	var entered atomic.Bool
	mock := currencyMock(nil, nil, nil)
	mock.GetFXRatesFunc = func([]string) (map[string]models.FXRate, error) {
		entered.Store(true)
		<-release
		return map[string]models.FXRate{"USD": {Currency: "USD", Rate: 84}}, nil
	}
	app := fxApp(t, mock, append(rub, models.CashBalance{Currency: "USD", Amount: 5}))

	app.ensureCurrencyData()
	if !waitFor(entered.Load) {
		t.Fatal("the load never started")
	}
	if s := overviewStatus(app); !strings.Contains(s, "загрузка") {
		t.Errorf("status while loading = %q, want a loading message", s)
	}

	app.Stop()
	close(release)
	time.Sleep(50 * time.Millisecond)

	if _, ok := rateOf(app, "USD"); ok {
		t.Error("a rate that arrived after Stop was written")
	}
}

// TestFX_RedrawWhenALoaderLands redraws the overview a loader has just filled,
// and leaves a screen nobody is looking at alone.
func TestFX_RedrawWhenALoaderLands(t *testing.T) {
	app := fxApp(t, currencyMock(nil, nil, nil), rub)
	status := app.portfolioView.TabbedView.Analytics.OverviewStatus

	app.dataMutex.Lock()
	app.analytics.fx.limited = true
	app.dataMutex.Unlock()

	app.analyticsView().SetScreen(AnalyticsTrades)
	app.redrawOverviewIfShown()
	if got := status.GetText(false); got != "" {
		t.Errorf("an overview off screen was redrawn: status %q", got)
	}

	app.analyticsView().SetScreen(AnalyticsOverview)
	app.redrawOverviewIfShown()
	if got := status.GetText(false); !strings.Contains(got, "лимит API") {
		t.Errorf("the overview on screen was not redrawn: status %q", got)
	}
}

// TestFX_FaceLookupOnlyWhereACalendarHelps: an ordinary rouble bond costs no
// calendar request; a replacement bond costs one, once per TTL.
func TestFX_FaceLookupOnlyWhereACalendarHelps(t *testing.T) {
	savedPace := payoutPace
	payoutPace = 0
	t.Cleanup(func() { payoutPace = savedPace })

	mock := currencyMock(
		map[string]models.InstrumentCurrency{
			"RU000A10BF48@MISX": {Quote: "RUB", FaceValue: 1000},
			"RU000A10A851@MISX": {Quote: "RUB", FaceValue: 200000},
		},
		map[string]models.UnitValue{
			"RU000A10BF48@MISX": {Currency: "RUB", Value: 1019.24},
			"RU000A10A851@MISX": {Currency: "RUB", Value: 16633960.33},
		},
		nil,
	)
	app := fxApp(t, mock, rub,
		models.Position{Symbol: "RU000A10BF48@MISX", Quantity: "10", CurrentPrice: "100.72"},
		models.Position{Symbol: "RU000A10A851@MISX", Quantity: "1", CurrentPrice: "97.25"},
	)

	app.ensureCurrencyData()
	waitFX(t, app)
	for range 5 {
		app.ensureCurrencyData()
	}
	waitFX(t, app)

	if n := mock.FaceLookupsFor("RU000A10BF48@MISX"); n != 0 {
		t.Errorf("the rouble bond cost %d calendar lookups, want 0", n)
	}
	if n := mock.FaceLookupsFor("RU000A10A851@MISX"); n != 1 {
		t.Errorf("the replacement bond was looked up %d times inside the TTL, want 1", n)
	}
}

// TestFX_FaceRateLimitLatchesToo: a calendar refused for the rate limit stops
// the schedule the same way a rate refused would.
func TestFX_FaceRateLimitLatchesToo(t *testing.T) {
	savedPace := payoutPace
	payoutPace = 0
	t.Cleanup(func() { payoutPace = savedPace })

	mock := currencyMock(
		map[string]models.InstrumentCurrency{"RU000A10A851@MISX": {Quote: "RUB", FaceValue: 200000}},
		map[string]models.UnitValue{"RU000A10A851@MISX": {Currency: "RUB", Value: 16633960.33}},
		nil,
	)
	mock.GetBondFaceCurrencyFunc = func(string) (string, error) {
		return "", status.Error(codes.ResourceExhausted, "Too many requests")
	}
	app := fxApp(t, mock, append(rub, models.CashBalance{Currency: "USD", Amount: 1}),
		models.Position{Symbol: "RU000A10A851@MISX", Quantity: "1", CurrentPrice: "97.25"},
	)
	mock.GetFXRatesFunc = func([]string) (map[string]models.FXRate, error) { return nil, nil }

	app.ensureCurrencyData()
	waitFX(t, app)
	if s := overviewStatus(app); !strings.Contains(s, "лимит API") {
		t.Errorf("status = %q, want the rate-limit message", s)
	}

	ageAttempts(app)
	app.ensureCurrencyData()
	waitFX(t, app)
	if n := mock.GetBondFaceCurrencyCalls.Load(); n != 1 {
		t.Errorf("calendar lookups after the latch = %d in total, want 1", n)
	}
}
