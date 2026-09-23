package backend

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWorkspaceStatusUsesPrivateGitInspection(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	root := t.TempDir()
	if output, err := exec.Command("git", "init", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}
	if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte("new\n"), 0600); err != nil {
		t.Fatal(err)
	}

	status, err := (&Service{workspace: root}).WorkspaceStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Git || !status.Ready || status.Changed != 1 || status.Error != "" {
		t.Fatalf("workspace status = %#v", status)
	}

	notGit, err := (&Service{workspace: t.TempDir()}).WorkspaceStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if notGit.Git || notGit.Ready || notGit.Error != "not a git workspace" {
		t.Fatalf("non-git status = %#v", notGit)
	}
}

func TestWorkspaceChangedCountMatchesPorcelainStates(t *testing.T) {
	if got := workspaceChangedCount(" M working\nM  staged\nMM both\n?? untracked\n"); got != 5 {
		t.Fatalf("changed = %d", got)
	}
}
