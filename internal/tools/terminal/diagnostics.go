package terminal

import (
	"regexp"
	"strings"
)

const commandTailBytes = 4000

var (
	failTestRe = regexp.MustCompile(`(?m)^--- FAIL: ([^\s]+)`)
	goFailRe   = regexp.MustCompile(`(?m)^FAIL\s+(\S+)`)
	fileLineRe = regexp.MustCompile(`([\w./\\-]+\.(?:go|ts|tsx|js|jsx|py|rs|java)):\d+`)
)

type commandDiagnostics struct {
	Command     string   `json:"command"`
	ExitCode    int      `json:"exit_code"`
	StdoutTail  string   `json:"stdout_tail,omitempty"`
	StderrTail  string   `json:"stderr_tail,omitempty"`
	Truncated   bool     `json:"truncated,omitempty"`
	FailedTests []string `json:"failed_tests,omitempty"`
	Locations   []string `json:"locations,omitempty"`
	Stdout      string   `json:"stdout,omitempty"`
	Stderr      string   `json:"stderr,omitempty"`
}

func diagnoseCommand(command string, args []string, out CommandOutput) commandDiagnostics {
	stdout, stderr := string(out.Stdout), string(out.Stderr)
	combined := stdout + "\n" + stderr
	return commandDiagnostics{
		Command:     strings.TrimSpace(command + " " + strings.Join(args, " ")),
		ExitCode:    out.ExitCode,
		StdoutTail:  tailBytes(stdout, commandTailBytes),
		StderrTail:  tailBytes(stderr, commandTailBytes),
		Truncated:   out.StdoutTruncated || out.StderrTruncated || len(stdout) > commandTailBytes || len(stderr) > commandTailBytes,
		FailedTests: uniqueNames(append(submatches(failTestRe, combined), submatches(goFailRe, combined)...), 8),
		Locations:   uniqueNames(fileLineRe.FindAllString(combined, 12), 12),
		Stdout:      stdout,
		Stderr:      stderr,
	}
}

func tailBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func submatches(re *regexp.Regexp, s string) []string {
	var out []string
	for _, m := range re.FindAllStringSubmatch(s, 8) {
		if len(m) > 1 {
			out = append(out, m[1])
		}
	}
	return out
}

func uniqueNames(values []string, limit int) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range values {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
		if len(out) >= limit {
			break
		}
	}
	return out
}
