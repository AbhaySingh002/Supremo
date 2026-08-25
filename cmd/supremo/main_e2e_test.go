package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"syscall"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/AbhaySingh002/supremo/internal/ui"
)

type recordingSender chan tea.Msg

func (s recordingSender) Send(msg tea.Msg) { s <- msg }

func TestInterruptSignalBecomesGracefulInterruptMessage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	interrupts := make(chan os.Signal, 1)
	messages := make(recordingSender, 1)
	done := make(chan struct{})
	go func() {
		forwardInterrupts(ctx, messages, interrupts)
		close(done)
	}()
	interrupts <- os.Interrupt
	msg := (<-messages).(ui.InterruptMsg)
	if msg.Terminate || ctx.Err() != nil {
		t.Fatalf("interrupt message=%#v context=%v", msg, ctx.Err())
	}
	cancel()
	select {
	case <-done:
	case <-ctx.Done():
		<-done
	}
}

func TestTerminateSignalRequestsGracefulShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	interrupts := make(chan os.Signal, 1)
	messages := make(recordingSender, 1)
	go forwardInterrupts(ctx, messages, interrupts)
	interrupts <- syscall.SIGTERM
	if msg := (<-messages).(ui.InterruptMsg); !msg.Terminate {
		t.Fatalf("SIGTERM message = %#v", msg)
	}
}

func TestTUIProgramRunsWithInjectedTerminal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var output bytes.Buffer
	model := ui.New(nil, ".", "test", ui.Options{Context: ctx, Shutdown: cancel})
	program := tea.NewProgram(model, tea.WithInput(bytes.NewBufferString("\x03\x03")), tea.WithOutput(&output), tea.WithContext(ctx), tea.WithoutSignalHandler())
	_, err := program.Run()
	if !errors.Is(err, tea.ErrProgramKilled) && err != nil {
		t.Fatalf("run TUI: %v", err)
	}
	if output.Len() == 0 {
		t.Fatal("expected TUI output")
	}
}
