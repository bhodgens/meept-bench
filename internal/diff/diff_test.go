package diff

import (
	"math"
	"testing"

	"github.com/bhodgens/meept-bench/internal/results"
)

func row(taskID string, attempt int, verdict string, wall, cost float64) results.Row {
	return results.Row{
		Suite:       "smoke",
		TaskID:      taskID,
		Attempt:     attempt,
		Verdict:     verdict,
		Passed:      verdict == "pass",
		WallSeconds: wall,
		CostUSD:     cost,
	}
}

func byID(diffs []TaskDiff) map[string]TaskDiff {
	m := make(map[string]TaskDiff, len(diffs))
	for _, d := range diffs {
		m[d.TaskID] = d
	}
	return m
}

func approxEq(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestCompareTransitions(t *testing.T) {
	base := []results.Row{
		row("t1", 1, "pass", 10, 0.01),  // unchanged-pass
		row("t2", 1, "pass", 10, 0.01),  // regresses
		row("t3", 1, "fail", 10, 0.01),  // fixed
		row("t4", 1, "fail", 10, 0.01),  // removed
		row("t5", 1, "pass", 12, 0.02),  // wall+cost delta on unchanged
		row("t7", 1, "pass", 10, 0.01),  // regresses via timeout
		row("t8", 1, "error", 3, 0.001), // unchanged-fail (error→fail)
	}
	cur := []results.Row{
		row("t1", 1, "pass", 10, 0.01),
		row("t2", 1, "fail", 15, 0.03),
		row("t3", 1, "pass", 11, 0.02),
		row("t6", 1, "fail", 5, 0.005), // new
		row("t5", 1, "pass", 14, 0.03),
		row("t7", 1, "timeout", 10, 0.01),
		row("t8", 1, "fail", 4, 0.002),
	}
	got := Compare(base, cur)
	if len(got) != 8 {
		t.Fatalf("want 8 diffs (union of task IDs), got %d: %+v", len(got), got)
	}
	m := byID(got)
	check := func(id, status, baseV, curV string, wallDelta, costDelta float64) {
		t.Helper()
		d, ok := m[id]
		if !ok {
			t.Fatalf("missing diff for %s", id)
		}
		if d.Status != status {
			t.Errorf("%s status = %q, want %q", id, d.Status, status)
		}
		if d.Baseline != baseV {
			t.Errorf("%s baseline = %q, want %q", id, d.Baseline, baseV)
		}
		if d.Current != curV {
			t.Errorf("%s current = %q, want %q", id, d.Current, curV)
		}
		if !approxEq(d.WallDelta, wallDelta) {
			t.Errorf("%s wall delta = %v, want %v", id, d.WallDelta, wallDelta)
		}
		if !approxEq(d.CostDelta, costDelta) {
			t.Errorf("%s cost delta = %v, want %v", id, d.CostDelta, costDelta)
		}
	}
	check("t1", "unchanged-pass", "pass", "pass", 0, 0)
	check("t2", "regressed", "pass", "fail", 5, 0.02)
	check("t3", "fixed", "fail", "pass", 1, 0.01)
	check("t5", "unchanged-pass", "pass", "pass", 2, 0.01)
	check("t6", "new", "absent", "fail", 5, 0.005)
	check("t7", "regressed", "pass", "timeout", 0, 0)
	check("t8", "unchanged-fail", "error", "fail", 1, 0.001)
	if d := m["t4"]; d.Status != "removed" || d.Current != "absent" {
		t.Errorf("t4 (baseline-only) = %+v, want removed/absent", d)
	}
	// Deterministic order: sorted by TaskID.
	for i := 1; i < len(got); i++ {
		if got[i-1].TaskID >= got[i].TaskID {
			t.Fatalf("diffs not sorted by TaskID: %+v", got)
		}
	}
}

func TestCompareBestAttemptSelection(t *testing.T) {
	base := []results.Row{
		row("t1", 1, "fail", 20, 0.02),
		row("t1", 2, "pass", 30, 0.04), // best: pass; wall/cost from the passing attempt
		row("t2", 1, "timeout", 100, 0.10),
		row("t2", 2, "error", 150, 0.15), // no pass: last attempt's verdict + stats
	}
	cur := []results.Row{
		row("t1", 1, "fail", 5, 0.01),
		row("t1", 2, "pass", 5, 0.01),  // now passes; best attempt = the fast pass
		row("t2", 1, "pass", 50, 0.05), // now passes on attempt 1
	}
	m := byID(Compare(base, cur))
	if len(m) != 2 {
		t.Fatalf("want 2 diffs, got %d: %+v", len(m), m)
	}
	d1 := m["t1"]
	if d1.Status != "unchanged-pass" || d1.Baseline != "pass" || d1.Current != "pass" {
		t.Fatalf("t1 = %+v, want unchanged-pass/pass/pass", d1)
	}
	if !approxEq(d1.WallDelta, -25) || !approxEq(d1.CostDelta, -0.03) {
		t.Errorf("t1 deltas = %v/%v, want -25/-0.03 (from the passing attempt)", d1.WallDelta, d1.CostDelta)
	}
	if !approxEq(d1.BaselineWall, 30) || !approxEq(d1.CurrentWall, 5) {
		t.Errorf("t1 walls = %v/%v, want 30/5", d1.BaselineWall, d1.CurrentWall)
	}
	d2 := m["t2"]
	if d2.Status != "fixed" || d2.Baseline != "error" || d2.Current != "pass" {
		t.Fatalf("t2 = %+v, want fixed/error/pass", d2)
	}
	if !approxEq(d2.BaselineWall, 150) || !approxEq(d2.WallDelta, -100) {
		t.Errorf("t2 wall = %v (delta %v), want 150 (-100)", d2.BaselineWall, d2.WallDelta)
	}
}

func TestCompareRepeatedRunsSameTask(t *testing.T) {
	// Real results.jsonl files append across runs: the same task+attempt can
	// appear many times. Best-of semantics still hold.
	base := []results.Row{
		row("t1", 1, "fail", 20, 0.02), // earlier run
		row("t1", 1, "pass", 25, 0.03), // later run, same attempt number
	}
	cur := []results.Row{
		row("t1", 1, "timeout", 40, 0.05),
		row("t1", 1, "pass", 35, 0.04),
	}
	m := byID(Compare(base, cur))
	d := m["t1"]
	if d.Status != "unchanged-pass" || d.Baseline != "pass" || d.Current != "pass" {
		t.Fatalf("t1 = %+v, want unchanged-pass", d)
	}
	if !approxEq(d.WallDelta, 10) || !approxEq(d.CostDelta, 0.01) {
		t.Errorf("t1 deltas = %v/%v, want 10/0.01", d.WallDelta, d.CostDelta)
	}
}

func TestCompareEmptySides(t *testing.T) {
	if got := Compare(nil, []results.Row{row("t1", 1, "pass", 1, 0.001)}); len(got) != 1 ||
		got[0].Status != "new" || got[0].Baseline != "absent" || got[0].Current != "pass" {
		t.Fatalf("empty baseline: %+v", got)
	}
	if got := Compare([]results.Row{row("t1", 1, "pass", 1, 0.001)}, nil); len(got) != 1 ||
		got[0].Status != "removed" || got[0].Current != "absent" {
		t.Fatalf("empty current: %+v", got)
	}
	if got := Compare(nil, nil); len(got) != 0 {
		t.Fatalf("both empty: %+v", got)
	}
}

func TestSummarizeWallThreshold(t *testing.T) {
	diffs := []TaskDiff{
		// baseline 10s → 15s: exactly +50% counts.
		{TaskID: "exact50", Status: "unchanged-pass", WallDelta: 5, BaselineWall: 10, CurrentWall: 15},
		// baseline 10s → 14.9s: +49% does not count.
		{TaskID: "under50", Status: "unchanged-pass", WallDelta: 4.9, BaselineWall: 10, CurrentWall: 14.9},
		// baseline 4s → 8s: +100% but baseline wall ≤ 5s noise floor.
		{TaskID: "fastnoise", Status: "unchanged-pass", WallDelta: 4, BaselineWall: 4, CurrentWall: 8},
		// baseline 5s → 7.5s: +50% but 5s is not > 5s.
		{TaskID: "fastboundary", Status: "unchanged-pass", WallDelta: 2.5, BaselineWall: 5, CurrentWall: 7.5},
		// baseline 6s → 8s: +33%, under threshold.
		{TaskID: "slower-but-under", Status: "unchanged-pass", WallDelta: 2, BaselineWall: 6, CurrentWall: 8},
		// baseline 10s → 5s: got faster.
		{TaskID: "improved", Status: "unchanged-pass", WallDelta: -5, BaselineWall: 10, CurrentWall: 5},
	}
	sum := Summarize(diffs)
	if len(sum.WallRegressed) != 1 || sum.WallRegressed[0] != "exact50" {
		t.Fatalf("WallRegressed = %v, want [exact50]", sum.WallRegressed)
	}
	for _, list := range []struct {
		name string
		got  []string
	}{
		{"Regressed", sum.Regressed}, {"Fixed", sum.Fixed},
		{"New", sum.New}, {"Removed", sum.Removed},
	} {
		if len(list.got) != 0 {
			t.Errorf("%s = %v, want empty", list.name, list.got)
		}
	}
}

func TestSummarizeBuckets(t *testing.T) {
	diffs := []TaskDiff{
		{TaskID: "r1", Status: "regressed"},
		{TaskID: "f1", Status: "fixed"},
		{TaskID: "n2", Status: "new"},
		{TaskID: "n1", Status: "new"},
		{TaskID: "x1", Status: "removed"},
		{TaskID: "u1", Status: "unchanged-pass"},
		{TaskID: "u2", Status: "unchanged-fail"},
	}
	sum := Summarize(diffs)
	want := map[string][]string{
		"Regressed":     {"r1"},
		"Fixed":         {"f1"},
		"New":           {"n1", "n2"},
		"Removed":       {"x1"},
		"WallRegressed": {},
	}
	for name, ids := range want {
		var got []string
		switch name {
		case "Regressed":
			got = sum.Regressed
		case "Fixed":
			got = sum.Fixed
		case "New":
			got = sum.New
		case "Removed":
			got = sum.Removed
		case "WallRegressed":
			got = sum.WallRegressed
		}
		if len(got) != len(ids) {
			t.Fatalf("%s = %v, want %v", name, got, ids)
		}
		for i, id := range ids {
			if got[i] != id {
				t.Errorf("%s[%d] = %q, want %q", name, i, got[i], id)
			}
		}
	}
}
