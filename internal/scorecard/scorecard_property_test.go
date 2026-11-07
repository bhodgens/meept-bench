package scorecard

// Property-based tests for scorecard aggregation, following the in-house
// hand-rolled Go pattern (strategist-usa
// go-shard-runner/internal/workers/parse_csv_property_test.go): deterministic
// PRNG, bounded case count, properties documented with file:line refs, no new
// dependencies.
//
// Properties under test:
//
//	P1 order-invariance (scorecard.go Build): permuting the input rows must
//	   produce an identical Card except GeneratedAt — counts are plain
//	   integer sums and TaskRates are sorted by task ID, so no COUNT may
//	   depend on row order. Float means (MeanCostUSD, MeanSeconds,
//	   TaskRate.MeanCost) are summed in a deterministic sort order
//	   (lessAccum, scorecard.go) precisely so they too are permutation-
//	   invariant and scorecard.json stays byte-stable for diffing. This lane
//	   asserts EXACT float equality by default.
//
//	FINDING-1 (RESOLVED 2026-11-06): totalCost/totalWall once accumulated
//	   rows in file order, so MeanCostUSD/MeanSeconds drifted across
//	   permutations at 1e-16 relative scale (replayable: seed 20261106,
//	   iter 1 — 4.963465199999999 vs 4.9634652). Fixed in Build: rows are
//	   copied and sorted by a total key (TaskID, Attempt, Seed, CostUSD,
//	   WallSeconds) before summing, making the card byte-stable under any
//	   permutation. The old tolerance lane is gone; strict equality IS the
//	   default lane.
//	P2 conservation (scorecard.go :94-100): Passes <= Attempts,
//	   Tasks == number of distinct TaskIDs, and sum(Failures values) +
//	   pass-with-error rows <= Attempts. The aggregate can never invent or
//	   lose attempts.
//	P3 bounds (scorecard.go pct :130-135): PassRate and every
//	   TaskRate.PassPct lie in [0,1]; MeanCostUSD and MeanSeconds are finite
//	   and non-negative for non-negative inputs.
//	P4 no-throw on malformed JSONL (scorecard.go LoadRows :40-57): arbitrary
//	   bytes either parse to rows or return a countable error — never an
//	   uncontrolled panic.
//
// Findings from a failing property are BUG REPORTS: do not weaken the
// property; report the minimal counterexample.

import (
	"bytes"
	"encoding/json"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bhodgens/meept-bench/internal/results"
)

// validRowJSON is one canonical results.Row JSONL line, used to seed hostile
// byte streams with real wire shapes (the overfit-generator pitfall).
func validRowJSON() string {
	b, _ := json.Marshal(results.Row{
		Suite: "prop-suite", TaskID: "task-a", Attempt: 1, Seed: 1,
		Verdict: "pass", Passed: true, CostUSD: 0.01, WallSeconds: 12.5,
		StartedAt: time.Unix(1_700_000_000, 0).UTC(),
	})
	return string(b)
}

// propSeed is fixed so failures are replayable. Bump the case count via
// MEEPT_BENCH_PROP_ITERS for an exhaustive local run.
const propSeed = 20261106

var verdicts = []string{"pass", "fail", "error", "timeout"}
var errorKinds = []string{"", "", "timeout", "auth_failure", "crash", "judge_error"}
var taskIDPool = []string{"task-a", "task-b", "task-c", "task-d", "task-e", "", "t\u00e9sk-\U0001F40D"}

func genRow(rnd *rand.Rand) results.Row {
	verdict := verdicts[rnd.Intn(len(verdicts))]
	return results.Row{
		Suite:       "prop-suite",
		TaskID:      taskIDPool[rnd.Intn(len(taskIDPool))],
		Attempt:     rnd.Intn(5) + 1,
		Seed:        rnd.Int63(),
		Verdict:     verdict,
		Passed:      rnd.Intn(2) == 0,
		CostUSD:     math.Round(rnd.Float64()*1e6) / 1e6 * 10, // cent-scale, no float artifacts
		WallSeconds: math.Round(rnd.Float64()*1000) / 1000 * 600,
		ErrorKind:   errorKinds[rnd.Intn(len(errorKinds))],
		StartedAt:   time.Unix(rnd.Int63n(1_700_000_000), 0).UTC(),
	}
}

func genRows(rnd *rand.Rand, n int) []results.Row {
	rows := make([]results.Row, n)
	for i := range rows {
		rows[i] = genRow(rnd)
	}
	return rows
}

func shuffled(rnd *rand.Rand, rows []results.Row) []results.Row {
	out := append([]results.Row(nil), rows...)
	rnd.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

func cardWithoutTime(c *Card) Card {
	cp := *c
	cp.GeneratedAt = time.Time{}
	return cp
}

// TestBuild_OrderInvariance ports P1: the full Card (except GeneratedAt) must
// be bit-identical under any permutation of the input rows — exact float
// equality, no tolerance lane (FINDING-1 fixed via deterministic accumulation;
// the old tolerance/strict split is gone).
func TestBuild_OrderInvariance(t *testing.T) {
	rnd := rand.New(rand.NewSource(propSeed))
	const iters = 500
	for i := 0; i < iters; i++ {
		rows := genRows(rnd, rnd.Intn(60)+1)
		base, err := Build("prop-suite", rows)
		if err != nil {
			t.Fatalf("iter %d: Build: %v", i, err)
		}
		bc := cardWithoutTime(base)
		for p := 0; p < 3; p++ {
			perm := shuffled(rnd, rows)
			got, err := Build("prop-suite", perm)
			if err != nil {
				t.Fatalf("iter %d perm %d: Build: %v", i, p, err)
			}
			gc := cardWithoutTime(got)
			if !reflect.DeepEqual(bc, gc) {
				t.Fatalf("iter %d perm %d: card not permutation-invariant (FINDING-1 regression)\nbase: %+v\ngot:  %+v", i, p, bc, gc)
			}
		}
	}
}

// TestBuild_InputNotMutated pins that Build leaves the caller's row slice
// untouched: the deterministic accumulation order sorts a private copy, so
// callers may rely on their own ordering afterwards (the runner logs rows in
// append order).
func TestBuild_InputNotMutated(t *testing.T) {
	rnd := rand.New(rand.NewSource(propSeed + 3))
	rows := genRows(rnd, 40)
	before := append([]results.Row(nil), rows...)
	if _, err := Build("prop-suite", rows); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !reflect.DeepEqual(rows, before) {
		t.Fatal("Build mutated the caller's rows slice")
	}
}

// TestBuild_ConservationAndBounds ports P2+P3: attempts are conserved and
// every rate lies in [0,1].
func TestBuild_ConservationAndBounds(t *testing.T) {
	rnd := rand.New(rand.NewSource(propSeed + 1))
	const iters = 500
	for i := 0; i < iters; i++ {
		rows := genRows(rnd, rnd.Intn(80)+1)
		card, err := Build("prop-suite", rows)
		if err != nil {
			t.Fatalf("iter %d: Build: %v", i, err)
		}

		// P2 conservation.
		if card.Attempts != len(rows) {
			t.Fatalf("iter %d: Attempts = %d, want %d (rows lost or invented)", i, card.Attempts, len(rows))
		}
		if card.Passes > card.Attempts {
			t.Fatalf("iter %d: Passes %d > Attempts %d", i, card.Passes, card.Attempts)
		}
		uniq := map[string]int{}
		for _, r := range rows {
			uniq[r.TaskID]++
		}
		if card.Tasks != len(uniq) {
			t.Fatalf("iter %d: Tasks = %d, want %d distinct TaskIDs", i, card.Tasks, len(uniq))
		}
		seen := map[string]bool{}
		totalTaskAttempts := 0
		for _, tr := range card.TaskRates {
			if seen[tr.TaskID] {
				t.Fatalf("iter %d: duplicate TaskRate for %s", i, tr.TaskID)
			}
			seen[tr.TaskID] = true
			if tr.N != uniq[tr.TaskID] {
				t.Fatalf("iter %d: TaskRate[%s].N = %d, want %d", i, tr.TaskID, tr.N, uniq[tr.TaskID])
			}
			if tr.PassPct < 0 || tr.PassPct > 1 {
				t.Fatalf("iter %d: TaskRate[%s].PassPct = %f outside [0,1]", i, tr.TaskID, tr.PassPct)
			}
			if tr.MeanCost < 0 {
				t.Fatalf("iter %d: negative MeanCost %f for %s", i, tr.MeanCost, tr.TaskID)
			}
			totalTaskAttempts += tr.N
		}
		if totalTaskAttempts != len(rows) {
			t.Fatalf("iter %d: per-task attempts %d != total rows %d", i, totalTaskAttempts, len(rows))
		}

		// P3 bounds on card-level rates/means.
		if card.PassRate < 0 || card.PassRate > 1 {
			t.Fatalf("iter %d: PassRate = %f outside [0,1]", i, card.PassRate)
		}
		if card.MeanCostUSD < 0 || math.IsNaN(card.MeanCostUSD) || math.IsInf(card.MeanCostUSD, 0) {
			t.Fatalf("iter %d: MeanCostUSD = %f not finite non-negative", i, card.MeanCostUSD)
		}
		if card.MeanSeconds < 0 || math.IsNaN(card.MeanSeconds) || math.IsInf(card.MeanSeconds, 0) {
			t.Fatalf("iter %d: MeanSeconds = %f not finite non-negative", i, card.MeanSeconds)
		}
	}
}

// TestLoadRows_NoThrowOnArbitraryBytes ports P4: arbitrary bytes through the
// JSONL reader either parse or produce a wrapped error, never a panic.
func TestLoadRows_NoThrowOnArbitraryBytes(t *testing.T) {
	hostileCorners := []string{
		"", "\n", "\n\n\n", "{", "not json", `{"suite":`, "null", "[]",
		`{"task_id":123}`, strings.Repeat("x", 64*1024),
		"\x00\xff\xfe", "\U0001F40D\u202E", `{bad json}` + "\n" + `{bad too}`,
	}
	rnd := rand.New(rand.NewSource(propSeed + 2))
	inputs := make([][]byte, 0, len(hostileCorners)*2+8)
	for _, s := range hostileCorners {
		inputs = append(inputs, []byte(s))
		inputs = append(inputs, []byte(validRowJSON()+"\n"+s+"\n"))
	}
	for i := 0; i < 200; i++ {
		buf := &bytes.Buffer{}
		for j := 0; j < rnd.Intn(20); j++ {
			if rnd.Intn(2) == 0 {
				buf.WriteString(validRowJSON() + "\n")
			} else {
				n := rnd.Intn(64)
				b := make([]byte, n)
				rnd.Read(b)
				buf.Write(b)
				buf.WriteByte('\n')
			}
		}
		inputs = append(inputs, buf.Bytes())
	}

	dir := t.TempDir()
	for i, input := range inputs {
		path := filepath.Join(dir, "f.jsonl")
		if err := os.WriteFile(path, input, 0o644); err != nil {
			t.Fatalf("write input %d: %v", i, err)
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("input %d (%q...) panicked: %v", i, truncate(input, 60), r)
				}
			}()
			// The property under test is no-panic; error-vs-rows is the
			// documented LoadRows contract (wrapped error on bad rows).
			_, _ = LoadRows(path)
		}()
	}
}

// TestBuild_ZeroRowsRejected pins the documented error contract for the empty
// input (scorecard.go :76-78).
func TestBuild_ZeroRowsRejected(t *testing.T) {
	if _, err := Build("prop-suite", nil); err == nil {
		t.Fatal("Build(nil) returned no error; contract requires 'no result rows' error")
	}
}

func truncate(b []byte, n int) string {
	s := string(b)
	if len(s) > n {
		s = s[:n] + "..."
	}
	return s
}
