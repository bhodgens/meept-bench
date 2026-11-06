package diff

// Property-based tests for the diff/median/best aggregation layer, following
// the in-house hand-rolled Go pattern (strategist-usa
// go-shard-runner/internal/workers/parse_csv_property_test.go): deterministic
// PRNG, bounded case count, properties documented with file:line refs, no new
// dependencies.
//
// Properties under test:
//
//	P1 median order-invariance (diff.go median :99-110): median copies and
//	   sorts before indexing, so it must be invariant under any permutation
//	   of its input — including adversarial near-equal float64s that would
//	   expose a naive selection-based implementation.
//	P2 median vs reference model (diff.go :99-110): for random floats the
//	   median equals the value computed by an independent sort-free
//	   reference (quickselect copy), plus the even-length averaging rule.
//	P3 Compare self-compare idempotence (diff.go Compare :114-176, best
//	   :73-94): diffing a row set against itself yields exactly
//	   unchanged-pass or unchanged-fail for every task — never
//	   regressed/fixed/new/removed, never a nonzero WallDelta/CostDelta.
//	P4 Summarize partition + determinism (diff.go Summarize :181-217): the
//	   status buckets partition the diffs by construction, every bucket is
//	   sorted and duplicate-free, and WallRegressed ⊆ tasks whose
//	   BaselinePassObs >= 2 ∧ BaselineWall > 5 ∧ WallDelta >= 0.5·BaselineWall
//	   (the gate conditions at :207-209).
//	P5 Compare order-invariance (diff.go :114-176): Compare groups rows via
//	   a per-task map (:115-121) and sorts output by TaskID (:133), so
//	   permuting rows BETWEEN tasks must not change the output. WITHIN-task
//	   attempt order is deliberately preserved by the generator: best()'
//	   no-pass contract (:63, :88) is "the LAST attempt's verdict", so row
//	   order inside a task is semantically load-bearing, not incidental —
//	   permuting it would test a contract the code does not claim.
//
//	FINDING-2 (attempt-order sensitivity of no-pass verdicts, diff.go :88):
//	   for tasks with no passing attempt, Verdict/Cost come from whichever
//	   row happens to be last in the file. results.jsonl consumers that
//	   reorder, merge, or shard rows will see verdict flips (e.g. "fail" vs
//	   "error") and cost changes with identical attempt content. Replayable:
//	   seed 20261106+3, iter 3 (task-b: fail/error flip, cost 59.95 vs
//	   56.36 under within-task permutation). Likely intentional (attempts
//	   are appended chronologically), but undocumented at the Compare entry
//	   point; a doc comment on Compare would prevent silent misuse.
//
// Findings from a failing property are BUG REPORTS: do not weaken the
// property; report the minimal counterexample.

import (
	"math"
	"math/rand"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/bhodgens/meept-bench/internal/results"
)

const diffPropSeed = 20261106

var diffVerdicts = []string{"pass", "fail", "error", "timeout"}

func genDiffRow(rnd *rand.Rand, taskID string) results.Row {
	return results.Row{
		Suite:       "prop-suite",
		TaskID:      taskID,
		Attempt:     rnd.Intn(5) + 1,
		Verdict:     diffVerdicts[rnd.Intn(len(diffVerdicts))],
		Passed:      rnd.Intn(2) == 0,
		CostUSD:     math.Round(rnd.Float64()*100*100) / 100, // cent-scale
		WallSeconds: math.Round(rnd.Float64()*6000) / 10,    // 0..600s, 0.1s grain
		StartedAt:   time.Unix(rnd.Int63n(1_700_000_000), 0).UTC(),
	}
}

// genTaskRows returns 1..6 attempt rows for one task, shuffled (results.jsonl
// append order is not guaranteed to be attempt order).
func genTaskRows(rnd *rand.Rand, taskID string) []results.Row {
	n := rnd.Intn(6) + 1
	rows := make([]results.Row, n)
	for i := range rows {
		rows[i] = genDiffRow(rnd, taskID)
	}
	rnd.Shuffle(len(rows), func(i, j int) { rows[i], rows[j] = rows[j], rows[i] })
	return rows
}

// genAllRows returns rows for 1-4 tasks. Within each task, attempt order is
// preserved (semantically load-bearing — see FINDING-2); only task grouping
// is randomized.
func genAllRows(rnd *rand.Rand) []results.Row {
	ids := []string{"task-a", "task-b", "task-c", "task-d"}
	var rows []results.Row
	for _, id := range ids {
		if rnd.Intn(4) != 0 { // sometimes a task is absent from one side
			rows = append(rows, genTaskRows(rnd, id)...)
		}
	}
	if len(rows) == 0 {
		rows = append(rows, genDiffRow(rnd, "task-a"))
	}
	return rows
}

// permuteBetweenTasks shuffles whole task blocks, never rows within a task.
func permuteBetweenTasks(rnd *rand.Rand, rows []results.Row) []results.Row {
	blocks := map[string][]results.Row{}
	var order []string
	for _, r := range rows {
		if _, ok := blocks[r.TaskID]; !ok {
			order = append(order, r.TaskID)
		}
		blocks[r.TaskID] = append(blocks[r.TaskID], r)
	}
	rnd.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
	var out []results.Row
	for _, id := range order {
		out = append(out, blocks[id]...)
	}
	return out
}

// referenceMedian is an independent implementation used as a differential
// oracle for median(): copy, sort, apply the even-length averaging rule. It
// shares the spec but not the code path, so a bug in median()'s index math
// cannot hide in both.
func referenceMedian(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	mid := len(s) / 2
	if len(s)%2 == 1 {
		return s[mid]
	}
	return (s[mid-1] + s[mid]) / 2
}

// TestMedian_OrderInvariance ports P1: median must not depend on input order.
func TestMedian_OrderInvariance(t *testing.T) {
	rnd := rand.New(rand.NewSource(diffPropSeed))
	const iters = 500
	for i := 0; i < iters; i++ {
		n := rnd.Intn(50) + 1
		v := make([]float64, n)
		for j := range v {
			if rnd.Intn(4) == 0 {
				// adversarial cluster: near-equal values stress float handling
				v[j] = 5.0 + math.Nextafter(5.0, math.Inf(1))*float64(rnd.Intn(3))
			} else {
				v[j] = math.Round(rnd.Float64()*1e6) / 100
			}
		}
		want := median(v)
		for p := 0; p < 5; p++ {
			perm := append([]float64(nil), v...)
			rnd.Shuffle(len(perm), func(a, b int) { perm[a], perm[b] = perm[b], perm[a] })
			if got := median(perm); got != want {
				t.Fatalf("iter %d perm %d: median changed under permutation: %v vs %v (input %v)",
					i, p, got, want, v)
			}
		}
	}
}

// TestMedian_MatchesReference ports P2: differential oracle against an
// independent sort-based implementation.
func TestMedian_MatchesReference(t *testing.T) {
	rnd := rand.New(rand.NewSource(diffPropSeed + 1))
	const iters = 2000
	for i := 0; i < iters; i++ {
		n := rnd.Intn(64) // includes 0 (documented zero-value contract) and even lengths
		v := make([]float64, n)
		for j := range v {
			switch rnd.Intn(5) {
			case 0:
				v[j] = 0
			case 1:
				v[j] = math.MaxFloat64 / 1e300 // huge-but-finite value
			default:
				v[j] = math.Round(rnd.Float64()*1e9) / 1000
			}
		}
		want := referenceMedian(v)
		got := median(v)
		if math.IsNaN(got) || math.IsInf(got, 0) || got != want {
			t.Fatalf("iter %d: median(%v) = %v, reference = %v", i, v, got, want)
		}
		if n == 0 && got != 0 {
			t.Fatalf("iter %d: median(empty) = %v, want 0 (zero-value contract)", i, got)
		}
	}
}

// TestCompare_SelfDiffIsNeutral ports P3: a run diffed against itself must be
// all unchanged-* with zero deltas for every task present.
func TestCompare_SelfDiffIsNeutral(t *testing.T) {
	rnd := rand.New(rand.NewSource(diffPropSeed + 2))
	const iters = 300
	for i := 0; i < iters; i++ {
		rows := genAllRows(rnd)
		diffs := Compare(rows, rows)
		if len(diffs) == 0 {
			t.Fatalf("iter %d: self-compare produced no diffs for %d rows", i, len(rows))
		}
		for _, d := range diffs {
			if d.Status != StatusUnchangedPass && d.Status != StatusUnchangedFail {
				t.Fatalf("iter %d: task %s self-compare status = %q, want unchanged-*", i, d.TaskID, d.Status)
			}
			if d.Baseline != d.Current {
				t.Fatalf("iter %d: task %s self-compare verdict drift: %q vs %q", i, d.TaskID, d.Baseline, d.Current)
			}
			if d.WallDelta != 0 || d.CostDelta != 0 {
				t.Fatalf("iter %d: task %s self-compare nonzero delta: wall %v cost %v", i, d.TaskID, d.WallDelta, d.CostDelta)
			}
			if d.BaselineWall != d.CurrentWall || d.BaselineCost != d.CurrentCost {
				t.Fatalf("iter %d: task %s self-compare wall/cost drift", i, d.TaskID)
			}
		}
		ids := make([]string, len(diffs))
		for k, d := range diffs {
			ids[k] = d.TaskID
		}
		if !sort.StringsAreSorted(ids) {
			t.Fatalf("iter %d: Compare output not sorted by TaskID: %v", i, ids)
		}
	}
}

// TestCompare_OrderInvariance ports P5: permuting rows between tasks must
// not change Compare's output (within-task attempt order is preserved —
// see FINDING-2).
func TestCompare_OrderInvariance(t *testing.T) {
	rnd := rand.New(rand.NewSource(diffPropSeed + 3))
	const iters = 300
	for i := 0; i < iters; i++ {
		base := genAllRows(rnd)
		cur := genAllRows(rnd)
		want := Compare(base, cur)
		for p := 0; p < 3; p++ {
			bp := permuteBetweenTasks(rnd, base)
			cp := permuteBetweenTasks(rnd, cur)
			got := Compare(bp, cp)
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("iter %d perm %d: Compare changed under between-task permutation\nwant: %+v\ngot:  %+v", i, p, want, got)
			}
		}
	}
}

// TestSummarize_PartitionAndDeterminism ports P4: buckets partition by
// status, are sorted+unique, and WallRegressed obeys its documented gate.
func TestSummarize_PartitionAndDeterminism(t *testing.T) {
	rnd := rand.New(rand.NewSource(diffPropSeed + 4))
	const iters = 300
	for i := 0; i < iters; i++ {
		diffs := Compare(genAllRows(rnd), genAllRows(rnd))
		s := Summarize(diffs)

		bucketed := map[string]bool{}
		nStatus := 0
		for _, d := range diffs {
			switch d.Status {
			case StatusUnchangedPass, StatusUnchangedFail:
				// not bucketed by design (diff.go :194-203)
			case StatusRegressed, StatusFixed, StatusNew, StatusRemoved:
				nStatus++
			}
		}
		total := len(s.Regressed) + len(s.Fixed) + len(s.New) + len(s.Removed)
		if total != nStatus {
			t.Fatalf("iter %d: bucket total %d != status-count %d (partition broken)", i, total, nStatus)
		}

		for _, b := range []struct {
			name string
			ids  []string
		}{{"regressed", s.Regressed}, {"fixed", s.Fixed}, {"new", s.New}, {"removed", s.Removed}, {"wall", s.WallRegressed}} {
			if !sort.StringsAreSorted(b.ids) {
				t.Fatalf("iter %d: bucket %s not sorted: %v", i, b.name, b.ids)
			}
			for k := 1; k < len(b.ids); k++ {
				if b.ids[k] == b.ids[k-1] {
					t.Fatalf("iter %d: bucket %s has duplicate %q", i, b.name, b.ids[k])
				}
			}
			for _, id := range b.ids {
				if bucketed[id] && b.name != "wall" {
					t.Fatalf("iter %d: id %q in two exclusive buckets", i, id)
				}
			}
			for _, id := range b.ids {
				bucketed[id] = true
			}
		}

		// WallRegressed gate (diff.go :207-209).
		gate := map[string]bool{}
		for _, d := range diffs {
			if d.BaselinePassObs >= 2 && d.BaselineWall > 5.0 && d.WallDelta >= 0.5*d.BaselineWall {
				gate[d.TaskID] = true
			}
		}
		for _, id := range s.WallRegressed {
			if !gate[id] {
				t.Fatalf("iter %d: WallRegressed contains %q which fails the gate", i, id)
			}
		}

		// Determinism: same input → same output.
		if !reflect.DeepEqual(s, Summarize(diffs)) {
			t.Fatalf("iter %d: Summarize not deterministic", i)
		}
	}
}

// TestSummarize_WallGateBoundaries pins the documented threshold semantics
// (diff.go :19-25 "Exactly +50% counts; +49% does not", plus the obs>=2 and
// >5s floors) with exact boundary values.
func TestSummarize_WallGateBoundaries(t *testing.T) {
	cases := []struct {
		name      string
		baseWall  float64
		curWall   float64
		obs       int
		wantWall  bool
	}{
		{"exactly 50 pct counts", 10, 15, 2, true},
		{"49 pct does not count", 10, 14.9, 2, false},
		{"single obs suppressed", 10, 15, 1, false},
		{"zero obs suppressed (gate needs obs>=2)", 6, 9.1, 0, false},
	}
	for _, tc := range cases {
		diffs := []TaskDiff{{
			TaskID: "task-b", Status: StatusUnchangedPass,
			BaselineWall: tc.baseWall, CurrentWall: tc.curWall,
			WallDelta: tc.curWall - tc.baseWall, BaselinePassObs: tc.obs,
		}}
		s := Summarize(diffs)
		got := len(s.WallRegressed) == 1
		if got != tc.wantWall {
			t.Fatalf("%s: WallRegressed = %v, want %v", tc.name, s.WallRegressed, tc.wantWall)
		}
	}
}
