package analytics

import (
	"sort"
	"time"

	"finam-terminal/models"
)

// MergeTrades combines a cached history with a freshly loaded tail.
//
// This is what the R key produces: the loader re-reads the last day or so, and
// the overlap arrives as records the cache already holds. Duplicates are
// resolved by id, keeping the copy already held — a re-fetch of the same id is
// the same trade, and preferring the newer copy would only add a way for a
// partial second answer to overwrite a complete first one.
//
// A record with no id cannot be deduplicated and is always kept: collapsing
// them would delete real trades to remove a duplicate that may not exist.
//
// The result is sorted by time, ties broken on id, so the FIFO matcher sees the
// same order on every refresh. Neither input is modified.
func MergeTrades(cached, fresh []models.Trade) []models.Trade {
	return mergeRecords(cached, fresh,
		func(t models.Trade) string { return t.ID },
		func(t models.Trade) time.Time { return t.Timestamp })
}

// MergeTransactions is MergeTrades for the money side of the history.
func MergeTransactions(cached, fresh []models.Transaction) []models.Transaction {
	return mergeRecords(cached, fresh,
		func(t models.Transaction) string { return t.ID },
		func(t models.Transaction) time.Time { return t.Timestamp })
}

// mergeRecords is the shared body: concatenate, drop repeated ids keeping the
// first, then sort by time with the id as the tiebreak.
func mergeRecords[T any](cached, fresh []T, id func(T) string, at func(T) time.Time) []T {
	out := make([]T, 0, len(cached)+len(fresh))
	seen := make(map[string]struct{}, len(cached)+len(fresh))

	for _, group := range [][]T{cached, fresh} {
		for _, item := range group {
			if key := id(item); key != "" {
				if _, dup := seen[key]; dup {
					continue
				}
				seen[key] = struct{}{}
			}
			out = append(out, item)
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		ti, tj := at(out[i]), at(out[j])
		if ti.Equal(tj) {
			return id(out[i]) < id(out[j])
		}
		return ti.Before(tj)
	})
	return out
}
