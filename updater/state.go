package updater

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"finam-terminal/config"
)

// stateFileName is the name of the update check cache inside the user config
// directory (~/.finam-cli/update.json), next to the token .env.
const stateFileName = "update.json"

// stateDirFunc resolves the directory holding the update cache. It is a
// package-level variable so tests can redirect it at a temporary directory
// without touching the real home directory.
var stateDirFunc = config.UserConfigDir

// State holds the update setting and the cached result of the last check.
// A missing, empty or damaged file enables updates with no known release, so
// existing installations keep their previous behaviour.
type State struct {
	// Enabled controls automatic checks, notifications and in-app updates.
	// LoadState defaults it to true when the file or key is absent.
	Enabled bool `json:"enabled"`
	// LastCheck is when the GitHub API was last queried successfully.
	LastCheck time.Time `json:"last_check"`
	// LatestVersion is the tag of the newest published release, e.g. "v0.14.0".
	LatestVersion string `json:"latest_version"`
	// ReleaseURL is the human-readable release page for LatestVersion.
	ReleaseURL string `json:"release_url"`
	// PublishedAt is when LatestVersion was published.
	PublishedAt time.Time `json:"published_at"`
}

// LoadState reads the update cache from ~/.finam-cli/update.json.
//
// A missing, empty or unparsable file yields Enabled=true with an empty cache
// and a nil error. A [WARN] line is logged for a damaged file so the situation
// is diagnosable. An error is returned only when the config directory itself
// cannot be resolved.
func LoadState() (State, error) {
	dir, err := stateDirFunc()
	if err != nil {
		return State{}, fmt.Errorf("resolve config dir: %w", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, stateFileName))
	if err != nil {
		// A missing cache is the normal first-run case, not a problem.
		if !os.IsNotExist(err) {
			log.Printf("[WARN] Update state unreadable: %v", err)
		}
		return State{Enabled: true}, nil
	}
	if len(data) == 0 {
		return State{Enabled: true}, nil
	}

	state := State{Enabled: true}
	if err := json.Unmarshal(data, &state); err != nil {
		log.Printf("[WARN] Update state corrupted, ignoring: %v", err)
		return State{Enabled: true}, nil
	}
	return state, nil
}

// UpdatesEnabled reads the user's update setting. If the config directory
// cannot be resolved, updates stay off just as the background checker does.
func UpdatesEnabled() bool {
	state, err := LoadState()
	return err == nil && state.Enabled
}

// SaveState writes the update cache atomically: the JSON is written to a
// temporary file in the same directory and then renamed over the target, so a
// crash mid-write can never leave a half-written cache behind.
//
// The config directory is created (0755) when missing; the file is 0644.
func SaveState(state State) error {
	dir, err := stateDirFunc()
	if err != nil {
		return fmt.Errorf("resolve config dir: %w", err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode update state: %w", err)
	}
	data = append(data, '\n')

	target := filepath.Join(dir, stateFileName)
	tmp, err := os.CreateTemp(dir, stateFileName+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp state file: %w", err)
	}
	tmpName := tmp.Name()
	// Remove the temp file on every failure path; a successful rename makes
	// this a harmless no-op.
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp state file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp state file: %w", err)
	}
	if err := os.Chmod(tmpName, 0644); err != nil {
		return fmt.Errorf("chmod temp state file: %w", err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("replace state file: %w", err)
	}
	return nil
}
