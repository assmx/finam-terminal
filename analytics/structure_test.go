package analytics

import (
	"math"
	"testing"

	"finam-terminal/models"
)

func pos(symbol, quantity, currentPrice string) models.Position {
	return models.Position{Symbol: symbol, Ticker: symbol, Quantity: quantity, CurrentPrice: currentPrice}
}

func quote(symbol, last string) *models.Quote {
	return &models.Quote{Symbol: symbol, Last: last}
}

func groupValue(a Allocation, name string) float64 {
	for _, g := range a.Groups {
		if g.Name == name {
			return g.Value
		}
	}
	return math.NaN()
}

func TestGroupForType(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"EQUITIES", "Акции"},
		{"BONDS", "Облигации"},
		{"FUNDS", "Фонды"},
		{"FUTURES", "Фьючерсы"},
		{"OPTIONS", "Опционы"},
		{"CURRENCIES", "Валюта"},
		// Values the reconnaissance found in the catalogue but the portfolio
		// grouping has no dedicated row for.
		{"INDICES", GroupOther},
		{"SPREADS", GroupOther},
		{"SWAPS", GroupOther},
		{"OTHER", GroupOther},
		// Unknown and absent both land in the same place.
		{"SOMETHING_NEW", GroupOther},
		{"", GroupOther},
		// The observed values are upper case, but the comparison does not rely
		// on that.
		{"equities", "Акции"},
		{"Bonds", "Облигации"},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := GroupForType(tt.in); got != tt.want {
				t.Errorf("GroupForType(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestStructure_SharesSumToOne is the headline promise of the screen: whatever
// the mix, the displayed shares add up to the whole portfolio.
func TestStructure_SharesSumToOne(t *testing.T) {
	in := StructureInput{
		Positions: []models.Position{
			pos("SBER@MISX", "100", "280"),
			pos("RU000A0JNPK5@MISX", "10", "990"),
			pos("SiH6@RTSX", "2", "5000"),
		},
		Quotes: map[string]*models.Quote{
			"SBER@MISX": quote("SBER@MISX", "285"),
		},
		Types: map[string]string{
			"SBER@MISX":         "EQUITIES",
			"RU000A0JNPK5@MISX": "BONDS",
			"SiH6@RTSX":         "FUTURES",
		},
		Cash: []models.CashBalance{{Currency: "RUB", Amount: 50000}},
	}

	got := Structure(in)
	if !got.Valid {
		t.Fatal("Valid = false, want true")
	}

	// 100*285 + 10*990 + 2*5000 + 50000 cash
	wantBase := 28500.0 + 9900 + 10000 + 50000
	if math.Abs(got.Base-wantBase) > 1e-9 {
		t.Errorf("Base = %v, want %v", got.Base, wantBase)
	}

	total := got.CashShare
	for _, g := range got.Groups {
		total += g.Share
	}
	if math.Abs(total-1) > 1e-9 {
		t.Errorf("shares sum to %v, want 1", total)
	}

	if v := groupValue(got, "Акции"); math.Abs(v-28500) > 1e-9 {
		t.Errorf("Акции value = %v, want 28500", v)
	}
	if v := groupValue(got, "Облигации"); math.Abs(v-9900) > 1e-9 {
		t.Errorf("Облигации value = %v, want 9900", v)
	}
	if math.Abs(got.Cash-50000) > 1e-9 {
		t.Errorf("Cash = %v, want 50000", got.Cash)
	}
	if got.PositionCount != 3 {
		t.Errorf("PositionCount = %d, want 3", got.PositionCount)
	}
}

// TestStructure_UnknownTypeIsOther keeps an instrument the bulk list never
// classified out of the wrong bucket rather than out of the total.
func TestStructure_UnknownTypeIsOther(t *testing.T) {
	got := Structure(StructureInput{
		Positions: []models.Position{
			pos("SBER@MISX", "10", "280"),
			pos("MYSTERY@MISX", "5", "100"),
		},
		Types: map[string]string{"SBER@MISX": "EQUITIES"},
	})

	if v := groupValue(got, GroupOther); math.Abs(v-500) > 1e-9 {
		t.Errorf("%s value = %v, want 500", GroupOther, v)
	}
	if math.Abs(got.Base-3300) > 1e-9 {
		t.Errorf("Base = %v, want 3300: an unclassified position still counts", got.Base)
	}
}

// TestStructure_GroupOrder pins the display order, so the screen does not
// reshuffle its rows between ticks as values move.
func TestStructure_GroupOrder(t *testing.T) {
	got := Structure(StructureInput{
		Positions: []models.Position{
			pos("A", "1", "100"),
			pos("B", "1", "100"),
			pos("C", "1", "100"),
			pos("D", "1", "100"),
		},
		Types: map[string]string{
			"A": "FUTURES",
			"B": "EQUITIES",
			"C": "WHATEVER",
			"D": "BONDS",
		},
	})

	want := []string{"Акции", "Облигации", "Фьючерсы", GroupOther}
	if len(got.Groups) != len(want) {
		t.Fatalf("len(Groups) = %d, want %d", len(got.Groups), len(want))
	}
	for i, name := range want {
		if got.Groups[i].Name != name {
			t.Errorf("Groups[%d].Name = %q, want %q", i, got.Groups[i].Name, name)
		}
	}
}

// TestStructure_EmptyGroupsAreOmitted keeps the panel short: a type nobody
// holds does not earn a zero row.
func TestStructure_EmptyGroupsAreOmitted(t *testing.T) {
	got := Structure(StructureInput{
		Positions: []models.Position{pos("SBER@MISX", "10", "280")},
		Types:     map[string]string{"SBER@MISX": "EQUITIES"},
	})

	if len(got.Groups) != 1 {
		t.Fatalf("len(Groups) = %d, want 1", len(got.Groups))
	}
	if got.Groups[0].Name != "Акции" {
		t.Errorf("Groups[0].Name = %q, want Акции", got.Groups[0].Name)
	}
}

// TestStructure_ZeroBase covers an account with nothing in it: shares are
// undefined and must be reported as such rather than divided by zero.
func TestStructure_ZeroBase(t *testing.T) {
	got := Structure(StructureInput{})

	if got.Valid {
		t.Error("Valid = true, want false for an empty portfolio")
	}
	if got.Base != 0 {
		t.Errorf("Base = %v, want 0", got.Base)
	}
	for _, g := range got.Groups {
		if math.IsNaN(g.Share) || math.IsInf(g.Share, 0) {
			t.Errorf("group %q has share %v", g.Name, g.Share)
		}
	}
	if len(got.Top) != 0 {
		t.Errorf("Top = %+v, want empty", got.Top)
	}
}

// TestStructure_PositionWithoutPrice counts a position nothing can value
// instead of silently treating it as worthless.
func TestStructure_PositionWithoutPrice(t *testing.T) {
	got := Structure(StructureInput{
		Positions: []models.Position{
			pos("SBER@MISX", "10", "280"),
			pos("GHOST@MISX", "5", "N/A"),
		},
		Types: map[string]string{"SBER@MISX": "EQUITIES"},
	})

	if got.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", got.Skipped)
	}
	if got.PositionCount != 1 {
		t.Errorf("PositionCount = %d, want 1: an unpriced position is not counted", got.PositionCount)
	}
	if math.Abs(got.Base-2800) > 1e-9 {
		t.Errorf("Base = %v, want 2800", got.Base)
	}
}

// TestStructure_NegativeCash keeps a margin loan out of the allocation base —
// borrowed money is not an asset — while still reporting it.
func TestStructure_NegativeCash(t *testing.T) {
	got := Structure(StructureInput{
		Positions: []models.Position{pos("SBER@MISX", "10", "280")},
		Types:     map[string]string{"SBER@MISX": "EQUITIES"},
		Cash:      []models.CashBalance{{Currency: "RUB", Amount: -5000}},
	})

	if got.Cash != 0 {
		t.Errorf("Cash = %v, want 0: a negative balance is a loan, not a holding", got.Cash)
	}
	if math.Abs(got.Base-2800) > 1e-9 {
		t.Errorf("Base = %v, want 2800", got.Base)
	}
	if got.CashShare != 0 {
		t.Errorf("CashShare = %v, want 0", got.CashShare)
	}
}

// TestStructure_BaseCurrency covers the three-step rule: RUB when present,
// otherwise the first entry, otherwise RUB.
func TestStructure_BaseCurrency(t *testing.T) {
	tests := []struct {
		name string
		cash []models.CashBalance
		want string
	}{
		{"RUB present but not first", []models.CashBalance{{Currency: "USD", Amount: 10}, {Currency: "RUB", Amount: 20}}, "RUB"},
		{"no RUB falls back to the first entry", []models.CashBalance{{Currency: "USD", Amount: 10}, {Currency: "CNY", Amount: 20}}, "USD"},
		{"no cash at all", nil, "RUB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Structure(StructureInput{Cash: tt.cash})
			if got.BaseCurrency != tt.want {
				t.Errorf("BaseCurrency = %q, want %q", got.BaseCurrency, tt.want)
			}
		})
	}
}

// TestStructure_CashOnlyBaseCurrency proves only the base currency's cash
// enters the base — there are no exchange rates in the API to convert the rest.
func TestStructure_CashOnlyBaseCurrency(t *testing.T) {
	got := Structure(StructureInput{
		Cash: []models.CashBalance{
			{Currency: "RUB", Amount: 1000},
			{Currency: "USD", Amount: 500},
		},
	})

	if math.Abs(got.Base-1000) > 1e-9 {
		t.Errorf("Base = %v, want 1000: the USD line cannot be converted", got.Base)
	}
	if math.Abs(got.CashShare-1) > 1e-9 {
		t.Errorf("CashShare = %v, want 1", got.CashShare)
	}
}

// TestStructure_ForeignCashReported keeps the money the base cannot include in
// sight: a positive balance in another currency is reported on its own rather
// than silently dropped. A loan stays with Borrowed, and an empty line is not
// money held.
func TestStructure_ForeignCashReported(t *testing.T) {
	got := Structure(StructureInput{
		Cash: []models.CashBalance{
			{Currency: "RUB", Amount: 1000},
			{Currency: "USD", Amount: 500},
			{Currency: "CNY", Amount: 0},
			{Currency: "EUR", Amount: -20},
		},
	})

	if len(got.ForeignCash) != 1 {
		t.Fatalf("ForeignCash = %+v, want only the USD line", got.ForeignCash)
	}
	if c := got.ForeignCash[0]; c.Currency != "USD" || c.Amount != 500 {
		t.Errorf("ForeignCash[0] = %+v, want USD 500", c)
	}
	if len(got.Borrowed) != 1 || got.Borrowed[0].Currency != "EUR" {
		t.Errorf("Borrowed = %+v, want the EUR loan", got.Borrowed)
	}
	if math.Abs(got.Base-1000) > 1e-9 {
		t.Errorf("Base = %v, want 1000: reporting the USD line must not fold it in", got.Base)
	}
}

// TestStructure_ForeignPositionsExcluded drops a position whose currency is
// known to differ from the base and has no rate, and reports how many were
// left out.
func TestStructure_ForeignPositionsExcluded(t *testing.T) {
	got := Structure(StructureInput{
		Positions: []models.Position{
			pos("SBER@MISX", "10", "280"),
			pos("AAPL@XNAS", "10", "200"),
			// Currency unknown: kept, because guessing it out would silently
			// shrink the portfolio.
			pos("MYSTERY@MISX", "1", "100"),
		},
		Types: map[string]string{"SBER@MISX": "EQUITIES"},
		Instruments: map[string]Instrument{
			"SBER@MISX": {Quote: "RUB"},
			"AAPL@XNAS": {Quote: "USD"},
		},
		Cash: []models.CashBalance{{Currency: "RUB", Amount: 0}},
	})

	if got.NoRateCount != 1 {
		t.Errorf("NoRateCount = %d, want 1", got.NoRateCount)
	}
	if math.Abs(got.Base-2900) > 1e-9 {
		t.Errorf("Base = %v, want 2900 (2800 + 100, without the USD position)", got.Base)
	}
	if got.PositionCount != 2 {
		t.Errorf("PositionCount = %d, want 2", got.PositionCount)
	}
}

// TestStructure_Sectors groups only the instruments the index composition
// knows, and says whether it knew anything at all.
func TestStructure_Sectors(t *testing.T) {
	in := StructureInput{
		Positions: []models.Position{
			pos("SBER@MISX", "10", "280"),
			pos("GAZP@MISX", "10", "160"),
			pos("MYSTERY@MISX", "10", "100"),
		},
		Types: map[string]string{
			"SBER@MISX": "EQUITIES",
			"GAZP@MISX": "EQUITIES",
		},
		Sectors: map[string]string{
			"SBER@MISX": "Финансы",
			"GAZP@MISX": "Нефть и газ",
		},
	}

	got := Structure(in)
	if !got.SectorsKnown {
		t.Fatal("SectorsKnown = false, want true when a composition was supplied")
	}
	if len(got.Sectors) != 3 {
		t.Fatalf("len(Sectors) = %d, want 3 (two named plus %s)", len(got.Sectors), GroupOther)
	}
	// Sectors sort by value, largest first.
	if got.Sectors[0].Name != "Финансы" {
		t.Errorf("Sectors[0].Name = %q, want Финансы", got.Sectors[0].Name)
	}
	if got.Sectors[len(got.Sectors)-1].Name != GroupOther {
		t.Errorf("last sector = %q, want %s", got.Sectors[len(got.Sectors)-1].Name, GroupOther)
	}

	// With no composition at all the caller must be able to say so instead of
	// showing an all-Прочее breakdown that looks like real data.
	in.Sectors = nil
	if bare := Structure(in); bare.SectorsKnown {
		t.Error("SectorsKnown = true with no composition, want false")
	}
}

// TestStructure_TopHoldings reports the three largest positions by share.
func TestStructure_TopHoldings(t *testing.T) {
	got := Structure(StructureInput{
		Positions: []models.Position{
			pos("SMALL", "1", "100"),
			pos("BIG", "1", "5000"),
			pos("MID", "1", "1000"),
			pos("TINY", "1", "10"),
		},
	})

	if len(got.Top) != 3 {
		t.Fatalf("len(Top) = %d, want 3", len(got.Top))
	}
	wantOrder := []string{"BIG", "MID", "SMALL"}
	for i, name := range wantOrder {
		if got.Top[i].Ticker != name {
			t.Errorf("Top[%d].Ticker = %q, want %q", i, got.Top[i].Ticker, name)
		}
	}
	if got.Top[0].Share <= got.Top[1].Share {
		t.Error("Top must be sorted by share, largest first")
	}
	if math.Abs(got.Top[0].Share-5000.0/6110.0) > 1e-9 {
		t.Errorf("Top[0].Share = %v, want %v", got.Top[0].Share, 5000.0/6110.0)
	}
}

// TestStructure_TopHoldingsFewerThanThree must not pad or panic.
func TestStructure_TopHoldingsFewerThanThree(t *testing.T) {
	got := Structure(StructureInput{
		Positions: []models.Position{pos("ONLY", "1", "100")},
	})

	if len(got.Top) != 1 {
		t.Fatalf("len(Top) = %d, want 1", len(got.Top))
	}
	if got.Top[0].Ticker != "ONLY" {
		t.Errorf("Top[0].Ticker = %q, want ONLY", got.Top[0].Ticker)
	}
}

// TestStructure_ShortPositionCountsByMagnitude keeps a short from cancelling
// out a long in the allocation base.
func TestStructure_ShortPositionCountsByMagnitude(t *testing.T) {
	got := Structure(StructureInput{
		Positions: []models.Position{
			pos("LONG", "10", "100"),
			pos("SHORT", "-10", "100"),
		},
	})

	if math.Abs(got.Base-2000) > 1e-9 {
		t.Errorf("Base = %v, want 2000: exposure adds up, it does not net out", got.Base)
	}
	for _, g := range got.Groups {
		if g.Value < 0 {
			t.Errorf("group %q has negative value %v", g.Name, g.Value)
		}
	}
}

// TestStructure_NoPanicOnEmptyInput is the package-wide contract: pure
// functions over possibly-empty broker data never panic and never emit NaN.
func TestStructure_NoPanicOnEmptyInput(t *testing.T) {
	inputs := []StructureInput{
		{},
		{Positions: []models.Position{}},
		{Positions: []models.Position{{}}},
		{Cash: []models.CashBalance{{}}},
		{Positions: []models.Position{pos("X", "", "")}},
	}

	for i, in := range inputs {
		got := Structure(in)
		if math.IsNaN(got.Base) || math.IsInf(got.Base, 0) {
			t.Errorf("input %d: Base = %v", i, got.Base)
		}
		for _, g := range got.Groups {
			if math.IsNaN(g.Share) || math.IsInf(g.Share, 0) {
				t.Errorf("input %d: group %q share = %v", i, g.Name, g.Share)
			}
		}
	}
}
