package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/providers"
	"github.com/AbhaySingh002/supremo/internal/sessionlog"
	"github.com/AbhaySingh002/supremo/internal/state"
)

const (
	CompactionInstruction = `You are acting as the compaction engine for this coding-agent session.

Condense the conversation above into a structured checkpoint that lets the agent continue the task without losing essential context.

Output exactly these sections:

## Primary Request and Intent
- original and evolving user goals
- exact wording where consequential

## Key Technical Concepts
- architecture, libraries, protocols, important conventions

## Files and Code
- exact file paths
- important symbols
- modifications already made
- relevant snippets only when necessary

## Errors and Fixes
- encountered failures
- root causes
- fixes
- user corrections

## Pending Jobs
- explicitly requested work not completed

## Current Work
- what was actively in progress

## Next Step
- immediate next action

## Critical Context
- decisions and rationale
- constraints
- user preferences
- confirmed human answers
- unresolved questions

Rules:
- use terse engineering bullets
- preserve exact paths, identifiers, commands, error strings and numeric values when important
- preserve user corrections faithfully
- never claim work was completed when it was not
- when an earlier compacted summary exists, merge still-valid information into the new checkpoint instead of copying or nesting the old summary
- output only the checkpoint
- do not call tools`

	SummaryFramingPrefix = "This is an automatically generated checkpoint condensing an earlier span of the conversation to free context. Treat it as established background and continue directly from the messages that follow without acknowledging the checkpoint.\n\n<compacted-summary>\n"
	SummaryFramingSuffix = "\n</compacted-summary>"
)

// FrameSummary encapsulates a compacted checkpoint inside the standard model-visible user envelope.
func FrameSummary(rawSummary string) string {
	return SummaryFramingPrefix + strings.TrimSpace(rawSummary) + SummaryFramingSuffix
}

// CompactionEngine manages conversation-prefix compaction.
type CompactionEngine interface {
	Compact(ctx context.Context, store state.EventStore, session *Session, provider providers.Provider, prompt *models.Prompt, measurement Measurement) (bool, error)
}

// DefaultCompactionEngine implements conversation-prefix compaction with safe tool boundaries.
type DefaultCompactionEngine struct{}

// NewDefaultCompactionEngine constructs a new DefaultCompactionEngine.
func NewDefaultCompactionEngine() *DefaultCompactionEngine {
	return &DefaultCompactionEngine{}
}

// SelectCompactionRange determines the safe prefix boundary to compact, retaining the recent tail.
func SelectCompactionRange(session *Session, measurement Measurement) ([]int64, error) {
	if session == nil {
		return nil, nil
	}
	session.ensureSurface()

	nodes := session.Nodes()
	if len(nodes) <= 2 {
		return nil, nil
	}

	// 1. Walk backward from surface tail accumulating tokens until retainTokens is reached
	accumulatedTailTokens := 0
	cutoffIdx := len(nodes)

	for i := len(measurement.Nodes) - 1; i >= 0; i-- {
		accumulatedTailTokens += measurement.Nodes[i].Tokens
		cutoffIdx = i
		if accumulatedTailTokens >= measurement.RetainTokens {
			break
		}
	}

	if cutoffIdx <= 0 || cutoffIdx >= len(nodes) {
		// Cannot retain requested tail tokens while leaving a prefix
		return nil, nil
	}

	// 2. Balance tool boundaries: walk cutoffIdx backward if it splits an assistant ToolCall and its ToolResults
	cutoffIdx = balanceToolBoundary(session, nodes, cutoffIdx)
	if cutoffIdx <= 0 {
		return nil, nil
	}

	candidateSeqs := make([]int64, cutoffIdx)
	copy(candidateSeqs, nodes[:cutoffIdx])
	return candidateSeqs, nil
}

// balanceToolBoundary adjusts cutoffIdx backward so no ToolCall/ToolResult batch is split.
func balanceToolBoundary(session *Session, nodes []int64, cutoffIdx int) int {
	for cutoffIdx > 0 {
		splitFound := false

		// Check all nodes in candidate prefix [0..cutoffIdx-1]
		// If an assistant message has tool calls, verify all corresponding tool results are in prefix
		for i := 0; i < cutoffIdx; i++ {
			event, ok := session.eventBySeq(nodes[i])
			if !ok || event.Type != EventAssistantMessage {
				continue
			}
			msg, ok := deriveEventMessage(event)
			if !ok || msg == nil || len(msg.ToolCalls) == 0 {
				continue
			}

			// For each tool call, check if its result is beyond cutoffIdx
			for _, call := range msg.ToolCalls {
				for j := cutoffIdx; j < len(nodes); j++ {
					resEv, resOk := session.eventBySeq(nodes[j])
					if resOk && resEv.Type == EventToolResult {
						resMsg, resMsgOk := deriveEventMessage(resEv)
						if resMsgOk && resMsg != nil && resMsg.ToolCallID == call.ID {
							// Split detected: assistant is in prefix, but tool result is in tail!
							// Walk cutoff backward to before this assistant message
							cutoffIdx = i
							splitFound = true
							break
						}
					}
				}
				if splitFound {
					break
				}
			}
			if splitFound {
				break
			}
		}

		if !splitFound {
			break
		}
	}

	return cutoffIdx
}

// Compact executes a single LLM summarization request over the candidate prefix and commits a surface replacement.
func (e *DefaultCompactionEngine) Compact(
	ctx context.Context,
	store state.EventStore,
	session *Session,
	provider providers.Provider,
	prompt *models.Prompt,
	measurement Measurement,
) (bool, error) {
	if session == nil || provider == nil {
		return false, nil
	}

	candidateSeqs, err := SelectCompactionRange(session, measurement)
	if err != nil || len(candidateSeqs) <= 1 {
		return false, err
	}

	// Calculate shadowed tokens
	shadowedTokens := 0
	var prefixMsgs []models.Message
	for _, seq := range candidateSeqs {
		event, ok := session.eventBySeq(seq)
		if !ok {
			continue
		}
		msg, ok := deriveEventMessage(event)
		if ok && msg != nil {
			shadowedTokens += EstimateMessageTokens(*msg)
			prefixMsgs = append(prefixMsgs, *msg)
		}
	}

	if len(prefixMsgs) <= 1 || shadowedTokens <= 0 {
		return false, nil
	}

	compactionID := fmt.Sprintf("compaction_%d", time.Now().UnixNano())
	startSeq := candidateSeqs[0]
	endSeq := candidateSeqs[len(candidateSeqs)-1]
	targetGeneration := surfaceGeneration(session)
	providerName, modelName := session.Provider, session.Model

	// 1. Append compaction/start.
	if err := appendCompactionEvent(ctx, store, session, EventCompactionStart, sessionlog.CompactionPayload{
		CompactionID: compactionID, Provider: providerName, Model: modelName, StartSeq: startSeq, EndSeq: endSeq, ShadowedSeqs: candidateSeqs, ShadowedTokens: shadowedTokens,
	}); err != nil {
		return false, err
	}

	// 2. Build summarization prompt from the exact frozen request. The final
	// instruction limits summarization to the selected oldest prefix.
	summarizeMsgs := append([]models.Message(nil), session.DeriveMessages()...)
	if prompt != nil {
		summarizeMsgs = append([]models.Message(nil), prompt.Messages...)
	}
	retained := max(0, len(session.Nodes())-len(candidateSeqs))
	summarizeMsgs = append(summarizeMsgs, models.Message{
		Role:    models.RoleUser,
		Content: fmt.Sprintf("%s\n\nSummarize only the oldest prefix covering surface events %d through %d (%d messages). The newest retained tail contains %d messages; exclude that tail from the summary and do not repeat it.", CompactionInstruction, startSeq, endSeq, len(prefixMsgs), retained),
	})

	sysPrompt := ""
	var toolDefs []models.ToolDefinition
	if prompt != nil {
		sysPrompt = prompt.System
		toolDefs = prompt.ToolDefinitions
	}

	compactionPrompt := &models.Prompt{
		System:          sysPrompt,
		Messages:        summarizeMsgs,
		ToolDefinitions: toolDefs,
	}

	// 3. Request summary from provider
	completion, err := provider.Chat(ctx, compactionPrompt)
	if err != nil || completion == nil || strings.TrimSpace(completion.Text) == "" {
		failErr := err
		if failErr == nil {
			failErr = fmt.Errorf("provider returned empty summary")
		}
		operationErr := fmt.Errorf("compaction summary generation failed: %w", failErr)
		return false, finishCompaction(ctx, store, session, sessionlog.CompactionPayload{CompactionID: compactionID, Provider: providerName, Model: modelName, Status: "failed", Error: failErr.Error()}, operationErr)
	}
	finishReason := providers.NormalizeFinishReason(completion.FinishReason)
	usage := completion.Usage
	if finishReason == providers.FinishMaxTokens {
		operationErr := fmt.Errorf("compaction rejected: summary terminated by max_tokens")
		return false, finishCompaction(ctx, store, session, sessionlog.CompactionPayload{CompactionID: compactionID, Provider: providerName, Model: modelName, FinishReason: string(finishReason), InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, Status: "failed", Error: operationErr.Error()}, operationErr)
	}

	rawSummary := strings.TrimSpace(completion.Text)
	framedSummary := FrameSummary(rawSummary)
	replacementMsg := models.Message{
		Role:    models.RoleUser,
		Content: framedSummary,
	}
	summaryTokens := EstimateMessageTokens(replacementMsg)

	// 4. Validate token reduction: summary must be strictly smaller than shadowed tokens
	if summaryTokens >= shadowedTokens {
		operationErr := fmt.Errorf("compaction rejected: summary tokens (%d) >= shadowed tokens (%d)", summaryTokens, shadowedTokens)
		return false, finishCompaction(ctx, store, session, sessionlog.CompactionPayload{
			CompactionID: compactionID, Provider: providerName, Model: modelName, FinishReason: string(finishReason), InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, Status: "failed", Error: operationErr.Error(), SummaryTokens: summaryTokens, ShadowedTokens: shadowedTokens,
		}, operationErr)
	}

	// 5. Verify Surface range has not changed
	currentNodes := session.Nodes()
	if surfaceGeneration(session) != targetGeneration || len(currentNodes) < len(candidateSeqs) {
		operationErr := fmt.Errorf("surface modified concurrently during compaction")
		return false, finishCompaction(ctx, store, session, sessionlog.CompactionPayload{CompactionID: compactionID, Provider: providerName, Model: modelName, FinishReason: string(finishReason), InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, Status: "failed", Error: operationErr.Error()}, operationErr)
	}
	for i, s := range candidateSeqs {
		if currentNodes[i] != s {
			operationErr := fmt.Errorf("surface range shifted concurrently during compaction")
			return false, finishCompaction(ctx, store, session, sessionlog.CompactionPayload{CompactionID: compactionID, Provider: providerName, Model: modelName, FinishReason: string(finishReason), InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, Status: "failed", Error: operationErr.Error()}, operationErr)
		}
	}

	// 6. Append compaction/summary metric.
	if err := appendCompactionEvent(ctx, store, session, EventCompactionSummary, sessionlog.CompactionPayload{
		CompactionID: compactionID, Provider: providerName, Model: modelName, FinishReason: string(finishReason), InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, Summary: rawSummary, ShadowedTokens: shadowedTokens, SummaryTokens: summaryTokens, TokensSaved: shadowedTokens - summaryTokens,
	}); err != nil {
		operationErr := fmt.Errorf("append compaction/summary: %w", err)
		return false, finishCompaction(ctx, store, session, sessionlog.CompactionPayload{CompactionID: compactionID, Provider: providerName, Model: modelName, FinishReason: string(finishReason), InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, Status: "failed", Error: operationErr.Error()}, operationErr)
	}

	// 7. Append replacement user/message with surfaceOp: replace(startSeq..endSeq).
	replaceEvent, err := sessionlog.New(EventUserMessage, replacementMsg)
	if err != nil {
		return false, finishCompaction(ctx, store, session, sessionlog.CompactionPayload{CompactionID: compactionID, Provider: providerName, Model: modelName, FinishReason: string(finishReason), InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, Status: "failed", Error: err.Error()}, err)
	}
	replaceEvent.Message = replacementMsg
	replaceEvent.SurfaceOp = &SurfaceOp{
		Kind:     surfaceOpReplace,
		StartSeq: startSeq,
		EndSeq:   endSeq,
	}
	replaceEvent.SourceEventSeqs = candidateSeqs
	if err := appendAndApplySessionEvent(ctx, store, session, replaceEvent); err != nil {
		operationErr := fmt.Errorf("apply compaction replacement message: %w", err)
		return false, finishCompaction(ctx, store, session, sessionlog.CompactionPayload{CompactionID: compactionID, Provider: providerName, Model: modelName, FinishReason: string(finishReason), InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, Status: "failed", Error: operationErr.Error()}, operationErr)
	}

	// 8. Append compaction/end (completed).
	if err := finishCompaction(ctx, store, session, sessionlog.CompactionPayload{
		CompactionID: compactionID, Provider: providerName, Model: modelName, FinishReason: string(finishReason), InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, Status: "completed", ShadowedTokens: shadowedTokens, SummaryTokens: summaryTokens, TokensSaved: shadowedTokens - summaryTokens,
	}, nil); err != nil {
		return false, err
	}

	return true, nil
}

func finishCompaction(ctx context.Context, store state.EventStore, session *Session, payload sessionlog.CompactionPayload, operationErr error) error {
	terminalCtx, cancel := terminalContext(ctx)
	defer cancel()
	terminalErr := appendCompactionEvent(terminalCtx, store, session, EventCompactionEnd, payload)
	if terminalErr != nil {
		terminalErr = fmt.Errorf("append compaction/end: %w", terminalErr)
	}
	return errors.Join(operationErr, terminalErr)
}

func appendCompactionEvent(ctx context.Context, store state.EventStore, session *Session, kind EventType, payload sessionlog.CompactionPayload) error {
	event, err := sessionlog.New(kind, payload)
	if err != nil {
		return err
	}
	return appendAndApplySessionEvent(ctx, store, session, event)
}
