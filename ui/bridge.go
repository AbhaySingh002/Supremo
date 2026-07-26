package ui

import (
	"context"
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/AbhaySingh002/supremo/internal/agent"
)

const progressQueueCapacity = 256

// eventBridge lets workers publish without touching Bubble Tea model state.
type eventBridge struct {
	ctx    context.Context
	mu     sync.Mutex
	queue  []agent.ProgressEvent
	wakeUp chan struct{}
	space  chan struct{}
	limit  int
}

func newEventBridge(ctx context.Context) *eventBridge {
	return &eventBridge{ctx: ctx, wakeUp: make(chan struct{}, 1), space: make(chan struct{}, 1), limit: progressQueueCapacity}
}

func (b *eventBridge) publish(event agent.ProgressEvent) {
	for {
		b.mu.Lock()
		if len(b.queue) < b.limit {
			b.queue = append(b.queue, event)
			b.mu.Unlock()
			break
		}
		b.mu.Unlock()
		// ponytail: block the producer to preserve every event; raise the capacity only
		// if profiling shows a terminal cannot keep up with normal progress traffic.
		select {
		case <-b.ctx.Done():
			return
		case <-b.space:
		}
	}
	select {
	case b.wakeUp <- struct{}{}:
	default:
	}
}

func (b *eventBridge) wait() tea.Cmd {
	return func() tea.Msg {
		for {
			b.mu.Lock()
			if len(b.queue) > 0 {
				event := b.queue[0]
				b.queue = b.queue[1:]
				b.mu.Unlock()
				select {
				case b.space <- struct{}{}:
				default:
				}
				return agentProgressMsg{event: event}
			}
			b.mu.Unlock()
			select {
			case <-b.ctx.Done():
				return nil
			case <-b.wakeUp:
			}
		}
	}
}
