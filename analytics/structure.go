package analytics

import (
	"math"
	"sort"
	"strings"

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
// types from the startup asset cache, and the sectors from the index
// composition. Nothing here costs a request.
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

	// Currencies maps a symbol or ticker to the instrument's currency, where
	// it is known. A position whose currency is known and differs from the
	// base is excluded from the shares — there are no exchange rates in the
	// Trade API — and counted in ForeignCount. An unknown currency is kept:
	// guessing it out would silently shrink the portfolio.
	Currencies map[string]string

	// FaceValues maps a symbol or ticker to a bond's face value, enabling the
	// percent-of-par formula for it. Empty today; see PositionValue.
	FaceValues map[string]float64
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

	// Skipped counts positions with no readable price, ForeignCount those
	// excluded for being in another currency. Both are shown, because a
	// portfolio quietly missing rows is worse than one that admits it.
	Skipped      int
	ForeignCount int

	// Borrowed holds the negative cash lines: money owed, not held. They stay
	// out of the base and are reported separately.
	Borrowed []models.CashBalance

	// ForeignCash holds the positive cash lines in any other currency. They
	// cannot join the base — there are no exchange rates in the Trade API — but
	// they are money the account holds, and dropping them would make it
	// disappear from the screen.
	ForeignCash []models.CashBalance
}

// Structure computes the portfolio breakdown.
//
// The base of every share is the sum of position values plus base-currency
// cash — not the broker's equity, which includes margin money and would make
// the shares add up to something other than the portfolio.
func Structure(in StructureInput) Allocation {
	result := Allocation{
		BaseCurrency: baseCurrency(in.Cash),
		SectorsKnown: in.Sectors != nil,
	}

	byGroup := make(map[string]float64, len(groupOrder))
	bySector := make(map[string]float64)
	var holdings []Holding

	for _, p := range in.Positions {
		if isForeign(p, in.Currencies, result.BaseCurrency) {
			result.ForeignCount++
			continue
		}

		value, ok := PositionValue(p.Quantity, quoteLast(in.Quotes, p.Symbol), p.CurrentPrice, lookup(in.FaceValues, p))
		if !ok {
			result.Skipped++
			continue
		}

		// Exposure, not direction: a short adds to the portfolio's size the
		// same way a long does, and must not cancel one out.
		value = math.Abs(value)
		if value == 0 {
			continue
		}

		result.Base += value
		result.PositionCount++
		byGroup[GroupForType(lookupString(in.Types, p))] += value
		bySector[sectorOf(in.Sectors, p)] += value
		holdings = append(holdings, Holding{Ticker: displayTicker(p), Name: p.Name, Value: value})
	}

	cash, borrowed, foreign := cashInBase(in.Cash, result.BaseCurrency)
	result.Cash = cash
	result.Borrowed = borrowed
	result.ForeignCash = foreign
	result.Base += cash

	result.Valid = result.Base > 0
	result.CashShare = SafeShare(cash, result.Base)
	result.Groups = orderedGroups(byGroup, result.Base)
	result.Sectors = sortedSectors(bySector, result.Base)
	result.Top = topByShare(holdings, result.Base)

	return result
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

// cashInBase sums the base-currency cash that counts as a holding, and
// collects the negative lines and the positive foreign ones separately. A
// negative balance is a margin loan: including it would net a debt against real
// assets and understate every other share. A foreign balance cannot be
// converted, so it is reported beside the base rather than inside it; an empty
// line is not money held and is reported nowhere.
func cashInBase(cash []models.CashBalance, base string) (float64, []models.CashBalance, []models.CashBalance) {
	var total float64
	var borrowed, foreign []models.CashBalance

	for _, c := range cash {
		switch {
		case c.Amount < 0:
			borrowed = append(borrowed, c)
		case strings.EqualFold(c.Currency, base):
			total += c.Amount
		case c.Amount > 0:
			foreign = append(foreign, c)
		}
	}
	return total, borrowed, foreign
}

// isForeign reports whether a position is denominated in a currency known to
// differ from the base. Unknown currencies are not foreign — see
// StructureInput.Currencies.
func isForeign(p models.Position, currencies map[string]string, base string) bool {
	currency := lookupString(currencies, p)
	return currency != "" && !strings.EqualFold(currency, base)
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

func lookup(m map[string]float64, p models.Position) float64 {
	if m == nil {
		return 0
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
