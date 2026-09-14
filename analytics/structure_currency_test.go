package analytics

import (
	"fmt"
	"math"
	"math/rand/v2"
	"testing"
	"time"

	"finam-terminal/models"
)

var (
	rateAt = time.Date(2026, 9, 10, 17, 30, 5, 0, time.UTC)
	nowAt  = rateAt.Add(time.Hour)

	testRates = map[string]models.FXRate{
		"USD": {Currency: "USD", Rate: 84, At: rateAt},
		"CNY": {Currency: "CNY", Rate: 12.5, At: rateAt},
	}
)

func currencyRow(a Allocation, currency string) (CurrencyRow, bool) {
	for _, r := range a.Currencies {
		if r.Currency == currency && !r.Unresolved {
			return r, true
		}
	}
	return CurrencyRow{}, false
}

func near(a, b float64) bool {
	return math.Abs(a-b) <= 1e-6*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}

// TestStructure_ConvertsForeignHoldings: every holding joins the base in the
// base currency, the currency breakdown puts the base first and the rest by
// size, and both breakdowns add up to the same base.
func TestStructure_ConvertsForeignHoldings(t *testing.T) {
	got := Structure(StructureInput{
		Positions: []models.Position{
			pos("YDEX@MISX", "10", "1000"),        // 10 000 RUB
			pos("AMZN@XNGS", "1", "100"),          // 100 USD = 8 400 RUB
			pos("RU000A10DQA8@MISX", "2", "93.5"), // 18 700 CNY = 233 750 RUB
		},
		Types: map[string]string{"YDEX@MISX": "EQUITIES", "AMZN@XNGS": "EQUITIES", "RU000A10DQA8@MISX": "BONDS"},
		Instruments: map[string]Instrument{
			"YDEX@MISX":         {Quote: "RUB"},
			"AMZN@XNGS":         {Quote: "USD"},
			"RU000A10DQA8@MISX": instOFZCNY,
		},
		Cash:  []models.CashBalance{{Currency: "RUB", Amount: 1000}},
		Rates: testRates,
		Now:   nowAt,
	})

	wantBase := 10000.0 + 8400 + 233750 + 1000
	if !near(got.Base, wantBase) {
		t.Fatalf("Base = %v, want %v", got.Base, wantBase)
	}
	if v := groupValue(got, "Облигации"); !near(v, 233750) {
		t.Errorf("Облигации = %v, want 233750 (converted)", v)
	}

	var codes []string
	for _, r := range got.Currencies {
		codes = append(codes, r.Currency)
	}
	if fmt.Sprint(codes) != "[RUB CNY USD]" {
		t.Errorf("currency order = %v, want [RUB CNY USD]: base first, then by size", codes)
	}

	cny, _ := currencyRow(got, "CNY")
	if !near(cny.Value, 233750) || !near(cny.Native, 18700) || !cny.HasRate || !near(cny.Rate.Value, 12.5) || cny.Stale {
		t.Errorf("CNY row = %+v, want 233 750 RUB from 18 700 CNY at a fresh 12.5", cny)
	}
	rub, _ := currencyRow(got, "RUB")
	if !near(rub.Value, 11000) || !near(rub.Rate.Value, 1) {
		t.Errorf("RUB row = %+v, want the equity plus the cash, at 1", rub)
	}
	if got.NoRateCount != 0 || got.UnknownCurrencyCount != 0 {
		t.Errorf("counters = no rate %d, unknown %d; want 0 and 0", got.NoRateCount, got.UnknownCurrencyCount)
	}
}

// TestStructure_ForeignCashJoinsCurrencyGroup: money bought in another currency
// is a currency holding — the broker files it under «Валюта» — and roubles stay
// «Кэш».
func TestStructure_ForeignCashJoinsCurrencyGroup(t *testing.T) {
	got := Structure(StructureInput{
		Cash: []models.CashBalance{
			{Currency: "RUB", Amount: 298.27},
			{Currency: "USD", Amount: 0.19},
		},
		Rates: map[string]models.FXRate{"USD": {Currency: "USD", Rate: 84.26, At: rateAt}},
		Now:   nowAt,
	})

	if v := groupValue(got, "Валюта"); !near(v, 0.19*84.26) {
		t.Errorf("Валюта = %v, want %v", v, 0.19*84.26)
	}
	if !near(got.Cash, 298.27) {
		t.Errorf("Cash = %v, want only the roubles", got.Cash)
	}
	if !near(got.Base, 298.27+0.19*84.26) {
		t.Errorf("Base = %v, want roubles plus converted dollars", got.Base)
	}
	usd, ok := currencyRow(got, "USD")
	if !ok || !near(usd.Native, 0.19) {
		t.Errorf("USD row = %+v, %v; want the cash line", usd, ok)
	}
}

// TestStructure_NoRateLeftOut: a holding in a currency without a rate never
// enters the base — no sum ever adds two currencies without a rate — and is
// shown at the end in its own money, without a share.
func TestStructure_NoRateLeftOut(t *testing.T) {
	got := Structure(StructureInput{
		Positions: []models.Position{
			pos("YDEX@MISX", "10", "1000"),
			pos("0700@XHKG", "100", "425.6"),
		},
		Instruments: map[string]Instrument{
			"YDEX@MISX": {Quote: "RUB"},
			"0700@XHKG": {Quote: "HKD"},
		},
		// The empty rouble line makes the rouble the base, as on a real MC
		// account; alone, the HKD line would become the base itself.
		Cash:  []models.CashBalance{{Currency: "RUB", Amount: 0}, {Currency: "HKD", Amount: 50}},
		Rates: testRates,
	})

	if !near(got.Base, 10000) {
		t.Errorf("Base = %v, want 10000 without the HKD holdings", got.Base)
	}
	if got.NoRateCount != 1 {
		t.Errorf("NoRateCount = %d, want the one position", got.NoRateCount)
	}
	last := got.Currencies[len(got.Currencies)-1]
	if last.Currency != "HKD" || last.HasRate || last.Share != 0 || last.Value != 0 || !near(last.Native, 42560+50) {
		t.Errorf("last row = %+v, want HKD 42 610 in its own money, no rate, no share", last)
	}
}

// TestStructure_UnknownCurrencyCountedInBase: a currency the broker did not
// name is counted in the base currency, and the count says so.
func TestStructure_UnknownCurrencyCountedInBase(t *testing.T) {
	got := Structure(StructureInput{
		Positions: []models.Position{pos("X@MISX", "1", "500")},
		Rates:     testRates,
	})
	if !near(got.Base, 500) || got.UnknownCurrencyCount != 1 {
		t.Errorf("Base = %v, unknown = %d; want 500 counted in roubles, and 1", got.Base, got.UnknownCurrencyCount)
	}
	if rub, ok := currencyRow(got, "RUB"); !ok || !near(rub.Value, 500) {
		t.Errorf("RUB row = %+v, %v; want the position", rub, ok)
	}
}

// TestStructure_UnresolvedFaceRow: a bond with a foreign face whose currency is
// still being looked up joins the base at the broker's per-piece value and gets
// its own row between the rated currencies and the unrated ones.
func TestStructure_UnresolvedFaceRow(t *testing.T) {
	got := Structure(StructureInput{
		Positions: []models.Position{
			pos("YDEX@MISX", "10", "1000"),
			pos("RU000A10A851@MISX", "1", "97.25"),
			pos("0700@XHKG", "1", "400"),
		},
		Instruments: map[string]Instrument{
			"YDEX@MISX":         {Quote: "RUB"},
			"RU000A10A851@MISX": instReplacement,
			"0700@XHKG":         {Quote: "HKD"},
		},
		Rates: testRates,
	})

	if !near(got.Base, 10000+16633960.33) {
		t.Errorf("Base = %v, want the equity plus the bond at its per-piece value", got.Base)
	}
	if got.FaceUnresolvedCount != 1 {
		t.Errorf("FaceUnresolvedCount = %d, want 1", got.FaceUnresolvedCount)
	}
	if len(got.Currencies) != 3 {
		t.Fatalf("Currencies = %+v, want RUB, the unresolved row, HKD", got.Currencies)
	}
	if r := got.Currencies[1]; !r.Unresolved || !near(r.Value, 16633960.33) || r.Share <= 0 {
		t.Errorf("second row = %+v, want the unresolved bond with a share", r)
	}
	if got.Currencies[2].Currency != "HKD" {
		t.Errorf("third row = %+v, want the currency without a rate last", got.Currencies[2])
	}
}

// TestStructure_ResolvedFaceGoesToItsCurrency: once the calendar names the face
// currency, the bond is a dollar holding, valued through its face.
func TestStructure_ResolvedFaceGoesToItsCurrency(t *testing.T) {
	got := Structure(StructureInput{
		Positions:   []models.Position{pos("RU000A10A851@MISX", "1", "97.25")},
		Instruments: map[string]Instrument{"RU000A10A851@MISX": withFace(instReplacement, "USD")},
		Rates:       testRates,
	})
	usd, ok := currencyRow(got, "USD")
	if !ok || !near(usd.Native, 194500) || !near(usd.Value, 194500*84) {
		t.Errorf("USD row = %+v, %v; want 194 500 USD", usd, ok)
	}
	if got.FaceUnresolvedCount != 0 {
		t.Errorf("FaceUnresolvedCount = %d, want 0", got.FaceUnresolvedCount)
	}
}

// TestStructure_FaceUncheckedCounted: a bond with nothing to check its face
// against is valued in its quote currency and counted.
func TestStructure_FaceUncheckedCounted(t *testing.T) {
	got := Structure(StructureInput{
		Positions:   []models.Position{pos("B@MISX", "10", "100")},
		Instruments: map[string]Instrument{"B@MISX": {Quote: "RUB", FaceValue: 1000}},
	})
	if !near(got.Base, 10000) || got.FaceUncheckedCount != 1 {
		t.Errorf("Base = %v, unchecked = %d; want 10000 and 1", got.Base, got.FaceUncheckedCount)
	}
}

// TestStructure_NonRoubleBase: an account holding only dollars is measured in
// dollars; a rouble holding crosses through the dollar rate.
func TestStructure_NonRoubleBase(t *testing.T) {
	got := Structure(StructureInput{
		Positions:   []models.Position{pos("YDEX@MISX", "1", "8400"), pos("AMZN@XNGS", "1", "100")},
		Instruments: map[string]Instrument{"YDEX@MISX": {Quote: "RUB"}, "AMZN@XNGS": {Quote: "USD"}},
		Cash:        []models.CashBalance{{Currency: "USD", Amount: 50}},
		Rates:       testRates,
	})
	if got.BaseCurrency != "USD" {
		t.Fatalf("BaseCurrency = %q, want USD", got.BaseCurrency)
	}
	if !near(got.Base, 100+100+50) {
		t.Errorf("Base = %v, want 250 USD (8 400 RUB is 100 USD)", got.Base)
	}
	if got.Currencies[0].Currency != "USD" {
		t.Errorf("first row = %+v, want the base currency first", got.Currencies[0])
	}
}

// TestStructure_StaleRateMarked: a rate older than FXRateStaleAfter is marked.
func TestStructure_StaleRateMarked(t *testing.T) {
	got := Structure(StructureInput{
		Cash:  []models.CashBalance{{Currency: "RUB", Amount: 1}, {Currency: "USD", Amount: 1}},
		Rates: testRates,
		Now:   rateAt.Add(48 * time.Hour),
	})
	usd, _ := currencyRow(got, "USD")
	if !usd.Stale || !usd.Rate.At.Equal(rateAt) {
		t.Errorf("USD row = %+v, want a stale rate stamped %v", usd, rateAt)
	}
}

// TestStructure_ForeignLoanNotACurrencyHolding: a negative line is a loan and
// stays out of the currency breakdown, as it stays out of the base.
func TestStructure_ForeignLoanNotACurrencyHolding(t *testing.T) {
	got := Structure(StructureInput{
		Cash:  []models.CashBalance{{Currency: "RUB", Amount: 1000}, {Currency: "USD", Amount: -300}},
		Rates: testRates,
	})
	if _, ok := currencyRow(got, "USD"); ok {
		t.Error("a dollar loan appeared as a dollar holding")
	}
	if len(got.Borrowed) != 1 {
		t.Errorf("Borrowed = %+v, want the loan", got.Borrowed)
	}
}

// TestStructure_ForeignShortByMagnitude: a short in another currency adds to
// the size of the portfolio, like any short.
func TestStructure_ForeignShortByMagnitude(t *testing.T) {
	got := Structure(StructureInput{
		Positions:   []models.Position{pos("AMZN@XNGS", "-1", "100")},
		Instruments: map[string]Instrument{"AMZN@XNGS": {Quote: "USD"}},
		Rates:       testRates,
	})
	if !near(got.Base, 8400) {
		t.Errorf("Base = %v, want 8400 by magnitude", got.Base)
	}
}

// TestStructure_CurrencyOrderTiesByCode: equal amounts order by code, so rows
// never swap between ticks.
func TestStructure_CurrencyOrderTiesByCode(t *testing.T) {
	rates := map[string]models.FXRate{"USD": {Rate: 10}, "CNY": {Rate: 10}, "EUR": {Rate: 10}}
	for i := 0; i < 20; i++ {
		got := Structure(StructureInput{
			Cash: []models.CashBalance{
				{Currency: "USD", Amount: 1}, {Currency: "RUB", Amount: 1},
				{Currency: "EUR", Amount: 1}, {Currency: "CNY", Amount: 1},
				{Currency: "HKD", Amount: 5}, {Currency: "GBP", Amount: 5},
			},
			Rates: rates,
		})
		var codes []string
		for _, r := range got.Currencies {
			codes = append(codes, r.Currency)
		}
		if fmt.Sprint(codes) != "[RUB CNY EUR USD GBP HKD]" {
			t.Fatalf("order = %v, want [RUB CNY EUR USD GBP HKD]", codes)
		}
	}
}

// TestStructure_CurrencyInvariants is the property the whole breakdown rests
// on, checked over random portfolios:
//
//   - both breakdowns share one base: the rows with a rate (and the unresolved
//     row) add up to it, and so do the type groups plus the cash;
//   - both sets of shares add up to one;
//   - no sum adds currencies without a rate: adding any number of holdings in
//     unrated currencies changes neither the base nor a single share;
//   - no figure is ever NaN or Inf.
func TestStructure_CurrencyInvariants(t *testing.T) {
	r := rand.New(rand.NewPCG(20260910, 1))
	currencies := []string{"RUB", "USD", "CNY", "EUR", ""}
	unrated := []string{"HKD", "GBP", "JPY"}
	types := []string{"EQUITIES", "BONDS", "FUNDS", "CURRENCIES", "OTHER"}

	// Coverage of the generator itself: a property that never met an unrated
	// or unresolved row would pass without having checked anything.
	var sawValid, sawUnresolved, sawUnrated, sawForeignBase int
	defer func() {
		if sawValid == 0 || sawUnresolved == 0 || sawUnrated == 0 || sawForeignBase == 0 {
			t.Errorf("generator too narrow: valid=%d unresolved=%d unrated=%d non-rouble base=%d",
				sawValid, sawUnresolved, sawUnrated, sawForeignBase)
		}
	}()

	for iter := 0; iter < 500; iter++ {
		rates := map[string]models.FXRate{
			"USD": {Rate: 60 + r.Float64()*40, At: rateAt},
			"CNY": {Rate: 10 + r.Float64()*5, At: rateAt},
		}
		if r.IntN(2) == 0 {
			rates["EUR"] = models.FXRate{Rate: 80 + r.Float64()*30, At: rateAt}
		}

		in := StructureInput{
			Instruments: map[string]Instrument{},
			Types:       map[string]string{},
			Rates:       rates,
			Now:         nowAt,
		}
		for i := 0; i < r.IntN(8); i++ {
			sym := fmt.Sprintf("P%d@MISX", i)
			qty := fmt.Sprintf("%d", r.IntN(200)-50)
			price := fmt.Sprintf("%.2f", 1+r.Float64()*500)
			inst := Instrument{Quote: currencies[r.IntN(len(currencies))]}
			if r.IntN(3) == 0 {
				inst.FaceValue = 1000
				if r.IntN(2) == 0 {
					inst.Unit = models.UnitValue{Currency: "RUB", Value: r.Float64() * 200000}
				}
			}
			in.Positions = append(in.Positions, pos(sym, qty, price))
			in.Instruments[sym] = inst
			in.Types[sym] = types[r.IntN(len(types))]
		}
		for i := 0; i < r.IntN(4); i++ {
			in.Cash = append(in.Cash, models.CashBalance{
				Currency: currencies[r.IntN(len(currencies)-1)],
				Amount:   r.Float64()*20000 - 2000,
			})
		}

		got := Structure(in)
		checkFinite(t, iter, got)

		if got.BaseCurrency != "RUB" {
			sawForeignBase++
		}
		for _, row := range got.Currencies {
			if row.Unresolved {
				sawUnresolved++
			} else if !row.HasRate {
				sawUnrated++
			}
		}

		if got.Valid {
			sawValid++
			rowSum, rowShares := 0.0, 0.0
			for _, row := range got.Currencies {
				if row.HasRate || row.Unresolved {
					rowSum += row.Value
					rowShares += row.Share
				} else if row.Value != 0 || row.Share != 0 {
					t.Fatalf("iter %d: unrated row %+v carries a value or a share", iter, row)
				}
			}
			groupSum, groupShares := got.Cash, got.CashShare
			for _, g := range got.Groups {
				groupSum += g.Value
				groupShares += g.Share
			}
			if !near(rowSum, got.Base) || !near(groupSum, got.Base) {
				t.Fatalf("iter %d: currency rows %v, groups %v, base %v — one base expected", iter, rowSum, groupSum, got.Base)
			}
			if math.Abs(rowShares-1) > 1e-9 || math.Abs(groupShares-1) > 1e-9 {
				t.Fatalf("iter %d: shares sum to %v (currency) and %v (type), want 1", iter, rowShares, groupShares)
			}
		}

		// Holdings in currencies without a rate must leave everything as it was.
		polluted := in
		polluted.Positions = append([]models.Position(nil), in.Positions...)
		polluted.Instruments = map[string]Instrument{}
		for k, v := range in.Instruments {
			polluted.Instruments[k] = v
		}
		polluted.Cash = append([]models.CashBalance(nil), in.Cash...)
		for i := 0; i < 1+r.IntN(4); i++ {
			sym := fmt.Sprintf("U%d@XHKG", i)
			polluted.Positions = append(polluted.Positions, pos(sym, fmt.Sprintf("%d", 1+r.IntN(1000)), "123.45"))
			polluted.Instruments[sym] = Instrument{Quote: unrated[r.IntN(len(unrated))]}
			// Appended after an existing line, so the base currency — the
			// rouble, or else the first line — stays what it was.
			if len(in.Cash) > 0 {
				polluted.Cash = append(polluted.Cash, models.CashBalance{Currency: unrated[r.IntN(len(unrated))], Amount: 1 + r.Float64()*1e6})
			}
		}
		again := Structure(polluted)
		checkFinite(t, iter, again)
		if !near(again.Base, got.Base) {
			t.Fatalf("iter %d: unrated holdings moved the base from %v to %v", iter, got.Base, again.Base)
		}
		for _, g := range got.Groups {
			if v := groupValue(again, g.Name); !near(v, g.Value) {
				t.Fatalf("iter %d: unrated holdings moved group %s from %v to %v", iter, g.Name, g.Value, v)
			}
		}
	}
}

func checkFinite(t *testing.T, iter int, a Allocation) {
	t.Helper()
	bad := func(v float64) bool { return math.IsNaN(v) || math.IsInf(v, 0) }
	if bad(a.Base) || bad(a.Cash) || bad(a.CashShare) || bad(a.Exposure) {
		t.Fatalf("iter %d: non-finite total in %+v", iter, a)
	}
	for _, g := range append(append([]Group(nil), a.Groups...), a.Sectors...) {
		if bad(g.Value) || bad(g.Share) {
			t.Fatalf("iter %d: non-finite group %+v", iter, g)
		}
	}
	for _, row := range a.Currencies {
		if bad(row.Value) || bad(row.Share) || bad(row.Native) || bad(row.Rate.Value) {
			t.Fatalf("iter %d: non-finite currency row %+v", iter, row)
		}
	}
}
