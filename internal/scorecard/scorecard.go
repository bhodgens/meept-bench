// Package scorecard aggregates result rows into pass rates, cost, and failure
// taxonomy; emits markdown + JSON.
package scorecard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/bhodgens/meept-bench/internal/results"
)

// Card is the aggregate view of one run.
type Card struct {
	Suite       string         `json:"suite"`
	GeneratedAt time.Time      `json:"generated_at"`
	Tasks       int            `json:"tasks"`
	Attempts    int            `json:"attempts"`
	Passes      int            `json:"passes"`
	PassRate    float64        `json:"pass_rate"`
	MeanCostUSD float64        `json:"mean_cost_usd"`
	MeanSeconds float64        `json:"mean_wall_seconds"`
	Failures    map[string]int `json:"failure_taxonomy"` // error_kind → count
	TaskRates   []TaskRate     `json:"task_rates"`
	Label       string         `json:"label"` // always self-run per repo rules
}

// TaskRate is per-task rollup.
type TaskRate struct {
	TaskID   string  `json:"task_id"`
	PassPct  float64 `json:"pass_pct"`
	N        int     `json:"n"`
	MeanCost float64 `json:"mean_cost_usd"`
}

// LoadRows reads a results.jsonl file.
func LoadRows(path string) ([]results.Row, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rows []results.Row
	for _, line := range splitLines(data) {
		if len(line) == 0 {
			continue
		}
		var r results.Row
		if err := json.Unmarshal(line, &r); err != nil {
			return nil, fmt.Errorf("bad row in %s: %w", path, err)
		}
		rows = append(rows, r)
	}
	return rows, nil
}

func splitLines(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			out = append(out, b[start:i])
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
	}
	return out
}

// Build aggregates rows into a card.
func Build(suiteName string, rows []results.Row) (*Card, error) {
	if len(rows) == 0 {
		return nil, fmt.Errorf("no result rows")
	}
	if suiteName == "" {
		suiteName = rows[0].Suite
	}
	c := &Card{
		Suite: suiteName, GeneratedAt: time.Now().UTC(),
		Attempts: len(rows), Label: "self-run",
		Failures: map[string]int{},
	}
	type agg struct {
		n, pass int
		cost    float64
	}
	byTask := map[string]*agg{}
	var totalCost, totalWall float64
	taskOrder := []string{}
	for _, r := range rows {
		c.Tasks++ // attempts counted per row; unique tasks below
		if r.Passed {
			c.Passes++
		} else if r.ErrorKind != "" {
			c.Failures[r.ErrorKind]++
		}
		a := byTask[r.TaskID]
		if a == nil {
			a = &agg{}
			byTask[r.TaskID] = a
			taskOrder = append(taskOrder, r.TaskID)
		}
		a.n++
		if r.Passed {
			a.pass++
		}
		a.cost += r.CostUSD
		totalCost += r.CostUSD
		totalWall += r.WallSeconds
	}
	sort.Strings(taskOrder)
	for _, id := range taskOrder {
		a := byTask[id]
		c.TaskRates = append(c.TaskRates, TaskRate{
			TaskID: id, N: a.n,
			PassPct: pct(a.pass, a.n), MeanCost: a.cost / float64(a.n),
		})
	}
	c.Tasks = len(byTask)
	c.PassRate = pct(c.Passes, len(rows))
	c.MeanCostUSD = totalCost / float64(len(rows))
	c.MeanSeconds = totalWall / float64(len(rows))
	return c, nil
}

func pct(num, den int) float64 {
	if den == 0 {
		return 0
	}
	return float64(num) / float64(den)
}

// Write emits scorecard.md and scorecard.json under dir. Returns both paths.
func Write(dir string, c *Card) (string, string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	mdPath := filepath.Join(dir, "scorecard.md")
	jsonPath := filepath.Join(dir, "scorecard.json")

	jdata, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return "", "", err
	}
	if err := os.WriteFile(jsonPath, jdata, 0o644); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(mdPath, []byte(render(c)), 0o644); err != nil {
		return mdPath, jsonPath, err
	}
	return mdPath, jsonPath, nil
}

func render(c *Card) string {
	s := fmt.Sprintf("# Scorecard — %s\n\n", c.Suite)
	s += fmt.Sprintf("- Generated: %s\n", c.GeneratedAt.Format(time.RFC3339))
	s += fmt.Sprintf("- Label: **%s** (self-run; not comparable to vendor-published numbers unless configs match)\n", c.Label)
	s += fmt.Sprintf("- Tasks: %d · Attempts: %d · Passes: %d\n", c.Tasks, c.Attempts, c.Passes)
	s += fmt.Sprintf("- Pass rate: %.1f%%\n", 100*c.PassRate)
	s += fmt.Sprintf("- Mean cost/task: $%.4f · Mean wall time: %.1fs\n\n", c.MeanCostUSD, c.MeanSeconds)

	s += "## Per-task\n\n| Task | Attempts | Pass % | Mean cost |\n|---|---|---|---|\n"
	for _, t := range c.TaskRates {
		s += fmt.Sprintf("| %s | %d | %.0f%% | $%.4f |\n", t.TaskID, t.N, 100*t.PassPct, t.MeanCost)
	}

	if len(c.Failures) > 0 {
		s += "\n## Failure taxonomy\n\n"
		kinds := make([]string, 0, len(c.Failures))
		for k := range c.Failures {
			kinds = append(kinds, k)
		}
		sort.Strings(kinds)
		for _, k := range kinds {
			s += fmt.Sprintf("- %s: %d\n", k, c.Failures[k])
		}
	}
	if c.Suite != "" {
		s += "\nPer docs/BENCHMARKS.md: published scorecards must state exact task list, seeds, models, date, and self-run labeling. See results.jsonl for per-row seeds and HF revision.\n"
	}
	return s
}
