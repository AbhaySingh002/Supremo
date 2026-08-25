package repeat

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/runtime"
)

var DefaultRepeatThresholds = []int{3, 5, 8}

const DefaultArgPreviewLen = 500

type RepeatChain struct {
	Key   string
	Count int
}

type Config struct {
	Thresholds          []int
	Include             []string
	Exclude             []string
	ArgumentsPreviewLen int
}

// Guard detects consecutive identical ToolCalls and emits advisory NextStep reminders.
type Guard struct {
	chain               *RepeatChain
	thresholds          []int
	include             []string
	exclude             []string
	argumentsPreviewLen int
}

func New(cfg Config) *Guard {
	thresholds := cfg.Thresholds
	if len(thresholds) == 0 {
		thresholds = append([]int(nil), DefaultRepeatThresholds...)
	} else if err := ValidateThresholds(thresholds); err != nil {
		thresholds = append([]int(nil), DefaultRepeatThresholds...)
	} else {
		thresholds = append([]int(nil), thresholds...)
	}
	maxLen := cfg.ArgumentsPreviewLen
	if maxLen <= 0 {
		maxLen = DefaultArgPreviewLen
	}
	return &Guard{
		thresholds:          thresholds,
		include:             append([]string(nil), cfg.Include...),
		exclude:             append([]string(nil), cfg.Exclude...),
		argumentsPreviewLen: maxLen,
	}
}

func ValidateThresholds(thresholds []int) error {
	if len(thresholds) == 0 {
		return errors.New("thresholds cannot be empty")
	}
	for i, t := range thresholds {
		if t < 2 {
			return fmt.Errorf("threshold must be >= 2 (got %d at index %d)", t, i)
		}
		if i > 0 && t <= thresholds[i-1] {
			return fmt.Errorf("thresholds must be strictly ascending and unique (got %d after %d)", t, thresholds[i-1])
		}
	}
	return nil
}

func (g *Guard) Tracked(toolName string) bool {
	if g == nil {
		return false
	}
	if len(g.include) > 0 {
		matched := false
		for _, pattern := range g.include {
			if pattern == "*" || pattern == toolName {
				matched = true
				break
			}
			if ok, _ := filepath.Match(pattern, toolName); ok {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	for _, pattern := range g.exclude {
		if pattern == "*" || pattern == toolName {
			return false
		}
		if ok, _ := filepath.Match(pattern, toolName); ok {
			return false
		}
	}
	return true
}

func (g *Guard) Observe(call models.ToolCall) *models.Message {
	if g == nil || !g.Tracked(call.Name) {
		return nil
	}
	key := ToolCallFingerprint(call)
	if g.chain != nil && g.chain.Key == key {
		g.chain.Count++
	} else {
		g.chain = &RepeatChain{Key: key, Count: 1}
	}
	if slices.Contains(g.thresholds, g.chain.Count) {
		reminder := g.buildReminder(call, g.chain.Count)
		return &reminder
	}
	return nil
}

func (g *Guard) Reset() {
	if g != nil {
		g.chain = nil
	}
}

func (g *Guard) Chain() *RepeatChain {
	if g == nil || g.chain == nil {
		return nil
	}
	c := *g.chain
	return &c
}

func (g *Guard) AfterTool(event runtime.AfterToolEvent) (runtime.AfterToolDecision, error) {
	reminder := g.Observe(event.Call)
	if reminder == nil {
		return runtime.AfterToolDecision{}, nil
	}
	return runtime.AfterToolDecision{NextStep: []models.Message{*reminder}}, nil
}

func (g *Guard) OnUserInput(runtime.UserInputEvent) { g.Reset() }

func ToolCallFingerprint(call models.ToolCall) string {
	return call.Name + ":" + CanonicalJSON(call.Arguments)
}

func CanonicalJSON(raw any) string {
	if raw == nil {
		return "{}"
	}
	var data []byte
	switch v := raw.(type) {
	case string:
		data = []byte(v)
	case []byte:
		data = v
	case json.RawMessage:
		data = v
	default:
		b, err := json.Marshal(raw)
		if err != nil {
			return "{}"
		}
		data = b
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return "{}"
	}
	if string(data) == "null" {
		return "null"
	}
	var val any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&val); err != nil {
		return string(data)
	}
	if val == nil {
		return "null"
	}
	canonicalBytes, err := json.Marshal(val)
	if err != nil {
		return string(data)
	}
	return string(canonicalBytes)
}

func (g *Guard) buildReminder(call models.ToolCall, count int) models.Message {
	firstThreshold := 0
	if len(g.thresholds) > 0 {
		firstThreshold = g.thresholds[0]
	}
	if count == firstThreshold {
		return models.Message{
			Role: models.RoleUser,
			Content: "You are repeating the exact same tool call with identical arguments. " +
				"Review the previous result before calling it again. " +
				"If the task is not complete, change the approach or arguments instead of repeating it.",
		}
	}
	canonicalArgs := CanonicalJSON(call.Arguments)
	preview := canonicalArgs
	maxLen := g.argumentsPreviewLen
	if maxLen <= 0 {
		maxLen = DefaultArgPreviewLen
	}
	if len(preview) > maxLen {
		preview = fmt.Sprintf("%s … (+%d chars)", preview[:maxLen], len(preview)-maxLen)
	}
	return models.Message{
		Role: models.RoleUser,
		Content: fmt.Sprintf("Repeated identical tool call detected.\n\n"+
			"Tool: %s\n"+
			"Consecutive calls: %d\n"+
			"Arguments: %s\n\n"+
			"The repeated calls are not producing new progress. Inspect the latest result and choose different arguments, another tool, or finish if enough evidence has already been gathered.",
			call.Name, count, preview),
	}
}
