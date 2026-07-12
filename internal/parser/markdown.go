package parser

import (
	"strings"
)

const (
	toolCallStart  = "<tool_call>"
	toolCallEnd    = "</tool_call>"
	finalStart     = "<final_answer>"
	finalEnd       = "</final_answer>"
)

// ExtractToolBlocks parses the raw LLM response and extracts tool call JSON blocks
// delimited by <tool_call>...</tool_call> tags and an optional final answer
// delimited by <final_answer>...</final_answer> tags.
// Text outside these tags is collected as the thought/reasoning.
func ExtractToolBlocks(raw string) (thought string, blocks []string, finalAnswer string) {
	var thoughtParts []string
	current := raw

	for {
		startIdx := strings.Index(current, toolCallStart)
		if startIdx == -1 {
			break
		}

		// Text before this tag is thought
		pre := strings.TrimSpace(current[:startIdx])
		if pre != "" {
			thoughtParts = append(thoughtParts, pre)
		}

		remaining := current[startIdx+len(toolCallStart):]
		endIdx := strings.Index(remaining, toolCallEnd)
		if endIdx == -1 {
			// Malformed: treat rest as block content
			blocks = append(blocks, strings.TrimSpace(remaining))
			current = ""
			break
		}

		blocks = append(blocks, strings.TrimSpace(remaining[:endIdx]))
		current = remaining[endIdx+len(toolCallEnd):]
	}

	// Check for <final_answer> in remaining text
	if faStart := strings.Index(current, finalStart); faStart != -1 {
		pre := strings.TrimSpace(current[:faStart])
		if pre != "" {
			thoughtParts = append(thoughtParts, pre)
		}

		afterStart := current[faStart+len(finalStart):]
		if faEnd := strings.Index(afterStart, finalEnd); faEnd != -1 {
			finalAnswer = strings.TrimSpace(afterStart[:faEnd])
			post := strings.TrimSpace(afterStart[faEnd+len(finalEnd):])
			if post != "" {
				thoughtParts = append(thoughtParts, post)
			}
		} else {
			finalAnswer = strings.TrimSpace(afterStart)
		}
	} else {
		// No final_answer tag: if no tool blocks were found, entire response is final answer
		leftover := strings.TrimSpace(current)
		if len(blocks) == 0 && leftover != "" {
			finalAnswer = leftover
		} else if leftover != "" {
			thoughtParts = append(thoughtParts, leftover)
		}
	}

	thought = strings.Join(thoughtParts, "\n")
	return thought, blocks, finalAnswer
}
