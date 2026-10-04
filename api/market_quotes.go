package api

import (
	"fmt"
	"strings"

	"finam-terminal/models"
)

// GetMarketQuotes reads prices for known, fully qualified symbols without an
// account or asset/lot resolution. Each LastQuote has its own deadline. Blocked
// venues and incomplete symbols are skipped; ordinary failures cost only that
// symbol, while a rate limit ends the batch and returns prices already read.
func (c *Client) GetMarketQuotes(symbols []string) (map[string]*models.Quote, error) {
	quotes := make(map[string]*models.Quote, len(symbols))
	for _, symbol := range symbols {
		at := strings.LastIndex(symbol, "@")
		if at <= 0 || at == len(symbol)-1 || IsBlockedSymbol(symbol) {
			continue
		}
		resp, err := c.lastQuote(symbol)
		if err != nil {
			c.logGRPCError("MarketDataService", "LastQuote", err, fmt.Sprintf("Symbol: %s", symbol))
			if IsRateLimited(err) {
				return quotes, fmt.Errorf("quote request rate limit reached: %w", err)
			}
			continue
		}
		if resp == nil || resp.Quote == nil {
			continue
		}
		quotes[symbol] = quoteToModel(symbol, resp.Quote)
	}
	return quotes, nil
}
