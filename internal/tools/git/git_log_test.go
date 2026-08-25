package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

func TestGitLogDisablesConfiguredSignatureVerifier(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper is a POSIX script")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	root := t.TempDir()
	gitCommand(t, root, "", "init", "-q")
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}
	gitCommand(t, root, "", "add", "file.txt")
	tree := gitCommand(t, root, "", "write-tree")
	commit := strings.Join([]string{
		"tree " + tree,
		"author Test <test@example.com> 1700000000 +0000",
		"committer Test <test@example.com> 1700000000 +0000",
		"gpgsig -----BEGIN PGP SIGNATURE-----",
		" ",
		" fake",
		" -----END PGP SIGNATURE-----",
		"",
		"signed commit",
		"",
	}, "\n")
	hash := gitCommand(t, root, commit, "hash-object", "-t", "commit", "-w", "--stdin")
	gitCommand(t, root, "", "update-ref", "HEAD", hash)

	marker := filepath.Join(root, "verifier-ran")
	verifier := filepath.Join(root, "verify-signature")
	if err := os.WriteFile(verifier, []byte("#!/bin/sh\n: > \"$SUPREMO_GPG_MARKER\"\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SUPREMO_GPG_MARKER", marker)
	gitCommand(t, root, "", "config", "log.showSignature", "true")
	gitCommand(t, root, "", "config", "gpg.program", verifier)

	result, err := (&GitLog{}).Execute(tools.WithWorkspace(context.Background(), root), map[string]any{"directory": ".", "limit": 1})
	if err != nil || result == nil || !result.Success {
		t.Fatalf("git log result=%#v err=%v", result, err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("repository-configured signature verifier executed: %v", err)
	}
}

func gitCommand(t *testing.T, directory, stdin string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	command.Stdin = strings.NewReader(stdin)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}
