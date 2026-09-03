package analytics

import (
	"testing"
	"time"

	"finam-terminal/models"
)

func quota(name string, limit, remaining int64) models.QuotaUsage {
	return models.QuotaUsage{Name: name, Limit: limit, Remaining: remaining}
}

func names(quotas []models.QuotaUsage) []string {
	out := make([]string, len(quotas))
	for i, q := range quotas {
		out[i] = q.Name
	}
	return out
}

func equal(a, b []string) bool {
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

// TestSortQuotas puts the quota closest to running out at the top, which is
// the only ordering the screen is useful in — the API sends them unsorted.
func TestSortQuotas(t *testing.T) {
	in := []models.QuotaUsage{
		quota("full", 200, 200),
		quota("nearly gone", 200, 12),
		quota("half", 200, 100),
		// Two thirds of a tiny quota is still two thirds. Ranking by the
		// absolute remainder would put this above "nearly gone", which has
		// six times as many calls left but a far smaller share of its window.
		quota("small limit, mostly left", 3, 2),
	}

	got := SortQuotas(in)

	want := []string{"nearly gone", "half", "small limit, mostly left", "full"}
	if !equal(names(got), want) {
		t.Errorf("order = %v, want %v", names(got), want)
	}
}

// TestSortQuotas_ZeroLimitLast keeps a quota with no limit out of the ranking:
// its share cannot be computed, so it goes to the end rather than to the top.
func TestSortQuotas_ZeroLimitLast(t *testing.T) {
	got := SortQuotas([]models.QuotaUsage{
		quota("no limit", 0, 0),
		quota("nearly gone", 200, 5),
		quota("another with no limit", 0, 7),
		quota("full", 200, 200),
	})

	want := []string{"nearly gone", "full", "no limit", "another with no limit"}
	if !equal(names(got), want) {
		t.Errorf("order = %v, want %v", names(got), want)
	}
}

// TestSortQuotas_StableForEqualShares keeps the API's own order between quotas
// that are equally full, so the table does not reshuffle on a refresh.
func TestSortQuotas_StableForEqualShares(t *testing.T) {
	got := SortQuotas([]models.QuotaUsage{
		quota("first", 200, 200),
		quota("second", 200, 200),
		quota("third", 100, 100),
	})

	want := []string{"first", "second", "third"}
	if !equal(names(got), want) {
		t.Errorf("order = %v, want %v", names(got), want)
	}
}

// TestSortQuotas_DoesNotMutateInput matters because the caller holds the
// fetched list and re-renders from it on every draw.
func TestSortQuotas_DoesNotMutateInput(t *testing.T) {
	in := []models.QuotaUsage{
		quota("full", 200, 200),
		quota("nearly gone", 200, 12),
	}

	SortQuotas(in)

	if in[0].Name != "full" || in[1].Name != "nearly gone" {
		t.Errorf("input was reordered: %v", names(in))
	}
}

// TestSortQuotas_Empty must not panic on the empty answer the API can return.
func TestSortQuotas_Empty(t *testing.T) {
	if got := SortQuotas(nil); len(got) != 0 {
		t.Errorf("SortQuotas(nil) = %v, want empty", got)
	}
	if got := SortQuotas([]models.QuotaUsage{}); len(got) != 0 {
		t.Errorf("SortQuotas(empty) = %v, want empty", got)
	}
}

// TestQuotaLevel colours by how much is left, boundaries included.
func TestQuotaLevel(t *testing.T) {
	tests := []struct {
		name string
		q    models.QuotaUsage
		want Level
	}{
		{"untouched", quota("q", 200, 200), LevelGood},
		{"just above the warning", quota("q", 200, 101), LevelGood},
		{"exactly at the warning", quota("q", 200, 100), LevelWarn},
		{"inside the warning band", quota("q", 200, 60), LevelWarn},
		{"just above the alarm", quota("q", 200, 41), LevelWarn},
		{"exactly at the alarm", quota("q", 200, 40), LevelBad},
		{"nearly gone", quota("q", 200, 1), LevelBad},
		{"exhausted", quota("q", 200, 0), LevelBad},

		// No limit means no share to colour: the row is shown plain rather
		// than painted red on a division that never happened.
		{"zero limit", quota("q", 0, 0), LevelGood},
		{"zero limit with a remainder", quota("q", 0, 5), LevelGood},

		// A broker that reports more left than the limit is not a crisis.
		{"remaining above the limit", quota("q", 200, 250), LevelGood},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := QuotaLevel(tt.q); got != tt.want {
				t.Errorf("QuotaLevel(%d/%d) = %v, want %v", tt.q.Remaining, tt.q.Limit, got, tt.want)
			}
		})
	}
}

// TestQuotaRemainingShare is what both the sort and the bar are drawn from.
func TestQuotaRemainingShare(t *testing.T) {
	tests := []struct {
		name string
		q    models.QuotaUsage
		want float64
	}{
		{"half", quota("q", 200, 100), 0.5},
		{"full", quota("q", 200, 200), 1},
		{"empty", quota("q", 200, 0), 0},
		{"zero limit", quota("q", 0, 5), 0},
		{"negative remaining is clamped", quota("q", 200, -5), 0},
		{"over-full is clamped", quota("q", 200, 400), 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := QuotaRemainingShare(tt.q); got != tt.want {
				t.Errorf("QuotaRemainingShare = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestFormatReset renders the countdown, and shows a dash where the API sent
// no reset time — the normal state for a quota nothing has spent this window,
// which was 37 of the 39 quotas the reconnaissance saw.
func TestFormatReset(t *testing.T) {
	now := time.Date(2026, 9, 3, 17, 10, 0, 0, time.UTC)

	tests := []struct {
		name string
		at   time.Time
		want string
	}{
		{"no reset time", time.Time{}, "—"},
		{"one minute", now.Add(60 * time.Second), "01:00"},
		{"under a minute", now.Add(45 * time.Second), "00:45"},
		{"rounds up a partial second", now.Add(1500 * time.Millisecond), "00:02"},
		{"already past", now.Add(-30 * time.Second), "00:00"},
		{"exactly now", now, "00:00"},
		{"long window", now.Add(3661 * time.Second), "61:01"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FormatReset(tt.at, now); got != tt.want {
				t.Errorf("FormatReset = %q, want %q", got, tt.want)
			}
		})
	}
}
