package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/bhodgens/meept-bench/internal/daemonclient"
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
  --auto-approved     disclose that approval gates ran without a human present`)
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
	scratch := fs.String("scratch", "/tmp/meept-bench", "worktree scratch root")
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

func dirOf(path string) string {
	if d := filepath.Dir(path); d != "." && d != "" {
		return d
	}
	return "."
}
