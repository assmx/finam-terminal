package api

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/accounts"
	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/assets"
	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/marketdata"
	"google.golang.org/genproto/googleapis/type/decimal"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// micRefusal is the live API's answer to GetAsset for a symbol without a MIC
// that the bulk asset list does not know (observed 2026-09-10 on FXRL, a blocked
// FinEx fund, and RU000A10AA02, a zero-quantity bond).
var micRefusal = status.Error(codes.InvalidArgument, "Mic must not be empty")

// refusalMocks serves a client with an empty MIC cache, counting every request
// a lot resolution can make. GetAsset answers assetErr, or SBER@TQBR with a lot
// of 10 once assetErr is nil.
type refusalMocks struct {
	assetCalls atomic.Int64
	paramCalls atomic.Int64
	quoteCalls atomic.Int64
	assetErr   error
}

func (m *refusalMocks) client() *Client {
	return &Client{
		assetsClient: &mockAssetsServiceClient{
			GetAssetFunc: func(ctx context.Context, in *assets.GetAssetRequest, opts ...grpc.CallOption) (*assets.GetAssetResponse, error) {
				m.assetCalls.Add(1)
				if m.assetErr != nil {
					return nil, m.assetErr
				}
				return &assets.GetAssetResponse{
					Ticker:  "SBER",
					Board:   "TQBR",
					LotSize: &decimal.Decimal{Value: "10"},
				}, nil
			},
			GetAssetParamsFunc: func(ctx context.Context, in *assets.GetAssetParamsRequest, opts ...grpc.CallOption) (*assets.GetAssetParamsResponse, error) {
				m.paramCalls.Add(1)
				return &assets.GetAssetParamsResponse{Symbol: in.Symbol}, nil
			},
		},
		marketDataClient: &mockMarketDataServiceClient{
			LastQuoteFunc: func(ctx context.Context, in *marketdata.QuoteRequest, opts ...grpc.CallOption) (*marketdata.QuoteResponse, error) {
				m.quoteCalls.Add(1)
				return &marketdata.QuoteResponse{Symbol: in.Symbol}, nil
			},
		},
		assetMicCache:       make(map[string]string),
		assetLotCache:       make(map[string]float64),
		tradeLotCache:       make(map[string]float64),
		instrumentNameCache: make(map[string]string),
	}
}

// TestRefreshTick_MicRefusalIsAskedOnce reproduces the live account of
// 2026-09-10: GetAccount lists FXRL and RU000A10AA02 without a MIC, the bulk list
// knows neither, and GetAsset refuses both with InvalidArgument. A refresh tick
// is what loadDataAsync does every five seconds — GetAccountDetails, then
// GetQuotes over the positions it returned. Each symbol may cost one GetAsset
// for the whole session; five ticks must not cost fifteen.
func TestRefreshTick_MicRefusalIsAskedOnce(t *testing.T) {
	m := &refusalMocks{assetErr: micRefusal}
	client := m.client()
	client.accountsClient = &mockAccountsServiceClient{
		GetAccountFunc: func(ctx context.Context, in *accounts.GetAccountRequest, opts ...grpc.CallOption) (*accounts.GetAccountResponse, error) {
			return &accounts.GetAccountResponse{
				AccountId: in.AccountId,
				Positions: []*accounts.Position{
					{Symbol: "FXRL", Quantity: &decimal.Decimal{Value: "7"}},
					{Symbol: "RU000A10AA02", Quantity: &decimal.Decimal{Value: "0"}},
				},
			}, nil
		},
	}

	for tick := 1; tick <= 5; tick++ {
		_, positions, err := client.GetAccountDetails("acc1")
		if err != nil {
			t.Fatalf("tick %d: GetAccountDetails error: %v", tick, err)
		}
		// The refusal changes what the terminal asks, not what it shows: the
		// fund stays on screen under the ticker the broker sent.
		if len(positions) != 1 || positions[0].Symbol != "FXRL" || positions[0].Ticker != "FXRL" || positions[0].MIC != "" {
			t.Fatalf("tick %d: positions = %+v, want FXRL alone, with no MIC", tick, positions)
		}
		if _, err := client.GetQuotes("acc1", []string{positions[0].Symbol}); err != nil {
			t.Fatalf("tick %d: GetQuotes error: %v", tick, err)
		}
	}

	if got := m.assetCalls.Load(); got != 2 {
		t.Errorf("GetAsset called %d times over 5 ticks, want 2 (one per symbol, then none)", got)
	}
	if got := m.paramCalls.Load(); got != 0 {
		t.Errorf("GetAssetParams called %d times, want 0 (nothing resolved to ask about)", got)
	}
	if got := m.quoteCalls.Load(); got != 0 {
		t.Errorf("LastQuote called %d times, want 0 (a symbol without a MIC cannot be quoted)", got)
	}
}

// TestGetFullSymbol_TransientFailureIsRetried pins what the refusal cache must
// leave alone: a failure that says nothing about the symbol is asked again on
// the next miss, and the answer that follows is used.
func TestGetFullSymbol_TransientFailureIsRetried(t *testing.T) {
	for _, code := range []codes.Code{codes.Unavailable, codes.DeadlineExceeded} {
		t.Run(code.String(), func(t *testing.T) {
			m := &refusalMocks{assetErr: status.Error(code, "try again later")}
			client := m.client()

			if got := client.getFullSymbol("SBER", "acc1"); got != "SBER" {
				t.Fatalf("getFullSymbol(SBER) during the outage = %q, want the ticker back", got)
			}

			m.assetErr = nil
			if got := client.getFullSymbol("SBER", "acc1"); got != "SBER@TQBR" {
				t.Errorf("getFullSymbol(SBER) after the outage = %q, want SBER@TQBR", got)
			}
			if got := m.assetCalls.Load(); got != 2 {
				t.Errorf("GetAsset called %d times, want 2 (the failure retried)", got)
			}
		})
	}
}

// TestGetFullSymbol_RefusalWithMicIsNotFiled verifies the refusal cache is kept
// to symbols without a MIC. Asked for SBER@TQBR with no account, the broker
// refuses the account, not the symbol; filing that against SBER would strip the
// MIC the bulk list knows from every lookup for a day.
func TestGetFullSymbol_RefusalWithMicIsNotFiled(t *testing.T) {
	m := &refusalMocks{assetErr: status.Error(codes.InvalidArgument, "Invalid arguments:account_id")}
	client := m.client()
	client.assetMicCache["SBER"] = "SBER@TQBR"

	for lookup := 1; lookup <= 2; lookup++ {
		if got := client.getFullSymbol("SBER", ""); got != "SBER@TQBR" {
			t.Fatalf("lookup %d: getFullSymbol(SBER) = %q, want SBER@TQBR", lookup, got)
		}
	}
	if got := m.assetCalls.Load(); got != 2 {
		t.Errorf("GetAsset called %d times, want 2 (the refusal not filed)", got)
	}
}

// TestGetFullSymbol_MicRefusalExpires verifies the refusal is remembered for
// refusedSymbolTTL rather than for ever: once it lapses the symbol is asked
// again.
func TestGetFullSymbol_MicRefusalExpires(t *testing.T) {
	defer func(ttl time.Duration) { refusedSymbolTTL = ttl }(refusedSymbolTTL)
	refusedSymbolTTL = 0

	m := &refusalMocks{assetErr: micRefusal}
	client := m.client()

	client.getFullSymbol("FXRL", "acc1")
	client.getFullSymbol("FXRL", "acc1")

	if got := m.assetCalls.Load(); got != 2 {
		t.Errorf("GetAsset called %d times with an expired refusal, want 2", got)
	}
}
