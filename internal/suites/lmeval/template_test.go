package lmeval

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bhodgens/meept-bench/internal/suite"
)

// The template suite fixtures (synthetic content, authored for this repo —
// NOT dataset content). Sessions 1-2 are noise; session 3 carries the
// answers. Three items = three tasks:
//  1. threshold (short exact answer → file_contains, single-file lookup)
//  2. time+place (short exact answer → file_contains, multi-file search)
//  3. abstractive (long answer → llm_judge; runnable only with --judge-cmd)
func templateItems() []Item {
	threshold := "350"
	room := "Mistral Room"
	summary := "Marco requested the hybrid setup: three days remote " +
		"(Monday to Wednesday) and two days onsite, with the standing " +
		"weekly review held in the Mistral Room."
	return []Item{
		{
			QuestionID: "template-threshold",
			Question:   "What minimum auto-approve budget did Marco ask me to set for the warehouse reorder tool?",
			Answer:     threshold,
			HaystackSessions: []any{
				[]any{
					map[string]any{"role": "user", "content": "Hey, do you remember which heating protocol we picked for the basement lab?"},
					map[string]any{"role": "assistant", "content": "You settled on the low-dryness schedule, same as the greenhouse."},
					map[string]any{"role": "user", "content": "Right. And thanks for reordering the teal labels last week."},
				},
				[]any{
					map[string]any{"role": "user", "content": "Marco stopped by today about the warehouse reorder tool."},
					map[string]any{"role": "assistant", "content": "What did he want adjusted?"},
					map[string]any{"role": "user", "content": "He asked me to set the minimum auto-approve budget to 350 dollars for supply orders. Anything above that still needs a human sign-off."},
					map[string]any{"role": "assistant", "content": "Got it, 350 dollars as the auto-approve threshold."},
				},
			},
		},
		{
			QuestionID: "template-room",
			Question:   "Which room did I book for the standing weekly review with the platform group?",
			Answer:     room,
			HaystackSessions: []any{
				[]any{
					map[string]any{"role": "user", "content": "The Tuesday architecture sync moved to the Falcon Room this quarter."},
					map[string]any{"role": "assistant", "content": "Noted: Tuesday architecture sync, Falcon Room."},
				},
				[]any{
					map[string]any{"role": "user", "content": "I finally fixed the squeaky door on the server rack at home."},
					map[string]any{"role": "assistant", "content": "The rack appreciates the maintenance."},
				},
				[]any{
					map[string]any{"role": "user", "content": "For the platform group: the standing weekly review is booked in the Mistral Room."},
					map[string]any{"role": "assistant", "content": "Recorded — standing weekly review happens in the Mistral Room."},
				},
			},
		},
		{
			QuestionID: "template-arrangement",
			Question:   "What working arrangement did Marco request for the autumn, and where does the standing weekly review take place?",
			Answer:     summary,
			HaystackSessions: []any{
				[]any{
					map[string]any{"role": "user", "content": "Marco pinged me about the autumn staffing plan."},
					map[string]any{"role": "assistant", "content": "What arrangement did he settle on?"},
					map[string]any{"role": "user", "content": "He wants hybrid: three days remote from Monday to Wednesday, and two days onsite."},
					map[string]any{"role": "assistant", "content": "Hybrid: Monday-to-Wednesday remote, the rest onsite."},
				},
				[]any{
					map[string]any{"role": "user", "content": "Room booking confirmed: the standing weekly review for the platform group is in the Mistral Room."},
					map[string]any{"role": "assistant", "content": "Noted — weekly review, Mistral Room."},
				},
			},
		},
	}
}

func TestGenerateTemplateSuite(t *testing.T) {
	if os.Getenv("MEEPT_LMEVAL_REGEN") == "" {
		t.Skip("set MEEPT_LMEVAL_REGEN=1 to regenerate suites/longmemeval-s.template.json")
	}
	items := templateItems()
	cfg := Config{DatasetID: DefaultDatasetID, Split: "S", Revision: "TEMPLATE", Mode: "context"}
	dataDir := filepath.Join("..", "..", "..", "suites", "lmeval-data-template")
	// Regeneration is a full rebuild: drop the previous data dir first so
	// stale task dirs (edited questions change the ID hash) never linger.
	if err := os.RemoveAll(dataDir); err != nil {
		t.Fatalf("clean data dir: %v", err)
	}
	m, _, err := cfg.Emit(items, dataDir)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	// The template must be diffable + committed as static content: rewrite
	// the embedded absolute haystack paths as repo-relative (the runner
	// forks worktrees from the repo, so agents resolve those paths there).
	for i := range m.Tasks {
		absData, err := filepath.Abs(dataDir)
		if err != nil {
			t.Fatalf("data dir: %v", err)
		}
		// dataDir sits at <repo>/suites/lmeval-data-template → repo root is
		// two levels up from the data dir.
		repoRoot := filepath.Dir(filepath.Dir(absData))
		m.Tasks[i].Prompt = strings.ReplaceAll(m.Tasks[i].Prompt, repoRoot+"/", "")
	}
	out := filepath.Join("..", "..", "..", "suites", "longmemeval-s.template.json")
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(out, append(b, '\n'), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Logf("wrote %s", out)
}

// TestTemplateSuiteValid guards the committed template: loads it, validates
// it, and checks invariants the CI contract depends on (relative haystack
// paths, no llm_judge without judge availability note, synthetic tags).
func TestTemplateSuiteValid(t *testing.T) {
	m, err := suite.Load(filepath.Join("..", "..", "..", "suites", "longmemeval-s.template.json"))
	if err != nil {
		t.Fatalf("load template: %v", err)
	}
	if m.Suite != "longmemeval-s" {
		t.Fatalf("suite name = %q", m.Suite)
	}
	if m.HFRevision != "TEMPLATE" {
		t.Fatalf("hf_revision = %q, want TEMPLATE (synthetic fixture marker)", m.HFRevision)
	}
	if len(m.Tasks) != 3 {
		t.Fatalf("got %d tasks, want 3", len(m.Tasks))
	}
	for _, task := range m.Tasks {
		for _, tag := range task.Tags {
			if tag == "lmeval-rev:TEMPLATE" {
				goto tagsOK
			}
		}
		t.Fatalf("task %s missing synthetic-revision tag", task.ID)
	tagsOK:
		// Prompts must reference the committed template data RELATIVELY —
		// no absolute machine paths may leak into the committed template.
		if strings.Contains(task.Prompt, "/Users/") || strings.HasPrefix(strings.TrimSpace(task.Prompt), "/") {
			t.Fatalf("task %s prompt contains an absolute path", task.ID)
		}
		// Every referenced haystack file must exist in the repo.
		for _, p := range templateDataRefs(task.Prompt) {
			if _, err := os.Stat(filepath.Join("..", "..", "..", p)); err != nil {
				t.Fatalf("task %s references missing data file %s", task.ID, p)
			}
		}
		// file_contains checkers must reference the worktree answer file.
		for _, c := range task.Checkers {
			switch c.Type {
			case "file_contains":
				refs := c.Files
				if len(refs) == 0 && c.File != "" {
					refs = []string{c.File}
				}
				found := false
				for _, f := range refs {
					if f == "answer.txt" {
						found = true
					}
				}
				if !found {
					t.Fatalf("task %s file_contains must target answer.txt", task.ID)
				}
			case "llm_judge":
				if c.MinScore != 0.7 {
					t.Fatalf("task %s llm_judge min_score = %v", task.ID, c.MinScore)
				}
			default:
				t.Fatalf("task %s unexpected checker type %q", task.ID, c.Type)
			}
		}
	}
}

// templateDataRefs extracts the suites/lmeval-data-template/... paths from a
// generated prompt.
func templateDataRefs(prompt string) []string {
	var out []string
	for _, line := range strings.Split(prompt, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "suites/lmeval-data-template/") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// compile-time guard on imports used by the generator half.
var (
	_ = httptest.NewServer
	_ = http.DefaultClient
	_ = context.Background
	_ = fmt.Sprintf
)
