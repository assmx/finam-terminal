package api

import (
	"fmt"
	"math"
	"strings"
	"time"

	"finam-terminal/models"

	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/marketdata"
)

// fxPair is where one currency's rate comes from: the symbol of its pair to the
// rouble, and how many units of the currency one quote is for.
type fxPair struct {
	Symbol string
	Per    float64
}

// fxSymbols is the rate source per currency, as the reconnaissance of
// 2026-09-10 chose it (spec, decision 4). The Trade API has no exchange-rate
// method, so a rate is the quote of a pair.
//
//   - USD and CNY come from the MOEX TOM pairs. Valuing a dollar balance at the
//     last USD000UTSTOM trade reproduced the broker's own equity to the kopeck,
//     twice; the #WWCP forex rate missed by 6 kopecks.
//   - EUR and INR come from the #WWCP forex feed. Every EUR pair on MISX has been
//     frozen since 2025-01-09, and the feed is the only live EUR/RUB in the API.
//   - KZT, AMD and KGS are quoted per 100 units.
//
// A currency outside the table has no rate: HKD, JPY, GBP, CHF, AED and UZS
// have no live pair to the rouble (HKD and JPY only as a cross through the
// dollar, which this table does not do). A variable so tests can point a
// currency at another pair.
var fxSymbols = map[string]fxPair{
	"USD": {Symbol: "USD000UTSTOM@MISX", Per: 1},
	"CNY": {Symbol: "CNYRUB_TOM@MISX", Per: 1},
	"EUR": {Symbol: "EURRUB@#WWCP", Per: 1},
	"INR": {Symbol: "INRRUB@#WWCP", Per: 1},
	"KZT": {Symbol: "KZTRUB_TOM@MISX", Per: 100},
	"BYN": {Symbol: "BYNRUB_TOM@MISX", Per: 1},
	"TRY": {Symbol: "TRYRUB_TOM@MISX", Per: 1},
	"AMD": {Symbol: "AMDRUB_TOM@MISX", Per: 100},
	"KGS": {Symbol: "KGSRUB_TOM@MISX", Per: 100},
}

// fxRateMaxAge is the oldest quote still taken as a rate. A pair that stopped
// trading keeps answering LastQuote with its last price — the frozen EUR pairs
// still give their January 2025 price — so the timestamp is the only thing
// that tells a halted pair from a quiet one. Fourteen days cover the New Year
// break, the longest the market is closed. A variable so tests can move it.
var fxRateMaxAge = 14 * 24 * time.Hour

// rateFromQuote reads a rate out of a pair quote: the last trade, else the
// close, divided by the units the pair is quoted per. A price that is not a
// positive finite number, and a quote older than fxRateMaxAge, are "no rate".
// A quote without a timestamp is still the latest price the broker has, so it
// is taken with a zero At.
func rateFromQuote(currency string, q *marketdata.Quote, per float64, now time.Time) (models.FXRate, bool) {
	if q == nil || per <= 0 {
		return models.FXRate{}, false
	}

	price := positiveDecimal(q.GetLast())
	if price == 0 {
		price = positiveDecimal(q.GetClose())
	}
	if price == 0 {
		return models.FXRate{}, false
	}

	at := timestampOrZero(q.GetTimestamp())
	if !at.IsZero() && now.Sub(at) > fxRateMaxAge {
		return models.FXRate{}, false
	}

	rate := price / per
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate <= 0 {
		return models.FXRate{}, false
	}
	return models.FXRate{Currency: currency, Rate: rate, At: at}, true
}

// GetFXRates returns the rouble rate of each currency asked for that has a pair
// in fxSymbols: one LastQuote per currency, in the order asked. The rouble
// needs no rate and costs nothing; neither does a currency without a pair, a
// blank, or a repeat.
//
// A currency whose quote fails or is unusable is simply absent from the result
// — an ordinary failure costs that one currency and nothing else. A rate limit
// ends the walk, since asking for the rest would only dig deeper into it; the
// rates already read are returned with an error IsRateLimited recognises, so
// the caller can latch for the session.
//
// LastQuote is called directly, not through GetQuotes. GetQuotes resolves each
// symbol's lot first, and for a pair the terminal has never resolved that would
// cost a GetAsset and a GetAssetParams on top of the quote.
func (c *Client) GetFXRates(currencies []string) (map[string]models.FXRate, error) {
	rates := make(map[string]models.FXRate)
	seen := make(map[string]bool)

	for _, raw := range currencies {
		currency := strings.ToUpper(strings.TrimSpace(raw))
		if currency == "" || currency == "RUB" || seen[currency] {
			continue
		}
		seen[currency] = true

		pair, ok := fxSymbols[currency]
		if !ok {
			continue
		}

		// One deadline per call, as in GetQuotes.
		resp, err := c.lastQuote(pair.Symbol)
		if err != nil {
			c.logGRPCError("MarketDataService", "LastQuote", err, fmt.Sprintf("Symbol: %s", pair.Symbol), fmt.Sprintf("Currency: %s", currency))
			if IsRateLimited(err) {
				return rates, fmt.Errorf("exchange rate request rate limit reached: %w", err)
			}
			continue
		}

		if rate, ok := rateFromQuote(currency, resp.GetQuote(), pair.Per, time.Now()); ok {
			rates[currency] = rate
		}
	}

	return rates, nil
}
