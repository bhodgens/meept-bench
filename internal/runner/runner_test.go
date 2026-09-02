package runner

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/bhodgens/meept-bench/internal/results"
	"github.com/bhodgens/meept-bench/internal/suite"
)

func TestErrRow(t *testing.T) {
	m := &suite.Manifest{Suite: "demo"}
	task := suite.Task{ID: "task-1"}
	start := time.Now().Add(-500 * time.Millisecond)

	row := errRow(m, task, 3, start, "error", "worktree: boom")
	if row == nil {
		t.Fatal("errRow returned nil")
	}
	if row.Suite != "demo" {
		t.Errorf("Suite = %q, want %q", row.Suite, "demo")
	}
	if row.TaskID != "task-1" {
		t.Errorf("TaskID = %q, want %q", row.TaskID, "task-1")
	}
	if row.Attempt != 3 {
		t.Errorf("Attempt = %d, want 3", row.Attempt)
	}
	if row.Verdict != "error" {
		t.Errorf("Verdict = %q, want %q", row.Verdict, "error")
	}
	if row.Passed {
		t.Error("Passed = true, want false")
	}
	if row.ErrorKind != "error" {
		t.Errorf("ErrorKind = %q, want %q", row.ErrorKind, "error")
	}
	if row.ErrorDetail != "worktree: boom" {
		t.Errorf("ErrorDetail = %q, want %q", row.ErrorDetail, "worktree: boom")
	}
	if row.WallSeconds < 0 {
		t.Errorf("WallSeconds = %v, want >= 0", row.WallSeconds)
	}
	if !row.StartedAt.Equal(start) {
		t.Errorf("StartedAt = %v, want %v", row.StartedAt, start)
	}

	// A timeout row should carry kind through to Verdict/ErrorKind as well.
	trow := errRow(m, task, 1, time.Now(), "timeout", "deadline exceeded")
	if trow.Verdict != "timeout" || trow.ErrorKind != "timeout" {
		t.Errorf("Verdict/ErrorKind = %q/%q, want timeout/timeout", trow.Verdict, trow.ErrorKind)
	}
	if trow.Passed {
		t.Error("timeout row Passed = true, want false")
	}
}

func TestToInt(t *testing.T) {
	cases := []struct {
		name   string
		in     any
		want   int64
		wantOK bool
	}{
		{"float64", float64(42.0), 42, true},
		{"float64 fractional", 3.9, 3, true},
		{"int64", int64(-7), -7, true},
		{"int", int(5), 5, true},
		{"string unsupported", "42", 0, false},
		{"nil unsupported", nil, 0, false},
		{"bool unsupported", true, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := toInt(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("toInt(%v) ok = %v, want %v", tc.in, ok, tc.wantOK)
			}
			if got != tc.want {
				t.Errorf("toInt(%v) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestToFloat(t *testing.T) {
	cases := []struct {
		name   string
		in     any
		want   float64
		wantOK bool
	}{
		{"float64", 1.25, 1.25, true},
		{"int64", int64(42), 42, true},
		{"int", int(7), 7, true},
		{"string unsupported", "1.5", 0, false},
		{"nil unsupported", nil, 0, false},
		{"bool unsupported", false, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := toFloat(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("toFloat(%v) ok = %v, want %v", tc.in, ok, tc.wantOK)
			}
			if got != tc.want {
				t.Errorf("toFloat(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestSanitize(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"suite/task a", "suite-task-a"},
		{"plain", "plain"},
		{"keep-alnum_012.xyz", "keep-alnum_012.xyz"},
		{"multi//path  here", "multi--path--here"},
		{"unicode → slash/", "unicode---slash-"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := sanitize(tc.in); got != tc.want {
			t.Errorf("sanitize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestAppendRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.jsonl")

	first := results.Row{
		Suite: "demo", TaskID: "task-1", Attempt: 1, Seed: 11,
		Verdict: "pass", Passed: true, StartedAt: time.Now().Truncate(time.Second),
	}
	second := results.Row{
		Suite: "demo", TaskID: "task-2", Attempt: 2, Seed: 22,
		Verdict: "error", Passed: false, ErrorKind: "error", ErrorDetail: "boom",
		StartedAt: time.Now().Truncate(time.Second),
	}

	if err := appendRow(path, first); err != nil {
		t.Fatalf("appendRow(first): %v", err)
	}
	if err := appendRow(path, second); err != nil {
		t.Fatalf("appendRow(second): %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	var got []results.Row
	sc := bufio.NewScanner(f)
	lines := 0
	for sc.Scan() {
		lines++
		var row results.Row
		if err := json.Unmarshal(sc.Bytes(), &row); err != nil {
			t.Fatalf("line %d is not valid JSON: %v", lines, err)
		}
		got = append(got, row)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if lines != 2 {
		t.Fatalf("got %d JSONL lines, want 2", lines)
	}
	if !reflect.DeepEqual(got[0], first) {
		t.Errorf("row 1 = %+v, want %+v", got[0], first)
	}
	if !reflect.DeepEqual(got[1], second) {
		t.Errorf("row 2 = %+v, want %+v", got[1], second)
	}
}

func TestOutDirOr(t *testing.T) {
	if got := (&Runner{}).outDirOr(); got != "results" {
		t.Errorf("empty Options outDirOr() = %q, want %q", got, "results")
	}
	r := &Runner{opt: Options{OutDir: "/x"}}
	if got := r.outDirOr(); got != "/x" {
		t.Errorf("OutDir set outDirOr() = %q, want %q", got, "/x")
	}
}
