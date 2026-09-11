package testserver

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/marketdata"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// QuoteStreamItem is one item fed into MockMarketDataServer.QuoteStreamQueue:
// quotes to send over the SubscribeQuote stream, an in-band stream error to
// report inside a response, or a transport error that ends the stream
// (simulating a dropped connection).
type QuoteStreamItem struct {
	Quotes    []*marketdata.Quote
	StreamErr *marketdata.StreamError
	Err       error
}

// MockMarketDataServer implements marketdata.MarketDataServiceServer for testing.
type MockMarketDataServer struct {
	marketdata.UnimplementedMarketDataServiceServer

	// QuoteOverride, if set, is called instead of the default behavior.
	QuoteOverride func(ctx context.Context, req *marketdata.QuoteRequest) (*marketdata.QuoteResponse, error)

	// LastQuoteCallCount counts LastQuote calls, overridden ones included, and
	// lastQuoteCalls counts them per symbol — the rate budget promises one
	// request per currency, and only a per-symbol count can show it.
	LastQuoteCallCount atomic.Int64
	lastQuoteMu        sync.Mutex
	lastQuoteCalls     map[string]int64

	// QuoteStreamCallCount tracks the number of SubscribeQuote calls (each call
	// is one stream open, i.e. an initial subscribe, a resubscribe after a
	// symbol change, or a reconnect).
	QuoteStreamCallCount atomic.Int64

	// QuoteStreamCalled is sent to (non-blocking) on every SubscribeQuote call.
	QuoteStreamCalled chan struct{}

	// QuoteStreamQueue feeds the active SubscribeQuote stream. The stream also
	// ends cleanly (nil) when its context is cancelled.
	QuoteStreamQueue chan QuoteStreamItem

	// LastQuoteStreamSymbols stores the symbol list ([]string) of the most
	// recent SubscribeQuote request.
	LastQuoteStreamSymbols atomic.Value

	// SubscribeQuoteOverride, if set, is called instead of the default stream
	// behavior.
	SubscribeQuoteOverride func(req *marketdata.SubscribeQuoteRequest, stream marketdata.MarketDataService_SubscribeQuoteServer) error
}

// NewMockMarketDataServer creates a MockMarketDataServer with defaults.
func NewMockMarketDataServer() *MockMarketDataServer {
	return &MockMarketDataServer{
		QuoteStreamCalled: make(chan struct{}, 100),
		QuoteStreamQueue:  make(chan QuoteStreamItem, 100),
	}
}

// SubscribeQuote streams whatever the test feeds into QuoteStreamQueue until the
// stream context is cancelled or an item carries a transport error.
func (m *MockMarketDataServer) SubscribeQuote(req *marketdata.SubscribeQuoteRequest, stream marketdata.MarketDataService_SubscribeQuoteServer) error {
	m.QuoteStreamCallCount.Add(1)
	m.LastQuoteStreamSymbols.Store(append([]string(nil), req.Symbols...))

	// Non-blocking notification
	select {
	case m.QuoteStreamCalled <- struct{}{}:
	default:
	}

	if m.SubscribeQuoteOverride != nil {
		return m.SubscribeQuoteOverride(req, stream)
	}

	ctx := stream.Context()

	// The live API answers a subscription carrying a blocked symbol with
	// silence — no quote for any of its symbols, no error, until the client
	// gives up (2026-09-11). The mock does the same, and takes nothing from
	// the queue meanwhile, so the quotes meant for a healthy stream still
	// reach it.
	for _, symbol := range req.Symbols {
		if blockedVenue(symbol) {
			<-ctx.Done()
			return nil
		}
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case item, ok := <-m.QuoteStreamQueue:
			if !ok {
				return nil
			}
			if item.Err != nil {
				return item.Err
			}
			if err := stream.Send(&marketdata.SubscribeQuoteResponse{
				Quote: item.Quotes,
				Error: item.StreamErr,
			}); err != nil {
				return err
			}
		}
	}
}

// blockedVenue reports whether a symbol sits on a venue the broker files
// blocked instruments on. It mirrors api.IsBlockedSymbol, which the test server
// cannot import: the api package's tests import this one.
func blockedVenue(symbol string) bool {
	at := strings.LastIndex(symbol, "@")
	if at < 0 {
		return false
	}
	switch strings.ToUpper(symbol[at+1:]) {
	case "_SPBZ", "_MMBZ":
		return true
	}
	return false
}

// LastQuoteCallsFor reports how many LastQuote calls asked for symbol.
func (m *MockMarketDataServer) LastQuoteCallsFor(symbol string) int64 {
	m.lastQuoteMu.Lock()
	defer m.lastQuoteMu.Unlock()
	return m.lastQuoteCalls[symbol]
}

// LastQuote returns a quote for the requested symbol.
func (m *MockMarketDataServer) LastQuote(ctx context.Context, req *marketdata.QuoteRequest) (*marketdata.QuoteResponse, error) {
	m.LastQuoteCallCount.Add(1)
	m.lastQuoteMu.Lock()
	if m.lastQuoteCalls == nil {
		m.lastQuoteCalls = make(map[string]int64)
	}
	m.lastQuoteCalls[req.GetSymbol()]++
	m.lastQuoteMu.Unlock()

	if m.QuoteOverride != nil {
		return m.QuoteOverride(ctx, req)
	}

	q := DefaultQuote(req.Symbol)
	if q == nil {
		return nil, status.Errorf(codes.NotFound, "quote not found for %s", req.Symbol)
	}
	return &marketdata.QuoteResponse{
		Symbol: req.Symbol,
		Quote:  q,
	}, nil
}

// Bars returns candlestick data for the requested symbol.
func (m *MockMarketDataServer) Bars(_ context.Context, req *marketdata.BarsRequest) (*marketdata.BarsResponse, error) {
	bars := DefaultBars(req.Symbol)
	return &marketdata.BarsResponse{
		Symbol: req.Symbol,
		Bars:   bars,
	}, nil
}
