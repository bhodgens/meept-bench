package daemonclient

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func unmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
func marshal(v any) ([]byte, error)   { return json.Marshal(v) }

func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// startFakeDaemon serves the length-prefixed protocol and answers "ping".
func startFakeDaemon(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "meept-bench-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "test.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serve(conn)
		}
	}()
	return sock
}

func serve(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	for {
		payload, err := readFrame(r)
		if err != nil {
			return
		}
		var req rpcRequest
		if err := unmarshal(payload, &req); err != nil {
			return
		}
		resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
		switch req.Method {
		case "ping":
			resp.Result = []byte(`"pong"`)
		case "status":
			resp.Result = []byte(`{"status":"running","budget":{"daily_used":1.25}}`)
		default:
			resp.Error = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
		}
		out, err := marshal(resp)
		if err != nil {
			return
		}
		if err := writeFrame(conn, out); err != nil {
			return
		}
	}
}

func TestPingAndStatus(t *testing.T) {
	sock := startFakeDaemon(t)
	c := New(sock)
	ctx, cancel := contextWithTimeout(3 * time.Second)
	defer cancel()

	if err := c.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	st, err := c.Status(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st["status"] != "running" {
		t.Fatalf("unexpected status %v", st)
	}

	// method-not-found surfaces as an error
	err = c.Call(ctx, "nope.nope", nil, nil)
	if err == nil || !containsStr(err.Error(), "-32601") {
		t.Fatalf("want method-not-found error, got %v", err)
	}
	c.Close()
}

func TestRoutingObservation(t *testing.T) {
	// Fixtures mirror rpc/dispatch_trace.go and metrics.DispatchEntry; count
	// is the returned slice length, not a total. No turn_id exists on wire.
	cases := []struct {
		name, response, status string
		fresh                  bool
	}{
		{"unique", `{"entries":[{"session_id":"conv-test","agent_id":"coder","intent_type":"code","classifier_method":"llm","task_id":"task-1","turn_no":1}],"count":1}`, "available", true},
		{"missing", `{"entries":[],"count":0}`, "unavailable", true},
		{"missing count", `{"entries":[{"session_id":"conv-test"}]}`, "unavailable", true},
		{"invalid count", `{"entries":[{"session_id":"conv-test"}],"count":2}`, "unavailable", true},
		{"malformed", `{"entries":"invalid","count":1}`, "unavailable", true},
		{"missing method", `{"entries":[{"session_id":"conv-test","agent_id":"coder","intent_type":"code"}],"count":1}`, "partial", true},
		{"ambiguous", `{"entries":[{"session_id":"conv-test"},{"session_id":"conv-test"}],"count":2}`, "unavailable", true},
		{"wrong identity", `{"entries":[{"session_id":"other","agent_id":"coder","intent_type":"code","classifier_method":"llm"}],"count":1}`, "unavailable", true},
		{"unproven session", `{"entries":[],"count":0}`, "unavailable", false},
		{"errored", `{"entries":[{"session_id":"conv-test","error":"failed"}],"count":1}`, "unavailable", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clientSide, serverSide := net.Pipe()
			c := New("")
			c.conn, c.writer, c.reader = clientSide, clientSide, &frameReader{bufio.NewReader(clientSide)}
			defer c.Close()
			defer serverSide.Close()
			calls := make(chan rpcRequest, 1)
			go func() {
				data, err := readFrame(bufio.NewReader(serverSide))
				if err != nil {
					return
				}
				var req rpcRequest
				_ = json.Unmarshal(data, &req)
				calls <- req
				b, _ := json.Marshal(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(tc.response)})
				_ = writeFrame(serverSide, b)
			}()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			got := c.RoutingObservation(ctx, "conv-test", tc.fresh)
			if got.EvidenceStatus != tc.status {
				t.Fatalf("got %+v want status %s", got, tc.status)
			}
			if tc.fresh {
				req := <-calls
				var params struct {
					SessionID string `json:"session_id"`
					Limit     int    `json:"limit"`
				}
				_ = json.Unmarshal(req.Params, &params)
				if req.Method != "session.dispatch_trace" || params.SessionID != "conv-test" || params.Limit < 2 {
					t.Fatalf("unsafe request: %+v %s", req, req.Params)
				}
			}
			if tc.name == "unique" && (got.AgentID != "coder" || got.Intent != "code" || got.ClassificationMethod != "llm" || got.TaskID != "task-1" || got.Association != "fresh_session_unique_dispatch") {
				t.Fatalf("lost single-record fields: %+v", got)
			}
		})
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
