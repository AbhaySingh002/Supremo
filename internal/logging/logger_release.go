//go:build !debug

package logging

// Init is a no-op in release builds. It never creates files or directories.
func Init(root string) func() {
	return func() {}
}

// IsEnabled returns false in release builds.
func IsEnabled() bool {
	return false
}

// LogFilePath returns empty in release builds.
func LogFilePath() string {
	return ""
}

// Info is a no-op in release builds.
func Info(format string, args ...any) {}

// Debug is a no-op in release builds.
func Debug(format string, args ...any) {}

// Warn is a no-op in release builds.
func Warn(format string, args ...any) {}

// Error is a no-op in release builds.
func Error(format string, args ...any) {}

// Recover is a no-op in release builds (re-panics if active).
func Recover(contextMsg string) {
	if r := recover(); r != nil {
		panic(r)
	}
}

// Close is a no-op in release builds.
func Close() {}
