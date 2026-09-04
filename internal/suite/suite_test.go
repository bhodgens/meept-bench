package suite

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAndValidate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "suite.json")
	content := `{
  "suite": "smoke",
  "tasks": [
    {"id": "t1", "prompt": "write answer.txt containing 42",
     "checkers": [{"type": "file_contains", "files": ["answer.txt"], "pattern": "42"}]}
  ]
}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Suite != "smoke" || len(m.Tasks) != 1 {
		t.Fatalf("unexpected manifest: %+v", m)
	}
	if got := m.Select("t1", nil); len(got) != 1 {
		t.Fatal("filter should match")
	}
	if got := m.Select("nope", nil); len(got) != 0 {
		t.Fatal("filter should not match")
	}
}

func TestSelectIgnoreTags(t *testing.T) {
	m := Manifest{
		Suite: "smoke",
		Tasks: []Task{
			{ID: "stable-a", Prompt: "p", Tags: []string{"regression"}, Checkers: []Check{{Type: "exit_zero", Command: []string{"true"}}}},
			{ID: "flaky-b", Prompt: "p", Tags: []string{"regression", "known-failure"}, Checkers: []Check{{Type: "exit_zero", Command: []string{"true"}}}},
			{ID: "flaky-c", Prompt: "p", Tags: []string{"known-failure", "flaky"}, Checkers: []Check{{Type: "exit_zero", Command: []string{"true"}}}},
			{ID: "untagged-d", Prompt: "p", Checkers: []Check{{Type: "exit_zero", Command: []string{"true"}}}},
		},
	}

	if got := m.Select("", nil); len(got) != 4 {
		t.Fatalf("no ignore list: want 4 tasks, got %d", len(got))
	}

	// ANY-match: a single ignored tag drops every task carrying it.
	if got := m.Select("", []string{"known-failure"}); len(got) != 2 {
		t.Fatalf("ignore known-failure: want 2 tasks, got %+v", got)
	}

	// Multiple tags are ORed; untagged tasks always survive the exclusion.
	got := m.Select("", []string{"known-failure", "flaky"})
	if len(got) != 2 || got[0].ID != "stable-a" || got[1].ID != "untagged-d" {
		t.Fatalf("ignore known-failure,flaky: want stable-a + untagged-d, got %+v", got)
	}

	// Tag exclusion composes with the ID-substring filter: "flaky" matches
	// both flaky tasks, but only flaky-c lacks the ignored tag.
	if got := m.Select("flaky", []string{"regression"}); len(got) != 1 || got[0].ID != "flaky-c" {
		t.Fatalf("filter flaky + ignore regression: want flaky-c, got %+v", got)
	}

	// Ignoring a tag no task carries is a no-op.
	if got := m.Select("", []string{"does-not-exist"}); len(got) != 4 {
		t.Fatalf("ignore absent tag: want 4 tasks, got %d", len(got))
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]Manifest{
		"no suite":   {Tasks: []Task{{ID: "x", Prompt: "p", Checkers: []Check{{Type: "exit_zero", Command: []string{"true"}}}}}},
		"no tasks":   {Suite: "s"},
		"no prompt":  {Suite: "s", Tasks: []Task{{ID: "x", Checkers: []Check{{Type: "exit_zero", Command: []string{"true"}}}}}},
		"no checker": {Suite: "s", Tasks: []Task{{ID: "x", Prompt: "p"}}},
		"bad type":   {Suite: "s", Tasks: []Task{{ID: "x", Prompt: "p", Checkers: []Check{{Type: "wat"}}}}},
		"bad regex":  {Suite: "s", Tasks: []Task{{ID: "x", Prompt: "p", Checkers: []Check{{Type: "file_contains", Files: []string{"f"}, Pattern: "[unclosed"}}}}},
	}
	for name, m := range cases {
		if err := m.Validate(); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
