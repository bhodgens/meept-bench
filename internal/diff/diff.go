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
// passed, else the last attempt's verdict. The reported wall/cost come from
// the best attempt: the fastest passing attempt when any attempt passed,
// otherwise the last attempt's.
func best(rows []results.Row) (verdict string, wall, cost float64) {
	verdict, wall, cost = "fail", 0, 0
	var passWall, passCost float64
	hadPass := false
	for _, r := range rows {
		if r.Passed {
			if !hadPass || r.WallSeconds < passWall {
				passWall, passCost = r.WallSeconds, r.CostUSD
			}
			hadPass = true
		}
		verdict, wall, cost = r.Verdict, r.WallSeconds, r.CostUSD
	}
	if hadPass {
		return "pass", passWall, passCost
	}
	return verdict, wall, cost
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
			curVerdict, curWall, curCost := best(curR)
			diffs = append(diffs, TaskDiff{
				TaskID: id, Baseline: "absent",
				Current: curVerdict, Status: StatusNew,
				CurrentWall: curWall, WallDelta: curWall, CurrentCost: curCost, CostDelta: curCost,
			})
		case !inCur:
			baseVerdict, baseWall, baseCost := best(baseR)
			diffs = append(diffs, TaskDiff{
				TaskID: id, Baseline: baseVerdict, Current: "absent",
				Status: StatusRemoved, BaselineWall: baseWall, WallDelta: -baseWall,
				BaselineCost: baseCost, CostDelta: -baseCost,
			})
		default:
			baseVerdict, baseWall, baseCost := best(baseR)
			curVerdict, curWall, curCost := best(curR)
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
		if d.BaselineWall > wallRegressMinBase &&
			d.WallDelta >= wallRegressFraction*d.BaselineWall {
			add(&s.WallRegressed, "wall", d.TaskID)
		}
	}
	for _, bucket := range []*[]string{&s.Regressed, &s.Fixed, &s.New, &s.Removed, &s.WallRegressed} {
		sort.Strings(*bucket)
	}
	return s
}
