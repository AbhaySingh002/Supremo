package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoggerLifecycle(t *testing.T) {
	tempDir := t.TempDir()

	cleanup := Init(tempDir)
	defer cleanup()

	Info("Testing info log message with param %d", 42)
	Warn("Testing warn message with key sk-1234567890abcdef1234567890")
	Error("Testing error message with Bearer token_secret_value")

	cleanup()

	if IsEnabled() {
		logPath := filepath.Join(tempDir, ".supremo-dev", "logs", "supremo-debug.log")
		data, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatalf("expected log file to exist in debug build: %v", err)
		}
		content := string(data)
		if !strings.Contains(content, "Testing info log message with param 42") {
			t.Errorf("expected info message in log, got:\n%s", content)
		}
		if !strings.Contains(content, "sk-[REDACTED]") {
			t.Errorf("expected redacted key in log, got:\n%s", content)
		}
		if strings.Contains(content, "token_secret_value") {
			t.Errorf("expected secret token to be redacted, got:\n%s", content)
		}
	} else {
		// In release build, .supremo-dev should never be created
		devDir := filepath.Join(tempDir, ".supremo-dev")
		if _, err := os.Stat(devDir); !os.IsNotExist(err) {
			t.Errorf("release build should never create .supremo-dev directory")
		}
	}
}

func TestLoggerSafeFailure(t *testing.T) {
	// Point to a file as root directory so MkdirAll fails
	tempFile := filepath.Join(t.TempDir(), "not_a_dir")
	if err := os.WriteFile(tempFile, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	cleanup := Init(tempFile)
	defer cleanup()

	// Should not panic or crash
	Info("Safe log message")
	Warn("Safe warn message")
	Error("Safe error message")
}
