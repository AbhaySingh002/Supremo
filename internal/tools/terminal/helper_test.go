package terminal

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	switch os.Getenv("HELPER_MODE") {
	case "environment-failure":
		_, _ = fmt.Fprint(os.Stdout, os.Getenv("SUPREMO_TEST_ENV"))
		_, _ = fmt.Fprint(os.Stderr, "diagnostic stderr")
		os.Exit(7)
	case "cwd":
		cwd, err := os.Getwd()
		if err != nil {
			os.Exit(2)
		}
		_, _ = fmt.Fprint(os.Stdout, cwd)
		os.Exit(0)
	case "sleep":
		time.Sleep(time.Second)
		os.Exit(0)
	case "tree-parent":
		child := helperCommand(context.Background(), "tree-child", 0)
		child.Env = append(child.Env, "TREE_MARKER="+os.Getenv("TREE_MARKER"))
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		if err := os.WriteFile(os.Getenv("TREE_STARTED"), []byte("started"), 0600); err != nil {
			os.Exit(4)
		}
		time.Sleep(time.Second)
		os.Exit(0)
	case "tree-parent-exit":
		child := helperCommand(context.Background(), "tree-child-release", 0)
		child.Env = append(child.Env,
			"TREE_MARKER="+os.Getenv("TREE_MARKER"),
			"TREE_CHILD_READY="+os.Getenv("TREE_CHILD_READY"),
			"TREE_RELEASE="+os.Getenv("TREE_RELEASE"),
		)
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(os.Getenv("TREE_CHILD_READY")); err == nil {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		if _, err := os.Stat(os.Getenv("TREE_CHILD_READY")); err != nil {
			os.Exit(3)
		}
		if err := os.WriteFile(os.Getenv("TREE_STARTED"), []byte("started"), 0600); err != nil {
			os.Exit(4)
		}
		os.Exit(0)
	case "tree-child":
		time.Sleep(400 * time.Millisecond)
		if err := os.WriteFile(os.Getenv("TREE_MARKER"), []byte("survived"), 0600); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	case "tree-child-release":
		if err := os.WriteFile(os.Getenv("TREE_CHILD_READY"), []byte("ready"), 0600); err != nil {
			os.Exit(5)
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(os.Getenv("TREE_RELEASE")); err == nil {
				if err := os.WriteFile(os.Getenv("TREE_MARKER"), []byte("survived"), 0600); err != nil {
					os.Exit(3)
				}
				os.Exit(0)
			}
			time.Sleep(5 * time.Millisecond)
		}
		os.Exit(0)
	}
	n, _ := strconv.Atoi(os.Getenv("OUTPUT_BYTES"))
	_, _ = io.CopyN(os.Stdout, zeroReader{}, int64(n))
	os.Exit(0)
}

type zeroReader struct{}

func (zeroReader) Read(data []byte) (int, error) {
	for i := range data {
		data[i] = 'x'
	}
	return len(data), nil
}

func helperCommand(ctx context.Context, mode string, bytes int) *exec.Cmd {
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=TestHelperProcess", "--")
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1", "HELPER_MODE="+mode, "OUTPUT_BYTES="+strconv.Itoa(bytes))
	return cmd
}

func TestExecuteCommandWithOutputBoundsAndCancels(t *testing.T) {
	output, err := ExecuteCommandWithOutput(context.Background(), helperCommand(context.Background(), "output", maxCommandOutputBytes+1))
	if err != nil || !output.StdoutTruncated || len(output.Stdout) != maxCommandOutputBytes {
		t.Fatalf("stdout limit: bytes=%d truncated=%t err=%v", len(output.Stdout), output.StdoutTruncated, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	output, err = ExecuteCommandWithOutput(ctx, helperCommand(ctx, "sleep", 0))
	if err != nil || !output.Canceled || output.ExitCode != -2 || strings.Contains(string(output.Stderr), "panic") {
		t.Fatalf("canceled command: output=%#v err=%v", output, err)
	}
}

func TestExecuteCommandCancellationKillsProcessTree(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "child-survived")
	started := filepath.Join(dir, "child-started")
	ctx, cancel := context.WithCancel(context.Background())
	cmd := helperCommand(ctx, "tree-parent", 0)
	cmd.Env = append(cmd.Env, "TREE_MARKER="+marker, "TREE_STARTED="+started)
	go func() {
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(started); err == nil {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		cancel()
	}()
	output, err := ExecuteCommandWithOutput(ctx, cmd)
	if err != nil || !output.Canceled {
		t.Fatalf("cancel process tree: output=%#v err=%v", output, err)
	}
	if _, err := os.Stat(started); err != nil {
		t.Fatalf("descendant never started: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("descendant survived cancellation: %v", err)
	}
}

func TestExecuteCommandNormalExitKillsProcessTree(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "child-survived")
	started := filepath.Join(dir, "child-started")
	ready := filepath.Join(dir, "child-ready")
	release := filepath.Join(dir, "child-release")
	cmd := helperCommand(context.Background(), "tree-parent-exit", 0)
	cmd.Env = append(cmd.Env,
		"TREE_MARKER="+marker,
		"TREE_STARTED="+started,
		"TREE_CHILD_READY="+ready,
		"TREE_RELEASE="+release,
	)
	output, err := ExecuteCommandWithOutput(context.Background(), cmd)
	if err != nil || output.ExitCode != 0 {
		t.Fatalf("normal process-tree cleanup: output=%#v err=%v", output, err)
	}
	if _, err := os.Stat(started); err != nil {
		t.Fatalf("descendant never started: %v", err)
	}
	if _, err := os.Stat(ready); err != nil {
		t.Fatalf("descendant was not ready before its parent exited: %v", err)
	}
	if err := os.WriteFile(release, []byte("release"), 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("descendant survived normal parent exit: %v", err)
	}
}

func TestExecuteCommandIncludesTailAndCommand(t *testing.T) {
	root := t.TempDir()
	ctx := tools.WithWorkspace(context.Background(), root)
	result, err := (&ExecuteCommand{}).Execute(ctx, map[string]any{
		"command": "sh",
		"args":    []string{"-c", "i=1; while [ \"$i\" -le 80 ]; do echo pad-$i; i=$((i+1)); done; echo UNIQUE_TAIL; echo '--- FAIL: TestFoo'; echo 'store.ts:103: boom'"},
	})
	if err != nil || result == nil || !result.Success {
		t.Fatalf("command: %#v %v", result, err)
	}
	tail, _ := result.Data["stdout_tail"].(string)
	full, _ := result.Data["stdout"].(string)
	if !strings.Contains(tail, "UNIQUE_TAIL") || !strings.Contains(full, "UNIQUE_TAIL") {
		t.Fatalf("missing tail in %#v", result.Data)
	}
	if !strings.Contains(fmt.Sprint(result.Data["failed_tests"]), "TestFoo") {
		t.Fatalf("failed tests %#v", result.Data["failed_tests"])
	}
	if !strings.Contains(fmt.Sprint(result.Data["locations"]), "store.ts:103") {
		t.Fatalf("locations %#v", result.Data["locations"])
	}
}

func TestExecuteCommandRejectsWorkingDirectoryOutsideWorkspace(t *testing.T) {
	ctx := tools.WithWorkspace(context.Background(), t.TempDir())
	result, err := (&ExecuteCommand{}).Execute(ctx, map[string]any{
		"command":   "pwd",
		"directory": t.TempDir(),
	})
	if err != nil || result == nil || result.Success || !strings.Contains(result.Message, "inside the workspace") {
		t.Fatalf("outside directory result: %#v, %v", result, err)
	}
}

func TestExecuteCommandPreservesEnvironmentStreamsAndExitCode(t *testing.T) {
	ctx := tools.WithWorkspace(context.Background(), t.TempDir())
	result, err := (&ExecuteCommand{}).Execute(ctx, map[string]any{
		"command": os.Args[0],
		"args":    []string{"-test.run=TestHelperProcess", "--"},
		"environment": map[string]string{
			"GO_WANT_HELPER_PROCESS": "1",
			"HELPER_MODE":            "environment-failure",
			"SUPREMO_TEST_ENV":       "visible",
		},
	})
	if err != nil || result == nil || result.Success || result.Data["exit_code"] != float64(7) {
		t.Fatalf("command result=%#v err=%v", result, err)
	}
	if result.Data["stdout"] != "visible" || result.Data["stderr"] != "diagnostic stderr" || !strings.Contains(result.Message, "exit code 7") {
		t.Fatalf("command diagnostics=%#v message=%q", result.Data, result.Message)
	}
}
