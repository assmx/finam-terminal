//go:build integration

package api

import (
	"errors"
	"testing"
	"time"

	"finam-terminal/api/testserver"

	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/marketdata"
)

// TestIntegration_BlockedPositions_CostNoRequest runs the account the
// reconnaissance saw — FXRL without a MIC, plus a position on a blocked venue —
// through the real client over bufconn. Both are recognised from the bulk list
// the client loaded at startup, and neither costs a lot resolution, a quote or
// a trading-parameters call: the last two hang on the live API.
func TestIntegration_BlockedPositions_CostNoRequest(t *testing.T) {
	client, ts := setupTestServer(t)
	ts.Accounts.Positions["ACC001"] = testserver.BlockedAccountPositions()

	assetCalls := ts.Assets.GetAssetCallCount.Load()
	paramCalls := ts.Assets.GetAssetParamsCallCount.Load()
	quoteCalls := ts.MarketData.LastQuoteCallCount.Load()

	_, positions, err := client.GetAccountDetails("ACC001")
	if err != nil {
		t.Fatalf("GetAccountDetails error: %v", err)
	}
	if len(positions) != 2 {
		t.Fatalf("%d positions, want 2", len(positions))
	}
	for _, p := range positions {
		if !p.Blocked {
			t.Errorf("%s not marked blocked", p.Symbol)
		}
	}
	if positions[0].Symbol != "FXRL" || positions[0].Name != "FinEx Russian RTS Equity MOEX" {
		t.Errorf("FXRL came back as %q named %q, want FXRL named after its twin", positions[0].Symbol, positions[0].Name)
	}

	symbols := []string{positions[0].Symbol, positions[1].Symbol}
	if _, err := client.GetQuotes("ACC001", symbols); err != nil {
		t.Fatalf("GetQuotes error: %v", err)
	}
	if _, err := client.GetAssetParams("ACC001", "AAPL.SPBZ@_SPBZ"); !errors.Is(err, ErrBlockedInstrument) {
		t.Errorf("GetAssetParams error = %v, want ErrBlockedInstrument", err)
	}

	if got := ts.Assets.GetAssetCallCount.Load() - assetCalls; got != 0 {
		t.Errorf("GetAsset called %d times, want 0", got)
	}
	if got := ts.Assets.GetAssetParamsCallCount.Load() - paramCalls; got != 0 {
		t.Errorf("GetAssetParams called %d times, want 0", got)
	}
	if got := ts.MarketData.LastQuoteCallCount.Load() - quoteCalls; got != 0 {
		t.Errorf("LastQuote called %d times, want 0", got)
	}
}

// TestIntegration_BlockedSymbolDoesNotSilenceStream is the failure the
// reconnaissance found: a blocked symbol in a subscription silences every
// symbol in it, and the mock silences it the same way. With the symbol kept out
// of the subscription, the live one beside it still streams.
func TestIntegration_BlockedSymbolDoesNotSilenceStream(t *testing.T) {
	client, ts := setupTestServer(t)
	sink := newQuoteStreamSink()

	sink.start(client)
	// An open profile of a blocked instrument joins the subscription after
	// the positions, which is how a blocked symbol reached a live shard.
	client.SetQuoteSymbols([]string{"SBER@TQBR", "AAPL.SPBZ@_SPBZ"})
	waitQuoteStreamCalls(t, ts, 1, 3*time.Second)

	symbols, _ := ts.MarketData.LastQuoteStreamSymbols.Load().([]string)
	if len(symbols) != 1 || symbols[0] != "SBER@TQBR" {
		t.Errorf("subscribed symbols = %v, want [SBER@TQBR]", symbols)
	}

	ts.MarketData.QuoteStreamQueue <- testserver.QuoteStreamItem{
		Quotes: []*marketdata.Quote{testserver.DefaultStreamQuote("SBER@TQBR", true)},
	}
	if q := sink.waitQuote(t, 3*time.Second); q.Symbol != "SBER@TQBR" {
		t.Errorf("streamed %q, want SBER@TQBR", q.Symbol)
	}
	sink.waitState(t, true, 3*time.Second)
}
