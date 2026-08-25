package filesystem

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/AbhaySingh002/supremo/internal/tools"
)

const (
	maxWholeFileLines = 400
	maxReadRangeLines = 300
)

type ReadFileInput struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
}

type ReadFileOutput struct {
	Path           string `json:"path"`
	Hash           string `json:"hash"`
	TotalLines     int    `json:"total_lines"`
	RequestedRange string `json:"requested_range,omitempty"`
	ReturnedRange  string `json:"returned_range"`
	Content        string `json:"content"`
	Truncated      bool   `json:"truncated,omitempty"`
	Size           int64  `json:"size"`
}

type ReadFile struct{}

func (t *ReadFile) Name() string { return "read_file" }

func (t *ReadFile) Capabilities() tools.CapabilitySet { return tools.CapabilityReadWorkspace }

func (t *ReadFile) Description() string {
	return "Reads a file, optionally by 1-based start_line/end_line. Returns numbered lines, full-file hash, total_lines, and truncation. Localize with search tools first; do not reread a fresh observation."
}

func (t *ReadFile) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Workspace-relative or absolute file path",
			},
			"start_line": map[string]any{
				"type":        "integer",
				"description": "Optional 1-based inclusive start line",
			},
			"end_line": map[string]any{
				"type":        "integer",
				"description": "Optional 1-based inclusive end line",
			},
		},
		"required": []string{"path"},
	}
}

func (t *ReadFile) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	var parsed ReadFileInput
	if err := tools.ParseInput(input, &parsed); err != nil {
		return nil, err
	}
	target, err := ResolveTarget(ctx, parsed.Path)
	if err != nil {
		return recoverableResult("Path cannot be empty or is invalid", nil), nil
	}
	absPath := target.AbsPath
	sessionID := tools.ProgressScopeFromContext(ctx).SessionID

	info, err := tools.PathExists(absPath)
	if err != nil {
		RecordTrustedObservation(ctx, sessionID, target, "read_file", "", true)
		return recoverableResult("File does not exist", map[string]any{"path": target.RelPath}), nil
	}
	if info.IsDir() {
		return recoverableResult("Path is a directory, not a file", map[string]any{"path": target.RelPath}), nil
	}

	hash, err := hashFile(absPath)
	if err != nil {
		return recoverableResult("Failed to hash file: "+err.Error(), nil), nil
	}
	RecordTrustedObservation(ctx, sessionID, target, "read_file", hash, false)
	rel := target.RelPath
	ranged := parsed.StartLine > 0 || parsed.EndLine > 0
	if !ranged && info.Size() > tools.MaxFileBytes {
		out := ReadFileOutput{
			Path: rel, Hash: hash, TotalLines: 0, Size: info.Size(),
			RequestedRange: "all", ReturnedRange: "none", Truncated: true,
		}
		msg := fmt.Sprintf("File is %d bytes; pass start_line/end_line instead of reading the whole file", info.Size())
		return truncatedRead(msg, out), nil
	}
	keep := 0
	if !ranged {
		keep = maxWholeFileLines
	}
	total, lines, err := readLineRange(absPath, parsed.StartLine, parsed.EndLine, keep)
	if err != nil {
		return recoverableResult("Failed to read file: "+err.Error(), nil), nil
	}

	start, end := 1, total
	if ranged {
		start, end = clampRange(parsed.StartLine, parsed.EndLine, total)
	}

	truncated := false
	if !ranged && total > maxWholeFileLines {
		previewEnd := min(total, 80)
		if len(lines) > previewEnd {
			lines = lines[:previewEnd]
		}
		content := capContent(formatNumberedLines(lines, 1))
		out := ReadFileOutput{
			Path: rel, Hash: hash, TotalLines: total, Size: info.Size(),
			RequestedRange: "all", ReturnedRange: fmt.Sprintf("1-%d", previewEnd),
			Content: content, Truncated: true,
		}
		return truncatedRead(fmt.Sprintf("File has %d lines (%d bytes); pass start_line/end_line instead of reading the whole file", total, info.Size()), out), nil
	} else if ranged && end-start+1 > maxReadRangeLines {
		end = start + maxReadRangeLines - 1
		truncated = true
		keepN := end - start + 1
		if keepN < len(lines) {
			lines = lines[:keepN]
		}
	}

	content := formatNumberedLines(lines, start)
	requested := "all"
	if ranged {
		requested = fmt.Sprintf("%d-%d", max(1, parsed.StartLine), parsed.EndLine)
		if parsed.EndLine <= 0 {
			requested = fmt.Sprintf("%d-%d", max(1, parsed.StartLine), total)
		}
	}
	out := ReadFileOutput{
		Path: rel, Hash: hash, TotalLines: total, Size: info.Size(),
		RequestedRange: requested, ReturnedRange: fmt.Sprintf("%d-%d of %d", start, start+len(lines)-1, total),
		Content: content, Truncated: truncated,
	}
	msg := fmt.Sprintf("%s\nlines %s\nhash: %s", rel, out.ReturnedRange, hash[:min(len(hash), 8)])
	result := tools.BuildSerializedToolResult(true, msg, out)
	if result != nil && content != "" {
		result.Preview = msg + "\n\n" + content
	}
	return result, nil
}

func truncatedRead(message string, out ReadFileOutput) *tools.ToolResult {
	result := tools.BuildSerializedToolResult(false, message, out)
	result.Retryable = true
	result.Error = &tools.ToolError{Class: "recoverable", Message: message}
	return result
}

func recoverableResult(message string, data map[string]any) *tools.ToolResult {
	return &tools.ToolResult{
		Success:   false,
		Status:    tools.ToolStatusFailed,
		Message:   message,
		Data:      data,
		Retryable: true,
		Error:     &tools.ToolError{Class: "recoverable", Message: message},
	}
}

func displayPath(ctx context.Context, absPath string) string {
	if root := tools.Workspace(ctx); root != "" {
		if rel, err := filepath.Rel(root, absPath); err == nil {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(absPath)
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

func readLineRange(path string, start, end, maxKeep int) (total int, lines []string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, nil, err
	}
	defer f.Close()
	wantAll := start <= 0 && end <= 0
	if start < 1 {
		start = 1
	}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		total++
		if wantAll {
			if maxKeep <= 0 || len(lines) < maxKeep {
				lines = append(lines, scanner.Text())
			}
			continue
		}
		if total >= start && (end <= 0 || total <= end) {
			lines = append(lines, scanner.Text())
		}
	}
	return total, lines, scanner.Err()
}

func capContent(s string) string {
	const max = 4000
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n…"
}

func clampRange(start, end, total int) (int, int) {
	if start < 1 {
		start = 1
	}
	if end <= 0 || end > total {
		end = total
	}
	if start > total {
		start = total
	}
	if start > end {
		start = end
	}
	return start, end
}

func formatNumberedLines(lines []string, start int) string {
	var b strings.Builder
	for i, line := range lines {
		fmt.Fprintf(&b, "%d | %s\n", start+i, line)
	}
	return strings.TrimRight(b.String(), "\n")
}

func fileHash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
