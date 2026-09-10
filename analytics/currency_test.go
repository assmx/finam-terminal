package analytics

import (
	"math"
	"testing"
	"time"

	"finam-terminal/models"
)

// The reconnaissance instruments of 2026-09-10, as the API caches describe
// them.
var (
	instYDEX = Instrument{Quote: "RUB", Unit: models.UnitValue{Currency: "RUB", Value: 3811}}
	instAMZN = Instrument{Quote: "USD", Unit: models.UnitValue{Currency: "USD", Value: 251.55}}

	// ЯНДЕКС1Р1: a rouble bond, the unit value is the dirty price.
	instYandexBond = Instrument{Quote: "RUB", FaceValue: 1000, Unit: models.UnitValue{Currency: "RUB", Value: 1019.24}}
	// ОФЗ 33 CNY: trades and settles in yuan.
	instOFZCNY = Instrument{Quote: "CNY", FaceValue: 10000, Unit: models.UnitValue{Currency: "CNY", Value: 9548.33}}
	// РФ ЗО 27 Д: 200 000 USD face settled in roubles.
	instReplacement = Instrument{Quote: "RUB", FaceValue: 200000, Unit: models.UnitValue{Currency: "RUB", Value: 16633960.33}}
	// ГПБ3P6CNY: yuan face settled in roubles.
	instGPBCNY = Instrument{Quote: "RUB", FaceValue: 100, Unit: models.UnitValue{Currency: "RUB", Value: 1320.68}}
)

// withFace returns the instrument with its calendar answer filled in.
func withFace(inst Instrument, face string) Instrument {
	inst.Face = face
	inst.FaceChecked = true
	return inst
}

// TestValuePosition covers every branch of the currency rule: a non-bond is in
// its quote currency; a bond is in its face currency when the calendar named
// it, in its quote currency when GetAssetParams shows the face to be there, and
// otherwise either "face in another currency, which is being looked up" —
// valued by the broker's own per-piece figure — or, with nothing to check
// against, its quote currency with the face flagged as unchecked.
func TestValuePosition(t *testing.T) {
	tests := []struct {
		name      string
		pos       models.Position
		last      string
		inst      Instrument
		wantCur   string
		wantValue float64
		wantState CurrencyState
		wantValid bool
	}{
		{"rouble equity", pos("YDEX@MISX", "9", "3811"), "", instYDEX, "RUB", 34299, CurrencyKnown, true},
		{"live price wins", pos("YDEX@MISX", "9", "3811"), "3800", instYDEX, "RUB", 34200, CurrencyKnown, true},
		{"dollar equity", pos("AMZN@XNGS", "10", "251.68"), "", instAMZN, "USD", 2516.8, CurrencyKnown, true},
		{"quote not reported: base currency, flagged", pos("X@MISX", "2", "100"), "", Instrument{}, "RUB", 200, CurrencyUnknown, true},
		{"rouble bond through the face", pos("RU000A10BF48@MISX", "10", "100.72"), "", instYandexBond, "RUB", 10072, CurrencyKnown, true},
		{"yuan bond on TQOY", pos("RU000A10DQA8@MISX", "2", "93.5"), "", instOFZCNY, "CNY", 18700, CurrencyKnown, true},
		{"replacement bond, face unresolved: broker's per-piece value", pos("RU000A10A851@MISX", "1", "97.25"), "", instReplacement, "RUB", 16633960.33, FaceUnresolved, true},
		{"replacement bond, calendar says dollars", pos("RU000A10A851@MISX", "1", "97.25"), "", withFace(instReplacement, "USD"), "USD", 194500, CurrencyKnown, true},
		{"yuan face settled in roubles, calendar says yuan", pos("RU000A1087C3@MISX", "10", "101.5662"), "", withFace(instGPBCNY, "CNY"), "CNY", 1015.662, CurrencyKnown, true},
		{"yuan face settled in roubles, face unresolved", pos("RU000A1087C3@MISX", "10", "101.5662"), "", instGPBCNY, "RUB", 13206.8, FaceUnresolved, true},
		{"calendar read, named nothing: still unresolved", pos("RU000A10A851@MISX", "1", "97.25"), "", withFace(instReplacement, ""), "RUB", 16633960.33, FaceUnresolved, true},
		{"bond without a unit value: quote currency, unchecked", pos("B@MISX", "10", "100"), "", Instrument{Quote: "RUB", FaceValue: 1000}, "RUB", 10000, FaceUnchecked, true},
		{"bond without quote or unit: base, unchecked", pos("B@MISX", "10", "100"), "", Instrument{FaceValue: 1000}, "RUB", 10000, FaceUnchecked, true},
		{"bond without quote, unit in band: settlement currency", pos("B@MISX", "10", "100"), "", Instrument{FaceValue: 1000, Unit: models.UnitValue{Currency: "CNY", Value: 1010}}, "CNY", 10000, CurrencyKnown, true},
		{"unit in another currency than the quote: unresolved", pos("B@MISX", "1", "100"), "", Instrument{Quote: "RUB", FaceValue: 1000, Unit: models.UnitValue{Currency: "USD", Value: 1000}}, "USD", 1000, FaceUnresolved, true},
		{"deep-discount bond with accrued interest stays in band", pos("SU26238RMFS4@MISX", "3", "53.424"), "", Instrument{Quote: "RUB", FaceValue: 1000, Unit: models.UnitValue{Currency: "RUB", Value: 553.64}}, "RUB", 1602.72, CurrencyKnown, true},
		{"short bond keeps its sign", pos("RU000A10BF48@MISX", "-5", "100.72"), "", instYandexBond, "RUB", -5036, CurrencyKnown, true},
		{"short unresolved keeps its sign", pos("RU000A10A851@MISX", "-1", "97.25"), "", instReplacement, "RUB", -16633960.33, FaceUnresolved, true},
		{"calendar wins over a contradicting quote", pos("B@MISX", "1", "100"), "", withFace(Instrument{Quote: "CNY", FaceValue: 1000}, "RUB"), "RUB", 1000, CurrencyKnown, true},
		{"no readable price", pos("YDEX@MISX", "9", "N/A"), "", instYDEX, "", 0, CurrencyKnown, false},
		{"no readable quantity", pos("YDEX@MISX", "N/A", "3811"), "", instYDEX, "", 0, CurrencyKnown, false},
		{"NaN price", pos("YDEX@MISX", "9", "NaN"), "", instYDEX, "", 0, CurrencyKnown, false},
		{"NaN unit value is no unit value", pos("B@MISX", "10", "100"), "", Instrument{Quote: "RUB", FaceValue: 1000, Unit: models.UnitValue{Currency: "RUB", Value: math.NaN()}}, "RUB", 10000, FaceUnchecked, true},
		{"overflowing face", pos("B@MISX", "1e300", "100"), "", Instrument{Quote: "RUB", FaceValue: 1e300}, "", 0, FaceUnchecked, false},
		{"zero price bond cannot be checked", pos("B@MISX", "10", "0"), "", instYandexBond, "RUB", 0, FaceUnchecked, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValuePosition(tt.pos, tt.last, tt.inst, "RUB")
			if got.Valid != tt.wantValid {
				t.Fatalf("Valid = %v, want %v (%+v)", got.Valid, tt.wantValid, got)
			}
			if got.State != tt.wantState {
				t.Errorf("State = %v, want %v", got.State, tt.wantState)
			}
			if !got.Valid {
				return
			}
			if got.Currency != tt.wantCur {
				t.Errorf("Currency = %q, want %q", got.Currency, tt.wantCur)
			}
			if math.Abs(got.Value-tt.wantValue) > 1e-6 {
				t.Errorf("Value = %v, want %v", got.Value, tt.wantValue)
			}
		})
	}
}

// TestValuePosition_UnknownCurrencyTakesTheGivenBase: an account whose base is
// not the rouble counts an unnamed currency in its own base.
func TestValuePosition_UnknownCurrencyTakesTheGivenBase(t *testing.T) {
	got := ValuePosition(pos("X@XNGS", "2", "10"), "", Instrument{}, "USD")
	if got.Currency != "USD" || got.State != CurrencyUnknown {
		t.Errorf("ValuePosition = %+v, want USD, CurrencyUnknown", got)
	}
}

// TestNeedsFaceCurrency: a calendar request is spent only on a bond whose face
// currency cannot be settled from what is already cached.
func TestNeedsFaceCurrency(t *testing.T) {
	tests := []struct {
		name string
		pos  models.Position
		inst Instrument
		want bool
	}{
		{"equity", pos("YDEX@MISX", "9", "3811"), instYDEX, false},
		{"rouble bond in band", pos("RU000A10BF48@MISX", "10", "100.72"), instYandexBond, false},
		{"yuan bond on TQOY in band", pos("RU000A10DQA8@MISX", "2", "93.5"), instOFZCNY, false},
		{"replacement bond out of band", pos("RU000A10A851@MISX", "1", "97.25"), instReplacement, true},
		{"yuan face settled in roubles", pos("RU000A1087C3@MISX", "10", "101.5662"), instGPBCNY, true},
		{"bond with nothing to check against", pos("B@MISX", "10", "100"), Instrument{Quote: "RUB", FaceValue: 1000}, true},
		{"bond without a price", pos("RU000A10BF48@MISX", "10", "N/A"), instYandexBond, true},
		{"calendar already answered", pos("RU000A10A851@MISX", "1", "97.25"), withFace(instReplacement, "USD"), false},
		{"calendar answered with nothing", pos("RU000A10A851@MISX", "1", "97.25"), withFace(instReplacement, ""), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NeedsFaceCurrency(tt.pos, "", tt.inst); got != tt.want {
				t.Errorf("NeedsFaceCurrency = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestRateTo converts through the rouble, since every pair is quoted against
// it, and refuses anything it cannot compute.
func TestRateTo(t *testing.T) {
	early := time.Date(2026, 9, 10, 17, 30, 5, 0, time.UTC)
	late := time.Date(2026, 9, 10, 18, 59, 58, 0, time.UTC)
	rates := map[string]models.FXRate{
		"USD": {Currency: "USD", Rate: 84.26, At: early},
		"CNY": {Currency: "CNY", Rate: 12.53, At: late},
		"EUR": {Currency: "EUR", Rate: 0, At: late},
		"TRY": {Currency: "TRY", Rate: math.NaN(), At: late},
	}

	tests := []struct {
		name      string
		currency  string
		base      string
		want      float64
		wantAt    time.Time
		wantValid bool
	}{
		{"same currency", "RUB", "RUB", 1, time.Time{}, true},
		{"same currency, any case", "usd", "USD", 1, time.Time{}, true},
		{"to the rouble", "USD", "RUB", 84.26, early, true},
		{"lower-case code", "cny", "rub", 12.53, late, true},
		{"rouble to a dollar base", "RUB", "USD", 1 / 84.26, early, true},
		{"cross through the rouble takes the older time", "CNY", "USD", 12.53 / 84.26, early, true},
		{"no rate", "HKD", "RUB", 0, time.Time{}, false},
		{"no rate for the base", "CNY", "HKD", 0, time.Time{}, false},
		{"zero rate", "EUR", "RUB", 0, time.Time{}, false},
		{"NaN rate", "TRY", "RUB", 0, time.Time{}, false},
		{"blank currency", "", "RUB", 0, time.Time{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RateTo(tt.currency, tt.base, rates)
			if got.Valid != tt.wantValid {
				t.Fatalf("RateTo(%s→%s) = %+v, want valid=%v", tt.currency, tt.base, got, tt.wantValid)
			}
			if !got.Valid {
				return
			}
			if math.Abs(got.Value-tt.want) > 1e-12 || !got.At.Equal(tt.wantAt) {
				t.Errorf("RateTo(%s→%s) = %+v, want %v at %v", tt.currency, tt.base, got, tt.want, tt.wantAt)
			}
		})
	}

	if got := RateTo("USD", "RUB", nil); got.Valid {
		t.Errorf("RateTo with no rates = %+v, want invalid", got)
	}
}

// TestRateIsStale: the evening after the MOEX close is not stale, the weekend
// and the morning before the first trade are, and a rate with no time cannot
// be judged.
func TestRateIsStale(t *testing.T) {
	close := time.Date(2026, 9, 10, 17, 30, 5, 0, time.Local)
	tests := []struct {
		name string
		at   time.Time
		now  time.Time
		want bool
	}{
		{"minutes old", close, close.Add(10 * time.Minute), false},
		{"late the same evening", close, close.Add(6 * time.Hour), false},
		{"exactly at the threshold", close, close.Add(FXRateStaleAfter), false},
		{"next morning before the first trade", close, close.Add(14 * time.Hour), true},
		{"Saturday", close.AddDate(0, 0, 1), close.AddDate(0, 0, 2), true},
		{"no time", time.Time{}, close, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RateIsStale(tt.at, tt.now); got != tt.want {
				t.Errorf("RateIsStale = %v, want %v", got, tt.want)
			}
		})
	}
}
