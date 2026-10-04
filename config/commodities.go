package config

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"unicode"

	commoditycalc "finam-terminal/commodity"
)

// Commodity describes an international quote, an optional MOEX futures family,
// and the conversion from its MOEX price into the international quote's units.
// An empty MOEXMask means the commodity is quote-only.
type Commodity struct {
	Symbol            string                   `json:"symbol"`
	Name              string                   `json:"name"`
	Decimals          int                      `json:"decimals"`
	MOEXMask          string                   `json:"moex_mask"`
	RollDays          int                      `json:"roll_days"`
	ExpiryWarningDays int                      `json:"expiry_warning_days"`
	Conversion        commoditycalc.Conversion `json:"conversion"`
}

// CommodityItem is a display entry identified by its configuration map key.
type CommodityItem struct {
	Commodity
	Code string
}

// CommoditiesConfig holds user-editable display settings.
// Order lists preferred map keys; unlisted entries follow in alphabetical order.
type CommoditiesConfig struct {
	Enabled     bool                 `json:"enabled"`
	Order       []string             `json:"order"`
	Commodities map[string]Commodity `json:"commodities"`
}

//go:embed example/commodities.json
var commoditiesTemplate []byte

var commoditySettingsMu sync.Mutex

var commoditiesDefaults = func() CommoditiesConfig {
	cfg, err := parseCommodities(commoditiesTemplate)
	if err != nil {
		panic("invalid embedded commodities configuration: " + err.Error())
	}
	return cfg
}()

// DefaultCommodities returns independent, editable defaults without accessing
// the filesystem. Changes to one returned configuration do not affect another.
func DefaultCommodities() CommoditiesConfig {
	cfg := CommoditiesConfig{
		Enabled:     commoditiesDefaults.Enabled,
		Order:       slices.Clone(commoditiesDefaults.Order),
		Commodities: make(map[string]Commodity, len(commoditiesDefaults.Commodities)),
	}
	for code, commodity := range commoditiesDefaults.Commodities {
		cfg.Commodities[code] = commodity
	}
	return cfg
}

// Items returns all configured entries in deterministic display order, even
// when the tab is disabled. Code is the map key, not the quote's ticker.
func (cfg CommoditiesConfig) Items() []CommodityItem {
	items := make([]CommodityItem, 0, len(cfg.Commodities))
	seen := make(map[string]bool, len(cfg.Commodities))
	for _, code := range cfg.Order {
		commodity, ok := cfg.Commodities[code]
		if ok && !seen[code] {
			items = append(items, CommodityItem{Commodity: commodity, Code: code})
			seen[code] = true
		}
	}
	extra := make([]string, 0, len(cfg.Commodities)-len(items))
	for code := range cfg.Commodities {
		if !seen[code] {
			extra = append(extra, code)
		}
	}
	slices.Sort(extra)
	for _, code := range extra {
		items = append(items, CommodityItem{Commodity: cfg.Commodities[code], Code: code})
	}
	return items
}

// LoadCommodities reads ~/.finam-cli/commodities.json, creating editable embedded
// defaults only when the file is missing. Invalid or unreadable settings return
// a zero configuration and an error; an existing file is never overwritten.
func LoadCommodities() (CommoditiesConfig, error) {
	dir, err := UserConfigDir()
	if err != nil {
		return CommoditiesConfig{}, fmt.Errorf("commodities configuration directory: %w", err)
	}
	commoditySettingsMu.Lock()
	defer commoditySettingsMu.Unlock()
	path := filepath.Join(dir, "commodities.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if err := createCommoditiesFile(dir, path); err != nil {
			return CommoditiesConfig{}, fmt.Errorf("create commodities configuration %s: %w", path, err)
		}
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return CommoditiesConfig{}, fmt.Errorf("read commodities configuration %s: %w", path, err)
	}
	cfg, err := parseCommodities(data)
	if err != nil {
		return CommoditiesConfig{}, fmt.Errorf("invalid commodities configuration %s: %w", path, err)
	}
	return cfg, nil
}

func createCommoditiesFile(dir, path string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".commodities-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(commoditiesTemplate); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// A hard link publishes the complete file atomically and fails if another
	// process or the user already created it. Rename can overwrite on Unix.
	if err := os.Link(tmp.Name(), path); err != nil && !os.IsExist(err) {
		return err
	}
	return nil
}

func parseCommodities(data []byte) (CommoditiesConfig, error) {
	cfg := &CommoditiesConfig{Enabled: true}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return CommoditiesConfig{}, err
	}
	if cfg == nil {
		return CommoditiesConfig{}, fmt.Errorf("expected a configuration object, got null")
	}
	// Legacy fixed contracts must not disappear silently on decode: replacing
	// them with a family is an explicit user migration, never a file rewrite.
	var raw struct {
		Commodities map[string]map[string]json.RawMessage `json:"commodities"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return CommoditiesConfig{}, err
	}
	for code, fields := range raw.Commodities {
		for field := range fields {
			if strings.EqualFold(field, "moex_symbol") {
				return CommoditiesConfig{}, fmt.Errorf("commodity %q uses legacy moex_symbol; replace it with moex_mask and roll_days", code)
			}
		}
	}
	seenOrder := make(map[string]bool, len(cfg.Order))
	for _, code := range cfg.Order {
		if _, ok := cfg.Commodities[code]; !ok {
			return CommoditiesConfig{}, fmt.Errorf("order references unknown code %q", code)
		}
		if seenOrder[code] {
			return CommoditiesConfig{}, fmt.Errorf("order repeats code %q", code)
		}
		seenOrder[code] = true
	}
	seenSymbols := make(map[string]string, len(cfg.Commodities))
	for code, commodity := range cfg.Commodities {
		warningSet := false
		for field := range raw.Commodities[code] {
			if strings.EqualFold(field, "expiry_warning_days") {
				warningSet = true
				break
			}
		}
		if !warningSet {
			commodity.ExpiryWarningDays = 5
			cfg.Commodities[code] = commodity
		}
		if commodity.ExpiryWarningDays < 0 {
			return CommoditiesConfig{}, fmt.Errorf("commodity %q expiry_warning_days must not be negative", code)
		}
		if strings.TrimSpace(code) == "" {
			return CommoditiesConfig{}, fmt.Errorf("commodity code must not be blank")
		}
		if !fullCommoditySymbol(commodity.Symbol) {
			return CommoditiesConfig{}, fmt.Errorf("commodity %q symbol must be a full ticker@MIC without whitespace", code)
		}
		if other, ok := seenSymbols[commodity.Symbol]; ok {
			return CommoditiesConfig{}, fmt.Errorf("commodity %q symbol duplicates %q", code, other)
		}
		seenSymbols[commodity.Symbol] = code
		if strings.TrimSpace(commodity.Name) == "" {
			return CommoditiesConfig{}, fmt.Errorf("commodity %q name must not be blank", code)
		}
		if commodity.Decimals < 0 || commodity.Decimals > 8 {
			return CommoditiesConfig{}, fmt.Errorf("commodity %q decimals must be between 0 and 8", code)
		}
		if commodity.MOEXMask != "" {
			if !moexCommoditySymbol(commodity.MOEXMask) || strings.ContainsAny(commodity.MOEXMask, "/\\") || strings.ContainsAny(commodity.MOEXMask[:1], "*?[") {
				return CommoditiesConfig{}, fmt.Errorf("commodity %q moex_mask must have a nonempty literal prefix and exact @RTSX, without separators or whitespace", code)
			}
			if _, err := path.Match(commodity.MOEXMask, ""); err != nil {
				return CommoditiesConfig{}, fmt.Errorf("commodity %q moex_mask is invalid: %w", code, err)
			}
		}
		if commodity.RollDays < 0 {
			return CommoditiesConfig{}, fmt.Errorf("commodity %q roll_days must not be negative", code)
		}
		if err := commoditycalc.ValidateConversion(commodity.Conversion); err != nil {
			return CommoditiesConfig{}, fmt.Errorf("commodity %q conversion: %w", code, err)
		}
	}
	return *cfg, nil
}

func fullCommoditySymbol(symbol string) bool {
	ticker, mic, ok := strings.Cut(symbol, "@")
	return ok && ticker != "" && mic != "" && !strings.Contains(mic, "@") && strings.IndexFunc(symbol, unicode.IsSpace) < 0
}

func moexCommoditySymbol(symbol string) bool {
	return fullCommoditySymbol(symbol) && strings.HasSuffix(symbol, "@RTSX")
}
