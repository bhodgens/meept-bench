package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bhodgens/meept-bench/internal/checkers"
	"github.com/bhodgens/meept-bench/internal/results"
	"github.com/bhodgens/meept-bench/internal/suite"
)

// RunTask is exercised over the real socket protocol, with real worktrees and
// an artifact written by the fake on chat.submit. No production daemon runs.
func TestRunTaskRoutingEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, entries, expectAgent, expectIntent, forbidden, verdict, terminal string
		unavailableRPC, createFailure                                          bool
	}{
		{name: "missing agent evidence", entries: `[]`, expectAgent: "coder", verdict: "error"},
		{name: "agent mismatch retains outcome", entries: `[{"session_id":"conv-route","agent_id":"writer","intent_type":"code","classifier_method":"llm"}]`, expectAgent: "coder", verdict: "fail"},
		{name: "all match", entries: `[{"session_id":"conv-route","agent_id":"coder","intent_type":"code","classifier_method":"llm"}]`, expectAgent: "coder", expectIntent: "code", forbidden: "short_message_guard", verdict: "pass"},
		{name: "forbidden method", entries: `[{"session_id":"conv-route","agent_id":"coder","intent_type":"code","classifier_method":"short_message_guard"}]`, forbidden: "short_message_guard", verdict: "fail"},
		{name: "intent mismatch", entries: `[{"session_id":"conv-route","agent_id":"coder","intent_type":"chat","classifier_method":"llm"}]`, expectIntent: "code", verdict: "fail"},
		{name: "unknown intent", entries: `[{"session_id":"conv-route","agent_id":"coder","classifier_method":"llm"}]`, expectIntent: "code", verdict: "error"},
		{name: "unknown method", entries: `[{"session_id":"conv-route","agent_id":"coder","intent_type":"code"}]`, forbidden: "guard", verdict: "error"},
		{name: "legacy outcome only", entries: `[]`, verdict: "pass"},
		{name: "RPC failure", entries: `[]`, expectAgent: "coder", unavailableRPC: true, verdict: "error"},
		{name: "freshness not established", entries: `[{"session_id":"conv-route","agent_id":"coder","intent_type":"code","classifier_method":"llm"}]`, expectAgent: "coder", createFailure: true, verdict: "error"},
		{name: "ambiguous", entries: `[{"session_id":"conv-route","agent_id":"coder"},{"session_id":"conv-route","agent_id":"writer"}]`, expectAgent: "coder", verdict: "error"},
		{name: "multi-turn association unavailable", entries: `[{"session_id":"conv-route","agent_id":"coder","intent_type":"code","classifier_method":"llm"}]`, expectAgent: "coder", verdict: "error"},
		{name: "submit transport error", terminal: "submit_error", verdict: "error"},
		{name: "terminal timeout", entries: `[]`, terminal: "timeout", verdict: "timeout"},
		{name: "terminal failed", entries: `[]`, terminal: "failed", verdict: "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sock, snapshots := routingFake(t, tc.entries, tc.unavailableRPC, tc.createFailure, tc.terminal)
			t.Setenv("MEEPT_BENCH_SOCKET", sock)
			repo, err := filepath.Abs("../..")
			if err != nil {
				t.Fatal(err)
			}
			out := t.TempDir()
			r, err := New(Options{RepoPath: repo, ScratchRoot: t.TempDir(), OutDir: out})
			if err != nil {
				t.Fatal(err)
			}
			task := suite.Task{ID: "route", Prompt: "produce the synthetic artifact", TimeoutS: 5, ExpectAgent: tc.expectAgent, ExpectIntent: tc.expectIntent, Checkers: []suite.Check{{Type: "file_contains", File: "route-artifact.txt", Pattern: "routing-test-marker"}}}
			if tc.name == "multi-turn association unavailable" {
				task.Turns = []suite.Turn{{DelayS: 60, Message: "second turn"}}
				task.TimeoutS = 1
			}
			if tc.forbidden != "" {
				task.ForbiddenClassificationMethods = []string{tc.forbidden}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			row := r.RunTask(ctx, &suite.Manifest{Suite: "routing"}, task, 1)
			if row.WorktreeKept {
				t.Cleanup(func() {
					if output, err := exec.Command("git", "-C", repo, "worktree", "remove", "--force", row.WorktreePath).CombinedOutput(); err != nil {
						t.Errorf("remove test worktree: %v: %s", err, output)
					}
				})
			}
			if row.Verdict != tc.verdict || row.Passed != (tc.verdict == "pass") {
				t.Errorf("verdict: got %+v want %s", row, tc.verdict)
			}
			if tc.terminal == "" {
				if len(row.Checks) != 1 {
					t.Errorf("completed turn must retain artifact check: %+v", row)
				} else if check, ok := row.Checks[0].(checkers.Result); !ok || !check.Passed {
					t.Errorf("artifact check = %+v", row.Checks)
				}
				if row.Routing == nil {
					t.Error("routing evidence status missing")
				}
				if tc.expectAgent != "" || tc.expectIntent != "" || tc.forbidden != "" {
					if len(row.RoutingChecks) == 0 {
						t.Error("routing assertions not recorded")
					}
				}
				if tc.verdict == "error" && row.ErrorKind != "routing_evidence" {
					t.Errorf("lost evidence error: %+v", row)
				}
			} else if len(row.Checks) != 0 {
				t.Error("incomplete turn must not run artifact checks")
			}
			data, err := os.ReadFile(filepath.Join(out, "transcripts", "routing-route-a1.json"))
			if err != nil {
				t.Fatal(err)
			}
			var tr results.Transcript
			if err := json.Unmarshal(data, &tr); err != nil {
				t.Fatal(err)
			}
			if tc.terminal != "submit_error" && (tr.FinalReply != "artifact ready" || len(tr.ToolTrace) != 1 || tr.Prompt != task.Prompt) {
				t.Errorf("transcript destroyed: %s", data)
			}
			creates, submits, traces := snapshots()
			if creates != 1 || submits != 1 {
				t.Errorf("freshness requires one create and one submit; got %d %d", creates, submits)
			}
			if tc.terminal == "" && !tc.createFailure && tc.name != "multi-turn association unavailable" && traces != 1 {
				t.Errorf("must read all route fields once, calls=%d", traces)
			}
		})
	}
}

func routingFake(t *testing.T, entries string, rpcFailure, createFailure bool, terminal string, onSubmit ...func(string, map[string]any)) (string, func() (int, int, int)) {
	t.Helper()
	return routingFakeTool(t, entries, rpcFailure, createFailure, terminal, nil, onSubmit...)
}

// routingFakeTool is routingFake with a configurable tool.execution.complete
// payload for the trace subscription.
func routingFakeTool(t *testing.T, entries string, rpcFailure, createFailure bool, terminal string, toolPayload map[string]any, onSubmit ...func(string, map[string]any)) (string, func() (int, int, int)) {
	t.Helper()
	dir, err := os.MkdirTemp("", "bench-route-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var mu sync.Mutex
	creates, submits, traces := 0, 0, 0
	workdir := ""
	nextSub := 0
	topics := map[string]bool{}
	sent := map[string]bool{}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				reader := bufio.NewReader(conn)
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					n, err := strconv.Atoi(strings.TrimSpace(line))
					if err != nil {
						return
					}
					data := make([]byte, n)
					if _, err := io.ReadFull(reader, data); err != nil {
						return
					}
					var req struct {
						ID     int64           `json:"id"`
						Method string          `json:"method"`
						Params json.RawMessage `json:"params"`
					}
					if json.Unmarshal(data, &req) != nil {
						return
					}
					mu.Lock()
					var params map[string]any
					_ = json.Unmarshal(req.Params, &params)
					var result any = map[string]any{}
					var rpcErr any
					switch req.Method {
					case "ping":
						result = "pong"
					case "session.create":
						creates++
						if createFailure {
							rpcErr = map[string]any{"code": -1, "message": "create failed"}
						} else {
							result = map[string]any{"id": "session-route", "conversation_id": "conv-route"}
						}
					case "project.register":
						workdir, _ = params["local_path"].(string)
					case "project.set":
						workdir, _ = params["path"].(string)
					case "bus.subscribe":
						nextSub++
						id := fmt.Sprint(nextSub)
						topics[id] = strings.Contains(string(req.Params), "turn.terminal")
						result = map[string]any{"subscription_id": id}
					case "chat.submit":
						for _, hook := range onSubmit {
							hook(workdir, params)
						}
						submits++
						if terminal == "submit_error" {
							rpcErr = map[string]any{"code": -1, "message": "submit transport failed"}
						}
						if !createFailure && params["conversation_id"] != "conv-route" {
							t.Errorf("submitted wrong identity: %s", req.Params)
						}
						if workdir != "" {
							if err := os.WriteFile(filepath.Join(workdir, "route-artifact.txt"), []byte("routing-test-marker"), 0600); err != nil {
								t.Error(err)
							}
						}
						result = map[string]any{"accepted": true, "turn_id": "turn-route", "conversation_id": "conv-route", "session_id": "session-route"}
					case "bus.poll":
						id, _ := params["subscription_id"].(string)
						events := []any{}
						if submits > 0 && !sent[id] {
							sent[id] = true
							topic := "tool.execution.complete"
							payload := map[string]any{"tool_name": "file_write", "session_id": "conv-route"}
							if toolPayload != nil {
								payload = toolPayload
							}
							if topics[id] {
								topic = "turn.terminal"
								status := terminal
								if status == "" {
									status = "completed"
								}
								payload = map[string]any{"turn_id": "turn-route", "session_id": "session-route", "conversation_id": "conv-route", "status": status, "reply": "artifact ready"}
							}
							events = append(events, map[string]any{"topic": topic, "timestamp": time.Now(), "payload": payload})
						}
						result = map[string]any{"events": events}
					case "session.dispatch_trace":
						traces++
						if rpcFailure {
							rpcErr = map[string]any{"code": -32601, "message": "method unavailable"}
						} else {
							var es []any
							_ = json.Unmarshal([]byte(entries), &es)
							result = map[string]any{"entries": es, "count": len(es)}
						}
					}
					mu.Unlock()
					resp := map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}
					if rpcErr != nil {
						delete(resp, "result")
						resp["error"] = rpcErr
					}
					b, _ := json.Marshal(resp)
					if _, err := fmt.Fprintf(conn, "%d\n%s", len(b), b); err != nil {
						return
					}
				}
			}()
		}
	}()
	return sock, func() (int, int, int) { mu.Lock(); defer mu.Unlock(); return creates, submits, traces }
}
