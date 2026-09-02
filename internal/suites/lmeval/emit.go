package lmeval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/bhodgens/meept-bench/internal/suite"
)

// maxLiteralAnswer is the gold-answer length under which the item is treated
// as exact-match QA (file_contains on the literal answer) rather than
// abstractive (llm_judge).
const maxLiteralAnswer = 64

// TaskDataDir is the per-task directory name written under the data dir.
const taskDir = "task"

// Emit converts items to a suite manifest plus per-task haystack data.
//
// Mode "context": each task's haystack is written to <dataDir>/<taskID>/
// session-N.txt and the prompt instructs the agent to answer from those
// files, then write its final answer to answer.txt in the worktree root.
// The prompt references the haystack via ABSOLUTE path (generated data is
// gitignored, so it does not exist inside runner-forked worktrees; the
// checker's file pattern still resolves inside the worktree).
//
// Mode "memory": the prompt includes the haystack inline (the agent is
// expected to retain it in daemon memory); tasks get only the answer-file
// instruction. Very token-heavy; template ships "context".
//
// The returned string is the absolute data dir that was used. Emission is
// deterministic for a given (items, dataDir, config) triple: fixed task ID
// scheme, fixed file layout, byte-identical JSON (indented, sorted maps).
func (c Config) Emit(items []Item, dataDir string) (*suite.Manifest, string, error) {
	if len(items) == 0 {
		return nil, "", fmt.Errorf("lmeval: no items to emit")
	}
	mode := c.emitMode()
	if mode != "context" && mode != "memory" {
		return nil, "", fmt.Errorf("lmeval: unknown mode %q (want \"context\" or \"memory\")", mode)
	}
	if dataDir == "" {
		return nil, "", fmt.Errorf("lmeval: dataDir is required")
	}
	absData, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, "", err
	}
	if mode == "context" {
		if err := os.MkdirAll(absData, 0o755); err != nil {
			return nil, "", err
		}
	}

	splitCode := c.splitCode()
	tasks := make([]suite.Task, 0, len(items))
	for i, it := range items {
		id := taskID(splitCode, i, it.Question)
		tags := []string{"lmeval", "split:" + splitCode, "lmeval-rev:" + c.revisionTag()}

		if mode == "context" {
			if len(it.HaystackSessions) == 0 {
				return nil, "", fmt.Errorf("lmeval: item %d (%s) has no haystack sessions", i, it.QuestionID)
			}
			tdir := filepath.Join(absData, id)
			if err := os.MkdirAll(tdir, 0o755); err != nil {
				return nil, "", err
			}
			var paths []string
			for j, sess := range it.HaystackSessions {
				name := fmt.Sprintf("session-%d.txt", j+1)
				if werr := os.WriteFile(filepath.Join(tdir, name), []byte(renderSession(sess)), 0o644); werr != nil {
					return nil, "", werr
				}
				paths = append(paths, filepath.Join(tdir, name))
			}
			tasks = append(tasks, suite.Task{
				ID:          id,
				Prompt:      contextPrompt(paths, it.Question),
				Checkers:    c.checkersFor(it),
				TimeoutS:    900,
				Seeds:       []int64{1},
				Tags:        tags,
				ExpectAgent: "coder", // file-read + file-write task; regression.json shows explicit routing beats classifier guesses on long prompts
			})
			continue
		}

		// memory mode: haystack inline in the prompt.
		var b strings.Builder
		for j, sess := range it.HaystackSessions {
			fmt.Fprintf(&b, "=== session %d ===\n%s\n", j+1, renderSession(sess))
		}
		tasks = append(tasks, suite.Task{
			ID:       id,
			Prompt:   memoryPrompt(b.String(), it.Question),
			Checkers: c.checkersFor(it),
			TimeoutS: 900,
			Seeds:    []int64{1},
			Tags:     tags,
		})
	}

	manifest := &suite.Manifest{
		Suite:      "longmemeval-" + strings.ToLower(splitCode),
		Internal:   true, // research benchmark; keep scorecards internal per BENCHMARKS.md rule 4
		HFRevision: c.revisionTag(),
		Tasks:      tasks,
		Meta: map[string]string{
			"dataset_id":   c.DatasetID,
			"mode":         mode,
			"license":      "MIT",
			"source_split": c.splitOr(),
			"generation":   "adapter",
			"adapter_seed": fmt.Sprintf("%d", c.Seed),
		},
	}
	return manifest, absData, nil
}

// revisionTag is the revision recorded on tasks and manifests. A configured
// Revision is used verbatim; otherwise callers can set Manifest.HFRevision
// from the resolved sha (see EmitResolved).
func (c Config) revisionTag() string {
	return c.Revision
}

// EmitResolved is Emit plus the manifest-level revision: use it when Fetch
// resolved a revision the config did not pin. Task tags record the same
// value so every result row remains attributable even without the manifest.
func EmitResolved(c Config, rev string, items []Item, dataDir string) (*suite.Manifest, string, error) {
	if rev != "" && strings.TrimSpace(c.Revision) == "" {
		c.Revision = rev
	}
	return c.Emit(items, dataDir)
}

// FetchResolved is Fetch plus the revision it resolved or used, so callers
// can stamp manifests/tags even when the config did not pin one.
func (c Config) FetchResolved(ctx context.Context, hc *http.Client) ([]Item, string, error) {
	rev := strings.TrimSpace(c.Revision)
	if rev == "" {
		if hc == nil {
			hc = http.DefaultClient
		}
		var err error
		if rev, err = c.resolveRevision(ctx, hc); err != nil {
			return nil, "", err
		}
	}
	pinned := c
	pinned.Revision = rev
	items, err := pinned.Fetch(ctx, hc)
	return items, rev, err
}

// checkersFor picks the checker strategy per item: short/exact gold answers
// get a file_contains on the literal answer (runs without a judge); longer,
// abstractive answers get llm_judge with an accept-paraphrase rubric.
func (c Config) checkersFor(it Item) []suite.Check {
	ans := strings.TrimSpace(it.Answer)
	if ans == "" {
		return []suite.Check{{
			Type: "llm_judge",
			Rubric: "The agent answered the user's question about the provided " +
				"conversation history. Score 1.0 if the answer is responsive and " +
				"plausibly correct, 0.0 if it is wrong or non-responsive.",
			MinScore: 0.7,
		}}
	}
	if len(ans) <= maxLiteralAnswer && !strings.ContainsAny(ans, "\n\r") {
		return []suite.Check{{
			Type:    "file_contains",
			Files:   []string{"answer.txt"},
			Pattern: regexpQuote(ans),
		}}
	}
	return []suite.Check{{
		Type:     "llm_judge",
		Rubric:   fmt.Sprintf("Does the answer state %q? Accept paraphrase.", ans),
		MinScore: 0.7,
	}}
}

// regexpQuote escapes a literal gold answer for embedding in a Go regexp
// (RE2) pattern, so file_contains matches the answer text verbatim.
func regexpQuote(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		// RE2 metacharacters (all ASCII punctuation that affects parsing).
		case '\\', '.', '+', '*', '?', '(', ')', '|', '[', ']', '{', '}', '^', '$':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// contextPrompt builds the mode-(b) prompt: read the haystack files, answer,
// write the answer file. Shape follows the patterns verified against the
// live daemon (suites/fragments/memory-recall.json findings + smoke.json):
// the file-write instruction LEADS so keyword routing reaches the coder
// agent even when the classifier model is unreachable, the tool is named
// explicitly, and direct:true is requested.
func contextPrompt(paths []string, question string) string {
	var b strings.Builder
	b.WriteString("Create a file named answer.txt in the repository root containing the answer to the question below. Use file_write with the direct:true argument so the file is written to disk immediately.\n\n")
	b.WriteString("First, read these session log files (they contain the user's past conversation history):\n")
	for _, p := range paths {
		fmt.Fprintf(&b, "  %s\n", p)
	}
	b.WriteString("\nQuestion: ")
	b.WriteString(question)
	b.WriteString("\n\nWrite only the answer into answer.txt, as short and direct as possible — usually a name, date, number, or short phrase.")
	return b.String()
}

// memoryPrompt builds the mode-(a) prompt: haystack inline, agent must
// retain it in memory before answering. The deliverable leads so keyword
// routing reaches a file-capable agent (see contextPrompt).
func memoryPrompt(haystack, question string) string {
	var b strings.Builder
	b.WriteString("Create a file named answer.txt in the repository root containing the answer to the question below. Use file_write with the direct:true argument so the file is written to disk immediately.\n\n")
	b.WriteString("Below are several session logs from a user's conversation history. ")
	b.WriteString("Read them carefully and retain the key facts.\n\n")
	b.WriteString(strings.TrimSpace(haystack))
	b.WriteString("\n\nNow, from memory (do not re-read the logs above), answer this question:\n\nQuestion: ")
	b.WriteString(question)
	b.WriteString("\n\nWrite only the answer into answer.txt, as short and direct as possible.")
	return b.String()
}

// renderSession flattens one haystack session (array of chat turns, or a
// string) into plain text. Turn objects vary between dataset revisions, so
// recognized keys are handled and unknown shapes fall back to JSON.
func renderSession(sess any) string {
	switch s := sess.(type) {
	case string:
		return s
	case []any:
		var b strings.Builder
		for _, turn := range s {
			b.WriteString(renderTurn(turn))
			b.WriteString("\n")
		}
		return strings.TrimRight(b.String(), "\n")
	default:
		data, err := jsonMarshal(sess)
		if err != nil {
			return fmt.Sprintf("%v", sess)
		}
		return string(data)
	}
}

// renderTurn renders one chat turn ("role": ..., "content": ...).
func renderTurn(turn any) string {
	m, ok := turn.(map[string]any)
	if !ok {
		return renderSession(turn)
	}
	role := stringField(m, "role", "speaker")
	content := stringField(m, "content", "text", "message")
	if role == "" && content == "" {
		return renderSession(any(m))
	}
	if role == "" {
		return content
	}
	return role + ": " + content
}

func stringField(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return ""
}

// taskID is deterministic: lmeval-<split>-<index>-<8-char question hash>.
// The hash makes IDs stable if items are re-fetched in a different order and
// the index keeps them unique for duplicate questions.
func taskID(split string, i int, question string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(question)))
	return fmt.Sprintf("lmeval-%s-%03d-%s", split, i, hex.EncodeToString(sum[:8]))
}

// dataFileName returns the answer file name for prompts/checkers.
func dataFileName() string { return "answer.txt" }

var _ = dataFileName // reserved for future checker plumbing
