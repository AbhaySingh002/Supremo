package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	DocumentKindObservation   = "observation"
	CurrentObservationVersion = 2
)

// Observation represents a compact, durable tool observation linked to an artifact.
type Observation struct {
	ID              string          `json:"id"`
	SessionID       string          `json:"session_id"`
	TaskID          string          `json:"task_id,omitempty"`
	Tool            string          `json:"tool"`
	CallFingerprint string          `json:"call_fingerprint"`
	CanonicalArgs   json.RawMessage `json:"canonical_args"`
	Scope           string          `json:"scope,omitempty"`
	Path            string          `json:"path,omitempty"`
	Summary         string          `json:"summary"`
	ArtifactID      string          `json:"artifact_id,omitempty"`
	SourceHash      string          `json:"source_hash,omitempty"`
	RepoGeneration  int64           `json:"repo_generation,omitempty"`
	Negative        bool            `json:"negative,omitempty"`
	Version         int             `json:"version,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
}

func (s *Store) SaveObservation(ctx context.Context, obs Observation) (Observation, error) {
	if obs.Tool == "" || obs.CallFingerprint == "" || obs.SessionID == "" {
		return Observation{}, errors.New("tool, call_fingerprint, and session_id are required for observation")
	}
	if obs.ID == "" {
		hash := sha256.Sum256([]byte(obs.CallFingerprint))
		obs.ID = "obs-" + hex.EncodeToString(hash[:8])
	}
	if obs.Version == 0 {
		obs.Version = CurrentObservationVersion
	}
	if obs.CreatedAt.IsZero() {
		obs.CreatedAt = time.Now().UTC()
	}
	payload, err := json.Marshal(obs)
	if err != nil {
		return Observation{}, err
	}
	provenance := Provenance{
		Authority:  AuthorityRuntime,
		ObservedAt: obs.CreatedAt,
	}
	if obs.ArtifactID != "" {
		provenance.EvidenceArtifactIDs = []string{obs.ArtifactID}
	}
	doc, err := s.SaveDocument(ctx, DocumentInput{
		ID:         obs.ID,
		Kind:       DocumentKindObservation,
		SessionID:  obs.SessionID,
		Status:     "active",
		Payload:    payload,
		Provenance: provenance,
		Event: EventInput{
			SessionID: obs.SessionID,
			Type:      "observation.recorded",
			Payload: map[string]any{
				"observation_id":   obs.ID,
				"tool":             obs.Tool,
				"call_fingerprint": obs.CallFingerprint,
				"path":             obs.Path,
				"negative":         obs.Negative,
			},
		},
	})
	if err != nil {
		return Observation{}, err
	}
	var saved Observation
	if err := json.Unmarshal(doc.Payload, &saved); err != nil {
		return obs, nil
	}
	return saved, nil
}

func (s *Store) Observations(ctx context.Context, sessionID, taskID string) ([]Observation, error) {
	docs, err := s.Documents(ctx, DocumentKindObservation, sessionID)
	if err != nil {
		return nil, err
	}
	observations := make([]Observation, 0, len(docs))
	for _, doc := range docs {
		var obs Observation
		if err := json.Unmarshal(doc.Payload, &obs); err != nil {
			continue
		}
		if taskID != "" && obs.TaskID != "" && obs.TaskID != taskID {
			continue
		}
		observations = append(observations, obs)
	}
	return observations, nil
}

func (s *Store) ObservationByFingerprint(ctx context.Context, sessionID, fingerprint string) (Observation, bool, error) {
	docs, err := s.Documents(ctx, DocumentKindObservation, sessionID)
	if err != nil {
		return Observation{}, false, err
	}
	for _, doc := range docs {
		var obs Observation
		if err := json.Unmarshal(doc.Payload, &obs); err != nil {
			continue
		}
		if obs.CallFingerprint == fingerprint {
			return obs, true, nil
		}
	}
	return Observation{}, false, nil
}

// LatestFileObservation returns the latest trusted file observation for a path in a session.
func (s *Store) LatestFileObservation(ctx context.Context, sessionID, relPath string) (Observation, bool, error) {
	if s == nil || sessionID == "" || relPath == "" {
		return Observation{}, false, nil
	}
	docs, err := s.Documents(ctx, DocumentKindObservation, sessionID)
	if err != nil {
		return Observation{}, false, err
	}
	for _, doc := range docs {
		var obs Observation
		if err := json.Unmarshal(doc.Payload, &obs); err != nil {
			continue
		}
		if obs.Path == relPath && (obs.Tool == "read_file" || obs.Tool == "write_file" || obs.Tool == "replace_in_file" || obs.Tool == "create_file" || obs.Tool == "delete_file" || obs.Tool == "rename_file") {
			return obs, true, nil
		}
	}
	return Observation{}, false, nil
}

// NormalizePath converts a path to a clean relative forward-slash path.
func NormalizePath(path, root string) string {
	path = strings.TrimSpace(path)
	if path == "" || path == "." {
		return "."
	}
	path = filepath.Clean(path)
	if root != "" && filepath.IsAbs(path) {
		if rel, err := filepath.Rel(root, path); err == nil {
			path = rel
		}
	}
	if path == "." || path == "" {
		return "."
	}
	return filepath.ToSlash(path)
}

// ComputeCallFingerprint creates a deterministic tool call fingerprint and canonical arguments.
// CallFingerprint = tool name + canonical normalized arguments.
// It deliberately does NOT include source hash or timestamps.
func ComputeCallFingerprint(toolName string, rawArgs any, root string) (string, json.RawMessage, string, string) {
	var argsMap map[string]any
	switch v := rawArgs.(type) {
	case string:
		_ = json.Unmarshal([]byte(v), &argsMap)
	case json.RawMessage:
		_ = json.Unmarshal(v, &argsMap)
	case []byte:
		_ = json.Unmarshal(v, &argsMap)
	case map[string]any:
		argsMap = v
	default:
		data, err := json.Marshal(rawArgs)
		if err == nil {
			_ = json.Unmarshal(data, &argsMap)
		}
	}
	if argsMap == nil {
		argsMap = make(map[string]any)
	}

	canonical := make(map[string]any)
	pathVal, _ := argsMap["path"].(string)
	if pathVal == "" {
		if fileVal, ok := argsMap["file"].(string); ok {
			pathVal = fileVal
		}
	}
	path := NormalizePath(pathVal, root)
	scope := path

	switch toolName {
	case "read_file":
		canonical["path"] = path
		if v := canonicalInt(argsMap["start_line"]); v > 0 {
			canonical["start_line"] = v
		}
		if v := canonicalInt(argsMap["end_line"]); v > 0 {
			canonical["end_line"] = v
		}
	case "list_directory":
		if path == "" {
			path = "."
		}
		canonical["path"] = path
		scope = path
	case "search_file_name":
		if path == "" {
			path = "."
		}
		pattern, _ := argsMap["pattern"].(string)
		canonical["path"] = path
		canonical["pattern"] = pattern
		scope = path
	case "grep_search", "search_text":
		if path == "" {
			path = "."
		}
		pattern, _ := argsMap["pattern"].(string)
		if pattern == "" {
			pattern, _ = argsMap["query"].(string)
		}
		canonical["path"] = path
		canonical["pattern"] = pattern
		canonical["case_sensitive"] = canonicalBool(argsMap["case_sensitive"])
		if glob, _ := argsMap["glob"].(string); glob != "" {
			canonical["glob"] = glob
		}
		if v := canonicalInt(argsMap["max_results"]); v > 0 {
			canonical["max_results"] = v
		}
		if v := canonicalInt(argsMap["context_lines"]); v > 0 {
			canonical["context_lines"] = v
		}
		scope = path
	case "find_symbol", "find_references":
		for k, v := range argsMap {
			canonical[k] = v
		}
		if path == "" {
			if dir, _ := argsMap["directory"].(string); dir != "" {
				path = NormalizePath(dir, root)
			}
		}
		scope = path
	case "repository_query":
		query, _ := argsMap["query"].(string)
		canonical["query"] = query
		if path != "" {
			canonical["path"] = path
		}
		canonical["exact"] = canonicalBool(argsMap["exact"])
		scope = "."
	default:
		for k, v := range argsMap {
			canonical[k] = v
		}
	}

	data, err := json.Marshal(canonical)
	if err != nil {
		data = []byte("{}")
	}
	fingerprint := toolName + ":" + string(data)
	return fingerprint, json.RawMessage(data), path, scope
}

func canonicalInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	default:
		return 0
	}
}

func canonicalBool(v any) bool {
	b, _ := v.(bool)
	return b
}

// extractToolData extracts structured result payload from resultData or rawOutput JSON string.
func extractToolData(resultData map[string]any, rawOutput string) map[string]any {
	if len(resultData) > 0 {
		return resultData
	}
	if strings.TrimSpace(rawOutput) == "" {
		return nil
	}
	var top map[string]any
	if err := json.Unmarshal([]byte(rawOutput), &top); err != nil {
		return nil
	}
	// Check if top-level contains domain fields
	if _, ok := top["entries"]; ok {
		return top
	}
	if _, ok := top["matches"]; ok {
		return top
	}
	if _, ok := top["count"]; ok {
		return top
	}
	if _, ok := top["content"]; ok {
		return top
	}
	if _, ok := top["candidates"]; ok {
		return top
	}
	// Check nested "data"
	if dataMap, ok := top["data"].(map[string]any); ok && len(dataMap) > 0 {
		return dataMap
	}
	// Check nested "result" (from Observation JSON payload)
	if resMap, ok := top["result"].(map[string]any); ok {
		if dataMap, ok := resMap["data"].(map[string]any); ok && len(dataMap) > 0 {
			return dataMap
		}
		if previewStr, ok := resMap["preview"].(string); ok && previewStr != "" {
			var previewMap map[string]any
			if err := json.Unmarshal([]byte(previewStr), &previewMap); err == nil && len(previewMap) > 0 {
				return previewMap
			}
		}
	}
	return nil
}

// ComputeDirectoryFingerprint generates a deterministic SHA256 hash of directory entries.
func ComputeDirectoryFingerprint(absPath string) string {
	entries, err := os.ReadDir(absPath)
	if err != nil {
		return ""
	}
	names := make([]string, 0, len(entries))
	entryMap := make(map[string]os.DirEntry, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
		entryMap[e.Name()] = e
	}
	sort.Strings(names)

	h := sha256.New()
	for _, name := range names {
		e := entryMap[name]
		entryType := "file"
		if e.IsDir() {
			entryType = "dir"
		}
		var size, modTime int64
		if info, err := e.Info(); err == nil {
			size = info.Size()
			modTime = info.ModTime().UnixNano()
		}
		fmt.Fprintf(h, "%s:%s:%d:%d\n", name, entryType, size, modTime)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ExtractObservationSummary extracts concise, high-value technical findings from tool execution results.
func ExtractObservationSummary(toolName, path string, resultData map[string]any, resultSuccess bool, resultMessage string, rawOutput string, root string) (summary string, negative bool, sourceHash string) {
	if !resultSuccess {
		return fmt.Sprintf("%s failed for %q: %s", toolName, path, resultMessage), true, ""
	}

	data := extractToolData(resultData, rawOutput)

	switch toolName {
	case "read_file":
		content := ""
		if data != nil {
			if c, ok := data["content"].(string); ok {
				content = c
			}
			if h, ok := data["hash"].(string); ok {
				sourceHash = h
			}
		}
		if content == "" && root != "" && path != "" {
			abs := filepath.Join(root, path)
			dataBytes, err := os.ReadFile(abs)
			if err == nil {
				content = string(dataBytes)
			}
		}
		if sourceHash == "" && content != "" {
			h := sha256.Sum256([]byte(content))
			sourceHash = hex.EncodeToString(h[:])
		}
		if content == "" {
			return fmt.Sprintf("File %q is empty or not found", path), true, sourceHash
		}
		summary = summarizeFileContent(path, content)
		return summary, false, sourceHash

	case "list_directory":
		var entries []string
		parsed := false
		if data != nil {
			if ents, ok := data["entries"].([]any); ok {
				parsed = true
				for _, e := range ents {
					if name, ok := e.(string); ok {
						entries = append(entries, name)
					} else if m, ok := e.(map[string]any); ok {
						if n, ok := m["name"].(string); ok {
							entries = append(entries, n)
						}
					}
				}
			}
		}
		if !parsed && root != "" && path != "" {
			abs := filepath.Join(root, path)
			if dirEntries, err := os.ReadDir(abs); err == nil {
				parsed = true
				for _, e := range dirEntries {
					entries = append(entries, e.Name())
				}
			}
		}
		if !parsed {
			return fmt.Sprintf("list_directory for %q completed", path), false, ""
		}
		if root != "" && path != "" {
			sourceHash = ComputeDirectoryFingerprint(filepath.Join(root, path))
		}
		if len(entries) == 0 {
			return fmt.Sprintf("Directory %q is empty (0 entries)", path), true, sourceHash
		}
		preview := entries
		if len(preview) > 8 {
			preview = append(preview[:8], "...")
		}
		return fmt.Sprintf("Directory %q (%d entries): %s", path, len(entries), strings.Join(preview, ", ")), false, sourceHash

	case "search_file_name":
		var matches []string
		parsed := false
		if data != nil {
			if m, ok := data["matches"].([]any); ok {
				parsed = true
				for _, match := range m {
					if str, ok := match.(string); ok {
						matches = append(matches, str)
					}
				}
			}
		}
		if !parsed {
			return fmt.Sprintf("search_file_name in %q completed", path), false, ""
		}
		if len(matches) == 0 {
			return fmt.Sprintf("search_file_name in %q: 0 matches (not found)", path), true, ""
		}
		preview := matches
		if len(preview) > 6 {
			preview = append(preview[:6], "...")
		}
		return fmt.Sprintf("search_file_name in %q found %d match(es): %s", path, len(matches), strings.Join(preview, ", ")), false, ""

	case "grep_search", "search_text":
		count := 0
		parsed := false
		if data != nil {
			if c, ok := data["count"].(float64); ok {
				parsed = true
				count = int(c)
			} else if c, ok := data["count"].(int); ok {
				parsed = true
				count = c
			} else if m, ok := data["matches"].([]any); ok {
				parsed = true
				count = len(m)
			}
		}
		if !parsed {
			return fmt.Sprintf("%s in %q completed", toolName, path), false, ""
		}
		if count == 0 {
			return fmt.Sprintf("%s in %q: 0 matches", toolName, path), true, ""
		}
		return fmt.Sprintf("%s in %q: %d match(es)", toolName, path, count), false, ""

	default:
		if resultMessage != "" {
			return resultMessage, false, ""
		}
		return fmt.Sprintf("%s completed successfully", toolName), false, ""
	}
}

// summarizeFileContent produces a 1-2 line concise summary of a file's structure.
func summarizeFileContent(path, content string) string {
	base := filepath.Base(path)
	lines := strings.Split(content, "\n")
	lineCount := len(lines)
	byteCount := len(content)

	switch {
	case base == "package.json":
		var pkg struct {
			Name            string            `json:"name"`
			Dependencies    map[string]string `json:"dependencies"`
			DevDependencies map[string]string `json:"devDependencies"`
			Scripts         map[string]string `json:"scripts"`
		}
		if json.Unmarshal([]byte(content), &pkg) == nil {
			deps := make([]string, 0, len(pkg.Dependencies))
			for d := range pkg.Dependencies {
				deps = append(deps, d)
			}
			sort.Strings(deps)
			if len(deps) > 6 {
				deps = append(deps[:6], "...")
			}
			scripts := make([]string, 0, len(pkg.Scripts))
			for s := range pkg.Scripts {
				scripts = append(scripts, s)
			}
			sort.Strings(scripts)
			return fmt.Sprintf("package.json (%d bytes): name=%q, deps=[%s], scripts=[%s]", byteCount, pkg.Name, strings.Join(deps, ", "), strings.Join(scripts, ", "))
		}
	case base == "tsconfig.json":
		return fmt.Sprintf("tsconfig.json (%d bytes): TypeScript compiler configuration", byteCount)
	case base == "next.config.js" || base == "next.config.mjs" || base == "next.config.ts":
		return fmt.Sprintf("%s (%d bytes): Next.js build configuration", base, byteCount)
	case base == "tailwind.config.js" || base == "tailwind.config.ts":
		return fmt.Sprintf("%s (%d bytes): Tailwind CSS configuration", base, byteCount)
	}

	// For general code files: take first non-empty lines or definitions
	preview := make([]string, 0, 2)
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed != "" && !strings.HasPrefix(trimmed, "//") && !strings.HasPrefix(trimmed, "/*") && !strings.HasPrefix(trimmed, "*") {
			preview = append(preview, trimmed)
			if len(preview) >= 2 {
				break
			}
		}
	}
	desc := strings.Join(preview, " | ")
	if len(desc) > 80 {
		desc = desc[:77] + "..."
	}
	return fmt.Sprintf("%s (%d bytes, %d lines): %s", path, byteCount, lineCount, desc)
}

// IsObservationValid checks whether an observation's underlying evidence is still fresh.
func IsObservationValid(ctx context.Context, obs Observation, store Repository, root string) bool {
	if obs.Version < CurrentObservationVersion {
		return false
	}
	if obs.Tool == "read_file" {
		absPath := filepath.Join(root, obs.Path)
		data, err := os.ReadFile(absPath)
		if obs.Negative {
			// Negative observation: valid if file still does not exist
			return os.IsNotExist(err)
		}
		if err != nil {
			return false
		}
		h := sha256.Sum256(data)
		currentHash := hex.EncodeToString(h[:])
		return currentHash == obs.SourceHash
	}

	if obs.Tool == "list_directory" {
		absPath := filepath.Join(root, obs.Path)
		if obs.Negative {
			currentFP := ComputeDirectoryFingerprint(absPath)
			if currentFP == "" {
				return false
			}
			entries, err := os.ReadDir(absPath)
			if err != nil || len(entries) > 0 {
				return false
			}
			return obs.SourceHash == "" || currentFP == obs.SourceHash
		}
		if obs.SourceHash != "" {
			currentFP := ComputeDirectoryFingerprint(absPath)
			return currentFP != "" && currentFP == obs.SourceHash
		}
		info, err := os.Stat(absPath)
		if err != nil || !info.IsDir() || info.ModTime().After(obs.CreatedAt) {
			return false
		}
		return true
	}

	if obs.Tool == "search_file_name" || obs.Tool == "grep_search" || obs.Tool == "search_text" || obs.Tool == "repository_query" {
		if store != nil {
			files, err := store.RepositoryFiles(ctx)
			if err == nil {
				for _, f := range files {
					if isInScope(f.Path, obs.Scope) {
						if f.ModifiedAt.After(obs.CreatedAt) {
							return false
						}
					}
				}
			}
		}
		return true
	}

	return true
}

func isInScope(filePath, scope string) bool {
	if scope == "." || scope == "" {
		return true
	}
	cleanScope := filepath.ToSlash(filepath.Clean(scope))
	cleanPath := filepath.ToSlash(filepath.Clean(filePath))
	if cleanPath == cleanScope {
		return true
	}
	return strings.HasPrefix(cleanPath, cleanScope+"/")
}

// FormatResearchEvidence produces the markdown text for the LayerDurableObs (L3) prompt candidate.
func FormatResearchEvidence(observations []Observation) string {
	return FormatRelevantResearchEvidence(observations, nil, "", "", 2000)
}

// FormatRelevantResearchEvidence produces a budgeted, relevance-ranked prompt candidate with an index for older evidence.
func FormatRelevantResearchEvidence(observations []Observation, workingPaths []string, objective, planStep string, maxTokens int) string {
	if len(observations) == 0 {
		return ""
	}
	if maxTokens <= 0 {
		maxTokens = 2000
	}

	type scoredObs struct {
		obs   Observation
		score float64
	}
	scored := make([]scoredObs, 0, len(observations))
	lowerObj := strings.ToLower(objective)
	lowerStep := strings.ToLower(planStep)

	for _, obs := range observations {
		s := 0.0
		for _, wp := range workingPaths {
			if wp != "" && (obs.Path == wp || obs.Scope == wp) {
				s += 100.0
				break
			}
		}
		if obs.Path != "" && (strings.Contains(lowerObj, strings.ToLower(obs.Path)) || strings.Contains(lowerStep, strings.ToLower(obs.Path))) {
			s += 50.0
		}
		if obs.Negative {
			s += 40.0 // Negative observations prevent redundant scans
		}
		ageMinutes := time.Since(obs.CreatedAt).Minutes()
		if ageMinutes < 60 {
			s += 30.0 - ageMinutes*0.5
		}
		scored = append(scored, scoredObs{obs: obs, score: s})
	}

	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	var inspected []string
	var discoveries []string
	var negative []string
	var indexed []string

	usedTokens := 50
	for _, so := range scored {
		obs := so.obs
		if obs.Negative {
			line := fmt.Sprintf("- %s absent/empty: %s", obs.Path, obs.Summary)
			lineTokens := (len(line) + 3) / 4
			if usedTokens+lineTokens <= maxTokens {
				negative = append(negative, line)
				usedTokens += lineTokens
			} else {
				indexed = append(indexed, fmt.Sprintf("- %s (absent): %s (id=%s)", obs.Path, obs.Summary, obs.ID))
			}
		} else if obs.Tool == "read_file" {
			hashShort := obs.SourceHash
			if len(hashShort) > 8 {
				hashShort = hashShort[:8]
			}
			block := fmt.Sprintf("- %s @ hash %s\n  %s", obs.Path, hashShort, obs.Summary)
			blockTokens := (len(block) + 3) / 4
			if usedTokens+blockTokens <= maxTokens {
				inspected = append(inspected, block)
				usedTokens += blockTokens
			} else {
				indexed = append(indexed, fmt.Sprintf("- %s: %s (id=%s)", obs.Path, obs.Summary, obs.ID))
			}
		} else {
			line := fmt.Sprintf("- %s: %s", obs.Tool, obs.Summary)
			lineTokens := (len(line) + 3) / 4
			if usedTokens+lineTokens <= maxTokens {
				discoveries = append(discoveries, line)
				usedTokens += lineTokens
			} else {
				indexed = append(indexed, fmt.Sprintf("- %s: %s (id=%s)", obs.Tool, obs.Summary, obs.ID))
			}
		}
	}

	var sb strings.Builder
	sb.WriteString("KNOWN RESEARCH EVIDENCE\n")

	if len(inspected) > 0 {
		sb.WriteString("\nInspected Files:\n")
		sb.WriteString(strings.Join(inspected, "\n"))
		sb.WriteString("\n")
	}

	if len(discoveries) > 0 {
		sb.WriteString("\nStructural & Search Discoveries:\n")
		sb.WriteString(strings.Join(discoveries, "\n"))
		sb.WriteString("\n")
	}

	if len(negative) > 0 {
		sb.WriteString("\nNegative Observations (Confirmed Missing/Empty):\n")
		sb.WriteString(strings.Join(negative, "\n"))
		sb.WriteString("\n")
	}

	if len(indexed) > 0 {
		sb.WriteString("\nRetained Evidence Index (Durable Memory):\n")
		sb.WriteString(strings.Join(indexed, "\n"))
		sb.WriteString("\n")
	}

	return strings.TrimSpace(sb.String())
}
