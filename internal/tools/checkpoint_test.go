package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type checkpointTestTool struct {
	name string
	run  func(any) error
}

func (t checkpointTestTool) Name() string        { return t.name }
func (t checkpointTestTool) Description() string { return t.name }
func (t checkpointTestTool) Schema() any         { return map[string]any{} }
func (t checkpointTestTool) Capabilities() CapabilitySet {
	switch t.name {
	case "execute_command":
		return CapabilityWriteWorkspace | CapabilityExecuteProcess
	default:
		return CapabilityWriteWorkspace
	}
}
func (t checkpointTestTool) Execute(_ context.Context, input any) (*ToolResult, error) {
	if err := t.run(input); err != nil {
		return nil, err
	}
	return BuildToolResult(true, "done", nil), nil
}

func checkpointTestManager(t *testing.T, root, name string, run func(any) error) (*Manager, context.Context) {
	t.Helper()
	registry := NewRegistry()
	if err := registry.Register(checkpointTestTool{name: name, run: run}); err != nil {
		t.Fatal(err)
	}
	ctx := WithWorkspace(context.Background(), root)
	ctx = WithApprovalMode(ctx, ApprovalSuperman)
	ctx = WithCheckpointSession(ctx, "chat", true)
	return NewManager(registry), ctx
}

func TestCheckpointRewindsBinaryAndProtectsLaterChanges(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "asset.bin")
	original := []byte{0, 1, 2, 3, 255}
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	manager, ctx := checkpointTestManager(t, root, "write_file", func(any) error {
		if err := os.WriteFile(path, []byte("agent"), 0644); err != nil {
			return err
		}
		return os.Chmod(path, 0644)
	})
	if _, err := manager.Execute(ctx, "write_file", map[string]any{"path": "asset.bin"}); err != nil {
		t.Fatal(err)
	}
	checkpoints, err := ListCheckpoints(root, "chat")
	if err != nil || len(checkpoints) != 1 || checkpoints[0].Files != 1 || checkpoints[0].Partial {
		t.Fatalf("checkpoints=%#v err=%v", checkpoints, err)
	}
	if err := os.WriteFile(path, []byte("user"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Rewind(ctx, root, "chat", checkpoints[0].ID, false); err == nil {
		t.Fatal("rewind overwrote a later user change without confirmation")
	} else {
		var conflict *CheckpointConflictError
		if !errors.As(err, &conflict) || len(conflict.Paths) != 1 {
			t.Fatalf("wrong conflict: %v", err)
		}
	}
	result, err := Rewind(ctx, root, "chat", checkpoints[0].ID, true)
	if err != nil || result.Restored != 1 || result.Backup == nil {
		t.Fatalf("rewind result=%#v err=%v", result, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != string(original) {
		t.Fatalf("binary was not restored: %v %v", data, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("mode was not restored: %v %v", info.Mode().Perm(), err)
	}

	checkpoints, err = ListCheckpoints(root, "chat")
	if err != nil || len(checkpoints) != 1 {
		t.Fatalf("rewind backup missing: %#v %v", checkpoints, err)
	}
	if _, err := Rewind(ctx, root, "chat", checkpoints[0].ID, false); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if string(data) != "user" {
		t.Fatalf("rewind backup did not reverse the rewind: %q", data)
	}
}

func TestCheckpointCoversDirectoriesRenamesAndSymlinks(t *testing.T) {
	root := t.TempDir()
	oldDir := filepath.Join(root, "old")
	if err := os.Mkdir(oldDir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "file.txt"), []byte("before"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file.txt", filepath.Join(oldDir, "link")); err != nil {
		t.Fatal(err)
	}
	newDir := filepath.Join(root, "new")
	manager, ctx := checkpointTestManager(t, root, "rename_file", func(any) error {
		return os.Rename(oldDir, newDir)
	})
	if _, err := manager.Execute(ctx, "rename_file", map[string]any{"old_path": "old", "new_path": "new"}); err != nil {
		t.Fatal(err)
	}
	checkpoints, err := ListCheckpoints(root, "chat")
	if err != nil || len(checkpoints) != 1 {
		t.Fatalf("checkpoints=%#v err=%v", checkpoints, err)
	}
	if _, err := Rewind(ctx, root, "chat", checkpoints[0].ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(newDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("renamed destination survived rewind: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(oldDir, "file.txt"))
	if err != nil || string(data) != "before" {
		t.Fatalf("renamed source was not restored: %q %v", data, err)
	}
	info, err := os.Stat(oldDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0750 {
		t.Fatalf("directory mode was not restored: %v", info.Mode().Perm())
	}
	target, err := os.Readlink(filepath.Join(oldDir, "link"))
	if err != nil || target != "file.txt" {
		t.Fatalf("symlink was not restored: %q %v", target, err)
	}
}

func TestCheckpointRestoresFileReplacedByOutsideSymlink(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file.txt")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	manager, ctx := checkpointTestManager(t, root, "write_file", func(any) error {
		if err := os.Remove(path); err != nil {
			return err
		}
		return os.Symlink(outside, path)
	})
	if _, err := manager.Execute(ctx, "write_file", map[string]any{"path": "file.txt"}); err != nil {
		t.Fatal(err)
	}
	checkpoints, err := ListCheckpoints(root, "chat")
	if err != nil || len(checkpoints) != 1 || checkpoints[0].Files != 1 || checkpoints[0].Partial {
		t.Fatalf("symlink checkpoint=%#v err=%v", checkpoints, err)
	}
	if _, err := Rewind(ctx, root, "chat", checkpoints[0].ID, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "before" {
		t.Fatalf("original file was not restored: %q %v", data, err)
	}
	data, err = os.ReadFile(outside)
	if err != nil || string(data) != "outside" {
		t.Fatalf("outside symlink target changed: %q %v", data, err)
	}
}

func TestBroadCheckpointRestoresDirectoryReplacedByOutsideSymlink(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "dir")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "file.txt"), []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	marker := filepath.Join(outside, "outside.txt")
	if err := os.WriteFile(marker, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	manager, ctx := checkpointTestManager(t, root, "execute_command", func(any) error {
		if err := os.RemoveAll(directory); err != nil {
			return err
		}
		return os.Symlink(outside, directory)
	})
	if _, err := manager.Execute(ctx, "execute_command", map[string]any{"command": "fake", "directory": "."}); err != nil {
		t.Fatal(err)
	}
	checkpoints, err := ListCheckpoints(root, "chat")
	if err != nil || len(checkpoints) != 1 {
		t.Fatalf("broad symlink checkpoint=%#v err=%v", checkpoints, err)
	}
	if _, err := Rewind(ctx, root, "chat", checkpoints[0].ID, false); err == nil {
		t.Fatal("rewind did not require confirmation for an unreachable descendant")
	}
	if _, err := Rewind(ctx, root, "chat", checkpoints[0].ID, true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(directory, "file.txt"))
	if err != nil || string(data) != "before" {
		t.Fatalf("directory contents were not restored: %q %v", data, err)
	}
	data, err = os.ReadFile(marker)
	if err != nil || string(data) != "outside" {
		t.Fatalf("outside directory changed: %q %v", data, err)
	}
}

func TestBroadCheckpointRestoresDirectoryReplacedByFile(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "dir")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "file.txt"), []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	manager, ctx := checkpointTestManager(t, root, "execute_command", func(any) error {
		if err := os.RemoveAll(directory); err != nil {
			return err
		}
		return os.WriteFile(directory, []byte("replacement"), 0600)
	})
	if _, err := manager.Execute(ctx, "execute_command", map[string]any{"command": "fake", "directory": "."}); err != nil {
		t.Fatal(err)
	}
	checkpoints, err := ListCheckpoints(root, "chat")
	if err != nil || len(checkpoints) != 1 {
		t.Fatalf("broad replacement checkpoint=%#v err=%v", checkpoints, err)
	}
	if _, err := Rewind(ctx, root, "chat", checkpoints[0].ID, false); err == nil {
		t.Fatal("rewind did not require confirmation for a descendant below a regular file")
	}
	if _, err := Rewind(ctx, root, "chat", checkpoints[0].ID, true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(directory, "file.txt"))
	if err != nil || string(data) != "before" {
		t.Fatalf("directory contents were not restored: %q %v", data, err)
	}
}

func TestCheckpointRetainsPreimageWhenPostScanIsIncomplete(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "dir")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "file.txt"), []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	manager, ctx := checkpointTestManager(t, root, "write_file", func(any) error {
		if err := os.RemoveAll(directory); err != nil {
			return err
		}
		return os.Symlink(outside, directory)
	})
	if _, err := manager.Execute(ctx, "write_file", map[string]any{"path": "dir/file.txt"}); err != nil {
		t.Fatal(err)
	}
	checkpoints, err := ListCheckpoints(root, "chat")
	if err != nil || len(checkpoints) != 1 || checkpoints[0].Files != 1 || !checkpoints[0].Partial {
		t.Fatalf("incomplete checkpoint=%#v err=%v", checkpoints, err)
	}
	preimage := filepath.Join(checkpointSessionDir(root, "chat"), checkpoints[0].ID, checkpointPreimageName)
	if info, err := os.Stat(preimage); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("incomplete post-scan lost its preimage: info=%v err=%v", info, err)
	}
}

func TestCheckpointBlocksInitialSymlinkTarget(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.txt", filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	calls := 0
	manager, ctx := checkpointTestManager(t, root, "write_file", func(any) error {
		calls++
		return nil
	})
	if _, err := manager.Execute(ctx, "write_file", map[string]any{"path": "link.txt"}); err == nil || !strings.Contains(err.Error(), "checkpoint target is a symlink") || calls != 0 {
		t.Fatalf("initial symlink failed open: err=%v calls=%d", err, calls)
	}
}

func TestBroadCheckpointKeepsExcludedDirectoriesOut(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.txt"), []byte("before"), 0644); err != nil {
		t.Fatal(err)
	}
	dependency := filepath.Join(root, "node_modules", "pkg.js")
	if err := os.MkdirAll(filepath.Dir(dependency), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dependency, []byte("dependency-before"), 0644); err != nil {
		t.Fatal(err)
	}
	manager, ctx := checkpointTestManager(t, root, "execute_command", func(any) error {
		if err := os.WriteFile(filepath.Join(root, "main.txt"), []byte("after"), 0644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(root, "created.txt"), []byte("new"), 0644); err != nil {
			return err
		}
		return os.WriteFile(dependency, []byte("dependency-after"), 0644)
	})
	if _, err := manager.Execute(ctx, "execute_command", map[string]any{"command": "fake"}); err != nil {
		t.Fatal(err)
	}
	checkpoints, err := ListCheckpoints(root, "chat")
	if err != nil || len(checkpoints) != 1 || !checkpoints[0].Partial {
		t.Fatalf("checkpoints=%#v err=%v", checkpoints, err)
	}
	if _, err := Rewind(ctx, root, "chat", checkpoints[0].ID, false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "main.txt"))
	if string(data) != "before" {
		t.Fatalf("eligible file was not restored: %q", data)
	}
	if _, err := os.Stat(filepath.Join(root, "created.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("created file survived rewind: %v", err)
	}
	data, _ = os.ReadFile(dependency)
	if string(data) != "dependency-after" {
		t.Fatalf("excluded dependency was unexpectedly restored: %q", data)
	}
}

func TestBroadCommandRetainsUncoveredEffects(t *testing.T) {
	root := t.TempDir()
	manager, ctx := checkpointTestManager(t, root, "execute_command", func(any) error {
		if err := os.MkdirAll(filepath.Join(root, "dist"), 0755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(root, "dist", "artifact"), []byte("built"), 0644)
	})
	if _, err := manager.Execute(ctx, "execute_command", map[string]any{"command": "go", "args": []string{"build", "./..."}, "directory": "."}); err != nil {
		t.Fatal(err)
	}
	checkpoints, err := ListCheckpoints(root, "chat")
	if err != nil || len(checkpoints) != 1 || !checkpoints[0].Partial || checkpoints[0].Files != 0 || len(checkpoints[0].Warnings) == 0 {
		t.Fatalf("uncovered build checkpoint=%#v err=%v", checkpoints, err)
	}
}

func TestCheckpointPreflightFailureStopsTool(t *testing.T) {
	tests := []struct {
		name       string
		sessionID  string
		path       string
		setup      func(string) error
		wantReason string
	}{
		{name: "invalid session", sessionID: "../chat", path: "file.txt", setup: func(string) error { return nil }, wantReason: "invalid session ID"},
		{name: "unavailable store", sessionID: "chat", path: "file.txt", setup: func(root string) error {
			sessionDir := checkpointSessionDir(root, "chat")
			if err := os.MkdirAll(filepath.Dir(sessionDir), 0700); err != nil {
				return err
			}
			_ = os.RemoveAll(sessionDir)
			return os.WriteFile(sessionDir, []byte("blocked"), 0600)
		}, wantReason: "checkpoint store"},
		{name: "invalid capture scope", sessionID: "chat", path: "../outside.txt", setup: func(string) error { return nil }, wantReason: "path is outside the workspace"},
		{name: "targeted excluded path", sessionID: "chat", path: ".session/private.json", setup: func(string) error { return nil }, wantReason: "Supremo and Git state is excluded"},
		{name: "targeted directory with excluded child", sessionID: "chat", path: ".", setup: func(root string) error {
			return os.Mkdir(filepath.Join(root, ".git"), 0700)
		}, wantReason: "Supremo and Git state is excluded"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := test.setup(root); err != nil {
				t.Fatal(err)
			}
			calls := 0
			registry := NewRegistry()
			if err := registry.Register(checkpointTestTool{name: "write_file", run: func(any) error {
				calls++
				return nil
			}}); err != nil {
				t.Fatal(err)
			}
			ctx := WithWorkspace(context.Background(), root)
			ctx = WithApprovalMode(ctx, ApprovalSuperman)
			ctx = WithCheckpointSession(ctx, test.sessionID, true)
			manager := NewManager(registry)
			_, err := manager.Execute(ctx, "write_file", map[string]any{"path": test.path})
			if err == nil || !strings.Contains(err.Error(), "checkpoint preflight failed") || !strings.Contains(err.Error(), test.wantReason) || calls != 0 {
				t.Fatalf("preflight err=%v calls=%d", err, calls)
			}
			activity := manager.Recent()
			if len(activity) != 1 || activity[0].Status != "failed" {
				t.Fatalf("preflight failure activity=%#v", activity)
			}
		})
	}
}

func TestEnabledCheckpointRequiresWorkspaceAndSession(t *testing.T) {
	for _, test := range []struct {
		name      string
		ctx       func() context.Context
		wantError string
	}{
		{
			name: "missing workspace",
			ctx: func() context.Context {
				return WithCheckpointSession(WithApprovalMode(context.Background(), ApprovalSuperman), "chat", true)
			},
			wantError: "missing workspace",
		},
		{
			name: "missing session",
			ctx: func() context.Context {
				return WithCheckpointSession(WithApprovalMode(WithWorkspace(context.Background(), t.TempDir()), ApprovalSuperman), "", true)
			},
			wantError: "missing session ID",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			registry := NewRegistry()
			if err := registry.Register(checkpointTestTool{name: "write_file", run: func(any) error {
				calls++
				return nil
			}}); err != nil {
				t.Fatal(err)
			}
			_, err := NewManager(registry).Execute(test.ctx(), "write_file", map[string]any{"path": "file.txt"})
			if err == nil || !strings.Contains(err.Error(), "checkpoint preflight failed") || !strings.Contains(err.Error(), test.wantError) || calls != 0 {
				t.Fatalf("enabled checkpoint failed open: err=%v calls=%d", err, calls)
			}
		})
	}
}

func TestOversizedCheckpointStopsTool(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "large.bin")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", int(checkpointFileLimit+1))), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	manager, ctx := checkpointTestManager(t, root, "write_file", func(any) error {
		calls++
		return os.WriteFile(path, []byte("changed"), 0600)
	})
	if _, err := manager.Execute(ctx, "write_file", map[string]any{"path": "large.bin"}); err == nil || !strings.Contains(err.Error(), "snapshot limit exceeded") {
		t.Fatalf("oversized preflight error=%v", err)
	}
	data, _ := os.ReadFile(path)
	if calls != 0 || len(data) != int(checkpointFileLimit+1) {
		t.Fatalf("oversized file mutated: calls=%d bytes=%d", calls, len(data))
	}
	if checkpoints, err := ListCheckpoints(root, "chat"); err != nil || len(checkpoints) != 0 {
		t.Fatalf("failed preflight left checkpoints=%#v err=%v", checkpoints, err)
	}
}

func TestBroadDirectoryCheckpointAllowsPreexistingOversizedFiles(t *testing.T) {
	root := t.TempDir()
	largePath := filepath.Join(root, "supremo_bin")
	if err := os.WriteFile(largePath, []byte(strings.Repeat("x", int(checkpointFileLimit+1))), 0755); err != nil {
		t.Fatal(err)
	}
	calls := 0
	manager, ctx := checkpointTestManager(t, root, "execute_command", func(any) error {
		calls++
		return nil
	})
	if _, err := manager.Execute(ctx, "execute_command", map[string]any{"command": "echo", "args": []string{"hello"}, "directory": "."}); err != nil {
		t.Fatalf("expected broad command to succeed despite preexisting oversized binary, got err=%v", err)
	}
	if calls != 1 {
		t.Fatalf("expected tool to execute, calls=%d", calls)
	}
}

func TestRewindBackupPreflightFailureStopsRestore(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "large.bin")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	manager, ctx := checkpointTestManager(t, root, "write_file", func(any) error {
		return os.WriteFile(path, []byte("agent"), 0600)
	})
	if _, err := manager.Execute(ctx, "write_file", map[string]any{"path": "large.bin"}); err != nil {
		t.Fatal(err)
	}
	checkpoints, err := ListCheckpoints(root, "chat")
	if err != nil || len(checkpoints) != 1 {
		t.Fatalf("checkpoints=%#v err=%v", checkpoints, err)
	}
	current := []byte(strings.Repeat("x", int(checkpointFileLimit+1)))
	if err := os.WriteFile(path, current, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Rewind(ctx, root, "chat", checkpoints[0].ID, true); err == nil || !strings.Contains(err.Error(), "rewind backup preflight failed") {
		t.Fatalf("rewind backup error=%v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) != len(current) {
		t.Fatalf("failed backup allowed rewind: bytes=%d err=%v", len(data), err)
	}
}

func TestRewindBackupFinalizationFailureRetainsOriginalCheckpoint(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "restore.txt")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	manager, ctx := checkpointTestManager(t, root, "write_file", func(any) error {
		return os.WriteFile(path, []byte("after"), 0600)
	})
	if _, err := manager.Execute(ctx, "write_file", map[string]any{"path": "restore.txt"}); err != nil {
		t.Fatal(err)
	}
	checkpoints, err := ListCheckpoints(root, "chat")
	if err != nil || len(checkpoints) != 1 {
		t.Fatalf("checkpoints=%#v err=%v", checkpoints, err)
	}
	originalRename := renameCheckpointDirectory
	renameCheckpointDirectory = func(_, _ string) error { return errors.New("forced backup rename failure") }
	t.Cleanup(func() { renameCheckpointDirectory = originalRename })
	result, err := Rewind(ctx, root, "chat", checkpoints[0].ID, false)
	if err == nil || !strings.Contains(err.Error(), "protective backup") || !result.Partial {
		t.Fatalf("rewind result=%#v err=%v", result, err)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil || string(data) != "before" {
		t.Fatalf("rewind did not restore before backup failure: %q, %v", data, readErr)
	}
	remaining, listErr := ListCheckpoints(root, "chat")
	if listErr != nil || len(remaining) != 1 || remaining[0].ID != checkpoints[0].ID {
		t.Fatalf("backup failure discarded original checkpoint: %#v, %v", remaining, listErr)
	}
}

func TestFailedToolStillSavesPartialMutationAndSessionCleanupIsIsolated(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "partial.txt")
	if err := os.WriteFile(path, []byte("before"), 0644); err != nil {
		t.Fatal(err)
	}
	manager, ctx := checkpointTestManager(t, root, "write_file", func(any) error {
		if err := os.WriteFile(path, []byte("partially changed"), 0644); err != nil {
			return err
		}
		return errors.New("tool failed after writing")
	})
	if _, err := manager.Execute(ctx, "write_file", map[string]any{"path": "partial.txt"}); err == nil {
		t.Fatal("test tool unexpectedly succeeded")
	}
	checkpoints, err := ListCheckpoints(root, "chat")
	if err != nil || len(checkpoints) != 1 {
		t.Fatalf("failed tool checkpoint=%#v err=%v", checkpoints, err)
	}
	if other, err := ListCheckpoints(root, "other-chat"); err != nil || len(other) != 0 {
		t.Fatalf("checkpoint leaked into another chat: %#v %v", other, err)
	}
	if _, err := Rewind(ctx, root, "chat", checkpoints[0].ID, false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "before" {
		t.Fatalf("failed tool mutation was not restored: %q", data)
	}
	if err := ClearCheckpoints(root, "chat"); err != nil {
		t.Fatal(err)
	}
	if checkpoints, err := ListCheckpoints(root, "chat"); err != nil || len(checkpoints) != 0 {
		t.Fatalf("checkpoint cleanup failed: %#v %v", checkpoints, err)
	}
}

func TestCanceledToolFinalizesCheckpoint(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "canceled.txt")
	if err := os.WriteFile(path, []byte("before"), 0644); err != nil {
		t.Fatal(err)
	}
	var cancel context.CancelFunc
	manager, base := checkpointTestManager(t, root, "write_file", func(any) error {
		if err := os.WriteFile(path, []byte("partial"), 0644); err != nil {
			return err
		}
		cancel()
		return context.Canceled
	})
	ctx, cancelTask := context.WithCancel(base)
	cancel = cancelTask
	if _, err := manager.Execute(ctx, "write_file", map[string]any{"path": "canceled.txt"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("tool cancellation=%v", err)
	}
	checkpoints, err := ListCheckpoints(root, "chat")
	if err != nil || len(checkpoints) != 1 {
		t.Fatalf("canceled checkpoint=%#v err=%v", checkpoints, err)
	}
	if _, err := Rewind(context.Background(), root, "chat", checkpoints[0].ID, false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "before" {
		t.Fatalf("canceled mutation was not restored: %q", data)
	}
}

func TestCheckpointFinalizationFailureRetainsEvidence(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "evidence.txt")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	var tempDir string
	manager, ctx := checkpointTestManager(t, root, "write_file", func(any) error {
		dir := checkpointSessionDir(root, "chat")
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.IsDir() && strings.HasPrefix(entry.Name(), ".tmp-") {
				tempDir = filepath.Join(dir, entry.Name())
				if err := os.WriteFile(filepath.Join(dir, strings.TrimPrefix(entry.Name(), ".tmp-")), []byte("block rename"), 0600); err != nil {
					return err
				}
				break
			}
		}
		if tempDir == "" {
			return errors.New("checkpoint temp directory not found")
		}
		return os.WriteFile(path, []byte("after"), 0600)
	})
	if _, err := manager.Execute(ctx, "write_file", map[string]any{"path": "evidence.txt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tempDir, checkpointManifestName)); err != nil {
		t.Fatalf("incomplete checkpoint evidence was discarded: %v", err)
	}
	if checkpoints, err := ListCheckpoints(root, "chat"); err != nil || len(checkpoints) != 0 {
		t.Fatalf("incomplete checkpoint should be retained but not offered for rewind: %#v, %v", checkpoints, err)
	}
	if _, err := os.Stat(tempDir); err != nil {
		t.Fatalf("listing checkpoints removed retained evidence: %v", err)
	}
}

func TestPanickingToolFinalizesCheckpoint(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "panic.txt")
	if err := os.WriteFile(path, []byte("before"), 0644); err != nil {
		t.Fatal(err)
	}
	manager, ctx := checkpointTestManager(t, root, "write_file", func(any) error {
		if err := os.WriteFile(path, []byte("partial"), 0644); err != nil {
			return err
		}
		panic("boom")
	})
	var events []Event
	manager.SetReporter(func(event Event) { events = append(events, event) })
	if _, err := manager.Execute(ctx, "write_file", map[string]any{"path": "panic.txt"}); err == nil || !strings.Contains(err.Error(), "panicked: boom") {
		t.Fatalf("tool panic=%v", err)
	}
	var failed, checkpointed bool
	for _, event := range events {
		failed = failed || event.Status == "failed"
		checkpointed = checkpointed || event.Status == "checkpoint"
	}
	if !failed || !checkpointed {
		t.Fatalf("panic lifecycle events=%#v", events)
	}
	checkpoints, err := ListCheckpoints(root, "chat")
	if err != nil || len(checkpoints) != 1 {
		t.Fatalf("panic checkpoints=%#v err=%v", checkpoints, err)
	}
	if _, err := Rewind(ctx, root, "chat", checkpoints[0].ID, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "before" {
		t.Fatalf("panic mutation was not restored: %q %v", data, err)
	}
}

func TestCheckpointRetentionAndCorruptManifestHandling(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "counter.txt")
	if err := os.WriteFile(path, []byte("0"), 0644); err != nil {
		t.Fatal(err)
	}
	counter := 0
	manager, ctx := checkpointTestManager(t, root, "write_file", func(any) error {
		counter++
		return os.WriteFile(path, []byte(strconv.Itoa(counter)), 0644)
	})
	for range checkpointSessionCount + 2 {
		if _, err := manager.Execute(ctx, "write_file", map[string]any{"path": "counter.txt"}); err != nil {
			t.Fatal(err)
		}
	}
	checkpoints, err := ListCheckpoints(root, "chat")
	if err != nil || len(checkpoints) != checkpointSessionCount {
		t.Fatalf("retention kept %d checkpoints, want %d: %v", len(checkpoints), checkpointSessionCount, err)
	}

	corrupt := filepath.Join(checkpointSessionDir(root, "chat"), "corrupt")
	if err := os.Mkdir(corrupt, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(corrupt, checkpointManifestName), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ListCheckpoints(root, "chat"); err == nil || !strings.Contains(err.Error(), "load checkpoint") {
		t.Fatalf("corrupt checkpoint was silently accepted: %v", err)
	}
	beforeCalls := counter
	beforeData, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Execute(ctx, "write_file", map[string]any{"path": "counter.txt"}); err == nil || !strings.Contains(err.Error(), "validate checkpoint store") {
		t.Fatalf("corrupt store did not block mutation: %v", err)
	}
	afterData, err := os.ReadFile(path)
	if err != nil || counter != beforeCalls || string(afterData) != string(beforeData) {
		t.Fatalf("corrupt store mutation ran: calls=%d want=%d before=%q after=%q err=%v", counter, beforeCalls, beforeData, afterData, err)
	}
}

func TestCheckpointStorageIsOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "code.go")
	if err := os.WriteFile(path, []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}
	manager, ctx := checkpointTestManager(t, root, "write_file", func(any) error {
		return os.WriteFile(path, []byte("package main\n// edited"), 0644)
	})
	if _, err := manager.Execute(ctx, "write_file", map[string]any{"path": "code.go"}); err != nil {
		t.Fatal(err)
	}

	// 1. In-workspace .session directory must NOT be created
	if _, err := os.Stat(filepath.Join(root, ".session")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("checkpoint created in-workspace .session directory: %v", err)
	}

	// 2. Checkpoint list succeeds from global storage
	checkpoints, err := ListCheckpoints(root, "chat")
	if err != nil || len(checkpoints) != 1 {
		t.Fatalf("expected 1 checkpoint in global storage: err=%v, checkpoints=%#v", err, checkpoints)
	}

	// 3. Rewind restores cleanly from global storage
	res, err := Rewind(ctx, root, "chat", checkpoints[0].ID, false)
	if err != nil || res.Restored != 1 {
		t.Fatalf("rewind failed: res=%#v, err=%v", res, err)
	}
	restoredData, err := os.ReadFile(path)
	if err != nil || string(restoredData) != "package main" {
		t.Fatalf("unexpected content after rewind: %q, err=%v", restoredData, err)
	}
}

func TestExcludedFrameworkAndBuildDirectories(t *testing.T) {
	excludedDirs := []string{
		".git", ".supremo", ".session", "node_modules", "vendor",
		"dist", "build", "target", ".next", "out", "bin", "obj",
		".turbo", ".cache", "coverage", ".gradle", ".pytest_cache", "__pycache__",
	}
	for _, dir := range excludedDirs {
		reason := excludedCheckpointPath(dir)
		if reason == "" {
			t.Errorf("expected directory %q to be excluded by excludedCheckpointPath, got empty", dir)
		}
		nestedReason := excludedCheckpointPath("src/" + dir + "/subfile.txt")
		if nestedReason == "" {
			t.Errorf("expected nested directory %q to be excluded, got empty", "src/"+dir+"/subfile.txt")
		}
	}
}

func TestBinaryExecutableExcludedFromBroadSnapshot(t *testing.T) {
	root := t.TempDir()
	// Create simulated binary file containing null bytes
	binaryPath := filepath.Join(root, "supremo")
	binaryContent := []byte{0x7f, 'E', 'L', 'F', 0x02, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00}
	binaryContent = append(binaryContent, []byte(strings.Repeat("\x00", 1024))...)
	if err := os.WriteFile(binaryPath, binaryContent, 0755); err != nil {
		t.Fatal(err)
	}

	sourcePath := filepath.Join(root, "main.go")
	if err := os.WriteFile(sourcePath, []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}

	executed := false
	manager, ctx := checkpointTestManager(t, root, "execute_command", func(any) error {
		executed = true
		return os.WriteFile(sourcePath, []byte("package main\n// command ran"), 0644)
	})

	// Broad execution must succeed without preflight error
	if _, err := manager.Execute(ctx, "execute_command", map[string]any{"command": "npm", "args": []string{"install"}, "directory": "."}); err != nil {
		t.Fatalf("broad execution failed on binary artifact: %v", err)
	}
	if !executed {
		t.Fatal("expected command to execute")
	}

	// Normal source file is safely snapshotted and rewindable
	checkpoints, err := ListCheckpoints(root, "chat")
	if err != nil || len(checkpoints) != 1 {
		t.Fatalf("expected checkpoint: err=%v, checkpoints=%#v", err, checkpoints)
	}
	if _, err := Rewind(ctx, root, "chat", checkpoints[0].ID, false); err != nil {
		t.Fatalf("rewind failed: %v", err)
	}
	restored, _ := os.ReadFile(sourcePath)
	if string(restored) != "package main" {
		t.Fatalf("source file not restored: %q", restored)
	}
}

func TestLegacyCheckpointMigrationAndRestore(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "legacy.txt")
	if err := os.WriteFile(sourcePath, []byte("legacy-original"), 0644); err != nil {
		t.Fatal(err)
	}

	// 1. Manually write a legacy checkpoint into <root>/.session/rewind/chat/cp-1/
	legacyCPDir := filepath.Join(root, ".session", "rewind", "chat", "cp-legacy")
	if err := os.MkdirAll(legacyCPDir, 0700); err != nil {
		t.Fatal(err)
	}
	blobName := checkpointBlobName("legacy.txt")
	if err := os.WriteFile(filepath.Join(legacyCPDir, blobName), []byte("legacy-original"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := checkpointManifest{
		Version: checkpointVersion,
		Summary: CheckpointSummary{
			ID:        "cp-legacy",
			CreatedAt: time.Now().UTC().Add(-time.Hour),
			Action:    "write_file legacy.txt",
			Files:     1,
		},
		Entries: []checkpointEntry{
			{
				Path:   "legacy.txt",
				Before: fileState{Kind: "file", Mode: 0644, Size: 15, Hash: "mock"},
				After:  fileState{Kind: "file", Mode: 0644, Size: 14, Hash: "mock2"},
				Blob:   blobName,
			},
		},
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyCPDir, checkpointManifestName), data, 0600); err != nil {
		t.Fatal(err)
	}

	// Modify the workspace file
	if err := os.WriteFile(sourcePath, []byte("legacy-modified"), 0644); err != nil {
		t.Fatal(err)
	}

	// 2. Listing checkpoints should discover the legacy checkpoint
	checkpoints, err := ListCheckpoints(root, "chat")
	if err != nil || len(checkpoints) != 1 || checkpoints[0].ID != "cp-legacy" {
		t.Fatalf("failed to discover legacy checkpoint: err=%v, checkpoints=%#v", err, checkpoints)
	}

	// 3. Rewind should restore the file from legacy storage
	res, err := Rewind(context.Background(), root, "chat", "cp-legacy", true)
	if err != nil || res.Restored != 1 {
		t.Fatalf("legacy rewind failed: res=%#v, err=%v", res, err)
	}
	restored, _ := os.ReadFile(sourcePath)
	if string(restored) != "legacy-original" {
		t.Fatalf("unexpected content after legacy rewind: %q", restored)
	}

	// 4. ClearCheckpoints should remove both legacy and global storage
	if err := ClearCheckpoints(root, "chat"); err != nil {
		t.Fatalf("clear checkpoints failed: %v", err)
	}
	if _, err := os.Stat(legacyCPDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected legacy directory to be removed, got %v", err)
	}
}
