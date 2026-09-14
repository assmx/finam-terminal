package api

import (
	"fmt"
	"math"
	"strings"

	"finam-terminal/models"

	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/assets"
	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/corporateactions"
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

// bondEventsCalendar is the calendar-cache kind of the bond-event calendar.
const bondEventsCalendar = "bondEvents"

// faceCurrencyLookupLimit is how many events a face-currency lookup asks for.
// One event naming a currency is enough; a few more cover an entry that names
// none.
const faceCurrencyLookupLimit = 5

// currencySymbols maps the symbols the bond calendar writes onto ISO codes.
// These four are the ones the reconnaissance of 2026-09-10 saw. "¥" is the
// yuan: every bond carrying it was a CNY bond, and MOEX lists no yen bonds.
var currencySymbols = map[string]string{
	"₽": "RUB",
	"$": "USD",
	"€": "EUR",
	"¥": "CNY",
}

// currencyCode turns what a calendar or a price field says about a currency
// into an ISO code: a known symbol, or three Latin letters in any case. RUR and
// SUR, the codes older MOEX systems still use for the rouble, become RUB.
// Anything else — another symbol, a percent sign, a blank — is not a currency
// this terminal can name.
func currencyCode(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if code, ok := currencySymbols[s]; ok {
		return code, true
	}
	if len(s) != 3 {
		return "", false
	}
	for _, r := range s {
		if (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
			return "", false
		}
	}
	code := strings.ToUpper(s)
	if code == "RUR" || code == "SUR" {
		return "RUB", true
	}
	return code, true
}

// faceCurrencyFromEvents returns the first currency a bond calendar names, or
// "" when it names none this terminal can read.
func faceCurrencyFromEvents(events []models.BondEvent) string {
	for _, e := range events {
		if code, ok := currencyCode(e.Currency); ok {
			return code
		}
	}
	return ""
}

// storeFaceCurrency remembers a bond's face currency for the session. A face
// currency does not change, so unlike the day-long calendar cache nothing
// expires it.
func (c *Client) storeFaceCurrency(symbol, code string) {
	c.assetMutex.Lock()
	defer c.assetMutex.Unlock()

	if c.faceCurrencyCache == nil {
		c.faceCurrencyCache = make(map[string]string)
	}
	c.faceCurrencyCache[symbol] = code
}

// BondFaceCurrencyCached returns a bond's face currency if it is already known
// — looked up earlier this session, or readable from a calendar another screen
// loaded today — and never issues a request. The second result is false when
// neither is available; an empty code with true means the calendar was read and
// named no currency.
func (c *Client) BondFaceCurrencyCached(symbol string) (string, bool) {
	if symbol == "" {
		return "", false
	}

	c.assetMutex.RLock()
	code, ok := c.faceCurrencyCache[symbol]
	c.assetMutex.RUnlock()
	if ok {
		return code, true
	}

	if events, ok := calendarPeek[models.BondEvent](c, bondEventsCalendar, symbol); ok {
		code := faceCurrencyFromEvents(events)
		c.storeFaceCurrency(symbol, code)
		return code, true
	}
	return "", false
}

// GetBondFaceCurrency returns the ISO code of a bond's face currency.
//
// The calendar is the only place the API names it. GetAsset's
// bond_details.currency is "%" on every bond, and quote_currency is the
// settlement currency, which for a replacement bond is the rouble although its
// face is in dollars. So the answer comes from, in order: the session cache;
// the day-long calendar cache, if the payout screen or a profile already
// loaded this bond; one future-calendar request; and the past calendar only
// when nothing is scheduled. A calendar that names no currency is an answer
// ("", nil) and is cached. A failure is not cached, so the next lookup asks
// again; a rate limit is recognisable with IsRateLimited.
//
// The reconnaissance saw a third of calendar calls time out after 30 s, so
// callers run this off the UI thread and only for the bonds that need it.
func (c *Client) GetBondFaceCurrency(symbol string) (string, error) {
	if symbol == "" {
		return "", nil
	}
	if code, ok := c.BondFaceCurrencyCached(symbol); ok {
		return code, nil
	}

	events, err := c.futureBondEvents(symbol)
	if err != nil {
		return "", err
	}
	if len(events) == 0 {
		// A bond with nothing scheduled still has a history that names it.
		if events, err = c.pastBondEvents(symbol); err != nil {
			return "", err
		}
	}

	mapped := make([]models.BondEvent, 0, len(events))
	for _, e := range events {
		if e != nil {
			mapped = append(mapped, mapBondEvent(e, false))
		}
	}
	code := faceCurrencyFromEvents(mapped)
	c.storeFaceCurrency(symbol, code)
	return code, nil
}

// futureBondEvents asks for a few of a bond's upcoming events under its own
// deadline.
func (c *Client) futureBondEvents(symbol string) ([]*corporateactions.BondEvent, error) {
	ctx, cancel := c.getContext()
	defer cancel()

	resp, err := c.corporateActionsClient.GetFutureBondsEvents(ctx, &corporateactions.GetFutureBondsEventsRequest{
		Symbol:        symbol,
		SortDirection: corporateactions.SortDirection_ASC,
		Limit:         faceCurrencyLookupLimit,
	})
	if err != nil {
		c.logGRPCError("CorporateActionsService", "GetFutureBondsEvents", err, fmt.Sprintf("Symbol: %s", symbol))
		return nil, fmt.Errorf("failed to get future bond events: %w", err)
	}
	return resp.GetEvents(), nil
}

// pastBondEvents asks for a few of a bond's recent events under its own
// deadline. It sends no interval: this endpoint refuses a date_to of today (see
// fetchBondEvents).
func (c *Client) pastBondEvents(symbol string) ([]*corporateactions.BondEvent, error) {
	ctx, cancel := c.getContext()
	defer cancel()

	resp, err := c.corporateActionsClient.GetPastBondsEvents(ctx, &corporateactions.GetPastBondsEventsRequest{
		Symbol:        symbol,
		SortDirection: corporateactions.SortDirection_DESC,
		Limit:         faceCurrencyLookupLimit,
	})
	if err != nil {
		c.logGRPCError("CorporateActionsService", "GetPastBondsEvents", err, fmt.Sprintf("Symbol: %s", symbol))
		return nil, fmt.Errorf("failed to get past bond events: %w", err)
	}
	return resp.GetEvents(), nil
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
