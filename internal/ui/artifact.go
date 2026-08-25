package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/AbhaySingh002/supremo/internal/api"
	"github.com/AbhaySingh002/supremo/internal/ui/composer"
)

type artifactLoadedMsg struct {
	sessionID string
	index     int
	artifact  api.Artifact
	err       error
}

func loadArtifactCmd(ctx context.Context, client api.Client, sessionID string, index int, hash string) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return artifactLoadedMsg{sessionID: sessionID, index: index, err: errors.New("evidence is unavailable")}
		}
		artifact, err := client.GetArtifact(ctx, api.ArtifactRequest{Hash: hash})
		return artifactLoadedMsg{sessionID: sessionID, index: index, artifact: artifact, err: err}
	}
}

func (m Model) latestArtifactIndex() int {
	for index := len(m.entries) - 1; index >= 0; index-- {
		if m.entries[index].artifactID != "" {
			return index
		}
	}
	return -1
}

func (m Model) latestArtifactID() string {
	if index := m.latestArtifactIndex(); index >= 0 {
		return m.entries[index].artifactID
	}
	return ""
}

func evidenceText(artifact api.Artifact) string {
	return evidenceTextForTool("", artifact)
}

func evidenceTextForTool(tool string, artifact api.Artifact) string {
	if !artifact.Previewable {
		return "Evidence was retained, but this binary or oversized result is not shown in the terminal."
	}
	if !composer.IsTextContent(string(artifact.Content)) {
		return "Evidence was retained, but this binary result is not shown in the terminal."
	}
	raw := strings.TrimSpace(string(artifact.Content))
	if raw == "" {
		return "The tool completed without displayable output."
	}
	var value any
	if json.Unmarshal([]byte(raw), &value) != nil {
		if looksLikeRawJSON(raw) {
			return "Structured output was retained but could not be previewed."
		}
		return safeText(raw)
	}
	value = unwrapEvidenceValue(value)
	if formatted := strings.TrimSpace(formatToolValue(tool, value)); formatted != "" {
		return safeText(formatted)
	}
	return safeText(formatEvidenceValue(value, 0))
}

func toolResultDetails(tool, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var value any
	if json.Unmarshal([]byte(raw), &value) != nil {
		if looksLikeRawJSON(raw) {
			return "Structured output retained; open details to inspect it."
		}
		return safeText(raw)
	}
	value = unwrapEvidenceValue(value)
	if formatted := strings.TrimSpace(formatToolValue(tool, value)); formatted != "" {
		return safeText(formatted)
	}
	return safeText(formatEvidenceValue(value, 0))
}

func formatToolValue(tool string, value any) string {
	object, ok := value.(map[string]any)
	if !ok {
		return formatEvidenceValue(value, 0)
	}
	switch toolFamilyFor(tool) {
	case toolCommand:
		return formatCommandValue(object)
	case toolRead:
		return formatReadValue(object)
	case toolList:
		return formatRecordCollection(object["entries"], "entry")
	case toolSearch:
		for _, key := range []string{"matches", "references", "candidates"} {
			if _, exists := object[key]; exists {
				return formatRecordCollection(object[key], "match")
			}
		}
	case toolWrite, toolCreate, toolDelete, toolRename:
		return formatMutationValue(object)
	case toolWeb:
		return formatWebValue(object)
	}
	return formatEvidenceValue(value, 0)
}

func formatCommandValue(value map[string]any) string {
	var lines []string
	if stdout, _ := value["stdout"].(string); strings.TrimSpace(stdout) != "" {
		lines = append(lines, strings.TrimRight(stdout, "\n"))
	}
	if stderr, _ := value["stderr"].(string); strings.TrimSpace(stderr) != "" {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, "stderr:", strings.TrimRight(stderr, "\n"))
	}
	if code, ok := evidenceInt(value["exit_code"]); ok {
		lines = append(lines, fmt.Sprintf("exit %d", code))
	}
	return strings.Join(lines, "\n")
}

func formatReadValue(value map[string]any) string {
	var lines []string
	path, _ := value["path"].(string)
	rangeText, _ := value["returned_range"].(string)
	if path != "" {
		header := path
		if rangeText != "" {
			header += " · lines " + rangeText
		}
		lines = append(lines, header)
	}
	if content, _ := value["content"].(string); strings.TrimSpace(content) != "" {
		lines = append(lines, strings.TrimRight(content, "\n"))
	}
	return strings.Join(lines, "\n")
}

func formatMutationValue(value map[string]any) string {
	var lines []string
	for _, key := range []string{"created_path", "deleted_path"} {
		if path, _ := value[key].(string); path != "" {
			lines = append(lines, strings.ReplaceAll(key, "_", " ")+"  "+path)
		}
	}
	oldPath, _ := value["old_path"].(string)
	newPath, _ := value["new_path"].(string)
	if oldPath != "" || newPath != "" {
		lines = append(lines, strings.TrimSpace(oldPath+" → "+newPath))
	}
	if count, ok := evidenceInt(value["bytes_written"]); ok {
		lines = append(lines, fmt.Sprintf("%d bytes written", count))
	}
	if diff, _ := value["diff"].(string); strings.TrimSpace(diff) != "" {
		lines = append(lines, strings.TrimRight(diff, "\n"))
	}
	return strings.Join(lines, "\n")
}

func formatWebValue(value map[string]any) string {
	var lines []string
	if title, _ := value["title"].(string); title != "" {
		lines = append(lines, title)
	}
	if status, ok := evidenceInt(value["status_code"]); ok {
		lines = append(lines, fmt.Sprintf("HTTP %d", status))
	}
	for _, key := range []string{"content", "body", "text"} {
		if content, _ := value[key].(string); strings.TrimSpace(content) != "" {
			lines = append(lines, strings.TrimRight(content, "\n"))
			break
		}
	}
	return strings.Join(lines, "\n")
}

func formatRecordCollection(value any, singular string) string {
	items, ok := value.([]any)
	if !ok {
		return ""
	}
	if len(items) == 0 {
		return "0 " + singular + "s"
	}
	lines := make([]string, 0, len(items))
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			if line := strings.TrimSpace(formatEvidenceValue(item, 0)); line != "" {
				lines = append(lines, line)
			}
			continue
		}
		pathKeys := []string{"path", "file", "name"}
		if singular == "entry" {
			pathKeys = []string{"name", "path", "file"}
		}
		path := firstEvidenceString(record, pathKeys...)
		if kind, _ := record["type"].(string); kind == "directory" && path != "" && !strings.HasSuffix(path, "/") {
			path += "/"
		}
		if line, ok := evidenceInt(record["line"]); ok && path != "" {
			path += fmt.Sprintf(":%d", line)
		}
		content := firstEvidenceString(record, "content", "context", "label")
		if path != "" && content != "" {
			lines = append(lines, path+"  "+content)
		} else if path != "" {
			lines = append(lines, path)
		} else if content != "" {
			lines = append(lines, content)
		} else if fallback := strings.TrimSpace(formatEvidenceValue(record, 0)); fallback != "" {
			lines = append(lines, fallback)
		}
	}
	return strings.Join(lines, "\n")
}

func firstEvidenceString(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if text, _ := value[key].(string); text != "" {
			return text
		}
	}
	return ""
}

func evidenceInt(value any) (int, bool) {
	switch value := value.(type) {
	case float64:
		return int(value), value == float64(int(value))
	case json.Number:
		n, err := value.Int64()
		return int(n), err == nil
	case int:
		return value, true
	default:
		return 0, false
	}
}

func unwrapEvidenceValue(value any) any {
	if text, ok := value.(string); ok {
		var decoded any
		if json.Unmarshal([]byte(strings.TrimSpace(text)), &decoded) == nil {
			return unwrapEvidenceValue(decoded)
		}
		return value
	}
	object, ok := value.(map[string]any)
	if !ok {
		return value
	}
	for _, key := range []string{"result", "data", "payload"} {
		if nested, exists := object[key]; exists && nested != nil {
			return unwrapEvidenceValue(nested)
		}
	}
	if preview, ok := object["preview"].(string); ok && strings.TrimSpace(preview) != "" {
		return unwrapEvidenceValue(preview)
	}
	for _, key := range []string{"output", "content"} {
		if nested, exists := object[key]; exists {
			switch nested := nested.(type) {
			case map[string]any, []any:
				return unwrapEvidenceValue(nested)
			case string:
				text := strings.TrimSpace(nested)
				if strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[") {
					return unwrapEvidenceValue(nested)
				}
			}
		}
	}
	return value
}

func looksLikeRawJSON(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "{") || strings.HasPrefix(value, "[")
}

func formatEvidenceValue(value any, depth int) string {
	indent := strings.Repeat("  ", depth)
	switch value := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			if !evidenceInternalField(key) {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		lines := make([]string, 0, len(keys))
		for _, key := range keys {
			formatted := formatEvidenceValue(value[key], depth+1)
			label := strings.ReplaceAll(key, "_", " ")
			if isEvidenceScalar(value[key]) {
				lines = append(lines, fmt.Sprintf("%s%s  %s", indent, label, strings.TrimSpace(formatted)))
			} else if strings.TrimSpace(formatted) != "" {
				lines = append(lines, indent+label+"\n"+formatted)
			}
		}
		return strings.Join(lines, "\n")
	case []any:
		lines := make([]string, 0, len(value))
		for _, item := range value {
			formatted := strings.TrimSpace(formatEvidenceValue(item, depth+1))
			if formatted != "" {
				lines = append(lines, indent+"- "+formatted)
			}
		}
		return strings.Join(lines, "\n")
	case string:
		return indent + value
	case nil:
		return indent + "none"
	default:
		return indent + fmt.Sprint(value)
	}
}

func evidenceInternalField(key string) bool {
	switch strings.ToLower(key) {
	case "artifact_id", "affected_entities", "metadata", "tool", "tool_call_id":
		return true
	default:
		return false
	}
}

func isEvidenceScalar(value any) bool {
	switch value.(type) {
	case nil, string, bool, float64, json.Number:
		return true
	default:
		return false
	}
}
