// Package runner executes suite tasks against a live meept daemon.
package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bhodgens/meept-bench/internal/checkers"
	"github.com/bhodgens/meept-bench/internal/daemonclient"
	"github.com/bhodgens/meept-bench/internal/isolate"
	"github.com/bhodgens/meept-bench/internal/results"
	"github.com/bhodgens/meept-bench/internal/suite"
)

// Options configures a run.
type Options struct {
	RepoPath     string // repo the daemon operates in; worktrees fork from it
	ScratchRoot  string
	Attempts     int
	Model        string // exercises meept model reassignment when set
	JudgeCmd     string
	KeepFailed   bool
	AutoApproved bool
	RerunFailed  bool
	OutDir       string // results/<suite>; created if empty
	Logf         func(format string, args ...any)
}

// Runner drives tasks.
type Runner struct {
	opt    Options
	mgr    *isolate.Manager
	judge  checkers.Judge
	outDir string
}

// New builds a Runner.
func New(opt Options) (*Runner, error) {
	if opt.Logf == nil {
		opt.Logf = func(string, ...any) {}
	}
	if opt.Attempts < 1 {
		opt.Attempts = 1
	}
	if opt.RepoPath == "" {
		var err error
		opt.RepoPath, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	mgr, err := isolate.NewManager(opt.ScratchRoot)
	if err != nil {
		return nil, err
	}
	var judge checkers.Judge
	if opt.JudgeCmd != "" {
		judge, err = checkers.NewCmdJudge(opt.JudgeCmd)
		if err != nil {
			return nil, err
		}
	}
	r := &Runner{opt: opt, mgr: mgr, judge: judge}
	return r, nil
}

// RunSuite executes every selected task and appends rows to results.jsonl.
func (r *Runner) RunSuite(ctx context.Context, m *suite.Manifest, filter string) (string, []results.Row, error) {
	outDir := r.opt.OutDir
	if outDir == "" {
		outDir = filepath.Join("results", sanitize(m.Suite))
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", nil, err
	}

	tasks := m.Select(filter)
	rows := make([]results.Row, 0, len(tasks))
	for _, t := range tasks {
		for attempt := 1; attempt <= r.opt.Attempts; attempt++ {
			row := r.RunTask(ctx, m, t, attempt)
			if row != nil {
				if err := appendRow(filepath.Join(outDir, "results.jsonl"), *row); err != nil {
					return outDir, rows, err
				}
				rows = append(rows, *row)
				status := "PASS"
				if !row.Passed {
					status = "FAIL"
					if row.Verdict == "error" || row.Verdict == "timeout" {
						status = row.Verdict
					}
				}
				r.opt.Logf("%s %s/%d seed=%d cost=$%.4f wall=%.1fs",
					status, t.ID, attempt, row.Seed, row.CostUSD, row.WallSeconds)
			}
			select {
			case <-ctx.Done():
				return outDir, rows, ctx.Err()
			default:
			}
		}
	}
	return outDir, rows, nil
}

// RunTask performs one attempt of one task.
func (r *Runner) RunTask(ctx context.Context, m *suite.Manifest, t suite.Task, attempt int) *results.Row {
	start := time.Now()
	name := fmt.Sprintf("%s-%s-a%d", m.Suite, t.ID, attempt)

	wt, err := r.mgr.Create(r.opt.RepoPath, name)
	if err != nil {
		return errRow(m, t, attempt, start, "error", "worktree: "+err.Error())
	}
	seed := int64(0)
	if len(t.Seeds) > 0 {
		seed = t.Seeds[(attempt-1)%len(t.Seeds)]
	}
	row := &results.Row{
		Suite: m.Suite, TaskID: t.ID, Attempt: attempt, Seed: seed,
		Model: r.pickModel(m), HFRevision: m.HFRevision,
		StartedAt: start, AutoApproved: r.opt.AutoApproved,
	}
	defer func() {
		row.WallSeconds = time.Since(start).Seconds()
		row.WorktreeKept = wt.Keep
		if wt.Keep {
			row.WorktreePath = wt.Path
		}
	}()

	client := daemonclient.NewDefault()
	defer client.Close()

	pingCtx, cancelPing := context.WithTimeout(ctx, 5*time.Second)
	if err := client.Ping(pingCtx); err != nil {
		cancelPing()
		return r.finishErr(wt, row, m, t, attempt, "error", "daemon unreachable: "+err.Error())
	}
	cancelPing()

	// Register the worktree as a project AND bind it to a real daemon session
	// so the agent loop's tools resolve their working dir to the worktree
	// (meept resolves cwd from the session store; project.set fails with
	// "session not found" for IDs the daemon has never seen).
	regCtx, cancelReg := context.WithTimeout(ctx, 15*time.Second)
	_ = client.ProjectRegister(regCtx, name, name, wt.Path)
	var sessionID, conversationID string
	if ids, err := client.SessionCreate(regCtx); err == nil {
		sessionID = ids.SessionID
		conversationID = ids.ConversationID
		if err := client.Call(regCtx, "project.set", map[string]any{
			"session_id": sessionID,
			"path":       wt.Path,
		}, nil); err != nil {
			r.opt.Logf("project.set failed: %v (agent may run in the wrong dir)", err)
		}
	} else {
		r.opt.Logf("warning: session.create returned no ids; agent will have no project binding")
	}
	cancelReg()

	// Subscribe to tool progress + chat_message events for the trace.
	subCtx, cancelSub := context.WithTimeout(ctx, 10*time.Second)
	sub, err := client.Subscribe(subCtx, []string{"tool.execution.progress", "chat_message"})
	cancelSub()
	if err != nil {
		return r.finishErr(wt, row, m, t, attempt, "error", "bus subscribe: "+err.Error())
	}
	transcript := &results.Transcript{
		Suite: m.Suite, TaskID: t.ID, Attempt: attempt, Seed: seed,
		Prompt: t.Prompt, StartedAt: start,
	}
	done := make(chan struct{})
	go sub.Collect(ctx, 250*time.Millisecond, done, func(evts []daemonclient.Event) {
		for _, e := range evts {
			transcript.ToolTrace = append(transcript.ToolTrace, results.ToolEvent{
				At: e.Timestamp, Topic: e.Topic, Type: e.Type, Source: e.Source, Raw: e.Payload,
			})
		}
	})

	chatCtx, cancelChat := context.WithTimeout(ctx, t.Timeout())
	defer cancelChat()
	// The daemon resolves the session (and its project binding) by
	// conversation ID, so chat with conv-…, not session-….
	resp, chatErr := client.Chat(chatCtx, t.Prompt, conversationID)
	close(done)
	_ = sub.Unsubscribe(context.Background())

	row.WallSeconds = time.Since(start).Seconds()
	if chatErr != nil {
		if ctx.Err() != nil || isDeadline(chatCtx) {
			return r.finishErr(wt, row, m, t, attempt, "timeout", chatErr.Error())
		}
		return r.finishErr(wt, row, m, t, attempt, "error", "chat: "+chatErr.Error())
	}
	if resp.Error != "" {
		return r.finishErr(wt, row, m, t, attempt, "error", "agent: "+resp.Error)
	}
	transcript.FinalReply = resp.Reply
	transcript.EndedAt = time.Now()
	r.writeTranscript(name, transcript)

	// Checkers.
	passed := true
	checkResults := make([]any, 0, len(t.Checkers))
	for _, c := range t.Checkers {
		res := checkers.Run(ctx, c, wt.Path, resp.Reply, r.judge)
		checkResults = append(checkResults, res)
		if !res.Passed {
			passed = false
		}
	}
	row.Checks = checkResults
	row.Passed = passed
	row.Verdict = map[bool]string{true: "pass", false: "fail"}[passed]

	// Cost from the daemon status snapshot (budget tracker totals).
	if st, err := client.Status(ctx); err == nil {
		row.TokensIn, _ = toInt(st["tokens_used"])
		if b, ok := st["budget"].(map[string]any); ok {
			cost, _ := toFloat(b["daily_used"])
			row.CostUSD = cost
		}
	} else if r.opt.Logf != nil {
		r.opt.Logf("warning: cost unavailable (status call failed)")
	}

	if !passed && r.opt.KeepFailed {
		wt.Keep = true
	} else if err := wt.Teardown(); err != nil {
		r.opt.Logf("teardown %s: %v", name, err)
	}
	return row
}

func (r *Runner) pickModel(m *suite.Manifest) string {
	if r.opt.Model != "" {
		return r.opt.Model
	}
	return m.Model
}

func (r *Runner) finishErr(wt *isolate.Worktree, row *results.Row, m *suite.Manifest, t suite.Task, attempt int, kind, detail string) *results.Row {
	row.Verdict = kind
	row.Passed = false
	row.ErrorKind = kind
	row.ErrorDetail = detail
	tr := &results.Transcript{
		Suite: m.Suite, TaskID: t.ID, Attempt: attempt, Seed: row.Seed,
		Prompt: t.Prompt, Error: detail,
		StartedAt: row.StartedAt, EndedAt: time.Now(),
	}
	r.writeTranscript(fmt.Sprintf("%s-%s-a%d", m.Suite, t.ID, attempt), tr)
	if kind == "timeout" || r.opt.KeepFailed {
		wt.Keep = true
	} else if err := wt.Teardown(); err != nil {
		r.opt.Logf("teardown: %v", err)
	}
	return row
}

func (r *Runner) writeTranscript(name string, tr *results.Transcript) {
	dir := filepath.Join(r.outDirOr(), "transcripts")
	_ = os.MkdirAll(dir, 0o755)
	data, err := json.MarshalIndent(tr, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, sanitize(name)+".json"), data, 0o644)
}

func (r *Runner) outDirOr() string {
	if r.opt.OutDir != "" {
		return r.opt.OutDir
	}
	return "results"
}

func errRow(m *suite.Manifest, t suite.Task, attempt int, start time.Time, kind, detail string) *results.Row {
	return &results.Row{
		Suite: m.Suite, TaskID: t.ID, Attempt: attempt,
		Verdict: kind, Passed: false, ErrorKind: kind, ErrorDetail: detail,
		StartedAt: start, WallSeconds: time.Since(start).Seconds(),
	}
}

func appendRow(path string, row results.Row) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := json.Marshal(row)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	return err
}

func isDeadline(ctx context.Context) bool {
	return ctx.Err() == context.DeadlineExceeded
}

func toInt(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	default:
		return 0, false
	}
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	default:
		return 0, false
	}
}

func sanitize(s string) string {
	out := make([]rune, 0, len(s))
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' {
			out = append(out, c)
		} else {
			out = append(out, '-')
		}
	}
	return string(out)
}
