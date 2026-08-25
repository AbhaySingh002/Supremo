package components

import (
	"strings"
	"testing"
)

func TestPrettyKnownPayloadsAreNotRawJSON(t *testing.T) {
	cases := []string{
		`{"content":"do not show this"}`,
		`{"matches":[{"path":"a.go","line":12}]}`,
		`{"entries":[{"name":"main.go","path":"main.go"}]}`,
		`{"branch":"main","staged":[],"modified":[{"path":"ui/view.go","status":"modified"}],"untracked":[]}`,
		`{"todos":[{"content":"Wire UI","status":"in_progress"},{"content":"Tests","status":"pending"}]}`,
	}
	for _, raw := range cases {
		got := Pretty(raw)
		if strings.TrimSpace(got) == "" {
			t.Fatalf("empty pretty for %s", raw)
		}
		if strings.HasPrefix(strings.TrimSpace(got), "{") {
			t.Fatalf("raw json leaked:\n%s", got)
		}
		if strings.Contains(got, "do not show this") {
			t.Fatalf("file content leaked:\n%s", got)
		}
		if strings.Contains(raw, "main.go") && !strings.Contains(got, "main.go") {
			t.Fatalf("collection row was clipped:\n%s", got)
		}
	}
}
