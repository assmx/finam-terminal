package analytics

import (
	"math"
	"testing"

	"finam-terminal/models"
)

// pnlPos is a position with everything Valuation reads.
func pnlPos(symbol, qty, avg, cur, daily, unrealized string) models.Position {
	return models.Position{
		Symbol: symbol, Ticker: symbol, Quantity: qty,
		AveragePrice: avg, CurrentPrice: cur,
		DailyPnL: daily, UnrealizedPnL: unrealized,
		MaintenanceMargin: "N/A",
	}
}

func rubAccount(equity, unrealized string) models.AccountInfo {
	return models.AccountInfo{
		Equity:        equity,
		UnrealizedPnL: unrealized,
		Cash:          []models.CashBalance{{Currency: "RUB", Amount: 0}},
	}
}

// TestValuation_ConvertsForeignDaily: a dollar position's day is in dollars —
// its unrealised result matches quantity × price move in dollars — and joins
// the rouble day at the rate.
func TestValuation_ConvertsForeignDaily(t *testing.T) {
	got := Valuation(ValuationInput{
		Account: rubAccount("100000", "1100"),
		Positions: []models.Position{
			pnlPos("YDEX@MISX", "10", "1000", "1010", "100", "100"),
			pnlPos("AMZN@XNGS", "10", "100", "110", "10", "100"),
		},
		Instruments: map[string]Instrument{"YDEX@MISX": {Quote: "RUB"}, "AMZN@XNGS": {Quote: "USD"}},
		Rates:       testRates,
	})

	if !got.Daily.Valid || !near(got.Daily.Value, 100+10*84) {
		t.Errorf("Daily = %+v, want 940 (100 RUB + 10 USD at 84)", got.Daily)
	}
	if got.DailyNoRate != 0 || !got.Opening.Valid {
		t.Errorf("DailyNoRate = %d, Opening = %+v; want 0 and a valid opening", got.DailyNoRate, got.Opening)
	}
	// Cost: 10 × 1000 RUB + 10 × 100 USD × 84.
	wantCost := 10000.0 + 1000*84
	if !got.UnrealizedShare.Valid || !near(got.UnrealizedShare.Value, 1100/wantCost) {
		t.Errorf("UnrealizedShare = %+v, want %v over the converted cost", got.UnrealizedShare, 1100/wantCost)
	}
}

// TestValuation_PnLAlreadyInBase: when the unrealised result only adds up with
// the rate applied, the broker has already converted this position's figures,
// and converting them again would multiply them by the rate twice.
func TestValuation_PnLAlreadyInBase(t *testing.T) {
	got := Valuation(ValuationInput{
		Account: rubAccount("100000", "8400"),
		Positions: []models.Position{
			// 10 × (110 − 100) = 100 USD, reported as 8 400: roubles.
			pnlPos("AMZN@XNGS", "10", "100", "110", "420", "8400"),
		},
		Instruments: map[string]Instrument{"AMZN@XNGS": {Quote: "USD"}},
		Rates:       testRates,
	})
	if !got.Daily.Valid || !near(got.Daily.Value, 420) {
		t.Errorf("Daily = %+v, want 420 taken as roubles", got.Daily)
	}
}

// TestValuation_AmbiguousPnLTakesTheRule: with no price move to compare, the
// check cannot tell, and the rule — the figure is in the position's currency —
// applies.
func TestValuation_AmbiguousPnLTakesTheRule(t *testing.T) {
	got := Valuation(ValuationInput{
		Account:     rubAccount("100000", "0"),
		Positions:   []models.Position{pnlPos("AMZN@XNGS", "10", "100", "100", "5", "0")},
		Instruments: map[string]Instrument{"AMZN@XNGS": {Quote: "USD"}},
		Rates:       testRates,
	})
	if !got.Daily.Valid || !near(got.Daily.Value, 5*84) {
		t.Errorf("Daily = %+v, want 420 (5 USD at 84)", got.Daily)
	}
}

// TestValuation_NoRateMakesTheDayPartial: a position whose result cannot be
// converted is counted apart — it did report — and, like a missing figure,
// refuses the opening value.
func TestValuation_NoRateMakesTheDayPartial(t *testing.T) {
	got := Valuation(ValuationInput{
		Account: rubAccount("100000", "150"),
		Positions: []models.Position{
			pnlPos("YDEX@MISX", "10", "1000", "1010", "100", "100"),
			pnlPos("0700@XHKG", "100", "400", "400.5", "50", "50"),
		},
		Instruments: map[string]Instrument{"YDEX@MISX": {Quote: "RUB"}, "0700@XHKG": {Quote: "HKD"}},
		Rates:       testRates,
	})
	if got.DailyNoRate != 1 || got.DailyUnreported != 0 {
		t.Errorf("DailyNoRate = %d, DailyUnreported = %d; want 1 and 0", got.DailyNoRate, got.DailyUnreported)
	}
	if !got.Daily.Valid || !near(got.Daily.Value, 100) {
		t.Errorf("Daily = %+v, want the rouble part, 100", got.Daily)
	}
	if got.Opening.Valid {
		t.Errorf("Opening = %+v, want refused: the day is partial", got.Opening)
	}
	if got.UnrealizedShare.Valid {
		t.Errorf("UnrealizedShare = %+v, want refused: one cost cannot be converted", got.UnrealizedShare)
	}
}

// TestValuation_UnresolvedFaceIsNoRate: a bond whose face currency is still
// being looked up cannot have its result converted either.
func TestValuation_UnresolvedFaceIsNoRate(t *testing.T) {
	got := Valuation(ValuationInput{
		Account:     rubAccount("20000000", "2500"),
		Positions:   []models.Position{pnlPos("RU000A10A851@MISX", "1", "96.0", "97.25", "10", "2500")},
		Instruments: map[string]Instrument{"RU000A10A851@MISX": instReplacement},
		Rates:       testRates,
	})
	if got.DailyNoRate != 1 {
		t.Errorf("DailyNoRate = %d, want 1", got.DailyNoRate)
	}
	if got.UnrealizedShare.Valid {
		t.Errorf("UnrealizedShare = %+v, want refused", got.UnrealizedShare)
	}
}

// TestValuation_BondCostThroughTheFace: a bond's average price is a percentage
// of face too, so its cost is quantity × price × face / 100 — the ЯНДЕКС1Р1
// position of the reconnaissance, whose −19 only adds up this way.
func TestValuation_BondCostThroughTheFace(t *testing.T) {
	got := Valuation(ValuationInput{
		Account:     rubAccount("67970.89", "-19"),
		Positions:   []models.Position{pnlPos("RU000A10BF48@MISX", "10", "100.89", "100.7", "-1", "-19")},
		Instruments: map[string]Instrument{"RU000A10BF48@MISX": instYandexBond},
		Rates:       testRates,
	})
	wantCost := 10 * 100.89 * 10
	if !got.UnrealizedShare.Valid || !near(got.UnrealizedShare.Value, -19/wantCost) {
		t.Errorf("UnrealizedShare = %+v, want %v over a cost of %v", got.UnrealizedShare, -19/wantCost, wantCost)
	}
	if !got.Daily.Valid || !near(got.Daily.Value, -1) {
		t.Errorf("Daily = %+v, want -1", got.Daily)
	}
}

// TestValuation_DollarFaceBondConverts: once the calendar names the dollar
// face, the replacement bond's result is dollars — it adds up as quantity ×
// move × face / 100 — and joins at the dollar rate.
func TestValuation_DollarFaceBondConverts(t *testing.T) {
	got := Valuation(ValuationInput{
		Account: rubAccount("20000000", "210000"),
		// 1 × (97.25 − 96.0) × 2000 = 2 500 USD.
		Positions:   []models.Position{pnlPos("RU000A10A851@MISX", "1", "96.0", "97.25", "100", "2500")},
		Instruments: map[string]Instrument{"RU000A10A851@MISX": withFace(instReplacement, "USD")},
		Rates:       testRates,
	})
	if !got.Daily.Valid || !near(got.Daily.Value, 100*84) {
		t.Errorf("Daily = %+v, want 8 400 (100 USD at 84)", got.Daily)
	}
	wantCost := 96.0 * 2000 * 84
	if !got.UnrealizedShare.Valid || !near(got.UnrealizedShare.Value, 210000/wantCost) {
		t.Errorf("UnrealizedShare = %+v, want %v", got.UnrealizedShare, 210000/wantCost)
	}
}

// TestValuation_NoInstrumentsIsTheOldBehaviour: without currency facts every
// position counts in the base currency at 1, exactly as before the currency
// layer.
func TestValuation_NoInstrumentsIsTheOldBehaviour(t *testing.T) {
	got := Valuation(ValuationInput{
		Account:   rubAccount("100000", "100"),
		Positions: []models.Position{pnlPos("X@MISX", "10", "100", "110", "30", "100")},
	})
	if !near(got.Daily.Value, 30) || !near(got.UnrealizedShare.Value, 100.0/1000) {
		t.Errorf("Worth = %+v, want the unconverted figures", got)
	}
	if math.IsNaN(got.Daily.Value) {
		t.Error("NaN")
	}
}

// TestPnLCurrency names the currency a position's own P&L figures are in, by
// the rule Valuation converts with — so the Positions tab labels exactly what
// the overview converts.
func TestPnLCurrency(t *testing.T) {
	tests := []struct {
		name string
		pos  models.Position
		inst Instrument
		want string
	}{
		{"rouble position", pnlPos("YDEX@MISX", "10", "1000", "1010", "100", "100"), Instrument{Quote: "RUB"}, "RUB"},
		{"dollar position, dollar figures", pnlPos("AMZN@XNGS", "10", "100", "110", "10", "100"), Instrument{Quote: "USD"}, "USD"},
		{"dollar position, figures already in roubles", pnlPos("AMZN@XNGS", "10", "100", "110", "420", "8400"), Instrument{Quote: "USD"}, "RUB"},
		{"no rate: the rule", pnlPos("0700@XHKG", "10", "100", "110", "5", "8400"), Instrument{Quote: "HKD"}, "HKD"},
		{"unknown currency: the base", pnlPos("X@MISX", "1", "1", "2", "1", "1"), Instrument{}, "RUB"},
		{"dollar-face bond", pnlPos("RU000A10A851@MISX", "1", "96.0", "97.25", "100", "2500"), withFace(instReplacement, "USD"), "USD"},
		{"unresolved face: not known", pnlPos("RU000A10A851@MISX", "1", "96.0", "97.25", "100", "2500"), instReplacement, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PnLCurrency(tt.pos, tt.inst, "RUB", testRates); got != tt.want {
				t.Errorf("PnLCurrency = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestStructure_ExposureIsPositionsOnly: leverage compares the positions with
// equity, so the exposure leaves every kind of cash out — roubles and bought
// currency alike.
func TestStructure_ExposureIsPositionsOnly(t *testing.T) {
	got := Structure(StructureInput{
		Positions:   []models.Position{pos("AMZN@XNGS", "1", "100"), pos("YDEX@MISX", "1", "1000")},
		Instruments: map[string]Instrument{"AMZN@XNGS": {Quote: "USD"}, "YDEX@MISX": {Quote: "RUB"}},
		Cash:        []models.CashBalance{{Currency: "RUB", Amount: 500}, {Currency: "USD", Amount: 10}},
		Rates:       testRates,
	})
	if !near(got.Exposure, 8400+1000) {
		t.Errorf("Exposure = %v, want 9400 — the two positions, converted", got.Exposure)
	}
	if !near(got.Base, 8400+1000+500+840) {
		t.Errorf("Base = %v, want positions plus both kinds of cash", got.Base)
	}
}
