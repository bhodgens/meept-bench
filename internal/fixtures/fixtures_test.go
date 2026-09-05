package fixtures

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHashURLMatchesMeeptConvention(t *testing.T) {
	// sha1("https://example.com"), verified independently via
	// `printf 'https://example.com' | shasum` — this pins the shared
	// naming convention with meept's ~/.meept/tool-cache lookups.
	const want = "327c3fda87ce286848a574982ddd0b7c7487f816"
	if got := HashURL("https://example.com"); got != want {
		t.Errorf("HashURL = %q, want %q", got, want)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	url := "https://example.com/page"
	fetched := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

	path, err := s.Save(url, "page body", fetched)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if filepath.Base(path) != HashURL(url)+".json" {
		t.Errorf("fixture name = %q, want %q", filepath.Base(path), HashURL(url)+".json")
	}

	e, found, err := s.Load(url)
	if err != nil || !found {
		t.Fatalf("Load: found=%v err=%v", found, err)
	}
	if e.URL != url || e.Body != "page body" || !e.FetchedAt.Equal(fetched) {
		t.Errorf("round-trip mismatch: %+v", e)
	}

	has, err := s.Has(url)
	if err != nil || !has {
		t.Errorf("Has = %v err=%v, want true nil", has, err)
	}
}

func TestLoadMiss(t *testing.T) {
	s := New(t.TempDir())
	if _, found, err := s.Load("https://never.example"); found || err != nil {
		t.Errorf("empty store: found=%v err=%v, want false nil", found, err)
	}
	has, err := s.Has("https://never.example")
	if has || err != nil {
		t.Errorf("Has on empty store = %v err=%v, want false nil", has, err)
	}
}

func TestLoadURLMismatchIsMiss(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	if _, err := s.Save("https://real.example", "x", time.Now()); err != nil {
		t.Fatal(err)
	}
	_, found, err := s.Load("https://other.example")
	if found || err != nil {
		t.Errorf("URL mismatch: found=%v err=%v, want false nil", found, err)
	}
}

func TestLoadCorruptIsError(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	url := "https://corrupt.example"
	if _, err := s.Save(url, "x", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(EntryPath(dir, url), []byte("not json at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Load(url); err == nil {
		t.Error("corrupt fixture: want error, got nil")
	}
}

func TestSaveRequiresURL(t *testing.T) {
	s := New(t.TempDir())
	if _, err := s.Save("", "body", time.Now()); err == nil {
		t.Error("Save with empty URL: want error, got nil")
	}
}

func TestSaveCreatesNestedDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fixtures", "nested")
	s := New(dir)
	if _, err := s.Save("https://deep.example", "body", time.Now()); err != nil {
		t.Fatalf("Save into missing dirs: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("nested dir not created/populated")
	}
}

func TestEntryJSONShape(t *testing.T) {
	// The fixture JSON must carry exactly the shared keys url, fetched_at,
	// body — meept's cache lookup unmarshals the same shape.
	fetched := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	data, err := json.Marshal(Entry{URL: "u", FetchedAt: fetched, Body: "b"})
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, key := range []string{`"url":"u"`, `"fetched_at":`, `"body":"b"`} {
		if !strings.Contains(got, key) {
			t.Errorf("entry JSON %s missing %s", got, key)
		}
	}
}
