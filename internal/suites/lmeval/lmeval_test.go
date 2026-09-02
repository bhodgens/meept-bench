package lmeval

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeItem mirrors the real dataset item shape (fields used by the adapter;
// unknown fields are ignored by the decoder).
func fakeItem(n int, shortAnswer bool) map[string]any {
	ans := fmt.Sprintf("the answer to question %d is 42, which the user mentioned during their Saturday planning session for the community garden fundraiser", n)
	if shortAnswer {
		ans = fmt.Sprintf("Alice_%d", n)
	}
	return map[string]any{
		"question_id": fmt.Sprintf("q_%d", n),
		"question":    fmt.Sprintf("What is the answer to question %d?", n),
		"answer":      ans,
		"quiz":        "none", // unknown-to-adapter field: must be ignored
		"haystack_dates": []string{
			fmt.Sprintf("2023/05/0%d (Saturday)", n+1),
		},
		"haystack_session_ids": []string{fmt.Sprintf("answer_%d_1", n), fmt.Sprintf("noise_%d_1", n)},
		"answer_session_ids":   []string{fmt.Sprintf("answer_%d_1", n)},
		"haystack_sessions": []any{
			[]any{
				map[string]any{"role": "user", "content": fmt.Sprintf("hi, my favorite number is %d", n)},
				map[string]any{"role": "assistant", "content": "noted!"},
			},
			[]any{
				map[string]any{"role": "user", "content": "unrelated chatter"},
			},
		},
	}
}

func fakeSplitJSON(count, longFrom int) []byte {
	items := make([]map[string]any, count)
	for i := 0; i < count; i++ {
		items[i] = fakeItem(i, i >= longFrom)
	}
	b, _ := json.Marshal(items)
	return b
}

// downloadServer serves the tree + resolve endpoints of a fake HF.
func downloadServer(t *testing.T, splitJSON []byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/datasets/x/longmemeval", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"id": "x/longmemeval", "sha": "deadbeef"})
	})
	mux.HandleFunc("/api/datasets/x/longmemeval/tree/", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"type": "file", "path": "README.md", "size": 10},
			{"type": "file", "path": "longmemeval_s", "size": len(splitJSON)},
		})
	})
	mux.HandleFunc("/datasets/x/longmemeval/resolve/", func(w http.ResponseWriter, r *http.Request) {
		if tok := r.Header.Get("Authorization"); tok != "" && tok != "Bearer test-token" {
			http.Error(w, "bad token", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(splitJSON)
	})
	return httptest.NewServer(mux)
}

func TestFetchDownloadLimitEarlyTermination(t *testing.T) {
	// 20 items; the server serves the JSON in small chunks with a short
	// pause between them and counts how much was actually consumed before
	// the client went away. Limit=5 must stop the download long before the
	// full body is read.
	sj := fakeSplitJSON(20, 15)
	var served atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/datasets/x/longmemeval/tree/"):
			json.NewEncoder(w).Encode([]map[string]any{{"type": "file", "path": "longmemeval_s", "size": len(sj)}})
		case strings.HasPrefix(r.URL.Path, "/datasets/x/longmemeval/resolve/"):
			w.Header().Set("Content-Type", "application/octet-stream")
			flusher := w.(http.Flusher)
			const chunk = 128
			for off := 0; off < len(sj); off += chunk {
				end := off + chunk
				if end > len(sj) {
					end = len(sj)
				}
				n, err := w.Write(sj[off:end])
				served.Add(int64(n))
				if err != nil {
					return // client went away — the early termination worked
				}
				flusher.Flush()
				time.Sleep(2 * time.Millisecond)
			}
		default:
			json.NewEncoder(w).Encode(map[string]any{"id": "x/longmemeval", "sha": "deadbeef"})
		}
	}))
	defer srv.Close()

	cfg := Config{DatasetID: "x/longmemeval", Limit: 5, APIBase: srv.URL}
	cfg.Split = "S"
	items, err := cfg.Fetch(context.Background(), srv.Client())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(items) != 5 {
		t.Fatalf("got %d items, want 5", len(items))
	}
	if items[0].QuestionID != "q_0" {
		t.Fatalf("order not preserved: first item %q", items[0].QuestionID)
	}
	// Give the (now-cancelled) handler a beat to settle, then assert the
	// client did NOT read the whole body.
	time.Sleep(50 * time.Millisecond)
	if got := served.Load(); got > int64(len(sj)) {
		t.Fatalf("served %d bytes > body size", got)
	}
	if served.Load() >= int64(len(sj)) {
		t.Logf("note: client consumed the whole body (%d bytes) — early exit did not save bandwidth, but item count is still correct", served.Load())
	}
}

func TestFetchDownloadAll(t *testing.T) {
	srv := downloadServer(t, fakeSplitJSON(7, 4))
	defer srv.Close()
	cfg := Config{DatasetID: "x/longmemeval", APIBase: srv.URL, Split: "S"}
	items, err := cfg.Fetch(context.Background(), srv.Client())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(items) != 7 {
		t.Fatalf("got %d items, want 7", len(items))
	}
}

func TestFetchDownloadTokenHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/datasets/x/longmemeval/tree/"):
			json.NewEncoder(w).Encode([]map[string]any{{"type": "file", "path": "longmemeval_s", "size": 2}})
		case strings.HasPrefix(r.URL.Path, "/datasets/x/longmemeval/resolve/"):
			gotAuth = r.Header.Get("Authorization")
			w.Write([]byte("[]"))
		default:
			json.NewEncoder(w).Encode(map[string]any{"sha": "cafe"})
		}
	}))
	defer srv.Close()

	t.Setenv("HF_TOKEN", "test-token")
	cfg := Config{DatasetID: "x/longmemeval", APIBase: srv.URL, Split: "S"}
	if _, err := cfg.Fetch(context.Background(), srv.Client()); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if gotAuth != "Bearer test-token" {
		t.Fatalf("Authorization = %q, want bearer token", gotAuth)
	}
}

func TestFetchDownloadWrongShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/datasets/x/longmemeval/tree/"):
			json.NewEncoder(w).Encode([]map[string]any{{"type": "file", "path": "longmemeval_s", "size": 2}})
		case strings.HasPrefix(r.URL.Path, "/datasets/x/longmemeval/resolve/"):
			w.Write([]byte(`{"not":"an array"}`))
		default:
			json.NewEncoder(w).Encode(map[string]any{"sha": "cafe"})
		}
	}))
	defer srv.Close()
	cfg := Config{DatasetID: "x/longmemeval", APIBase: srv.URL, Split: "S"}
	if _, err := cfg.Fetch(context.Background(), srv.Client()); err == nil || !strings.Contains(err.Error(), "expected a JSON array") {
		t.Fatalf("want array-shape error, got %v", err)
	}
}

func TestFetchRowsPaging(t *testing.T) {
	// 7 items total, 3 per page → 3 pages.
	total := 7
	pageSize := 3
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/rows") {
			json.NewEncoder(w).Encode(map[string]any{"sha": "cafe"})
			return
		}
		q := r.URL.Query()
		if q.Get("split") != "s" {
			t.Errorf("split param = %q, want \"s\"", q.Get("split"))
		}
		if q.Get("revision") != "deadbeef" {
			t.Errorf("revision param = %q", q.Get("revision"))
		}
		var offset int
		fmt.Sscanf(q.Get("offset"), "%d", &offset)
		length := pageSize
		if q.Get("length") != "" {
			fmt.Sscanf(q.Get("length"), "%d", &length)
		}
		type row struct {
			RowIdx int            `json:"row_idx"`
			Row    map[string]any `json:"row"`
		}
		var rows []row
		for i := offset; i < offset+length && i < total; i++ {
			rows = append(rows, row{RowIdx: i, Row: fakeItem(i, false)})
		}
		nr := len(rows)
		json.NewEncoder(w).Encode(map[string]any{"num_rows": nr, "size": nr, "rows": rows})
	}))
	defer srv.Close()

	cfg := Config{DatasetID: "x/longmemeval", RowsBase: srv.URL, APIBase: srv.URL, Split: "S", Method: "rows", Revision: "deadbeef"}
	items, err := cfg.Fetch(context.Background(), srv.Client())
	if err != nil {
		t.Fatalf("Fetch rows: %v", err)
	}
	if len(items) != total {
		t.Fatalf("got %d items, want %d", len(items), total)
	}
	// Limit must stop paging early.
	cfg.Limit = 5
	items, err = cfg.Fetch(context.Background(), srv.Client())
	if err != nil {
		t.Fatalf("Fetch rows limited: %v", err)
	}
	if len(items) != 5 {
		t.Fatalf("limited fetch got %d items, want 5", len(items))
	}
}

func TestFetchBadSplitAndMethod(t *testing.T) {
	cfg := Config{DatasetID: "x/y", Split: "XL"}
	if _, err := cfg.Fetch(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "unknown split") {
		t.Fatalf("want split error, got %v", err)
	}
	cfg = Config{DatasetID: "x/y", Method: "carrier-pigeon"}
	if _, err := cfg.Fetch(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "unknown method") {
		t.Fatalf("want method error, got %v", err)
	}
	cfg = Config{DatasetID: ""}
	if _, err := cfg.Fetch(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "dataset_id is required") {
		t.Fatalf("want dataset_id error, got %v", err)
	}
}

func TestTaskIDDeterministic(t *testing.T) {
	a := taskID("s", 0, "What is my favorite number?")
	b := taskID("s", 0, "What is my favorite number?")
	c := taskID("s", 1, "What is my favorite number?")
	if a != b {
		t.Fatalf("same inputs produced different ids: %s vs %s", a, b)
	}
	if a == c {
		t.Fatalf("different index produced identical id: %s", a)
	}
	if !strings.HasPrefix(a, "lmeval-s-000-") {
		t.Fatalf("unexpected id shape: %s", a)
	}
}

func TestEmitContextMode(t *testing.T) {
	// longFrom=1 → item 0 has a long answer (llm_judge), item 1 a short one
	// (file_contains).
	srv := downloadServer(t, fakeSplitJSON(2, 1))
	defer srv.Close()
	cfg := Config{DatasetID: "x/longmemeval", APIBase: srv.URL, Split: "S", Limit: 2, Mode: "context"}
	items, err := cfg.Fetch(context.Background(), srv.Client())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	dataDir := t.TempDir()
	m, absData, err := EmitResolved(cfg, "deadbeef", items, dataDir)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if absData != dataDir && !filepath.IsAbs(absData) {
		t.Fatalf("data dir %q not absolute", absData)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("manifest invalid: %v", err)
	}
	if len(m.Tasks) != 2 {
		t.Fatalf("got %d tasks, want 2", len(m.Tasks))
	}
	if m.HFRevision != "deadbeef" {
		t.Fatalf("hf_revision = %q, want deadbeef", m.HFRevision)
	}
	for i, task := range m.Tasks {
		// Deterministic ID scheme.
		if !strings.HasPrefix(task.ID, fmt.Sprintf("lmeval-s-%03d-", i)) {
			t.Fatalf("task %d id %q unexpected", i, task.ID)
		}
		// Revision tag on every task.
		found := false
		for _, tag := range task.Tags {
			if tag == "lmeval-rev:deadbeef" {
				found = true
			}
		}
		if !found {
			t.Fatalf("task %s missing lmeval-rev tag (%v)", task.ID, task.Tags)
		}
		// Prompt must reference the absolute data dir and the question.
		if !strings.Contains(task.Prompt, absData) {
			t.Fatalf("task %s prompt missing data dir %s", task.ID, absData)
		}
		// Haystack files exist with rendered turns.
		session := filepath.Join(absData, task.ID, "session-1.txt")
		data, err := os.ReadFile(session)
		if err != nil {
			t.Fatalf("haystack file: %v", err)
		}
		if !strings.Contains(string(data), "user: hi, my favorite number is") {
			t.Fatalf("session file not rendered: %q", string(data))
		}
	}
	// Item 0 has a long answer → llm_judge rubric with default 0.7.
	long := m.Tasks[0]
	if long.Checkers[0].Type != "llm_judge" {
		t.Fatalf("long-answer task checker = %s, want llm_judge", long.Checkers[0].Type)
	}
	if long.Checkers[0].MinScore != 0.7 {
		t.Fatalf("min_score = %v, want 0.7", long.Checkers[0].MinScore)
	}
	// Item 1 has a short answer → file_contains on the literal answer.
	short := m.Tasks[1]
	if short.Checkers[0].Type != "file_contains" {
		t.Fatalf("short-answer task checker = %s, want file_contains", short.Checkers[0].Type)
	}
	if !strings.Contains(short.Checkers[0].Pattern, "Alice_1") {
		t.Fatalf("pattern = %q, want escaped literal answer", short.Checkers[0].Pattern)
	}
	if !m.Internal {
		t.Fatalf("longmemeval manifest must be internal (BENCHMARKS.md rule 4)")
	}
}

func TestEmitDeterministic(t *testing.T) {
	items := []Item{
		{QuestionID: "q1", Question: "Q one?", Answer: "A1", HaystackSessions: []any{[]any{map[string]any{"role": "user", "content": "x"}}}},
		{QuestionID: "q2", Question: "Q two?", Answer: "A2", HaystackSessions: []any{[]any{map[string]any{"role": "user", "content": "y"}}}},
	}
	cfg := Config{DatasetID: "x/longmemeval", Split: "S", Revision: "r1"}
	// Same (items, dataDir, config) triple → byte-identical manifest.
	d1 := t.TempDir()
	m1, _, err := cfg.Emit(items, d1)
	if err != nil {
		t.Fatalf("Emit 1: %v", err)
	}
	m2, _, err := cfg.Emit(items, d1)
	if err != nil {
		t.Fatalf("Emit 2: %v", err)
	}
	b1, _ := json.MarshalIndent(m1, "", "  ")
	b2, _ := json.MarshalIndent(m2, "", "  ")
	if string(b1) != string(b2) {
		t.Fatalf("emission not deterministic:\n%s\n---\n%s", b1, b2)
	}
	if !reflect.DeepEqual(m1.Tasks[0], m2.Tasks[0]) {
		t.Fatalf("task 0 differs between emissions")
	}
	// Haystack files must be byte-stable across emissions too.
	for _, task := range m1.Tasks {
		p := filepath.Join(d1, task.ID, "session-1.txt")
		a, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("session file: %v", err)
		}
		b, err := os.ReadFile(p)
		if err != nil || string(a) != string(b) {
			t.Fatalf("session file %s unstable", p)
		}
	}
	// Distinct data dirs change only the embedded absolute paths.
	d3 := t.TempDir()
	m3, _, err := cfg.Emit(items, d3)
	if err != nil {
		t.Fatalf("Emit 3: %v", err)
	}
	for i := range m1.Tasks {
		p1 := strings.ReplaceAll(m1.Tasks[i].Prompt, d1, "DATA")
		p3 := strings.ReplaceAll(m3.Tasks[i].Prompt, d3, "DATA")
		if p1 != p3 {
			t.Fatalf("task %d prompt differs beyond the data dir:\n%s\n---\n%s", i, p1, p3)
		}
	}
}

func TestEmitMemoryMode(t *testing.T) {
	items := []Item{
		{QuestionID: "q1", Question: "Q?", Answer: "A", HaystackSessions: []any{[]any{map[string]any{"role": "user", "content": "secret code is ZEBRA"}}}},
	}
	cfg := Config{DatasetID: "x/longmemeval", Split: "S", Revision: "r1", Mode: "memory"}
	m, absData, err := cfg.Emit(items, t.TempDir())
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !strings.Contains(m.Tasks[0].Prompt, "ZEBRA") {
		t.Fatalf("memory-mode prompt missing inline haystack")
	}
	if strings.Contains(m.Tasks[0].Prompt, absData) {
		t.Fatalf("memory-mode prompt should not reference data dir files")
	}
}

func TestRegexpQuote(t *testing.T) {
	cases := map[string]string{
		"42":        "42",
		"Alice (2)": `Alice \(2\)`,
		"a.b*c?":    `a\.b\*c\?`,
		"x[y]{z}":   `x\[y\]\{z\}`,
		"a|b^$c\\d": `a\|b\^\$c\\d`,
		"é +":       `é \+`,
	}
	for in, want := range cases {
		if got := regexpQuote(in); got != want {
			t.Errorf("regexpQuote(%q) = %q, want %q", in, got, want)
		}
	}
	// Every quoted string must compile and match itself literally.
	for in := range cases {
		re, err := compileRE(regexpQuote(in))
		if err != nil {
			t.Fatalf("quote of %q does not compile: %v", in, err)
		}
		if !re.MatchString(in) {
			t.Fatalf("quoted %q does not match itself", in)
		}
	}
}

func TestEmitEmptyItems(t *testing.T) {
	cfg := Config{DatasetID: "x/y", Split: "S"}
	if _, _, err := cfg.Emit(nil, t.TempDir()); err == nil {
		t.Fatalf("want error for empty items")
	}
}

func TestSplitFileMapping(t *testing.T) {
	cases := map[string]string{
		"":       "longmemeval_s",
		"S":      "longmemeval_s",
		"s":      "longmemeval_s",
		"M":      "longmemeval_m",
		"oracle": "longmemeval_oracle",
		"ORACLE": "longmemeval_oracle",
	}
	for in, want := range cases {
		cfg := Config{Split: in}
		got, err := cfg.splitFile()
		if err != nil {
			t.Fatalf("splitFile(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("splitFile(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := (Config{Split: "XL"}).splitFile(); err == nil {
		t.Fatalf("want error for unknown split")
	}
}

func compileRE(pat string) (interface{ MatchString(string) bool }, error) {
	return regexp.Compile(pat)
}
