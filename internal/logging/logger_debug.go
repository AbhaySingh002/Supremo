//go:build debug

package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
)

var (
	mu         sync.Mutex
	logFile    *os.File
	logPath    string
	teaLogFile *os.File
)

// Init initializes development logging inside <root>/.supremo-dev/logs/supremo-debug.log.
// It fails safely without panicking if directories or files cannot be created.
func Init(root string) func() {
	mu.Lock()
	defer mu.Unlock()

	if logFile != nil {
		return Close
	}

	if root == "" {
		root = "."
	}

	logsDir := filepath.Join(root, ".supremo-dev", "logs")
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		return func() {}
	}

	logPath = filepath.Join(logsDir, "supremo-debug.log")
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return func() {}
	}

	logFile = file

	// Also redirect Bubble Tea logs to the same destination
	teaLogFile = file
	tea.LogToFile(logPath, "bubbletea")

	writeDirect("[INFO] Logging initialized at " + logPath + "\n")

	return Close
}

// IsEnabled reports whether debug logging is compiled into the binary.
func IsEnabled() bool {
	return true
}

// LogFilePath returns the path to the active log file if enabled.
func LogFilePath() string {
	mu.Lock()
	defer mu.Unlock()
	return logPath
}

func writeDirect(entry string) {
	if logFile == nil {
		return
	}
	_, _ = logFile.WriteString(Redact(entry))
	_ = logFile.Sync()
}

func logMessage(level, format string, args ...any) {
	mu.Lock()
	defer mu.Unlock()

	if logFile == nil {
		return
	}

	msg := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format("2006-01-02 15:04:05.000000")
	line := fmt.Sprintf("[%s] [%s] %s\n", timestamp, level, msg)
	writeDirect(line)
}

// Info logs an informational message.
func Info(format string, args ...any) {
	logMessage("INFO", format, args...)
}

// Debug logs a debug-level message.
func Debug(format string, args ...any) {
	logMessage("DEBUG", format, args...)
}

// Warn logs a warning message.
func Warn(format string, args ...any) {
	logMessage("WARN", format, args...)
}

// Error logs an error message.
func Error(format string, args ...any) {
	logMessage("ERROR", format, args...)
}

// Recover catches panics, logs the stack trace to the log file, and re-panics.
func Recover(contextMsg string) {
	if r := recover(); r != nil {
		stack := string(debug.Stack())
		Error("PANIC in %s: %v\nStack trace:\n%s", contextMsg, r, stack)
		panic(r)
	}
}

// Close closes the active log file safely.
func Close() {
	mu.Lock()
	defer mu.Unlock()

	if logFile != nil {
		writeDirect("[INFO] Logging shut down cleanly\n")
		_ = logFile.Close()
		logFile = nil
	}
}
