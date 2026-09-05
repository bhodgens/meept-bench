// Package runner executes suite tasks against a live meept daemon.
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	DoubleJudge  bool // run llm_judge twice; flag disagreement in Result detail
	KeepFailed   bool
	AutoApproved bool
	RerunFailed  bool
	IgnoreTags   []string // tasks carrying ANY of these tags are excluded
	OutDir       string   // results/<suite>; created if empty
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

	tasks := m.Select(filter, r.opt.IgnoreTags)
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
		return r.finishErr(wt, row, m, t, attempt, "error", "daemon unreachable: "+err.Error(), nil)
	}
	cancelPing()

	// Deterministic tools gate (phase-2-3 P2.3): a task declaring
	// tools.cached_fetch must run against a daemon whose cached-fetch
	// mode is live (config [agent.tools].deterministic_tools=true or the
	// MEEPT_DETERMINISTIC_TOOLS env override). Verify via the daemon's
	// status disclosure BEFORE the chat so a misconfigured environment
	// fails fast instead of after burning an LLM call.
	if t.Tools != nil && t.Tools.CachedFetch {
		gateCtx, cancelGate := context.WithTimeout(ctx, 5*time.Second)
		st, err := client.Status(gateCtx)
		cancelGate()
		if err != nil {
			return r.finishErr(wt, row, m, t, attempt, "error",
				"deterministic gate: status check failed: "+err.Error(), nil)
		}
		if on, _ := st["deterministic_tools"].(bool); !on {
			return r.finishErr(wt, row, m, t, attempt, "error",
				"deterministic gate: task requires tools.cached_fetch but daemon reports "+
					"deterministic_tools=false; set [agent.tools].deterministic_tools=true in the daemon "+
					"config (or MEEPT_DETERMINISTIC_TOOLS=1 in the daemon env) and set "+
					"MEEPT_TOOL_CACHE_DIR to the fixtures directory", nil)
		}
		row.DeterministicFetch = true
	}

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

	// Subscribe to tool progress + completion + chat_message + agent
	// lifecycle events for the trace. tool.execution.complete carries the
	// success flag per tool call, which detectPendingWrites correlates
	// against in-flight writes. agent.lifecycle.started carries the
	// RESOLVED thread conversation ID the loop actually runs under — the
	// daemon's thread router may remap conv-… to conv-…-thread-<topic>-…,
	// and steering queues register under that resolved ID.
	subCtx, cancelSub := context.WithTimeout(ctx, 10*time.Second)
	sub, err := client.Subscribe(subCtx, []string{
		"tool.execution.progress", "tool.execution.complete", "chat_message",
		"agent.lifecycle.started",
	})
	cancelSub()
	if err != nil {
		return r.finishErr(wt, row, m, t, attempt, "error", "bus subscribe: "+err.Error(), nil)
	}
	transcript := &results.Transcript{
		Suite: m.Suite, TaskID: t.ID, Attempt: attempt, Seed: seed,
		Prompt: t.Prompt, StartedAt: start,
	}
	done := make(chan struct{})
	// resolvedConv receives the thread conversation ID the daemon actually
	// ran the primary turn under, once the lifecycle event arrives.
	resolvedConv := make(chan string, 4)
	go sub.Collect(ctx, 250*time.Millisecond, done, func(evts []daemonclient.Event) {
		for _, e := range evts {
			transcript.ToolTrace = append(transcript.ToolTrace, results.ToolEvent{
				At: e.Timestamp, Topic: e.Topic, Type: e.Type, Source: e.Source, Raw: e.Payload,
			})
			if e.Topic == "agent.lifecycle.started" {
				var p struct {
					ConversationID string `json:"conversation_id"`
				}
				if json.Unmarshal(e.Payload, &p) == nil && p.ConversationID != "" &&
					p.ConversationID != conversationID {
					select {
					case resolvedConv <- p.ConversationID:
					default:
					}
				}
			}
		}
	})

	// Snapshot daemon status before the chat so per-task cost can be
	// derived as a before/after delta. The raw status values are daemon-wide
	// cumulative counters; subtracting two snapshots isolates this task's
	// usage even though concurrent tasks make the split approximate.
	snapCtx, cancelSnap := context.WithTimeout(ctx, 5*time.Second)
	beforeStatus, beforeErr := client.Status(snapCtx)
	cancelSnap()
	if beforeErr != nil && r.opt.Logf != nil {
		r.opt.Logf("warning: pre-chat status unavailable (%v); cost will be totals, not delta", beforeErr)
	}

	chatCtx, cancelChat := context.WithTimeout(ctx, t.Timeout())
	defer cancelChat()
	// The daemon resolves the session (and its project binding) by
	// conversation ID, so chat with conv-…, not session-….
	promptSentAt := time.Now()

	// Multi-turn steering (P2.4): deliver scheduled follow-up turns while
	// the primary agent run is in flight. Turns go over a forked
	// connection because Chat serializes the primary connection's mutex
	// for the whole agent round-trip — a turn RPC on the same connection
	// would only land after the reply, defeating the point of steering.
	var turnEvents []results.TurnEvent
	var turnWait func()
	turnCtx, cancelTurns := context.WithCancel(chatCtx)
	defer cancelTurns()
	if len(t.Turns) > 0 {
		steerSvr := client.Fork()
		defer steerSvr.Close()
		// steerTarget resolves to the thread conversation ID once the
		// daemon publishes the loop's lifecycle event; until then turns
		// try the session conversation ID first (works on legacy
		// daemon configs with the thread router disabled).
		steerTarget := &steerTarget{conversationID: conversationID, resolved: resolvedConv}
		turnEvents, turnWait = r.scheduleTurns(turnCtx, steerSvr, steerTarget, t.Turns, promptSentAt)
	}

	resp, chatErr := client.Chat(chatCtx, t.Prompt, conversationID)
	close(done)
	_ = sub.Unsubscribe(context.Background())

	// Give in-flight turn deliveries a bounded grace window to record
	// their outcome, then force-cancel stragglers (e.g. a fallback chat
	// turn still being answered). Whatever happened lands in the
	// transcript, including deliveries that never got accepted.
	if len(turnEvents) > 0 {
		wgDone := make(chan struct{})
		go func() {
			turnWait()
			close(wgDone)
		}()
		select {
		case <-wgDone:
		case <-time.After(turnGraceWindow):
			r.opt.Logf("steering: turn delivery still pending %s after the primary reply; cancelling", turnGraceWindow)
			cancelTurns()
			<-wgDone
		}
		accepted := 0
		for _, ev := range turnEvents {
			if ev.Accepted {
				accepted++
				r.opt.Logf("steering: turn accepted via %s (delay=%ds): %.60s", ev.PathUsed, ev.DelayS, ev.Message)
			} else if ev.Error != "" {
				r.opt.Logf("steering: turn NOT accepted (path=%s): %s", ev.PathUsed, ev.Error)
			}
		}
		r.opt.Logf("steering: %d/%d turn(s) accepted", accepted, len(turnEvents))
		transcript.Turns = turnEvents

		// A turn dispatched via the chat fallback becomes its own agent
		// run on the daemon, often finishing AFTER the primary reply.
		// The checkers inspect the worktree, so give any turn-driven run
		// a bounded window to finish before checking: wait until the
		// derived thread conversation's loop reports lifecycle.ended (or
		// the window expires).
		if accepted > 0 {
			r.waitForTurnRuns(ctx, sub, client, conversationID, turnGraceWindow, promptSentAt)
		}
	}

	row.WallSeconds = time.Since(start).Seconds()
	if chatErr != nil {
		if ctx.Err() != nil || isDeadline(chatCtx) {
			return r.finishErr(wt, row, m, t, attempt, "timeout", chatErr.Error(), turnEvents)
		}
		return r.finishErr(wt, row, m, t, attempt, "error", "chat: "+chatErr.Error(), turnEvents)
	}
	if resp.Error != "" {
		return r.finishErr(wt, row, m, t, attempt, "error", "agent: "+resp.Error, turnEvents)
	}
	transcript.FinalReply = resp.Reply
	transcript.EndedAt = time.Now()

	// Routing assertion: ask the daemon which agent handled this
	// conversation and record it in the transcript. When the suite
	// declares expect_agent and the daemon reports a different agent,
	// fail the row with a routing-mismatch check result — the task may
	// have been executed by an agent without the tools/skills it needed.
	agentCtx, cancelAgent := context.WithTimeout(ctx, 5*time.Second)
	routed, agentErr := client.DispatchedAgent(agentCtx, conversationID)
	cancelAgent()
	if agentErr != nil && r.opt.Logf != nil {
		r.opt.Logf("warning: dispatch trace unavailable (%v); routing not asserted", agentErr)
	}
	transcript.RoutedAgent = routed
	// Classification-method provenance for the same dispatch decision:
	// which classifier produced the routing (capability_matcher, llm,
	// keyword, semantic, heuristic_fallback, …). Recorded for regression
	// tracking across daemon upgrades; empty means unknown.
	methodCtx, cancelMethod := context.WithTimeout(ctx, 5*time.Second)
	method, methodErr := client.ClassificationMethod(methodCtx, conversationID)
	cancelMethod()
	if methodErr != nil && r.opt.Logf != nil {
		r.opt.Logf("warning: classification method unavailable (%v)", methodErr)
	}
	transcript.ClassificationMethod = method
	if t.ExpectAgent != "" && routed != "" && routed != t.ExpectAgent {
		r.writeTranscript(name, transcript)
		return r.finishErr(wt, row, m, t, attempt, "fail",
			fmt.Sprintf("routing mismatch: expect_agent=%s routed=%s", t.ExpectAgent, routed), nil)
	}

	r.writeTranscript(name, transcript)

	// Checkers.
	passed := true
	checkResults := make([]any, 0, len(t.Checkers))
	for _, c := range t.Checkers {
		res := checkers.Run(ctx, c, wt.Path, resp.Reply, r.judge, r.runCheckOpts()...)
		checkResults = append(checkResults, res)
		if !res.Passed {
			passed = false
		}
	}
	row.Checks = checkResults
	row.Passed = passed
	row.Verdict = map[bool]string{true: "pass", false: "fail"}[passed]

	// Surface staged (pending-change) writes on failures: the agent may have
	// staged a file write for review instead of writing to disk, so the file
	// is absent from the worktree and the failure looks mysterious. Only
	// annotate failing rows — passing rows don't need the diagnosis.
	if !passed {
		if pending := detectPendingWrites(transcript); len(pending) > 0 {
			// Append to the Detail of failing file checkers (exact_file,
			// file_contains); for other check types add a synthetic
			// "pending_writes" result so scorecards still show WHY.
			for i, c := range t.Checkers {
				isFileCheck := c.Type == "exact_file" || c.Type == "file_contains"
				if isFileCheck && !passedCheck(checkResults[i]) {
					checkResults[i] = appendDetail(checkResults[i],
						"possible cause: "+strings.Join(pending, "; "))
				}
			}
			row.Checks = append(checkResults, map[string]any{
				"check":  "pending_writes",
				"passed": false,
				"detail": "agent staged " + fmt.Sprint(len(pending)) +
					" write(s) as pending changes instead of writing directly; " +
					"they were never accepted: " + strings.Join(pending, "; "),
			})
			row.Passed = passed
		}
	}

	// Per-task cost via before/after status delta. `status` exposes
	// daemon-wide cumulative counters (tokens_used = hourly tokens,
	// daily_cost_used = daily cost), so the subtraction isolates what this
	// task actually consumed. When the pre-chat snapshot failed we fall
	// back to the raw totals — wrong attribution, but better than zero.
	if afterStatus, err := client.Status(ctx); err == nil {
		tokensIn := statusTokens(afterStatus)
		costUSD := statusCost(afterStatus)
		if beforeErr == nil {
			tokensIn -= statusTokens(beforeStatus)
			costUSD -= statusCost(beforeStatus)
			if tokensIn < 0 {
				tokensIn = 0 // counters reset (daily rollover) mid-task
			}
			if costUSD < 0 {
				costUSD = 0
			}
		}
		row.TokensIn = tokensIn
		row.CostUSD = costUSD
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

// turnGraceWindow bounds how long the runner waits after the primary reply
// for still-pending turn deliveries before force-cancelling them.
const turnGraceWindow = 90 * time.Second

// turnDeliveryTimeout bounds each individual steering/follow-up/chat RPC so a
// hung daemon can't wedge a turn goroutine past the task deadline.
const turnDeliveryTimeout = 15 * time.Second

// chatFallbackWindow bounds the plain-chat delivery path. Chat normally waits
// proxyWindow+graceWindow (over two minutes) for a reply; a scheduled turn
// must fail fast instead of pinning transcript assembly.
const chatFallbackWindow = 30 * time.Second

// steerTarget tracks which conversation ID the daemon's steering queues live
// under. The thread router may remap the session conversation ID
// (conv-…) to a thread conversation ID (conv-…-thread-<topic>-…), and the
// agent loop registers its steering queue under the resolved ID. The
// lifecycle watcher feeds the resolved ID in as soon as the daemon announces
// the loop; until then callers fall back to the session conversation ID.
type steerTarget struct {
	conversationID string        // session-level conversation ID (fallback)
	resolved       <-chan string // resolved thread conversation ID, if any
	once           sync.Once     // resolve only once
	resolvedID     string        // cached resolution result ("" = none)
}

// ids returns conversation IDs to try for steering, best-first: the resolved
// thread ID from the lifecycle event (loop conv under thread routing), the
// deterministic default-thread derivation, then the raw session conversation
// ID (legacy daemons without thread routing). Callers should fall back to
// later IDs when the daemon reports "queue not found" for earlier ones.
func (st *steerTarget) ids() []string {
	if st == nil {
		return nil
	}
	var out []string
	if st.resolved != nil {
		st.once.Do(func() {
			select {
			case st.resolvedID = <-st.resolved:
			default:
			}
		})
		if st.resolvedID != "" {
			out = append(out, st.resolvedID)
		}
	}
	if derived := deriveThreadConv(st.conversationID); derived != "" {
		out = append(out, derived)
	}
	out = append(out, st.conversationID)
	return out
}

// deriveThreadConv deterministically derives meept's default-thread
// conversation ID from a session conversation ID
// ("conv-<hex>" → "conv-<hex>-thread-general-<last4>"). Returns "" for IDs
// that don't look like session conversation IDs (already-derived thread IDs,
// test strings).
func deriveThreadConv(conversationID string) string {
	if !strings.HasPrefix(conversationID, "conv-") || strings.Contains(conversationID, "-thread-") {
		return ""
	}
	suffix := conversationID
	if len(suffix) > 4 {
		suffix = suffix[len(suffix)-4:]
	}
	return conversationID + "-thread-general-" + suffix
}

// scheduleTurns launches one goroutine per scheduled turn. Each goroutine
// waits delay_s after the primary prompt was sent, then delivers its message
// on the steering conversation: first via the daemon's steering RPC (mid-run
// interruption), then via the follow-up RPC (natural-stop delivery), and
// finally — when both queues are gone, i.e. the agent already finished — via
// a plain chat on the same conversation ID. The outcome of every delivery is
// recorded in the returned TurnEvents.
//
// It exists because meept exposes real steering RPCs (chat.steer /
// chat.followup, internal/rpc/queue.go); the fallback path is only for the
// benign race where the agent completed before the scheduled turn fired.
//
// The returned wait function blocks until every turn goroutine has finished
// (success, error, or context cancellation).
func (r *Runner) scheduleTurns(sctx context.Context, steerSvr *daemonclient.Client, target *steerTarget, turns []suite.Turn, promptSentAt time.Time) ([]results.TurnEvent, func()) {
	events := make([]results.TurnEvent, len(turns))
	var wg sync.WaitGroup
	for i := range turns {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			turn := turns[i]
			ev := &events[i]
			ev.Message = turn.Message
			ev.DelayS = turn.DelayS

			// Wait out the delay relative to the primary prompt send,
			// unless the primary chat has already finished/cancelled.
			if turn.DelayS > 0 {
				deadline := promptSentAt.Add(time.Duration(turn.DelayS) * time.Second)
				timer := time.NewTimer(time.Until(deadline))
				defer timer.Stop()
				select {
				case <-timer.C:
				case <-sctx.Done():
					ev.Error = "cancelled before delivery: " + sctx.Err().Error()
					return
				}
			}

			// Deliver via steering → follow-up → chat fallback. Steering
			// and follow-up are attempted against every candidate
			// conversation ID (resolved thread ID, deterministic
			// derivation, raw session ID) because the daemon registers
			// the active queue under whichever ID its dispatcher chose.
			// Each attempt gets a short window so a hung daemon can't
			// wedge the runner past the task deadline.
			steer := func(c context.Context, convID string) error {
				return steerSvr.Steer(c, convID, turn.Message)
			}
			followup := func(c context.Context, convID string) error {
				return steerSvr.FollowUp(c, convID, turn.Message)
			}
			chat := func(c context.Context, convID string) error {
				// Chat's bus-fallback path waits proxyWindow +
				// graceWindow for a reply; bound it so a turn that
				// can't be delivered can't wedge the runner.
				chatCtx, cancelChat := context.WithTimeout(c, chatFallbackWindow)
				defer cancelChat()
				resp, err := steerSvr.Chat(chatCtx, turn.Message, convID)
				if err != nil {
					return err
				}
				if resp != nil && resp.Error != "" {
					return fmt.Errorf("agent: %s", resp.Error)
				}
				return nil
			}

			// candidate returns the send function for the next untried
			// conversation ID on the given path, or nil when all IDs
			// have been tried.
			candidateIDs := target.ids()
			triedIdx := map[string]int{}
			nextCandidate := func(path string, send func(context.Context, string) error) func(context.Context) error {
				return func(c context.Context) error {
					i := triedIdx[path]
					if i >= len(candidateIDs) {
						return errCandidatesExhausted
					}
					triedIdx[path] = i + 1
					r.opt.Logf("steering: %s → %s", path, candidateIDs[i])
					return send(c, candidateIDs[i])
				}
			}

			steerSend := nextCandidate("steer", steer)
			followupSend := nextCandidate("followup", followup)

			for {
				callCtx, cancel := context.WithTimeout(sctx, turnDeliveryTimeout)
				err := steerSend(callCtx)
				cancel()
				ev.SentAt = time.Now()
				ev.PathUsed = "steer"
				switch {
				case err == nil:
					ev.Accepted = true
					return
				case errors.Is(err, errCandidatesExhausted):
					// All steering IDs tried without an active queue.
				case sctx.Err() != nil:
					ev.Error = "delivery aborted: " + sctx.Err().Error()
					return
				case isQueueGone("steer", err):
					ev.Error = fmt.Sprintf("steer: %v", err)
					continue
				default:
					// Non-queue error (dial, params): steering is not
					// going to work at all; fall through to follow-up.
					ev.Error = fmt.Sprintf("steer: %v", err)
				}
				break
			}

			for {
				callCtx, cancel := context.WithTimeout(sctx, turnDeliveryTimeout)
				err := followupSend(callCtx)
				cancel()
				ev.SentAt = time.Now()
				ev.PathUsed = "followup"
				switch {
				case err == nil:
					ev.Accepted = true
					return
				case errors.Is(err, errCandidatesExhausted):
				case sctx.Err() != nil:
					ev.Error = "delivery aborted: " + sctx.Err().Error()
					return
				case isQueueGone("followup", err):
					ev.Error = fmt.Sprintf("followup: %v", err)
					continue
				default:
					ev.Error = fmt.Sprintf("followup: %v", err)
				}
				break
			}

			// Final fallback: plain chat on the raw session conversation
			// ID — the daemon dispatches it as a new user turn (steering
			// into the loop's queue when one is active, a new turn when
			// not). meept's proxy enforces a 120s server-side reply
			// window: a timeout here means "dispatched, reply pending",
			// NOT "message dropped", so treat it as accepted.
			callCtx, cancel := context.WithTimeout(sctx, chatFallbackWindow)
			err := chat(callCtx, target.conversationID)
			cancel()
			ev.SentAt = time.Now()
			ev.PathUsed = "chat"
			if err == nil || isChatDispatchTimeout(err) {
				ev.Accepted = true
				if err != nil {
					ev.Error = "reply pending (dispatch accepted): " + err.Error()
				} else {
					ev.Error = ""
				}
				return
			}
			ev.Error = fmt.Sprintf("chat: %v", err)
		}(i)
	}
	return events, wg.Wait
}

// isChatDispatchTimeout reports whether err from the chat fallback means the
// message WAS dispatched but the reply window elapsed. meept's chat proxy
// times out waiting for the response while the agent keeps working.
func isChatDispatchTimeout(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "context deadline exceeded") ||
		strings.Contains(msg, "timeout waiting for response")
}

// errCandidatesExhausted signals that every candidate conversation ID for a
// delivery path has been tried and rejected with queue-gone.
var errCandidatesExhausted = errors.New("all candidate conversation IDs exhausted")

// waitForTurnRuns blocks (bounded by window) until any turn-driven agent run
// finishes. When a scheduled turn is delivered via the plain-chat fallback,
// the daemon runs it as its own async task that completes AFTER the primary
// reply; checkers read the worktree, so racing them against that run produces
// false failures. Two completion signals are used, whichever comes first:
//
//   - a chat_message event for this session conversation (the daemon pushes
//     the turn task's result to chat on completion), or
//   - the derived thread conversation's queue staying inactive for a quiet
//     period of consecutive polls.
//
// The wait starts from turnDeliveredAt so in-flight dispatch time doesn't eat
// the quiet-period budget.
func (r *Runner) waitForTurnRuns(ctx context.Context, sub *daemonclient.Subscription, client *daemonclient.Client, conversationID string, window time.Duration, turnDeliveredAt time.Time) {
	derived := deriveThreadConv(conversationID)
	deadline := time.NewTimer(window)
	defer deadline.Stop()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	const quietPolls = 3 // 3 × 2s = 6s of continuous queue inactivity
	quiet := 0
	r.opt.Logf("steering: waiting up to %s for turn run to finish", window)
	for {
		select {
		case <-deadline.C:
			r.opt.Logf("steering: turn-run wait window expired")
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Signal 1: the turn task's result chat_message.
			if evts, err := sub.Poll(ctx); err == nil {
				for _, e := range evts {
					if e.Topic != "chat_message" || e.Timestamp.Before(turnDeliveredAt) {
						continue
					}
					var p struct {
						Role           string `json:"role"`
						ConversationID string `json:"conversation_id"`
					}
					if json.Unmarshal(e.Payload, &p) == nil &&
						p.Role == "assistant" && p.ConversationID == conversationID {
						r.opt.Logf("steering: turn run finished (result pushed to chat)")
						return
					}
				}
			}
			// Signal 2: quiet queue on the derived thread conv.
			if derived != "" {
				sCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				_, _, active, err := client.QueueStatus(sCtx, derived)
				cancel()
				if err != nil || active {
					quiet = 0
					continue
				}
				quiet++
				if quiet >= quietPolls {
					r.opt.Logf("steering: turn run finished (queue quiet %ds)", quiet*2)
					return
				}
			}
		}
	}
}

// isQueueGone reports whether err means "no active queue for this
// conversation" — the daemon's signal that the agent isn't running (or just
// finished), so the next delivery path should be tried. Mirrors meept's
// agent.ErrQueueNotFound / ErrQueueClosed wording.
func isQueueGone(path string, err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	switch path {
	case "steer", "followup":
		return strings.Contains(msg, "queue not found") ||
			strings.Contains(msg, "queue is closed") ||
			strings.Contains(msg, "steer failed") && strings.Contains(msg, "queue") ||
			strings.Contains(msg, "follow-up failed") && strings.Contains(msg, "queue")
	default:
		return false
	}
}

// runCheckOpts builds the checker RunOptions from runner flags.
func (r *Runner) runCheckOpts() []checkers.RunOption {
	var opts []checkers.RunOption
	if r.opt.DoubleJudge {
		opts = append(opts, checkers.WithDoubleJudge())
	}
	return opts
}

func (r *Runner) finishErr(wt *isolate.Worktree, row *results.Row, m *suite.Manifest, t suite.Task, attempt int, kind, detail string, turns []results.TurnEvent) *results.Row {
	row.Verdict = kind
	row.Passed = false
	row.ErrorKind = kind
	row.ErrorDetail = detail
	tr := &results.Transcript{
		Suite: m.Suite, TaskID: t.ID, Attempt: attempt, Seed: row.Seed,
		Prompt: t.Prompt, Turns: turns, Error: detail,
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

// statusTokens extracts the daemon's cumulative hourly token counter from a
// status map. Top-level first, then the nested budget map.
func statusTokens(st map[string]any) int64 {
	if n, ok := toInt(st["tokens_used"]); ok {
		return n
	}
	if b, ok := st["budget"].(map[string]any); ok {
		n, _ := toInt(b["hourly_used"])
		return n
	}
	return 0
}

// statusCost extracts the daemon's cumulative daily cost (USD) from a
// status map. Prefers the real cost counter (daily_cost_used, both top-level
// and under budget); daily_used (tokens, not USD) is a legacy fallback.
func statusCost(st map[string]any) float64 {
	if c, ok := toFloat(st["daily_cost_used"]); ok {
		return c
	}
	if b, ok := st["budget"].(map[string]any); ok {
		if c, ok := toFloat(b["daily_cost_used"]); ok {
			return c
		}
		c, _ := toFloat(b["daily_used"])
		return c
	}
	return 0
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

// maxPendingWriteDetails caps the diagnostic noise per failing row.
const maxPendingWriteDetails = 5

// detectPendingWrites scans the tool trace for file_write/file_edit tool
// calls that staged a pending change instead of writing to disk, returning
// one human-readable detail string per offending call (capped at
// maxPendingWriteDetails).
//
// Two signals are used, in order of reliability:
//
//  1. Correlation: the daemon's WriteFileTool emits a "writing …" progress
//     (percent 10) for every write, but only the direct path reaches the
//     "write complete" progress (percent 100) — the staged path returns
//     right after creating the pending change. A file_write call that
//     started but never completed therefore staged its write.
//  2. Payload text: the sentinel result text ("Created pending change …",
//     "use 'resolve' tool") or arguments carrying direct:false / no direct
//     flag, whenever the payload shape includes them.
//
// All payload shapes are handled defensively: any decode failure simply
// yields no detail for that event, never a panic.
func detectPendingWrites(transcript *results.Transcript) []string {
	if transcript == nil {
		return nil
	}

	type callState struct {
		tool     string
		startMsg string
		progress bool // saw a completion progress ("write complete", percent 100)
		staged   bool // sentinel text / direct-flag evidence
		detail   string
	}
	calls := map[string]*callState{}
	var order []string // tool_call_ids in first-seen order, for stable output

	record := func(id string) *callState {
		if id == "" {
			return nil
		}
		c, ok := calls[id]
		if !ok {
			c = &callState{}
			calls[id] = c
			order = append(order, id)
		}
		return c
	}

	for _, ev := range transcript.ToolTrace {
		if len(ev.Raw) == 0 {
			continue
		}
		var p toolProgressPayload
		if err := json.Unmarshal(ev.Raw, &p); err != nil {
			continue // unknown shape: conservative, skip
		}
		if !isFileMutationTool(p.ToolName) {
			continue
		}
		c := record(p.ToolCallID)
		if c != nil {
			c.tool = p.ToolName
		}

		// Signal 1: completion progress on the streaming path.
		msg := strings.ToLower(p.Message)
		if p.Percent >= 100 || strings.Contains(msg, "complete") {
			if c != nil {
				c.progress = true
			}
			continue
		}
		// Remember the start message; it carries the target path.
		if c != nil && c.startMsg == "" && strings.HasPrefix(msg, "writing ") {
			c.startMsg = p.Message
		}

		// Signal 2: sentinel text or direct-flag evidence.
		if d := pendingWriteDetail(p); d != "" && (c == nil || c.detail == "") {
			if c != nil {
				c.staged = true
				c.detail = d
			}
		}
	}

	var out []string
	emit := func(d string) {
		if d != "" && len(out) < maxPendingWriteDetails {
			out = append(out, d)
		}
	}
	// Explicit evidence first (sentinel text / direct:false).
	for _, id := range order {
		c := calls[id]
		if c.staged && c.detail != "" {
			emit(c.detail)
		}
	}
	// Then inference: file_write calls that started but never completed.
	for _, id := range order {
		c := calls[id]
		if c.staged || c.progress || strings.Contains(strings.ToLower(c.tool), "file_edit") {
			continue
		}
		emit(fmt.Sprintf("%s: write to %s started but never completed — staged as a pending change instead of writing to disk (use direct:true to write immediately)",
			c.tool, pathFromStartMessage(c.startMsg)))
	}
	return out
}

// toolProgressPayload mirrors the fields the daemon puts on
// tool.execution.progress / tool.execution.complete payloads, plus optional
// argument fields some shapes carry. Everything is optional; absent fields
// simply don't contribute evidence.
type toolProgressPayload struct {
	ToolCallID    string          `json:"tool_call_id"`
	ToolName      string          `json:"tool_name"`
	Tool          string          `json:"tool"` // alternative key used by some shapes
	Message       string          `json:"message"`
	Status        string          `json:"status"`
	Detail        string          `json:"detail"`
	Result        string          `json:"result"`
	Percent       float64         `json:"percent"`
	ArgsSummary   json.RawMessage `json:"args_summary"`
	Arguments     json.RawMessage `json:"arguments"`
	PartialResult json.RawMessage `json:"partial_result"`
}

func isFileMutationTool(name string) bool {
	if name == "" {
		return false
	}
	n := strings.ToLower(name)
	return strings.Contains(n, "file_write") || strings.Contains(n, "file_edit")
}

// pendingWriteDetail returns "" unless the payload itself carries staged-
// write evidence: the daemon's sentinel result text or file-tool arguments
// with direct:false / direct absent.
func pendingWriteDetail(p toolProgressPayload) string {
	summary := strings.ToLower(strings.Join([]string{p.Message, p.Status, p.Detail, p.Result}, " "))
	if strings.Contains(summary, "pending change") ||
		strings.Contains(summary, "pending_change_created") ||
		strings.Contains(summary, "use 'resolve' tool") {
		return fmt.Sprintf("%s: %s", p.ToolName, firstNonEmpty(p.Result, p.Detail, p.Message))
	}

	// Fall back to the direct flag in the call arguments, when the payload
	// carries them. direct absent is treated as staged because the daemon
	// defaults to staging whenever the pending-changes registry is wired.
	raw := firstNonEmptyJSON(p.Arguments, p.ArgsSummary)
	if len(raw) > 0 {
		var args struct {
			Direct *bool `json:"direct"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			return "" // malformed args: no evidence either way
		}
		switch {
		case args.Direct != nil && !*args.Direct:
			return fmt.Sprintf("%s: direct=false (write staged for review, not applied)", p.ToolName)
		case args.Direct == nil:
			return fmt.Sprintf("%s: no direct flag (write may be staged for review)", p.ToolName)
		}
	}
	return ""
}

// pathFromStartMessage extracts the target path from a "writing <path>
// (N bytes)..." progress message, falling back to the raw message.
func pathFromStartMessage(msg string) string {
	if msg == "" {
		return "(unknown path)"
	}
	s := strings.TrimPrefix(msg, "writing ")
	if i := strings.Index(s, " ("); i != -1 {
		s = s[:i]
	}
	return strings.TrimSuffix(strings.TrimSpace(s), "...")
}

// passedCheck reports whether a checker result (checkers.Result or a map,
// depending on how it was stored) passed.
func passedCheck(res any) bool {
	switch v := res.(type) {
	case checkers.Result:
		return v.Passed
	case *checkers.Result:
		return v != nil && v.Passed
	case map[string]any:
		b, _ := v["passed"].(bool)
		return b
	default:
		return true // unknown shape: don't annotate
	}
}

// appendDetail appends to a checker result's Detail, returning the updated
// result. Unknown shapes are returned unchanged.
func appendDetail(res any, extra string) any {
	switch v := res.(type) {
	case checkers.Result:
		v.Detail = strings.TrimSpace(v.Detail + " " + extra)
		return v
	case *checkers.Result:
		if v != nil {
			v.Detail = strings.TrimSpace(v.Detail + " " + extra)
		}
		return v
	case map[string]any:
		d, _ := v["detail"].(string)
		v["detail"] = strings.TrimSpace(d + " " + extra)
		return v
	default:
		return res
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstNonEmptyJSON(vals ...json.RawMessage) json.RawMessage {
	for _, v := range vals {
		if len(v) > 0 {
			return v
		}
	}
	return nil
}
