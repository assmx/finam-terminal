package updater

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// withStateDir points the state file at a temporary directory for the
// duration of a test and restores the production resolver afterwards.
func withStateDir(t *testing.T, dir string) {
	t.Helper()
	prev := stateDirFunc
	stateDirFunc = func() (string, error) { return dir, nil }
	t.Cleanup(func() { stateDirFunc = prev })
}

// TestLoadStateMissingFile verifies a missing cache is not an error: the very
// first run must behave as "never checked" without disturbing startup.
func TestLoadStateMissingFile(t *testing.T) {
	withStateDir(t, t.TempDir())

	state, err := LoadState()
	if err != nil {
		t.Fatalf("LoadState() error = %v, want nil", err)
	}
	if !state.Enabled || !state.LastCheck.IsZero() || state.LatestVersion != "" || state.ReleaseURL != "" {
		t.Errorf("LoadState() = %+v, want enabled updates with an empty cache", state)
	}
}

// TestLoadStateCorruptJSON verifies a damaged cache degrades to "never
// checked" instead of failing the launch.
func TestLoadStateCorruptJSON(t *testing.T) {
	dir := t.TempDir()
	withStateDir(t, dir)

	if err := os.WriteFile(filepath.Join(dir, stateFileName), []byte("{not json"), 0644); err != nil {
		t.Fatalf("seed corrupt state: %v", err)
	}

	state, err := LoadState()
	if err != nil {
		t.Fatalf("LoadState() error = %v, want nil", err)
	}
	if !state.Enabled || !state.LastCheck.IsZero() || state.LatestVersion != "" {
		t.Errorf("LoadState() = %+v, want enabled updates with an empty cache", state)
	}
}

// TestLoadStateEmptyFile verifies a zero-length file is treated like a
// missing one (a crash mid-write must not wedge the checker).
func TestLoadStateEmptyFile(t *testing.T) {
	dir := t.TempDir()
	withStateDir(t, dir)

	if err := os.WriteFile(filepath.Join(dir, stateFileName), nil, 0644); err != nil {
		t.Fatalf("seed empty state: %v", err)
	}

	state, err := LoadState()
	if err != nil {
		t.Fatalf("LoadState() error = %v, want nil", err)
	}
	if !state.Enabled || !state.LastCheck.IsZero() {
		t.Errorf("LoadState() = %+v, want enabled updates with an empty cache", state)
	}
}

// TestSaveLoadRoundTrip verifies every field survives a write/read cycle.
func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	withStateDir(t, dir)

	want := State{
		Enabled:       true,
		LastCheck:     time.Date(2026, 8, 25, 9, 12, 33, 0, time.UTC),
		LatestVersion: "v0.14.0",
		ReleaseURL:    "https://github.com/updevru/finam-terminal/releases/tag/v0.14.0",
		PublishedAt:   time.Date(2026, 8, 25, 8, 0, 0, 0, time.UTC),
	}

	if err := SaveState(want); err != nil {
		t.Fatalf("SaveState() error = %v", err)
	}

	got, err := LoadState()
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}
	if got.Enabled != want.Enabled {
		t.Errorf("Enabled = %v, want %v", got.Enabled, want.Enabled)
	}
	if !got.LastCheck.Equal(want.LastCheck) {
		t.Errorf("LastCheck = %v, want %v", got.LastCheck, want.LastCheck)
	}
	if !got.PublishedAt.Equal(want.PublishedAt) {
		t.Errorf("PublishedAt = %v, want %v", got.PublishedAt, want.PublishedAt)
	}
	if got.LatestVersion != want.LatestVersion {
		t.Errorf("LatestVersion = %q, want %q", got.LatestVersion, want.LatestVersion)
	}
	if got.ReleaseURL != want.ReleaseURL {
		t.Errorf("ReleaseURL = %q, want %q", got.ReleaseURL, want.ReleaseURL)
	}
}

// TestSaveStateIsAtomic verifies the temp+rename write leaves no leftovers
// and that a second save overwrites cleanly.
func TestSaveStateIsAtomic(t *testing.T) {
	dir := t.TempDir()
	withStateDir(t, dir)

	if err := SaveState(State{Enabled: true, LatestVersion: "v0.14.0", LastCheck: time.Now()}); err != nil {
		t.Fatalf("SaveState() error = %v", err)
	}
	if err := SaveState(State{Enabled: true, LatestVersion: "v0.15.0", LastCheck: time.Now()}); err != nil {
		t.Fatalf("second SaveState() error = %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temporary file %q left behind after SaveState", e.Name())
		}
	}
	if len(entries) != 1 {
		t.Errorf("directory contains %d entries, want exactly 1 (%s)", len(entries), stateFileName)
	}

	got, err := LoadState()
	if err != nil {
		t.Fatalf("LoadState() error = %v", err)
	}
	if got.LatestVersion != "v0.15.0" {
		t.Errorf("LatestVersion = %q, want %q", got.LatestVersion, "v0.15.0")
	}
}

// TestSaveStateCreatesDirectory verifies the config directory is created on
// demand — the updater may run before any token was ever saved.
func TestSaveStateCreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", ".finam-cli")
	withStateDir(t, dir)

	if err := SaveState(State{Enabled: true, LatestVersion: "v0.14.0"}); err != nil {
		t.Fatalf("SaveState() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, stateFileName)); err != nil {
		t.Fatalf("state file not created: %v", err)
	}
}

// TestStateDirUnavailable verifies both operations fail softly when the home
// directory cannot be resolved: no panic, LoadState still yields zero state.
func TestStateDirUnavailable(t *testing.T) {
	prev := stateDirFunc
	stateDirFunc = func() (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { stateDirFunc = prev })

	state, err := LoadState()
	if err == nil {
		t.Error("LoadState() error = nil, want an error when the config dir is unavailable")
	}
	if !state.LastCheck.IsZero() {
		t.Errorf("LoadState() = %+v, want zero state", state)
	}
	if err := SaveState(State{Enabled: true, LatestVersion: "v0.14.0"}); err == nil {
		t.Error("SaveState() error = nil, want an error when the config dir is unavailable")
	}
}

func TestStateEnabledCompatibility(t *testing.T) {
	for _, tt := range []struct {
		name    string
		json    string
		enabled bool
	}{
		{name: "legacy cache", json: `{"latest_version":"v0.14.0"}`, enabled: true},
		{name: "enabled cache", json: `{"enabled":true,"latest_version":"v0.14.0"}`, enabled: true},
		{name: "disabled cache", json: `{"enabled":false,"latest_version":"v0.14.0"}`, enabled: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			withStateDir(t, dir)
			if err := os.WriteFile(filepath.Join(dir, stateFileName), []byte(tt.json), 0644); err != nil {
				t.Fatal(err)
			}
			state, err := LoadState()
			if err != nil {
				t.Fatal(err)
			}
			if got := ShouldCheck(state, time.Now()); got != tt.enabled {
				t.Errorf("loaded state due for check = %v, want %v", got, tt.enabled)
			}
			if got := UpdatesEnabled(); got != tt.enabled {
				t.Errorf("UpdatesEnabled() = %v, want %v", got, tt.enabled)
			}
			if state.LatestVersion != "v0.14.0" {
				t.Errorf("latest version = %q, want existing cache preserved", state.LatestVersion)
			}
			if err := SaveState(state); err != nil {
				t.Fatal(err)
			}
			saved, err := LoadState()
			if err != nil {
				t.Fatal(err)
			}
			if saved.Enabled != tt.enabled || saved.LatestVersion != state.LatestVersion {
				t.Errorf("round trip changed state: %+v", saved)
			}
		})
	}
}
