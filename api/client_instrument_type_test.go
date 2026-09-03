package api

import (
	"context"
	"testing"

	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/assets"
	"google.golang.org/grpc"
)

// typeCacheClient builds a Client whose asset cache was loaded from the given
// bulk asset list, exercising loadAssetCache rather than hand-filling the maps.
func typeCacheClient(t *testing.T, list []*assets.Asset) *Client {
	t.Helper()

	client := &Client{
		assetsClient: &mockAssetsServiceClient{
			AssetsFunc: func(_ context.Context, _ *assets.AssetsRequest, _ ...grpc.CallOption) (*assets.AssetsResponse, error) {
				return &assets.AssetsResponse{Assets: list}, nil
			},
		},
		assetMicCache:       map[string]string{},
		assetLotCache:       map[string]float64{},
		tradeLotCache:       map[string]float64{},
		instrumentNameCache: map[string]string{},
		assetTypeCache:      map[string]string{},
	}

	if err := client.loadAssetCache(); err != nil {
		t.Fatalf("loadAssetCache failed: %v", err)
	}
	return client
}

// TestGetInstrumentType resolves a type by full symbol and by bare ticker, and
// answers an empty string for anything the bulk list never mentioned.
func TestGetInstrumentType(t *testing.T) {
	client := typeCacheClient(t, []*assets.Asset{
		{Ticker: "SBER", Symbol: "SBER@MISX", Mic: "MISX", Name: "Сбер Банк", Type: "EQUITIES"},
		{Ticker: "RU000A0JNPK5", Symbol: "RU000A0JNPK5@MISX", Mic: "MISX", Name: "Облигация", Type: "BONDS"},
		{Ticker: "SiH6", Symbol: "SiH6@RTSX", Mic: "RTSX", Name: "Фьючерс USD/RUB", Type: "FUTURES"},
	})

	tests := []struct {
		name string
		key  string
		want string
	}{
		{"full symbol", "SBER@MISX", "EQUITIES"},
		{"bare ticker", "SBER", "EQUITIES"},
		{"bond by symbol", "RU000A0JNPK5@MISX", "BONDS"},
		{"future by ticker", "SiH6", "FUTURES"},
		{"unknown symbol", "NOPE@MISX", ""},
		{"unknown ticker", "NOPE", ""},
		{"empty key", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := client.GetInstrumentType(tt.key); got != tt.want {
				t.Errorf("GetInstrumentType(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

// TestGetInstrumentType_EmptyType keeps an instrument whose Type the API left
// blank indistinguishable from one it never sent: both answer "", and the
// caller maps that to Прочее.
func TestGetInstrumentType_EmptyType(t *testing.T) {
	client := typeCacheClient(t, []*assets.Asset{
		{Ticker: "WEIRD", Symbol: "WEIRD@MISX", Mic: "MISX", Name: "Без типа"},
	})

	if got := client.GetInstrumentType("WEIRD"); got != "" {
		t.Errorf("GetInstrumentType(WEIRD) = %q, want empty", got)
	}
}

// TestLoadAssetCache_TypeInSecurityCache checks the type also travels on
// SecurityInfo, which is what the search window and profile already consume.
func TestLoadAssetCache_TypeInSecurityCache(t *testing.T) {
	client := typeCacheClient(t, []*assets.Asset{
		{Ticker: "SBER", Symbol: "SBER@MISX", Mic: "MISX", Name: "Сбер Банк", Type: "EQUITIES"},
	})

	results, err := client.SearchSecurities("SBER")
	if err != nil {
		t.Fatalf("SearchSecurities failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].Type != "EQUITIES" {
		t.Errorf("SecurityInfo.Type = %q, want EQUITIES", results[0].Type)
	}
}

// TestLoadAssetCache_SingleRequest guards the budget the whole Analytics tab
// rests on: the type map is filled by the one bulk call already made at
// startup, never by a per-instrument lookup.
func TestLoadAssetCache_SingleRequest(t *testing.T) {
	calls := 0
	client := &Client{
		assetsClient: &mockAssetsServiceClient{
			AssetsFunc: func(_ context.Context, _ *assets.AssetsRequest, _ ...grpc.CallOption) (*assets.AssetsResponse, error) {
				calls++
				return &assets.AssetsResponse{Assets: []*assets.Asset{
					{Ticker: "SBER", Symbol: "SBER@MISX", Mic: "MISX", Type: "EQUITIES"},
				}}, nil
			},
		},
		assetMicCache:       map[string]string{},
		assetLotCache:       map[string]float64{},
		tradeLotCache:       map[string]float64{},
		instrumentNameCache: map[string]string{},
		assetTypeCache:      map[string]string{},
	}

	if err := client.loadAssetCache(); err != nil {
		t.Fatalf("loadAssetCache failed: %v", err)
	}

	for range 10 {
		client.GetInstrumentType("SBER")
		client.GetInstrumentType("SBER@MISX")
		client.GetInstrumentType("UNKNOWN")
	}

	if calls != 1 {
		t.Errorf("Assets called %d times, want exactly 1", calls)
	}
}
