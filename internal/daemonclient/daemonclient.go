// Package daemonclient speaks JSON-RPC 2.0 to the meept daemon over its
// length-prefixed unix-socket transport, plus bus event subscription via
// bus.subscribe / bus.poll.
//
// Wire format (mirrors meept internal/rpc/protocol.go):
//
//	<length>\n<payload JSON>
package daemonclient

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultSocketPath is meept's default state-dir socket location.
func DefaultSocketPath() string {
	if p := os.Getenv("MEEPT_BENCH_SOCKET"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/meept/meept.sock"
	}
	return filepath.Join(home, ".meept", "meept.sock")
}

// Client is a JSON-RPC client over a single unix-socket connection.
// It is safe for concurrent use; calls are serialized by a mutex because
// meept's frame transport is a synchronous request/response stream.
type Client struct {
	path string

	mu     sync.Mutex
	conn   net.Conn
	reader *frameReader
	writer io.Writer
	nextID int64
}

type frameReader struct{ r *bufio.Reader }

func readFrame(r *bufio.Reader) ([]byte, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("read length: %w", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || n <= 0 || n > 10*1024*1024 {
		return nil, fmt.Errorf("invalid frame length %q", line)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, fmt.Errorf("read payload: %w", err)
	}
	return payload, nil
}

func writeFrame(w io.Writer, payload []byte) error {
	if _, err := fmt.Fprintf(w, "%d\n", len(payload)); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// New creates a client for the daemon at socket path. No connection is made
// until the first call.
func New(path string) *Client { return &Client{path: path} }

// NewDefault creates a client for the default socket.
func NewDefault() *Client { return New(DefaultSocketPath()) }

// Fork returns a new Client over a separate connection to the same socket.
// meept-bench serializes RPCs per connection (see the Client doc comment), so
// a concurrent steering call would otherwise queue behind an in-flight Chat
// that holds the connection mutex for the whole agent round-trip. A forked
// client lets follow-up RPCs land while the primary chat is still being
// awaited. The returned client has its own connection lifecycle; Close it
// independently.
func (c *Client) Fork() *Client {
	return New(c.path)
}

// Path returns the configured socket path.
func (c *Client) Path() string { return c.path }

func (c *Client) connect(ctx context.Context) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.path)
	if err != nil {
		return fmt.Errorf("dial %s: %w", c.path, err)
	}
	c.conn = conn
	c.reader = &frameReader{bufio.NewReader(conn)}
	c.writer = conn
	return nil
}

// Close closes the underlying connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}

// Call performs one JSON-RPC request and decodes result into out (may be nil).
func (c *Client) Call(ctx context.Context, method string, params, out any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.callLocked(ctx, method, params, out)
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	if e.Data != nil {
		return fmt.Sprintf("rpc error %d: %s (%v)", e.Code, e.Message, e.Data)
	}
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

const maxRetries = 1 // reconnect once on a dead pipe

func (c *Client) callLocked(ctx context.Context, method string, params, out any) error {
	var rawParams json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		rawParams = b
	}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if c.conn == nil {
			if err := c.connect(ctx); err != nil {
				return err
			}
		}

		c.nextID++
		id := c.nextID
		req := rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: rawParams}
		data, err := json.Marshal(req)
		if err != nil {
			return err
		}
		if err := writeFrame(c.writer, data); err != nil {
			c.drop()
			continue // retry once with a fresh connection
		}

		type res struct {
			resp *rpcResponse
			err  error
		}
		done := make(chan res, 1)
		go func() {
			payload, err := readFrame(c.reader.r)
			if err != nil {
				done <- res{err: err}
				return
			}
			var resp rpcResponse
			if err := json.Unmarshal(payload, &resp); err != nil {
				done <- res{err: fmt.Errorf("decode response: %w", err)}
				return
			}
			done <- res{resp: &resp}
		}()

		select {
		case <-ctx.Done():
			c.drop()
			return ctx.Err()
		case r := <-done:
			if r.err != nil {
				c.drop()
				continue
			}
			if r.resp.ID != id {
				return fmt.Errorf("response id mismatch: got %d want %d", r.resp.ID, id)
			}
			if r.resp.Error != nil {
				return r.resp.Error
			}
			if out != nil && len(r.resp.Result) > 0 {
				return json.Unmarshal(r.resp.Result, out)
			}
			return nil
		}
	}
	return fmt.Errorf("connection to daemon lost while calling %s", method)
}

func (c *Client) drop() {
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
}

// Ping checks daemon liveness.
func (c *Client) Ping(ctx context.Context) error {
	var s string
	return c.Call(ctx, "ping", nil, &s)
}

// Status returns the daemon status map (method "status").
func (c *Client) Status(ctx context.Context) (map[string]any, error) {
	var st map[string]any
	err := c.Call(ctx, "status", nil, &st)
	return st, err
}

// Chat sends a message and waits for the agent's reply via the bus proxy
// ("chat" RPC method → chat.request/chat.response topics).
//
// meept's RPC proxy enforces a HARD 120s server-side timeout on the chat
// round-trip regardless of client context. Long agent runs therefore return
// a proxy-timeout error while the work continues on the daemon. To stay
// correct for benchmark tasks that exceed 120s, we subscribe to the
// `chat_message` bus topic BEFORE sending; if the RPC times out we keep
// waiting there for the assistant reply carrying our conversation ID.
func (c *Client) Chat(ctx context.Context, message, sessionID string) (*ChatResponse, error) {
	conversation := sessionID
	if conversation == "" {
		conversation = fmt.Sprintf("bench-%d", time.Now().UnixNano())
	}

	// Watcher drains chat_message events into a channel keyed by conversation.
	type replyMsg struct {
		Role           string `json:"role"`
		Content        string `json:"content"`
		SessionID      string `json:"session_id"`
		ConversationID string `json:"conversation_id"`
		Error          string `json:"error"`
	}
	replies := make(chan replyMsg, 16)
	subCtx, cancelSub := context.WithTimeout(context.Background(), 10*time.Second)
	sub, err := c.Subscribe(subCtx, []string{"chat_message"})
	cancelSub()
	if err == nil {
		go func() {
			defer close(replies)
			for {
				evts, err := sub.Poll(context.Background())
				if err != nil {
					return
				}
				for _, e := range evts {
					var m replyMsg
					if json.Unmarshal(e.Payload, &m) == nil {
						select {
						case replies <- m:
						default:
						}
					}
				}
			}
		}()
		defer sub.Unsubscribe(context.Background())
	}
	// err from Subscribe is non-fatal: fall back to plain RPC below.

	params := map[string]any{
		"message":         message,
		"source_client":   "meept-bench",
		"session_id":      sessionID,
		"conversation_id": conversation,
	}
	var resp ChatResponse
	rpcErr := c.Call(ctx, "chat", params, &resp)
	if rpcErr == nil && resp.Error == "" {
		return &resp, nil
	}
	if rpcErr == nil && resp.Error != "" && !isProxyTimeout(resp.Error) {
		return &resp, nil // real agent-reported error
	}
	// Proxy timeout (or agent error mentioning timeout): wait on bus events.
	if err != nil && sub == nil {
		if isProxyTimeout(fmt.Sprint(rpcErr)) || (rpcErr != nil && strings.Contains(rpcErr.Error(), "timeout")) {
			return nil, fmt.Errorf("chat RPC timed out after %s and no event fallback available (subscribe failed)", proxyWindow)
		}
		return nil, rpcErr
	}

	deadline := time.NewTimer(proxyWindow + graceWindow)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			if rpcErr != nil {
				return nil, fmt.Errorf("chat: %w (and no reply within event-fallback window)", rpcErr)
			}
			return &resp, nil
		case m, ok := <-replies:
			if !ok {
				continue
			}
			if m.Role == "assistant" && (m.ConversationID == conversation || m.SessionID == conversation) {
				out := &ChatResponse{
					Reply:          m.Content,
					ConversationID: m.ConversationID,
					SessionID:      m.SessionID,
					Error:          m.Error,
				}
				if m.Error != "" {
					out.Reply = ""
				}
				return out, nil
			}
		}
	}
}

const (
	proxyWindow = 125 * time.Second // just above meept's 120s proxy cap
	graceWindow = 10 * time.Minute  // extra time for the agent to finish on the bus
)

// isProxyTimeout recognizes meept's chat.response proxy timeout message.
func isProxyTimeout(s string) bool {
	return strings.Contains(s, "timeout waiting for response") ||
		strings.Contains(s, "context deadline exceeded")
}

// ChatResponse mirrors meept's agent.ChatResponse.
type ChatResponse struct {
	Reply          string `json:"reply"`
	ConversationID string `json:"conversation_id"`
	SessionID      string `json:"session_id,omitempty"`
	Error          string `json:"error,omitempty"`
}

// --- Bus events ---

// Event is a single buffered bus event from bus.poll.
type Event struct {
	Topic     string          `json:"topic"`
	Type      string          `json:"type"`
	Source    string          `json:"source"`
	Timestamp time.Time       `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

// Subscription is an active bus event subscription.
type Subscription struct {
	ID  string
	c   *Client
	mu  sync.Mutex
	sin time.Time // cursor; zero means all buffered events
}

// Subscribe subscribes to the given bus topics (wildcards supported by meept).
func (c *Client) Subscribe(ctx context.Context, topics []string) (*Subscription, error) {
	var out struct {
		SubscriptionID string `json:"subscription_id"`
	}
	if err := c.Call(ctx, "bus.subscribe", map[string]any{"topics": topics}, &out); err != nil {
		return nil, err
	}
	return &Subscription{ID: out.SubscriptionID, c: c}, nil
}

// Poll drains events newer than the last poll.
func (s *Subscription) Poll(ctx context.Context) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	params := map[string]any{"subscription_id": s.ID}
	if !s.sin.IsZero() {
		params["since"] = s.sin.Format(time.RFC3339Nano)
	}
	var out struct {
		Events []Event `json:"events"`
	}
	if err := s.c.Call(ctx, "bus.poll", params, &out); err != nil {
		return nil, err
	}
	for _, e := range out.Events {
		if e.Timestamp.After(s.sin) {
			s.sin = e.Timestamp
		}
	}
	return out.Events, nil
}

// Unsubscribe removes the subscription.
func (s *Subscription) Unsubscribe(ctx context.Context) error {
	return s.c.Call(ctx, "bus.unsubscribe", map[string]any{"subscription_id": s.ID}, nil)
}

// Collect runs fn over each polled batch until ctx is cancelled or done fires.
// Returns when done receives a value or closes.
func (s *Subscription) Collect(ctx context.Context, interval time.Duration, done <-chan struct{}, fn func([]Event)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			// final drain so nothing published just before completion is lost
			if evts, err := s.Poll(context.WithoutCancel(ctx)); err == nil && len(evts) > 0 {
				fn(evts)
			}
			return
		case <-ticker.C:
			evts, err := s.Poll(ctx)
			if err != nil {
				return
			}
			if len(evts) > 0 {
				fn(evts)
			}
		}
	}
}

// ProjectRegister registers (or updates) a local project binding on the daemon.
func (c *Client) ProjectRegister(ctx context.Context, id, name, localPath string) error {
	var out any
	return c.Call(ctx, "project.register", map[string]any{
		"id": id, "name": name, "local_path": localPath,
	}, &out)
}

// SessionIDs holds both identifiers meept uses: the primary session ID
// ("session-…", needed for project.set) and the conversation ID ("conv-…",
// which the agent loop uses for session-store lookups).
type SessionIDs struct {
	SessionID      string
	ConversationID string
}

// SessionCreate opens a new chat session and returns both IDs.
func (c *Client) SessionCreate(ctx context.Context) (*SessionIDs, error) {
	var out map[string]any
	if err := c.Call(ctx, "session.create", map[string]any{}, &out); err != nil {
		return nil, err
	}
	ids := &SessionIDs{}
	if v, ok := out["id"].(string); ok {
		ids.SessionID = v
	}
	if v, ok := out["conversation_id"].(string); ok {
		ids.ConversationID = v
	}
	if ids.SessionID == "" && ids.ConversationID == "" {
		return nil, fmt.Errorf("session.create returned no ids")
	}
	return ids, nil
}

// DispatchedAgent returns the agent the dispatcher routed the most recent
// message in the given session/conversation to, by querying the daemon's
// "session.dispatch_trace" RPC (persistent dispatch audit log, most recent
// first). Dispatch decisions are recorded keyed by the conversation ID
// (meept internal/agent/handler.go passes conversationID to RecordDispatch),
// so callers may pass either identifier.
//
// Returns ("", nil) — not an error — when the daemon exposes no dispatch
// trace (RPC method not registered / metrics store absent) or when no
// entries exist for the session; the runner treats empty as "routing
// unknown, skip assertion".
func (c *Client) DispatchedAgent(ctx context.Context, sessionOrConversationID string) (string, error) {
	if sessionOrConversationID == "" {
		return "", nil
	}
	var out struct {
		Entries []struct {
			AgentID string `json:"agent_id"`
			Error   string `json:"error"`
		} `json:"entries"`
	}
	err := c.Call(ctx, "session.dispatch_trace", map[string]any{
		"session_id": sessionOrConversationID,
		"limit":      1,
	}, &out)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return "", err
		}
		// Unknown-method / unavailable store: treat as "no routing info".
		return "", nil
	}
	for _, e := range out.Entries {
		if e.Error != "" {
			continue // skip failed dispatches
		}
		if e.AgentID != "" {
			return e.AgentID, nil
		}
	}
	return "", nil
}

// ClassificationMethod returns the classifier that produced the routing
// decision for the most recent message in the given session/conversation,
// by querying the same "session.dispatch_trace" RPC DispatchedAgent uses
// (entry field "classifier_method": e.g. "capability_matcher", "llm",
// "keyword", "semantic", "heuristic_fallback", "short_message_guard",
// "llm_empty_fallback_chat", "fallback", "compound").
//
// Returns ("", nil) — not an error — when the dispatch trace is unavailable
// or carries no method for the session; the runner records empty as
// "classification method unknown".
func (c *Client) ClassificationMethod(ctx context.Context, sessionOrConversationID string) (string, error) {
	if sessionOrConversationID == "" {
		return "", nil
	}
	var out struct {
		Entries []struct {
			ClassifierMethod string `json:"classifier_method"`
			Error            string `json:"error"`
		} `json:"entries"`
	}
	err := c.Call(ctx, "session.dispatch_trace", map[string]any{
		"session_id": sessionOrConversationID,
		"limit":      1,
	}, &out)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return "", err
		}
		// Unknown-method / unavailable store: treat as "no method info".
		return "", nil
	}
	for _, e := range out.Entries {
		if e.Error != "" {
			continue // skip failed dispatches
		}
		if e.ClassifierMethod != "" {
			return e.ClassifierMethod, nil
		}
	}
	return "", nil
}

// --- Steering / follow-up queue ---

// steerParams matches meept's chat.steer / chat.followup request schema
// (internal/rpc/queue.go QueueHandler).
type steerParams struct {
	Message        string `json:"message"`
	ConversationID string `json:"conversation_id"`
	Source         string `json:"source,omitempty"`
}

// Steer injects a steering message into the daemon's steering queue for the
// conversation. Steering messages interrupt the agent's current flow: the
// loop drains at most one steering message per iteration and injects it into
// the model context mid-run. Returns an error when no active queue exists
// for the conversation (agent idle / queue already closed).
func (c *Client) Steer(ctx context.Context, conversationID, message string) error {
	var out map[string]any
	return c.Call(ctx, "chat.steer", steerParams{
		Message:        message,
		ConversationID: conversationID,
		Source:         "meept-bench",
	}, &out)
}

// FollowUp injects a follow-up message into the daemon's follow-up queue for
// the conversation. Follow-ups wait for the agent to reach a natural stopping
// point and are then dispatched as the next user turn on the same
// conversation. Returns an error when no active queue exists.
func (c *Client) FollowUp(ctx context.Context, conversationID, message string) error {
	var out map[string]any
	return c.Call(ctx, "chat.followup", steerParams{
		Message:        message,
		ConversationID: conversationID,
		Source:         "meept-bench",
	}, &out)
}

// QueueStatus reports the steering/follow-up queue depths for a conversation
// plus whether the queue is active. Useful for asserting that a steering
// message was actually accepted (steering_depth > 0) before the agent drains
// it.
func (c *Client) QueueStatus(ctx context.Context, conversationID string) (steeringDepth, followUpDepth int, isActive bool, err error) {
	var out struct {
		SteeringDepth int    `json:"steering_depth"`
		FollowUpDepth int    `json:"followup_depth"`
		IsActive      bool   `json:"is_active"`
		Generation    uint64 `json:"generation"`
	}
	err = c.Call(ctx, "chat.queue_status", map[string]any{
		"conversation_id": conversationID,
	}, &out)
	return out.SteeringDepth, out.FollowUpDepth, out.IsActive, err
}
