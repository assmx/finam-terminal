package ui

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"finam-terminal/api"
	"finam-terminal/models"

	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/marketdata"
)

// mockClient is a shared mock for testing UI components
type mockClient struct {
	GetAccountsFunc       func() ([]models.AccountInfo, error)
	GetAccountDetailsFunc func(accountID string) (*models.AccountInfo, []models.Position, error)
	GetQuotesFunc         func(accountID string, symbols []string) (map[string]*models.Quote, error)
	PlaceOrderFunc        func(accountID string, symbol string, buySell string, quantity float64, params *models.OrderParams) (string, error)
	ClosePositionFunc     func(accountID string, symbol string, currentQuantity string, closeQuantity float64) (string, error)
	PlaceSLTPOrderFunc    func(accountID, symbol, buySell string, slQty, slPrice, tpQty, tpPrice float64) (string, error)

	SearchSecuritiesFunc  func(query string) ([]models.SecurityInfo, error)
	GetSnapshotsFunc      func(accountID string, symbols []string) (map[string]models.Quote, error)
	GetLotSizeFunc        func(ticker string) float64
	EnsureLotSizeFunc     func(accountID, symbol string) float64
	StartQuoteStreamFunc  func(onQuote func(models.Quote), onState func(up bool))
	SetQuoteSymbolsFunc   func(symbols []string)
	SubscribedSymbolsFunc func() []string
	GetInstrumentNameFunc func(key string) string

	GetTradeHistoryFunc func(accountID string) ([]models.Trade, error)
	GetActiveOrdersFunc func(accountID string) ([]models.Order, error)
	CancelOrderFunc     func(accountID, orderID string) error

	GetBarsFunc        func(accountID string, symbol string, timeframe marketdata.TimeFrame, from, to time.Time) ([]models.Bar, error)
	GetAssetInfoFunc   func(accountID string, symbol string) (*models.AssetDetails, error)
	GetAssetParamsFunc func(accountID string, symbol string) (*models.AssetParams, error)
	GetScheduleFunc    func(symbol string) ([]models.TradingSession, error)

	GetDividendsFunc  func(symbol string) ([]models.Dividend, error)
	GetSplitsFunc     func(symbol string) ([]models.Split, error)
	GetBondEventsFunc func(symbol string) ([]models.BondEvent, error)

	GetIndexConstituentsFunc  func(indexSymbol string) ([]models.IndexConstituent, error)
	GetIndexConstituentsCalls atomic.Int64

	// Analytics. GetInstrumentTypeCalls exists for the budget assertions the
	// track rests on: it proves the type lookup stays a memory read rather
	// than becoming a request.
	GetInstrumentTypeFunc  func(symbol string) string
	GetInstrumentTypeCalls atomic.Int64

	// GetQuotesCalls backs the same budget assertions: a redraw of the
	// Analytics overview must not reach for quotes either.
	GetQuotesCalls atomic.Int64

	// History. LoadHistoryCalls is the counter the laziness assertions rest
	// on: one pass per account per session, none on a tick, none on a repeat
	// visit. LoadHistoryDelay lets a test observe the in-flight state, and
	// LoadHistoryProgress is replayed to the caller so the progress line can
	// be exercised without a real walk.
	LoadHistoryFunc     func(ctx context.Context, req api.HistoryRequest) (*api.HistoryBundle, error)
	LoadHistoryCalls    atomic.Int64
	LoadHistoryDelay    time.Duration
	LoadHistoryProgress []api.HistoryProgress

	// Bars and calendars are counted for the same reason: the benchmark asks
	// for two narrow windows and the payout screen two calendars per position,
	// and both promise not to ask again on a repeat visit.
	GetBarsCalls       atomic.Int64
	GetDividendsCalls  atomic.Int64
	GetSplitsCalls     atomic.Int64
	GetBondEventsCalls atomic.Int64

	// Currency. The three reads are free in the real client; the two lookups
	// are not, and their counters carry the currency budget: one rate per
	// currency per TTL, none for a rouble account, no calendar for an
	// ordinary bond, and nothing at all on a redraw.
	GetInstrumentCurrencyFunc  func(symbol string) (models.InstrumentCurrency, bool)
	GetUnitValueFunc           func(symbol string) (models.UnitValue, bool)
	BondFaceCurrencyCachedFunc func(symbol string) (string, bool)
	GetBondFaceCurrencyFunc    func(symbol string) (string, error)
	GetFXRatesFunc             func(currencies []string) (map[string]models.FXRate, error)
	GetBondFaceCurrencyCalls   atomic.Int64
	GetFXRatesCalls            atomic.Int64

	currencyMu     sync.Mutex
	fxRatesAsked   map[string]int
	faceLookupsFor map[string]int
}

// FXRatesAskedFor reports how many GetFXRates calls included currency.
func (m *mockClient) FXRatesAskedFor(currency string) int {
	m.currencyMu.Lock()
	defer m.currencyMu.Unlock()
	return m.fxRatesAsked[currency]
}

// FaceLookupsFor reports how many GetBondFaceCurrency calls asked for symbol.
func (m *mockClient) FaceLookupsFor(symbol string) int {
	m.currencyMu.Lock()
	defer m.currencyMu.Unlock()
	return m.faceLookupsFor[symbol]
}

func (m *mockClient) GetInstrumentCurrency(symbol string) (models.InstrumentCurrency, bool) {
	if m.GetInstrumentCurrencyFunc != nil {
		return m.GetInstrumentCurrencyFunc(symbol)
	}
	return models.InstrumentCurrency{}, false
}

func (m *mockClient) GetUnitValue(symbol string) (models.UnitValue, bool) {
	if m.GetUnitValueFunc != nil {
		return m.GetUnitValueFunc(symbol)
	}
	return models.UnitValue{}, false
}

func (m *mockClient) BondFaceCurrencyCached(symbol string) (string, bool) {
	if m.BondFaceCurrencyCachedFunc != nil {
		return m.BondFaceCurrencyCachedFunc(symbol)
	}
	return "", false
}

func (m *mockClient) GetBondFaceCurrency(symbol string) (string, error) {
	m.GetBondFaceCurrencyCalls.Add(1)
	m.currencyMu.Lock()
	if m.faceLookupsFor == nil {
		m.faceLookupsFor = make(map[string]int)
	}
	m.faceLookupsFor[symbol]++
	m.currencyMu.Unlock()

	if m.GetBondFaceCurrencyFunc != nil {
		return m.GetBondFaceCurrencyFunc(symbol)
	}
	return "", nil
}

func (m *mockClient) GetFXRates(currencies []string) (map[string]models.FXRate, error) {
	m.GetFXRatesCalls.Add(1)
	m.currencyMu.Lock()
	if m.fxRatesAsked == nil {
		m.fxRatesAsked = make(map[string]int)
	}
	for _, c := range currencies {
		m.fxRatesAsked[c]++
	}
	m.currencyMu.Unlock()

	if m.GetFXRatesFunc != nil {
		return m.GetFXRatesFunc(currencies)
	}
	return map[string]models.FXRate{}, nil
}

func (m *mockClient) LoadHistory(ctx context.Context, req api.HistoryRequest, progress func(api.HistoryProgress)) (*api.HistoryBundle, error) {
	m.LoadHistoryCalls.Add(1)

	if m.LoadHistoryDelay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(m.LoadHistoryDelay):
		}
	}
	if progress != nil {
		for _, p := range m.LoadHistoryProgress {
			progress(p)
		}
	}
	if m.LoadHistoryFunc != nil {
		return m.LoadHistoryFunc(ctx, req)
	}
	return &api.HistoryBundle{Boundary: req.To, Complete: true}, nil
}

func (m *mockClient) GetAccounts() ([]models.AccountInfo, error) {
	if m.GetAccountsFunc != nil {
		return m.GetAccountsFunc()
	}
	return nil, nil
}

func (m *mockClient) GetAccountDetails(accountID string) (*models.AccountInfo, []models.Position, error) {
	if m.GetAccountDetailsFunc != nil {
		return m.GetAccountDetailsFunc(accountID)
	}
	return &models.AccountInfo{}, nil, nil
}

func (m *mockClient) GetQuotes(accountID string, symbols []string) (map[string]*models.Quote, error) {
	m.GetQuotesCalls.Add(1)
	if m.GetQuotesFunc != nil {
		return m.GetQuotesFunc(accountID, symbols)
	}
	return make(map[string]*models.Quote), nil
}

func (m *mockClient) PlaceOrder(accountID string, symbol string, buySell string, quantity float64, params *models.OrderParams) (string, error) {
	if m.PlaceOrderFunc != nil {
		return m.PlaceOrderFunc(accountID, symbol, buySell, quantity, params)
	}
	return "tx-123", nil
}

func (m *mockClient) PlaceSLTPOrder(accountID, symbol, buySell string, slQty, slPrice, tpQty, tpPrice float64) (string, error) {
	if m.PlaceSLTPOrderFunc != nil {
		return m.PlaceSLTPOrderFunc(accountID, symbol, buySell, slQty, slPrice, tpQty, tpPrice)
	}
	return "tx-123", nil
}

func (m *mockClient) ClosePosition(accountID string, symbol string, currentQuantity string, closeQuantity float64) (string, error) {
	if m.ClosePositionFunc != nil {
		return m.ClosePositionFunc(accountID, symbol, currentQuantity, closeQuantity)
	}
	return "tx-123", nil
}

func (m *mockClient) SearchSecurities(query string) ([]models.SecurityInfo, error) {
	if m.SearchSecuritiesFunc != nil {
		return m.SearchSecuritiesFunc(query)
	}
	return nil, nil
}

func (m *mockClient) GetSnapshots(accountID string, symbols []string) (map[string]models.Quote, error) {
	if m.GetSnapshotsFunc != nil {
		return m.GetSnapshotsFunc(accountID, symbols)
	}
	return make(map[string]models.Quote), nil
}

func (m *mockClient) StartQuoteStream(onQuote func(models.Quote), onState func(up bool)) {
	if m.StartQuoteStreamFunc != nil {
		m.StartQuoteStreamFunc(onQuote, onState)
	}
}

func (m *mockClient) SetQuoteSymbols(symbols []string) {
	if m.SetQuoteSymbolsFunc != nil {
		m.SetQuoteSymbolsFunc(symbols)
	}
}

func (m *mockClient) SubscribedSymbols() []string {
	if m.SubscribedSymbolsFunc != nil {
		return m.SubscribedSymbolsFunc()
	}
	return nil
}

func (m *mockClient) EnsureLotSize(accountID, symbol string) float64 {
	if m.EnsureLotSizeFunc != nil {
		return m.EnsureLotSizeFunc(accountID, symbol)
	}
	return 0
}

func (m *mockClient) GetLotSize(ticker string) float64 {
	if m.GetLotSizeFunc != nil {
		return m.GetLotSizeFunc(ticker)
	}
	return 1
}

func (m *mockClient) GetInstrumentName(key string) string {
	if m.GetInstrumentNameFunc != nil {
		return m.GetInstrumentNameFunc(key)
	}
	return ""
}

func (m *mockClient) GetTradeHistory(accountID string) ([]models.Trade, error) {
	if m.GetTradeHistoryFunc != nil {
		return m.GetTradeHistoryFunc(accountID)
	}
	return nil, nil
}

func (m *mockClient) GetActiveOrders(accountID string) ([]models.Order, error) {
	if m.GetActiveOrdersFunc != nil {
		return m.GetActiveOrdersFunc(accountID)
	}
	return nil, nil
}

func (m *mockClient) GetBars(accountID string, symbol string, timeframe marketdata.TimeFrame, from, to time.Time) ([]models.Bar, error) {
	m.GetBarsCalls.Add(1)
	if m.GetBarsFunc != nil {
		return m.GetBarsFunc(accountID, symbol, timeframe, from, to)
	}
	return nil, nil
}

func (m *mockClient) GetAssetInfo(accountID string, symbol string) (*models.AssetDetails, error) {
	if m.GetAssetInfoFunc != nil {
		return m.GetAssetInfoFunc(accountID, symbol)
	}
	return nil, nil
}

func (m *mockClient) GetAssetParams(accountID string, symbol string) (*models.AssetParams, error) {
	if m.GetAssetParamsFunc != nil {
		return m.GetAssetParamsFunc(accountID, symbol)
	}
	return nil, nil
}

func (m *mockClient) GetSchedule(symbol string) ([]models.TradingSession, error) {
	if m.GetScheduleFunc != nil {
		return m.GetScheduleFunc(symbol)
	}
	return nil, nil
}

func (m *mockClient) GetDividends(symbol string) ([]models.Dividend, error) {
	m.GetDividendsCalls.Add(1)
	if m.GetDividendsFunc != nil {
		return m.GetDividendsFunc(symbol)
	}
	return nil, nil
}

func (m *mockClient) GetSplits(symbol string) ([]models.Split, error) {
	m.GetSplitsCalls.Add(1)
	if m.GetSplitsFunc != nil {
		return m.GetSplitsFunc(symbol)
	}
	return nil, nil
}

func (m *mockClient) GetBondEvents(symbol string) ([]models.BondEvent, error) {
	m.GetBondEventsCalls.Add(1)
	if m.GetBondEventsFunc != nil {
		return m.GetBondEventsFunc(symbol)
	}
	return nil, nil
}

func (m *mockClient) GetIndexConstituents(indexSymbol string) ([]models.IndexConstituent, error) {
	m.GetIndexConstituentsCalls.Add(1)
	if m.GetIndexConstituentsFunc != nil {
		return m.GetIndexConstituentsFunc(indexSymbol)
	}
	return nil, nil
}

func (m *mockClient) GetInstrumentType(symbol string) string {
	m.GetInstrumentTypeCalls.Add(1)
	if m.GetInstrumentTypeFunc != nil {
		return m.GetInstrumentTypeFunc(symbol)
	}
	return ""
}

func (m *mockClient) CancelOrder(accountID, orderID string) error {
	if m.CancelOrderFunc != nil {
		return m.CancelOrderFunc(accountID, orderID)
	}
	return nil
}

// mockClient must satisfy the interface the App is given; a compile-time check
// beats discovering a missing method inside a goroutine at run time.
var _ APIClient = (*mockClient)(nil)
