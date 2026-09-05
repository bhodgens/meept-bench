package runner

import (
	"testing"
	"time"

	"github.com/bhodgens/meept-bench/internal/suite"
)

// deterministicGateStatus builds the daemon status payload the gate reads.
func TestDeterministicGateAcceptsEnabledDaemon(t *testing.T) {
	st := map[string]any{"deterministic_tools": true}
	if on, _ := st["deterministic_tools"].(bool); !on {
		t.Fatal("gate must accept deterministic_tools=true")
	}
}

func TestDeterministicGateRejectsDisabledDaemon(t *testing.T) {
	for _, st := range []map[string]any{
		{"deterministic_tools": false},
		{},                              // key absent (older daemon)
		{"deterministic_tools": "true"}, // wrong type: not a string-coercing read
	} {
		if on, _ := st["deterministic_tools"].(bool); on {
			t.Errorf("gate must reject status %v", st)
		}
	}
}

func TestErrRowCarriesDeterministicGateDetail(t *testing.T) {
	m := &suite.Manifest{Suite: "det"}
	task := suite.Task{ID: "det-1", Tools: &suite.ToolsConfig{CachedFetch: true}}
	row := errRow(m, task, 1, time.Now(), "error",
		"deterministic gate: task requires tools.cached_fetch but daemon reports deterministic_tools=false")
	if row.Verdict != "error" || row.Passed {
		t.Errorf("verdict=%q passed=%v, want error/false", row.Verdict, row.Passed)
	}
	if row.ErrorDetail == "" {
		t.Error("gate failure must carry an actionable ErrorDetail")
	}
}
