package config_test

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"finam-terminal/config"
	"finam-terminal/models"
)

func TestCommodityFuturesCachePreservesFamiliesAndSettings(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "commodities.json")
	// Even invalid settings are outside the cache writer's responsibility.
	settings := []byte("{user editing settings")
	if err := os.WriteFile(settingsPath, settings, 0644); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	entries := map[string]config.CommodityFuturesCacheEntry{
		"NG*@RTSX": {UpdatedAt: at, RollDays: 5, Contract: models.FutureContract{Symbol: "NGX6@RTSX", Name: "Gas", Expiration: at.AddDate(0, 1, 0), Decimals: 3}},
		"BR*@RTSX": {UpdatedAt: at, RollDays: 2},
	}
	var wg sync.WaitGroup
	for mask, entry := range entries {
		wg.Go(func() {
			if err := config.SaveCommodityFuturesCacheEntryIn(dir, mask, entry); err != nil {
				t.Errorf("save: %v", err)
			}
		})
	}
	wg.Wait()
	got, err := config.LoadCommodityFuturesCacheIn(dir)
	if err != nil || !reflect.DeepEqual(got, entries) {
		t.Fatalf("cache = %+v, %v; want %+v", got, err, entries)
	}
	updated := entries["BR*@RTSX"]
	updated.RollDays = 0
	updated.UpdatedAt = at.Add(time.Minute)
	entries["BR*@RTSX"] = updated
	if err := config.SaveCommodityFuturesCacheEntryIn(dir, "BR*@RTSX", updated); err != nil {
		t.Fatal(err)
	}
	got, err = config.LoadCommodityFuturesCacheIn(dir)
	if err != nil || !reflect.DeepEqual(got, entries) {
		t.Fatalf("updated cache = %+v, %v", got, err)
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil || !bytes.Equal(data, settings) {
		t.Fatalf("settings changed: %q, %v", data, err)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 2 {
		t.Fatalf("temporary files left: %v, %v", files, err)
	}
}

func TestCommodityFuturesCacheNeverCreatesSettings(t *testing.T) {
	dir := t.TempDir()
	got, err := config.LoadCommodityFuturesCacheIn(dir)
	if err != nil || len(got) != 0 {
		t.Fatalf("missing cache = %+v, %v", got, err)
	}
	entry := config.CommodityFuturesCacheEntry{UpdatedAt: time.Now().UTC(), RollDays: 5}
	if err := config.SaveCommodityFuturesCacheEntryIn(dir, "SF*@RTSX", entry); err != nil {
		t.Fatal(err)
	}
	got, err = config.LoadCommodityFuturesCacheIn(dir)
	if err != nil || !reflect.DeepEqual(got, map[string]config.CommodityFuturesCacheEntry{"SF*@RTSX": entry}) {
		t.Fatalf("cache = %+v, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "commodities.json")); !os.IsNotExist(err) {
		t.Fatalf("cache created settings: %v", err)
	}
}

func TestCommodityFuturesCacheRecoversCorruptData(t *testing.T) {
	for _, contents := range []string{"", "{unfinished", "null", "[]", `{"NG*@RTSX":{"updated_at":"invalid"}}`, `{"NG*@RTSX":{"updated_at":"2026-10-04T12:00:00Z","roll_days":5,"contract":null}}`, `{"NG*@RTSX":{"updated_at":"2026-10-04T12:00:00Z","contract":{}}}`} {
		t.Run(contents, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "commodities_cache.json")
			if err := os.WriteFile(target, []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := config.LoadCommodityFuturesCacheIn(dir); err == nil {
				t.Fatal("corrupt cache accepted")
			}
			entry := config.CommodityFuturesCacheEntry{UpdatedAt: time.Now().UTC(), RollDays: 2}
			if err := config.SaveCommodityFuturesCacheEntryIn(dir, "NG*@RTSX", entry); err != nil {
				t.Fatal(err)
			}
			got, err := config.LoadCommodityFuturesCacheIn(dir)
			if err != nil || !reflect.DeepEqual(got, map[string]config.CommodityFuturesCacheEntry{"NG*@RTSX": entry}) {
				t.Fatalf("recovered cache = %+v, %v", got, err)
			}
		})
	}
}

func TestCommodityFuturesCacheRejectsIncompleteEntries(t *testing.T) {
	at := time.Now()
	for _, tt := range []struct {
		name, mask string
		entry      config.CommodityFuturesCacheEntry
	}{
		{"missing mask", "", config.CommodityFuturesCacheEntry{UpdatedAt: at}},
		{"invalid mask", "NG[@RTSX", config.CommodityFuturesCacheEntry{UpdatedAt: at}},
		{"wide mask", "*@RTSX", config.CommodityFuturesCacheEntry{UpdatedAt: at}},
		{"missing timestamp", "NG*@RTSX", config.CommodityFuturesCacheEntry{}},
		{"negative roll", "NG*@RTSX", config.CommodityFuturesCacheEntry{UpdatedAt: at, RollDays: -1}},
		{"wrong family", "NG*@RTSX", config.CommodityFuturesCacheEntry{UpdatedAt: at, Contract: models.FutureContract{Symbol: "BRX6@RTSX", Expiration: at}}},
		{"missing expiry", "NG*@RTSX", config.CommodityFuturesCacheEntry{UpdatedAt: at, Contract: models.FutureContract{Symbol: "NGX6@RTSX"}}},
		{"missing symbol", "NG*@RTSX", config.CommodityFuturesCacheEntry{UpdatedAt: at, Contract: models.FutureContract{Expiration: at}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := config.SaveCommodityFuturesCacheEntryIn(t.TempDir(), tt.mask, tt.entry); err == nil {
				t.Fatal("invalid entry accepted")
			}
		})
	}
}

func TestSameLocalDate(t *testing.T) {
	oldLocal := time.Local
	time.Local = time.FixedZone("UTC+3", 3*60*60)
	t.Cleanup(func() { time.Local = oldLocal })
	now := time.Date(2026, 10, 4, 0, 10, 0, 0, time.Local)
	for _, tt := range []struct {
		name string
		at   time.Time
		want bool
	}{
		{"same local day across UTC date", time.Date(2026, 10, 3, 21, 1, 0, 0, time.UTC), true},
		{"previous date ten minutes ago", now.Add(-20 * time.Minute), false},
		{"same instant", now, true},
		{"future same day", now.Add(time.Minute), false},
		{"future day", now.AddDate(0, 0, 1), false},
		{"zero", time.Time{}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := config.SameLocalDate(tt.at, now); got != tt.want {
				t.Fatalf("SameLocalDate(%v, %v) = %v; want %v", tt.at, now, got, tt.want)
			}
		})
	}
	if config.SameLocalDate(now, time.Time{}) {
		t.Fatal("zero reference date accepted")
	}
}
