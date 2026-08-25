package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const maxVisibleDiffBytes = 64 << 10

// fileChange captures only regular UTF-8-ish text files. It is presentation
// data for the interactive UI, never an input to a tool or approval decision.
type fileChange struct {
	beforePath string
	afterPath  string
	beforeName string
	afterName  string
	before     []byte
	beforeOK   bool
}

func captureFileChange(ctx context.Context, desc ToolDescriptor, input any) fileChange {
	if inputValue(input, "old_path") != "" && inputValue(input, "new_path") != "" {
		return fileChangeForPaths(ctx, inputValue(input, "old_path"), inputValue(input, "new_path"))
	}
	if desc.SideEffect == ToolSideEffectWorkspace {
		path := inputValue(input, "path")
		return fileChangeForPaths(ctx, path, path)
	}
	return fileChange{}
}

func fileChangeForPaths(ctx context.Context, beforePath, afterPath string) fileChange {
	if beforePath == "" || afterPath == "" {
		return fileChange{}
	}
	beforeAbs, err := ValidateAndResolvePath(ctx, beforePath)
	if err != nil {
		return fileChange{}
	}
	afterAbs, err := ValidateAndResolvePath(ctx, afterPath)
	if err != nil {
		return fileChange{}
	}
	before, ok := readDiffText(beforeAbs)
	return fileChange{
		beforePath: beforeAbs, afterPath: afterAbs,
		beforeName: workspaceDiffPath(ctx, beforeAbs), afterName: workspaceDiffPath(ctx, afterAbs),
		before: before, beforeOK: ok,
	}
}

func readDiffText(path string) ([]byte, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxVisibleDiffBytes {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil || bytes.IndexByte(data, 0) >= 0 {
		return nil, false
	}
	return data, true
}

func (c fileChange) diff(completed bool) string {
	if !completed || c.afterPath == "" {
		return ""
	}
	after, afterOK := readDiffText(c.afterPath)
	if !c.beforeOK && !afterOK {
		return ""
	}
	if c.beforeOK && afterOK && bytes.Equal(c.before, after) && c.beforePath == c.afterPath {
		return ""
	}
	return unifiedTextDiff(c.beforeName, c.afterName, c.before, after, c.beforeOK, afterOK)
}

func CompactFileDiff(path string, before, after []byte) (diff string, ranges []string, truncated bool) {
	diff = unifiedTextDiff(path, path, before, after, true, true)
	const maxDiff = 8 << 10
	if len(diff) > maxDiff {
		diff = diff[:maxDiff] + "\n… diff truncated"
		truncated = true
	}
	oldLines, newLines := splitDiffLines(string(before)), splitDiffLines(string(after))
	prefix := 0
	for prefix < len(oldLines) && prefix < len(newLines) && oldLines[prefix] == newLines[prefix] {
		prefix++
	}
	if prefix < len(oldLines) || prefix < len(newLines) {
		end := prefix + 1
		if len(newLines) > len(oldLines) {
			end = prefix + 1 + (len(newLines) - len(oldLines))
		}
		ranges = []string{fmt.Sprintf("%d-%d", prefix+1, end)}
	}
	return diff, ranges, truncated
}

func unifiedTextDiff(beforePath, afterPath string, before, after []byte, beforeOK, afterOK bool) string {
	beforeName := "/dev/null"
	if beforeOK {
		beforeName = "a/" + beforePath
	}
	afterName := "/dev/null"
	if afterOK {
		afterName = "b/" + afterPath
	}
	oldLines, newLines := splitDiffLines(string(before)), splitDiffLines(string(after))
	prefix := 0
	for prefix < len(oldLines) && prefix < len(newLines) && oldLines[prefix] == newLines[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(oldLines)-prefix && suffix < len(newLines)-prefix && oldLines[len(oldLines)-1-suffix] == newLines[len(newLines)-1-suffix] {
		suffix++
	}
	oldChanged := oldLines[prefix : len(oldLines)-suffix]
	newChanged := newLines[prefix : len(newLines)-suffix]
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n@@ -%d,%d +%d,%d @@\n", beforeName, afterName, prefix+1, len(oldChanged), prefix+1, len(newChanged))
	for _, line := range oldChanged {
		out.WriteString("-")
		out.WriteString(line)
		out.WriteByte('\n')
	}
	for _, line := range newChanged {
		out.WriteString("+")
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return strings.TrimSuffix(out.String(), "\n")
}

func splitDiffLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

func workspaceDiffPath(ctx context.Context, path string) string {
	if root := Workspace(ctx); root != "" {
		if relative, err := filepath.Rel(root, path); err == nil {
			path = relative
		}
	}
	return filepath.ToSlash(strings.TrimPrefix(path, string(filepath.Separator)))
}
