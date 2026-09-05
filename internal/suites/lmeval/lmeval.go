// Package lmeval adapts the HuggingFace LongMemEval dataset into meept-bench
// suite manifests.
//
// Licensing: LongMemEval is MIT-licensed (verified 2026-09-01 against HF
// revision 2ec2a557f339b6c0369619b1ed5793734cc87533; evidence recorded in
// docs/LONGMEMEVAL.md). Per docs/BENCHMARKS.md the dataset content is never
// committed: Fetch reads from HuggingFace at run time and Emit writes
// gitignored artifacts only. Every emitted task carries the dataset revision
// in its tags ("lmeval-rev:<hash>") and the manifest carries it in
// hf_revision so result rows stay reproducible.
package lmeval

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strings"
)

const (
	DefaultAPIBase  = "https://huggingface.co"
	DefaultRowsBase = "https://datasets-server.huggingface.co"
	// DefaultDatasetID is the canonical (lowercase) LongMemEval dataset id.
	DefaultDatasetID = "xiaowu0162/longmemeval"
)

// Config configures the LongMemEval adapter. It doubles as the CLI config
// file schema (JSON keys in docs/LONGMEMEVAL.md).
type Config struct {
	DatasetID string `json:"dataset_id"`         // e.g. "xiaowu0162/longmemeval"
	Revision  string `json:"revision,omitempty"` // pin for reproducibility; empty = resolve latest at fetch time
	Split     string `json:"split,omitempty"`    // "S" (default), "M", "oracle"
	Limit     int    `json:"limit,omitempty"`    // 0 = all items; template uses 5
	Mode      string `json:"mode,omitempty"`     // "context" (default) | "memory"
	Seed      int64  `json:"seed,omitempty"`     // deterministic item selection; 0 = dataset order
	Method    string `json:"method,omitempty"`   // "download" (default) | "rows"
	// ConfigName is the datasets-server config used by Method "rows"
	// (default "default"). Not used by "download".
	ConfigName string `json:"config_name,omitempty"`
	// APIBase/RowsBase override the service endpoints (tests only; "-" keeps
	// them out of the config-file schema).
	APIBase  string `json:"-"`
	RowsBase string `json:"-"`
}

// Item is one LongMemEval question. Field names follow the dataset's JSON
// schema (docs/LONGMEMEVAL.md); haystack rendering is defensive because the
// turn objects vary between dataset revisions.
type Item struct {
	QuestionID       string   `json:"question_id"`
	Question         string   `json:"question"`
	Answer           string   `json:"answer"`
	HaystackSessions []any    `json:"haystack_sessions,omitempty"`
	SessionIDs       []string `json:"haystack_session_ids,omitempty"`
	AnswerSessionIDs []string `json:"answer_session_ids,omitempty"`
}

// UnmarshalJSON accepts the dataset's type drift: some revisions encode
// `answer` as a number (e.g. "5" vs 5) or other scalars. Coerce anything
// JSON-parseable into its string form; only true structural garbage fails.
func (it *Item) UnmarshalJSON(data []byte) error {
	type itemAlias Item // dodge recursive UnmarshalJSON
	var raw struct {
		itemAlias
		Answer json.RawMessage `json:"answer"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*it = Item(raw.itemAlias)
	if len(raw.Answer) > 0 {
		var s string
		if err := json.Unmarshal(raw.Answer, &s); err == nil {
			it.Answer = s
			return nil
		}
		it.Answer = string(raw.Answer) // numbers/bools: use literal form
	}
	return nil
}

// Fetch downloads the split from HuggingFace and returns the selected items.
// No SDK dependency: Method "download" streams the split JSON array over the
// resolve endpoint (early-closing the body once Limit items are read, so a
// 5-item fetch does not pull the whole 278MB file); Method "rows" pages the
// HF datasets-server /rows API. Auth uses HF_TOKEN from the environment when
// set (public datasets work unauthenticated).
func (c Config) Fetch(ctx context.Context, hc *http.Client) ([]Item, error) {
	if hc == nil {
		hc = http.DefaultClient
	}
	if strings.TrimSpace(c.DatasetID) == "" {
		return nil, fmt.Errorf("lmeval: dataset_id is required")
	}
	if m := c.method(); m != "download" && m != "rows" {
		return nil, fmt.Errorf("lmeval: unknown method %q (want \"download\" or \"rows\")", m)
	}
	if m := c.emitMode(); m != "context" && m != "memory" {
		return nil, fmt.Errorf("lmeval: unknown mode %q (want \"context\" or \"memory\")", m)
	}
	// Validate the split name BEFORE any network call so bad configs fail
	// fast and locally.
	if _, err := c.splitFile(); err != nil {
		return nil, err
	}
	rev := strings.TrimSpace(c.Revision)
	if rev == "" {
		var err error
		if rev, err = c.resolveRevision(ctx, hc); err != nil {
			return nil, err
		}
	}
	if c.method() == "rows" {
		return c.fetchRows(ctx, hc, rev)
	}
	return c.fetchDownload(ctx, hc, rev)
}

// fetchDownload streams the split file (a top-level JSON array) and decodes
// items incrementally with bounded memory.
func (c Config) fetchDownload(ctx context.Context, hc *http.Client, rev string) ([]Item, error) {
	file, err := c.splitFile()
	if err != nil {
		return nil, err
	}
	tree, err := c.fetchTree(ctx, hc, rev)
	if err != nil {
		return nil, err
	}
	found := false
	for _, e := range tree {
		if e.Type == "file" && e.Path == file {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("lmeval: file %q not found in %s@%s (%d tree entries)", file, c.DatasetID, rev, len(tree))
	}

	u := fmt.Sprintf("%s/datasets/%s/resolve/%s/%s", c.apiBase(), c.DatasetID, url.PathEscape(rev), pathURL(file))
	req, err := c.request(ctx, http.MethodGet, u, false)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lmeval: fetch %s: %w", u, err)
	}
	defer func() {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("lmeval: fetch %s: HTTP %d", u, resp.StatusCode)
	}

	dec := json.NewDecoder(resp.Body)
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("lmeval: %s: not JSON: %w", file, err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		return nil, fmt.Errorf("lmeval: %s: expected a JSON array, got %v", file, tok)
	}

	useReservoir := c.Seed != 0
	var items []Item
	var reservoir []Item
	rng := rand.New(rand.NewSource(c.Seed)) //nolint:gosemble // deterministic selection, not security
	n := 0
	for dec.More() {
		var it Item
		if err := dec.Decode(&it); err != nil {
			return nil, fmt.Errorf("lmeval: decode %s item %d: %w", file, n, err)
		}
		n++
		if !useReservoir {
			items = append(items, it)
			if c.Limit > 0 && len(items) >= c.Limit {
				// Early exit: closing the body here abandons the remainder
				// of the (potentially very large) download.
				resp.Body.Close()
				return items, nil
			}
			continue
		}
		// Algorithm R reservoir sampling: deterministic for a pinned
		// revision + seed, but scans the whole split.
		if c.Limit <= 0 || len(reservoir) < c.Limit {
			reservoir = append(reservoir, it)
		} else {
			j := rng.Intn(n)
			if j < c.Limit {
				reservoir[j] = it
			}
		}
	}
	if useReservoir {
		items = reservoir
	}
	if c.Limit > 0 && len(items) > c.Limit {
		items = items[:c.Limit]
	}
	return items, nil
}

// rowsPage is one datasets-server /rows page.
type rowsPage struct {
	NumRows int `json:"num_rows"`
	Size    int `json:"size"`
	Rows    []struct {
		RowIdx int  `json:"row_idx"`
		Row    Item `json:"row"`
	} `json:"rows"`
}

// fetchRows pages the datasets-server /rows API (10..100 rows per page
// depending on row width; the loop honors whatever the server returns).
func (c Config) fetchRows(ctx context.Context, hc *http.Client, rev string) ([]Item, error) {
	split, err := c.splitFile()
	if err != nil {
		return nil, err
	}
	splitName := strings.TrimPrefix(split, "longmemeval_")
	cfgName := c.ConfigName
	if cfgName == "" {
		cfgName = "default"
	}
	base := c.rowsBase()
	var items []Item
	const maxPages = 10000
	for offset, page := 0, 0; page < maxPages; page++ {
		u := fmt.Sprintf("%s/rows?dataset=%s&config=%s&split=%s&revision=%s&offset=%d&length=%d",
			base, url.QueryEscape(c.DatasetID), url.QueryEscape(cfgName),
			url.QueryEscape(splitName), url.QueryEscape(rev), offset, pageSize(c.Limit, len(items)))
		req, err := c.request(ctx, http.MethodGet, u, true)
		if err != nil {
			return nil, err
		}
		resp, err := hc.Do(req)
		if err != nil {
			return nil, fmt.Errorf("lmeval: rows page at offset %d: %w", offset, err)
		}
		var p rowsPage
		err = json.NewDecoder(resp.Body).Decode(&p)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("lmeval: rows page at offset %d: HTTP %d: %w", offset, resp.StatusCode, err)
		}
		if p.NumRows == 0 && len(p.Rows) == 0 {
			break
		}
		for _, r := range p.Rows {
			items = append(items, r.Row)
			if c.Limit > 0 && len(items) >= c.Limit {
				return items, nil
			}
		}
		if p.NumRows <= 0 {
			break
		}
		offset += p.NumRows
	}
	return items, nil
}

func pageSize(limit, have int) int {
	const max = 100 // datasets-server hard cap per page
	if limit > 0 {
		want := limit - have
		if want < max {
			if want < 1 {
				want = 1
			}
			return want
		}
	}
	return max
}

type treeEntry struct {
	Type string `json:"type"`
	Path string `json:"path"`
	Size int64  `json:"size"`
}

func (c Config) fetchTree(ctx context.Context, hc *http.Client, rev string) ([]treeEntry, error) {
	u := fmt.Sprintf("%s/api/datasets/%s/tree/%s", c.apiBase(), c.DatasetID, url.PathEscape(rev))
	req, err := c.request(ctx, http.MethodGet, u, true)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lmeval: tree listing: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("lmeval: tree listing: HTTP %d", resp.StatusCode)
	}
	var tree []treeEntry
	if err := json.NewDecoder(resp.Body).Decode(&tree); err != nil {
		return nil, fmt.Errorf("lmeval: tree listing: %w", err)
	}
	return tree, nil
}

func (c Config) resolveRevision(ctx context.Context, hc *http.Client) (string, error) {
	u := fmt.Sprintf("%s/api/datasets/%s", c.apiBase(), c.DatasetID)
	req, err := c.request(ctx, http.MethodGet, u, true)
	if err != nil {
		return "", err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("lmeval: resolve revision: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("lmeval: resolve revision: HTTP %d", resp.StatusCode)
	}
	var info struct {
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", fmt.Errorf("lmeval: resolve revision: %w", err)
	}
	if info.SHA == "" {
		return "", fmt.Errorf("lmeval: resolve revision: empty sha for %s", c.DatasetID)
	}
	return info.SHA, nil
}

// request builds a request, attaching HF_TOKEN when the environment provides
// one. acceptJSON negotiates API (JSON) responses only.
func (c Config) request(ctx context.Context, method, u string, acceptJSON bool) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return nil, err
	}
	if acceptJSON {
		req.Header.Set("Accept", "application/json")
	}
	if tok := strings.TrimSpace(os.Getenv("HF_TOKEN")); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	req.Header.Set("User-Agent", "meept-bench-lmeval")
	return req, nil
}

func (c Config) apiBase() string {
	if c.APIBase != "" {
		return strings.TrimRight(c.APIBase, "/")
	}
	return DefaultAPIBase
}

func (c Config) rowsBase() string {
	if c.RowsBase != "" {
		return strings.TrimRight(c.RowsBase, "/")
	}
	return DefaultRowsBase
}

func (c Config) method() string {
	m := strings.ToLower(strings.TrimSpace(c.Method))
	if m == "" {
		return "download"
	}
	return m
}

// emitMode normalizes Mode; "" defaults to "context".
func (c Config) emitMode() string {
	m := strings.ToLower(strings.TrimSpace(c.Mode))
	if m == "" {
		return "context"
	}
	return m
}

// splitFile maps the friendly split name to the dataset's (extensionless)
// data filename.
func (c Config) splitFile() (string, error) {
	switch strings.ToLower(strings.TrimSpace(c.splitOr())) {
	case "", "s", "longmemeval_s":
		return "longmemeval_s", nil
	case "m", "longmemeval_m":
		return "longmemeval_m", nil
	case "oracle", "longmemeval_oracle":
		return "longmemeval_oracle", nil
	}
	return "", fmt.Errorf("lmeval: unknown split %q (want S, M, or oracle)", c.Split)
}

// splitCode is the short id component used in deterministic task ids.
func (c Config) splitCode() string {
	switch strings.ToLower(strings.TrimSpace(c.splitOr())) {
	case "m", "longmemeval_m":
		return "m"
	case "oracle", "longmemeval_oracle":
		return "oracle"
	default:
		return "s"
	}
}

func (c Config) splitOr() string {
	if c.Split == "" {
		return "S"
	}
	return c.Split
}
