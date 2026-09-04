package analytics

import (
	"math"
	"testing"
	"time"

	"finam-terminal/models"
)

var benchTo = time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

func bar(day time.Time, close float64) models.Bar {
	return models.Bar{Timestamp: day, Close: close, Open: close, High: close, Low: close}
}

// TestBenchmarkFromBars_Basics takes the first close on the horizon and the
// last one available, and derives both changes from them.
func TestBenchmarkFromBars_Basics(t *testing.T) {
	from := benchTo.AddDate(-2, 0, 0)

	first := []models.Bar{
		bar(from.AddDate(0, 0, 1), 2000),
		bar(from.AddDate(0, 0, 2), 2050),
	}
	last := []models.Bar{
		bar(benchTo.AddDate(0, 0, -2), 2380),
		bar(benchTo.AddDate(0, 0, -1), 2420),
	}

	b, ok := BenchmarkFromBars(first, last, from, benchTo)
	if !ok {
		t.Fatal("BenchmarkFromBars refused a complete pair of windows")
	}
	if !approx(b.FirstClose, 2000) || !approx(b.LastClose, 2420) {
		t.Errorf("closes = %v -> %v, want 2000 -> 2420", b.FirstClose, b.LastClose)
	}
	if !approx(b.Cumulative, 0.21) {
		t.Errorf("Cumulative = %v, want 0.21", b.Cumulative)
	}
	if !b.AnnualValid {
		t.Fatal("AnnualValid = false over a two-year horizon")
	}
	// 2420/2000 = 1.21 over almost exactly two years, so about 10% a year.
	if math.Abs(b.Annual-0.10) > 0.005 {
		t.Errorf("Annual = %v, want about 0.10", b.Annual)
	}
}

// TestBenchmarkFromBars_SkipsBarsBeforeTheHorizon: the request window is wider
// than the horizon on purpose (a seven-day window can be empty over the New
// Year holidays), so bars before the start must not be taken as the first one.
func TestBenchmarkFromBars_SkipsBarsBeforeTheHorizon(t *testing.T) {
	from := benchTo.AddDate(-1, 0, 0)

	first := []models.Bar{
		bar(from.AddDate(0, 0, -3), 1000), // before the horizon
		bar(from.AddDate(0, 0, -1), 1100),
		bar(from.AddDate(0, 0, 2), 2000), // the first one that counts
		bar(from.AddDate(0, 0, 3), 2100),
	}
	last := []models.Bar{bar(benchTo, 2200)}

	b, ok := BenchmarkFromBars(first, last, from, benchTo)
	if !ok {
		t.Fatal("BenchmarkFromBars refused")
	}
	if !approx(b.FirstClose, 2000) {
		t.Errorf("FirstClose = %v, want 2000 — bars before the horizon do not count", b.FirstClose)
	}
}

// TestBenchmarkFromBars_UnorderedInput does not assume the API sorted its
// answer.
func TestBenchmarkFromBars_UnorderedInput(t *testing.T) {
	from := benchTo.AddDate(-1, 0, 0)

	first := []models.Bar{
		bar(from.AddDate(0, 0, 5), 2100),
		bar(from.AddDate(0, 0, 1), 2000),
	}
	last := []models.Bar{
		bar(benchTo.AddDate(0, 0, -5), 2200),
		bar(benchTo.AddDate(0, 0, -1), 2300),
	}

	b, ok := BenchmarkFromBars(first, last, from, benchTo)
	if !ok {
		t.Fatal("BenchmarkFromBars refused")
	}
	if !approx(b.FirstClose, 2000) || !approx(b.LastClose, 2300) {
		t.Errorf("closes = %v -> %v, want 2000 -> 2300", b.FirstClose, b.LastClose)
	}
}

// TestBenchmarkFromBars_EmptyWindow refuses rather than inventing a price. The
// reconnaissance showed both ways this happens: a horizon older than the index
// itself, and a seven-day window landing entirely inside the January holidays.
func TestBenchmarkFromBars_EmptyWindow(t *testing.T) {
	from := benchTo.AddDate(-1, 0, 0)
	some := []models.Bar{bar(from.AddDate(0, 0, 1), 2000)}

	if _, ok := BenchmarkFromBars(nil, some, from, benchTo); ok {
		t.Error("an empty first window produced a benchmark")
	}
	if _, ok := BenchmarkFromBars(some, nil, from, benchTo); ok {
		t.Error("an empty last window produced a benchmark")
	}
	if _, ok := BenchmarkFromBars(nil, nil, from, benchTo); ok {
		t.Error("two empty windows produced a benchmark")
	}
}

// TestBenchmarkFromBars_AllBarsBeforeHorizon refuses: there is no first close
// on the horizon at all.
func TestBenchmarkFromBars_AllBarsBeforeHorizon(t *testing.T) {
	from := benchTo.AddDate(-1, 0, 0)

	first := []models.Bar{bar(from.AddDate(0, 0, -5), 2000)}
	last := []models.Bar{bar(benchTo, 2200)}

	if _, ok := BenchmarkFromBars(first, last, from, benchTo); ok {
		t.Error("a window entirely before the horizon produced a benchmark")
	}
}

// TestBenchmarkFromBars_ShortHorizon keeps the cumulative change and refuses to
// annualise. A week extrapolated to a year is arithmetic, not information.
func TestBenchmarkFromBars_ShortHorizon(t *testing.T) {
	from := benchTo.AddDate(0, 0, -10)

	first := []models.Bar{bar(from.AddDate(0, 0, 1), 2000)}
	last := []models.Bar{bar(benchTo, 2100)}

	b, ok := BenchmarkFromBars(first, last, from, benchTo)
	if !ok {
		t.Fatal("BenchmarkFromBars refused a short but complete horizon")
	}
	if !approx(b.Cumulative, 0.05) {
		t.Errorf("Cumulative = %v, want 0.05", b.Cumulative)
	}
	if b.AnnualValid {
		t.Errorf("Annual = %v, want it refused over ten days", b.Annual)
	}
}

// TestBenchmarkFromBars_ZeroClose refuses rather than dividing by zero.
func TestBenchmarkFromBars_ZeroClose(t *testing.T) {
	from := benchTo.AddDate(-1, 0, 0)

	first := []models.Bar{bar(from.AddDate(0, 0, 1), 0)}
	last := []models.Bar{bar(benchTo, 2200)}

	if _, ok := BenchmarkFromBars(first, last, from, benchTo); ok {
		t.Error("a zero opening close produced a benchmark")
	}

	negative := []models.Bar{bar(from.AddDate(0, 0, 1), -5)}
	if _, ok := BenchmarkFromBars(negative, last, from, benchTo); ok {
		t.Error("a negative close produced a benchmark")
	}
}

// TestBenchmarkFromBars_LastBeforeFirst refuses a pair of windows that run
// backwards, which would otherwise annualise over a negative span.
func TestBenchmarkFromBars_LastBeforeFirst(t *testing.T) {
	from := benchTo.AddDate(-1, 0, 0)

	first := []models.Bar{bar(benchTo.AddDate(0, 0, -1), 2000)}
	last := []models.Bar{bar(from.AddDate(0, 0, 1), 2200)}

	if _, ok := BenchmarkFromBars(first, last, from, benchTo); ok {
		t.Error("windows in the wrong order produced a benchmark")
	}
}

// TestBenchmarkFromBars_SameBar is a horizon that resolves to one instant: no
// change and nothing to annualise.
func TestBenchmarkFromBars_SameBar(t *testing.T) {
	from := benchTo.AddDate(-1, 0, 0)
	only := []models.Bar{bar(from.AddDate(0, 0, 1), 2000)}

	b, ok := BenchmarkFromBars(only, only, from, benchTo)
	if !ok {
		t.Fatal("a single shared bar was refused")
	}
	if !approx(b.Cumulative, 0) {
		t.Errorf("Cumulative = %v, want 0", b.Cumulative)
	}
	if b.AnnualValid {
		t.Error("AnnualValid = true over a zero-length span")
	}
}

// TestBenchmarkDifference states the comparison the Money screen prints: the
// account's annual rate minus the index's, in percentage points.
func TestBenchmarkDifference(t *testing.T) {
	b := Benchmark{Annual: 0.10, AnnualValid: true}

	if got := b.DifferencePP(0.135); math.Abs(got-3.5) > 1e-6 {
		t.Errorf("DifferencePP(0.135) = %v, want 3.5", got)
	}
	if got := b.DifferencePP(0.02); math.Abs(got-(-8)) > 1e-6 {
		t.Errorf("DifferencePP(0.02) = %v, want -8", got)
	}
}

// TestBenchmarkFromBars_NothingIsNaN over a spread of degenerate inputs.
func TestBenchmarkFromBars_NothingIsNaN(t *testing.T) {
	from := benchTo.AddDate(-1, 0, 0)

	cases := [][]models.Bar{
		{bar(from.AddDate(0, 0, 1), math.NaN())},
		{bar(from.AddDate(0, 0, 1), math.Inf(1))},
		{bar(time.Time{}, 2000)},
	}
	for i, first := range cases {
		b, ok := BenchmarkFromBars(first, []models.Bar{bar(benchTo, 2200)}, from, benchTo)
		if !ok {
			continue
		}
		if math.IsNaN(b.Cumulative) || math.IsInf(b.Cumulative, 0) ||
			math.IsNaN(b.Annual) || math.IsInf(b.Annual, 0) {
			t.Errorf("case %d produced %+v", i, b)
		}
	}
}
