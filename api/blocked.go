package api

import (
	"errors"
	"strings"
)

// blockedMICs are the venues the broker moves an instrument to once it is
// blocked. The reconnaissance of 2026-09-11 found all of them on two: _SPBZ
// (436 instruments in the bulk list, every ticker suffixed .SPBZ) and _MMBZ
// (384, suffixed .MMBZ). The other service venues (_TRES, _CRYP, _EURB, _SMMA,
// _CMF, _NPRO, _SCI) have nothing to do with it. A variable rather than a
// constant, like fxSymbols: it is a table of observed facts, and a new venue
// is a line here.
//
// The venue is the only reliable mark. The name is cut at 30 characters on
// these venues (110 of the 820 lost the word BLOCKED with it) and says Block on
// 13 live instruments elsewhere; is_archived is false on all 17 012.
var blockedMICs = map[string]bool{
	"_SPBZ": true,
	"_MMBZ": true,
}

// ErrBlockedInstrument is the client's answer, in place of a request, for a
// call that hangs on a blocked instrument.
var ErrBlockedInstrument = errors.New("instrument is blocked")

// IsBlockedSymbol reports whether a full symbol sits on a blocked venue. The
// MIC is what follows the last "@" — the list carries FME@DE.SPBZ@_SPBZ — and a
// symbol without a MIC is not judged here: recognising one takes the bulk list
// (see blockedTwinLocked).
//
// It is a pure function because every guard needs it: on a blocked symbol
// LastQuote and GetAssetParams hang, and one such symbol in a SubscribeQuote
// subscription silences the whole subscription.
func IsBlockedSymbol(symbol string) bool {
	at := strings.LastIndex(symbol, "@")
	if at < 0 {
		return false
	}
	return blockedMICs[strings.ToUpper(symbol[at+1:])]
}

// blockedTwin is an instrument the bulk list files on a blocked venue, kept
// under the ticker a position of it would carry: FXRL.MMBZ@_MMBZ under FXRL.
type blockedTwin struct {
	Symbol string
	Name   string
}

// fileBlockedTwinLocked files a bulk-list instrument on a blocked venue under
// its base ticker — its own ticker without the venue suffix — so a position the
// broker sends without a MIC can be recognised by it. The first twin of a
// ticker wins, so the name is the same on every load. The caller must hold
// assetMutex for writing.
func (c *Client) fileBlockedTwinLocked(ticker, fullSymbol, name string) {
	if ticker == "" || !IsBlockedSymbol(fullSymbol) {
		return
	}

	mic := fullSymbol[strings.LastIndex(fullSymbol, "@")+1:]
	base := ticker
	if suffix := "." + strings.TrimPrefix(mic, "_"); len(ticker) > len(suffix) && strings.EqualFold(ticker[len(ticker)-len(suffix):], suffix) {
		base = ticker[:len(ticker)-len(suffix)]
	}

	if c.blockedTwinCache == nil {
		c.blockedTwinCache = make(map[string]blockedTwin)
	}
	if _, filed := c.blockedTwinCache[base]; filed {
		return
	}
	// The list pads a cut name with the space it was cut at.
	c.blockedTwinCache[base] = blockedTwin{Symbol: fullSymbol, Name: strings.TrimSpace(name)}
}

// isBlocked reports whether a symbol is blocked by either rule, given the
// symbol as the caller holds it and what getFullSymbol resolved it to.
func (c *Client) isBlocked(symbol, resolved string) bool {
	if IsBlockedSymbol(symbol) || IsBlockedSymbol(resolved) {
		return true
	}
	c.assetMutex.RLock()
	defer c.assetMutex.RUnlock()
	_, ok := c.blockedTwinLocked(symbol)
	return ok
}

// blockedTwinLocked returns the twin that marks a ticker the broker sent
// without a MIC as blocked. It answers only for a ticker the bulk list knows on
// no venue at all: 621 of the 820 blocked instruments share their base ticker
// with a live listing (AAPL.SPBZ and AAPL@RUSX), and a ticker the list knows
// live keeps resolving there, as it always did — the one position observed
// (FXRL) gives no licence to reinterpret those. Absence from the list alone
// proves nothing either: RU000A10AA02 is absent and is not blocked.
//
// The caller must hold assetMutex.
func (c *Client) blockedTwinLocked(ticker string) (blockedTwin, bool) {
	if ticker == "" || strings.Contains(ticker, "@") {
		return blockedTwin{}, false
	}
	if _, live := c.assetMicCache[ticker]; live {
		return blockedTwin{}, false
	}
	twin, ok := c.blockedTwinCache[ticker]
	return twin, ok
}
