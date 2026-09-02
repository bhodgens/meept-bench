package diff

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/bhodgens/meept-bench/internal/results"
)

// LoadRows reads a results.jsonl file line by line, skipping blank lines.
// Malformed JSON reports the 1-based line number in the error.
func LoadRows(path string) ([]results.Row, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	rows, err := decodeRows(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return rows, nil
}

func decodeRows(r io.Reader) ([]results.Row, error) {
	var rows []results.Row
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024) // rows carry check details; tolerate large lines
	line := 0
	for sc.Scan() {
		line++
		raw := sc.Bytes()
		if len(raw) == 0 {
			continue
		}
		var row results.Row
		if err := json.Unmarshal(raw, &row); err != nil {
			return nil, fmt.Errorf("bad JSON on line %d: %w", line, err)
		}
		rows = append(rows, row)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
