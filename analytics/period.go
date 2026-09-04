package analytics

import "time"

// Preset is one of the windows the Trades and Money screens can be looked at
// through. The end of every window is now; only the start differs.
type Preset int

const (
	// PresetMonth is the last 30 days.
	PresetMonth Preset = iota
	// PresetQuarter is the last 90 days.
	PresetQuarter
	// PresetYear is the last 365 days.
	PresetYear
	// PresetYTD starts at the first of January in the viewer's own timezone.
	PresetYTD
	// PresetAll starts when the account was opened.
	PresetAll

	// presetCount bounds the cycle. Keep it last.
	presetCount
)

// DefaultPreset is what the tab opens on. Three months is long enough to hold
// a few round trips on an ordinary account and short enough to still be about
// recent behaviour.
const DefaultPreset = PresetQuarter

// unknownHorizon is how far back PresetAll reaches when the account's opening
// date is unknown. Longer than any account the API will serve history for, so
// the window is never the thing that truncates the result.
const unknownHorizon = 20 * 365 * 24 * time.Hour

// presetLabels are the header labels. Kept beside the constants so adding a
// preset without a label is a compile-time gap rather than a blank cell.
var presetLabels = [presetCount]string{"1М", "3М", "1Г", "YTD", "Всё"}

// Label is what the header shows.
func (p Preset) Label() string {
	if p < 0 || p >= presetCount {
		return presetLabels[DefaultPreset]
	}
	return presetLabels[p]
}

// Next is the preset the P key moves to, wrapping around at the end.
func (p Preset) Next() Preset {
	if p < 0 || p >= presetCount-1 {
		return PresetMonth
	}
	return p + 1
}

// Range is the window this preset covers at the instant now.
//
// since is when the account was opened, used only by PresetAll. The returned
// window always runs forwards: an unknown or impossible opening date falls back
// to a horizon long enough to cover any real account, because a reversed
// interval is what the API rejects outright and an empty one would silently
// show nothing.
func (p Preset) Range(now, since time.Time) (time.Time, time.Time) {
	switch p {
	case PresetMonth:
		return now.AddDate(0, 0, -30), now
	case PresetQuarter:
		return now.AddDate(0, 0, -90), now
	case PresetYear:
		return now.AddDate(0, 0, -365), now
	case PresetYTD:
		// The year turns at the viewer's midnight, not UTC's: on the evening
		// of 31 December in Moscow, "since 1 January" must still mean this
		// year's January.
		local := now.Local()
		return time.Date(local.Year(), time.January, 1, 0, 0, 0, 0, time.Local), now
	case PresetAll:
		if since.IsZero() || !since.Before(now) {
			return now.Add(-unknownHorizon), now
		}
		return since, now
	default:
		return DefaultPreset.Range(now, since)
	}
}

// InRange reports whether an instant falls inside a window, endpoints included.
//
// A zero time is never inside one. Records arrive without a timestamp often
// enough — the API leaves it unset and the mapping keeps it zero rather than
// inventing the epoch — and counting those in every window would put a
// mis-dated record in every period at once.
func InRange(ts, from, to time.Time) bool {
	if ts.IsZero() {
		return false
	}
	return !ts.Before(from) && !ts.After(to)
}
