package updater

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// releaseServer starts a test server that always answers with the latest
// release payload and reports how many times it was asked.
func releaseServer(t *testing.T) func() int {
	t.Helper()
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		_, _ = w.Write([]byte(latestReleaseJSON))
	}))
	t.Cleanup(srv.Close)
	withAPIBase(t, srv.URL)
	return func() int {
		mu.Lock()
		defer mu.Unlock()
		return calls
	}
}

// TestShouldCheck covers the once-a-day schedule, including the "never
// checked" case.
func TestShouldCheck(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		last time.Time
		want bool
	}{
		{name: "never checked", last: time.Time{}, want: true},
		{name: "checked just now", last: now, want: false},
		{name: "checked an hour ago", last: now.Add(-time.Hour), want: false},
		{name: "checked 23h59m ago", last: now.Add(-24*time.Hour + time.Minute), want: false},
		{name: "checked exactly 24h ago", last: now.Add(-24 * time.Hour), want: true},
		{name: "checked a week ago", last: now.Add(-7 * 24 * time.Hour), want: true},
		{name: "clock moved backwards", last: now.Add(time.Hour), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShouldCheck(State{Enabled: true, LastCheck: tt.last}, now); got != tt.want {
				t.Errorf("ShouldCheck(last=%v) = %v, want %v", tt.last, got, tt.want)
			}
		})
	}
	for _, last := range []time.Time{{}, now.Add(-7 * 24 * time.Hour)} {
		if ShouldCheck(State{Enabled: false, LastCheck: last}, now) {
			t.Errorf("disabled state with LastCheck %v is due for a check", last)
		}
	}
}

// TestRunSkipsNonRelease verifies a dev build performs no I/O whatsoever: no
// request, no state file, and an immediate return.
func TestRunSkipsNonRelease(t *testing.T) {
	dir := t.TempDir()
	withStateDir(t, dir)
	calls := releaseServer(t)

	for _, current := range []string{"dev", "dev (a1b2c3d)", ""} {
		called := false
		Run(context.Background(), current, func(string) { called = true })
		if called {
			t.Errorf("Run(%q) invoked the callback, want silence on a dev build", current)
		}
	}

	if got := calls(); got != 0 {
		t.Errorf("release endpoint called %d times, want 0 on a dev build", got)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Errorf("config dir contains %v (err %v), want no state file on a dev build", entries, err)
	}
}

// TestRunFreshStateSkipsRequest verifies a check performed less than a day ago
// is not repeated on the next launch.
func TestRunFreshStateSkipsRequest(t *testing.T) {
	withStateDir(t, t.TempDir())
	calls := releaseServer(t)

	if err := SaveState(State{Enabled: true, LastCheck: time.Now(), LatestVersion: "v0.13.0"}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(ctx, "v0.13.0", func(string) {})
	}()

	cancel()
	<-done

	if got := calls(); got != 0 {
		t.Errorf("release endpoint called %d times, want 0 with a fresh cache", got)
	}
}

// TestRunStaleStateChecksAndSaves verifies an expired cache triggers a
// request, persists the result and reports the new version exactly once.
func TestRunStaleStateChecksAndSaves(t *testing.T) {
	dir := t.TempDir()
	withStateDir(t, dir)

	calls := releaseServer(t)

	if err := SaveState(State{Enabled: true, LastCheck: time.Now().Add(-48 * time.Hour)}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	notified := make(chan string, 4)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(ctx, "v0.13.0", func(latest string) { notified <- latest })
	}()

	select {
	case got := <-notified:
		if got != "v0.14.0" {
			t.Errorf("callback got %q, want v0.14.0", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no new version reported with an expired cache")
	}
	cancel()
	<-done

	if got := calls(); got != 1 {
		t.Errorf("release endpoint called %d times, want exactly 1", got)
	}
	select {
	case extra := <-notified:
		t.Errorf("callback fired a second time with %q, want one notification per version", extra)
	default:
	}

	state, err := LoadState()
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}
	if state.LatestVersion != "v0.14.0" {
		t.Errorf("saved LatestVersion = %q, want v0.14.0", state.LatestVersion)
	}
	if state.ReleaseURL == "" {
		t.Error("saved ReleaseURL is empty, want the release page URL")
	}
	if state.LastCheck.IsZero() {
		t.Error("saved LastCheck is zero, want the time of the check")
	}
	if _, err := os.Stat(filepath.Join(dir, stateFileName)); err != nil {
		t.Errorf("state file missing after a successful check: %v", err)
	}
}

// TestCheckOnceNotifiesOncePerVersion verifies a repeated check of the same
// release does not fire the callback again.
func TestCheckOnceNotifiesOncePerVersion(t *testing.T) {
	withStateDir(t, t.TempDir())
	calls := releaseServer(t)

	var notified []string
	c := &checker{current: "v0.13.0", onNewVersion: func(latest string) {
		notified = append(notified, latest)
	}}

	if err := c.checkOnce(context.Background()); err != nil {
		t.Fatalf("first checkOnce() error = %v", err)
	}
	if err := c.checkOnce(context.Background()); err != nil {
		t.Fatalf("second checkOnce() error = %v", err)
	}

	if got := calls(); got != 2 {
		t.Errorf("release endpoint called %d times, want 2", got)
	}
	if len(notified) != 1 || notified[0] != "v0.14.0" {
		t.Errorf("callback invocations = %v, want exactly [v0.14.0]", notified)
	}
}

// TestCheckOnceIgnoresOlderRelease verifies a locally newer build is never
// asked to downgrade, while the check result is still cached.
func TestCheckOnceIgnoresOlderRelease(t *testing.T) {
	withStateDir(t, t.TempDir())
	releaseServer(t)

	called := false
	c := &checker{current: "v0.15.0", onNewVersion: func(string) { called = true }}
	if err := c.checkOnce(context.Background()); err != nil {
		t.Fatalf("checkOnce() error = %v", err)
	}

	if called {
		t.Error("callback fired for an older release, want no downgrade offer")
	}

	state, err := LoadState()
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}
	if state.LatestVersion != "v0.14.0" {
		t.Errorf("saved LatestVersion = %q, want the check result cached anyway", state.LatestVersion)
	}
}

// TestCheckOnceNetworkFailureKeepsState verifies a failed check leaves the
// cache untouched, so the next attempt happens on the normal schedule.
func TestCheckOnceNetworkFailureKeepsState(t *testing.T) {
	withStateDir(t, t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"API rate limit exceeded"}`, http.StatusForbidden)
	}))
	defer srv.Close()
	withAPIBase(t, srv.URL)

	seed := State{Enabled: true, LastCheck: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), LatestVersion: "v0.13.0"}
	if err := SaveState(seed); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	called := false
	c := &checker{current: "v0.13.0", onNewVersion: func(string) { called = true }}
	if err := c.checkOnce(context.Background()); err == nil {
		t.Error("checkOnce() error = nil, want the rate-limit error")
	}
	if called {
		t.Error("callback fired after a failed check")
	}

	state, err := LoadState()
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}
	if !state.LastCheck.Equal(seed.LastCheck) || state.LatestVersion != seed.LatestVersion {
		t.Errorf("state = %+v, want it untouched (%+v) after a failed check", state, seed)
	}
}

// TestRunStopsOnContextCancel verifies the background loop exits promptly when
// the application shuts down.
func TestRunStopsOnContextCancel(t *testing.T) {
	withStateDir(t, t.TempDir())
	releaseServer(t)

	if err := SaveState(State{Enabled: true, LastCheck: time.Now()}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(ctx, "v0.13.0", func(string) {})
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the context was cancelled")
	}
}

func TestDisabledUpdatesSkipRequestsAndKeepCache(t *testing.T) {
	for _, direct := range []bool{false, true} {
		name := "Run"
		if direct {
			name = "checkOnce"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			withStateDir(t, dir)
			calls := releaseServer(t)
			seed := State{
				Enabled:       false,
				LastCheck:     time.Now().Add(-48 * time.Hour),
				LatestVersion: "v0.14.0",
			}
			if err := SaveState(seed); err != nil {
				t.Fatalf("seed state: %v", err)
			}
			before, err := os.ReadFile(filepath.Join(dir, stateFileName))
			if err != nil {
				t.Fatal(err)
			}
			called := false
			if direct {
				c := &checker{current: "v0.13.0", onNewVersion: func(string) { called = true }}
				if err := c.checkOnce(context.Background()); err != nil {
					t.Fatalf("checkOnce() error = %v", err)
				}
			} else {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				Run(ctx, "v0.13.0", func(string) { called = true })
				if ctx.Err() != nil {
					t.Fatal("disabled Run did not return immediately")
				}
			}
			if got := calls(); got != 0 || called {
				t.Errorf("disabled updates made %d requests and callback=%v, want no requests or notification", got, called)
			}
			after, err := os.ReadFile(filepath.Join(dir, stateFileName))
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) {
				t.Error("disabled updates rewrote the cache")
			}
		})
	}
}

func TestCheckOnceDisabledDuringRequestKeepsSetting(t *testing.T) {
	dir := t.TempDir()
	withStateDir(t, dir)
	started := make(chan struct{})
	finish := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(finish) }) }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-finish
		_, _ = w.Write([]byte(latestReleaseJSON))
	}))
	defer srv.Close()
	defer release()
	withAPIBase(t, srv.URL)

	notified := make(chan string, 1)
	c := &checker{current: "v0.13.0", onNewVersion: func(latest string) { notified <- latest }}
	done := make(chan error, 1)
	go func() { done <- c.checkOnce(context.Background()) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("release request did not start")
	}
	if err := SaveState(State{Enabled: false, LatestVersion: "v0.12.0"}); err != nil {
		t.Fatalf("disable updates: %v", err)
	}
	before, err := os.ReadFile(filepath.Join(dir, stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("checkOnce() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("checkOnce did not finish")
	}
	select {
	case got := <-notified:
		t.Errorf("disabled updates notified about %q", got)
	default:
	}
	after, err := os.ReadFile(filepath.Join(dir, stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Error("completed check overwrote the user's disabled setting or cache")
	}
}

func TestRunChecksWithDefaultAndLegacySettings(t *testing.T) {
	for _, tt := range []struct {
		name string
		json string
	}{
		{name: "missing file"},
		{name: "legacy cache", json: `{"latest_version":"v0.12.0"}`},
		{name: "explicit enabled", json: `{"enabled":true,"latest_version":"v0.12.0"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			withStateDir(t, dir)
			if tt.json != "" {
				if err := os.WriteFile(filepath.Join(dir, stateFileName), []byte(tt.json), 0644); err != nil {
					t.Fatal(err)
				}
			}
			calls := releaseServer(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			latest := ""
			Run(ctx, "v0.13.0", func(tag string) {
				latest = tag
				cancel()
			})
			if got := calls(); got != 1 || latest != "v0.14.0" {
				t.Errorf("Run made %d requests, notified %q; want one request and v0.14.0", got, latest)
			}
			state, err := LoadState()
			if err != nil {
				t.Fatal(err)
			}
			if !state.Enabled || state.LatestVersion != "v0.14.0" || state.LastCheck.IsZero() {
				t.Errorf("successful check saved %+v, want enabled updates with the new release", state)
			}
		})
	}
}
