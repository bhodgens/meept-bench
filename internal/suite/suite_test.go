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
	if got := m.Select("t1"); len(got) != 1 {
		t.Fatal("filter should match")
	}
	if got := m.Select("nope"); len(got) != 0 {
		t.Fatal("filter should not match")
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
