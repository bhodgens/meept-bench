// Package fixtures implements meept-bench's captured-fetch store: the
// fixture files `capture` writes and deterministic suites consume. The
// on-disk format is shared byte-for-byte with meept's tool-cache
// (internal/tools/builtin deterministic_cache.go):
//
//	<dir>/<sha1(url)>.json  containing  {"url","fetched_at","body"}
package fixtures

import (
	"crypto/sha1" //nolint:gosec // non-security use: cache address derived from URL, matches meept's shared format
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Entry is one captured fetch.
type Entry struct {
	URL       string    `json:"url"`
	FetchedAt time.Time `json:"fetched_at"`
	Body      string    `json:"body"`
}

// HashURL returns the hex sha1 of url — the fixture file stem shared
// with meept's cached-fetch lookup.
func HashURL(url string) string {
	sum := sha1.Sum([]byte(url)) //nolint:gosec // see package doc
	return hex.EncodeToString(sum[:])
}

// EntryPath returns the fixture path for url under dir.
func EntryPath(dir, url string) string {
	return filepath.Join(dir, HashURL(url)+".json")
}

// Store is a directory of captured fixtures.
type Store struct{ Dir string }

// New creates a Store rooted at dir (not created until Save).
func New(dir string) *Store { return &Store{Dir: dir} }

// Save writes entry under the store directory, creating the directory as
// needed, and returns the written path.
func (s *Store) Save(url, body string, fetchedAt time.Time) (string, error) {
	if url == "" {
		return "", errors.New("fixtures: url is required")
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(Entry{URL: url, FetchedAt: fetchedAt.UTC(), Body: body}, "", "  ")
	if err != nil {
		return "", err
	}
	path := EntryPath(s.Dir, url)
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// Load reads the fixture for url. found is false when no fixture exists.
// A present-but-corrupt fixture is an error; a URL mismatch is a miss
// (the stored URL is verified so a hash collision cannot serve another
// page).
func (s *Store) Load(url string) (Entry, bool, error) {
	data, err := os.ReadFile(EntryPath(s.Dir, url))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Entry{}, false, nil
		}
		return Entry{}, false, fmt.Errorf("fixtures: read %s: %w", s.Dir, err)
	}
	var e Entry
	if err := json.Unmarshal(data, &e); err != nil {
		return Entry{}, false, fmt.Errorf("fixtures: corrupt %s: %w", s.Dir, err)
	}
	if e.URL != url {
		return Entry{}, false, nil
	}
	return e, true, nil
}

// Has reports whether url has a fixture.
func (s *Store) Has(url string) (bool, error) {
	_, found, err := s.Load(url)
	return found, err
}
