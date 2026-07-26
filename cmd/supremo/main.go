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
		fmt.Println("[DEBUG MODE ENABLED]")
	}

	fmt.Println("==================================================")
	fmt.Println("Supremo CLI")
	fmt.Println("Type /help to list available commands.")
	fmt.Println("==================================================")

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	defer signal.Stop(signals)

	session, err := agent.LoadOrCreateSession(".", "cli-session")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Session error: %v\n", err)
		return
	}
	cmdRegistry := commands.NewRegistry()
	application.Agent.SetProgress(func(message string) { fmt.Println("\n" + message) })

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

	type taskResult struct {
		response string
		err      error
	}
	var active bool
	var taskCh chan taskResult
	var cancelTask context.CancelFunc
	startTask := func(run func(context.Context) (string, error)) error {
		if active {
			return errors.New("a task is already running; use /cancel first")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancelTask = cancel
		taskCh = make(chan taskResult, 1)
		active = true
		go func() {
			response, err := run(ctx)
			taskCh <- taskResult{response: response, err: err}
		}()
		return nil
	}
	cmdRegistry.SetCancel(func() bool {
		if !active || cancelTask == nil {
			return false
		}
		cancelTask()
		return true
	})
	cmdRegistry.SetResume(func() error {
		return startTask(func(ctx context.Context) (string, error) {
			return application.Agent.ResumePlan(ctx, session)
		})
	})

	for {
		if !active {
			fmt.Print("> ")
		}
		select {
		case <-signals:
			if active && cancelTask != nil {
				cancelTask()
				fmt.Println("\nCancellation requested.")
				continue
			}
			fmt.Println("\nInterrupted.")
			return
		case l := <-inputCh:
			if l.err != nil {
				fmt.Println()
				return
			}
			input := strings.TrimSpace(l.text)
			if input == "" {
				continue
			}

			if active && !isActiveControl(input) {
				fmt.Println("A task is running; use /approve, /deny, or /cancel.")
				continue
			}
			cmdOutput, handled, cmdErr := cmdRegistry.Handle(context.Background(), application, session, input)
			if handled {
				if cmdErr != nil {
					if errors.Is(cmdErr, commands.ErrExit) {
						if cancelTask != nil {
							cancelTask()
						}
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

			if err := startTask(func(ctx context.Context) (string, error) {
				return application.Agent.Run(ctx, session, input)
			}); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			}
		case result := <-taskCh:
			active = false
			cancelTask = nil
			if result.err != nil {
				if errors.Is(result.err, context.Canceled) {
					fmt.Println("Task canceled.")
				} else {
					fmt.Fprintf(os.Stderr, "Error: %v\n", result.err)
				}
			} else if result.response != "" {
				fmt.Println(result.response)
			}
		}
	}
}

func isActiveControl(input string) bool {
	return input == "/approve" || strings.HasPrefix(input, "/deny") || input == "/cancel" || input == "/exit" || input == "/help" || input == "/tools" || input == "/activity"
}
