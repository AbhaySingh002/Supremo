package ui

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/AbhaySingh002/supremo/internal/api"
)

func TestConcurrentCopyDuringStreaming(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	model := newTestModel(api.Session{ID: "test-concurrent-copy", Name: "Concurrent Copy Test"}, ctx, cancel)
	model.width, model.height = 100, 30
	model.layout()

	// Populate an initial assistant response
	model.appendEntry(entryAssistant, "Initial assistant message to copy.")

	var wg sync.WaitGroup
	var mu sync.Mutex

	// 1. Simulate active streaming updates arriving in rapid succession
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 25; i++ {
			mu.Lock()
			_ = model.applyProgress(progressEvent{
				Kind:    progressStream,
				Message: " streamed-chunk",
			})
			_ = model.View()
			mu.Unlock()
			time.Sleep(2 * time.Millisecond)
		}
	}()

	// 2. Concurrently execute multiple copy actions
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			mu.Lock()
			cmd := model.copyLastAssistantResponse()
			mu.Unlock()

			if cmd != nil {
				// Just invoke the cmd; we can't intercept tea.SetClipboard
				// in tests, but the model must not panic under concurrency.
				_ = cmd()
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	wg.Wait()

	// Verify model view remains intact after concurrent access
	view := model.View()
	if view.Content == "" {
		t.Fatal("expected non-empty rendered view")
	}
}
