package api

import (
	"context"
	"fmt"
	"log"
	"path"
	"sort"
	"strings"
	"time"

	"finam-terminal/commodity"
	"finam-terminal/config"
	"finam-terminal/models"

	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/assets"
	"google.golang.org/grpc/metadata"
)

// A family can contain many listed series; spread metadata requests out rather
// than turning the startup catalogue into a burst against GetAsset's quota.
var moexFuturesPace = 150 * time.Millisecond

type moexFuturesCacheEntry struct {
	contract  models.FutureContract
	fetchedAt time.Time
	rollDays  int
}

// GetMOEXFuture resolves one active contract for a narrow family mask against
// the broker catalogue and commodity.SelectFuture's rollover threshold.
// Discovery metadata exists only for this pass; only the selected contract
// persists, shared by accounts. Daily refresh follows that contract alone.
// Full discovery runs initially or when the tracked contract must roll.
// A successful empty answer is cached until the next local calendar date, for
// the same rollover threshold only.
//
// Failed refreshes return the previous contract AND the error without changing
// its timestamp. Callers must check its eligibility before displaying/trading it.
// Missing expiry fails the whole pass rather than guessing a later contract.
// This blocking method belongs off the UI loop. Calls share a pass; Close
// cancels requests and pacing. There are no automatic retries or AllAssets walks.
func (c *Client) GetMOEXFuture(accountID, mask string, rollDays int) (models.FutureContract, error) {
	if strings.TrimSpace(accountID) == "" {
		return models.FutureContract{}, fmt.Errorf("MOEX futures metadata requires an account")
	}
	if err := validateMOEXFuturesMask(mask); err != nil {
		return models.FutureContract{}, err
	}
	if rollDays < 0 {
		return models.FutureContract{}, fmt.Errorf("MOEX futures rollover days must not be negative")
	}

	c.futuresMu.Lock()
	defer c.futuresMu.Unlock()

	c.loadMOEXFuturesCache()
	c.futuresCacheMu.RLock()
	entry, exists := c.futuresCache[mask]
	c.futuresCacheMu.RUnlock()
	cached, fetchedAt := entry.contract, entry.fetchedAt
	ctx := c.futuresContext
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return cached, err
	}
	now := time.Now()
	_, eligible := commodity.SelectFuture([]models.FutureContract{cached}, now, rollDays)
	emptyForRollDays := cached == (models.FutureContract{}) && entry.rollDays == rollDays
	if exists && config.SameLocalDate(fetchedAt, now) && (emptyForRollDays || eligible) {
		return cached, nil
	}
	var tracked models.FutureContract
	if eligible {
		// Tracking an existing active future costs exactly one metadata request,
		// not another walk over all family expirations or a bulk catalogue.
		var err error
		tracked, err = c.fetchMOEXFuture(ctx, accountID, models.SecurityInfo{Symbol: cached.Symbol, Name: cached.Name})
		if err != nil {
			return cached, err
		}
	}
	at := time.Now()
	selected, selectedOK := commodity.SelectFuture([]models.FutureContract{tracked}, at, rollDays)
	if !selectedOK {
		if err := c.ensureMOEXFutureCatalogue(ctx); err != nil {
			return cached, err
		}
		candidates, err := c.moexFutureCandidates(mask)
		if err != nil {
			return cached, err
		}
		if tracked.Symbol != "" {
			// Its expiry was just refreshed: do not request it twice during a roll.
			for i, candidate := range candidates {
				if candidate.Symbol == tracked.Symbol {
					candidates = append(candidates[:i], candidates[i+1:]...)
					break
				}
			}
		}
		contracts, err := c.fetchMOEXFutures(ctx, accountID, candidates, tracked.Symbol != "")
		if err != nil {
			return cached, err
		}
		at = time.Now()
		selected, _ = commodity.SelectFuture(contracts, at, rollDays)
	}
	c.futuresCacheMu.Lock()
	c.futuresCache[mask] = moexFuturesCacheEntry{
		contract: selected, fetchedAt: at, rollDays: rollDays,
	}
	c.futuresCacheMu.Unlock()
	if err := c.saveMOEXFuturesCache(mask, config.CommodityFuturesCacheEntry{
		UpdatedAt: at, Contract: selected, RollDays: rollDays,
	}); err != nil {
		// Persistence is optional: a filesystem failure must not discard a
		// successful broker answer or label it as a failed metadata lookup.
		log.Printf("[WARN] MOEX futures cache not saved: %v", err)
	}
	return selected, nil
}

// CachedMOEXFuture restores the disk cache once and returns the active contract
// plus its last successful metadata timestamp, without network I/O or waiting
// for an in-flight refresh. The raw cached contract may need rollover; callers
// must check eligibility before treating it as live.
func (c *Client) CachedMOEXFuture(mask string) (models.FutureContract, time.Time, bool) {
	if validateMOEXFuturesMask(mask) != nil {
		return models.FutureContract{}, time.Time{}, false
	}
	c.loadMOEXFuturesCache()
	c.futuresCacheMu.RLock()
	cached, exists := c.futuresCache[mask]
	c.futuresCacheMu.RUnlock()
	return cached.contract, cached.fetchedAt, exists
}

func (c *Client) loadMOEXFuturesCache() {
	c.futuresCacheMu.RLock()
	loaded := c.futuresCacheLoaded
	c.futuresCacheMu.RUnlock()
	if loaded {
		return
	}
	c.futuresCacheMu.Lock()
	defer c.futuresCacheMu.Unlock()
	if c.futuresCacheLoaded {
		return
	}
	c.futuresCacheLoaded = true
	if c.futuresCache == nil {
		c.futuresCache = make(map[string]moexFuturesCacheEntry)
	}
	var entries map[string]config.CommodityFuturesCacheEntry
	var err error
	if c.futuresCacheDir == "" {
		entries, err = config.LoadCommodityFuturesCache()
	} else {
		entries, err = config.LoadCommodityFuturesCacheIn(c.futuresCacheDir)
	}
	if err != nil {
		log.Printf("[WARN] MOEX futures cache not loaded: %v", err)
		return
	}
	for mask, entry := range entries {
		if entry.UpdatedAt.IsZero() || validateMOEXFuturesMask(mask) != nil {
			continue
		}
		contract := entry.Contract
		complete := contract == (models.FutureContract{})
		if !complete {
			matched, _ := path.Match(mask, contract.Symbol)
			complete = matched && isMOEXFutureSymbol(contract.Symbol) && !contract.Expiration.IsZero()
		}
		if _, exists := c.futuresCache[mask]; complete && !exists {
			c.futuresCache[mask] = moexFuturesCacheEntry{
				contract: contract, fetchedAt: entry.UpdatedAt, rollDays: entry.RollDays,
			}
		}
	}
}

func (c *Client) saveMOEXFuturesCache(mask string, entry config.CommodityFuturesCacheEntry) error {
	if c.futuresCacheDir == "" {
		return config.SaveCommodityFuturesCacheEntry(mask, entry)
	}
	return config.SaveCommodityFuturesCacheEntryIn(c.futuresCacheDir, mask, entry)
}

func validateMOEXFuturesMask(mask string) error {
	ticker, mic, ok := strings.Cut(mask, "@")
	if !ok || mic != "RTSX" || ticker == "" || strings.ContainsAny(ticker, "@/\\ \t\r\n") {
		return fmt.Errorf("invalid MOEX futures mask %q: expected a family mask at @RTSX", mask)
	}
	prefix := ticker
	if wildcard := strings.IndexAny(ticker, "*?["); wildcard >= 0 {
		prefix = ticker[:wildcard]
	}
	if prefix == "" {
		return fmt.Errorf("invalid MOEX futures mask %q: a literal family prefix is required", mask)
	}
	if _, err := path.Match(mask, ""); err != nil {
		return fmt.Errorf("invalid MOEX futures mask %q: %w", mask, err)
	}
	return nil
}

func isMOEXFutureSymbol(symbol string) bool {
	ticker, mic, ok := strings.Cut(symbol, "@")
	return ok && mic == "RTSX" && ticker != "" && !strings.ContainsAny(ticker, "@/\\*?[] \t\r\n")
}

// Only the dedicated candidate cache is refreshed; search and lot cache
// semantics are unchanged. A failed bulk request leaves the last list intact.
// GetMOEXFuture holds futuresMu, so families share one catalogue refresh.
func (c *Client) ensureMOEXFutureCatalogue(ctx context.Context) error {
	c.assetMutex.RLock()
	fresh := c.futuresCandidates != nil && config.SameLocalDate(c.futuresCatalogueAt, time.Now())
	c.assetMutex.RUnlock()
	if fresh {
		return nil
	}
	if c.assetsClient == nil {
		return fmt.Errorf("MOEX futures catalogue is unavailable")
	}
	requestCtx, cancel := c.moexFuturesRequestContext(ctx)
	defer cancel()
	//nolint:staticcheck // Reuse the same bulk catalogue as startup, not AllAssets.
	resp, err := c.assetsClient.Assets(requestCtx, &assets.AssetsRequest{})
	if err != nil {
		c.logGRPCError("AssetsService", "Assets", err)
		return fmt.Errorf("failed to refresh MOEX futures catalogue: %w", err)
	}
	if resp == nil {
		return fmt.Errorf("failed to refresh MOEX futures catalogue: empty response")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	candidates := make([]models.SecurityInfo, 0)
	for _, asset := range resp.Assets {
		if asset == nil || asset.IsArchived || asset.Type != "FUTURES" {
			continue
		}
		symbol := asset.Symbol
		if !strings.Contains(symbol, "@") && asset.Ticker != "" && asset.Mic != "" {
			symbol = asset.Ticker + "@" + asset.Mic
		}
		if isMOEXFutureSymbol(symbol) {
			candidates = append(candidates, models.SecurityInfo{
				Ticker: asset.Ticker, Symbol: symbol, Name: asset.Name, Type: asset.Type,
			})
		}
	}
	c.assetMutex.Lock()
	c.futuresCandidates = candidates
	c.futuresCatalogueAt = time.Now()
	c.assetMutex.Unlock()
	return nil
}

func (c *Client) moexFuturesRequestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	c.tokenMutex.RLock()
	requestCtx = metadata.AppendToOutgoingContext(requestCtx, "Authorization", c.token)
	c.tokenMutex.RUnlock()
	return requestCtx, cancel
}

// Take only matching candidates under assetMutex, then release it before any
// network I/O. General search semantics (including archived entries) stay intact.
func (c *Client) moexFutureCandidates(mask string) ([]models.SecurityInfo, error) {
	c.assetMutex.RLock()
	defer c.assetMutex.RUnlock()
	if c.futuresCandidates == nil {
		return nil, fmt.Errorf("MOEX futures catalogue is unavailable: startup asset loading did not succeed")
	}
	var candidates []models.SecurityInfo
	seen := make(map[string]struct{})
	for _, candidate := range c.futuresCandidates {
		matched, err := path.Match(mask, candidate.Symbol)
		if err != nil {
			return nil, err
		}
		if !matched {
			continue
		}
		if _, duplicate := seen[candidate.Symbol]; duplicate {
			continue
		}
		seen[candidate.Symbol] = struct{}{}
		candidates = append(candidates, candidate)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Symbol < candidates[j].Symbol })
	return candidates, nil
}

func (c *Client) fetchMOEXFutures(ctx context.Context, accountID string, candidates []models.SecurityInfo, alreadyRequested bool) ([]models.FutureContract, error) {
	contracts := make([]models.FutureContract, 0, len(candidates))
	for i, candidate := range candidates {
		if (i > 0 || alreadyRequested) && moexFuturesPace > 0 {
			timer := time.NewTimer(moexFuturesPace)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
		contract, err := c.fetchMOEXFuture(ctx, accountID, candidate)
		if err != nil {
			return nil, err
		}
		contracts = append(contracts, contract)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return contracts, nil
}

func (c *Client) fetchMOEXFuture(ctx context.Context, accountID string, candidate models.SecurityInfo) (models.FutureContract, error) {
	if err := ctx.Err(); err != nil {
		return models.FutureContract{}, err
	}
	if c.assetsClient == nil {
		return models.FutureContract{}, fmt.Errorf("MOEX futures metadata is unavailable")
	}
	requestCtx, cancel := c.moexFuturesRequestContext(ctx)
	resp, err := c.assetsClient.GetAsset(requestCtx, &assets.GetAssetRequest{
		Symbol: candidate.Symbol, AccountId: accountID,
	})
	cancel()
	if err != nil {
		c.logGRPCError("AssetsService", "GetAsset", err,
			fmt.Sprintf("Symbol: %s", candidate.Symbol), fmt.Sprintf("AccountId: %s", accountID))
		return models.FutureContract{}, fmt.Errorf("failed to get MOEX future %s: %w", candidate.Symbol, err)
	}
	if err := ctx.Err(); err != nil {
		return models.FutureContract{}, err
	}
	future := resp.GetFutureDetails()
	if future == nil || future.ExpirationDate == nil || future.ExpirationDate.CheckValid() != nil {
		return models.FutureContract{}, fmt.Errorf("MOEX future %s has no valid expiration timestamp", candidate.Symbol)
	}
	expiration := future.ExpirationDate.AsTime()
	if expiration.IsZero() {
		return models.FutureContract{}, fmt.Errorf("MOEX future %s has no expiration timestamp", candidate.Symbol)
	}
	c.storeInstrumentCurrency(resp, candidate.Symbol)
	c.storeAssetLotSize(resp, candidate.Symbol)
	name := strings.TrimSpace(resp.Name)
	if name == "" {
		name = strings.TrimSpace(candidate.Name)
	}
	return models.FutureContract{
		Symbol: candidate.Symbol, Name: name, Expiration: expiration, Decimals: int(resp.Decimals),
	}, nil
}
