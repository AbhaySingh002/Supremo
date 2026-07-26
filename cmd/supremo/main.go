package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/AbhaySingh002/supremo/internal/agent"
	"github.com/AbhaySingh002/supremo/internal/app"
	"github.com/AbhaySingh002/supremo/ui"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Println("supremo", version)
		return
	}
	debug := len(os.Args) > 1 && os.Args[1] == "--debug"

	application, err := app.New()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Initialization error: %v\n", err)
		os.Exit(1)
	}
	if debug {
		application.Agent.SetDebug(true)
	}

	session, err := agent.LoadOrCreateSession(".", "cli-session")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Session error: %v\n", err)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	model := ui.New(application, session, ctx, stop)
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithContext(ctx), tea.WithoutSignalHandler())
	if _, err := program.Run(); err != nil && !errors.Is(err, tea.ErrProgramKilled) && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "Supremo stopped: %v\n", err)
	}
}
