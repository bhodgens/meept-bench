// Package isolation centralizes the bench's guard against the user's live
// meept state dir (~/.meept). Bench runs write memories, tasks, and
// tool-cache entries into the daemon's state dir; a run pointed at the
// user's live daemon pollutes the state of the human running the benchmark
// (BENCH-7391 contamination incident, 2026-10-22: 119 fixture rows had to
// be purged from ~/.meept/memory by hand).
//
// Every code path that can dispatch bench traffic must consult this package:
// cmd/meept-bench (the `run` verb), internal/runner (defense in depth, so a
// new entry point cannot forget the check), and any tools/ or cmd/ probe
// binaries that talk to a daemon socket.
package isolation

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultSocketPath returns the socket path a bench client would use with
// no explicit override: $MEEPT_BENCH_SOCKET if set, else ~/.meept/meept.sock
// (falling back to /tmp/meept/meept.sock when $HOME is unresolvable).
// This mirrors daemonclient.DefaultSocketPath; both read the same env var.
func DefaultSocketPath() string {
	if p := os.Getenv("MEEPT_BENCH_SOCKET"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/meept/meept.sock"
	}
	return filepath.Join(home, ".meept", "meept.sock")
}

// IsUserDaemonSocket reports whether path points at a user-wide meept state
// dir (~/.meept) rather than an isolated scratch rig. Bench runs against
// such a socket write persistent state belonging to the human user of the
// daemon — memories, tasks, and caches — and must not touch it.
func IsUserDaemonSocket(path string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	userStateDir := filepath.Join(home, ".meept")
	return path == filepath.Join(userStateDir, "meept.sock") ||
		strings.HasPrefix(path, userStateDir+string(filepath.Separator))
}

// CheckSocket returns a descriptive error when path resolves inside the
// user's live state dir, nil otherwise. Callers that must hard-stop (CLI
// verbs) print their own message and exit; library callers (runner.New)
// wrap and return the error.
func CheckSocket(path string) error {
	if !IsUserDaemonSocket(path) {
		return nil
	}
	return fmt.Errorf(
		"refusing to talk to the user's live meept daemon: socket %s resolves "+
			"inside the user's state dir (~/.meept), and bench traffic writes "+
			"memories, tasks, and tool-cache entries into it; set "+
			"MEEPT_BENCH_SOCKET to a scratch-rig socket "+
			"(one-command rig: scripts/scratch-rig.sh; see the scratch-rig "+
			"section of docs/RUNBOOK.md)", path)
}
