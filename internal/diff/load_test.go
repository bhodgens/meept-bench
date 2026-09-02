package diff

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "results.jsonl")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadRowsValid(t *testing.T) {
	path := writeTemp(t, `{"task_id":"t1","attempt":1,"verdict":"pass","passed":true,"wall_seconds":1.5,"cost_usd":0.01}

{"task_id":"t2","attempt":2,"verdict":"fail","passed":false,"wall_seconds":2.5,"cost_usd":0.02}
`)
	rows, err := LoadRows(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d: %+v", len(rows), rows)
	}
	if rows[0].TaskID != "t1" || rows[0].Attempt != 1 || !rows[0].Passed ||
		rows[0].Verdict != "pass" || rows[0].WallSeconds != 1.5 || rows[0].CostUSD != 0.01 {
		t.Errorf("row 0 = %+v", rows[0])
	}
	if rows[1].TaskID != "t2" || rows[1].Attempt != 2 || rows[1].Passed || rows[1].Verdict != "fail" {
		t.Errorf("row 1 = %+v", rows[1])
	}
}

func TestLoadRowsEmpty(t *testing.T) {
	for _, content := range []string{"", "\n", "\n\n"} {
		rows, err := LoadRows(writeTemp(t, content))
		if err != nil {
			t.Fatalf("LoadRows(%q): %v", content, err)
		}
		if len(rows) != 0 {
			t.Fatalf("LoadRows(%q): want 0 rows, got %d", content, len(rows))
		}
	}
}

func TestLoadRowsBadLineReportsLineNumber(t *testing.T) {
	path := writeTemp(t, `{"task_id":"t1","attempt":1,"verdict":"pass","passed":true,"wall_seconds":1}

{"task_id":"t2","attempt":1,"verdict":"fail","passed":false,"wall_seconds":2}
{not valid json
{"task_id":"t3","attempt":1,"verdict":"pass","passed":true,"wall_seconds":3}
`)
	_, err := LoadRows(path)
	if err == nil {
		t.Fatal("want error for malformed line, got nil")
	}
	// Physical lines: 1 valid, 2 blank, 3 valid, 4 malformed, 5 valid.
	if !strings.Contains(err.Error(), "line 4") {
		t.Fatalf("error %q does not mention line 4", err)
	}
}

func TestLoadRowsTrailingGarbageRejected(t *testing.T) {
	path := writeTemp(t, `{"task_id":"t1","verdict":"pass","passed":true} oops
`)
	if _, err := LoadRows(path); err == nil {
		t.Fatal("want error for trailing data after JSON object, got nil")
	}
}

func TestLoadRowsMissingFile(t *testing.T) {
	if _, err := LoadRows(filepath.Join(t.TempDir(), "nope.jsonl")); err == nil {
		t.Fatal("want error for missing file, got nil")
	}
}
