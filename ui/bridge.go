package ui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/AbhaySingh002/supremo/internal/agent"
)

const progressQueueCapacity = 256

// eventBridge lets workers publish without touching Bubble Tea model state.
type eventBridge struct {
	ctx    context.Context
	events chan agent.ProgressEvent
}

func newEventBridge(ctx context.Context, capacity int) *eventBridge {
	return &eventBridge{ctx: ctx, events: make(chan agent.ProgressEvent, capacity)}
}

func (b *eventBridge) publish(event agent.ProgressEvent) {
	select {
	case b.events <- event:
		return
	default:
	}
	select {
	case b.events <- event:
	case <-b.ctx.Done():
	}
}

func (b *eventBridge) wait() tea.Cmd {
	return func() tea.Msg {
		select {
		case event := <-b.events:
			return agentProgressMsg{event: event}
		default:
		}
		select {
		case event := <-b.events:
			return agentProgressMsg{event: event}
		case <-b.ctx.Done():
			return nil
		}
	}
}
