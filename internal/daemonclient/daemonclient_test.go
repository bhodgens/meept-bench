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

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
