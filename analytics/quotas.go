package analytics

import (
	"fmt"
	"sort"
	"time"

	"finam-terminal/models"
)

// Thresholds for the API quota table, by how much of the window is left.
const (
	quotaWarnShare = 0.5
	quotaBadShare  = 0.2

	// noResetPlaceholder is shown for a quota the API sent no reset_time for,
	// which is what it does for anything untouched in the current window.
	noResetPlaceholder = "—"
)

// SortQuotas returns the quotas ordered by how close each is to running out,
// most depleted first. The API's own order is arbitrary and differs between
// calls, so without this the table would be unreadable and would reshuffle on
// every refresh.
//
// The comparison is by share rather than by absolute remainder, so a small
// quota that is nearly spent outranks a large one that is merely dented.
// Quotas with no limit have no share to compare and go to the end, keeping
// their relative order. The input slice is not modified: the caller keeps the
// fetched list and re-renders from it.
func SortQuotas(quotas []models.QuotaUsage) []models.QuotaUsage {
	if len(quotas) == 0 {
		return nil
	}

	sorted := make([]models.QuotaUsage, len(quotas))
	copy(sorted, quotas)

	sort.SliceStable(sorted, func(i, j int) bool {
		iLimited, jLimited := sorted[i].Limit > 0, sorted[j].Limit > 0
		if iLimited != jLimited {
			return iLimited
		}
		if !iLimited {
			// Neither has a share; SliceStable keeps the API's order.
			return false
		}
		return QuotaRemainingShare(sorted[i]) < QuotaRemainingShare(sorted[j])
	})

	return sorted
}

// QuotaRemainingShare is the fraction of the window still available, clamped
// to 0..1. A quota with no limit answers 0 — it has no share, and the callers
// that care check Limit themselves.
func QuotaRemainingShare(q models.QuotaUsage) float64 {
	if q.Limit <= 0 {
		return 0
	}

	share := float64(q.Remaining) / float64(q.Limit)
	switch {
	case share < 0:
		return 0
	case share > 1:
		return 1
	default:
		return share
	}
}

// QuotaLevel colours a quota row by how much of it is left.
//
// A quota with no limit is not coloured: there is no share to judge it by, and
// painting it red would report a division that never happened as a problem.
func QuotaLevel(q models.QuotaUsage) Level {
	if q.Limit <= 0 {
		return LevelGood
	}

	share := QuotaRemainingShare(q)
	switch {
	case share <= quotaBadShare:
		return LevelBad
	case share <= quotaWarnShare:
		return LevelWarn
	default:
		return LevelGood
	}
}

// FormatReset renders the time left until a quota's window rolls over, as
// mm:ss.
//
// A zero reset time means the API sent none, which it does for every quota
// untouched in the current window — the normal state for most of them — so
// that gets a dash rather than a countdown. A window that has already elapsed
// shows 00:00 instead of a negative number: the answer is simply stale.
func FormatReset(resetAt, now time.Time) string {
	if resetAt.IsZero() {
		return noResetPlaceholder
	}

	remaining := resetAt.Sub(now)
	if remaining < 0 {
		remaining = 0
	}

	// Round up, so a countdown never shows 00:00 while time is still left.
	seconds := int((remaining + time.Second - 1) / time.Second)
	return fmt.Sprintf("%02d:%02d", seconds/60, seconds%60)
}
