package terminal

import (
	"context"
	"io"
	"os"
	"os/exec"
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
	if os.Getenv("HELPER_MODE") == "sleep" {
		time.Sleep(time.Second)
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
