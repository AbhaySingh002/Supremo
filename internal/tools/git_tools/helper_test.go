package git_tools

import (
	"context"
	"errors"
	"testing"
)

func TestIsGitRepositoryHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := IsGitRepository(ctx, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled git check, got %v", err)
	}
}
