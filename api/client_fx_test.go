package api

import (
	"context"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/assets"
	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/marketdata"
	"google.golang.org/genproto/googleapis/type/decimal"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// fxQuote builds a pair quote; an empty last or close is left absent, the way
// a pair that has not traded today arrives.
func fxQuote(last, close string, at time.Time) *marketdata.Quote {
	q := &marketdata.Quote{}
	if last != "" {
		q.Last = &decimal.Decimal{Value: last}
	}
	if close != "" {
		q.Close = &decimal.Decimal{Value: close}
	}
	if !at.IsZero() {
		q.Timestamp = timestamppb.New(at)
	}
	return q
}

// TestRateFromQuote reads a rate out of a pair quote: last, else close, divided
// by what the pair is quoted per, refused when unusable or frozen.
func TestRateFromQuote(t *testing.T) {
	now := time.Date(2026, 9, 10, 22, 2, 31, 0, time.UTC)
	fresh := now.Add(-5 * time.Minute)

	tests := []struct {
		name   string
		quote  *marketdata.Quote
		per    float64
		want   float64
		wantAt time.Time
		wantOK bool
	}{
		{"last wins", fxQuote("84.26", "85.1675", fresh), 1, 84.26, fresh, true},
		{"close when last is absent", fxQuote("", "83.77", fresh), 1, 83.77, fresh, true},
		{"close when last is zero", fxQuote("0", "12.665", fresh), 1, 12.665, fresh, true},
		{"quoted per 100", fxQuote("19.11", "19.1975", fresh), 100, 0.1911, fresh, true},
		{"comma decimal", fxQuote("84,26", "", fresh), 1, 84.26, fresh, true},
		{"no timestamp is still a price", fxQuote("97.634", "", time.Time{}), 1, 97.634, time.Time{}, true},
		{"old but inside the max age", fxQuote("84.0", "", now.Add(-13*24*time.Hour)), 1, 84, now.Add(-13 * 24 * time.Hour), true},
		{"frozen pair", fxQuote("95.62", "95.62", time.Date(2025, 1, 9, 10, 0, 3, 0, time.UTC)), 1, 0, time.Time{}, false},
		{"neither last nor close", fxQuote("", "", fresh), 1, 0, time.Time{}, false},
		{"negative", fxQuote("-84", "-84", fresh), 1, 0, time.Time{}, false},
		{"not a number", fxQuote("NaN", "Inf", fresh), 1, 0, time.Time{}, false},
		{"unparsable", fxQuote("N/A", "", fresh), 1, 0, time.Time{}, false},
		{"no per", fxQuote("84.26", "", fresh), 0, 0, time.Time{}, false},
		{"nil quote", nil, 1, 0, time.Time{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := rateFromQuote("USD", tt.quote, tt.per, now)
			if ok != tt.wantOK {
				t.Fatalf("rateFromQuote ok = %v, want %v (got %+v)", ok, tt.wantOK, got)
			}
			if !ok {
				return
			}
			if got.Currency != "USD" || math.Abs(got.Rate-tt.want) > 1e-9 || !got.At.Equal(tt.wantAt) {
				t.Errorf("rateFromQuote = %+v, want USD %v at %v", got, tt.want, tt.wantAt)
			}
		})
	}
}

// TestFXSymbols pins the table the reconnaissance chose: a live pair to the
// rouble for each currency, and the multiplier for those quoted per 100.
func TestFXSymbols(t *testing.T) {
	want := map[string]fxPair{
		"USD": {Symbol: "USD000UTSTOM@MISX", Per: 1},
		"CNY": {Symbol: "CNYRUB_TOM@MISX", Per: 1},
		"EUR": {Symbol: "EURRUB@#WWCP", Per: 1},
		"INR": {Symbol: "INRRUB@#WWCP", Per: 1},
		"KZT": {Symbol: "KZTRUB_TOM@MISX", Per: 100},
		"BYN": {Symbol: "BYNRUB_TOM@MISX", Per: 1},
		"TRY": {Symbol: "TRYRUB_TOM@MISX", Per: 1},
		"AMD": {Symbol: "AMDRUB_TOM@MISX", Per: 100},
		"KGS": {Symbol: "KGSRUB_TOM@MISX", Per: 100},
	}
	if len(fxSymbols) != len(want) {
		t.Errorf("fxSymbols has %d entries, want %d", len(fxSymbols), len(want))
	}
	for currency, pair := range want {
		if got := fxSymbols[currency]; got != pair {
			t.Errorf("fxSymbols[%s] = %+v, want %+v", currency, got, pair)
		}
	}
	for _, none := range []string{"RUB", "HKD", "JPY", "GBP", "CHF", "UZS"} {
		if _, ok := fxSymbols[none]; ok {
			t.Errorf("fxSymbols carries %s, which has no live pair to the rouble", none)
		}
	}
}

// fxMocks answers LastQuote from a per-symbol table and records every call.
// The assets mock fails the test if touched: a rate must not drag a lot
// resolution along.
type fxMocks struct {
	t      *testing.T
	mu     sync.Mutex
	quotes map[string]*marketdata.Quote
	errs   map[string]error
	asked  []string
}

func (m *fxMocks) client() *Client {
	return &Client{
		marketDataClient: &mockMarketDataServiceClient{
			LastQuoteFunc: func(ctx context.Context, in *marketdata.QuoteRequest, opts ...grpc.CallOption) (*marketdata.QuoteResponse, error) {
				if _, ok := ctx.Deadline(); !ok {
					m.t.Errorf("LastQuote(%s) carried no deadline", in.Symbol)
				}
				m.mu.Lock()
				m.asked = append(m.asked, in.Symbol)
				m.mu.Unlock()
				if err := m.errs[in.Symbol]; err != nil {
					return nil, err
				}
				q, ok := m.quotes[in.Symbol]
				if !ok {
					return nil, status.Error(codes.NotFound, "no quote")
				}
				return &marketdata.QuoteResponse{Symbol: in.Symbol, Quote: q}, nil
			},
		},
		assetsClient: &mockAssetsServiceClient{
			GetAssetFunc: func(ctx context.Context, in *assets.GetAssetRequest, opts ...grpc.CallOption) (*assets.GetAssetResponse, error) {
				m.t.Errorf("GetAsset(%s) called for a rate", in.Symbol)
				return nil, status.Error(codes.Internal, "unexpected")
			},
			GetAssetParamsFunc: func(ctx context.Context, in *assets.GetAssetParamsRequest, opts ...grpc.CallOption) (*assets.GetAssetParamsResponse, error) {
				m.t.Errorf("GetAssetParams(%s) called for a rate", in.Symbol)
				return nil, status.Error(codes.Internal, "unexpected")
			},
		},
		assetMicCache:       make(map[string]string),
		assetLotCache:       make(map[string]float64),
		tradeLotCache:       make(map[string]float64),
		instrumentNameCache: make(map[string]string),
	}
}

func (m *fxMocks) calls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.asked...)
}

func liveFX() map[string]*marketdata.Quote {
	at := time.Now().Add(-time.Minute)
	return map[string]*marketdata.Quote{
		"USD000UTSTOM@MISX": fxQuote("84.26", "85.1675", at),
		"CNYRUB_TOM@MISX":   fxQuote("12.53", "12.665", at),
		"EURRUB@#WWCP":      fxQuote("97.634", "98.874", at),
		"KZTRUB_TOM@MISX":   fxQuote("19.11", "19.1975", at),
	}
}

// TestGetFXRates_OneLastQuotePerCurrency: each currency costs exactly one
// LastQuote on its pair, and nothing else.
func TestGetFXRates_OneLastQuotePerCurrency(t *testing.T) {
	m := &fxMocks{t: t, quotes: liveFX()}
	client := m.client()

	rates, err := client.GetFXRates([]string{"USD", "CNY", "KZT", "EUR"})
	if err != nil {
		t.Fatalf("GetFXRates: %v", err)
	}

	want := map[string]float64{"USD": 84.26, "CNY": 12.53, "KZT": 0.1911, "EUR": 97.634}
	for currency, rate := range want {
		got, ok := rates[currency]
		if !ok || math.Abs(got.Rate-rate) > 1e-9 || got.Currency != currency || got.At.IsZero() {
			t.Errorf("rates[%s] = %+v, %v; want %v with a time", currency, got, ok, rate)
		}
	}
	asked := m.calls()
	if strings.Join(asked, ",") != "USD000UTSTOM@MISX,CNYRUB_TOM@MISX,KZTRUB_TOM@MISX,EURRUB@#WWCP" {
		t.Errorf("LastQuote asked for %v, want each pair once, in the order asked", asked)
	}
}

// TestGetFXRates_OnlyWhatHasAPair: the rouble needs no rate, a currency outside
// the table has none to ask for, and a repeat or a blank costs nothing.
func TestGetFXRates_OnlyWhatHasAPair(t *testing.T) {
	m := &fxMocks{t: t, quotes: liveFX()}
	client := m.client()

	rates, err := client.GetFXRates([]string{"RUB", "HKD", "", " usd ", "USD"})
	if err != nil {
		t.Fatalf("GetFXRates: %v", err)
	}
	if len(rates) != 1 || rates["USD"].Rate != 84.26 {
		t.Errorf("rates = %+v, want only USD", rates)
	}
	if asked := m.calls(); len(asked) != 1 {
		t.Errorf("LastQuote asked %d times (%v), want 1", len(asked), asked)
	}

	if rates, err := client.GetFXRates(nil); err != nil || len(rates) != 0 {
		t.Errorf("GetFXRates(nil) = %v, %v; want empty, nil", rates, err)
	}
}

// TestGetFXRates_RateLimitEndsTheBatch: a refusal stops the walk — asking for
// the rest would only dig deeper into the limit — and what arrived is kept.
func TestGetFXRates_RateLimitEndsTheBatch(t *testing.T) {
	m := &fxMocks{t: t, quotes: liveFX(), errs: map[string]error{
		"USD000UTSTOM@MISX": status.Error(codes.ResourceExhausted, "Too many requests"),
	}}
	client := m.client()

	rates, err := client.GetFXRates([]string{"CNY", "USD", "EUR"})
	if err == nil || !IsRateLimited(err) {
		t.Fatalf("GetFXRates error = %v, want a recognisable rate limit", err)
	}
	if _, ok := rates["CNY"]; !ok {
		t.Error("the rate that arrived before the refusal was dropped")
	}
	if asked := m.calls(); len(asked) != 2 {
		t.Errorf("LastQuote asked %v, want the walk to stop at the refusal", asked)
	}
}

// TestGetFXRates_OrdinaryErrorCostsOneCurrency: a failure on one pair leaves
// that currency without a rate and the others untouched.
func TestGetFXRates_OrdinaryErrorCostsOneCurrency(t *testing.T) {
	m := &fxMocks{t: t, quotes: liveFX(), errs: map[string]error{
		"USD000UTSTOM@MISX": status.Error(codes.DeadlineExceeded, "slow"),
	}}
	client := m.client()

	rates, err := client.GetFXRates([]string{"USD", "CNY"})
	if err != nil {
		t.Fatalf("GetFXRates error = %v, want none for an ordinary failure", err)
	}
	if _, ok := rates["USD"]; ok {
		t.Error("the failed currency has a rate")
	}
	if _, ok := rates["CNY"]; !ok {
		t.Error("the healthy currency lost its rate")
	}
}

// TestGetFXRates_UnusableQuotesAreNoRate: an empty quote, a frozen pair and a
// zero price are all "no rate", not an error.
func TestGetFXRates_UnusableQuotesAreNoRate(t *testing.T) {
	quotes := liveFX()
	quotes["USD000UTSTOM@MISX"] = fxQuote("", "", time.Now())
	quotes["CNYRUB_TOM@MISX"] = fxQuote("12.5", "12.5", time.Date(2025, 1, 9, 10, 0, 3, 0, time.UTC))
	quotes["EURRUB@#WWCP"] = fxQuote("0", "0", time.Now())
	m := &fxMocks{t: t, quotes: quotes}
	client := m.client()

	rates, err := client.GetFXRates([]string{"USD", "CNY", "EUR", "KZT"})
	if err != nil {
		t.Fatalf("GetFXRates: %v", err)
	}
	if len(rates) != 1 {
		t.Errorf("rates = %+v, want only KZT", rates)
	}
	if _, ok := rates["KZT"]; !ok {
		t.Error("the usable rate is missing")
	}
}

// TestGetFXRates_NilQuoteInAnswer: an answer that carries no quote at all is
// "no rate".
func TestGetFXRates_NilQuoteInAnswer(t *testing.T) {
	client := &Client{marketDataClient: &mockMarketDataServiceClient{
		LastQuoteFunc: func(ctx context.Context, in *marketdata.QuoteRequest, opts ...grpc.CallOption) (*marketdata.QuoteResponse, error) {
			return &marketdata.QuoteResponse{Symbol: in.Symbol}, nil
		},
	}}

	rates, err := client.GetFXRates([]string{"USD"})
	if err != nil || len(rates) != 0 {
		t.Errorf("GetFXRates = %v, %v; want empty, nil", rates, err)
	}
}
