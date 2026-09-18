package suite

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// TrustedScript returns loader-owned, hash-verified code, never a worktree path.
func (c Check) TrustedScript() string { return c.scriptCode }

func (c *Check) loadTrustedScript(root string) error {
	if c.Type != "trusted_python" {
		return nil
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer r.Close()
	f, err := r.Open(c.Script)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("trusted checker must be a regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return err
	}
	sum := sha256.Sum256(b)
	if len(b) > 1<<20 || hex.EncodeToString(sum[:]) != c.Hash {
		return fmt.Errorf("trusted checker %s: size/hash mismatch", c.Script)
	}
	c.scriptCode = string(b)
	return nil
}
