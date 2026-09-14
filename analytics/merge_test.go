package analytics

import (
	"testing"
	"time"

	"finam-terminal/models"
)

var mergeBase = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

func mtrade(id string, hours int) models.Trade {
	return models.Trade{ID: id, Symbol: "SBER@MISX", Timestamp: mergeBase.Add(time.Duration(hours) * time.Hour)}
}

func mtx(id string, hours int) models.Transaction {
	return models.Transaction{ID: id, Timestamp: mergeBase.Add(time.Duration(hours) * time.Hour)}
}

func tradeIDs(in []models.Trade) []string {
	out := make([]string, len(in))
	for i, t := range in {
		out[i] = t.ID
	}
	return out
}

func txIDs(in []models.Transaction) []string {
	out := make([]string, len(in))
	for i, t := range in {
		out[i] = t.ID
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestMergeTrades_OverlappingWindows is what the R key produces: a tail
// reloaded over the last day, overlapping records the cache already holds.
func TestMergeTrades_OverlappingWindows(t *testing.T) {
	cached := []models.Trade{mtrade("a", 0), mtrade("b", 1), mtrade("c", 2)}
	tail := []models.Trade{mtrade("c", 2), mtrade("d", 3)}

	got := MergeTrades(cached, tail)
	if want := []string{"a", "b", "c", "d"}; !equalStrings(tradeIDs(got), want) {
		t.Errorf("ids = %v, want %v", tradeIDs(got), want)
	}
}

// TestMergeTrades_Sorts puts the result in time order whatever order the parts
// arrived in — the loader walks backwards, so they arrive newest first.
func TestMergeTrades_Sorts(t *testing.T) {
	got := MergeTrades(
		[]models.Trade{mtrade("c", 2), mtrade("a", 0)},
		[]models.Trade{mtrade("d", 3), mtrade("b", 1)},
	)
	if want := []string{"a", "b", "c", "d"}; !equalStrings(tradeIDs(got), want) {
		t.Errorf("ids = %v, want %v", tradeIDs(got), want)
	}
}

// TestMergeTrades_TiesBreakOnID keeps the order deterministic when two records
// share an instant, which is what stops the FIFO result from wobbling between
// refreshes.
func TestMergeTrades_TiesBreakOnID(t *testing.T) {
	got := MergeTrades(
		[]models.Trade{mtrade("z", 0)},
		[]models.Trade{mtrade("a", 0)},
	)
	if want := []string{"a", "z"}; !equalStrings(tradeIDs(got), want) {
		t.Errorf("ids = %v, want %v", tradeIDs(got), want)
	}
}

// TestMergeTrades_FirstWins keeps the copy already held when a record arrives
// twice. A re-fetch of the same id is the same trade; preferring the newer copy
// would only add a way for a partial second answer to overwrite a full first.
func TestMergeTrades_FirstWins(t *testing.T) {
	held := mtrade("a", 0)
	held.Price = "100"
	refetched := mtrade("a", 0)
	refetched.Price = ""

	got := MergeTrades([]models.Trade{held}, []models.Trade{refetched})
	if len(got) != 1 {
		t.Fatalf("got %d trades, want 1", len(got))
	}
	if got[0].Price != "100" {
		t.Errorf("Price = %q, want the copy already held", got[0].Price)
	}
}

// TestMergeTrades_KeepsRecordsWithoutIDs. An id-less record cannot be
// deduplicated, and dropping every one but the first would delete real trades.
func TestMergeTrades_KeepsRecordsWithoutIDs(t *testing.T) {
	got := MergeTrades(
		[]models.Trade{{Symbol: "SBER@MISX", Timestamp: mergeBase}},
		[]models.Trade{{Symbol: "GAZP@MISX", Timestamp: mergeBase.Add(time.Hour)}},
	)
	if len(got) != 2 {
		t.Errorf("got %d trades, want both kept: %+v", len(got), got)
	}
}

// TestMergeTrades_Empty handles every empty combination.
func TestMergeTrades_Empty(t *testing.T) {
	if got := MergeTrades(nil, nil); len(got) != 0 {
		t.Errorf("MergeTrades(nil, nil) = %+v, want empty", got)
	}
	one := []models.Trade{mtrade("a", 0)}
	if got := MergeTrades(one, nil); !equalStrings(tradeIDs(got), []string{"a"}) {
		t.Errorf("MergeTrades(one, nil) = %v", tradeIDs(got))
	}
	if got := MergeTrades(nil, one); !equalStrings(tradeIDs(got), []string{"a"}) {
		t.Errorf("MergeTrades(nil, one) = %v", tradeIDs(got))
	}
}

// TestMergeTrades_DoesNotMutateInputs: the cache slice handed in must come back
// unchanged, since the caller keeps holding it.
func TestMergeTrades_DoesNotMutateInputs(t *testing.T) {
	cached := []models.Trade{mtrade("c", 2), mtrade("a", 0)}
	before := tradeIDs(cached)

	MergeTrades(cached, []models.Trade{mtrade("b", 1)})

	if !equalStrings(tradeIDs(cached), before) {
		t.Errorf("the input was reordered: %v, was %v", tradeIDs(cached), before)
	}
}

// TestMergeTransactions mirrors the trade behaviour on the other record type.
func TestMergeTransactions(t *testing.T) {
	got := MergeTransactions(
		[]models.Transaction{mtx("b", 1), mtx("a", 0)},
		[]models.Transaction{mtx("b", 1), mtx("c", 2)},
	)
	if want := []string{"a", "b", "c"}; !equalStrings(txIDs(got), want) {
		t.Errorf("ids = %v, want %v", txIDs(got), want)
	}
}

// TestMergeTransactions_Empty.
func TestMergeTransactions_Empty(t *testing.T) {
	if got := MergeTransactions(nil, nil); len(got) != 0 {
		t.Errorf("got %+v, want empty", got)
	}
}
