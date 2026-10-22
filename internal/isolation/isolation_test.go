package isolation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultSocketPathEnvOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	t.Setenv("MEEPT_BENCH_SOCKET", "/tmp/rig/.meept/meept.sock")
	if got := DefaultSocketPath(); got != "/tmp/rig/.meept/meept.sock" {
		t.Errorf("DefaultSocketPath() with env = %q, want env value", got)
	}

	t.Setenv("MEEPT_BENCH_SOCKET", "")
	want := filepath.Join(home, ".meept", "meept.sock")
	if got := DefaultSocketPath(); got != want {
		t.Errorf("DefaultSocketPath() = %q, want %q", got, want)
	}
}

func TestIsUserDaemonSocket(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	tests := []struct {
		path string
		want bool
	}{
		{filepath.Join(home, ".meept", "meept.sock"), true},
		{filepath.Join(home, ".meept", "memory", "x.db"), true}, // anything under the dir
		{filepath.Join(home, "rig", "home", ".meept", "meept.sock"), false},
		{filepath.Join(t.TempDir(), ".meept", "meept.sock"), false},
		{"/tmp/meept/meept.sock", false},
	}
	for _, tt := range tests {
		if got := IsUserDaemonSocket(tt.path); got != tt.want {
			t.Errorf("IsUserDaemonSocket(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestCheckSocket(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	live := filepath.Join(home, ".meept", "meept.sock")
	err := CheckSocket(live)
	if err == nil {
		t.Fatalf("CheckSocket(%q) = nil, want refusal", live)
	}
	if !strings.Contains(err.Error(), "refusing to talk to the user's live meept daemon") {
		t.Errorf("error message missing the refusal phrase: %v", err)
	}
	if !strings.Contains(err.Error(), "MEEPT_BENCH_SOCKET") {
		t.Errorf("error message must name the fix env var: %v", err)
	}

	if err := CheckSocket("/tmp/rig/home/.meept/meept.sock"); err != nil {
		t.Errorf("CheckSocket(scratch rig) = %v, want nil", err)
	}
}

// TestScratchRigScriptGuards pins the one-command scratch rig contract:
// scripts/scratch-rig.sh must exist, be executable, tear down via trap,
// redirect the daemon through MEEPT_HOME, and refuse the live runtime
// ports (8080/8081/8082/8084/8143 on this machine) when probing its own.
func TestScratchRigScriptGuards(t *testing.T) {
	path := filepath.Join(repoRoot(t), "scripts", "scratch-rig.sh")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode()&0o111 == 0 {
		t.Errorf("%s is not executable", path)
	}
	src := string(data)
	for _, want := range []string{
		"trap",                                 // teardown on EXIT
		"MEEPT_HOME",                           // daemon state redirected into the rig
		"MEEPT_BENCH_SOCKET",                   // bench pointed at the rig socket
		"8080", "8081", "8082", "8084", "8143", // avoided ports named
	} {
		if !strings.Contains(src, want) {
			t.Errorf("scratch-rig.sh missing %q", want)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	// internal/isolation -> repo root
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(filepath.Dir(wd))
}
