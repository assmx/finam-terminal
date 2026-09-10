package api

import (
	"math"
	"strings"

	"finam-terminal/models"

	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/assets"
	"google.golang.org/genproto/googleapis/type/decimal"
	"google.golang.org/genproto/googleapis/type/money"
)

// instrumentCurrencyFromAsset reads the money facts out of a GetAsset answer:
// quote_currency and, for a bond, the face value.
//
// bond_details.currency is not read. The reconnaissance of 2026-09-10 found
// "%" in it on every bond — rouble, yuan and a replacement bond with a USD face
// alike — so it names the unit of the price and would put a percent sign where
// a currency code belongs. A face that does not parse to a positive finite
// number is "not reported", never a bond worth nothing.
func instrumentCurrencyFromAsset(resp *assets.GetAssetResponse) models.InstrumentCurrency {
	cur := models.InstrumentCurrency{
		Quote: strings.ToUpper(strings.TrimSpace(resp.GetQuoteCurrency())),
	}
	if bd := resp.GetBondDetails(); bd != nil {
		if face := positiveDecimal(bd.GetBondFaceValue()); face > 0 {
			cur.FaceValue = face
		}
	}
	return cur
}

// storeInstrumentCurrency files a GetAsset answer's money facts under every key
// a caller may later ask by: the ones the request was made with, the ticker the
// broker answered with, and its ticker@mic.
//
// A successful answer is filed even when it names nothing. "The broker was
// asked and reported no currency" is different from "never asked", and the
// second read tells them apart.
func (c *Client) storeInstrumentCurrency(resp *assets.GetAssetResponse, keys ...string) {
	if resp == nil {
		return
	}
	cur := instrumentCurrencyFromAsset(resp)

	if ticker := resp.GetTicker(); ticker != "" {
		keys = append(keys, ticker)
		if mic := resp.GetMic(); mic != "" {
			keys = append(keys, ticker+"@"+mic)
		}
	}

	c.assetMutex.Lock()
	defer c.assetMutex.Unlock()

	if c.instrumentCurrencyCache == nil {
		c.instrumentCurrencyCache = make(map[string]models.InstrumentCurrency)
	}
	for _, key := range keys {
		if key != "" {
			c.instrumentCurrencyCache[key] = cur
		}
	}
}

// GetInstrumentCurrency returns what GetAsset said about an instrument's money,
// by full symbol or bare ticker. The second result is false when GetAsset has
// not answered for the instrument this session — which is different from an
// answer that named no currency (true, with an empty Quote).
//
// This is a pure cache read and never issues a request. The cache is filled by
// the GetAsset calls the terminal already makes to resolve a position's lot and
// to load an instrument profile.
func (c *Client) GetInstrumentCurrency(symbol string) (models.InstrumentCurrency, bool) {
	if symbol == "" {
		return models.InstrumentCurrency{}, false
	}

	c.assetMutex.RLock()
	defer c.assetMutex.RUnlock()

	if cur, ok := c.instrumentCurrencyCache[symbol]; ok {
		return cur, true
	}
	if ticker, _, found := strings.Cut(symbol, "@"); found {
		cur, ok := c.instrumentCurrencyCache[ticker]
		return cur, ok
	}
	return models.InstrumentCurrency{}, false
}

// unitValueFromParams derives the value of one piece of an instrument from a
// GetAssetParams answer: margin × 100 / risk rate / trade lot, in the margin's
// currency.
//
// The margin the broker asks to open a position is its value times the risk
// rate, which the API sends in percent (75 means 75%). The reconnaissance of
// 2026-09-10 checked the division against ten instruments — equities, funds, a
// currency pair with a lot of 1 000 and bonds — and on a bond it yields the
// dirty price with the face and, for a foreign face settled in roubles, the
// conversion already applied. The long pair is tried first and the short one
// second; an answer with neither usable is "unknown", never zero.
func unitValueFromParams(resp *assets.GetAssetParamsResponse) (models.UnitValue, bool) {
	if resp == nil {
		return models.UnitValue{}, false
	}

	lot := float64(resp.GetTradeLotSize())
	if lot <= 0 {
		// trade_lot_size 0 means the API has none; the margin is then per piece.
		lot = 1
	}

	pairs := []struct {
		margin *money.Money
		rate   *decimal.Decimal
	}{
		{resp.GetLongInitialMargin(), resp.GetLongRiskRate()},
		{resp.GetShortInitialMargin(), resp.GetShortRiskRate()},
	}
	for _, p := range pairs {
		currency := strings.ToUpper(strings.TrimSpace(p.margin.GetCurrencyCode()))
		if currency == "" {
			continue
		}
		rate := positiveDecimal(p.rate)
		margin := moneyAmount(p.margin)
		if rate <= 0 || margin <= 0 {
			continue
		}
		value := margin * 100 / rate / lot
		if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
			continue
		}
		return models.UnitValue{Currency: currency, Value: value}, true
	}
	return models.UnitValue{}, false
}

// storeUnitValue files the per-piece value from a GetAssetParams answer under
// the full symbol and its ticker. An answer without a usable margin leaves the
// cache as it was: the value stays unknown rather than becoming zero.
func (c *Client) storeUnitValue(fullSymbol string, resp *assets.GetAssetParamsResponse) {
	if fullSymbol == "" {
		return
	}
	unit, ok := unitValueFromParams(resp)
	if !ok {
		return
	}

	c.assetMutex.Lock()
	defer c.assetMutex.Unlock()

	if c.unitValueCache == nil {
		c.unitValueCache = make(map[string]models.UnitValue)
	}
	c.unitValueCache[fullSymbol] = unit
	if ticker, _, found := strings.Cut(fullSymbol, "@"); found && ticker != "" {
		c.unitValueCache[ticker] = unit
	}
}

// GetUnitValue returns the value of one piece of an instrument in its
// settlement currency, by full symbol or bare ticker; false when it is not
// known. Like GetInstrumentCurrency it is a pure cache read, filled by the
// GetAssetParams call the terminal already makes for the trade lot.
func (c *Client) GetUnitValue(symbol string) (models.UnitValue, bool) {
	if symbol == "" {
		return models.UnitValue{}, false
	}

	c.assetMutex.RLock()
	defer c.assetMutex.RUnlock()

	if unit, ok := c.unitValueCache[symbol]; ok {
		return unit, true
	}
	if ticker, _, found := strings.Cut(symbol, "@"); found {
		unit, ok := c.unitValueCache[ticker]
		return unit, ok
	}
	return models.UnitValue{}, false
}

// positiveDecimal parses a google Decimal and answers 0 for anything that is
// not a positive finite number — absent, unparsable, zero, negative, NaN or
// infinite. strconv accepts "NaN" and "Inf", so the check is not redundant.
func positiveDecimal(d *decimal.Decimal) float64 {
	v := parseDecimalFloat(d)
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return 0
	}
	return v
}
