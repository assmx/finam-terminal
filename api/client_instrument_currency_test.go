package api

import (
	"context"
	"math"
	"sync/atomic"
	"testing"

	"finam-terminal/models"

	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/assets"
	"google.golang.org/genproto/googleapis/type/decimal"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// replacementBondAsset is GetAsset for «РФ ЗО 27 Д» as the real API answered it
// on 2026-09-10: a 200 000 USD face, settled in roubles, and the "%" GetAsset
// puts where a reader would expect the face currency.
func replacementBondAsset() *assets.GetAssetResponse {
	return &assets.GetAssetResponse{
		Ticker:        "RU000A10A851",
		Board:         "TQCB",
		Mic:           "MISX",
		Type:          "BONDS",
		LotSize:       &decimal.Decimal{Value: "1"},
		QuoteCurrency: "RUB",
		AssetDetails: &assets.GetAssetResponse_BondDetails_{BondDetails: &assets.GetAssetResponse_BondDetails{
			BondFaceValue: &decimal.Decimal{Value: "200000.0"},
			Currency:      "%",
		}},
	}
}

// currencyMocks serves one GetAsset answer and one GetAssetParams answer for
// every symbol, counting both calls.
type currencyMocks struct {
	asset      *assets.GetAssetResponse
	assetErr   error
	params     *assets.GetAssetParamsResponse
	paramsErr  error
	assetCalls atomic.Int64
	paramCalls atomic.Int64
}

func (m *currencyMocks) client() *Client {
	return &Client{
		assetsClient: &mockAssetsServiceClient{
			GetAssetFunc: func(ctx context.Context, in *assets.GetAssetRequest, opts ...grpc.CallOption) (*assets.GetAssetResponse, error) {
				m.assetCalls.Add(1)
				if m.assetErr != nil {
					return nil, m.assetErr
				}
				return m.asset, nil
			},
			GetAssetParamsFunc: func(ctx context.Context, in *assets.GetAssetParamsRequest, opts ...grpc.CallOption) (*assets.GetAssetParamsResponse, error) {
				m.paramCalls.Add(1)
				if m.paramsErr != nil {
					return nil, m.paramsErr
				}
				if m.params == nil {
					return &assets.GetAssetParamsResponse{Symbol: in.Symbol}, nil
				}
				return m.params, nil
			},
		},
		assetMicCache:       make(map[string]string),
		assetLotCache:       make(map[string]float64),
		tradeLotCache:       make(map[string]float64),
		instrumentNameCache: make(map[string]string),
	}
}

// TestGetInstrumentCurrency_FilledByLotResolution: resolving a position's lot
// already asks GetAsset, and that one answer now also files the currency — by
// full symbol and by ticker.
func TestGetInstrumentCurrency_FilledByLotResolution(t *testing.T) {
	m := &currencyMocks{asset: replacementBondAsset()}
	client := m.client()

	client.getFullSymbol("RU000A10A851@MISX", "acc1")

	want := models.InstrumentCurrency{Quote: "RUB", FaceValue: 200000}
	for _, key := range []string{"RU000A10A851@MISX", "RU000A10A851"} {
		got, ok := client.GetInstrumentCurrency(key)
		if !ok || got != want {
			t.Errorf("GetInstrumentCurrency(%q) = %+v, %v; want %+v, true", key, got, ok, want)
		}
	}
	if got := m.assetCalls.Load(); got != 1 {
		t.Errorf("GetAsset called %d times, want exactly the one lot resolution already made", got)
	}
}

// TestGetInstrumentCurrency_FilledByBareTickerResolution covers the other lot
// path: a bare ticker whose MIC is known but whose lot is not goes through
// resolveAssetLot, which must file the currency as well.
func TestGetInstrumentCurrency_FilledByBareTickerResolution(t *testing.T) {
	m := &currencyMocks{asset: &assets.GetAssetResponse{
		Ticker:        "SBER",
		Board:         "MISX",
		Mic:           "MISX",
		LotSize:       &decimal.Decimal{Value: "1"},
		QuoteCurrency: "RUB",
	}}
	client := m.client()
	client.assetMicCache["SBER"] = "SBER@MISX"

	client.getFullSymbol("SBER", "acc1")

	for _, key := range []string{"SBER", "SBER@MISX"} {
		got, ok := client.GetInstrumentCurrency(key)
		if !ok || got.Quote != "RUB" || got.FaceValue != 0 {
			t.Errorf("GetInstrumentCurrency(%q) = %+v, %v; want RUB with no face, true", key, got, ok)
		}
	}
}

// TestGetInstrumentCurrency_FilledByAssetInfo: the instrument profile makes the
// same GetAsset call, so opening a profile files the currency too.
func TestGetInstrumentCurrency_FilledByAssetInfo(t *testing.T) {
	m := &currencyMocks{asset: replacementBondAsset()}
	client := m.client()
	// Lots already known, so the only GetAsset is the profile's own.
	client.assetLotCache["RU000A10A851@MISX"] = 1
	client.tradeLotCache["RU000A10A851@MISX"] = 1

	if _, err := client.GetAssetInfo("acc1", "RU000A10A851@MISX"); err != nil {
		t.Fatalf("GetAssetInfo: %v", err)
	}

	got, ok := client.GetInstrumentCurrency("RU000A10A851@MISX")
	if !ok || got.Quote != "RUB" || got.FaceValue != 200000 {
		t.Errorf("GetInstrumentCurrency = %+v, %v; want RUB / 200000, true", got, ok)
	}
}

// TestGetInstrumentCurrency_EmptyAnswerIsCached: "the broker answered and named
// no currency" is a fact worth keeping, and different from "never asked".
func TestGetInstrumentCurrency_EmptyAnswerIsCached(t *testing.T) {
	m := &currencyMocks{asset: &assets.GetAssetResponse{
		Ticker:  "ROSN",
		Board:   "TQBR",
		Mic:     "MISX",
		LotSize: &decimal.Decimal{Value: "1"},
	}}
	client := m.client()

	client.getFullSymbol("ROSN@MISX", "acc1")

	got, ok := client.GetInstrumentCurrency("ROSN@MISX")
	if !ok {
		t.Fatal("an answer without a currency must still be cached")
	}
	if got != (models.InstrumentCurrency{}) {
		t.Errorf("GetInstrumentCurrency = %+v, want the zero value", got)
	}
}

// TestGetInstrumentCurrency_FailureNotCached: a failed GetAsset leaves nothing
// behind, so the next lot miss asks again.
func TestGetInstrumentCurrency_FailureNotCached(t *testing.T) {
	m := &currencyMocks{assetErr: status.Error(codes.Unavailable, "down")}
	client := m.client()

	client.getFullSymbol("RU000A10A851@MISX", "acc1")

	if got, ok := client.GetInstrumentCurrency("RU000A10A851@MISX"); ok {
		t.Errorf("GetInstrumentCurrency after a failure = %+v, true; want nothing cached", got)
	}
}

// TestGetInstrumentCurrency_NeverRequests: a cold read answers "unknown" on the
// spot. It is what lets the overview redraw on every tick for free.
func TestGetInstrumentCurrency_NeverRequests(t *testing.T) {
	m := &currencyMocks{asset: replacementBondAsset()}
	client := m.client()

	if _, ok := client.GetInstrumentCurrency("RU000A10A851@MISX"); ok {
		t.Error("cold cache answered a currency")
	}
	if _, ok := client.GetInstrumentCurrency(""); ok {
		t.Error("empty symbol answered a currency")
	}
	if got := m.assetCalls.Load() + m.paramCalls.Load(); got != 0 {
		t.Errorf("a cache read issued %d requests, want 0", got)
	}
}

// TestInstrumentCurrencyFromAsset_NilSafe: every field of the answer may be
// missing, and a face that does not parse to a positive finite number is "not
// reported", never a zero-value bond.
func TestInstrumentCurrencyFromAsset_NilSafe(t *testing.T) {
	bond := func(face *decimal.Decimal) *assets.GetAssetResponse {
		return &assets.GetAssetResponse{
			QuoteCurrency: " cny ",
			AssetDetails:  &assets.GetAssetResponse_BondDetails_{BondDetails: &assets.GetAssetResponse_BondDetails{BondFaceValue: face}},
		}
	}

	tests := []struct {
		name string
		resp *assets.GetAssetResponse
		want models.InstrumentCurrency
	}{
		{"nil response", nil, models.InstrumentCurrency{}},
		{"empty response", &assets.GetAssetResponse{}, models.InstrumentCurrency{}},
		{"quote is normalised", bond(&decimal.Decimal{Value: "1000"}), models.InstrumentCurrency{Quote: "CNY", FaceValue: 1000}},
		{"comma decimal face", bond(&decimal.Decimal{Value: "1000,5"}), models.InstrumentCurrency{Quote: "CNY", FaceValue: 1000.5}},
		{"nil face", bond(nil), models.InstrumentCurrency{Quote: "CNY"}},
		{"zero face", bond(&decimal.Decimal{Value: "0"}), models.InstrumentCurrency{Quote: "CNY"}},
		{"negative face", bond(&decimal.Decimal{Value: "-100"}), models.InstrumentCurrency{Quote: "CNY"}},
		{"NaN face", bond(&decimal.Decimal{Value: "NaN"}), models.InstrumentCurrency{Quote: "CNY"}},
		{"Inf face", bond(&decimal.Decimal{Value: "Inf"}), models.InstrumentCurrency{Quote: "CNY"}},
		{"unparsable face", bond(&decimal.Decimal{Value: "N/A"}), models.InstrumentCurrency{Quote: "CNY"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := instrumentCurrencyFromAsset(tt.resp); got != tt.want {
				t.Errorf("instrumentCurrencyFromAsset = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// rub builds a google.type.Money the way the API sends it: units and nanos
// with the same sign.
func rub(currency string, units int64, nanos int32) *money.Money {
	return &money.Money{CurrencyCode: currency, Units: units, Nanos: nanos}
}

// TestUnitValueFromParams checks the per-piece value against the real answers
// of 2026-09-10: margin × 100 / risk rate / trade lot.
func TestUnitValueFromParams(t *testing.T) {
	tests := []struct {
		name   string
		resp   *assets.GetAssetParamsResponse
		want   models.UnitValue
		wantOK bool
	}{
		{
			name: "rouble bond, dirty price",
			resp: &assets.GetAssetParamsResponse{
				LongRiskRate: &decimal.Decimal{Value: "75.0"}, LongInitialMargin: rub("RUB", 764, 430000000), TradeLotSize: 1,
			},
			want: models.UnitValue{Currency: "RUB", Value: 1019.24}, wantOK: true,
		},
		{
			name: "replacement bond: face and conversion already applied",
			resp: &assets.GetAssetParamsResponse{
				LongRiskRate: &decimal.Decimal{Value: "33.0"}, LongInitialMargin: rub("RUB", 5489206, 908900000), TradeLotSize: 1,
			},
			want: models.UnitValue{Currency: "RUB", Value: 16633960.33}, wantOK: true,
		},
		{
			name: "yuan bond settled in yuan",
			resp: &assets.GetAssetParamsResponse{
				LongRiskRate: &decimal.Decimal{Value: "25.0"}, LongInitialMargin: rub("CNY", 2387, 82500000), TradeLotSize: 1,
			},
			want: models.UnitValue{Currency: "CNY", Value: 9548.33}, wantOK: true,
		},
		{
			name: "lot of 1000 is divided out",
			resp: &assets.GetAssetParamsResponse{
				LongRiskRate: &decimal.Decimal{Value: "10.0"}, LongInitialMargin: rub("RUB", 1251, 350000000), TradeLotSize: 1000,
			},
			want: models.UnitValue{Currency: "RUB", Value: 12.5135}, wantOK: true,
		},
		{
			name: "no trade lot counts as one",
			resp: &assets.GetAssetParamsResponse{
				LongRiskRate: &decimal.Decimal{Value: "25"}, LongInitialMargin: rub("USD", 62, 887500000),
			},
			want: models.UnitValue{Currency: "USD", Value: 251.55}, wantOK: true,
		},
		{
			name: "short pair when the long one is unusable",
			resp: &assets.GetAssetParamsResponse{
				LongRiskRate: &decimal.Decimal{Value: "0"}, LongInitialMargin: rub("RUB", 100, 0),
				ShortRiskRate: &decimal.Decimal{Value: "100"}, ShortInitialMargin: rub("RUB", 1018, 940000000), TradeLotSize: 1,
			},
			want: models.UnitValue{Currency: "RUB", Value: 1018.94}, wantOK: true,
		},
		{
			name:   "no margin at all",
			resp:   &assets.GetAssetParamsResponse{LongRiskRate: &decimal.Decimal{Value: "25"}},
			wantOK: false,
		},
		{
			name:   "margin without a currency",
			resp:   &assets.GetAssetParamsResponse{LongRiskRate: &decimal.Decimal{Value: "25"}, LongInitialMargin: rub("", 10, 0)},
			wantOK: false,
		},
		{
			name:   "risk rate missing",
			resp:   &assets.GetAssetParamsResponse{LongInitialMargin: rub("RUB", 10, 0)},
			wantOK: false,
		},
		{
			name:   "risk rate negative",
			resp:   &assets.GetAssetParamsResponse{LongRiskRate: &decimal.Decimal{Value: "-5"}, LongInitialMargin: rub("RUB", 10, 0)},
			wantOK: false,
		},
		{
			name:   "risk rate not a number",
			resp:   &assets.GetAssetParamsResponse{LongRiskRate: &decimal.Decimal{Value: "NaN"}, LongInitialMargin: rub("RUB", 10, 0)},
			wantOK: false,
		},
		{
			name:   "zero margin",
			resp:   &assets.GetAssetParamsResponse{LongRiskRate: &decimal.Decimal{Value: "25"}, LongInitialMargin: rub("RUB", 0, 0)},
			wantOK: false,
		},
		{
			name:   "negative margin",
			resp:   &assets.GetAssetParamsResponse{LongRiskRate: &decimal.Decimal{Value: "25"}, LongInitialMargin: rub("RUB", -10, 0)},
			wantOK: false,
		},
		{name: "nil response", resp: nil, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := unitValueFromParams(tt.resp)
			if ok != tt.wantOK {
				t.Fatalf("unitValueFromParams ok = %v, want %v (got %+v)", ok, tt.wantOK, got)
			}
			if !ok {
				return
			}
			if got.Currency != tt.want.Currency || math.Abs(got.Value-tt.want.Value) > 0.005 {
				t.Errorf("unitValueFromParams = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestGetUnitValue_FilledByTradeLotFetch: the GetAssetParams call the terminal
// already makes for the trade lot files the per-piece value at no extra cost.
func TestGetUnitValue_FilledByTradeLotFetch(t *testing.T) {
	m := &currencyMocks{
		asset: replacementBondAsset(),
		params: &assets.GetAssetParamsResponse{
			LongRiskRate: &decimal.Decimal{Value: "33.0"}, LongInitialMargin: rub("RUB", 5489206, 908900000), TradeLotSize: 1,
		},
	}
	client := m.client()

	client.getFullSymbol("RU000A10A851@MISX", "acc1")

	for _, key := range []string{"RU000A10A851@MISX", "RU000A10A851"} {
		got, ok := client.GetUnitValue(key)
		if !ok || got.Currency != "RUB" || math.Abs(got.Value-16633960.33) > 0.005 {
			t.Errorf("GetUnitValue(%q) = %+v, %v; want RUB 16633960.33, true", key, got, ok)
		}
	}
	if got := m.paramCalls.Load(); got != 1 {
		t.Errorf("GetAssetParams called %d times, want exactly the one trade-lot fetch", got)
	}
}

// TestGetUnitValue_FilledByProfileParams: the instrument profile's own
// GetAssetParams files the value too.
func TestGetUnitValue_FilledByProfileParams(t *testing.T) {
	m := &currencyMocks{
		asset: replacementBondAsset(),
		params: &assets.GetAssetParamsResponse{
			LongRiskRate: &decimal.Decimal{Value: "75.0"}, LongInitialMargin: rub("RUB", 764, 430000000), TradeLotSize: 1,
		},
	}
	client := m.client()
	client.assetLotCache["RU000A10BF48@MISX"] = 1
	client.tradeLotCache["RU000A10BF48@MISX"] = 1

	if _, err := client.GetAssetParams("acc1", "RU000A10BF48@MISX"); err != nil {
		t.Fatalf("GetAssetParams: %v", err)
	}

	got, ok := client.GetUnitValue("RU000A10BF48@MISX")
	if !ok || got.Currency != "RUB" || math.Abs(got.Value-1019.24) > 0.005 {
		t.Errorf("GetUnitValue = %+v, %v; want RUB 1019.24, true", got, ok)
	}
}

// TestGetUnitValue_NotCachedWhenUnusable: a failed call and an answer with no
// usable margin both leave the value unknown rather than zero.
func TestGetUnitValue_NotCachedWhenUnusable(t *testing.T) {
	t.Run("failed call", func(t *testing.T) {
		m := &currencyMocks{asset: replacementBondAsset(), paramsErr: status.Error(codes.Unavailable, "down")}
		client := m.client()
		client.getFullSymbol("RU000A10A851@MISX", "acc1")
		if got, ok := client.GetUnitValue("RU000A10A851@MISX"); ok {
			t.Errorf("GetUnitValue after a failure = %+v, true; want nothing", got)
		}
	})
	t.Run("no margin in the answer", func(t *testing.T) {
		m := &currencyMocks{asset: replacementBondAsset(), params: &assets.GetAssetParamsResponse{TradeLotSize: 1}}
		client := m.client()
		client.getFullSymbol("RU000A10A851@MISX", "acc1")
		if got, ok := client.GetUnitValue("RU000A10A851@MISX"); ok {
			t.Errorf("GetUnitValue without a margin = %+v, true; want nothing", got)
		}
	})
	t.Run("cold read never requests", func(t *testing.T) {
		m := &currencyMocks{asset: replacementBondAsset()}
		client := m.client()
		if _, ok := client.GetUnitValue("RU000A10A851@MISX"); ok {
			t.Error("cold cache answered a value")
		}
		if _, ok := client.GetUnitValue(""); ok {
			t.Error("empty symbol answered a value")
		}
		if got := m.assetCalls.Load() + m.paramCalls.Load(); got != 0 {
			t.Errorf("a cache read issued %d requests, want 0", got)
		}
	})
}
