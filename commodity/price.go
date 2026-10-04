package commodity

import (
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"

	"finam-terminal/models"
)

// Conversion scales a MOEX quote, then optionally multiplies or divides it by
// an FX quote. A zero multiplier means 1; an omitted FX operation means divide.
type Conversion struct {
	Multiplier  float64 `json:"multiplier"`
	FXSymbol    string  `json:"fx_symbol"`
	FXOperation string  `json:"fx_operation"`
}

// SelectFuture chooses the earliest real expiration after the rollover
// threshold. The threshold itself is excluded, so a contract rolls exactly
// RollDays civil days before expiry. Missing dates and malformed symbols are
// ignored; ticker month codes are never used to infer expiration.
func SelectFuture(contracts []models.FutureContract, now time.Time, rollDays int) (models.FutureContract, bool) {
	if rollDays < 0 {
		return models.FutureContract{}, false
	}
	threshold := now.AddDate(0, 0, rollDays)
	var selected models.FutureContract
	found := false
	for _, contract := range contracts {
		if !fullSymbol(contract.Symbol) || !strings.HasSuffix(contract.Symbol, "@RTSX") || strings.ContainsAny(contract.Symbol, "*?[]\\") || contract.Expiration.IsZero() || !contract.Expiration.After(threshold) {
			continue
		}
		if !found || contract.Expiration.Before(selected.Expiration) || (contract.Expiration.Equal(selected.Expiration) && contract.Symbol < selected.Symbol) {
			selected = contract
			found = true
		}
	}
	return selected, found
}

// ConvertPrice converts a MOEX price without rounding it. FX is used
// only when configured and must be an available positive finite quote. Invalid
// inputs, overflow and a non-positive result return zero and false, not a
// made-up exchange rate or an unconverted value.
func ConvertPrice(price float64, conversion Conversion, fx float64) (float64, bool) {
	if !positiveNumber(price) || ValidateConversion(conversion) != nil {
		return 0, false
	}
	multiplier := conversion.Multiplier
	if multiplier == 0 {
		multiplier = 1
	}
	value := price * multiplier
	if conversion.FXSymbol != "" {
		if !positiveNumber(fx) {
			return 0, false
		}
		if conversion.FXOperation == "multiply" {
			value *= fx
		} else {
			value /= fx
		}
	}
	if !positiveNumber(value) {
		return 0, false
	}
	return value, true
}

// ValidateConversion rejects malformed FX settings and non-positive or
// non-finite multipliers, while accepting omitted conversion defaults.
func ValidateConversion(conversion Conversion) error {
	if conversion.Multiplier != 0 && !positiveNumber(conversion.Multiplier) {
		return fmt.Errorf("multiplier must be finite and positive, or 0 for the default 1")
	}
	if conversion.FXSymbol != "" && (!fullSymbol(conversion.FXSymbol) || strings.ContainsAny(conversion.FXSymbol, "*?[]\\")) {
		return fmt.Errorf("fx_symbol must be a full ticker@MIC without wildcards or whitespace")
	}
	if conversion.FXOperation != "" && conversion.FXOperation != "divide" && conversion.FXOperation != "multiply" {
		return fmt.Errorf("fx_operation must be divide or multiply")
	}
	if conversion.FXSymbol == "" && conversion.FXOperation != "" {
		return fmt.Errorf("fx_operation requires fx_symbol")
	}
	return nil
}

func positiveNumber(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func fullSymbol(symbol string) bool {
	ticker, mic, ok := strings.Cut(symbol, "@")
	return ok && ticker != "" && mic != "" && !strings.Contains(mic, "@") && strings.IndexFunc(symbol, unicode.IsSpace) < 0
}
