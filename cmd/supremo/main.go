package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	tea "charm.land/bubbletea/v2"

	"github.com/AbhaySingh002/supremo/internal/agent"
	"github.com/AbhaySingh002/supremo/internal/app"
	"github.com/AbhaySingh002/supremo/internal/logging"
	"github.com/AbhaySingh002/supremo/internal/providers"
	httptransport "github.com/AbhaySingh002/supremo/internal/transport/http"
	"github.com/AbhaySingh002/supremo/internal/ui"
)

var version = "dev"

func main() {
	defer logging.Recover("main")
	cleanup := logging.Init(".")
	defer cleanup()

	options, err := parseCLI(os.Args[1:], os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Usage error:", err)
		logging.Error("CLI parse error: %v", err)
		os.Exit(2)
	}
	if options.version {
		fmt.Println("supremo", version)
		return
	}
	if options.help {
		printUsage(os.Stdout)
		return
	}

	logging.Info("Supremo startup version=%s debug_flag=%v prompt_len=%d", version, options.debug, len(options.prompt))

	application, err := app.NewWithRuntimeOverrides(options.overrides)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Initialization error: %v\n", err)
		logging.Error("Application initialization error: %v", err)
		os.Exit(1)
	}
	defer application.Close()
	if options.debug {
		application.Agent.SetDebug(true)
	}
	if options.serve {
		if err := runServer(application, options); err != nil {
			fmt.Fprintln(os.Stderr, "Server error:", err)
			logging.Error("Server error: %v", err)
			os.Exit(1)
		}
		return
	}

	session, err := openSession(".", options)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Session error: %v\n", err)
		logging.Error("Session open error: %v", err)
		os.Exit(1)
	}
	logging.Info("Session opened ID=%s", session.ID)

	if options.prompt != "" {
		if err := runOneShot(application, session, options); err != nil {
			fmt.Fprintln(os.Stderr, "Supremo:", err)
			logging.Error("One-shot run error: %v", err)
			os.Exit(1)
		}
		logging.Info("One-shot prompt completed successfully")
		return
	}

	ctx, shutdown := context.WithCancel(context.Background())
	defer shutdown()
	application.Backend.SetVersion(version)
	if err := application.Backend.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Backend startup error: %v\n", err)
		return
	}
	model := ui.New(application.Backend, application.Workspace, session.ID, ui.Options{
		Context: ctx, Shutdown: shutdown, Debug: options.debug,
		Purge: func(context.Context) error {
			if err := application.Close(); err != nil {
				return err
			}
			return app.RemoveWorkspaceState(application.Workspace)
		},
	})
	program := tea.NewProgram(model, tea.WithContext(ctx), tea.WithoutSignalHandler())
	interrupts := make(chan os.Signal, 2)
	signal.Notify(interrupts, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(interrupts)
	go forwardInterrupts(ctx, program, interrupts)
	if _, err := program.Run(); err != nil && !errors.Is(err, tea.ErrProgramKilled) && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "Supremo stopped: %v\n", err)
		logging.Error("TUI program error: %v", err)
	}
	logging.Info("Supremo normal shutdown")
}

type cliOptions struct {
	serve     bool
	listen    string
	version   bool
	help      bool
	debug     bool
	prompt    string
	session   string
	resume    string
	plan      bool
	approve   bool
	overrides providers.RuntimeOverrides
}

func parseCLI(args []string, stdin *os.File) (cliOptions, error) {
	if len(args) == 1 && args[0] == "version" {
		return cliOptions{version: true}, nil
	}
	options := cliOptions{listen: "127.0.0.1:0"}
	if len(args) > 0 && args[0] == "serve" {
		options.serve = true
		args = args[1:]
	}
	flags := flag.NewFlagSet("supremo", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.BoolVar(&options.version, "version", false, "print version")
	flags.BoolVar(&options.help, "help", false, "show help")
	flags.BoolVar(&options.debug, "debug", false, "enable diagnostics")
	flags.StringVar(&options.listen, "listen", options.listen, "loopback server address")
	flags.StringVar(&options.prompt, "prompt", "", "run one prompt and print the response")
	flags.StringVar(&options.prompt, "p", "", "run one prompt and print the response")
	flags.StringVar(&options.session, "session", "", "explicit session ID; creates it when absent")
	flags.StringVar(&options.resume, "resume", "", "resume an existing session ID")
	flags.BoolVar(&options.plan, "plan", false, "research and prepare a durable plan in one-shot mode")
	flags.BoolVar(&options.approve, "approve", false, "allow tools in one-shot mode")
	flags.StringVar(&options.overrides.Provider, "provider", "", "provider for this run")
	flags.StringVar(&options.overrides.Model, "model", "", "model for this run")
	flags.StringVar(&options.overrides.Endpoint, "endpoint", "", "endpoint for this run")
	flags.StringVar(&options.overrides.APIKey, "api-key", "", "API key for this run")
	if err := flags.Parse(args); err != nil {
		return cliOptions{}, err
	}
	if options.version || options.help {
		return options, nil
	}
	if options.session != "" && options.resume != "" {
		return cliOptions{}, errors.New("use either --session or --resume")
	}
	if options.serve && (options.session != "" || options.resume != "" || options.plan || options.approve || options.prompt != "" || len(flags.Args()) > 0) {
		return cliOptions{}, errors.New("serve accepts only --listen, --debug, and runtime configuration flags")
	}
	if options.prompt != "" && len(flags.Args()) > 0 {
		return cliOptions{}, errors.New("use either --prompt or positional prompt text")
	}
	if options.prompt == "" && len(flags.Args()) > 0 {
		options.prompt = strings.Join(flags.Args(), " ")
	}
	if !options.serve && options.prompt == "" && !isTerminal(stdin) {
		data, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
		if err != nil {
			return cliOptions{}, fmt.Errorf("read stdin: %w", err)
		}
		options.prompt = strings.TrimSpace(string(data))
		if options.prompt == "" {
			return cliOptions{}, errors.New("stdin did not contain a prompt")
		}
	}
	applyEnvironmentOverrides(&options)
	return options, nil
}

func openSession(root string, options cliOptions) (*agent.Session, error) {
	if options.resume != "" {
		return agent.LoadSession(root, options.resume)
	}
	if options.session != "" {
		return agent.LoadOrCreateSession(root, options.session)
	}
	return agent.NewSession(root)
}

func isTerminal(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func applyEnvironmentOverrides(options *cliOptions) {
	if options.overrides.Provider == "" {
		options.overrides.Provider = os.Getenv("SUPREMO_PROVIDER")
	}
	if options.overrides.Model == "" {
		options.overrides.Model = os.Getenv("SUPREMO_MODEL")
	}
	if options.overrides.Endpoint == "" {
		options.overrides.Endpoint = os.Getenv("SUPREMO_ENDPOINT")
	}
	if options.overrides.APIKey == "" {
		options.overrides.APIKey = os.Getenv("SUPREMO_API_KEY")
	}
}

func runOneShot(application *app.App, session *agent.Session, options cliOptions) error {
	// One-shot runs never wait on an invisible confirmation. Without --approve,
	// reads may proceed but every mutating tool is reported as a dry run.
	session.NeedsName = false
	session.ApprovalMode = "superman"
	session.DryRun = !options.approve
	if options.plan {
		if err := application.Agent.SetPlanMode(context.Background(), session, true); err != nil {
			return err
		}
	}
	response, err := application.Agent.Run(context.Background(), session, options.prompt)
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, response)
	return nil
}

func runServer(application *app.App, options cliOptions) error {
	application.Backend.SetVersion(version)
	server, err := httptransport.Listen(options.listen, "", version, application.Backend)
	if err != nil {
		return err
	}
	defer server.Close()
	if err := application.Backend.Start(context.Background()); err != nil {
		return err
	}
	startup := map[string]any{"url": server.URL(), "token": server.Token(), "pid": os.Getpid(), "version": version}
	if err := json.NewEncoder(os.Stdout).Encode(startup); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return server.Serve(ctx)
}

func printUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: supremo [flags] [prompt]")
	fmt.Fprintln(output, "       echo 'prompt' | supremo [flags]")
	fmt.Fprintln(output, "       supremo serve [--listen 127.0.0.1:0]")
	fmt.Fprintln(output, "\nOne-shot flags: --prompt/-p, --session, --resume, --plan, --approve")
	fmt.Fprintln(output, "Runtime configuration: --provider, --model, --endpoint, --api-key")
	fmt.Fprintln(output, "Precedence: flags > SUPREMO_PROVIDER/MODEL/ENDPOINT/API_KEY > saved configuration > defaults.")
	fmt.Fprintln(output, "Without --approve, mutating actions are dry-run only. --plan performs research and prepares a plan; it never executes it.")
}

type messageSender interface{ Send(tea.Msg) }

func forwardInterrupts(ctx context.Context, program messageSender, interrupts <-chan os.Signal) {
	for {
		select {
		case <-ctx.Done():
			return
		case sig, ok := <-interrupts:
			if !ok {
				return
			}
			program.Send(ui.InterruptMsg{Terminate: sig == syscall.SIGTERM})
		}
	}
}
