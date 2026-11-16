package checkers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bhodgens/meept-bench/internal/suite"
)

// sessionWorkdirScript is the canonical trusted checker body for the
// session-workdir-bound task, resolved relative to this file
// (internal/checkers -> repo root is two levels up).
const sessionWorkdirScript = "../../suites/checkers/session_workdir_bound_check.py"

// sessionWorkdirScriptFragment is the fragment-local copy. It must stay
// byte-identical to the canonical one (os.OpenRoot refuses to let a fragment
// manifest point outside its own directory, and the fragment runs standalone
// via `make memory-fragments`, so a copy is unavoidable — the test below is
// what keeps the copy honest).
const sessionWorkdirScriptFragment = "../../suites/fragments/checkers/session_workdir_bound_check.py"

// sessionWorkdirManifests are the manifests that carry the task.
var sessionWorkdirManifests = []string{
	"suites/regression.json",
	"suites/fragments/memory-recall.json",
}

// sessionWorkdirCheck loads the real committed trusted checker from the
// regression suite, failing the test if its hash no longer matches the
// manifest that references it.
func sessionWorkdirCheck(t *testing.T) suite.Check {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	m, err := suite.Load(filepath.Join(root, "suites/regression.json"))
	if err != nil {
		t.Fatalf("loading regression suite (trusted checker must resolve): %v", err)
	}
	for _, task := range m.Tasks {
		if task.ID != "session-workdir-bound" {
			continue
		}
		for _, c := range task.Checkers {
			if c.Type == "trusted_python" {
				if c.TrustedScript() == "" {
					t.Fatal("trusted checker body not loaded")
				}
				return c
			}
		}
	}
	t.Fatal("session-workdir-bound has no trusted_python checker")
	return suite.Check{}
}

// TestSessionWorkdirCheckerScriptPinned pins, for every manifest that carries
// the task: the script loads (suite.Load verifies its sha256), and the
// fragment-local copy is byte-identical to the canonical one. suite.Load
// already refuses a drifted hash; failing here names the real problem (the
// script changed but the manifest — or the duplicate — was not updated).
func TestSessionWorkdirCheckerScriptPinned(t *testing.T) {
	canonical, err := os.ReadFile(sessionWorkdirScript)
	if err != nil {
		t.Fatalf("reading %s: %v", sessionWorkdirScript, err)
	}
	sum := sha256.Sum256(canonical)
	t.Logf("canonical checker sha256 = %x", sum)
	if !strings.Contains(string(canonical), "cwd bound to scratch worktree") {
		t.Fatal("checker script does not look like the session-workdir checker")
	}

	fragment, err := os.ReadFile(sessionWorkdirScriptFragment)
	if err != nil {
		t.Fatalf("reading %s: %v", sessionWorkdirScriptFragment, err)
	}
	if !bytes.Equal(canonical, fragment) {
		t.Fatalf(
			"fragment copy %s drifted from canonical %s — keep them identical",
			sessionWorkdirScriptFragment, sessionWorkdirScript,
		)
	}

	// suite.Load resolves each manifest's script relative to that manifest's
	// own directory and verifies its hash, so a drifted digest fails here.
	for _, rel := range sessionWorkdirManifests {
		m, err := suite.Load(filepath.Join(repoRoot(t), rel))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		found := false
		for _, task := range m.Tasks {
			if task.ID != "session-workdir-bound" {
				continue
			}
			for _, c := range task.Checkers {
				if c.Type != "trusted_python" {
					continue
				}
				if c.TrustedScript() == "" {
					t.Fatalf("%s: checker script body not loaded", rel)
				}
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: session-workdir-bound has no trusted_python checker", rel)
		}
	}
}

// repoRoot is the meept-bench checkout this test runs in.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// gitCmd runs git in dir, failing the test on error.
func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// initBenchRepo creates a git repo with one commit and returns its path.
func initBenchRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q")
	gitCmd(t, dir, "config", "user.email", "bench@test")
	gitCmd(t, dir, "config", "user.name", "bench")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "add", "f.txt")
	gitCmd(t, dir, "commit", "-qm", "init")
	return dir
}

// addWorktree forks a linked worktree at path, mirroring what
// isolate.Manager.Create does for every bench attempt.
func addWorktree(t *testing.T, repo, path string) {
	t.Helper()
	gitCmd(t, repo, "worktree", "add", "-q", "-b", "bench/"+filepath.Base(path), path, "HEAD")
}

// writeCwd writes cwd.txt with content into dir.
func writeCwd(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "cwd.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSessionWorkdirCheckerAcceptsRealBoundDirs is the (a) direction: every
// working-directory shape the harness actually produces must PASS. Before the
// fix the scratch-rig shapes failed because the pattern only knew
// `.meept-bench/` and `/tmp/meept-bench/`.
func TestSessionWorkdirCheckerAcceptsRealBoundDirs(t *testing.T) {
	c := sessionWorkdirCheck(t)

	// A real linked worktree of a real repo: the scratch-rig shape.
	repo := initBenchRepo(t)
	wt := t.TempDir() + "/regression-session-workdir-bound-a1"
	addWorktree(t, repo, wt)

	// Real directories on disk, because the checker requires the reported
	// path to exist (a fabricated path is a failure, by design).
	scratchRigPrivate := t.TempDir() + "/worktrees/task-a1"
	if err := os.MkdirAll(scratchRigPrivate, 0o755); err != nil {
		t.Fatal(err)
	}
	scratchRigTmp := t.TempDir() + "/worktrees/task-a1"
	if err := os.MkdirAll(scratchRigTmp, 0o755); err != nil {
		t.Fatal(err)
	}
	homeBench := t.TempDir() + "/task-a1"
	if err := os.MkdirAll(homeBench, 0o755); err != nil {
		t.Fatal(err)
	}
	tmpBench := t.TempDir() + "/task-a1"
	if err := os.MkdirAll(tmpBench, 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		dir  string // cwd the checker runs in (cmd.Dir)
		path string // the path cwd.txt reports
	}{
		{"bound worktree", wt, wt},
		{"scratch rig /private/tmp", wt, scratchRigPrivate},
		{"scratch rig /tmp", wt, scratchRigTmp},
		{"$HOME/.meept-bench", wt, homeBench},
		{"/tmp/meept-bench", wt, tmpBench},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writeCwd(t, tc.dir, tc.path+"\n")
			r := Run(context.Background(), c, tc.dir, "", nil)
			if !r.Passed {
				t.Fatalf("bound dir %q rejected: %+v", tc.path, r)
			}
		})
	}
}

// TestSessionWorkdirCheckerRejectsWrongDirs is the (b) direction: the two
// wrong-directory outcomes the task exists to catch must FAIL, plus the
// malformed-file cases. The old exit_zero grep hardcoded one developer's
// absolute path; these assertions are portable (derived from git + $HOME).
func TestSessionWorkdirCheckerRejectsWrongDirs(t *testing.T) {
	c := sessionWorkdirCheck(t)

	repo := initBenchRepo(t)
	wt := t.TempDir() + "/regression-session-workdir-bound-a1"
	addWorktree(t, repo, wt)

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}

	cases := []struct {
		name    string
		content string
	}{
		{"bench repo checkout", repo + "\n"},
		{"operator home", home + "\n"},
		{"relative path", "worktrees/task-a1\n"},
		{"empty file", ""},
		{"two lines", "/tmp/meept-bench/a\n/tmp/meept-bench/b\n"},
		{"nonexistent absolute path", "/tmp/meept-bench/does-not-exist-xyz\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writeCwd(t, wt, tc.content)
			r := Run(context.Background(), c, wt, "", nil)
			if r.Passed {
				t.Fatalf("wrong cwd %q accepted: %+v", tc.content, r)
			}
			if r.Detail == "" {
				t.Fatalf("failure carries no detail: %+v", r)
			}
		})
	}

	t.Run("cwd.txt missing", func(t *testing.T) {
		dir := t.TempDir() // no cwd.txt written
		r := Run(context.Background(), c, dir, "", nil)
		if r.Passed {
			t.Fatalf("missing cwd.txt accepted: %+v", r)
		}
	})
}

// TestSessionWorkdirCheckerRejectsSymlinkAliasToHome pins the realpath half of
// the rejection: a /tmp spelling of $HOME must not slip past when /tmp is a
// symlink to /private/tmp (macOS) and the home dir is reachable both ways.
func TestSessionWorkdirCheckerRejectsSymlinkAliasToHome(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("/tmp -> /private/tmp symlink alias is a macOS shape")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	// Only meaningful when the alias is real on this host.
	if rp, err := filepath.EvalSymlinks("/tmp"); err != nil || rp != "/private/tmp" {
		t.Skipf("/tmp is not a /private/tmp alias here (resolved %q)", rp)
	}
	c := sessionWorkdirCheck(t)
	wt := t.TempDir()
	writeCwd(t, wt, "/tmp"+home+"\n")
	if r := Run(context.Background(), c, wt, "", nil); r.Passed {
		t.Fatalf("symlinked alias of $HOME accepted: %+v", r)
	}
}
