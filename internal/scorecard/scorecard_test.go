package scorecard

import (
	"os"
	"testing"
	"time"

	"github.com/bhodgens/meept-bench/internal/results"
)

func rows() []results.Row {
	base := time.Now().UTC()
	return []results.Row{
		{Suite: "smoke", TaskID: "t1", Attempt: 1, Passed: true, Verdict: "pass", CostUSD: 0.10, WallSeconds: 5, StartedAt: base},
		{Suite: "smoke", TaskID: "t1", Attempt: 2, Passed: false, Verdict: "fail", ErrorKind: "fail", CostUSD: 0.20, WallSeconds: 7, StartedAt: base},
		{Suite: "smoke", TaskID: "t2", Attempt: 1, Passed: false, Verdict: "timeout", ErrorKind: "timeout", CostUSD: 0.30, WallSeconds: 600, StartedAt: base},
	}
}

func TestBuild(t *testing.T) {
	c, err := Build("smoke", rows())
	if err != nil {
		t.Fatal(err)
	}
	if c.Attempts != 3 || c.Tasks != 2 || c.Passes != 1 {
		t.Fatalf("counts wrong: %+v", c)
	}
	if c.PassRate <= 0.32 || c.PassRate >= 0.34 {
		t.Fatalf("pass rate %v", c.PassRate)
	}
	if c.Failures["timeout"] != 1 {
		t.Fatalf("taxonomy wrong: %v", c.Failures)
	}
	if _, _, err := Write(t.TempDir(), c); err != nil {
		t.Fatal(err)
	}
}

func TestWriteEmitsBothFiles(t *testing.T) {
	dir := t.TempDir()
	c, _ := Build("smoke", rows())
	mdPath, jsonPath, err := Write(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{mdPath, jsonPath} {
		if st, err := os.Stat(p); err != nil || st.Size() == 0 {
			t.Fatalf("missing/empty output %s", p)
		}
	}
}
