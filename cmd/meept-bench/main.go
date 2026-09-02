package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/bhodgens/meept-bench/internal/daemonclient"
	"github.com/bhodgens/meept-bench/internal/diff"
	"github.com/bhodgens/meept-bench/internal/runner"
	"github.com/bhodgens/meept-bench/internal/scorecard"
	"github.com/bhodgens/meept-bench/internal/suite"
	"github.com/bhodgens/meept-bench/internal/version"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version":
		fmt.Println(version.String())
	case "doctor":
		doctor()
	case "run":
		run(os.Args[2:])
	case "scorecard":
		scorecardCmd(os.Args[2:])
	case "diff":
		diffCmd(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Println(`meept-bench — benchmark harness for the meept agent daemon

Usage:
  meept-bench version                       print build identity
  meept-bench doctor                        check daemon connectivity + worktree tooling
  meept-bench run --suite FILE [flags]      execute a suite manifest
  meept-bench scorecard RESULTS.jsonl       generate markdown+JSON scorecard
  meept-bench diff --baseline FILE --current FILE
                                            compare two results.jsonl runs

Run flags:
  --suite FILE        suite manifest (required)
  --task SUBSTR       only tasks whose ID contains SUBSTR
  --attempts N        attempts per task (default 1)
  --model ALIAS       override model (exercises meept model reassignment)
  --repo PATH         repo to fork task worktrees from (default cwd)
  --scratch PATH      worktree scratch root (default /tmp/meept-bench)
  --out DIR           results output dir (default results/<suite>)
  --judge-cmd SPEC    external llm_judge command ("prog args...")
  --keep-failed       preserve failed-attempt worktrees for postmortems
  --rerun-failures    reserved: rerun only previously failed rows
  --auto-approved     disclose that approval gates ran without a human present

Diff flags:
  --baseline FILE     baseline results.jsonl (required)
  --current FILE      current results.jsonl (required)
  --json              emit Summary as JSON instead of text`)
}

func doctor() {
	c := daemonclient.NewDefault()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer c.Close()
	if err := c.Ping(ctx); err != nil {
		fmt.Printf("FAIL daemon ping (%s): %v\n", c.Path(), err)
		os.Exit(1)
	}
	st, err := c.Status(ctx)
	if err != nil {
		fmt.Printf("WARN ping ok but status failed: %v\n", err)
		return
	}
	data, _ := json.MarshalIndent(st, "", "  ")
	fmt.Printf("OK daemon reachable at %s\n%s\n", c.Path(), data)
}

func run(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	suitePath := fs.String("suite", "", "suite manifest path")
	taskFilter := fs.String("task", "", "task ID substring filter")
	attempts := fs.Int("attempts", 1, "attempts per task")
	model := fs.String("model", "", "model alias override")
	repo := fs.String("repo", "", "source repo for worktrees")
	scratch := fs.String("scratch", os.Getenv("HOME")+"/.meept-bench", "worktree scratch root (keep under ~ so daemon security allowlists like allowed_paths=[\"~/*\"] cover it)")
	out := fs.String("out", "", "results output dir")
	judgeCmd := fs.String("judge-cmd", os.Getenv("MEEPT_BENCH_JUDGE_CMD"), "external judge command")
	keepFailed := fs.Bool("keep-failed", false, "preserve failed attempt worktrees")
	autoApproved := fs.Bool("auto-approved", false, "disclose auto-approval mode")
	fs.Parse(args)

	if *suitePath == "" {
		fmt.Fprintln(os.Stderr, "run: --suite is required")
		os.Exit(2)
	}
	m, err := suite.Load(*suitePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "run: %v\n", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	r, err := runner.New(runner.Options{
		RepoPath:     *repo,
		ScratchRoot:  *scratch,
		Attempts:     *attempts,
		Model:        *model,
		JudgeCmd:     *judgeCmd,
		KeepFailed:   *keepFailed,
		AutoApproved: *autoApproved,
		OutDir:       *out,
		Logf:         func(f string, a ...any) { fmt.Printf(f+"\n", a...) },
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "run: %v\n", err)
		os.Exit(1)
	}

	outDir, rows, err := r.RunSuite(ctx, m, *taskFilter)
	if err != nil && len(rows) == 0 {
		fmt.Fprintf(os.Stderr, "run: %v\n", err)
		os.Exit(1)
	}
	sc, err := scorecard.Build(m.Suite, rows)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scorecard: %v\n", err)
		os.Exit(1)
	}
	mdPath, jsonPath, werr := scorecard.Write(outDir, sc)
	if werr == nil {
		fmt.Printf("scorecard: %s\n           %s\n", mdPath, jsonPath)
	}
}

func scorecardCmd(args []string) {
	fs := flag.NewFlagSet("scorecard", flag.ExitOnError)
	out := fs.String("out", "", "output dir (default alongside input)")
	fs.Parse(args)
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "scorecard: results.jsonl path required")
		os.Exit(2)
	}
	path := fs.Arg(0)
	rows, err := scorecard.LoadRows(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scorecard: %v\n", err)
		os.Exit(1)
	}
	var name string
	if len(rows) > 0 {
		name = rows[0].Suite
	}
	sc, err := scorecard.Build(name, rows)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scorecard: %v\n", err)
		os.Exit(1)
	}
	dir := *out
	if dir == "" {
		dir = dirOf(path)
	}
	mdPath, jsonPath, err := scorecard.Write(dir, sc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scorecard: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("scorecard: %s\n           %s\n", mdPath, jsonPath)
}

func diffCmd(args []string) {
	fs := flag.NewFlagSet("diff", flag.ExitOnError)
	baselinePath := fs.String("baseline", "", "baseline results.jsonl path (required)")
	currentPath := fs.String("current", "", "current results.jsonl path (required)")
	asJSON := fs.Bool("json", false, "emit Summary as JSON instead of text")
	fs.Parse(args)

	if *baselinePath == "" || *currentPath == "" {
		fmt.Fprintln(os.Stderr, "diff: --baseline and --current are required")
		os.Exit(2)
	}
	base, err := diff.LoadRows(*baselinePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "diff: %v\n", err)
		os.Exit(2)
	}
	cur, err := diff.LoadRows(*currentPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "diff: %v\n", err)
		os.Exit(2)
	}

	diffs := diff.Compare(base, cur)
	sum := diff.Summarize(diffs)
	if *asJSON {
		data, err := json.MarshalIndent(sum, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "diff: %v\n", err)
			os.Exit(2)
		}
		fmt.Println(string(data))
	} else {
		printDiffText(sum, diffs)
	}
	if len(sum.Regressed) > 0 {
		os.Exit(1)
	}
}

func printDiffText(sum diff.Summary, diffs []diff.TaskDiff) {
	list := func(ids []string) string {
		if len(ids) == 0 {
			return "(none)"
		}
		return strings.Join(ids, " ")
	}
	byID := make(map[string]diff.TaskDiff, len(diffs))
	for _, d := range diffs {
		byID[d.TaskID] = d
	}
	fmt.Printf("REGRESSED (pass→fail): %s\n", list(sum.Regressed))
	fmt.Printf("FIXED (fail→pass):     %s\n", list(sum.Fixed))
	fmt.Printf("NEW:                   %s\n", list(sum.New))
	fmt.Printf("REMOVED:               %s\n", list(sum.Removed))
	if len(sum.WallRegressed) > 0 {
		parts := make([]string, 0, len(sum.WallRegressed))
		for _, id := range sum.WallRegressed {
			d := byID[id]
			parts = append(parts, fmt.Sprintf("%s (%.1fs → %.1fs)", id, d.BaselineWall, d.CurrentWall))
		}
		fmt.Printf("wall-time deltas > +50%%: %s\n", strings.Join(parts, " "))
	}
	// Cost deltas line only when at least one non-zero delta exists.
	var costParts []string
	for _, d := range diffs {
		if d.CostDelta != 0 {
			costParts = append(costParts, fmt.Sprintf("%s ($%.4f → $%.4f)", d.TaskID, d.BaselineCost, d.CurrentCost))
		}
	}
	if len(costParts) > 0 {
		fmt.Printf("cost deltas:           %s\n", strings.Join(costParts, " "))
	}
}

func dirOf(path string) string {
	if d := filepath.Dir(path); d != "." && d != "" {
		return d
	}
	return "."
}
