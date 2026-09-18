package suite

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestRoutingRepairManifest(t *testing.T) {
	_, here, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(here), "../../suites/routing-repair.json")
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Suite != "routing-repair" || !m.Internal || len(m.Tasks) != 9 {
		t.Fatalf("wrong suite identity or case count: %+v", m)
	}
	expected := map[string]struct{ agent, intent, forbidden, class, role string }{
		"path-question":            {"", "", "short_message_guard", "arithmetic-path", "negative"},
		"arithmetic-control":       {"", "chat", "", "arithmetic-path", "control"},
		"media-url-as-data":        {"coder", "code", "media_url_guard", "media-data", "negative"},
		"media-transcript-control": {"analyst", "analyze", "", "media-data", "control"},
		"locative-file":            {"coder", "code", "", "locative-time", "negative"},
		"timed-reminder-control":   {"scheduler", "schedule", "", "locative-time", "control"},
		"polite-file-punctuated":   {"coder", "code", "", "polite-prefix", "negative"},
		"polite-file-plain":        {"coder", "code", "", "polite-prefix", "negative"},
		"git-status-control":       {"committer", "git", "", "polite-prefix", "control"},
	}
	seen := map[string]bool{}
	for _, task := range m.Tasks {
		want, ok := expected[task.ID]
		if !ok || seen[task.ID] {
			t.Fatalf("unexpected/duplicate ID %q", task.ID)
		}
		seen[task.ID] = true
		if task.ExpectAgent != want.agent || task.ExpectIntent != want.intent {
			t.Errorf("%s lost routing assertion", task.ID)
		}
		if want.forbidden != "" && (len(task.ForbiddenClassificationMethods) != 1 || task.ForbiddenClassificationMethods[0] != want.forbidden) {
			t.Errorf("%s lost forbidden method", task.ID)
		}
		for _, tag := range []string{"regression", "synthetic", want.class, want.role} {
			if !hasTag(task, tag) {
				t.Errorf("%s missing tag %s", task.ID, tag)
			}
		}
		if task.TimeoutS <= 0 || len(task.Seeds) != 1 || task.Seeds[0] != 1 || len(task.Turns) != 0 || len(task.Checkers) == 0 {
			t.Errorf("%s not a frozen single-turn checked case", task.ID)
		}
		for _, check := range task.Checkers {
			if check.Type == "exit_zero" && len(check.Command) == 1 && check.Command[0] == "true" {
				t.Fatal("placeholder checker")
			}
		}
	}
}
