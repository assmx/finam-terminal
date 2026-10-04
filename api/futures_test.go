package api

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"finam-terminal/models"

	"github.com/FinamWeb/finam-trade-api/go/grpc/tradeapi/v1/assets"
	"google.golang.org/genproto/googleapis/type/decimal"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func futureMetadata(expiration time.Time) *assets.GetAssetResponse {
	return &assets.GetAssetResponse{
		Type: "FUTURES", Name: "Natural gas", Decimals: 3,
		AssetDetails: &assets.GetAssetResponse_FutureDetails_{
			FutureDetails: &assets.GetAssetResponse_FutureDetails{
				ExpirationDate: timestamppb.New(expiration),
			},
		},
	}
}

func futuresTestClient(t *testing.T, catalogue []*assets.Asset, getAsset func(context.Context, *assets.GetAssetRequest, ...grpc.CallOption) (*assets.GetAssetResponse, error)) *Client {
	t.Helper()
	oldPace := moexFuturesPace
	moexFuturesPace = 0
	t.Cleanup(func() { moexFuturesPace = oldPace })
	c := &Client{
		futuresCacheDir: t.TempDir(),
		assetMicCache:   make(map[string]string), assetTypeCache: make(map[string]string),
		instrumentNameCache: make(map[string]string),
		assetsClient: &mockAssetsServiceClient{
			AssetsFunc: func(context.Context, *assets.AssetsRequest, ...grpc.CallOption) (*assets.AssetsResponse, error) {
				return &assets.AssetsResponse{Assets: catalogue}, nil
			},
			GetAssetFunc: getAsset,
		},
	}
	if err := c.loadAssetCache(); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestGetMOEXFutureFiltersCatalogueAndSelectsRealExpiry(t *testing.T) {
	first := time.Now().AddDate(0, 1, 0)
	later := first.AddDate(0, 1, 0)
	catalogue := []*assets.Asset{
		{Symbol: "NGZ6@RTSX", Ticker: "NGZ6", Mic: "RTSX", Type: "FUTURES"},
		{Symbol: "NGX6@RTSX", Ticker: "NGX6", Mic: "RTSX", Type: "FUTURES"},
		{Symbol: "NGW6@RTSX", Ticker: "NGW6", Mic: "RTSX", Type: "FUTURES"},
		{Symbol: "NGQ6@RTSX", Ticker: "NGQ6", Mic: "RTSX", Type: "FUTURES", IsArchived: true},
		{Symbol: "NG@RTSX", Ticker: "NG", Mic: "RTSX", Type: "EQUITIES"},
		{Symbol: "NGZ6@XNYS", Ticker: "NGZ6", Mic: "XNYS", Type: "FUTURES"},
		{Symbol: "BRZ6@RTSX", Ticker: "BRZ6", Mic: "RTSX", Type: "FUTURES"},
		nil,
	}
	var calls []string
	c := futuresTestClient(t, catalogue, func(_ context.Context, req *assets.GetAssetRequest, _ ...grpc.CallOption) (*assets.GetAssetResponse, error) {
		calls = append(calls, req.Symbol)
		if req.AccountId != "ACC1" {
			t.Fatalf("metadata account = %q", req.AccountId)
		}
		if req.Symbol == "NGZ6@RTSX" {
			return futureMetadata(later), nil
		}
		return futureMetadata(first), nil
	})
	got, err := c.GetMOEXFuture("ACC1", "NG*@RTSX", 5)
	if err != nil {
		t.Fatal(err)
	}
	want := models.FutureContract{Symbol: "NGW6@RTSX", Name: "Natural gas", Expiration: first, Decimals: 3}
	if got.Symbol != want.Symbol || got.Name != want.Name || got.Decimals != want.Decimals || !got.Expiration.Equal(want.Expiration) {
		t.Fatalf("contracts = %+v; want %+v", got, want)
	}
	if len(calls) != 3 {
		t.Fatalf("metadata calls = %v", calls)
	}
	if len(c.securityCache) != len(catalogue)-1 {
		t.Fatal("futures filtering changed the general search catalogue")
	}
}

func TestGetMOEXFutureSharesAccountsIsolatesMasksAndRefetches(t *testing.T) {
	date := time.Now().AddDate(0, 2, 0)
	calls := 0
	c := futuresTestClient(t, []*assets.Asset{{Symbol: "NGX6@RTSX", Type: "FUTURES"}}, func(context.Context, *assets.GetAssetRequest, ...grpc.CallOption) (*assets.GetAssetResponse, error) {
		calls++
		return futureMetadata(date.AddDate(0, 0, calls)), nil
	})
	first, err := c.GetMOEXFuture("ACC1", "NG*@RTSX", 5)
	if err != nil {
		t.Fatal(err)
	}
	first.Symbol = "caller mutation"
	for range 20 {
		got, err := c.GetMOEXFuture("ACC1", "NG*@RTSX", 5)
		if err != nil || got.Symbol != "NGX6@RTSX" || calls != 1 {
			t.Fatalf("cache hit = %+v, %v; calls %d", got, err, calls)
		}
	}
	if _, err := c.GetMOEXFuture("ACC2", "NG*@RTSX", 5); err != nil || calls != 1 {
		t.Fatalf("account did not reuse metadata: calls %d, err %v", calls, err)
	}
	if _, err := c.GetMOEXFuture("ACC1", "NG?6@RTSX", 5); err != nil || calls != 2 {
		t.Fatalf("mask cache isolation: calls %d, err %v", calls, err)
	}
	key := "NG*@RTSX"
	entry := c.futuresCache[key]
	entry.fetchedAt = time.Now().AddDate(0, 0, -1)
	c.futuresCache[key] = entry
	got, err := c.GetMOEXFuture("ACC1", "NG*@RTSX", 5)
	if err != nil || calls != 3 || !got.Expiration.Equal(date.AddDate(0, 0, 3)) {
		t.Fatalf("expired cache = %+v, %v; calls %d", got, err, calls)
	}
}

func TestGetMOEXFutureFailureKeepsActiveContract(t *testing.T) {
	date := time.Now().AddDate(0, 2, 0)
	fail := false
	calls := 0
	c := futuresTestClient(t, []*assets.Asset{
		{Symbol: "NGX6@RTSX", Type: "FUTURES"}, {Symbol: "NGZ6@RTSX", Type: "FUTURES"},
	}, func(_ context.Context, req *assets.GetAssetRequest, _ ...grpc.CallOption) (*assets.GetAssetResponse, error) {
		calls++
		if fail && req.Symbol == "NGX6@RTSX" {
			return nil, status.Error(codes.ResourceExhausted, "quota")
		}
		return futureMetadata(date), nil
	})
	want, err := c.GetMOEXFuture("ACC1", "NG*@RTSX", 5)
	if err != nil {
		t.Fatal(err)
	}
	key := "NG*@RTSX"
	entry := c.futuresCache[key]
	entry.fetchedAt = time.Now().AddDate(0, 0, -1)
	c.futuresCache[key] = entry
	fail = true
	got, err := c.GetMOEXFuture("ACC1", "NG*@RTSX", 5)
	if !IsRateLimited(err) || !reflect.DeepEqual(got, want) {
		t.Fatalf("failed refresh = %+v, %v; want old active and rate limit", got, err)
	}
	got, err = c.GetMOEXFuture("ACC2", "NG*@RTSX", 5)
	if !IsRateLimited(err) || !reflect.DeepEqual(got, want) {
		t.Fatalf("another account lost the shared active: %+v, %v", got, err)
	}
	fail = false
	before := calls
	got, err = c.GetMOEXFuture("ACC2", "NG*@RTSX", 5)
	if err != nil || got != want || calls != before+1 {
		t.Fatalf("failure poisoned cache: %+v, %v; calls %d", got, err, calls)
	}
}

func TestGetMOEXFutureMissingExpiryRejectsDiscovery(t *testing.T) {
	for _, bad := range []*assets.GetAssetResponse{
		nil, {},
		{AssetDetails: &assets.GetAssetResponse_FutureDetails_{}},
		{AssetDetails: &assets.GetAssetResponse_FutureDetails_{FutureDetails: &assets.GetAssetResponse_FutureDetails{}}},
		{AssetDetails: &assets.GetAssetResponse_FutureDetails_{FutureDetails: &assets.GetAssetResponse_FutureDetails{ExpirationDate: &timestamppb.Timestamp{Nanos: -1}}}},
	} {
		c := futuresTestClient(t, []*assets.Asset{{Symbol: "NGX6@RTSX", Type: "FUTURES"}, {Symbol: "NGZ6@RTSX", Type: "FUTURES"}}, func(_ context.Context, req *assets.GetAssetRequest, _ ...grpc.CallOption) (*assets.GetAssetResponse, error) {
			if req.Symbol == "NGZ6@RTSX" {
				return bad, nil
			}
			return futureMetadata(time.Now().AddDate(0, 1, 0)), nil
		})
		got, err := c.GetMOEXFuture("ACC1", "NG*@RTSX", 5)
		if err == nil || got != (models.FutureContract{}) {
			t.Fatalf("unknown expiry produced a guessed/shortened catalogue: %+v, %v", got, err)
		}
	}
}

func TestGetMOEXFutureRejectsUnsafeMasksAndMissingAccount(t *testing.T) {
	c := &Client{futuresCacheDir: t.TempDir()}
	for _, mask := range []string{"", "*@RTSX", "?G*@RTSX", "[NB]G*@RTSX", "NG*@*", "NG*@MISX", "NG*", "NG*@RTSX@RTSX", "NG[*@RTSX", "NG/*@RTSX", " NG*@RTSX"} {
		if _, err := c.GetMOEXFuture("ACC1", mask, 5); err == nil {
			t.Errorf("unsafe mask %q accepted", mask)
		}
	}
	if _, err := c.GetMOEXFuture("", "NG*@RTSX", 5); err == nil {
		t.Fatal("missing account accepted")
	}
	if _, err := c.GetMOEXFuture("ACC1", "NG*@RTSX", -1); err == nil {
		t.Fatal("negative rollover days accepted")
	}
}

func TestGetMOEXFutureEmptyCatalogueAndUnavailableCatalogue(t *testing.T) {
	c := futuresTestClient(t, nil, func(context.Context, *assets.GetAssetRequest, ...grpc.CallOption) (*assets.GetAssetResponse, error) {
		t.Fatal("empty catalogue made a metadata request")
		return nil, nil
	})
	for range 2 {
		got, err := c.GetMOEXFuture("ACC1", "NG*@RTSX", 5)
		if err != nil || got != (models.FutureContract{}) {
			t.Fatalf("empty catalogue = %+v, %v", got, err)
		}
	}
	if _, err := (&Client{futuresCacheDir: t.TempDir()}).GetMOEXFuture("ACC1", "NG*@RTSX", 5); err == nil {
		t.Fatal("failed startup catalogue treated as successful empty answer")
	}
}

func TestGetMOEXFutureCloseCancelsMetadata(t *testing.T) {
	started := make(chan struct{})
	c := futuresTestClient(t, []*assets.Asset{{Symbol: "NGX6@RTSX", Type: "FUTURES"}}, func(ctx context.Context, _ *assets.GetAssetRequest, _ ...grpc.CallOption) (*assets.GetAssetResponse, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	c.futuresContext, c.refreshCancel = context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := c.GetMOEXFuture("ACC1", "NG*@RTSX", 5)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("metadata request did not start")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Close cancellation = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel metadata")
	}
}

func TestGetMOEXFutureCatalogueRefreshFailurePreservesCandidates(t *testing.T) {
	calls := 0
	c := futuresTestClient(t, []*assets.Asset{{Symbol: "NGX6@RTSX", Type: "FUTURES"}}, func(context.Context, *assets.GetAssetRequest, ...grpc.CallOption) (*assets.GetAssetResponse, error) {
		calls++
		return futureMetadata(time.Now().AddDate(0, 2, 0)), nil
	})
	mock := c.assetsClient.(*mockAssetsServiceClient)
	for _, failure := range []error{status.Error(codes.Unavailable, "catalogue unavailable"), nil} {
		mock.AssetsFunc = func(context.Context, *assets.AssetsRequest, ...grpc.CallOption) (*assets.AssetsResponse, error) {
			return nil, failure
		}
		if err := c.loadAssetCache(); err == nil {
			t.Fatal("failed/nil catalogue response accepted")
		}
	}
	got, err := c.GetMOEXFuture("ACC1", "NG[WX]6@RTSX", 5)
	if err != nil || got.Symbol != "NGX6@RTSX" || calls != 1 {
		t.Fatalf("catalogue failure poisoned candidates: %+v, %v; calls %d", got, err, calls)
	}
}

func TestGetMOEXFutureConcurrentCallsSharePass(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	calls := 0
	c := futuresTestClient(t, []*assets.Asset{{Symbol: "NGX6@RTSX", Type: "FUTURES"}}, func(context.Context, *assets.GetAssetRequest, ...grpc.CallOption) (*assets.GetAssetResponse, error) {
		calls++
		if calls == 1 {
			close(started)
		}
		<-release
		return futureMetadata(time.Now().AddDate(0, 2, 0)), nil
	})
	type result struct {
		contract models.FutureContract
		err      error
	}
	done := make(chan result, 2)
	request := func(accountID string) {
		contract, err := c.GetMOEXFuture(accountID, "NG*@RTSX", 5)
		done <- result{contract: contract, err: err}
	}
	go request("ACC1")
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("metadata request did not start")
	}
	go request("ACC2")
	close(release)
	for range 2 {
		select {
		case got := <-done:
			if got.err != nil || got.contract.Symbol != "NGX6@RTSX" {
				t.Fatalf("concurrent metadata result = %+v, %v", got.contract, got.err)
			}
		case <-time.After(time.Second):
			t.Fatal("concurrent metadata request did not finish")
		}
	}
	if calls != 1 {
		t.Fatalf("concurrent calls made %d metadata requests, want 1", calls)
	}
}

func TestGetMOEXFutureRolloverDiscoversNewSeriesWithoutChangingSearch(t *testing.T) {
	oldAsset := &assets.Asset{Symbol: "NGX6@RTSX", Type: "FUTURES"}
	newAsset := &assets.Asset{Symbol: "NGZ6@RTSX", Type: "FUTURES"}
	metadataCalls := 0
	roll := false
	c := futuresTestClient(t, []*assets.Asset{oldAsset}, func(_ context.Context, req *assets.GetAssetRequest, _ ...grpc.CallOption) (*assets.GetAssetResponse, error) {
		metadataCalls++
		if roll && req.Symbol == oldAsset.Symbol {
			return futureMetadata(time.Now().AddDate(0, 0, 4)), nil
		}
		return futureMetadata(time.Now().AddDate(0, 2, 0)), nil
	})
	catalogueCalls := 0
	c.assetsClient.(*mockAssetsServiceClient).AssetsFunc = func(context.Context, *assets.AssetsRequest, ...grpc.CallOption) (*assets.AssetsResponse, error) {
		catalogueCalls++
		return &assets.AssetsResponse{Assets: []*assets.Asset{
			oldAsset, newAsset, {Symbol: "NGH7@RTSX", Type: "FUTURES", IsArchived: true},
		}}, nil
	}
	if _, err := c.GetMOEXFuture("ACC1", "NG*@RTSX", 5); err != nil {
		t.Fatal(err)
	}
	if catalogueCalls != 0 || metadataCalls != 1 {
		t.Fatalf("first visit did not reuse startup catalogue: catalogue %d, metadata %d", catalogueCalls, metadataCalls)
	}
	roll = true
	expireMOEXFuturesCache(c)
	got, err := c.GetMOEXFuture("ACC1", "NG*@RTSX", 5)
	if err != nil || got.Symbol != "NGZ6@RTSX" || catalogueCalls != 1 || metadataCalls != 3 {
		t.Fatalf("new series not discovered: %+v, %v; catalogue %d, metadata %d", got, err, catalogueCalls, metadataCalls)
	}
	if len(c.securityCache) != 1 || c.securityCache[0].Symbol != "NGX6@RTSX" {
		t.Fatal("dedicated futures refresh changed general search catalogue")
	}
	if _, err := c.GetMOEXFuture("ACC1", "NG[XYZ]*@RTSX", 5); err != nil || catalogueCalls != 1 {
		t.Fatalf("another family mask repeated bulk catalogue request: %d, %v", catalogueCalls, err)
	}
}

func TestGetMOEXFutureFailedRolloverCatalogueRefreshReturnsOldContractAndError(t *testing.T) {
	c := futuresTestClient(t, []*assets.Asset{{Symbol: "NGX6@RTSX", Type: "FUTURES"}}, func(context.Context, *assets.GetAssetRequest, ...grpc.CallOption) (*assets.GetAssetResponse, error) {
		return futureMetadata(time.Now().AddDate(0, 2, 0)), nil
	})
	want, err := c.GetMOEXFuture("ACC1", "NG*@RTSX", 5)
	if err != nil {
		t.Fatal(err)
	}
	expireMOEXFuturesCache(c)
	want.Expiration = time.Now().Add(-time.Second)
	entry := c.futuresCache["NG*@RTSX"]
	entry.contract = want
	c.futuresCache["NG*@RTSX"] = entry
	c.assetsClient.(*mockAssetsServiceClient).AssetsFunc = func(context.Context, *assets.AssetsRequest, ...grpc.CallOption) (*assets.AssetsResponse, error) {
		return &assets.AssetsResponse{}, status.Error(codes.ResourceExhausted, "catalogue quota")
	}
	got, err := c.GetMOEXFuture("ACC1", "NG*@RTSX", 5)
	if !IsRateLimited(err) || !reflect.DeepEqual(got, want) || len(c.futuresCandidates) != 1 {
		t.Fatalf("failed rollover lost old active: %+v, %v; candidates %+v", got, err, c.futuresCandidates)
	}
}

func TestGetMOEXFutureRetriesUnavailableStartupCatalogueOnEntry(t *testing.T) {
	calls := 0
	c := &Client{futuresCacheDir: t.TempDir(), assetsClient: &mockAssetsServiceClient{
		AssetsFunc: func(context.Context, *assets.AssetsRequest, ...grpc.CallOption) (*assets.AssetsResponse, error) {
			calls++
			if calls == 1 {
				return nil, status.Error(codes.Unavailable, "startup unavailable")
			}
			return &assets.AssetsResponse{}, nil
		},
	}}
	if err := c.loadAssetCache(); err == nil {
		t.Fatal("startup failure not returned")
	}
	for range 2 {
		got, err := c.GetMOEXFuture("ACC1", "NG*@RTSX", 5)
		if err != nil || got != (models.FutureContract{}) {
			t.Fatalf("entry did not recover unavailable startup catalogue: %+v, %v", got, err)
		}
	}
	if calls != 2 {
		t.Fatalf("startup/entry catalogue budget = %d; want 2", calls)
	}
}

func TestMOEXMetadataWarmsAssetLotWithoutReplacingTradeLot(t *testing.T) {
	assetCalls, paramsCalls := 0, 0
	symbol := "NGV6@RTSX"
	c := futuresTestClient(t, []*assets.Asset{{Symbol: symbol, Ticker: "NGV6", Mic: "RTSX", Type: "FUTURES"}}, func(context.Context, *assets.GetAssetRequest, ...grpc.CallOption) (*assets.GetAssetResponse, error) {
		assetCalls++
		resp := futureMetadata(time.Now().AddDate(0, 1, 0))
		resp.Ticker, resp.Mic, resp.QuoteCurrency = "NGV6", "RTSX", "USD"
		resp.LotSize = &decimal.Decimal{Value: "100"}
		return resp, nil
	})
	c.tradeLotCache = make(map[string]float64)
	c.assetsClient.(*mockAssetsServiceClient).GetAssetParamsFunc = func(context.Context, *assets.GetAssetParamsRequest, ...grpc.CallOption) (*assets.GetAssetParamsResponse, error) {
		paramsCalls++
		return &assets.GetAssetParamsResponse{TradeLotSize: 1}, nil
	}
	if _, err := c.GetMOEXFuture("ACC1", "NG*@RTSX", 5); err != nil {
		t.Fatal(err)
	}
	if got := c.EnsureLotSize("ACC1", symbol); got != 1 {
		t.Fatalf("order lot = %v; metadata's asset lot 100 must not replace the trade lot 1", got)
	}
	if assetCalls != 1 || paramsCalls != 1 {
		t.Fatalf("metadata and order cost %d GetAsset + %d GetAssetParams, want one each", assetCalls, paramsCalls)
	}
}
