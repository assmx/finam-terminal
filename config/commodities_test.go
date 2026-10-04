package config_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"finam-terminal/commodity"
	"finam-terminal/config"
)

func commoditiesConfigPath(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir, err := config.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "commodities.json")
}

func writeCommoditiesConfig(t *testing.T, path string, cfg config.CommoditiesConfig) {
	t.Helper()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func customCommoditiesConfig() config.CommoditiesConfig {
	return config.CommoditiesConfig{
		Enabled: true,
		Order:   []string{"CUSTOM"},
		Commodities: map[string]config.Commodity{
			"CUSTOM": {Symbol: "CUSTOM@XNYM", Name: "Мой газ", Decimals: 4, MOEXMask: "NG*@RTSX", RollDays: 5, Conversion: commodity.Conversion{Multiplier: 1000, FXSymbol: "USD000UTSTOM@MISX", FXOperation: "divide"}},
			"ZINC":   {Symbol: "ZN@XCEC", Name: "Цинк", Decimals: 2},
			"BRENT":  {Symbol: "BZ@IFEU", Name: "Brent", Decimals: 0},
		},
	}
}

func TestLoadCommoditiesCustomDisplaySettings(t *testing.T) {
	path := commoditiesConfigPath(t)
	want := customCommoditiesConfig()
	writeCommoditiesConfig(t, path, want)
	got, err := config.LoadCommodities()
	if err != nil {
		t.Fatal(err)
	}
	items := got.Items()
	wantItems := []config.CommodityItem{
		{Code: "CUSTOM", Commodity: want.Commodities["CUSTOM"]},
		{Code: "BRENT", Commodity: want.Commodities["BRENT"]},
		{Code: "ZINC", Commodity: want.Commodities["ZINC"]},
	}
	if !reflect.DeepEqual(items, wantItems) {
		t.Fatalf("Items() = %+v; want %+v", items, wantItems)
	}
	got.Order = nil
	wantItems = []config.CommodityItem{wantItems[1], wantItems[0], wantItems[2]}
	if !reflect.DeepEqual(got.Items(), wantItems) {
		t.Fatalf("Items() without order = %+v; want %+v", got.Items(), wantItems)
	}
}

func TestLoadCommoditiesExpiryWarningDays(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field string
		want  int
	}{
		{"omitted", "", 5},
		{"custom", `,"expiry_warning_days":9`, 9},
		{"disabled", `,"expiry_warning_days":0`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := commoditiesConfigPath(t)
			data := `{"commodities":{"CUSTOM":{"symbol":"CUSTOM@XNYM","name":"Custom","decimals":2` + tc.field + `}}}`
			if err := os.WriteFile(path, []byte(data), 0644); err != nil {
				t.Fatal(err)
			}
			got, err := config.LoadCommodities()
			if err != nil {
				t.Fatal(err)
			}
			if days := got.Commodities["CUSTOM"].ExpiryWarningDays; days != tc.want {
				t.Fatalf("ExpiryWarningDays = %d; want %d", days, tc.want)
			}
		})
	}
	for code, commodity := range config.DefaultCommodities().Commodities {
		if commodity.ExpiryWarningDays != 5 {
			t.Errorf("default %s ExpiryWarningDays = %d; want 5", code, commodity.ExpiryWarningDays)
		}
	}
}

func TestLoadCommoditiesRetainsDisabledSettings(t *testing.T) {
	path := commoditiesConfigPath(t)
	want := customCommoditiesConfig()
	want.Enabled = false
	writeCommoditiesConfig(t, path, want)
	got, err := config.LoadCommodities()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) || len(got.Items()) != 3 {
		t.Fatalf("disabled settings were discarded: %+v", got)
	}
}

func TestLoadCommoditiesRejectsInvalidSettings(t *testing.T) {
	tests := []struct {
		name   string
		change func(*config.CommoditiesConfig)
		field  string
	}{
		{"unknown order", func(c *config.CommoditiesConfig) { c.Order = []string{"MISSING"} }, "order"},
		{"duplicate order", func(c *config.CommoditiesConfig) { c.Order = []string{"CUSTOM", "CUSTOM"} }, "order"},
		{"duplicate symbol", func(c *config.CommoditiesConfig) { c.Commodities["ZINC"] = c.Commodities["CUSTOM"] }, "symbol"},
		{"blank code", func(c *config.CommoditiesConfig) { c.Commodities[" "] = c.Commodities["CUSTOM"] }, "code"},
		{"blank name", changeCommodity(func(c *config.Commodity) { c.Name = " \t" }), "name"},
		{"blank symbol", changeCommodity(func(c *config.Commodity) { c.Symbol = "" }), "symbol"},
		{"quote without venue", changeCommodity(func(c *config.Commodity) { c.Symbol = "NG" }), "symbol"},
		{"quote without ticker", changeCommodity(func(c *config.Commodity) { c.Symbol = "@XNYM" }), "symbol"},
		{"quote with extra separator", changeCommodity(func(c *config.Commodity) { c.Symbol = "NG@XNYM@RTSX" }), "symbol"},
		{"quote with whitespace", changeCommodity(func(c *config.Commodity) { c.Symbol = "NG @XNYM" }), "symbol"},
		{"negative decimals", changeCommodity(func(c *config.Commodity) { c.Decimals = -1 }), "decimals"},
		{"too many decimals", changeCommodity(func(c *config.Commodity) { c.Decimals = 9 }), "decimals"},
		{"international trading venue", changeCommodity(func(c *config.Commodity) { c.MOEXMask = "NG*@XNYM" }), "moex_mask"},
		{"equity trading venue", changeCommodity(func(c *config.Commodity) { c.MOEXMask = "SBER*@MISX" }), "moex_mask"},
		{"trading without venue", changeCommodity(func(c *config.Commodity) { c.MOEXMask = "NG*" }), "moex_mask"},
		{"trading without ticker", changeCommodity(func(c *config.Commodity) { c.MOEXMask = "@RTSX" }), "moex_mask"},
		{"trading with extra separator", changeCommodity(func(c *config.Commodity) { c.MOEXMask = "NG*@XNYM@RTSX" }), "moex_mask"},
		{"trading with whitespace", changeCommodity(func(c *config.Commodity) { c.MOEXMask = " NG*@RTSX" }), "moex_mask"},
		{"malformed mask", changeCommodity(func(c *config.Commodity) { c.MOEXMask = "NG[@RTSX" }), "moex_mask"},
		{"leading star", changeCommodity(func(c *config.Commodity) { c.MOEXMask = "*NG@RTSX" }), "moex_mask"},
		{"leading question", changeCommodity(func(c *config.Commodity) { c.MOEXMask = "?NG@RTSX" }), "moex_mask"},
		{"leading bracket", changeCommodity(func(c *config.Commodity) { c.MOEXMask = "[N]G*@RTSX" }), "moex_mask"},
		{"slash in mask", changeCommodity(func(c *config.Commodity) { c.MOEXMask = "NG/*@RTSX" }), "moex_mask"},
		{"backslash in mask", changeCommodity(func(c *config.Commodity) { c.MOEXMask = `NG\*@RTSX` }), "moex_mask"},
		{"negative roll days", changeCommodity(func(c *config.Commodity) { c.RollDays = -1 }), "roll_days"},
		{"negative expiry warning days", changeCommodity(func(c *config.Commodity) { c.ExpiryWarningDays = -1 }), "expiry_warning_days"},
		{"negative multiplier", changeCommodity(func(c *config.Commodity) { c.Conversion.Multiplier = -1 }), "multiplier"},
		{"FX without venue", changeCommodity(func(c *config.Commodity) { c.Conversion.FXSymbol = "USD000UTSTOM" }), "fx_symbol"},
		{"FX with whitespace", changeCommodity(func(c *config.Commodity) { c.Conversion.FXSymbol = "USD000UTSTOM @MISX" }), "fx_symbol"},
		{"FX with extra separator", changeCommodity(func(c *config.Commodity) { c.Conversion.FXSymbol = "USD@MISX@RTSX" }), "fx_symbol"},
		{"FX wildcard", changeCommodity(func(c *config.Commodity) { c.Conversion.FXSymbol = "USD*@MISX" }), "fx_symbol"},
		{"unknown FX operation", changeCommodity(func(c *config.Commodity) { c.Conversion.FXOperation = "subtract" }), "fx_operation"},
		{"operation without FX", changeCommodity(func(c *config.Commodity) { c.Conversion.FXSymbol = "" }), "fx_operation"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := commoditiesConfigPath(t)
			cfg := customCommoditiesConfig()
			tt.change(&cfg)
			writeCommoditiesConfig(t, path, cfg)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			got, err := config.LoadCommodities()
			if err == nil || !strings.Contains(err.Error(), tt.field) {
				t.Fatalf("LoadCommodities() = %+v, %v; want %s error", got, err, tt.field)
			}
			if got.Enabled || len(got.Items()) != 0 {
				t.Fatalf("invalid configuration exposed usable items: %+v", got)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("invalid settings were overwritten: %v", err)
			}
		})
	}
}

func changeCommodity(change func(*config.Commodity)) func(*config.CommoditiesConfig) {
	return func(cfg *config.CommoditiesConfig) {
		commodity := cfg.Commodities["CUSTOM"]
		change(&commodity)
		cfg.Commodities["CUSTOM"] = commodity
	}
}

func TestLoadCommoditiesDoesNotOverwriteMalformedFile(t *testing.T) {
	for _, contents := range []string{"", "{", "null", "[]", `{"enabled":"yes"}`, `{"enabled":true} {}`, `{"commodities":{"X":{"symbol":"X@MIC","name":"X","conversion":{"multiplier":1e309}}}}`} {
		t.Run(contents, func(t *testing.T) {
			path := commoditiesConfigPath(t)
			if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.LoadCommodities()
			if err == nil || cfg.Enabled || len(cfg.Items()) != 0 {
				t.Fatalf("LoadCommodities() = %+v, %v; want disabled error result", cfg, err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != contents {
				t.Fatalf("malformed file changed: %q, %v", data, err)
			}
		})
	}
}

func TestLoadCommoditiesCreatesFileAndConsumesUserEdits(t *testing.T) {
	path := commoditiesConfigPath(t)
	if err := os.Remove(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	_, err := config.LoadCommodities()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("first launch did not persist defaults: %v", err)
	}
	var persisted config.CommoditiesConfig
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	persisted.Order = []string{"NG"}
	gas := config.Commodity{
		Symbol: "NG@XNYM", Name: "Газ пользователя",
		Decimals: 8, MOEXMask: "NG?7@RTSX", RollDays: 2,
	}
	persisted.Commodities = map[string]config.Commodity{"NG": gas}
	writeCommoditiesConfig(t, path, persisted)
	reloaded, err := config.LoadCommodities()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reloaded.Items(), []config.CommodityItem{{Code: "NG", Commodity: gas}}) {
		t.Fatalf("persisted edits were not consumed: %+v", reloaded.Items())
	}
}

func TestLoadCommoditiesConcurrentFirstCreation(t *testing.T) {
	path := commoditiesConfigPath(t)
	const callers = 12
	var wg sync.WaitGroup
	wg.Add(callers)
	for range callers {
		go func() {
			defer wg.Done()
			cfg, err := config.LoadCommodities()
			if err != nil || !cfg.Enabled || len(cfg.Items()) != 9 {
				t.Errorf("concurrent load = %+v, %v", cfg, err)
			}
		}()
	}
	wg.Wait()
	files, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name() != "commodities.json" {
		t.Fatalf("temporary creation files left behind: %v", files)
	}
}

func TestLoadCommoditiesReportsUnreadablePath(t *testing.T) {
	path := commoditiesConfigPath(t)
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadCommodities()
	if err == nil || cfg.Enabled || len(cfg.Items()) != 0 {
		t.Fatalf("directory at config path = %+v, %v; want disabled error result", cfg, err)
	}
}

func TestLoadCommoditiesRejectsLegacyFixedContract(t *testing.T) {
	for _, legacy := range []string{`"NGZ6@RTSX"`, `""`, `null`} {
		t.Run(legacy, func(t *testing.T) {
			path := commoditiesConfigPath(t)
			contents := `{"commodities":{"NG":{"symbol":"NG@XNYM","name":"Gas","moex_symbol":` + legacy + `,"moex_mask":"NG*@RTSX"}}}`
			if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.LoadCommodities()
			if err == nil || !strings.Contains(err.Error(), "moex_symbol") || !strings.Contains(err.Error(), "moex_mask") {
				t.Fatalf("LoadCommodities() = %+v, %v; want explicit migration error", cfg, err)
			}
			if cfg.Enabled || len(cfg.Items()) != 0 {
				t.Fatalf("legacy configuration exposed usable settings: %+v", cfg)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != contents {
				t.Fatalf("legacy configuration was rewritten: %q, %v", data, err)
			}
		})
	}
}

func TestLoadCommoditiesAllowsOmittedConversionDefaults(t *testing.T) {
	path := commoditiesConfigPath(t)
	contents := `{"commodities":{"CC":{"symbol":"CC@IFUS","name":"Cocoa","moex_mask":"CC*@RTSX","conversion":{"fx_symbol":"USD000UTSTOM@MISX"}}}}`
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadCommodities()
	if err != nil {
		t.Fatal(err)
	}
	got, ok := commodity.ConvertPrice(270000, cfg.Commodities["CC"].Conversion, 90)
	if !ok || got != 3000 {
		t.Fatalf("omitted multiplier/operation conversion = %v, %v; want 3000, true", got, ok)
	}
}

func TestLoadCommoditiesIgnoresObsoleteCacheSection(t *testing.T) {
	path := commoditiesConfigPath(t)
	want := customCommoditiesConfig()
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	fields["futures"] = json.RawMessage(`{"NG*@RTSX":{"contract":null,"updated_at":"invalid"}}`)
	data, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	got, err := config.LoadCommodities()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("settings depend on obsolete cache: %+v, %v", got, err)
	}
}

func TestDefaultCommoditiesReturnsIndependentMaps(t *testing.T) {
	first := config.DefaultCommodities()
	second := config.DefaultCommodities()
	delete(first.Commodities, "NG")
	first.Order[0] = "user edit"
	if len(second.Commodities) != 9 || second.Order[0] == "user edit" {
		t.Fatalf("default configurations share mutable data: %+v", second)
	}
}
