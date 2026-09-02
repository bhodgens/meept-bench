package suite

import (
	"runtime"
	"strings"
	"testing"
)

// TestRegressionManifestLoads pins the committed regression suite against
// schema drift: it loads suites/regression.json from the repo root relative
// to this file (internal/suite -> repo root is two levels up) and asserts
// the invariants the gate relies on.
func TestRegressionManifestLoads(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// thisFile = <repo>/internal/suite/regression_manifest_test.go
	repoRoot := strings.TrimSuffix(thisFile, "/internal/suite/regression_manifest_test.go")
	path := repoRoot + "/suites/regression.json"

	m, err := Load(path)
	if err != nil {
		t.Fatalf("loading %s: %v", path, err)
	}
	if m.Suite != "regression" {
		t.Errorf("suite name = %q, want %q", m.Suite, "regression")
	}
	if len(m.Tasks) < 6 {
		t.Errorf("task count = %d, want >= 6", len(m.Tasks))
	}
	seen := make(map[string]bool, len(m.Tasks))
	for i, task := range m.Tasks {
		if seen[task.ID] {
			t.Errorf("task[%d]: duplicate id %q", i, task.ID)
		}
		seen[task.ID] = true
		if !hasTag(task, "regression") {
			t.Errorf("task %s: missing %q tag", task.ID, "regression")
		}
		if task.TimeoutS <= 0 {
			t.Errorf("task %s: timeout_seconds not set", task.ID)
		}
		if len(task.Seeds) == 0 {
			t.Errorf("task %s: seeds not set", task.ID)
		}
	}
	// Known-failure tasks stay LAST so a red xfail row is visually distinct
	// at the bottom of scorecards.
	for i, task := range m.Tasks {
		if !hasTag(task, "known-failure") {
			continue
		}
		for j := i + 1; j < len(m.Tasks); j++ {
			if !hasTag(m.Tasks[j], "known-failure") {
				t.Errorf("task %s (known-failure) at [%d] precedes non-known-failure task %s at [%d]; known-failures must be last",
					task.ID, i, m.Tasks[j].ID, j)
			}
		}
	}
}

func hasTag(task Task, tag string) bool {
	for _, t := range task.Tags {
		if t == tag {
			return true
		}
	}
	return false
}
