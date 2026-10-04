package commodity_test

import (
	"encoding/binary"
	"math"
	"reflect"
	"testing"
	"time"

	"finam-terminal/commodity"
	"finam-terminal/models"
)

func TestSelectFuture(t *testing.T) {
	now := time.Date(2026, time.October, 3, 12, 0, 0, 0, time.UTC)
	contract := func(symbol string, days int) models.FutureContract {
		return models.FutureContract{Symbol: symbol, Name: symbol, Expiration: now.AddDate(0, 0, days), Decimals: 3}
	}
	tests := []struct {
		name      string
		contracts []models.FutureContract
		rollDays  int
		want      models.FutureContract
		ok        bool
	}{
		{"monthly irregular expiration", []models.FutureContract{contract("NGZ6@RTSX", 30), contract("NGX6@RTSX", 8), contract("NGV6@RTSX", 4)}, 5, contract("NGX6@RTSX", 8), true},
		{"quarterly irregular expiration", []models.FutureContract{contract("SFH7@RTSX", 90), contract("SFZ6@RTSX", 12)}, 5, contract("SFZ6@RTSX", 12), true},
		{"expiration overrides ticker ordering", []models.FutureContract{contract("NGZ6@RTSX", 8), contract("NGX6@RTSX", 30)}, 5, contract("NGZ6@RTSX", 8), true},
		{"inclusive rollover threshold", []models.FutureContract{contract("NGX6@RTSX", 5), contract("NGZ6@RTSX", 6)}, 5, contract("NGZ6@RTSX", 6), true},
		{"expired and expiring now", []models.FutureContract{contract("NGU6@RTSX", -1), contract("NGV6@RTSX", 0)}, 0, models.FutureContract{}, false},
		{"unknown expiry", []models.FutureContract{{Symbol: "NGX6@RTSX"}, contract("NGZ6@RTSX", 30)}, 5, contract("NGZ6@RTSX", 30), true},
		{"invalid symbols", []models.FutureContract{contract("", 6), contract("@RTSX", 6), contract("NGX6", 6), contract("NGX6@RTSX@RTSX", 6), contract("NG X6@RTSX", 6), contract("NG*@RTSX", 6), contract("NGX6@XNYM", 6), contract("NGZ6@RTSX", 30)}, 5, contract("NGZ6@RTSX", 30), true},
		{"equal expiration stable symbol tie", []models.FutureContract{contract("NGZ6@RTSX", 30), contract("NGX6@RTSX", 30)}, 5, contract("NGX6@RTSX", 30), true},
		{"negative roll rejected", []models.FutureContract{contract("NGZ6@RTSX", 30)}, -1, models.FutureContract{}, false},
		{"empty family", nil, 5, models.FutureContract{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := append([]models.FutureContract(nil), tt.contracts...)
			got, ok := commodity.SelectFuture(tt.contracts, now, tt.rollDays)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("SelectFuture() = %+v, %v; want %+v, %v", got, ok, tt.want, tt.ok)
			}
			if !reflect.DeepEqual(tt.contracts, before) {
				t.Fatal("selection mutated contract metadata")
			}
		})
	}
}

func TestSelectFutureCalendarRollover(t *testing.T) {
	t.Run("year boundary", func(t *testing.T) {
		location := time.FixedZone("UTC+03", 3*60*60)
		now := time.Date(2026, time.December, 31, 23, 30, 0, 0, location)
		threshold := now.AddDate(0, 0, 5)
		contracts := []models.FutureContract{
			{Symbol: "NGF7@RTSX", Expiration: threshold},
			{Symbol: "NGG7@RTSX", Expiration: threshold.Add(time.Nanosecond)},
		}
		got, ok := commodity.SelectFuture(contracts, now, 5)
		if !ok || got.Symbol != "NGG7@RTSX" {
			t.Fatalf("year-boundary calendar selection = %+v, %v", got, ok)
		}
	})
	t.Run("daylight saving", func(t *testing.T) {
		// TZif v1: one transition from UTC+1 to UTC+2. This keeps the test
		// independent of whether the host has an IANA timezone database.
		zone := make([]byte, 69)
		copy(zone, "TZif")
		binary.BigEndian.PutUint32(zone[32:36], 1)
		binary.BigEndian.PutUint32(zone[36:40], 2)
		binary.BigEndian.PutUint32(zone[40:44], 8)
		binary.BigEndian.PutUint32(zone[44:48], uint32(time.Date(2026, time.March, 29, 1, 0, 0, 0, time.UTC).Unix()))
		zone[48] = 1
		binary.BigEndian.PutUint32(zone[49:53], 3600)
		binary.BigEndian.PutUint32(zone[55:59], 7200)
		zone[59], zone[60] = 1, 4
		copy(zone[61:], "STD\x00DST\x00")
		location, err := time.LoadLocationFromTZData("Test/Commodity", zone)
		if err != nil {
			t.Fatal(err)
		}
		now := time.Date(2026, time.March, 28, 12, 0, 0, 0, location)
		threshold := now.AddDate(0, 0, 1)
		if threshold.Sub(now) != 23*time.Hour {
			t.Fatal("fixture did not cross the clock change")
		}
		contracts := []models.FutureContract{
			{Symbol: "NGJ6@RTSX", Expiration: threshold},
			{Symbol: "NGK6@RTSX", Expiration: threshold.Add(30 * time.Minute)},
		}
		got, ok := commodity.SelectFuture(contracts, now, 1)
		if !ok || got.Symbol != "NGK6@RTSX" {
			t.Fatalf("civil-day selection = %+v, %v", got, ok)
		}
	})
}

func TestConvertPrice(t *testing.T) {
	fx := "USD000UTSTOM@MISX"
	tests := []struct {
		name       string
		price      float64
		conversion commodity.Conversion
		fx         float64
		want       float64
		ok         bool
	}{
		{"identity with omitted multiplier", 3.123456789, commodity.Conversion{}, 0, 3.123456789, true},
		{"scale without fictitious FX", 1.23456789, commodity.Conversion{Multiplier: 1000}, math.NaN(), 1234.56789, true},
		{"CC roubles per kilogram to dollars per tonne", 270, commodity.Conversion{Multiplier: 1000, FXSymbol: fx, FXOperation: "divide"}, 90, 3000, true},
		{"CC decimal quote", 285.75, commodity.Conversion{Multiplier: 1000, FXSymbol: fx}, 95.25, 3000, true},
		{"CC observed quote", 481.3, commodity.Conversion{Multiplier: 1000, FXSymbol: fx}, 84.41, 481300.0 / 84.41, true},
		{"KC dollars per pound to cents per pound", 2.936, commodity.Conversion{Multiplier: 100}, 0, 293.6, true},
		{"SPY approximate index normalization", 774.8, commodity.Conversion{Multiplier: 10}, 0, 7748, true},
		{"default FX operation", 123.456789, commodity.Conversion{FXSymbol: fx}, 3, 41.152263, true},
		{"multiply FX", 12.3456789, commodity.Conversion{Multiplier: 2, FXSymbol: fx, FXOperation: "multiply"}, 3, 74.0740734, true},
		{"missing FX quote", 270, commodity.Conversion{Multiplier: 1000, FXSymbol: fx}, 0, 0, false},
		{"negative FX quote", 270, commodity.Conversion{FXSymbol: fx}, -90, 0, false},
		{"NaN FX quote", 270, commodity.Conversion{FXSymbol: fx}, math.NaN(), 0, false},
		{"infinite FX quote", 270, commodity.Conversion{FXSymbol: fx}, math.Inf(1), 0, false},
		{"zero price", 0, commodity.Conversion{}, 0, 0, false},
		{"negative price", -1, commodity.Conversion{}, 0, 0, false},
		{"NaN price", math.NaN(), commodity.Conversion{}, 0, 0, false},
		{"infinite price", math.Inf(1), commodity.Conversion{}, 0, 0, false},
		{"negative multiplier", 1, commodity.Conversion{Multiplier: -1}, 0, 0, false},
		{"NaN multiplier", 1, commodity.Conversion{Multiplier: math.NaN()}, 0, 0, false},
		{"infinite multiplier", 1, commodity.Conversion{Multiplier: math.Inf(1)}, 0, 0, false},
		{"unknown operation", 1, commodity.Conversion{FXSymbol: fx, FXOperation: "subtract"}, 1, 0, false},
		{"operation without FX", 1, commodity.Conversion{FXOperation: "multiply"}, 1, 0, false},
		{"malformed FX symbol", 1, commodity.Conversion{FXSymbol: "USD"}, 1, 0, false},
		{"overflow refused", math.MaxFloat64, commodity.Conversion{Multiplier: 2}, 0, 0, false},
		{"division overflow refused", math.MaxFloat64, commodity.Conversion{FXSymbol: fx}, 0.1, 0, false},
		{"underflow refused", math.SmallestNonzeroFloat64, commodity.Conversion{Multiplier: 0.1}, 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := commodity.ConvertPrice(tt.price, tt.conversion, tt.fx)
			if ok != tt.ok || math.IsNaN(got) || math.IsInf(got, 0) || math.Abs(got-tt.want) > math.Max(1, math.Abs(tt.want))*1e-14 {
				t.Fatalf("ConvertPrice() = %.12g, %v; want %.12g, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestValidateConversion(t *testing.T) {
	fx := "USD000UTSTOM@MISX"
	tests := []struct {
		name       string
		conversion commodity.Conversion
		valid      bool
	}{
		{"omitted defaults", commodity.Conversion{}, true},
		{"positive multiplier", commodity.Conversion{Multiplier: 1000}, true},
		{"FX default division", commodity.Conversion{FXSymbol: fx}, true},
		{"explicit division", commodity.Conversion{FXSymbol: fx, FXOperation: "divide"}, true},
		{"explicit multiplication", commodity.Conversion{FXSymbol: fx, FXOperation: "multiply"}, true},
		{"negative multiplier", commodity.Conversion{Multiplier: -1}, false},
		{"NaN multiplier", commodity.Conversion{Multiplier: math.NaN()}, false},
		{"infinite multiplier", commodity.Conversion{Multiplier: math.Inf(1)}, false},
		{"missing venue", commodity.Conversion{FXSymbol: "USD000UTSTOM"}, false},
		{"empty ticker", commodity.Conversion{FXSymbol: "@MISX"}, false},
		{"empty venue", commodity.Conversion{FXSymbol: "USD@"}, false},
		{"extra separator", commodity.Conversion{FXSymbol: "USD@MISX@RTSX"}, false},
		{"ASCII whitespace", commodity.Conversion{FXSymbol: "USD @MISX"}, false},
		{"Unicode whitespace", commodity.Conversion{FXSymbol: "USD\u00a0@MISX"}, false},
		{"wildcard", commodity.Conversion{FXSymbol: "USD*@MISX"}, false},
		{"unknown operation", commodity.Conversion{FXSymbol: fx, FXOperation: "subtract"}, false},
		{"operation without FX", commodity.Conversion{FXOperation: "multiply"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := commodity.ValidateConversion(tt.conversion); (err == nil) != tt.valid {
				t.Fatalf("ValidateConversion(%+v) = %v; want valid=%v", tt.conversion, err, tt.valid)
			}
		})
	}
}
