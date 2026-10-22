package main

import (
	"fmt"
	"os"

	"github.com/bhodgens/meept-bench/internal/isolation"
)

// defaultSocketPath returns the socket path the bench would use with no
// explicit override: $MEEPT_BENCH_SOCKET if set, else ~/.meept/meept.sock.
// Thin wrapper over the shared isolation package (internal/isolation) so
// the CLI guard and the runner's defense-in-depth guard can never drift.
func defaultSocketPath() string { return isolation.DefaultSocketPath() }

// isUserDaemonSocket reports whether path points at a user-wide meept state
// dir (~/.meept) rather than an isolated scratch rig.
func isUserDaemonSocket(path string) bool { return isolation.IsUserDaemonSocket(path) }

// requireIsolatedSocket refuses to run when the bench would talk to the
// user's live meept daemon. Every bench run MUST set MEEPT_BENCH_SOCKET to a
// scratch rig socket (or pass an equivalent override) so benchmark traffic,
// learned memories, and tool-cache writes never land in the user's state dir.
func requireIsolatedSocket() {
	if err := isolation.CheckSocket(defaultSocketPath()); err != nil {
		fmt.Fprintf(os.Stderr, "run: %v\n\nFix: launch (or reuse) an isolated scratch daemon and point the bench at it:\n\n  scripts/scratch-rig.sh            # one-command rig (own home, daemon, ports, teardown)\n  export MEEPT_BENCH_SOCKET=/path/to/scratch/rig/home/.meept/meept.sock\n", err)
		os.Exit(2)
	}
}
