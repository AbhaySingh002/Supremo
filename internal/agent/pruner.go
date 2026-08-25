package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/AbhaySingh002/supremo/internal/sessionlog"
	"github.com/AbhaySingh002/supremo/internal/state"
)

const (
	PruneThresholdChars = 8192
	PruneHeadChars      = 4096
	PruneTailChars      = 1024
	PruneMarker         = "\n\n[... tool result middle pruned ...]\n\n"
)

// PruneText deterministically slices oversized text into head + marker + tail using Unicode runes.
func PruneText(content string) (string, bool) {
	runes := []rune(content)
	if len(runes) <= PruneThresholdChars {
		return content, false
	}
	if strings.Contains(content, PruneMarker) {
		return content, false // idempotent
	}
	pruned := string(runes[:PruneHeadChars]) + PruneMarker + string(runes[len(runes)-PruneTailChars:])
	return pruned, true
}

// ToolResultPruner deterministically prunes oversized visible ToolResults on the active Surface.
type ToolResultPruner interface {
	Prune(ctx context.Context, store state.EventStore, session *Session) (int, error)
}

// DefaultToolResultPruner implements deterministic ToolResult pruning.
type DefaultToolResultPruner struct{}

// NewDefaultToolResultPruner constructs a new ToolResultPruner.
func NewDefaultToolResultPruner() *DefaultToolResultPruner {
	return &DefaultToolResultPruner{}
}

// Prune scans the active Surface for oversized EventToolResult messages and commits replacement events.
func (p *DefaultToolResultPruner) Prune(ctx context.Context, store state.EventStore, session *Session) (int, error) {
	if session == nil {
		return 0, nil
	}
	session.ensureSurface()

	nodes := session.Nodes()
	prunedCount := 0

	for _, seq := range nodes {
		origEvent, ok := session.eventBySeq(seq)
		if !ok || origEvent.Type != EventToolResult {
			continue
		}

		msg, ok := deriveEventMessage(origEvent)
		if !ok || msg == nil || msg.Content == "" {
			continue
		}

		prunedContent, pruned := PruneText(msg.Content)
		if !pruned {
			continue
		}

		// Prepare replacement message preserving exact ToolCallID, ToolName, and causality
		replacementMsg := *msg
		replacementMsg.Content = prunedContent

		// 1. Durable non-surface metric event: compaction/prune
		pruneMetricEvent, err := sessionlog.New(EventCompactionPrune, sessionlog.CompactionPayload{
			TargetSeq: origEvent.Seq, ToolCallID: msg.ToolCallID, ToolName: msg.ToolName,
			OriginalChars: len([]rune(msg.Content)), PrunedChars: len([]rune(prunedContent)), CharactersSaved: len([]rune(msg.Content)) - len([]rune(prunedContent)),
		})
		if err != nil {
			return prunedCount, err
		}
		if err := appendAndApplySessionEvent(ctx, store, session, pruneMetricEvent); err != nil {
			return prunedCount, fmt.Errorf("apply compaction/prune metric: %w", err)
		}

		// 2. Replacement tool/result event with surface replace operation
		replacementEvent, err := sessionlog.New(EventToolResult, replacementMsg)
		if err != nil {
			return prunedCount, err
		}
		replacementEvent.Message = replacementMsg
		replacementEvent.SurfaceOp = &SurfaceOp{
			Kind:     surfaceOpReplace,
			StartSeq: origEvent.Seq,
			EndSeq:   origEvent.Seq,
		}
		replacementEvent.SourceEventSeqs = []int64{origEvent.Seq}
		if err := appendAndApplySessionEvent(ctx, store, session, replacementEvent); err != nil {
			return prunedCount, fmt.Errorf("apply pruned tool/result replacement: %w", err)
		}

		prunedCount++
	}

	return prunedCount, nil
}
