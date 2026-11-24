package suite

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTaskToolsCachedFetchParsing(t *testing.T) {
	const raw = `{
	  "suite": "deterministic",
	  "tasks": [
	    {
	      "id": "fetch-cached",
	      "prompt": "Fetch https://example.com and save it.",
	      "tools": {"cached_fetch": true},
	      "checkers": [{"type": "exit_zero", "command": ["true"]}]
	    },
	    {
	      "id": "fetch-live",
	      "prompt": "Fetch https://example.com and save it.",
	      "checkers": [{"type": "exit_zero", "command": ["true"]}]
	    }
	  ]
	}`
	var got Manifest
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got.Tasks[0].Tools == nil || !got.Tasks[0].Tools.CachedFetch {
		t.Errorf("task 0 Tools = %+v, want cached_fetch=true", got.Tasks[0].Tools)
	}
	if got.Tasks[1].Tools != nil {
		t.Errorf("task 1 Tools = %+v, want nil (unset)", got.Tasks[1].Tools)
	}
}

func TestTaskToolsOmittedInJSON(t *testing.T) {
	// omitempty: tasks without tools must serialize without a tools key
	// so committed baseline manifests stay byte-stable.
	b, err := json.Marshal(Task{ID: "x", Prompt: "p", Checkers: []Check{{Type: "exit_zero", Command: []string{"true"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"tools"`) {
		t.Errorf("marshaled task carries tools key: %s", b)
	}
}
