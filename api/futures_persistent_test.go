package api

import (
	"context"
	"reflect"
	"testing"
	"time"

	"finam-terminal/commodity"
	"finam-terminal/config"
	"finam-terminal/models"

	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/assets"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func expireMOEXFuturesCache(c *Client) {
	at := time.Now().AddDate(0, 0, -1)
	c.futuresCatalogueAt = at
	for key, entry := range c.futuresCache {
		entry.fetchedAt = at
		c.futuresCache[key] = entry
	}
}

func TestMOEXFutureRestartSameDaySkipsEveryRPC(t *testing.T) {
	c := futuresTestClient(t, []*assets.Asset{
		{Symbol: "NGX6@RTSX", Type: "FUTURES"}, {Symbol: "NGZ6@RTSX", Type: "FUTURES"},
	}, func(_ context.Context, req *assets.GetAssetRequest, _ ...grpc.CallOption) (*assets.GetAssetResponse, error) {
		at := time.Now().AddDate(0, 1, 0)
		if req.Symbol == "NGZ6@RTSX" {
			at = at.AddDate(0, 1, 0)
		}
		return futureMetadata(at), nil
	})
	want, err := c.GetMOEXFuture("ACC1", "NG*@RTSX", 5)
	if err != nil {
		t.Fatal(err)
	}
	_, at, ok := c.CachedMOEXFuture("NG*@RTSX")
	if !ok || at.IsZero() {
		t.Fatal("successful metadata has no cache timestamp")
	}
	// No startup catalogue or service is needed to restore today's active.
	restarted := &Client{futuresCacheDir: c.futuresCacheDir}
	got, restoredAt, ok := restarted.CachedMOEXFuture("NG*@RTSX")
	if !ok || !restoredAt.Equal(at) || !reflect.DeepEqual(got, want) || got.Symbol != "NGX6@RTSX" {
		t.Fatalf("restored active = %+v, %v, %v; want %+v, %v", got, restoredAt, ok, want, at)
	}
	got.Symbol = "caller mutation"
	got, err = restarted.GetMOEXFuture("ACC2", "NG*@RTSX", 5)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("restart made an RPC or lost active: %+v, %v", got, err)
	}
	if _, _, ok := restarted.CachedMOEXFuture("NG?6@RTSX"); ok {
		t.Fatal("cache leaked across masks")
	}
	if _, err := restarted.GetMOEXFuture("", "NG*@RTSX", 5); err == nil {
		t.Fatal("missing account accepted even with cached metadata")
	}
}

func TestMOEXFutureNextLocalDateTracksOnlyActiveUnder24Hours(t *testing.T) {
	c := futuresTestClient(t, nil, func(context.Context, *assets.GetAssetRequest, ...grpc.CallOption) (*assets.GetAssetResponse, error) {
		return futureMetadata(time.Now().AddDate(0, 2, 0)), nil
	})
	now := time.Now().UTC()
	y, m, d := now.In(time.Local).Date()
	at := time.Date(y, m, d, 0, 0, 0, 0, time.Local).Add(-time.Nanosecond)
	old := models.FutureContract{Symbol: "NGX6@RTSX", Expiration: now.AddDate(0, 1, 0)}
	if err := config.SaveCommodityFuturesCacheEntryIn(c.futuresCacheDir, "NG*@RTSX", config.CommodityFuturesCacheEntry{
		UpdatedAt: at, Contract: old,
	}); err != nil {
		t.Fatal(err)
	}
	catalogueCalls, metadataCalls := 0, 0
	mock := c.assetsClient.(*mockAssetsServiceClient)
	mock.AssetsFunc = func(context.Context, *assets.AssetsRequest, ...grpc.CallOption) (*assets.AssetsResponse, error) {
		catalogueCalls++
		return &assets.AssetsResponse{Assets: []*assets.Asset{
			{Symbol: "NGX6@RTSX", Type: "FUTURES"}, {Symbol: "NGZ6@RTSX", Type: "FUTURES"},
		}}, nil
	}
	mock.GetAssetFunc = func(_ context.Context, req *assets.GetAssetRequest, _ ...grpc.CallOption) (*assets.GetAssetResponse, error) {
		metadataCalls++
		if req.Symbol != old.Symbol {
			t.Fatalf("daily tracking asked for another contract %q", req.Symbol)
		}
		return futureMetadata(now.AddDate(0, 2, 0)), nil
	}
	c.futuresCatalogueAt = at
	cached, cachedAt, ok := c.CachedMOEXFuture("NG*@RTSX")
	if !ok || !cachedAt.Equal(at) || !reflect.DeepEqual(cached, old) || catalogueCalls != 0 || metadataCalls != 0 {
		t.Fatalf("stale synchronous restore = %+v, %v, %v; RPCs %d/%d", cached, cachedAt, ok, catalogueCalls, metadataCalls)
	}
	got, err := c.GetMOEXFuture("ACC1", "NG*@RTSX", 5)
	if err != nil || got.Symbol != old.Symbol || catalogueCalls != 0 || metadataCalls != 1 {
		t.Fatalf("next date refresh = %+v, %v; RPCs %d/%d", got, err, catalogueCalls, metadataCalls)
	}
	_, updatedAt, _ := c.CachedMOEXFuture("NG*@RTSX")
	if !config.SameLocalDate(updatedAt, time.Now()) {
		t.Fatalf("successful refresh timestamp = %v", updatedAt)
	}
}

func TestMOEXFutureFailedRefreshPreservesPersistentTimestamp(t *testing.T) {
	c := futuresTestClient(t, []*assets.Asset{
		{Symbol: "NGX6@RTSX", Type: "FUTURES"}, {Symbol: "NGZ6@RTSX", Type: "FUTURES"},
	}, func(_ context.Context, req *assets.GetAssetRequest, _ ...grpc.CallOption) (*assets.GetAssetResponse, error) {
		if req.Symbol == "NGX6@RTSX" {
			return nil, status.Error(codes.ResourceExhausted, "quota")
		}
		return futureMetadata(time.Now().AddDate(0, 3, 0)), nil
	})
	at := time.Now().UTC().AddDate(0, 0, -1)
	want := models.FutureContract{Symbol: "NGX6@RTSX", Name: "Old near", Expiration: at.AddDate(0, 1, 0)}
	if err := config.SaveCommodityFuturesCacheEntryIn(c.futuresCacheDir, "NG*@RTSX", config.CommodityFuturesCacheEntry{
		UpdatedAt: at, Contract: want,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := c.GetMOEXFuture("ACC1", "NG*@RTSX", 5)
	if !IsRateLimited(err) || !reflect.DeepEqual(got, want) {
		t.Fatalf("failed refresh = %+v, %v; want old active", got, err)
	}
	for _, client := range []*Client{c, {futuresCacheDir: c.futuresCacheDir}} {
		got, gotAt, ok := client.CachedMOEXFuture("NG*@RTSX")
		if !ok || !gotAt.Equal(at) || !reflect.DeepEqual(got, want) {
			t.Fatalf("failed refresh stamped success: %+v, %v, %v", got, gotAt, ok)
		}
	}
}

func TestMOEXFutureEmptyAnswerPersists(t *testing.T) {
	c := futuresTestClient(t, nil, nil)
	if _, err := c.GetMOEXFuture("ACC1", "NG*@RTSX", 5); err != nil {
		t.Fatal(err)
	}
	restarted := &Client{futuresCacheDir: c.futuresCacheDir}
	got, at, ok := restarted.CachedMOEXFuture("NG*@RTSX")
	if !ok || at.IsZero() || got != (models.FutureContract{}) {
		t.Fatalf("successful empty answer not persisted: %+v, %v, %v", got, at, ok)
	}
	if _, err := restarted.GetMOEXFuture("ACC1", "NG*@RTSX", 5); err != nil {
		t.Fatalf("empty restart repeated network lookup: %v", err)
	}
}

func TestCachedMOEXFutureDoesNotWaitForStaleRefresh(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	c := futuresTestClient(t, []*assets.Asset{{Symbol: "NGX6@RTSX", Type: "FUTURES"}}, func(context.Context, *assets.GetAssetRequest, ...grpc.CallOption) (*assets.GetAssetResponse, error) {
		close(started)
		<-release
		return futureMetadata(time.Now().AddDate(0, 2, 0)), nil
	})
	at := time.Now().UTC().AddDate(0, 0, -1)
	want := models.FutureContract{Symbol: "NGX6@RTSX", Name: "Old", Expiration: at.AddDate(0, 1, 0)}
	if err := config.SaveCommodityFuturesCacheEntryIn(c.futuresCacheDir, "NG*@RTSX", config.CommodityFuturesCacheEntry{
		UpdatedAt: at, Contract: want,
	}); err != nil {
		t.Fatal(err)
	}
	go func() {
		_, err := c.GetMOEXFuture("ACC1", "NG*@RTSX", 5)
		done <- err
	}()
	t.Cleanup(func() { close(release); <-done })
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("refresh did not start")
	}
	type snapshot struct {
		contract models.FutureContract
		at       time.Time
		ok       bool
	}
	restored := make(chan snapshot, 1)
	go func() {
		contract, at, ok := c.CachedMOEXFuture("NG*@RTSX")
		restored <- snapshot{contract, at, ok}
	}()
	select {
	case got := <-restored:
		if !got.ok || !got.at.Equal(at) || !reflect.DeepEqual(got.contract, want) {
			t.Fatalf("in-flight cache = %+v; want old active", got)
		}
	case <-time.After(time.Second):
		t.Fatal("synchronous cache getter waited for network metadata")
	}
}

func TestMOEXFutureSameDayCacheRespectsRolloverBoundary(t *testing.T) {
	for _, tt := range []struct {
		name      string
		offset    time.Duration
		rollDays  int
		want      string
		wantCalls int
	}{
		{"before threshold", time.Hour, 5, "NGX6@RTSX", 0},
		{"at threshold", 0, 5, "NGZ6@RTSX", 2},
		{"past threshold", -time.Hour, 5, "NGZ6@RTSX", 2},
		{"changed roll days", time.Hour, 6, "NGZ6@RTSX", 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Now()
			near := models.FutureContract{Symbol: "NGX6@RTSX", Expiration: now.AddDate(0, 0, 5).Add(tt.offset)}
			far := now.AddDate(0, 1, 0)
			calls := 0
			c := futuresTestClient(t, []*assets.Asset{
				{Symbol: near.Symbol, Type: "FUTURES"}, {Symbol: "NGZ6@RTSX", Type: "FUTURES"},
			}, func(_ context.Context, req *assets.GetAssetRequest, _ ...grpc.CallOption) (*assets.GetAssetResponse, error) {
				calls++
				if req.Symbol == near.Symbol {
					return futureMetadata(near.Expiration), nil
				}
				return futureMetadata(far), nil
			})
			if err := config.SaveCommodityFuturesCacheEntryIn(c.futuresCacheDir, "NG*@RTSX", config.CommodityFuturesCacheEntry{
				UpdatedAt: now, Contract: near,
			}); err != nil {
				t.Fatal(err)
			}
			got, err := c.GetMOEXFuture("ACC1", "NG*@RTSX", tt.rollDays)
			if err != nil || got.Symbol != tt.want || calls != tt.wantCalls {
				t.Fatalf("same-day rollover = %+v, %v; calls %d; want %s, %d", got, err, calls, tt.want, tt.wantCalls)
			}
			restarted := &Client{futuresCacheDir: c.futuresCacheDir}
			cached, _, ok := restarted.CachedMOEXFuture("NG*@RTSX")
			if !ok || !reflect.DeepEqual(cached, got) {
				t.Fatalf("selected active not persisted: %+v, %v; want %+v", cached, ok, got)
			}
		})
	}
}

func TestMOEXFutureNoEligibleCandidatePersistsZero(t *testing.T) {
	expiration := time.Now().AddDate(0, 0, 4)
	c := futuresTestClient(t, []*assets.Asset{{Symbol: "NGX6@RTSX", Type: "FUTURES"}}, func(context.Context, *assets.GetAssetRequest, ...grpc.CallOption) (*assets.GetAssetResponse, error) {
		return futureMetadata(expiration), nil
	})
	got, err := c.GetMOEXFuture("ACC1", "NG*@RTSX", 5)
	if err != nil || got != (models.FutureContract{}) {
		t.Fatalf("no eligible future = %+v, %v", got, err)
	}
	restarted := &Client{futuresCacheDir: c.futuresCacheDir}
	got, err = restarted.GetMOEXFuture("ACC2", "NG*@RTSX", 5)
	if err != nil || got != (models.FutureContract{}) {
		t.Fatalf("zero answer not reused after restart: %+v, %v", got, err)
	}
}

func TestMOEXFutureSameDayEmptyCacheRespectsChangedRollDays(t *testing.T) {
	for _, tt := range []struct {
		name     string
		restart  bool
		rollDays int
		want     string
	}{
		{"memory less restrictive", false, 1, "NGX6@RTSX"},
		{"memory more restrictive", false, 6, ""},
		{"restart less restrictive", true, 1, "NGX6@RTSX"},
		{"restart more restrictive", true, 6, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			const mask = "NG*@RTSX"
			expiration := time.Now().AddDate(0, 0, 4)
			catalogue := []*assets.Asset{{Symbol: "NGX6@RTSX", Type: "FUTURES"}}
			metadataCalls, catalogueCalls := 0, 0
			c := futuresTestClient(t, catalogue, func(context.Context, *assets.GetAssetRequest, ...grpc.CallOption) (*assets.GetAssetResponse, error) {
				metadataCalls++
				return futureMetadata(expiration), nil
			})
			mock := c.assetsClient.(*mockAssetsServiceClient)
			mock.AssetsFunc = func(context.Context, *assets.AssetsRequest, ...grpc.CallOption) (*assets.AssetsResponse, error) {
				catalogueCalls++
				return &assets.AssetsResponse{Assets: catalogue}, nil
			}
			got, err := c.GetMOEXFuture("ACC1", mask, 5)
			if err != nil || got != (models.FutureContract{}) || metadataCalls != 1 {
				t.Fatalf("initial ineligible future = %+v, %v; metadata calls %d", got, err, metadataCalls)
			}
			if tt.restart {
				c = &Client{futuresCacheDir: c.futuresCacheDir, assetsClient: mock}
			}
			got, err = c.GetMOEXFuture("ACC2", mask, 5)
			if err != nil || got != (models.FutureContract{}) || metadataCalls != 1 || catalogueCalls != 0 {
				t.Fatalf("matching empty cache = %+v, %v; RPCs %d/%d", got, err, catalogueCalls, metadataCalls)
			}
			got, err = c.GetMOEXFuture("ACC2", mask, tt.rollDays)
			wantCatalogueCalls := 0
			if tt.restart {
				wantCatalogueCalls = 1
			}
			if err != nil || got.Symbol != tt.want || metadataCalls != 2 || catalogueCalls != wantCatalogueCalls {
				t.Fatalf("changed rollover = %+v, %v; RPCs %d/%d; want %q", got, err, catalogueCalls, metadataCalls, tt.want)
			}
			if tt.want != "" && !got.Expiration.Equal(expiration) {
				t.Fatalf("changed rollover expiration = %v; want %v", got.Expiration, expiration)
			}
			// The replacement answer, including another empty one, is reusable
			// for the new threshold both in memory and after another restart.
			for _, client := range []*Client{c, {futuresCacheDir: c.futuresCacheDir}} {
				reused, err := client.GetMOEXFuture("ACC3", mask, tt.rollDays)
				if err != nil || reused != got || metadataCalls != 2 || catalogueCalls != wantCatalogueCalls {
					t.Fatalf("new threshold cache = %+v, %v; RPCs %d/%d", reused, err, catalogueCalls, metadataCalls)
				}
			}
			entries, err := config.LoadCommodityFuturesCacheIn(c.futuresCacheDir)
			if err != nil || entries[mask].RollDays != tt.rollDays {
				t.Fatalf("persisted rollover threshold = %+v, %v; want %d", entries[mask], err, tt.rollDays)
			}
		})
	}
}

func TestMOEXFutureFailedRolloverPreservesExpiredContractAndTimestamp(t *testing.T) {
	c := futuresTestClient(t, []*assets.Asset{{Symbol: "NGZ6@RTSX", Type: "FUTURES"}}, func(context.Context, *assets.GetAssetRequest, ...grpc.CallOption) (*assets.GetAssetResponse, error) {
		return nil, status.Error(codes.ResourceExhausted, "quota")
	})
	at := time.Now().UTC()
	want := models.FutureContract{Symbol: "NGX6@RTSX", Expiration: at.Add(-time.Second)}
	if err := config.SaveCommodityFuturesCacheEntryIn(c.futuresCacheDir, "NG*@RTSX", config.CommodityFuturesCacheEntry{
		UpdatedAt: at, Contract: want,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := c.GetMOEXFuture("ACC1", "NG*@RTSX", 5)
	if !IsRateLimited(err) || !reflect.DeepEqual(got, want) {
		t.Fatalf("failed rollover = %+v, %v; want previous expired contract and error", got, err)
	}
	if _, eligible := commodity.SelectFuture([]models.FutureContract{got}, time.Now(), 5); eligible {
		t.Fatal("expired fallback treated as eligible")
	}
	for _, client := range []*Client{c, {futuresCacheDir: c.futuresCacheDir}} {
		cached, cachedAt, ok := client.CachedMOEXFuture("NG*@RTSX")
		if !ok || !cachedAt.Equal(at) || !reflect.DeepEqual(cached, want) {
			t.Fatalf("failed rollover changed persisted answer: %+v, %v, %v", cached, cachedAt, ok)
		}
	}
}
