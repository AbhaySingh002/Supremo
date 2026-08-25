package git

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

func TestGitDiffRejectsOptionTargetsBeforeExecution(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "escaped.diff")
	ctx := tools.WithWorkspace(context.Background(), root)
	for _, target := range []string{"--output=" + output, "--ext-diff", "--textconv"} {
		result, err := (&GitDiff{}).Execute(ctx, map[string]any{"directory": ".", "target": target})
		if err != nil || result == nil || result.Success || !strings.Contains(result.Message, "not an option") {
			t.Fatalf("target %q result=%#v err=%v", target, result, err)
		}
	}
}
