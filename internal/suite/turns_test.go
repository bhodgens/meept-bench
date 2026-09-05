package suite

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestTurnSchemaParsing verifies that Turns decode from suite JSON with the
// documented field names (delay_s, message) and survive a Load round-trip.
func TestTurnSchemaParsing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "steering.json")
	content := `{
  "suite": "steering",
  "tasks": [
    {
      "id": "midrun",
      "prompt": "create result.txt",
      "turns": [
        {"delay_s": 5, "message": "actually name it result2.txt instead"},
        {"message": "confirm when done"}
      ],
      "checkers": [{"type": "file_contains", "files": ["result2.txt"], "pattern": "^7$"}]
    },
    {
      "id": "control",
      "prompt": "single turn",
      "checkers": [{"type": "exit_zero", "command": ["true"]}]
    }
  ]
}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(m.Tasks) != 2 {
		t.Fatalf("tasks = %d, want 2", len(m.Tasks))
	}

	midrun := m.Tasks[0]
	if len(midrun.Turns) != 2 {
		t.Fatalf("midrun turns = %d, want 2", len(midrun.Turns))
	}
	t0 := midrun.Turns[0]
	if t0.DelayS != 5 {
		t.Errorf("turn[0].delay_s = %d, want 5", t0.DelayS)
	}
	if t0.Message != "actually name it result2.txt instead" {
		t.Errorf("turn[0].message = %q", t0.Message)
	}
	// delay_s omitted → zero value, still valid (fire immediately).
	t1 := midrun.Turns[1]
	if t1.DelayS != 0 || t1.Message != "confirm when done" {
		t.Errorf("turn[1] = %+v", t1)
	}

	control := m.Tasks[1]
	if len(control.Turns) != 0 {
		t.Errorf("control turns = %d, want 0", len(control.Turns))
	}
}

// TestTurnSchemaJSONTags locks the wire format: the JSON keys must stay
// delay_s / message so hand-written suites and downstream parsers agree.
func TestTurnSchemaJSONTags(t *testing.T) {
	b, err := json.Marshal(Turn{DelayS: 7, Message: "steer left"})
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["delay_s"]; !ok {
		t.Errorf("expected key delay_s, got %s", b)
	}
	if _, ok := raw["message"]; !ok {
		t.Errorf("expected key message, got %s", b)
	}
	if len(raw) != 2 {
		t.Errorf("unexpected extra keys in %s", b)
	}
}

// TestValidateTurnErrors covers the schema validation added with Turns:
// empty messages and negative delays must be rejected at load time.
func TestValidateTurnErrors(t *testing.T) {
	validCheck := []Check{{Type: "exit_zero", Command: []string{"true"}}}
	cases := map[string]Task{
		"empty message": {
			ID: "x", Prompt: "p", Checkers: validCheck,
			Turns: []Turn{{DelayS: 3, Message: ""}},
		},
		"negative delay": {
			ID: "x", Prompt: "p", Checkers: validCheck,
			Turns: []Turn{{DelayS: -1, Message: "hello"}},
		},
	}
	for name, task := range cases {
		m := Manifest{Suite: "s", Tasks: []Task{task}}
		if err := m.Validate(); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}

	// The happy path must still validate.
	ok := Manifest{Suite: "s", Tasks: []Task{{
		ID: "x", Prompt: "p", Checkers: validCheck,
		Turns: []Turn{{DelayS: 0, Message: "go faster"}},
	}}}
	if err := ok.Validate(); err != nil {
		t.Errorf("valid turns rejected: %v", err)
	}
}

// TestTurnsTimeoutNotAffected guards a subtle interaction: scheduled turns
// must not change the effective timeout computation.
func TestTurnsTimeoutNotAffected(t *testing.T) {
	task := Task{Turns: []Turn{{DelayS: 5, Message: "m"}}}
	if got := task.Timeout(); got != 10*time.Minute {
		t.Errorf("Timeout with turns = %v, want default 10m", got)
	}
}
