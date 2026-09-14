package api

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/accounts"
	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/assets"
	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/marketdata"
	"google.golang.org/genproto/googleapis/type/decimal"
	"google.golang.org/grpc"
)

// blockedList is a bulk asset list shaped like the live one of 2026-09-11: a
// blocked FinEx fund whose name the list cut at 30 characters (the word BLOCKED
// lost with it), a blocked SPB share whose base ticker also trades on a live
// venue, and an ordinary share.
func blockedList() []*assets.Asset {
	return []*assets.Asset{
		{Symbol: "FXRL.MMBZ@_MMBZ", Ticker: "FXRL.MMBZ", Mic: "_MMBZ", Type: "FUNDS", Name: "FinEx Russian RTS Equity MOEX "},
		{Symbol: "AAPL.SPBZ@_SPBZ", Ticker: "AAPL.SPBZ", Mic: "_SPBZ", Type: "EQUITIES", Name: "Apple BLOCKED"},
		{Symbol: "AAPL@RUSX", Ticker: "AAPL", Mic: "RUSX", Type: "EQUITIES", Name: "Apple Inc."},
		{Symbol: "SBER@MISX", Ticker: "SBER", Mic: "MISX", Type: "EQUITIES", Name: "Сбербанк"},
	}
}

// blockedClient loads list through loadAssetCache into a client whose every
// request a lot resolution or a quote can make is counted by m.
func blockedClient(t *testing.T, m *refusalMocks, list []*assets.Asset) *Client {
	t.Helper()

	client := m.client()
	mock := client.assetsClient.(*mockAssetsServiceClient)
	mock.AssetsFunc = func(_ context.Context, _ *assets.AssetsRequest, _ ...grpc.CallOption) (*assets.AssetsResponse, error) {
		return &assets.AssetsResponse{Assets: list}, nil
	}
	// Answer for the instrument asked about, so a live ticker resolves to its
	// own venue rather than to whatever the shared mock names.
	mock.GetAssetFunc = func(_ context.Context, in *assets.GetAssetRequest, _ ...grpc.CallOption) (*assets.GetAssetResponse, error) {
		m.assetCalls.Add(1)
		if m.assetErr != nil {
			return nil, m.assetErr
		}
		ticker, board, _ := strings.Cut(in.Symbol, "@")
		return &assets.GetAssetResponse{Ticker: ticker, Board: board, LotSize: &decimal.Decimal{Value: "1"}}, nil
	}
	client.assetTypeCache = map[string]string{}

	if err := client.loadAssetCache(); err != nil {
		t.Fatalf("loadAssetCache failed: %v", err)
	}
	return client
}

// holding answers GetAccount with the given positions, each seven pieces.
func holding(symbols ...string) *mockAccountsServiceClient {
	return &mockAccountsServiceClient{
		GetAccountFunc: func(_ context.Context, in *accounts.GetAccountRequest, _ ...grpc.CallOption) (*accounts.GetAccountResponse, error) {
			positions := make([]*accounts.Position, 0, len(symbols))
			for _, s := range symbols {
				positions = append(positions, &accounts.Position{
					Symbol:       s,
					Quantity:     &decimal.Decimal{Value: "7"},
					CurrentPrice: &decimal.Decimal{Value: "21.83"},
				})
			}
			return &accounts.GetAccountResponse{AccountId: in.AccountId, Positions: positions}, nil
		},
	}
}

// TestIsBlockedSymbol pins the venue rule: the MIC is what follows the last
// "@" (the live list carries FME@DE.SPBZ@_SPBZ), compared without regard to
// case, and a symbol without a MIC is not judged here at all.
func TestIsBlockedSymbol(t *testing.T) {
	tests := []struct {
		symbol string
		want   bool
	}{
		{"AAPL.SPBZ@_SPBZ", true},
		{"FXRL.MMBZ@_MMBZ", true},
		{"FME@DE.SPBZ@_SPBZ", true},
		{"fxrl.mmbz@_mmbz", true},
		{"SBER@MISX", false},
		{"AAPL@RUSX", false},
		{"USDRUB@#WWCP", false},
		{"X@_SPBZX", false},
		{"_SPBZ", false},
		{"FXRL", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsBlockedSymbol(tt.symbol); got != tt.want {
			t.Errorf("IsBlockedSymbol(%q) = %v, want %v", tt.symbol, got, tt.want)
		}
	}
}

// TestGetAccountDetails_TwinMarksMicLessPositionBlocked is the live FXRL: the
// broker sends the ticker without a MIC, the bulk list does not know it but
// knows its twin on a blocked venue. The position is marked blocked, named
// after the twin, left under the ticker the broker sent — and costs no request
// on any tick, not even the one GetAsset refusal the refusal cache allowed.
func TestGetAccountDetails_TwinMarksMicLessPositionBlocked(t *testing.T) {
	m := &refusalMocks{assetErr: micRefusal}
	client := blockedClient(t, m, blockedList())
	client.accountsClient = holding("FXRL")

	for tick := 1; tick <= 3; tick++ {
		_, positions, err := client.GetAccountDetails("acc1")
		if err != nil {
			t.Fatalf("tick %d: GetAccountDetails error: %v", tick, err)
		}
		if len(positions) != 1 {
			t.Fatalf("tick %d: %d positions, want 1", tick, len(positions))
		}
		p := positions[0]
		if !p.Blocked {
			t.Errorf("tick %d: FXRL not marked blocked", tick)
		}
		if p.Symbol != "FXRL" || p.Ticker != "FXRL" || p.MIC != "" {
			t.Errorf("tick %d: symbol %q ticker %q mic %q, want FXRL as the broker sent it", tick, p.Symbol, p.Ticker, p.MIC)
		}
		if p.Name != "FinEx Russian RTS Equity MOEX" {
			t.Errorf("tick %d: name %q, want the twin's, trimmed", tick, p.Name)
		}
	}

	if got := m.assetCalls.Load(); got != 0 {
		t.Errorf("GetAsset called %d times, want 0 (the twin already says it is blocked)", got)
	}
	if got := m.paramCalls.Load(); got != 0 {
		t.Errorf("GetAssetParams called %d times, want 0", got)
	}
}

// TestGetAccountDetails_BlockedVenueMarksPosition covers a position the broker
// sends on a blocked venue. GetAssetParams hangs on such a symbol, so the lot
// resolution must not ask anything about it.
func TestGetAccountDetails_BlockedVenueMarksPosition(t *testing.T) {
	m := &refusalMocks{}
	client := blockedClient(t, m, blockedList())
	client.accountsClient = holding("AAPL.SPBZ@_SPBZ")

	_, positions, err := client.GetAccountDetails("acc1")
	if err != nil {
		t.Fatalf("GetAccountDetails error: %v", err)
	}
	if len(positions) != 1 {
		t.Fatalf("%d positions, want 1", len(positions))
	}
	p := positions[0]
	if !p.Blocked {
		t.Error("AAPL.SPBZ@_SPBZ not marked blocked")
	}
	if p.Symbol != "AAPL.SPBZ@_SPBZ" || p.Ticker != "AAPL.SPBZ" || p.MIC != "_SPBZ" {
		t.Errorf("symbol %q ticker %q mic %q", p.Symbol, p.Ticker, p.MIC)
	}
	if p.Name != "Apple BLOCKED" {
		t.Errorf("name %q, want the bulk list's", p.Name)
	}
	if got := m.assetCalls.Load() + m.paramCalls.Load(); got != 0 {
		t.Errorf("%d GetAsset/GetAssetParams calls, want 0", got)
	}
}

// TestGetAccountDetails_LiveNamesakeIsNotBlocked pins the limit of the twin
// rule: 621 of the 820 blocked instruments share their base ticker with a live
// listing, and a ticker the bulk list knows on a live venue resolves there, as
// it always did.
func TestGetAccountDetails_LiveNamesakeIsNotBlocked(t *testing.T) {
	m := &refusalMocks{}
	client := blockedClient(t, m, blockedList())
	client.accountsClient = holding("AAPL")

	_, positions, err := client.GetAccountDetails("acc1")
	if err != nil {
		t.Fatalf("GetAccountDetails error: %v", err)
	}
	if len(positions) != 1 {
		t.Fatalf("%d positions, want 1", len(positions))
	}
	if positions[0].Blocked {
		t.Error("AAPL marked blocked although the bulk list knows it on a live venue")
	}
	if positions[0].Symbol != "AAPL@RUSX" {
		t.Errorf("symbol %q, want AAPL@RUSX", positions[0].Symbol)
	}
}

// TestGetAccountDetails_MicLessWithoutTwinIsNotBlocked pins the other limit:
// absence from the bulk list alone says nothing (RU000A10AA02 is absent too and
// is a zero-quantity bond, not a blocked one). Such a ticker keeps its old path —
// one GetAsset, refused, then remembered.
func TestGetAccountDetails_MicLessWithoutTwinIsNotBlocked(t *testing.T) {
	m := &refusalMocks{assetErr: micRefusal}
	client := blockedClient(t, m, blockedList())
	client.accountsClient = holding("RU000A10AA03")

	for tick := 1; tick <= 3; tick++ {
		_, positions, err := client.GetAccountDetails("acc1")
		if err != nil {
			t.Fatalf("tick %d: GetAccountDetails error: %v", tick, err)
		}
		if len(positions) != 1 || positions[0].Blocked {
			t.Fatalf("tick %d: positions = %+v, want one, not blocked", tick, positions)
		}
	}
	if got := m.assetCalls.Load(); got != 1 {
		t.Errorf("GetAsset called %d times, want 1 (the refusal cache, unchanged)", got)
	}
}

// TestGetFullSymbol_BlockedAsksNothing covers every caller of the lot
// resolution — the order modal and GetQuotes included — not only the account
// load: a blocked symbol comes back as it went in, with no request.
func TestGetFullSymbol_BlockedAsksNothing(t *testing.T) {
	m := &refusalMocks{}
	client := blockedClient(t, m, blockedList())

	for _, symbol := range []string{"AAPL.SPBZ@_SPBZ", "FXRL"} {
		if got := client.getFullSymbol(symbol, "acc1"); got != symbol {
			t.Errorf("getFullSymbol(%q) = %q, want it back unchanged", symbol, got)
		}
	}
	if got := m.assetCalls.Load() + m.paramCalls.Load(); got != 0 {
		t.Errorf("%d GetAsset/GetAssetParams calls, want 0", got)
	}
}

// TestGetQuotes_SkipsBlockedSymbols pins the quote guard: LastQuote on a
// blocked symbol hangs until its deadline (30 s on 2026-09-10), so a batch that
// holds one must not ask about it — silently, since it is not a failure — while
// every other symbol is still quoted.
func TestGetQuotes_SkipsBlockedSymbols(t *testing.T) {
	m := &refusalMocks{}
	client := blockedClient(t, m, blockedList())
	var asked []string
	client.marketDataClient = &mockMarketDataServiceClient{
		LastQuoteFunc: func(_ context.Context, in *marketdata.QuoteRequest, _ ...grpc.CallOption) (*marketdata.QuoteResponse, error) {
			asked = append(asked, in.Symbol)
			return &marketdata.QuoteResponse{Symbol: in.Symbol, Quote: &marketdata.Quote{Symbol: in.Symbol, Last: &decimal.Decimal{Value: "1"}}}, nil
		},
	}

	quotes, err := client.GetQuotes("acc1", []string{"AAPL.SPBZ@_SPBZ", "SBER@MISX", "FXRL"})
	if err != nil {
		t.Fatalf("GetQuotes error: %v", err)
	}
	if len(asked) != 1 || asked[0] != "SBER@MISX" {
		t.Errorf("LastQuote asked for %v, want [SBER@MISX] alone", asked)
	}
	if len(quotes) != 1 || quotes["SBER@MISX"] == nil {
		t.Errorf("quotes = %v, want SBER@MISX alone", quotes)
	}
}

// TestGetAssetParams_RefusesBlocked pins the trading-parameters guard: the call
// hangs on a blocked symbol (two of three probes on 2026-09-11), so the client
// answers ErrBlockedInstrument itself — for a symbol on a blocked venue and for
// a ticker known only by its blocked twin — and still asks about anything else.
func TestGetAssetParams_RefusesBlocked(t *testing.T) {
	m := &refusalMocks{}
	client := blockedClient(t, m, blockedList())

	for _, symbol := range []string{"AAPL.SPBZ@_SPBZ", "FXRL"} {
		if _, err := client.GetAssetParams("acc1", symbol); !errors.Is(err, ErrBlockedInstrument) {
			t.Errorf("GetAssetParams(%q) error = %v, want ErrBlockedInstrument", symbol, err)
		}
	}
	if got := m.paramCalls.Load(); got != 0 {
		t.Errorf("GetAssetParams sent %d requests for blocked symbols, want 0", got)
	}

	if _, err := client.GetAssetParams("acc1", "SBER@MISX"); err != nil {
		t.Fatalf("GetAssetParams(SBER@MISX) error: %v", err)
	}
	if got := m.paramCalls.Load(); got == 0 {
		t.Error("GetAssetParams(SBER@MISX) sent no request; the guard must stop only blocked symbols")
	}
}

// TestLoadAssetCache_FirstTwinNamesTheTicker keeps the name deterministic when a
// ticker has a twin on both blocked venues: the first in the list wins, and
// either way the position is blocked.
func TestLoadAssetCache_FirstTwinNamesTheTicker(t *testing.T) {
	m := &refusalMocks{}
	client := blockedClient(t, m, []*assets.Asset{
		{Symbol: "XYZ.SPBZ@_SPBZ", Ticker: "XYZ.SPBZ", Mic: "_SPBZ", Name: "XYZ SPB BLOCKED"},
		{Symbol: "XYZ.MMBZ@_MMBZ", Ticker: "XYZ.MMBZ", Mic: "_MMBZ", Name: "XYZ MOEX BLOCKED"},
	})
	client.accountsClient = holding("XYZ")

	_, positions, err := client.GetAccountDetails("acc1")
	if err != nil {
		t.Fatalf("GetAccountDetails error: %v", err)
	}
	if len(positions) != 1 || !positions[0].Blocked {
		t.Fatalf("positions = %+v, want XYZ blocked", positions)
	}
	if positions[0].Name != "XYZ SPB BLOCKED" {
		t.Errorf("name %q, want the first twin's", positions[0].Name)
	}
}
