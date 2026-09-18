package checkers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/bhodgens/meept-bench/internal/suite"
)

func TestTrustedPythonSnapshotAndIsolatedImports(t *testing.T) {
	root, wt := t.TempDir(), t.TempDir()
	source := "import pathlib, json; raise SystemExit(0 if pathlib.Path('fixture.txt').read_text() == 'edited' else 1)"
	sum := sha256.Sum256([]byte(source))
	script := filepath.Join(root, "checker.py")
	os.WriteFile(script, []byte(source), 0600)
	manifest := suite.Manifest{Suite: "trusted", Tasks: []suite.Task{{ID: "probe", Prompt: "edit fixture", Checkers: []suite.Check{{Type: "trusted_python", Script: "checker.py", Hash: hex.EncodeToString(sum[:])}}}}}
	b, _ := json.Marshal(manifest)
	path := filepath.Join(root, "suite.json")
	os.WriteFile(path, b, 0600)
	m, err := suite.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	// Mutating even the source after load must not replace evaluator bytes.
	os.WriteFile(script, []byte("raise SystemExit(0)"), 0600)
	os.WriteFile(filepath.Join(wt, "json.py"), []byte("raise SystemExit(0)"), 0600)
	os.WriteFile(filepath.Join(wt, "fixture.txt"), []byte("wrong"), 0600)
	t.Setenv("PYTHONPATH", wt)
	c := m.Tasks[0].Checkers[0]
	if r := Run(context.Background(), c, wt, "", nil); r.Passed {
		t.Fatalf("mutated evaluator/import passed: %+v", r)
	}
	os.WriteFile(filepath.Join(wt, "fixture.txt"), []byte("edited"), 0600)
	if r := Run(context.Background(), c, wt, "", nil); !r.Passed {
		t.Fatalf("legitimate fixture edit rejected: %+v", r)
	}
	if _, err := suite.Load(path); err == nil {
		t.Fatal("changed source hash accepted")
	}
	c = manifest.Tasks[0].Checkers[0]
	if r := Run(context.Background(), c, wt, "", nil); r.Passed {
		t.Fatal("unloaded checker accepted")
	}
}
