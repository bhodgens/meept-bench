package suite

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// SetupFile is an explicit copy from the manifest directory into a fresh
// worktree. No globs, commands, overwrites, or implicit working-copy sync.
type SetupFile struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	SHA256      string `json:"sha256"`
}

func setupPathOK(path string) bool {
	if !filepath.IsLocal(path) || filepath.Clean(path) != path || path == "." || strings.Contains(path, "\\") {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if strings.EqualFold(part, ".git") {
			return false
		}
	}
	return true
}

func validateSetup(files []SetupFile) error {
	seen := map[string]bool{}
	for i, f := range files {
		if !setupPathOK(f.Source) || !setupPathOK(f.Destination) {
			return fmt.Errorf("setup_files[%d]: source and destination must be clean relative paths outside .git", i)
		}
		key := strings.ToLower(f.Destination)
		if seen[key] {
			return fmt.Errorf("setup_files[%d]: duplicate destination", i)
		}
		seen[key] = true
		b, err := hex.DecodeString(f.SHA256)
		if err != nil || len(b) != sha256.Size || strings.ToLower(f.SHA256) != f.SHA256 {
			return fmt.Errorf("setup_files[%d]: lowercase sha256 is required", i)
		}
	}
	return nil
}

// ApplySetup copies only the selected files, checking their hashes at the time
// of the attempt. os.Root confines source reads and destination writes even
// through symlinks; exclusive creation refuses existing files and symlinks.
// The runner calls this before contacting the daemon.
func (t *Task) ApplySetup(worktree string) error {
	if len(t.SetupFiles) == 0 {
		return nil
	}
	if err := validateSetup(t.SetupFiles); err != nil {
		return err
	}
	if t.setupRoot == "" {
		return fmt.Errorf("setup files require a manifest loaded with suite.Load")
	}
	src, err := os.OpenRoot(t.setupRoot)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenRoot(worktree)
	if err != nil {
		return err
	}
	defer dst.Close()
	for _, f := range t.SetupFiles {
		in, err := src.Open(f.Source)
		if err != nil {
			return fmt.Errorf("setup source %s: %w", f.Source, err)
		}
		st, statErr := in.Stat()
		if statErr != nil || !st.Mode().IsRegular() {
			in.Close()
			return fmt.Errorf("setup source %s is not a regular file", f.Source)
		}
		// Fixtures are deliberately small; cap allocation and reject oversized files.
		data, readErr := io.ReadAll(io.LimitReader(in, 1<<20+1))
		in.Close()
		if readErr != nil {
			return readErr
		}
		if len(data) > 1<<20 {
			return fmt.Errorf("setup source %s exceeds 1 MiB", f.Source)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != f.SHA256 {
			return fmt.Errorf("setup source %s: sha256 mismatch", f.Source)
		}
		// MkdirAll is not available on os.Root in the module's Go 1.24 floor.
		parent := filepath.Dir(f.Destination)
		if parent != "." {
			parts := strings.Split(filepath.ToSlash(parent), "/")
			for i := range parts {
				dir := filepath.Join(parts[:i+1]...)
				if err := dst.Mkdir(dir, 0755); err != nil && !os.IsExist(err) {
					return err
				}
			}
		}
		out, err := dst.OpenFile(f.Destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			return fmt.Errorf("setup destination %s: %w", f.Destination, err)
		}
		_, writeErr := out.Write(data)
		closeErr := out.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
