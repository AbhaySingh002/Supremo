package parser

import (
	"strings"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
)

// Response is the agent's lightweight view of a provider-native completion.
// Assistant prose is never parsed as a control envelope.
type Response struct {
	ToolCalls    []models.ToolCall
	TurnProgress *models.TurnProgress
}

// ExtractAssistantTurnProgress supports optional human-readable progress
// markers only. It intentionally never JSON-parses assistant prose.
func ExtractAssistantTurnProgress(text string) *models.TurnProgress {
	prog, goal := "", ""
	for _, line := range strings.Split(text, "\n") {
		clean := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "-*# \t"))
		lower := strings.ToLower(clean)
		if strings.HasPrefix(lower, "progress:") {
			prog = strings.TrimSpace(clean[len("progress:"):])
		}
		if strings.HasPrefix(lower, "next goal:") {
			goal = strings.TrimSpace(clean[len("next goal:"):])
		}
	}
	if prog == "" && goal == "" {
		return nil
	}
	return &models.TurnProgress{Progress: prog, NextGoal: goal}
}
