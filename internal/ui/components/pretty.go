package components

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/table"
	"github.com/charmbracelet/x/ansi"
)

// Pretty turns a tool payload into a display string. Known JSON objects never
// render as a raw dump starting with '{'.
func Pretty(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if values, ok := decodeObject(raw); ok {
		if text := prettyObject(values); text != "" {
			return text
		}
	}
	if items, ok := decodeArray(raw); ok {
		if text := prettyArray(items); text != "" {
			return text
		}
	}
	if strings.HasPrefix(raw, "{") || strings.HasPrefix(raw, "[") {
		return "Structured output"
	}
	return raw
}

func prettyObject(values map[string]any) string {
	var lines []string
	if stdout, ok := values["stdout"].(string); ok && strings.TrimSpace(stdout) != "" {
		lines = append(lines, strings.TrimSpace(stdout))
	}
	if stderr, ok := values["stderr"].(string); ok && strings.TrimSpace(stderr) != "" {
		lines = append(lines, strings.TrimSpace(stderr))
	}
	if content, ok := values["content"].(string); ok {
		lines = append(lines, fmt.Sprintf("Read %d bytes", len(content)))
	}
	if n, ok := asInt(values["bytes_written"]); ok {
		lines = append(lines, fmt.Sprintf("Wrote %d bytes", n))
	}
	if path, ok := values["created_path"].(string); ok && path != "" {
		lines = append(lines, "Created "+baseName(path))
	}
	if path, ok := values["deleted_path"].(string); ok && path != "" {
		lines = append(lines, "Deleted "+baseName(path))
	}
	if oldPath, ok := values["old_path"].(string); ok && oldPath != "" {
		if newPath, ok := values["new_path"].(string); ok && newPath != "" {
			lines = append(lines, baseName(oldPath)+" → "+baseName(newPath))
		}
	}
	if diff, ok := values["diff"].(string); ok && diff != "" {
		lines = append(lines, fmt.Sprintf("Diff ready · %d lines", strings.Count(diff, "\n")+1))
	}
	if title, ok := values["title"].(string); ok && title != "" {
		lines = append(lines, "Fetched "+truncate(title, 120))
	}
	if n, ok := asInt(values["status_code"]); ok {
		lines = append(lines, fmt.Sprintf("HTTP %d", n))
	}
	if n, ok := asInt(values["exit_code"]); ok {
		lines = append(lines, fmt.Sprintf("Exit %d", n))
	}
	if branch, ok := values["branch"].(string); ok && branch != "" {
		lines = append(lines, "Branch "+branch)
	}
	for _, key := range []string{"staged", "modified", "untracked"} {
		if table := fileStatusTable(key, values[key]); table != "" {
			lines = append(lines, table)
		}
	}
	if todos := ParseTodos(mustJSON(values)); len(todos) > 0 {
		lines = append(lines, Todos(todos))
	}
	for _, collection := range []struct{ key, label string }{
		{"entries", "entries"},
		{"matches", "matches"},
		{"references", "references"},
		{"commits", "commits"},
		{"tools", "tools"},
	} {
		if table := collectionTable(values[collection.key], collection.label); table != "" {
			lines = append(lines, table)
		}
	}
	if len(lines) > 0 {
		return strings.Join(lines, "\n")
	}
	return kvTable(values)
}

func prettyArray(items []any) string {
	rows := make([][]string, 0, min(32, len(items)))
	for i, item := range items {
		if i >= 32 {
			break
		}
		rows = append(rows, []string{compactValue(item)})
	}
	if len(rows) == 0 {
		return fmt.Sprintf("%d items", len(items))
	}
	return renderTable([]string{"Item"}, rows)
}

func fileStatusTable(label string, raw any) string {
	items, ok := raw.([]any)
	if !ok || len(items) == 0 {
		return ""
	}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		path, _ := row["path"].(string)
		status, _ := row["status"].(string)
		rows = append(rows, []string{baseName(path), status})
	}
	if len(rows) == 0 {
		return ""
	}
	return strings.ToUpper(label[:1]) + label[1:] + "\n" + renderTable([]string{"Path", "Status"}, rows)
}

func collectionTable(raw any, label string) string {
	items, ok := raw.([]any)
	if !ok {
		return ""
	}
	if len(items) == 0 {
		return "0 " + label
	}
	rows := make([][]string, 0, min(16, len(items)))
	for i, item := range items {
		if i >= 16 {
			break
		}
		rows = append(rows, []string{compactValue(item)})
	}
	return fmt.Sprintf("%d %s", len(items), label) + "\n" + renderTable([]string{label}, rows)
}

func kvTable(values map[string]any) string {
	skip := map[string]bool{
		"stdout": true, "stderr": true, "content": true, "todos": true,
		"matches": true, "entries": true, "references": true, "commits": true, "tools": true,
		"staged": true, "modified": true, "untracked": true, "diff": true,
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		if skip[key] {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	rows := make([][]string, 0, len(keys))
	for _, key := range keys {
		rows = append(rows, []string{key, compactValue(values[key])})
	}
	if len(rows) == 0 {
		return "Done"
	}
	return renderTable([]string{"Field", "Value"}, rows)
}

func renderTable(headers []string, rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}
	cols := make([]table.Column, len(headers))
	tableWidth := 0
	for i, header := range headers {
		width := len(header)
		for _, row := range rows {
			if i < len(row) && len(row[i]) > width {
				width = len(row[i])
			}
		}
		if width > 48 {
			width = 48
		}
		if width < 8 {
			width = 8
		}
		cols[i] = table.Column{Title: header, Width: width + 2}
		tableWidth += cols[i].Width + 2
	}
	tableRows := make([]table.Row, len(rows))
	for i, row := range rows {
		cells := make(table.Row, len(headers))
		for j := range headers {
			if j < len(row) {
				cells[j] = truncate(row[j], 48)
			}
		}
		tableRows[i] = cells
	}
	t := table.New(
		table.WithColumns(cols),
		table.WithRows(tableRows),
		table.WithWidth(tableWidth),
		table.WithHeight(len(tableRows)+1),
		table.WithFocused(false),
	)
	return strings.TrimSpace(t.View())
}

func decodeObject(raw string) (map[string]any, bool) {
	if start := strings.Index(raw, "{"); start >= 0 {
		raw = raw[start:]
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil || decoded == nil {
		return nil, false
	}
	return decoded, true
}

func decodeArray(raw string) ([]any, bool) {
	if start := strings.Index(raw, "["); start >= 0 {
		raw = raw[start:]
	}
	var decoded []any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, false
	}
	return decoded, true
}

func compactValue(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return truncate(v, 80)
	case float64:
		if v == float64(int(v)) {
			return strconv.Itoa(int(v))
		}
		return strconv.FormatFloat(v, 'f', 2, 64)
	case bool:
		return strconv.FormatBool(v)
	case []any:
		return fmt.Sprintf("%d items", len(v))
	case map[string]any:
		if name := recordName(v); name != "" {
			return name
		}
		return fmt.Sprintf("%d fields", len(v))
	default:
		return truncate(fmt.Sprint(v), 80)
	}
}

func recordName(values map[string]any) string {
	for _, key := range []string{"name", "path", "file", "symbol", "title", "id"} {
		if text, ok := values[key].(string); ok && text != "" {
			if line, ok := asInt(values["line"]); ok {
				return fmt.Sprintf("%s:%d", baseName(text), line)
			}
			return baseName(text)
		}
	}
	return ""
}

func asInt(value any) (int, bool) {
	switch v := value.(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	case int64:
		return int(v), true
	case json.Number:
		n, err := v.Int64()
		return int(n), err == nil
	default:
		return 0, false
	}
}

func baseName(p string) string {
	return path.Base(strings.TrimRight(strings.TrimSpace(p), "/\\"))
}

func truncate(value string, limit int) string {
	if limit <= 0 {
		return strings.TrimSpace(value)
	}
	return ansi.Truncate(strings.TrimSpace(value), limit, "…")
}

func mustJSON(values map[string]any) string {
	data, err := json.Marshal(values)
	if err != nil {
		return ""
	}
	return string(data)
}

// ToolsTable renders a tool catalog (name, policy, description).
func ToolsTable(rows [][]string) string {
	return renderTable([]string{"Tool", "Policy", "Description"}, rows)
}
