package analytics

import (
	"fmt"
	"math/rand/v2"
	"testing"

	"finam-terminal/models"
)

// blocked is a position the broker holds on a blocked venue and values at
// zero, priced by the broker's own figure as it arrives from GetAccount.
func blocked(symbol, quantity, currentPrice string) models.Position {
	p := pos(symbol, quantity, currentPrice)
	p.Blocked = true
	return p
}

// fxrl is the live position of 2026-09-11: 100 pieces at 21.83, the broker's
// result zeroed, the average price from before the block.
func fxrl() models.Position {
	p := blocked("FXRL", "100", "21.83")
	p.AveragePrice = "41.38"
	p.DailyPnL = "0.0"
	p.UnrealizedPnL = "0.0"
	return p
}

// TestStructure_BlockedLeftOutOfEveryBreakdown: the broker leaves a blocked
// position out of equity, so the overview leaves it out of everything it sums —
// the base, the type, sector and currency breakdowns, the concentration and
// the exposure — and reports it on its own: a count, and what it would be worth
// at the broker's price, by magnitude and in the base currency.
func TestStructure_BlockedLeftOutOfEveryBreakdown(t *testing.T) {
	short := blocked("AAPL.SPBZ@_SPBZ", "-3", "180")
	got := Structure(StructureInput{
		Positions: []models.Position{pos("SBER@MISX", "10", "100"), fxrl(), short},
		// A quote the stream could never deliver: the figure stays the
		// broker's, which is what the line on screen says it is.
		Quotes:      map[string]*models.Quote{"AAPL.SPBZ@_SPBZ": quote("AAPL.SPBZ@_SPBZ", "999")},
		Types:       map[string]string{"SBER@MISX": "EQUITIES", "FXRL": "FUNDS", "AAPL.SPBZ@_SPBZ": "EQUITIES"},
		Sectors:     map[string]string{"SBER@MISX": "Финансы", "FXRL": "Фонды"},
		Instruments: map[string]Instrument{"SBER@MISX": {Quote: "RUB"}},
		Cash:        []models.CashBalance{{Currency: "RUB", Amount: 83.05}},
		Now:         nowAt,
	})

	if !near(got.Base, 1083.05) {
		t.Errorf("Base = %v, want 1083.05 (SBER and the cash, nothing blocked)", got.Base)
	}
	if !near(got.Exposure, 1000) || got.PositionCount != 1 {
		t.Errorf("Exposure = %v over %d positions, want 1000 over 1", got.Exposure, got.PositionCount)
	}
	if len(got.Groups) != 1 || got.Groups[0].Name != "Акции" || !near(got.Groups[0].Value, 1000) {
		t.Errorf("Groups = %+v, want Акции 1000 alone", got.Groups)
	}
	if len(got.Sectors) != 1 || got.Sectors[0].Name != "Финансы" {
		t.Errorf("Sectors = %+v, want Финансы alone", got.Sectors)
	}
	if len(got.Top) != 1 || got.Top[0].Ticker != "SBER@MISX" {
		t.Errorf("Top = %+v, want SBER alone", got.Top)
	}
	if len(got.Currencies) != 1 || got.Currencies[0].Currency != "RUB" || !near(got.Currencies[0].Value, 1083.05) {
		t.Errorf("Currencies = %+v, want one RUB row of 1083.05", got.Currencies)
	}
	if got.UnknownCurrencyCount != 0 || got.Skipped != 0 || got.NoRateCount != 0 {
		t.Errorf("counters unknown=%d skipped=%d norate=%d, want 0: a blocked position is none of these",
			got.UnknownCurrencyCount, got.Skipped, got.NoRateCount)
	}

	if got.BlockedCount != 2 {
		t.Errorf("BlockedCount = %d, want 2", got.BlockedCount)
	}
	if want := 100*21.83 + 3*180.0; !near(got.BlockedValue, want) {
		t.Errorf("BlockedValue = %v, want %v (broker's price, by magnitude)", got.BlockedValue, want)
	}
}

// TestStructure_OnlyBlockedAndCash is the account …5519: FXRL and 83.05
// roubles. The broker's equity is the cash alone, and so is the overview's
// total now.
func TestStructure_OnlyBlockedAndCash(t *testing.T) {
	got := Structure(StructureInput{
		Positions: []models.Position{fxrl()},
		Cash:      []models.CashBalance{{Currency: "RUB", Amount: 83.05}},
	})

	if !got.Valid || !near(got.Base, 83.05) || !near(got.CashShare, 1) {
		t.Errorf("Valid=%v Base=%v CashShare=%v, want the cash alone at 100%%", got.Valid, got.Base, got.CashShare)
	}
	if len(got.Groups) != 0 || len(got.Top) != 0 {
		t.Errorf("Groups=%+v Top=%+v, want none", got.Groups, got.Top)
	}
	if got.BlockedCount != 1 || !near(got.BlockedValue, 2183) {
		t.Errorf("blocked %d worth %v, want 1 worth 2183", got.BlockedCount, got.BlockedValue)
	}
}

// TestStructure_OnlyBlocked has no base at all, and still says what is frozen.
func TestStructure_OnlyBlocked(t *testing.T) {
	got := Structure(StructureInput{Positions: []models.Position{fxrl()}})

	if got.Valid {
		t.Errorf("Valid with nothing valued: %+v", got)
	}
	if got.BlockedCount != 1 || !near(got.BlockedValue, 2183) {
		t.Errorf("blocked %d worth %v, want 1 worth 2183", got.BlockedCount, got.BlockedValue)
	}
}

// TestStructure_BlockedValueNeedsPriceAndRate: a blocked position the broker
// sent no readable price for, or one in a currency without a rate, is counted
// and left out of the amount — the amount never adds two currencies either.
func TestStructure_BlockedValueNeedsPriceAndRate(t *testing.T) {
	got := Structure(StructureInput{
		Positions: []models.Position{
			blocked("FXRL", "100", "N/A"),
			blocked("0700.SPBZ@_SPBZ", "10", "400"),
			blocked("AAPL.SPBZ@_SPBZ", "3", "180"),
		},
		Instruments: map[string]Instrument{
			"0700.SPBZ@_SPBZ": {Quote: "HKD"},
			"AAPL.SPBZ@_SPBZ": {Quote: "USD"},
		},
		Rates: testRates,
		Now:   nowAt,
	})

	if got.BlockedCount != 3 {
		t.Errorf("BlockedCount = %d, want 3", got.BlockedCount)
	}
	if want := 3 * 180 * 84.0; !near(got.BlockedValue, want) {
		t.Errorf("BlockedValue = %v, want %v (the dollar one converted, the other two counted only)", got.BlockedValue, want)
	}
	if got.Skipped != 0 || got.NoRateCount != 0 {
		t.Errorf("Skipped=%d NoRateCount=%d, want 0: those count positions in the base", got.Skipped, got.NoRateCount)
	}
}

// TestStructure_BlockedNeverMovesTheBase adds blocked positions of every kind to
// random portfolios: the base, both breakdowns and the exposure stay as they
// were, and every one of them is counted.
func TestStructure_BlockedNeverMovesTheBase(t *testing.T) {
	r := rand.New(rand.NewPCG(20260911, 1))
	currencies := []string{"RUB", "USD", "CNY", ""}

	for iter := 0; iter < 300; iter++ {
		in := StructureInput{
			Instruments: map[string]Instrument{},
			Types:       map[string]string{},
			Rates:       testRates,
			Now:         nowAt,
		}
		for i := 0; i < r.IntN(6); i++ {
			sym := fmt.Sprintf("P%d@MISX", i)
			in.Positions = append(in.Positions, pos(sym, fmt.Sprintf("%d", r.IntN(200)-50), fmt.Sprintf("%.2f", 1+r.Float64()*500)))
			in.Instruments[sym] = Instrument{Quote: currencies[r.IntN(len(currencies))]}
			in.Types[sym] = "EQUITIES"
		}
		if r.IntN(2) == 0 {
			in.Cash = []models.CashBalance{{Currency: "RUB", Amount: r.Float64() * 10000}}
		}
		got := Structure(in)

		withBlocked := in
		withBlocked.Positions = append([]models.Position(nil), in.Positions...)
		withBlocked.Instruments = map[string]Instrument{}
		for k, v := range in.Instruments {
			withBlocked.Instruments[k] = v
		}
		added := 1 + r.IntN(4)
		for i := 0; i < added; i++ {
			sym := fmt.Sprintf("B%d.MMBZ@_MMBZ", i)
			if r.IntN(2) == 0 {
				sym = fmt.Sprintf("B%d", i) // sent without a MIC, like FXRL
			}
			withBlocked.Positions = append(withBlocked.Positions, blocked(sym, fmt.Sprintf("%d", r.IntN(2000)-500), fmt.Sprintf("%.2f", r.Float64()*300)))
			withBlocked.Instruments[sym] = Instrument{Quote: currencies[r.IntN(len(currencies))]}
		}
		again := Structure(withBlocked)
		checkFinite(t, iter, again)

		if again.Valid != got.Valid || !near(again.Base, got.Base) || !near(again.Exposure, got.Exposure) {
			t.Fatalf("iter %d: blocked positions moved base %v→%v, exposure %v→%v", iter, got.Base, again.Base, got.Exposure, again.Exposure)
		}
		if len(again.Groups) != len(got.Groups) || len(again.Currencies) != len(got.Currencies) {
			t.Fatalf("iter %d: blocked positions changed the rows: groups %d→%d, currencies %d→%d",
				iter, len(got.Groups), len(again.Groups), len(got.Currencies), len(again.Currencies))
		}
		for i, row := range got.Currencies {
			if !near(again.Currencies[i].Value, row.Value) || !near(again.Currencies[i].Native, row.Native) {
				t.Fatalf("iter %d: blocked positions moved currency row %+v to %+v", iter, row, again.Currencies[i])
			}
		}
		if again.BlockedCount != added {
			t.Fatalf("iter %d: BlockedCount = %d, want %d", iter, again.BlockedCount, added)
		}
		if again.BlockedValue < 0 {
			t.Fatalf("iter %d: BlockedValue = %v, want a magnitude", iter, again.BlockedValue)
		}
	}
}

// TestValuation_BlockedLeftOut: the broker's equity and unrealised result both
// leave the blocked position out (…5519: equity equals the cash, unrealized
// 0.0 although FXRL's price fell by half), so the cost the unrealised share is
// measured against leaves it out too, and so does the day.
func TestValuation_BlockedLeftOut(t *testing.T) {
	sber := pos("SBER@MISX", "10", "103")
	sber.AveragePrice = "90"
	sber.DailyPnL = "5"
	aapl := blocked("AAPL.SPBZ@_SPBZ", "3", "180")
	aapl.AveragePrice = "170"
	aapl.DailyPnL = "N/A"

	w := Valuation(ValuationInput{
		Account:   models.AccountInfo{Equity: "1113.05", UnrealizedPnL: "130"},
		Positions: []models.Position{sber, fxrl(), aapl},
	})

	if !w.Daily.Valid || !near(w.Daily.Value, 5) || w.DailyUnreported != 0 || w.DailyNoRate != 0 {
		t.Errorf("Daily = %+v (unreported %d, no rate %d), want 5 in full", w.Daily, w.DailyUnreported, w.DailyNoRate)
	}
	if !w.Opening.Valid || !near(w.Opening.Value, 1108.05) {
		t.Errorf("Opening = %+v, want 1108.05", w.Opening)
	}
	if !w.UnrealizedShare.Valid || !near(w.UnrealizedShare.Value, 130.0/900) {
		t.Errorf("UnrealizedShare = %+v, want 130/900 (SBER's cost alone)", w.UnrealizedShare)
	}
}

// TestValuation_OnlyBlockedIsAFlatDay: an account holding nothing but a blocked
// position holds nothing the broker values, so its day is a real zero, as for
// an account holding nothing at all — even when the blocked position reports
// no day's result — and there is no cost to measure a share against.
func TestValuation_OnlyBlockedIsAFlatDay(t *testing.T) {
	p := fxrl()
	p.DailyPnL = "N/A"

	w := Valuation(ValuationInput{
		Account:   models.AccountInfo{Equity: "83.05", UnrealizedPnL: "0.0"},
		Positions: []models.Position{p},
	})

	if !w.Daily.Valid || w.Daily.Value != 0 || w.DailyUnreported != 0 {
		t.Errorf("Daily = %+v (unreported %d), want a real zero", w.Daily, w.DailyUnreported)
	}
	if !w.Opening.Valid || !near(w.Opening.Value, 83.05) {
		t.Errorf("Opening = %+v, want 83.05", w.Opening)
	}
	if w.UnrealizedShare.Valid {
		t.Errorf("UnrealizedShare = %+v, want refused: nothing valued has a cost", w.UnrealizedShare)
	}
}

// TestRatesToFetch_SkipsBlocked: a blocked holding needs no rate — it is in no
// sum — so it costs no request.
func TestRatesToFetch_SkipsBlocked(t *testing.T) {
	in := StructureInput{
		Positions:   []models.Position{pos("SBER@MISX", "1", "100"), blocked("AAPL.SPBZ@_SPBZ", "3", "180")},
		Instruments: map[string]Instrument{"SBER@MISX": {Quote: "RUB"}, "AAPL.SPBZ@_SPBZ": {Quote: "USD"}},
	}
	if got := RatesToFetch(in); len(got) != 0 {
		t.Errorf("RatesToFetch = %v, want none", got)
	}
}

// TestBondsNeedingFace_SkipsBlocked: nor does a blocked bond get a calendar
// request for its face currency.
func TestBondsNeedingFace_SkipsBlocked(t *testing.T) {
	bond := blocked("XS000.MMBZ@_MMBZ", "5", "90")
	in := StructureInput{
		Positions:   []models.Position{bond},
		Instruments: map[string]Instrument{bond.Symbol: {Quote: "RUB", FaceValue: 1000, Unit: models.UnitValue{Currency: "RUB", Value: 76000}}},
	}
	if got := BondsNeedingFace(in); len(got) != 0 {
		t.Errorf("BondsNeedingFace = %v, want none", got)
	}
}

// TestExpectedPayouts_BlockedSkipped: a blocked holding pays its holder nothing
// the terminal can promise, and the forecast does not list it even when a
// calendar is at hand.
func TestExpectedPayouts_BlockedSkipped(t *testing.T) {
	p := holding("AAPL.SPBZ@_SPBZ", "AAPL.SPBZ", "3")
	p.Blocked = true

	payouts, totals := ExpectedPayouts(
		[]models.Position{p},
		map[string][]models.Dividend{"AAPL.SPBZ@_SPBZ": {futureDividend(20, "0.26", "USD")}},
		nil, payoutNow,
	)
	if len(payouts) != 0 || len(totals.In90) != 0 {
		t.Errorf("payouts = %+v, totals = %+v, want none", payouts, totals)
	}
}
