package checkers

import (
	"fmt"
	"strings"
)

// minRubricLintLen skips tiny expected strings ("a", "1", "?") that would
// substring-match almost any rubric and turn the lint into noise.
const minRubricLintLen = 2

// lintRubric rejects rubrics that leak the literal expected answer to the
// judge. The llm_judge backend is meant to be blind to the answer; a rubric
// containing it (compared case-insensitively) anchors the score and defeats
// the check. An empty expected disables the lint.
func lintRubric(rubric, expected string) error {
	expected = strings.TrimSpace(expected)
	rubric = strings.TrimSpace(rubric)
	if expected == "" || rubric == "" {
		return nil
	}
	if len([]rune(expected)) < minRubricLintLen {
		return nil
	}
	if strings.Contains(strings.ToLower(rubric), strings.ToLower(expected)) {
		return fmt.Errorf(
			"rubric lint: rubric leaks the literal expected answer %q; judges must stay blind to it — rewrite the rubric in terms of qualities to check, not the answer itself",
			expected)
	}
	return nil
}
