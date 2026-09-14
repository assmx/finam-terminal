package analytics

import (
	"math"
	"testing"

	"finam-terminal/models"
)

// TestValuation_OpeningIsCurrentMinusDaily uses the figures from the broker's
// own terminal: 67 629.18 now and +43.26 for the day put the start of the day
// at 67 585.92, and the day at +0.06%.
func TestValuation_OpeningIsCurrentMinusDaily(t *testing.T) {
	got := Valuation(ValuationInput{
		Account: models.AccountInfo{Equity: "67629.18"},
		Positions: []models.Position{
			{Symbol: "SBER@MISX", DailyPnL: "40.00"},
			{Symbol: "GAZP@MISX", DailyPnL: "3.26"},
		},
	})

	if !got.Current.Valid || !approx(got.Current.Value, 67629.18) {
		t.Errorf("Current = %+v, want 67629.18", got.Current)
	}
	if !got.Daily.Valid || !approx(got.Daily.Value, 43.26) {
		t.Errorf("Daily = %+v, want 43.26", got.Daily)
	}
	if !got.Opening.Valid || !approx(got.Opening.Value, 67585.92) {
		t.Errorf("Opening = %+v, want 67585.92", got.Opening)
	}
	if !got.DailyShare.Valid || !approx(got.DailyShare.Value, 43.26/67585.92) {
		t.Errorf("DailyShare = %+v, want %v", got.DailyShare, 43.26/67585.92)
	}
}

// TestValuation_DailyPartial covers the broker leaving daily_pnl empty for a
// FORTS position. The day's figure is still worth showing, marked partial, but
// the opening value built from it would be wrong by exactly the missing part,
// so that one is refused.
func TestValuation_DailyPartial(t *testing.T) {
	got := Valuation(ValuationInput{
		Account: models.AccountInfo{Equity: "100000"},
		Positions: []models.Position{
			{Symbol: "SBER@MISX", DailyPnL: "500"},
			{Symbol: "SiZ6@RTSX", DailyPnL: "N/A"},
		},
	})

	if !got.Daily.Valid || !approx(got.Daily.Value, 500) {
		t.Errorf("Daily = %+v, want 500 from the position that reports it", got.Daily)
	}
	if got.DailyUnreported != 1 {
		t.Errorf("DailyUnreported = %d, want 1", got.DailyUnreported)
	}
	if got.Opening.Valid {
		t.Errorf("Opening = %+v, want invalid: the day's result is incomplete", got.Opening)
	}
	if got.DailyShare.Valid {
		t.Errorf("DailyShare = %+v, want invalid without an opening value", got.DailyShare)
	}
}

// TestValuation_DailyNotReported: when no position reports a day's result
// there is no figure at all — a zero would read as a flat day.
func TestValuation_DailyNotReported(t *testing.T) {
	got := Valuation(ValuationInput{
		Account: models.AccountInfo{Equity: "100000"},
		Positions: []models.Position{
			{Symbol: "SiZ6@RTSX", DailyPnL: "N/A"},
			{Symbol: "RIZ6@RTSX", DailyPnL: ""},
		},
	})

	if got.Daily.Valid {
		t.Errorf("Daily = %+v, want invalid when nothing reports it", got.Daily)
	}
	if got.DailyUnreported != 2 {
		t.Errorf("DailyUnreported = %d, want 2", got.DailyUnreported)
	}
	if got.Opening.Valid || got.DailyShare.Valid {
		t.Errorf("Opening/DailyShare must be invalid too: %+v / %+v", got.Opening, got.DailyShare)
	}
}

// TestValuation_NoPositionsIsAFlatDay: an account holding only money has
// nothing that could have moved, so its day is a real zero.
func TestValuation_NoPositionsIsAFlatDay(t *testing.T) {
	got := Valuation(ValuationInput{Account: models.AccountInfo{Equity: "250000"}})

	if !got.Daily.Valid || got.Daily.Value != 0 {
		t.Errorf("Daily = %+v, want a valid zero", got.Daily)
	}
	if !got.Opening.Valid || !approx(got.Opening.Value, 250000) {
		t.Errorf("Opening = %+v, want 250000", got.Opening)
	}
	if !got.DailyShare.Valid || got.DailyShare.Value != 0 {
		t.Errorf("DailyShare = %+v, want a valid zero", got.DailyShare)
	}
}

// TestValuation_EquityNotReported leaves every figure that depends on equity
// invalid, while the day's result, which does not, stays.
func TestValuation_EquityNotReported(t *testing.T) {
	got := Valuation(ValuationInput{
		Account:   models.AccountInfo{Equity: "N/A"},
		Positions: []models.Position{{Symbol: "SBER@MISX", DailyPnL: "10"}},
	})

	if got.Current.Valid {
		t.Errorf("Current = %+v, want invalid", got.Current)
	}
	if got.Opening.Valid || got.DailyShare.Valid {
		t.Errorf("Opening/DailyShare = %+v / %+v, want invalid without equity", got.Opening, got.DailyShare)
	}
	if !got.Daily.Valid || !approx(got.Daily.Value, 10) {
		t.Errorf("Daily = %+v, want 10", got.Daily)
	}
}

// TestValuation_NonPositiveOpeningHasNoShare: a percentage of nothing, or of a
// negative, is not a percentage.
func TestValuation_NonPositiveOpeningHasNoShare(t *testing.T) {
	got := Valuation(ValuationInput{
		Account:   models.AccountInfo{Equity: "100"},
		Positions: []models.Position{{Symbol: "SBER@MISX", DailyPnL: "100"}},
	})

	if !got.Opening.Valid || got.Opening.Value != 0 {
		t.Errorf("Opening = %+v, want a valid zero", got.Opening)
	}
	if got.DailyShare.Valid {
		t.Errorf("DailyShare = %+v, want invalid over a zero opening value", got.DailyShare)
	}
}

// TestValuation_UnrealizedShareOverCost measures the result on open positions
// against what they cost: 10 × 300 + 20 × 150 = 6 000 paid, −600 on paper.
func TestValuation_UnrealizedShareOverCost(t *testing.T) {
	got := Valuation(ValuationInput{
		Account: models.AccountInfo{Equity: "10000", UnrealizedPnL: "-600"},
		Positions: []models.Position{
			{Symbol: "SBER@MISX", Quantity: "10", AveragePrice: "300"},
			{Symbol: "GAZP@MISX", Quantity: "20", AveragePrice: "150"},
		},
	})

	if !got.Unrealized.Valid || !approx(got.Unrealized.Value, -600) {
		t.Errorf("Unrealized = %+v, want -600", got.Unrealized)
	}
	if !got.UnrealizedShare.Valid || !approx(got.UnrealizedShare.Value, -0.1) {
		t.Errorf("UnrealizedShare = %+v, want -0.1", got.UnrealizedShare)
	}
}

// TestValuation_ShortCostsByMagnitude: a short was opened at a price too, and
// what it put at stake is that price times its size.
func TestValuation_ShortCostsByMagnitude(t *testing.T) {
	got := Valuation(ValuationInput{
		Account:   models.AccountInfo{Equity: "10000", UnrealizedPnL: "100"},
		Positions: []models.Position{{Symbol: "SBER@MISX", Quantity: "-10", AveragePrice: "100"}},
	})

	if !got.UnrealizedShare.Valid || !approx(got.UnrealizedShare.Value, 0.1) {
		t.Errorf("UnrealizedShare = %+v, want 0.1 over a cost of 1000", got.UnrealizedShare)
	}
}

// TestValuation_UnrealizedShareNeedsEveryCost: the broker's unrealised figure
// covers every position, so a percentage over the cost of only some of them
// would overstate it. The amount stays; the percentage is refused.
func TestValuation_UnrealizedShareNeedsEveryCost(t *testing.T) {
	got := Valuation(ValuationInput{
		Account: models.AccountInfo{Equity: "10000", UnrealizedPnL: "-600"},
		Positions: []models.Position{
			{Symbol: "SBER@MISX", Quantity: "10", AveragePrice: "300"},
			{Symbol: "SiZ6@RTSX", Quantity: "1", AveragePrice: "N/A"},
		},
	})

	if !got.Unrealized.Valid {
		t.Errorf("Unrealized = %+v, want the reported amount", got.Unrealized)
	}
	if got.UnrealizedShare.Valid {
		t.Errorf("UnrealizedShare = %+v, want invalid with a cost missing", got.UnrealizedShare)
	}
}

// TestValuation_OverflowedCostHasNoShare: a cost that left the float range
// would divide any result down to a plausible-looking 0%.
func TestValuation_OverflowedCostHasNoShare(t *testing.T) {
	got := Valuation(ValuationInput{
		Account:   models.AccountInfo{Equity: "10000", UnrealizedPnL: "5"},
		Positions: []models.Position{{Symbol: "SBER@MISX", Quantity: "1e308", AveragePrice: "1e308"}},
	})

	if got.UnrealizedShare.Valid {
		t.Errorf("UnrealizedShare = %+v, want invalid over an overflowed cost", got.UnrealizedShare)
	}
}

// TestValuation_UnrealizedNotReported: an absent figure is Н/Д, not zero.
func TestValuation_UnrealizedNotReported(t *testing.T) {
	got := Valuation(ValuationInput{
		Account:   models.AccountInfo{Equity: "10000"},
		Positions: []models.Position{{Symbol: "SBER@MISX", Quantity: "10", AveragePrice: "300"}},
	})

	if got.Unrealized.Valid || got.UnrealizedShare.Valid {
		t.Errorf("Unrealized/Share = %+v / %+v, want invalid when not reported",
			got.Unrealized, got.UnrealizedShare)
	}
}

// TestValuation_UnrealizedWithoutPositions has an amount and no base.
func TestValuation_UnrealizedWithoutPositions(t *testing.T) {
	got := Valuation(ValuationInput{Account: models.AccountInfo{Equity: "10000", UnrealizedPnL: "0"}})

	if !got.Unrealized.Valid || got.Unrealized.Value != 0 {
		t.Errorf("Unrealized = %+v, want a valid zero", got.Unrealized)
	}
	if got.UnrealizedShare.Valid {
		t.Errorf("UnrealizedShare = %+v, want invalid with nothing held", got.UnrealizedShare)
	}
}

// TestValuation_FortsMargin sums the collateral the FORTS positions report and
// ignores the positions that report none.
func TestValuation_FortsMargin(t *testing.T) {
	got := Valuation(ValuationInput{
		Account: models.AccountInfo{Equity: "100000"},
		Positions: []models.Position{
			{Symbol: "SiZ6@RTSX", MaintenanceMargin: "15000.5"},
			{Symbol: "RIZ6@RTSX", MaintenanceMargin: "4999.5"},
			{Symbol: "SBER@MISX", MaintenanceMargin: "N/A"},
		},
	})

	if !got.FortsMargin.Valid || !approx(got.FortsMargin.Value, 20000) {
		t.Errorf("FortsMargin = %+v, want 20000", got.FortsMargin)
	}
}

// TestValuation_FortsMarginNotReported: an account with no FORTS positions has
// nothing to report, and the figure says so rather than claiming zero.
func TestValuation_FortsMarginNotReported(t *testing.T) {
	got := Valuation(ValuationInput{
		Account:   models.AccountInfo{Equity: "100000"},
		Positions: []models.Position{{Symbol: "SBER@MISX", MaintenanceMargin: "N/A"}},
	})

	if got.FortsMargin.Valid {
		t.Errorf("FortsMargin = %+v, want invalid when no position reports it", got.FortsMargin)
	}
}

// TestValuation_NeverNaNOrInf feeds values whose products and sums overflow,
// and the spellings strconv would accept as NaN or Inf.
func TestValuation_NeverNaNOrInf(t *testing.T) {
	got := Valuation(ValuationInput{
		Account: models.AccountInfo{Equity: "1e308", UnrealizedPnL: "NaN"},
		Positions: []models.Position{
			{Symbol: "A", Quantity: "1e308", AveragePrice: "1e308", DailyPnL: "-1e308", MaintenanceMargin: "1e308"},
			{Symbol: "B", Quantity: "1", AveragePrice: "Inf", DailyPnL: "-1e308", MaintenanceMargin: "1e308"},
		},
	})

	for name, m := range map[string]Metric{
		"Current": got.Current, "Daily": got.Daily, "Opening": got.Opening,
		"DailyShare": got.DailyShare, "Unrealized": got.Unrealized,
		"UnrealizedShare": got.UnrealizedShare, "FortsMargin": got.FortsMargin,
	} {
		if math.IsNaN(m.Value) || math.IsInf(m.Value, 0) {
			t.Errorf("%s = %v, want a finite value", name, m.Value)
		}
	}
	if got.Unrealized.Valid {
		t.Error("Unrealized must be invalid for a NaN spelling")
	}
}
