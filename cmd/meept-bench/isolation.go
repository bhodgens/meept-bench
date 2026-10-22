package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// defaultSocketPath returns the socket path the bench would use with no
// explicit override: $MEEPT_BENCH_SOCKET if set, else ~/.meept/meept.sock.
func defaultSocketPath() string {
	if p := os.Getenv("MEEPT_BENCH_SOCKET"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/meept/meept.sock"
	}
	return filepath.Join(home, ".meept", "meept.sock")
}

// isUserDaemonSocket reports whether path points at a user-wide meept state
// dir (~/.meept) rather than an isolated scratch rig. Bench runs against such
// a socket write memories, tasks, and caches persistent to the human user of
// the daemon — a bench run must not touch them.
func isUserDaemonSocket(path string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	userStateDir := filepath.Join(home, ".meept")
	return path == filepath.Join(userStateDir, "meept.sock") ||
		strings.HasPrefix(path, userStateDir+string(filepath.Separator))
}

// requireIsolatedSocket refuses to run when the bench would talk to the
// user's live meept daemon. Every bench run MUST set MEEPT_BENCH_SOCKET to a
// scratch rig socket (or pass an equivalent override) so benchmark traffic,
// learned memories, and tool-cache writes never land in the user's state dir.
func requireIsolatedSocket() {
	path := defaultSocketPath()
	if !isUserDaemonSocket(path) {
		return
	}
	fmt.Fprintf(os.Stderr, `run: refusing to talk to the user's live meept daemon.

Bench runs write memories, tasks, and tool-cache entries into the daemon's
state dir; pointing them at %s would pollute the state of the human
running the benchmark.

Fix: launch (or reuse) an isolated scratch daemon and point the bench at it:

  export MEEPT_BENCH_SOCKET=/path/to/scratch/rig/home/.meept/meept.sock

See the scratch-rig section of docs/RUNBOOK.md for the full rig recipe
(config, agents dir, provenance.json, health check).
`, path)
	os.Exit(2)
}
