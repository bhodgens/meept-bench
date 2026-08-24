// Package version holds meept-bench build identity.
package version

import "runtime"

// Version is the harness version, not meept's version. Bump on suite-schema changes.
const Version = "0.0.1-scaffold"

// String returns the full identity line including Go platform info.
func String() string {
	return "meept-bench " + Version + " " + runtime.GOOS + "/" + runtime.GOARCH + " " + runtime.Version()
}
