package config

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"finam-terminal/models"
)

// Serialize cache read-modify-write passes within this process.
var commodityFuturesCacheMu sync.Mutex

// CommodityFuturesCacheEntry stores the selected contract and the rollover
// threshold used to select it. A zero contract is a successful empty answer.
type CommodityFuturesCacheEntry struct {
	UpdatedAt time.Time             `json:"updated_at"`
	RollDays  int                   `json:"roll_days"`
	Contract  models.FutureContract `json:"contract"`
}

// SameLocalDate reports whether a timestamp belongs to today's local date.
// Missing or future timestamps are never fresh.
func SameLocalDate(at, now time.Time) bool {
	if at.IsZero() || now.IsZero() || at.After(now) {
		return false
	}
	ay, am, ad := at.In(time.Local).Date()
	ny, nm, nd := now.In(time.Local).Date()
	return ay == ny && am == nm && ad == nd
}

// LoadCommodityFuturesCache reads commodities_cache.json, never user settings.
// Missing files are an empty cache; callers may discard an invalid cache.
func LoadCommodityFuturesCache() (map[string]CommodityFuturesCacheEntry, error) {
	dir, err := UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("resolve commodity futures cache directory: %w", err)
	}
	return LoadCommodityFuturesCacheIn(dir)
}

// LoadCommodityFuturesCacheIn reads the cache in an explicit directory.
func LoadCommodityFuturesCacheIn(dir string) (map[string]CommodityFuturesCacheEntry, error) {
	commodityFuturesCacheMu.Lock()
	defer commodityFuturesCacheMu.Unlock()
	return readCommodityFuturesCache(filepath.Join(dir, "commodities_cache.json"))
}

func readCommodityFuturesCache(target string) (map[string]CommodityFuturesCacheEntry, error) {
	data, err := os.ReadFile(target)
	if os.IsNotExist(err) {
		return make(map[string]CommodityFuturesCacheEntry), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read commodity futures cache: %w", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("decode commodity futures cache: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("commodity futures cache must be an object")
	}
	entries := make(map[string]CommodityFuturesCacheEntry, len(raw))
	for mask, data := range raw {
		var fields struct {
			UpdatedAt time.Time              `json:"updated_at"`
			RollDays  *int                   `json:"roll_days"`
			Contract  *models.FutureContract `json:"contract"`
		}
		if err := json.Unmarshal(data, &fields); err != nil {
			return nil, fmt.Errorf("decode commodity futures cache %q: %w", mask, err)
		}
		if fields.RollDays == nil || fields.Contract == nil {
			return nil, fmt.Errorf("commodity futures cache %q requires roll_days and contract", mask)
		}
		entry := CommodityFuturesCacheEntry{UpdatedAt: fields.UpdatedAt, RollDays: *fields.RollDays, Contract: *fields.Contract}
		if err := validateCommodityFuturesCacheEntry(mask, entry); err != nil {
			return nil, err
		}
		entries[mask] = entry
	}
	return entries, nil
}

// SaveCommodityFuturesCacheEntry atomically saves one family's successful answer.
// Settings in commodities.json are never read or changed.
func SaveCommodityFuturesCacheEntry(mask string, entry CommodityFuturesCacheEntry) error {
	dir, err := UserConfigDir()
	if err != nil {
		return fmt.Errorf("resolve commodity futures cache directory: %w", err)
	}
	return SaveCommodityFuturesCacheEntryIn(dir, mask, entry)
}

// SaveCommodityFuturesCacheEntryIn saves into an explicit directory.
// Malformed disposable cache data is replaced after a successful broker lookup.
func SaveCommodityFuturesCacheEntryIn(dir, mask string, entry CommodityFuturesCacheEntry) error {
	if err := validateCommodityFuturesCacheEntry(mask, entry); err != nil {
		return err
	}
	commodityFuturesCacheMu.Lock()
	defer commodityFuturesCacheMu.Unlock()
	target := filepath.Join(dir, "commodities_cache.json")
	entries, err := readCommodityFuturesCache(target)
	if err != nil {
		// I/O failures are not corrupt data and must not trigger replacement.
		if _, statErr := os.ReadFile(target); statErr != nil {
			return err
		}
		log.Printf("[WARN] Replacing invalid commodity futures cache: %v", err)
		entries = make(map[string]CommodityFuturesCacheEntry)
	}
	entries[mask] = entry
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("encode commodity futures cache: %w", err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create commodity futures cache directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".commodities-cache-*.tmp")
	if err != nil {
		return fmt.Errorf("create commodity futures cache file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write commodity futures cache: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync commodity futures cache: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close commodity futures cache: %w", err)
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		return fmt.Errorf("replace commodity futures cache: %w", err)
	}
	return nil
}

func validateCommodityFuturesCacheEntry(mask string, entry CommodityFuturesCacheEntry) error {
	if strings.TrimSpace(mask) == "" || entry.UpdatedAt.IsZero() || entry.RollDays < 0 {
		return fmt.Errorf("commodity futures cache requires a mask, successful timestamp and nonnegative roll_days")
	}
	if !moexCommoditySymbol(mask) || strings.ContainsAny(mask, "/\\") || strings.ContainsAny(mask[:1], "*?[") {
		return fmt.Errorf("commodity futures cache requires a narrow @RTSX mask")
	}
	if _, err := path.Match(mask, ""); err != nil {
		return fmt.Errorf("commodity futures cache mask: %w", err)
	}
	if entry.Contract != (models.FutureContract{}) {
		matched, _ := path.Match(mask, entry.Contract.Symbol)
		if !matched || entry.Contract.Symbol == "" || entry.Contract.Expiration.IsZero() || entry.Contract.Decimals < 0 || entry.Contract.Decimals > 8 {
			return fmt.Errorf("commodity futures cache has incomplete contract %q", entry.Contract.Symbol)
		}
	}
	return nil
}
