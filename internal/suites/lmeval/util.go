package lmeval

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// pathURL escapes a repo-relative file path for embedding in a URL path
// (each segment percent-escaped, separators preserved).
func pathURL(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// jsonMarshal is deterministic JSON encoding (sorted keys) for hay ItemStack
// fallback rendering, so emitted haystack files are byte-stable.
func jsonMarshal(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("lmeval: marshal: %w", err)
	}
	return b, nil
}
