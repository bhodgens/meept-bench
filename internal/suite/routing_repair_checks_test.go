package suite

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRoutingRepairDeterministicJudge(t *testing.T) {
	_, here, _, _ := runtime.Caller(0)
	script := filepath.Join(filepath.Dir(here), "../../suites/routing-repair-data/judge.py")
	for _, tc := range []struct {
		rule, answer string
		pass         bool
	}{
		{"arithmetic", "408", true},
		{"arithmetic", "17 * 24 = 408.", true},
		{"arithmetic", "409", false},
		{"arithmetic", "It might be 408 or 409", false},
		{"path", "The problem is return width + height; area needs width * height, not addition.", true},
		{"path", "The answer is 408", false},
		{"path", "I cannot inspect the file", false},
		{"unknown", "anything", false},
	} {
		t.Run(tc.rule+tc.answer, func(t *testing.T) {
			cmd := exec.Command("python3", script)
			cmd.Stdin = strings.NewReader("routing-repair:" + tc.rule + "\n---\n" + tc.answer)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("judge failed: %v %s", err, out)
			}
			if strings.HasPrefix(string(out), "1 ") != tc.pass {
				t.Fatalf("got %s want pass=%v", out, tc.pass)
			}
		})
	}
}
