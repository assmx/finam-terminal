package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"finam-terminal/version"
)

// TestPendingUpdateSkipsDevBuild verifies a development build never even looks
// at the update cache: no dialog, no file, no network. This is the guarantee
// that `go run main.go` and the Docker image behave exactly as they did before
// the updater existed.
func TestPendingUpdateSkipsDevBuild(t *testing.T) {
	for _, v := range []string{"dev", "dev (a1b2c3d)", "dev (a1b2c3d, dirty)", ""} {
		t.Run(v, func(t *testing.T) {
			prev := version.Version
			version.Version = v
			t.Cleanup(func() { version.Version = prev })

			if got := pendingUpdate(); got != "" {
				t.Errorf("pendingUpdate() = %q for version %q, want empty on a dev build", got, v)
			}
		})
	}
}

// TestPendingUpdateSkipsWhenCurrentIsNewer verifies a release build that is
// ahead of whatever the cache holds is never offered a downgrade.
func TestPendingUpdateSkipsWhenCurrentIsNewer(t *testing.T) {
	setUpdateState(t, `{"latest_version":"v0.14.0"}`)
	prev := version.Version
	version.Version = "v999.0.0"
	t.Cleanup(func() { version.Version = prev })

	if got := pendingUpdate(); got != "" {
		t.Errorf("pendingUpdate() = %q while running a version newer than any release, want empty", got)
	}
}

// TestOfferPendingUpdateIgnoresEmptyVersion verifies no dialog is raised when
// there is nothing to offer — the empty version must short-circuit before any
// tview application is created.
func TestOfferPendingUpdateIgnoresEmptyVersion(t *testing.T) {
	if offerPendingUpdate("") {
		t.Error("offerPendingUpdate(\"\") = true, want false with nothing to install")
	}
}

func setUpdateState(t *testing.T, data string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".finam-cli")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "update.json"), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestPendingUpdateRespectsEnabled(t *testing.T) {
	for _, tt := range []struct {
		name string
		json string
		want string
	}{
		{name: "legacy cache", json: `{"latest_version":"v0.14.0"}`, want: "v0.14.0"},
		{name: "enabled cache", json: `{"enabled":true,"latest_version":"v0.14.0"}`, want: "v0.14.0"},
		{name: "disabled cache", json: `{"enabled":false,"latest_version":"v0.14.0"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			setUpdateState(t, tt.json)
			prev := version.Version
			version.Version = "v0.13.0"
			t.Cleanup(func() { version.Version = prev })
			if got := pendingUpdate(); got != tt.want {
				t.Errorf("pendingUpdate() = %q, want %q", got, tt.want)
			}
		})
	}
}

type updateRequestFunc func(*http.Request) (*http.Response, error)

func (f updateRequestFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestInstallUpdateDisabledDoesNotFetch(t *testing.T) {
	setUpdateState(t, `{"enabled":false,"latest_version":"v0.14.0"}`)
	calls := 0
	prev := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: updateRequestFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, fmt.Errorf("unexpected update request")
	})}
	t.Cleanup(func() { http.DefaultClient = prev })
	if offerPendingUpdate("v0.14.0") {
		t.Error("disabled update offered a startup installation")
	}
	if installUpdate() {
		t.Error("disabled update requested a restart")
	}
	if calls != 0 {
		t.Errorf("disabled update made %d HTTP requests, want none", calls)
	}
}
