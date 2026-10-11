package diff

import (
	"sort"

	"github.com/bhodgens/meept-bench/internal/results"
)

// Status values for TaskDiff.Status.
const (
	StatusUnchangedPass = "unchanged-pass"
	StatusUnchangedFail = "unchanged-fail"
	StatusRegressed     = "regressed"
	StatusFixed         = "fixed"
	StatusNew           = "new"
	StatusRemoved       = "removed"
)

// wallRegressFraction is the relative wall-time growth that counts as a
// regression warning; wallRegressMinBase is the noise floor below which wall
// growth is ignored. Exactly +50% counts; +49% does not.
const (
	wallRegressFraction = 0.50
	wallRegressMinBase  = 5.0
)

// TaskDiff is one task-level comparison between baseline and current.
type TaskDiff struct {
	TaskID       string
	Baseline     string  // best verdict in baseline ("pass"/"fail"/"absent")
	Current      string  // best verdict in current
	Status       string  // unchanged-pass | unchanged-fail | regressed | fixed | new | removed
	WallDelta    float64 // current wall - baseline wall (best attempt each)
	CostDelta    float64 // current cost - baseline cost
	BaselineWall float64 // baseline best-attempt wall seconds (absent: 0)
	CurrentWall  float64 // current best-attempt wall seconds (absent: 0)
	BaselineCost float64 // baseline best-attempt cost USD (absent: 0)
	CurrentCost  float64 // current best-attempt cost USD (absent: 0)

	// BaselinePassObs is how many passing attempts the baseline wall was
	// computed over. Wall-regression warnings are suppressed when this is 1:
	// a single-observation baseline is a sample of unknown variance, and the
	// 2026-10-10 gate noise (session-workdir-bound 5.6s vs 9.1s, off a baseline
	// of n=1 drawn from a 1.1s-601s spread) came from exactly that.
	BaselinePassObs int
}

// Summary buckets a set of TaskDiffs. All ID slices are sorted and unique.
// WallRegressed lists tasks whose wall grew by +50% or more of the baseline
// wall AND whose baseline wall exceeded 5s (avoids noise on fast tasks).
type Summary struct {
	Regressed     []string
	Fixed         []string
	New           []string
	Removed       []string
	WallRegressed []string
}

// best collapses one task's attempt rows to best-attempt verdict, wall, and
// cost. Best-attempt semantics: a task's verdict is "pass" if any attempt
// passed, else the last attempt's verdict. The reported verdict/cost come from
// the best attempt: the fastest passing attempt when any attempt passed,
// otherwise the last attempt's.
//
// WALL is deliberately NOT the best attempt. A baseline recorded as the
// single fastest observation is a minimum, and comparing a fresh run against a
// minimum trips a relative threshold at random: session-workdir-bound recorded
// walls spanning 1.1s-601.2s across 52 runs, so its 5.6s "baseline" was the
// luckiest sample and every later run looked like a +50% regression. The wall
// is therefore the MEDIAN over passing attempts when at least one passed (the
// median of all attempts otherwise), which is stable against both a lucky fast
// run and a slow contended one.
func best(rows []results.Row) (verdict string, wall, cost float64, passObs int) {
	verdict, wall, cost = "fail", 0, 0
	var passWall, passCost float64
	hadPass := false
	var passing []float64
	var all []float64
	for _, r := range rows {
		if r.Passed {
			if !hadPass || r.WallSeconds < passWall {
				passWall, passCost = r.WallSeconds, r.CostUSD
			}
			passing = append(passing, r.WallSeconds)
			hadPass = true
		}
		all = append(all, r.WallSeconds)
		verdict, wall, cost = r.Verdict, r.WallSeconds, r.CostUSD
	}
	if hadPass {
		return "pass", median(passing), passCost, len(passing)
	}
	return verdict, median(all), cost, 0
}

// median returns the median of a non-empty slice (average of the two middle
// values for even lengths). Empty input returns 0, matching the zero-value
// contract of the wall fields.
func median(v []float64) float64 {
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

// Compare consumes raw rows and returns one TaskDiff per task ID seen in
// either file, sorted by TaskID.
func Compare(baseline, current []results.Row) []TaskDiff {
	byTask := func(rows []results.Row) map[string][]results.Row {
		m := make(map[string][]results.Row)
		for _, r := range rows {
			m[r.TaskID] = append(m[r.TaskID], r)
		}
		return m
	}
	baseRows, curRows := byTask(baseline), byTask(current)

	ids := make([]string, 0, len(baseRows)+len(curRows))
	for id := range baseRows {
		ids = append(ids, id)
	}
	for id := range curRows {
		if _, ok := baseRows[id]; !ok {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)

	diffs := make([]TaskDiff, 0, len(ids))
	for _, id := range ids {
		baseR, inBase := baseRows[id]
		curR, inCur := curRows[id]
		switch {
		case !inBase:
			curVerdict, curWall, curCost, _ := best(curR)
			diffs = append(diffs, TaskDiff{
				TaskID: id, Baseline: "absent",
				Current: curVerdict, Status: StatusNew,
				CurrentWall: curWall, WallDelta: curWall, CurrentCost: curCost, CostDelta: curCost,
			})
		case !inCur:
			baseVerdict, baseWall, baseCost, _ := best(baseR)
			diffs = append(diffs, TaskDiff{
				TaskID: id, Baseline: baseVerdict, Current: "absent",
				Status: StatusRemoved, BaselineWall: baseWall, WallDelta: -baseWall,
				BaselineCost: baseCost, CostDelta: -baseCost,
			})
		default:
			baseVerdict, baseWall, baseCost, baseObs := best(baseR)
			curVerdict, curWall, curCost, _ := best(curR)
			status := StatusUnchangedFail
			switch {
			case baseVerdict == "pass" && curVerdict == "pass":
				status = StatusUnchangedPass
			case baseVerdict == "pass":
				status = StatusRegressed
			case curVerdict == "pass":
				status = StatusFixed
			}
			diffs = append(diffs, TaskDiff{
				TaskID: id, Baseline: baseVerdict, Current: curVerdict, Status: status,
				WallDelta: curWall - baseWall, CostDelta: curCost - baseCost,
				BaselineWall: baseWall, CurrentWall: curWall,
				BaselineCost: baseCost, CurrentCost: curCost,
				BaselinePassObs: baseObs,
			})
		}
	}
	return diffs
}

// Summarize buckets diffs by status and flags wall-time regressions:
// WallDelta >= +50% of BaselineWall AND BaselineWall > 5s. Every ID slice is
// sorted and deduplicated so printed output is deterministic.
func Summarize(diffs []TaskDiff) Summary {
	s := Summary{}
	seen := map[string][]string{}
	add := func(bucket *[]string, key, id string) {
		for _, have := range seen[key] {
			if have == id {
				return
			}
		}
		seen[key] = append(seen[key], id)
		*bucket = append(*bucket, id)
	}
	for _, d := range diffs {
		switch d.Status {
		case StatusRegressed:
			add(&s.Regressed, "regressed", d.TaskID)
		case StatusFixed:
			add(&s.Fixed, "fixed", d.TaskID)
		case StatusNew:
			add(&s.New, "new", d.TaskID)
		case StatusRemoved:
			add(&s.Removed, "removed", d.TaskID)
		}
		// BaselinePassObs >= 2 is the sample-size gate: one observation has no
		// variance estimate, so a +50% delta off it is indistinguishable from
		// noise. See BaselinePassObs and best().
		if d.BaselinePassObs >= 2 &&
			d.BaselineWall > wallRegressMinBase &&
			d.WallDelta >= wallRegressFraction*d.BaselineWall {
			add(&s.WallRegressed, "wall", d.TaskID)
		}
	}
	for _, bucket := range []*[]string{&s.Regressed, &s.Fixed, &s.New, &s.Removed, &s.WallRegressed} {
		sort.Strings(*bucket)
	}
	return s
}
