//go:build integration

package api

import (
	"math"
	"testing"

	"finam-terminal/api/testserver"
	"finam-terminal/models"
)

// TestIntegration_InstrumentCurrencyFromExistingCalls proves the currency layer
// rides on requests the terminal already makes. Loading an account resolves
// each position's lot through one GetAsset and one GetAssetParams; after that
// the quote currency, the face value and the per-piece value are all cache
// reads, and a second load or any number of reads issues nothing more.
func TestIntegration_InstrumentCurrencyFromExistingCalls(t *testing.T) {
	client, ts := setupTestServer(t)
	ts.Accounts.Positions["ACC001"] = testserver.CurrencyAccountPositions()

	if _, _, err := client.GetAccountDetails("ACC001"); err != nil {
		t.Fatalf("GetAccountDetails: %v", err)
	}

	positions := len(testserver.CurrencyAccountPositions())
	assetCalls := ts.Assets.GetAssetCallCount.Load()
	paramCalls := ts.Assets.GetAssetParamsCallCount.Load()
	// One lot resolution per position: exactly what the terminal did before
	// the currency layer existed.
	if assetCalls != int64(positions) || paramCalls != int64(positions) {
		t.Fatalf("first load: GetAsset=%d GetAssetParams=%d, want %d and %d", assetCalls, paramCalls, positions, positions)
	}

	tests := []struct {
		symbol   string
		currency models.InstrumentCurrency
		unit     models.UnitValue
	}{
		{"YDEX@MISX", models.InstrumentCurrency{Quote: "RUB"}, models.UnitValue{Currency: "RUB", Value: 3811}},
		{"RU000A10DQA8@MISX", models.InstrumentCurrency{Quote: "CNY", FaceValue: 10000}, models.UnitValue{Currency: "CNY", Value: 9548.33}},
		{"RU000A10A851@MISX", models.InstrumentCurrency{Quote: "RUB", FaceValue: 200000}, models.UnitValue{Currency: "RUB", Value: 16633960.33}},
		{"RU000A1087C3@MISX", models.InstrumentCurrency{Quote: "RUB", FaceValue: 100}, models.UnitValue{Currency: "RUB", Value: 1320.68}},
	}
	for _, tt := range tests {
		t.Run(tt.symbol, func(t *testing.T) {
			cur, ok := client.GetInstrumentCurrency(tt.symbol)
			if !ok || cur != tt.currency {
				t.Errorf("GetInstrumentCurrency = %+v, %v; want %+v, true", cur, ok, tt.currency)
			}
			unit, ok := client.GetUnitValue(tt.symbol)
			if !ok || unit.Currency != tt.unit.Currency || math.Abs(unit.Value-tt.unit.Value) > 0.01 {
				t.Errorf("GetUnitValue = %+v, %v; want %+v, true", unit, ok, tt.unit)
			}
		})
	}

	// A second tick and repeated reads cost nothing.
	if _, _, err := client.GetAccountDetails("ACC001"); err != nil {
		t.Fatalf("second GetAccountDetails: %v", err)
	}
	for i := 0; i < 20; i++ {
		client.GetInstrumentCurrency("RU000A10A851@MISX")
		client.GetUnitValue("RU000A10A851")
	}
	if got := ts.Assets.GetAssetCallCount.Load(); got != assetCalls {
		t.Errorf("GetAsset calls grew to %d after warm reads, want %d", got, assetCalls)
	}
	if got := ts.Assets.GetAssetParamsCallCount.Load(); got != paramCalls {
		t.Errorf("GetAssetParams calls grew to %d after warm reads, want %d", got, paramCalls)
	}
}

// TestIntegration_InstrumentCurrencyBrokerNamedNone: a GetAsset answer without
// quote_currency is still an answer — cached as "known, empty" so the next tick
// does not ask again.
func TestIntegration_InstrumentCurrencyBrokerNamedNone(t *testing.T) {
	client, _ := setupTestServer(t)

	if _, _, err := client.GetAccountDetails("ACC001"); err != nil {
		t.Fatalf("GetAccountDetails: %v", err)
	}

	// The default fixtures name no currency.
	cur, ok := client.GetInstrumentCurrency("SBER@TQBR")
	if !ok || cur != (models.InstrumentCurrency{}) {
		t.Errorf("GetInstrumentCurrency(SBER@TQBR) = %+v, %v; want zero value, true", cur, ok)
	}
	// Nor does the default GetAssetParams carry a margin.
	if unit, ok := client.GetUnitValue("SBER@TQBR"); ok {
		t.Errorf("GetUnitValue(SBER@TQBR) = %+v, true; want nothing", unit)
	}
}
