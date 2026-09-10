//go:build integration

package api

import (
	"context"
	"math"
	"testing"

	"finam-terminal/api/testserver"

	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/marketdata"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestIntegration_FXRates walks the rate path over bufconn: one LastQuote per
// currency with a pair, none for the rouble or a currency without one, and no
// lot resolution dragged along.
func TestIntegration_FXRates(t *testing.T) {
	client, ts := setupTestServer(t)
	assetCalls := ts.Assets.GetAssetCallCount.Load()
	paramCalls := ts.Assets.GetAssetParamsCallCount.Load()

	rates, err := client.GetFXRates([]string{"USD", "CNY", "EUR", "KZT", "HKD", "RUB"})
	if err != nil {
		t.Fatalf("GetFXRates: %v", err)
	}

	want := map[string]float64{"USD": 84.26, "CNY": 12.53, "EUR": 97.634, "KZT": 0.1911}
	if len(rates) != len(want) {
		t.Errorf("rates = %+v, want exactly %v", rates, want)
	}
	for currency, rate := range want {
		got, ok := rates[currency]
		if !ok || math.Abs(got.Rate-rate) > 1e-9 || got.At.IsZero() {
			t.Errorf("rates[%s] = %+v, %v; want %v with a quote time", currency, got, ok, rate)
		}
	}

	if n := ts.MarketData.LastQuoteCallCount.Load(); n != 4 {
		t.Errorf("LastQuote called %d times, want 4 — one per currency with a pair", n)
	}
	for _, symbol := range []string{"USD000UTSTOM@MISX", "CNYRUB_TOM@MISX", "EURRUB@#WWCP", "KZTRUB_TOM@MISX"} {
		if n := ts.MarketData.LastQuoteCallsFor(symbol); n != 1 {
			t.Errorf("LastQuote(%s) called %d times, want 1", symbol, n)
		}
	}
	if got := ts.Assets.GetAssetCallCount.Load(); got != assetCalls {
		t.Errorf("GetAsset calls went from %d to %d — a rate must not resolve a lot", assetCalls, got)
	}
	if got := ts.Assets.GetAssetParamsCallCount.Load(); got != paramCalls {
		t.Errorf("GetAssetParams calls went from %d to %d — a rate must not resolve a lot", paramCalls, got)
	}
}

// TestIntegration_FXRatesFrozenPair: a pair that stopped trading keeps
// answering with its old price; past fxRateMaxAge that is "no rate".
func TestIntegration_FXRatesFrozenPair(t *testing.T) {
	client, _ := setupTestServer(t)

	saved := fxSymbols["EUR"]
	fxSymbols["EUR"] = fxPair{Symbol: "EUR_RUB__TOM@MISX", Per: 1}
	t.Cleanup(func() { fxSymbols["EUR"] = saved })

	rates, err := client.GetFXRates([]string{"EUR", "USD"})
	if err != nil {
		t.Fatalf("GetFXRates: %v", err)
	}
	if got, ok := rates["EUR"]; ok {
		t.Errorf("a pair frozen since January 2025 produced a rate: %+v", got)
	}
	if _, ok := rates["USD"]; !ok {
		t.Error("the live pair lost its rate")
	}
}

// TestIntegration_FXRatesRateLimited: a refusal ends the walk and is reported
// so the caller can latch; an ordinary refusal costs one currency only.
func TestIntegration_FXRatesRateLimited(t *testing.T) {
	client, ts := setupTestServer(t)

	ts.MarketData.QuoteOverride = func(_ context.Context, req *marketdata.QuoteRequest) (*marketdata.QuoteResponse, error) {
		switch req.GetSymbol() {
		case "CNYRUB_TOM@MISX":
			return nil, status.Error(codes.ResourceExhausted, "Too many requests")
		case "USD000UTSTOM@MISX":
			return nil, status.Error(codes.Unavailable, "blip")
		}
		return &marketdata.QuoteResponse{Symbol: req.GetSymbol(), Quote: testserver.DefaultQuote(req.GetSymbol())}, nil
	}

	rates, err := client.GetFXRates([]string{"EUR", "USD", "CNY", "KZT"})
	if err == nil || !IsRateLimited(err) {
		t.Fatalf("GetFXRates error = %v, want a rate limit", err)
	}
	if _, ok := rates["EUR"]; !ok {
		t.Error("EUR arrived before the refusal and was dropped")
	}
	if _, ok := rates["USD"]; ok {
		t.Error("USD failed and still has a rate")
	}
	if n := ts.MarketData.LastQuoteCallsFor("KZTRUB_TOM@MISX"); n != 0 {
		t.Errorf("KZT was asked for %d times after the refusal, want 0", n)
	}
}
