package analytics

import (
	"math"
	"sort"
	"strings"
	"time"

	"finam-terminal/models"
)

// Group names shown on the overview. Kept as constants because the renderer
// and the tests both need to name the two special rows.
const (
	// GroupOther collects everything the type table has no row for. The
	// reconnaissance found four such values in the catalogue (INDICES,
	// SPREADS, SWAPS, OTHER) plus anything the API adds later, so this row is
	// a normal sight rather than a sign of a bug.
	GroupOther = "Прочее"

	// GroupCash is the money line of the breakdown.
	GroupCash = "Кэш"

	// defaultBaseCurrency is what an account with no cash lines is assumed to
	// be denominated in.
	defaultBaseCurrency = "RUB"

	// topHoldings is how many largest positions the concentration block shows.
	topHoldings = 3
)

// typeGroups maps Asset.Type to a display group. It is a var, not a const map,
// so a future API value can be routed without touching the algorithm.
//
// The keys are the values the bulk asset list actually uses (confirmed over the
// whole catalogue on 2026-09-03). Lookup is case-insensitive: the observed
// values are upper case, but nothing in the contract promises that.
var typeGroups = map[string]string{
	"EQUITIES":   "Акции",
	"BONDS":      "Облигации",
	"FUNDS":      "Фонды",
	"FUTURES":    "Фьючерсы",
	"OPTIONS":    "Опционы",
	"CURRENCIES": "Валюта",
}

// groupOrder fixes the row order of the breakdown. Sorting by value instead
// would make rows jump between five-second ticks, which is unreadable.
var groupOrder = []string{"Акции", "Облигации", "Фонды", "Фьючерсы", "Опционы", "Валюта", GroupOther}

// GroupForType maps an Asset.Type to its display group. An unknown type and an
// absent one both answer GroupOther — the distinction would not change what
// the user sees.
func GroupForType(assetType string) string {
	if group, ok := typeGroups[strings.ToUpper(strings.TrimSpace(assetType))]; ok {
		return group
	}
	return GroupOther
}

// StructureInput is everything the breakdown needs, all of it already in the
// terminal's memory: the account's positions and the quotes the stream
// delivers, the cash lines from the same GetAccount response, the instrument
// types from the startup asset cache, the sectors from the index composition,
// the instruments' money facts from the caches the lot resolution fills, and
// the rates the overview keeps. Nothing here costs a request.
type StructureInput struct {
	Positions []models.Position

	// Quotes is keyed by full symbol, as the UI stores it. A missing entry is
	// normal: the position falls back to the broker's own price.
	Quotes map[string]*models.Quote

	// Cash is the account's own money by currency.
	Cash []models.CashBalance

	// Types maps a symbol or ticker to Asset.Type.
	Types map[string]string

	// Sectors maps a symbol or ticker to a sector name, from the index
	// composition. A nil map means the composition is not loaded, which the
	// result reports through SectorsKnown so the screen can say so instead of
	// showing an all-Прочее breakdown that looks like real data.
	Sectors map[string]string

	// Instruments maps a symbol or ticker to what the API caches know about the
	// instrument's money — its currency, a bond's face and per-piece value, a
	// face currency the calendar named (see ValuePosition). A missing entry is
	// an instrument the broker has not described yet: it is counted in the
	// base currency and flagged, as it was before currencies were read.
	Instruments map[string]Instrument

	// Rates are the rouble rates of the currencies the overview has looked up.
	// A holding in a currency without one stays out of the base and is shown
	// in its own money: no sum ever adds two currencies without a rate.
	Rates map[string]models.FXRate

	// Now is what a rate's age is judged against; the zero time marks none.
	Now time.Time
}

// CurrencyRow is one row of the breakdown by currency.
type CurrencyRow struct {
	// Currency is the ISO code; empty on the Unresolved row.
	Currency string

	// Native is the money in its own currency: positions by magnitude plus
	// the positive cash line.
	Native float64

	// Value is Native in the base currency and Share its part of the base.
	// Both are 0 on a row without a rate, which is shown in its own money.
	Value float64
	Share float64

	// Rate converted Native into Value — 1 for the base currency — and Stale
	// marks one older than FXRateStaleAfter.
	Rate    Rate
	HasRate bool
	Stale   bool

	// Unresolved is the row of bonds whose face is in another currency than
	// they settle in and which currency is still being looked up. It is valued
	// in the base at the broker's per-piece value, so it has a share; only its
	// currency is open.
	Unresolved bool
}

// Group is one row of a breakdown: a name, the money behind it and its share
// of the base.
type Group struct {
	Name  string
	Value float64
	Share float64 // 0..1; 0 when the base is not positive
}

// Holding is one position in the concentration block.
type Holding struct {
	Ticker string
	Name   string
	Value  float64
	Share  float64
}

// Allocation is the computed portfolio breakdown.
//
// Valid is false when the base is not positive — an empty account, or one
// whose every position is unpriced. Shares are then all zero and the renderer
// shows "Н/Д" rather than bars that mean nothing.
type Allocation struct {
	Valid        bool
	Base         float64
	BaseCurrency string

	Groups    []Group // by instrument type, in groupOrder; empty groups omitted
	Cash      float64 // base-currency cash included in Base; never negative
	CashShare float64

	Sectors      []Group // by sector, largest first, GroupOther last
	SectorsKnown bool

	Top           []Holding // up to topHoldings largest positions by share
	PositionCount int       // positions that made it into the base

	// Exposure is the positions' part of Base, by magnitude and in the base
	// currency — the gross exposure leverage compares with equity.
	Exposure float64

	// Currencies is the breakdown by currency over the same base as Groups:
	// the base currency first, then the others by value, then the Unresolved
	// row, then the currencies without a rate in their own money. Ties break
	// by code, so rows never swap between ticks.
	Currencies []CurrencyRow

	// Skipped counts positions with no readable price, NoRateCount those left
	// out for being in a currency without a rate. Both are shown, because a
	// portfolio quietly missing rows is worse than one that admits it.
	Skipped     int
	NoRateCount int

	// UnknownCurrencyCount counts positions whose currency the broker did not
	// name, counted in the base currency; FaceUncheckedCount bonds valued in
	// their quote currency with nothing to check the face against;
	// FaceUnresolvedCount bonds with a face in another currency still being
	// looked up.
	UnknownCurrencyCount int
	FaceUncheckedCount   int
	FaceUnresolvedCount  int

	// Borrowed holds the negative cash lines: money owed, not held. They stay
	// out of the base and are reported separately.
	Borrowed []models.CashBalance

	// ForeignCash holds the positive cash lines in any other currency than the
	// base, with or without a rate. With one they are in the base too, under
	// «Валюта»; this list is the lines as the broker reported them.
	ForeignCash []models.CashBalance
}

// Structure computes the portfolio breakdown.
//
// The base of every share is the sum of position values plus the cash, all in
// the base currency — not the broker's equity, which includes margin money and
// would make the shares add up to something other than the portfolio. Every
// holding is valued in its own currency (ValuePosition) and converted by its
// rate (RateTo). A holding in a currency without a rate stays out of the base
// and is shown in its own money, so no sum ever adds two currencies without a
// rate, and the type and currency breakdowns always share one base.
func Structure(in StructureInput) Allocation {
	base := strings.ToUpper(baseCurrency(in.Cash))
	result := Allocation{
		BaseCurrency: base,
		SectorsKnown: in.Sectors != nil,
	}

	byGroup := make(map[string]float64, len(groupOrder))
	bySector := make(map[string]float64)
	rows := newCurrencyRows(base, in.Now)
	var holdings []Holding

	for _, p := range in.Positions {
		money := ValuePosition(p, quoteLast(in.Quotes, p.Symbol), lookupInstrument(in.Instruments, p), base)
		if !money.Valid {
			result.Skipped++
			continue
		}

		// Exposure, not direction: a short adds to the portfolio's size the
		// same way a long does, and must not cancel one out.
		native := math.Abs(money.Value)
		if native == 0 {
			continue
		}
		result.countState(money.State)

		rate := RateTo(money.Currency, base, in.Rates)
		if !rate.Valid {
			result.NoRateCount++
			rows.unrated(money.Currency, native)
			continue
		}
		value := native * rate.Value
		if math.IsNaN(value) || math.IsInf(value, 0) {
			result.Skipped++
			continue
		}

		rows.rated(money.Currency, native, value, rate, money.State == FaceUnresolved)
		result.Base += value
		result.Exposure += value
		result.PositionCount++
		byGroup[GroupForType(lookupString(in.Types, p))] += value
		bySector[sectorOf(in.Sectors, p)] += value
		holdings = append(holdings, Holding{Ticker: displayTicker(p), Name: p.Name, Value: value})
	}

	// A negative line is a margin loan: including it would net a debt against
	// real assets and understate every other share. Roubles held are «Кэш»;
	// money bought in another currency is a currency holding, filed under
	// «Валюта» the way the broker files it. An empty line is not money held.
	for _, c := range in.Cash {
		switch {
		case c.Amount < 0:
			result.Borrowed = append(result.Borrowed, c)
		case c.Amount == 0:
		case strings.EqualFold(c.Currency, base):
			result.Cash += c.Amount
			rows.rated(base, c.Amount, c.Amount, Rate{Value: 1, Valid: true}, false)
		default:
			result.ForeignCash = append(result.ForeignCash, c)
			if strings.TrimSpace(c.Currency) == "" {
				continue
			}
			rate := RateTo(c.Currency, base, in.Rates)
			if !rate.Valid {
				rows.unrated(c.Currency, c.Amount)
				continue
			}
			value := c.Amount * rate.Value
			if math.IsNaN(value) || math.IsInf(value, 0) {
				continue
			}
			rows.rated(c.Currency, c.Amount, value, rate, false)
			result.Base += value
			byGroup[currencyGroup] += value
		}
	}
	result.Base += result.Cash

	result.Valid = result.Base > 0
	result.CashShare = SafeShare(result.Cash, result.Base)
	result.Groups = orderedGroups(byGroup, result.Base)
	result.Sectors = sortedSectors(bySector, result.Base)
	result.Top = topByShare(holdings, result.Base)
	result.Currencies = rows.ordered(result.Base)

	return result
}

// currencyGroup is the type group foreign cash joins.
var currencyGroup = GroupForType("CURRENCIES")

// countState files a position under the counter its currency state calls for.
func (a *Allocation) countState(state CurrencyState) {
	switch state {
	case CurrencyUnknown:
		a.UnknownCurrencyCount++
	case FaceUnchecked:
		a.FaceUncheckedCount++
	case FaceUnresolved:
		a.FaceUnresolvedCount++
	}
}

// currencyRows accumulates the breakdown by currency.
type currencyRows struct {
	base       string
	now        time.Time
	byCode     map[string]*CurrencyRow
	unresolved *CurrencyRow
}

func newCurrencyRows(base string, now time.Time) *currencyRows {
	return &currencyRows{base: base, now: now, byCode: make(map[string]*CurrencyRow)}
}

// row returns the row for a currency, creating it with the rate it converts at.
func (r *currencyRows) row(code string, rate Rate) *CurrencyRow {
	code = strings.ToUpper(strings.TrimSpace(code))
	row, ok := r.byCode[code]
	if !ok {
		row = &CurrencyRow{
			Currency: code,
			Rate:     rate,
			HasRate:  rate.Valid,
			Stale:    rate.Valid && RateIsStale(rate.At, r.now),
		}
		r.byCode[code] = row
	}
	return row
}

// rated adds a converted holding: to its currency's row, or to the Unresolved
// row for a bond whose face currency is still being looked up.
func (r *currencyRows) rated(code string, native, value float64, rate Rate, unresolved bool) {
	if unresolved {
		if r.unresolved == nil {
			r.unresolved = &CurrencyRow{Unresolved: true, HasRate: true}
		}
		r.unresolved.Native += value
		r.unresolved.Value += value
		return
	}
	row := r.row(code, rate)
	row.Native += native
	row.Value += value
}

// unrated adds a holding that has no rate: its own money, no value in the base.
func (r *currencyRows) unrated(code string, native float64) {
	r.row(code, Rate{}).Native += native
}

// ordered lays the rows out: the base currency, the others by value, the
// Unresolved row, then the currencies without a rate by code.
func (r *currencyRows) ordered(total float64) []CurrencyRow {
	var (
		baseRow        *CurrencyRow
		rated, unrated []CurrencyRow
	)
	for code, row := range r.byCode {
		if !row.HasRate {
			row.Value, row.Share = 0, 0
			unrated = append(unrated, *row)
			continue
		}
		row.Share = SafeShare(row.Value, total)
		if code == r.base {
			baseRow = row
			continue
		}
		rated = append(rated, *row)
	}

	sort.Slice(rated, func(i, j int) bool {
		if rated[i].Value != rated[j].Value {
			return rated[i].Value > rated[j].Value
		}
		return rated[i].Currency < rated[j].Currency
	})
	sort.Slice(unrated, func(i, j int) bool { return unrated[i].Currency < unrated[j].Currency })

	out := make([]CurrencyRow, 0, len(r.byCode)+1)
	if baseRow != nil {
		out = append(out, *baseRow)
	}
	out = append(out, rated...)
	if r.unresolved != nil {
		r.unresolved.Share = SafeShare(r.unresolved.Value, total)
		out = append(out, *r.unresolved)
	}
	return append(out, unrated...)
}

// baseCurrency picks the currency the shares are denominated in: roubles when
// the account holds any, otherwise whatever the first cash line uses, and
// roubles again when there are no cash lines at all.
func baseCurrency(cash []models.CashBalance) string {
	for _, c := range cash {
		if strings.EqualFold(c.Currency, defaultBaseCurrency) {
			return defaultBaseCurrency
		}
	}
	for _, c := range cash {
		if c.Currency != "" {
			return c.Currency
		}
	}
	return defaultBaseCurrency
}

// sectorOf resolves a position's sector, defaulting to GroupOther for anything
// outside the index composition.
func sectorOf(sectors map[string]string, p models.Position) string {
	if sector := lookupString(sectors, p); sector != "" {
		return sector
	}
	return GroupOther
}

// lookupString finds a value by full symbol, falling back to the bare ticker.
// Callers hold whichever key their source used.
func lookupString(m map[string]string, p models.Position) string {
	if m == nil {
		return ""
	}
	if v, ok := m[p.Symbol]; ok {
		return v
	}
	return m[p.Ticker]
}

// lookupInstrument finds an instrument's money facts by full symbol, falling
// back to the bare ticker; a missing one is the zero Instrument.
func lookupInstrument(m map[string]Instrument, p models.Position) Instrument {
	if m == nil {
		return Instrument{}
	}
	if v, ok := m[p.Symbol]; ok {
		return v
	}
	return m[p.Ticker]
}

func quoteLast(quotes map[string]*models.Quote, symbol string) string {
	if q := quotes[symbol]; q != nil {
		return q.Last
	}
	return ""
}

func displayTicker(p models.Position) string {
	if p.Ticker != "" {
		return p.Ticker
	}
	return p.Symbol
}

// orderedGroups renders the type breakdown in the fixed display order, keeping
// only groups that hold something.
func orderedGroups(byGroup map[string]float64, base float64) []Group {
	groups := make([]Group, 0, len(byGroup))
	for _, name := range groupOrder {
		value, ok := byGroup[name]
		if !ok || value == 0 {
			continue
		}
		groups = append(groups, Group{Name: name, Value: value, Share: SafeShare(value, base)})
	}
	return groups
}

// sortedSectors orders sectors by value, largest first, with GroupOther last
// regardless of size — it is a leftover bucket, not a sector.
func sortedSectors(bySector map[string]float64, base float64) []Group {
	if len(bySector) == 0 {
		return nil
	}

	sectors := make([]Group, 0, len(bySector))
	for name, value := range bySector {
		sectors = append(sectors, Group{Name: name, Value: value, Share: SafeShare(value, base)})
	}

	sort.SliceStable(sectors, func(i, j int) bool {
		if (sectors[i].Name == GroupOther) != (sectors[j].Name == GroupOther) {
			return sectors[j].Name == GroupOther
		}
		if sectors[i].Value != sectors[j].Value {
			return sectors[i].Value > sectors[j].Value
		}
		return sectors[i].Name < sectors[j].Name
	})
	return sectors
}

// topByShare returns the largest holdings, at most topHoldings of them.
func topByShare(holdings []Holding, base float64) []Holding {
	if len(holdings) == 0 || base <= 0 {
		return nil
	}

	sort.SliceStable(holdings, func(i, j int) bool {
		if holdings[i].Value != holdings[j].Value {
			return holdings[i].Value > holdings[j].Value
		}
		return holdings[i].Ticker < holdings[j].Ticker
	})

	top := holdings[:min(topHoldings, len(holdings))]
	for i := range top {
		top[i].Share = SafeShare(top[i].Value, base)
	}
	return top
}
