#!/usr/bin/env python3
"""Re-apply token-accounting instrumentation to internal/llm (meept).

Idempotent: checks for the marker symbol first and skips files already
patched. Re-runnable after sibling `git checkout` sweeps revert the tree.
"""
import sys

BASE = '/Users/caimlas/git/meept'

def rep(src, old, new, count=1, tag=''):
    n = src.count(old)
    assert n == count, f"MATCH FAIL ({n} != {count}) [{tag}]: {old[:60]!r}"
    return src.replace(old, new, count)

# ---------------- client.go ----------------
p = f'{BASE}/internal/llm/client.go'
src = open(p).read()
if 'recordUsageStore' not in src:
    src = rep(src, '''	"github.com/caimlas/meept/internal/llm/metrics"
)''',
'''	"github.com/caimlas/meept/internal/llm/metrics"
	appmetrics "github.com/caimlas/meept/internal/metrics"
)''', tag='client-import')

    src = rep(src, '''	tokenResolver TokenResolver
	oauthProvider string
	extraHeaders  map[string]string
	uploadStore   UploadStore
	// quotaMaxWait''',
'''	tokenResolver TokenResolver
	oauthProvider string
	extraHeaders  map[string]string
	uploadStore   UploadStore
	// usageStore is the app-level metrics store (metrics.db). When set,
	// every completed call (success or terminal failure) appends to the
	// llm_calls ledger and the model_performance rollup for per-provider
	// /per-agent token accounting.
	usageStore *appmetrics.Store
	// quotaMaxWait''', tag='client-struct')

    src = rep(src, '''// WithTimeoutCalculator sets the adaptive timeout calculator for the client.''',
'''// SetUsageStore attaches the app-level metrics store (metrics.db) for
// per-provider/per-agent token accounting. Nil-safe.
func (c *Client) SetUsageStore(store *appmetrics.Store) {
	if c == nil || store == nil {
		return
	}
	c.usageStore = store
}

// WithTimeoutCalculator sets the adaptive timeout calculator for the client.''', tag='client-setter')

    src = rep(src, '''	stopSequences    []string
	taskID           string
	sessionID        string
	reasoning        *ReasoningConfig''',
'''	stopSequences    []string
	taskID           string
	sessionID        string
	// agentID is the calling agent's identity (coder/researcher/...),
	// stamped by the agent loop and recorded into per-agent token
	// accounting. Empty when the caller has no agent identity.
	agentID          string
	reasoning        *ReasoningConfig''', tag='client-chatopts')

    src = rep(src, '''// WithAdapter sets the LoRA adapter path to use for this request.''',
'''// WithAgentScope sets the calling agent's identity for per-agent token
// accounting. Empty string is a no-op (unknown agent).
func WithAgentScope(agentID string) ChatOption {
	return func(o *chatOptions) {
		if agentID != "" {
			o.agentID = agentID
		}
	}
}

// WithAdapter sets the LoRA adapter path to use for this request.''', tag='client-agentscope')

    src = rep(src, '''// parseResponse converts a raw ChatResponse to a Response.''',
'''// recordUsageStore appends one completed LLM call to the app-level metrics
// store (metrics.db llm_calls ledger + model_performance rollup). No-op when
// no usage store is attached. Storage errors are logged, never fatal.
func (c *Client) recordUsageStore(providerID, modelID, agentID string, usage TokenUsage, isErr bool, errMsg string, latencyMs int64) {
	if c.usageStore == nil {
		return
	}
	//nolint:gosec // goroutine outlives request context
	go func() {
		c.usageStore.RecordLLMCall(appmetrics.LLMCallRecord{
			Timestamp:    time.Now(),
			Provider:     providerID,
			ModelID:      modelID,
			AgentID:      agentID,
			TokensSent:   usage.PromptTokens,
			TokensRecv:   usage.CompletionTokens,
			TokensCached: usage.CachedTokens,
			IsError:      isErr,
			ErrorMessage: errMsg,
			LatencyMs:    latencyMs,
		})
	}()
}

// parseResponse converts a raw ChatResponse to a Response.''', tag='client-helper')

    src = rep(src, '''		// Store in cache
		if c.tokenCache != nil && c.keyBuilder != nil {
			cacheKey := c.keyBuilder.Build("", cfg.ModelID, messages)
			c.tokenCache.Put(ctx, cacheKey, resp)
		}

		return resp, nil
	}

	// D8 exhaustion cap: every attempt was classified (throttle escalations
	// already returned ThrottleBackoffError above); reaching here means the
	// remaining retries were server errors — the historical ClientError
	// shape stands (leaf Notes).
	return nil, &ClientError{
		Message: fmt.Sprintf("All %d attempts failed", shortRetries),
		Cause:   lastErr,
	}
}''',
'''		// Store in cache
		if c.tokenCache != nil && c.keyBuilder != nil {
			cacheKey := c.keyBuilder.Build("", cfg.ModelID, messages)
			c.tokenCache.Put(ctx, cacheKey, resp)
		}

		// Per-provider/per-agent token accounting (metrics.db llm_calls).
		c.recordUsageStore(cfg.ProviderID, cfg.ModelID, chatOpts.agentID, resp.Usage, false, "", 0)

		return resp, nil
	}

	// D8 exhaustion cap: every attempt was classified (throttle escalations
	// already returned ThrottleBackoffError above); reaching here means the
	// remaining retries were server errors — the historical ClientError
	// shape stands (leaf Notes).
	allFailed := &ClientError{
		Message: fmt.Sprintf("All %d attempts failed", shortRetries),
		Cause:   lastErr,
	}
	c.recordUsageStore(cfg.ProviderID, cfg.ModelID, chatOpts.agentID, TokenUsage{}, true, allFailed.Message, 0)
	return nil, allFailed
}''', tag='client-chat')

    src = rep(src, '''		// Report completion with token count
		reportProgress(ProgressStageDone, fmt.Sprintf("Complete: %d tokens", resp.Usage.TotalTokens))

		return resp, nil
	}

	reportProgress(ProgressStageDone, fmt.Sprintf("Failed after %d attempts", shortRetries))
	return nil, &ClientError{
		Message: fmt.Sprintf("All %d attempts failed", shortRetries),
		Cause:   lastErr,
	}
}''',
'''		// Report completion with token count
		reportProgress(ProgressStageDone, fmt.Sprintf("Complete: %d tokens", resp.Usage.TotalTokens))

		// Per-provider/per-agent token accounting (metrics.db llm_calls).
		c.recordUsageStore(cfg.ProviderID, cfg.ModelID, chatOpts.agentID, resp.Usage, false, "", 0)

		return resp, nil
	}

	reportProgress(ProgressStageDone, fmt.Sprintf("Failed after %d attempts", shortRetries))
	allFailed := &ClientError{
		Message: fmt.Sprintf("All %d attempts failed", shortRetries),
		Cause:   lastErr,
	}
	c.recordUsageStore(cfg.ProviderID, cfg.ModelID, chatOpts.agentID, TokenUsage{}, true, allFailed.Message, 0)
	return nil, allFailed
}''', tag='client-cwp')

    src = rep(src, '''				_ = c.metricsStore.Record(context.Background(), record)
				}()
			}
			return resp, nil
		}''',
'''				_ = c.metricsStore.Record(context.Background(), record)
				}()
			}

			// Per-provider/per-agent token accounting (metrics.db llm_calls).
			if resp != nil {
				c.recordUsageStore(cfg.ProviderID, cfg.ModelID, chatOpts.agentID, resp.Usage, false, "", 0)
			}
			return resp, nil
		}''', tag='client-stream-ok')

    src = rep(src, '''	return nil, &ClientError{
		Message: fmt.Sprintf("streaming failed after %d attempts", shortRetries),
		Cause:   lastErr,
	}
}''',
'''	streamFailed := &ClientError{
		Message: fmt.Sprintf("streaming failed after %d attempts", shortRetries),
		Cause:   lastErr,
	}
	c.recordUsageStore(cfg.ProviderID, cfg.ModelID, chatOpts.agentID, TokenUsage{}, true, streamFailed.Message, 0)
	return nil, streamFailed
}''', tag='client-stream-fail')

    open(p, 'w').write(src)
    print(f"client.go PATCHED -> {len(src)} bytes")
else:
    print("client.go already patched")

# ---------------- anthropic.go ----------------
p = f'{BASE}/internal/llm/anthropic.go'
src = open(p).read()
if 'recordUsageStore' not in src:
    src = rep(src, '''	"github.com/caimlas/meept/internal/llm/metrics"
)''',
'''	"github.com/caimlas/meept/internal/llm/metrics"
	appmetrics "github.com/caimlas/meept/internal/metrics"
)''', tag='anthropic-import')

    src = rep(src, '''	tokenResolver TokenResolver
	oauthProvider string
	// quotaMaxWait is the upper bound applied to derived quota waits. Zero''',
'''	tokenResolver TokenResolver
	oauthProvider string
	// usageStore is the app-level metrics store (metrics.db). See
	// Client.usageStore.
	usageStore *appmetrics.Store
	// quotaMaxWait is the upper bound applied to derived quota waits. Zero''', tag='anthropic-struct')

    src = rep(src, '''// WithAnthropicTimeout sets the HTTP timeout for the client.''',
'''// SetUsageStore attaches the app-level metrics store (metrics.db) for
// per-provider/per-agent token accounting. Nil-safe.
func (c *AnthropicClient) SetUsageStore(store *appmetrics.Store) {
	if c == nil || store == nil {
		return
	}
	c.usageStore = store
}

// WithAnthropicTimeout sets the HTTP timeout for the client.''', tag='anthropic-setter')

    src = rep(src, '''// Chat sends a chat completion request to Anthropic's Messages API.
func (c *AnthropicClient) Chat(ctx context.Context, messages []ChatMessage, opts ...ChatOption) (*Response, error) {''',
'''// recordUsageStore appends one completed call to the app-level metrics
// store (metrics.db llm_calls + model_performance). No-op without a store.
func (c *AnthropicClient) recordUsageStore(usage TokenUsage, isErr bool, errMsg string, latencyMs int64, chatOpts *chatOptions) {
	if c.usageStore == nil {
		return
	}
	cfg := c.config
	if cfg == nil {
		return
	}
	//nolint:gosec // goroutine outlives request context
	go func() {
		agentID := ""
		if chatOpts != nil {
			agentID = chatOpts.agentID
		}
		c.usageStore.RecordLLMCall(appmetrics.LLMCallRecord{
			Timestamp:    time.Now(),
			Provider:     cfg.ProviderID,
			ModelID:      cfg.ModelID,
			AgentID:      agentID,
			TokensSent:   usage.PromptTokens,
			TokensRecv:   usage.CompletionTokens,
			TokensCached: usage.CachedTokens,
			IsError:      isErr,
			ErrorMessage: errMsg,
			LatencyMs:    latencyMs,
		})
	}()
}

// Chat sends a chat completion request to Anthropic's Messages API.
func (c *AnthropicClient) Chat(ctx context.Context, messages []ChatMessage, opts ...ChatOption) (*Response, error) {''', tag='anthropic-helper')

    src = rep(src, '''		if c.budget != nil {
			c.budget.RecordUsageWithScope(resp.Usage, chatOpts.taskID, chatOpts.sessionID)
			// Record cost with scope if model pricing is available
			if cfg != nil {''',
'''		// Per-provider/per-agent token accounting (metrics.db llm_calls).
		c.recordUsageStore(resp.Usage, false, "", 0, chatOpts)

		if c.budget != nil {
			c.budget.RecordUsageWithScope(resp.Usage, chatOpts.taskID, chatOpts.sessionID)
			// Record cost with scope if model pricing is available
			if cfg != nil {''', count=2, tag='anthropic-ok-x2')

    src = rep(src, '''	// D8 exhaustion cap: throttle escalations already returned
	// ThrottleBackoffError above; reaching here means the remaining retries
	// were server errors — the historical ClientError shape stands.
	return nil, &ClientError{
		Message: fmt.Sprintf("All %d attempts failed", shortRetries),
		Cause:   lastErr,
	}
}''',
'''	// D8 exhaustion cap: throttle escalations already returned
	// ThrottleBackoffError above; reaching here means the remaining retries
	// were server errors — the historical ClientError shape stands.
	allFailed := &ClientError{
		Message: fmt.Sprintf("All %d attempts failed", shortRetries),
		Cause:   lastErr,
	}
	c.recordUsageStore(TokenUsage{}, true, allFailed.Message, 0, chatOpts)
	return nil, allFailed
}''', tag='anthropic-exhaust')

    src = rep(src, '''	reportProgress(ProgressStageDone, fmt.Sprintf("Failed after %d attempts", shortRetries))
	return nil, &ClientError{
		Message: fmt.Sprintf("All %d attempts failed", shortRetries),
		Cause:   lastErr,
	}
}''',
'''	reportProgress(ProgressStageDone, fmt.Sprintf("Failed after %d attempts", shortRetries))
	allFailed := &ClientError{
		Message: fmt.Sprintf("All %d attempts failed", shortRetries),
		Cause:   lastErr,
	}
	c.recordUsageStore(TokenUsage{}, true, allFailed.Message, 0, chatOpts)
	return nil, allFailed
}''', tag='anthropic-cwp-exhaust')

    open(p, 'w').write(src)
    print(f"anthropic.go PATCHED -> {len(src)} bytes")
else:
    print("anthropic.go already patched")

# ---------------- codex.go ----------------
p = f'{BASE}/internal/llm/codex.go'
src = open(p).read()
if 'recordUsageStore' not in src:
    src = rep(src, '''	"sync/atomic"
	"time"
)''',
'''	"sync/atomic"
	"time"

	appmetrics "github.com/caimlas/meept/internal/metrics"
)''', tag='codex-import')

    src = rep(src, '''	tokenResolver TokenResolver
	oauthProvider string
}''',
'''	tokenResolver TokenResolver
	oauthProvider string
	// usageStore is the app-level metrics store (metrics.db). See
	// Client.usageStore.
	usageStore *appmetrics.Store
}''', tag='codex-struct')

    src = rep(src, '''// WithCodexTokenResolver''',
'''// SetUsageStore attaches the app-level metrics store (metrics.db) for
// per-provider/per-agent token accounting. Nil-safe.
func (c *CodexClient) SetUsageStore(store *appmetrics.Store) {
	if c == nil || store == nil {
		return
	}
	c.usageStore = store
}

// WithCodexTokenResolver''', tag='codex-setter')

    src = rep(src, '''// Chat sends a non-streaming Responses request (stream:false + single JSON
// body) and returns the parsed Response.''',
'''// recordUsageStore appends one completed call to the app-level metrics
// store (metrics.db llm_calls + model_performance). No-op without a store.
func (c *CodexClient) recordUsageStore(cfg *ModelConfig, chatOpts *chatOptions, usage TokenUsage, isErr bool, errMsg string) {
	if c.usageStore == nil || cfg == nil {
		return
	}
	agentID := ""
	if chatOpts != nil {
		agentID = chatOpts.agentID
	}
	//nolint:gosec // goroutine outlives request context
	go func() {
		c.usageStore.RecordLLMCall(appmetrics.LLMCallRecord{
			Timestamp:    time.Now(),
			Provider:     cfg.ProviderID,
			ModelID:      cfg.ModelID,
			AgentID:      agentID,
			TokensSent:   usage.PromptTokens,
			TokensRecv:   usage.CompletionTokens,
			TokensCached: usage.CachedTokens,
			IsError:      isErr,
			ErrorMessage: errMsg,
		})
	}()
}

// Chat sends a non-streaming Responses request (stream:false + single JSON
// body) and returns the parsed Response.''', tag='codex-helper')

    src = rep(src, '''		}
	}
	return resp, nil
}

// ChatWithProgress behaves like Chat, reporting start/done progress stages.''',
'''		}
	}

	// Per-provider/per-agent token accounting (metrics.db llm_calls).
	c.recordUsageStore(cfg, chatOpts, resp.Usage, false, "")

	return resp, nil
}

// ChatWithProgress behaves like Chat, reporting start/done progress stages.''', tag='codex-ok')

    src = rep(src, '''	payload := c.buildPayload(messages, cfg, chatOpts, false)
	resp, err := c.doRequest(ctx, payload, cfg, chatOpts.sessionID, nil)
	if err != nil {
		return nil, err
	}''',
'''	payload := c.buildPayload(messages, cfg, chatOpts, false)
	resp, err := c.doRequest(ctx, payload, cfg, chatOpts.sessionID, nil)
	if err != nil {
		c.recordUsageStore(cfg, chatOpts, TokenUsage{}, true, err.Error())
		return nil, err
	}''', tag='codex-err')

    open(p, 'w').write(src)
    print(f"codex.go PATCHED -> {len(src)} bytes")
else:
    print("codex.go already patched")

# ---------------- codex_sse.go ----------------
p = f'{BASE}/internal/llm/codex_sse.go'
src = open(p).read()
if 'recordUsageStore' not in src:
    src = rep(src, '''	payload := c.buildPayload(messages, cfg, chatOpts, true)
	resp, err := c.doRequest(ctx, payload, cfg, chatOpts.sessionID, onDelta)
	if err != nil {
		return nil, err
	}''',
'''	payload := c.buildPayload(messages, cfg, chatOpts, true)
	resp, err := c.doRequest(ctx, payload, cfg, chatOpts.sessionID, onDelta)
	if err != nil {
		c.recordUsageStore(cfg, chatOpts, TokenUsage{}, true, err.Error())
		return nil, err
	}''', tag='sse-err')

    src = rep(src, '''			}, chatOpts.taskID, chatOpts.sessionID)
			}
		}
	}
	return resp, nil
}''',
'''			}, chatOpts.taskID, chatOpts.sessionID)
			}
		}
	}

	// Per-provider/per-agent token accounting (metrics.db llm_calls).
	c.recordUsageStore(cfg, chatOpts, resp.Usage, false, "")

	return resp, nil
}''', tag='sse-ok')

    open(p, 'w').write(src)
    print(f"codex_sse.go PATCHED -> {len(src)} bytes")
else:
    print("codex_sse.go already patched")

# ---------------- provider_manager.go ----------------
p = f'{BASE}/internal/llm/provider_manager.go'
src = open(p).read()
if 'AttachUsageStore' not in src:
    src = rep(src, '''	"sync"
	"time"
)''',
'''	"sync"
	"time"

	appmetrics "github.com/caimlas/meept/internal/metrics"
)''', tag='pm-import')

    src = rep(src, '''	// quotaMaxWait caps how far into the future a quota block is persisted.
	// Zero means DefaultQuotaMaxWait.
	quotaMaxWait time.Duration
	// failurePolicy is the failure-policy config propagated onto''',
'''	// quotaMaxWait caps how far into the future a quota block is persisted.
	// Zero means DefaultQuotaMaxWait.
	quotaMaxWait time.Duration
	// usageStore is the app-level metrics store fanned out to every
	// managed chatter (metrics.db llm_calls accounting). Guarded by mu.
	usageStore *appmetrics.Store
	// failurePolicy is the failure-policy config propagated onto''', tag='pm-struct')

    src = rep(src, '''// AddProvider adds a new provider dynamically.''',
'''// SetUsageStore attaches the app-level metrics store (metrics.db) to every
// managed chatter (and to future ones via AddProvider) for per-provider
// /per-agent token accounting. Nil-safe.
func (pm *ProviderManager) SetUsageStore(store *appmetrics.Store) {
	if pm == nil || store == nil {
		return
	}
	pm.mu.Lock()
	pm.usageStore = store
	for _, entry := range pm.providers {
		AttachUsageStore(entry.Chatter, store)
	}
	pm.mu.Unlock()
}

// AddProvider adds a new provider dynamically.''', tag='pm-setter')

    src = rep(src, '''	if pm.failurePolicy != nil {
		if c, ok := entry.Chatter.(*Client); ok {
			c.SetFailurePolicyConfig(pm.failurePolicy)
		}
	}

	pm.providers = append(pm.providers, entry)''',
'''	if pm.failurePolicy != nil {
		if c, ok := entry.Chatter.(*Client); ok {
			c.SetFailurePolicyConfig(pm.failurePolicy)
		}
	}
	AttachUsageStore(entry.Chatter, pm.usageStore)

	pm.providers = append(pm.providers, entry)''', tag='pm-addprovider')

    src += '''

// UsageStoreAttacher is implemented by LLM clients that can record
// per-provider/per-agent token accounting into the app-level metrics
// store (metrics.db llm_calls).
type UsageStoreAttacher interface {
	SetUsageStore(store *appmetrics.Store)
}

// AttachUsageStore attaches the store to a Chatter when the underlying
// client implements UsageStoreAttacher. No-op otherwise (unknown Chatter
// implementations keep their current behavior). Nil-safe.
func AttachUsageStore(chatter Chatter, store *appmetrics.Store) {
	if chatter == nil || store == nil {
		return
	}
	if sa, ok := chatter.(UsageStoreAttacher); ok {
		sa.SetUsageStore(store)
	}
}
'''
    open(p, 'w').write(src)
    print(f"provider_manager.go PATCHED -> {len(src)} bytes")
else:
    print("provider_manager.go already patched")

print("ALL DONE")
