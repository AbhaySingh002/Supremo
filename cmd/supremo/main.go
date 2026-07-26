package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/AbhaySingh002/supremo/internal/agent"
	"github.com/AbhaySingh002/supremo/internal/app"
	"github.com/AbhaySingh002/supremo/internal/commands"
)

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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	session := &agent.Session{ID: "cli-session"}
	cmdRegistry := commands.NewRegistry()

	//  stdin read runs in goroutine so Ctrl+C cancels ctx and exits, not kills
	type line struct {
		text string
		err  error
	}
	inputCh := make(chan line)
	go func() {
		reader := bufio.NewReader(os.Stdin)
		for {
			text, err := reader.ReadString('\n')
			inputCh <- line{text, err}
			if err != nil {
				return
			}
		}
	}()

	for {
		fmt.Print("> ")
		select {
		case <-ctx.Done():
			fmt.Println("\nInterrupted.")
			return
		case l := <-inputCh:
			if l.err != nil {
				fmt.Println()
				break
			}
			input := strings.TrimSpace(l.text)
			if input == "" {
				continue
			}

			cmdOutput, handled, cmdErr := cmdRegistry.Handle(ctx, application, session, input)
			if handled {
				if cmdErr != nil {
					if errors.Is(cmdErr, commands.ErrExit) {
						return
					}
					fmt.Fprintf(os.Stderr, "Error: %v\n", cmdErr)
					continue
				}
				if cmdOutput != "" {
					fmt.Println(cmdOutput)
				}
				continue
			}

			response, err := application.Agent.Run(ctx, session, input)
			if err != nil {
				if ctx.Err() != nil {
					fmt.Println("\nInterrupted.")
					return
				}
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				continue
			}
			if response != "" {
				fmt.Println(response)
			}
		}
	}
}
