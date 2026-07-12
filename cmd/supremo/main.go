package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/AbhaySingh002/supremo/internal/agent"
	"github.com/AbhaySingh002/supremo/internal/app"
	"github.com/AbhaySingh002/supremo/internal/commands"
)

// CLIStream handles the events emitted by the agent during the Run loop.
type CLIStream struct{}

func (s *CLIStream) Emit(event agent.Event) {}

func main() {
	debug := len(os.Args) > 1 && os.Args[1] == "--debug"

	application, err := app.New()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Initialization error: %v\n", err)
		os.Exit(1)
	}

	if debug {
		application.Agent.SetDebug(true)
		fmt.Println("[DEBUG MODE ENABLED]")
	}

	fmt.Println("==================================================")
	fmt.Println("Supremo CLI")
	fmt.Println("Type /help to list available commands.")
	fmt.Println("==================================================")

	reader := bufio.NewReader(os.Stdin)
	ctx := context.Background()
	session := &agent.Session{ID: "cli-session"}
	stream := &CLIStream{}
	cmdRegistry := commands.NewRegistry()

	for {
		fmt.Print("> ")
		input, err := reader.ReadString('\n')
		if err != nil {
			fmt.Println()
			break // EOF or Ctrl+D
		}

		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}

		// Handle interactive commands
		cmdOutput, handled, cmdErr := cmdRegistry.Handle(ctx, application, session, input)
		if handled {
			if cmdErr != nil {
				if errors.Is(cmdErr, commands.ErrExit) {
					break
				}
				fmt.Fprintf(os.Stderr, "Error: %v\n", cmdErr)
				continue
			}
			if cmdOutput != "" {
				fmt.Println(cmdOutput)
			}
			continue
		}

		// Quit shortcut still supported
		if input == "exit" || input == "quit" {
			break
		}

		response, err := application.Agent.Run(ctx, session, input, stream)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			continue
		}
		if response != "" {
			fmt.Println(response)
		}
	}
}
