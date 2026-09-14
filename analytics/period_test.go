package analytics

import (
	"testing"
	"time"
)

// TestPresetLabels pins the labels the header renders.
func TestPresetLabels(t *testing.T) {
	want := map[Preset]string{
		PresetMonth:   "1М",
		PresetQuarter: "3М",
		PresetYear:    "1Г",
		PresetYTD:     "YTD",
		PresetAll:     "Всё",
	}
	for p, label := range want {
		if got := p.Label(); got != label {
			t.Errorf("Preset(%d).Label() = %q, want %q", int(p), got, label)
		}
	}

	// An out-of-range value must still render something rather than an empty
	// header cell.
	if got := Preset(99).Label(); got == "" {
		t.Error("an unknown preset rendered an empty label")
	}
}

// TestPresetNext walks the cycle the P key steps through, and back to the start.
func TestPresetNext(t *testing.T) {
	order := []Preset{PresetMonth, PresetQuarter, PresetYear, PresetYTD, PresetAll}

	p := PresetMonth
	for i, want := range append(order[1:], PresetMonth) {
		p = p.Next()
		if p != want {
			t.Fatalf("step %d: got %s, want %s", i, p.Label(), want.Label())
		}
	}

	// An out-of-range value returns to the start rather than looping outside.
	if got := Preset(99).Next(); got != PresetMonth {
		t.Errorf("Preset(99).Next() = %s, want the first preset", got.Label())
	}
}

// TestDefaultPreset: the tab opens on three months.
func TestDefaultPreset(t *testing.T) {
	if DefaultPreset != PresetQuarter {
		t.Errorf("DefaultPreset = %s, want 3М", DefaultPreset.Label())
	}
}

// TestPresetRange checks each window against the same instant.
func TestPresetRange(t *testing.T) {
	now := time.Date(2026, 9, 4, 15, 30, 0, 0, time.Local)
	since := time.Date(2019, 4, 17, 0, 0, 0, 0, time.Local)

	cases := []struct {
		preset Preset
		want   time.Time
	}{
		{PresetMonth, now.AddDate(0, 0, -30)},
		{PresetQuarter, now.AddDate(0, 0, -90)},
		{PresetYear, now.AddDate(0, 0, -365)},
		{PresetYTD, time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)},
		{PresetAll, since},
	}
	for _, c := range cases {
		t.Run(c.preset.Label(), func(t *testing.T) {
			from, to := c.preset.Range(now, since)
			if !from.Equal(c.want) {
				t.Errorf("from = %v, want %v", from, c.want)
			}
			if !to.Equal(now) {
				t.Errorf("to = %v, want now (%v)", to, now)
			}
		})
	}
}

// TestPresetRange_YTDIsLocal: the year boundary is the user's midnight, not
// UTC's. On the evening of 31 December in Moscow, "since 1 January" must not
// already mean the coming year.
func TestPresetRange_YTDIsLocal(t *testing.T) {
	now := time.Date(2026, 12, 31, 23, 30, 0, 0, time.Local)
	from, _ := PresetYTD.Range(now, time.Time{})

	if from.Year() != 2026 || from.Month() != time.January || from.Day() != 1 {
		t.Errorf("YTD start = %v, want 2026-01-01", from)
	}
	if from.Location() != time.Local {
		t.Errorf("YTD start location = %v, want the local zone", from.Location())
	}
	if !from.Before(now) {
		t.Error("the YTD window starts in the future")
	}
}

// TestPresetRange_AllWithoutSince falls back to a long window when the account
// has no known opening date, rather than producing an empty or reversed one.
func TestPresetRange_AllWithoutSince(t *testing.T) {
	now := time.Date(2026, 9, 4, 15, 30, 0, 0, time.Local)

	from, to := PresetAll.Range(now, time.Time{})
	if !from.Before(to) {
		t.Fatalf("range = %v..%v, want a window that runs forwards", from, to)
	}
	if to.Sub(from) < 10*365*24*time.Hour {
		t.Errorf("window = %v, want at least a decade when the opening date is unknown", to.Sub(from))
	}
}

// TestPresetRange_AllWithFutureSince keeps the window forwards even when the
// broker reports an opening date that has not happened.
func TestPresetRange_AllWithFutureSince(t *testing.T) {
	now := time.Date(2026, 9, 4, 15, 30, 0, 0, time.Local)
	from, to := PresetAll.Range(now, now.AddDate(1, 0, 0))

	if !from.Before(to) {
		t.Errorf("range = %v..%v, want a window that runs forwards", from, to)
	}
}

// TestInRange is the shared filter both screens use.
func TestInRange(t *testing.T) {
	from := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		ts   time.Time
		want bool
	}{
		{"before", from.Add(-time.Second), false},
		{"exactly at the start", from, true},
		{"inside", from.Add(24 * time.Hour), true},
		{"exactly at the end", to, true},
		{"after", to.Add(time.Second), false},
		{"zero time", time.Time{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := InRange(c.ts, from, to); got != c.want {
				t.Errorf("InRange(%v) = %v, want %v", c.ts, got, c.want)
			}
		})
	}
}
