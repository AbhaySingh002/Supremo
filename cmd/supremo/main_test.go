package main

import (
	"os"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/agent"
)

func TestVersionDefaultsToNonEmptyValue(t *testing.T) {
	if version == "" {
		t.Fatal("version must be set for --version output")
	}
}

func TestDefaultCLIStartsFreshSessionAndResumeIsExplicit(t *testing.T) {
	options, err := parseCLI([]string{"--prompt", "hi"}, os.Stdin)
	if err != nil || options.session != "" || options.resume != "" {
		t.Fatalf("default options unexpectedly select a session: %#v, %v", options, err)
	}
	root := t.TempDir()
	first, err := openSession(root, options)
	if err != nil {
		t.Fatal(err)
	}
	second, err := openSession(root, options)
	if err != nil || first.ID == second.ID {
		t.Fatalf("default CLI session was reused: %q %q, %v", first.ID, second.ID, err)
	}
	if _, err := agent.LoadSession(root, first.ID); err != nil {
		t.Fatal(err)
	}
	resume, err := parseCLI([]string{"--resume", first.ID, "--prompt", "hi"}, os.Stdin)
	if err != nil || resume.resume != first.ID {
		t.Fatalf("resume parsing failed: %#v, %v", resume, err)
	}
	restored, err := openSession(root, resume)
	if err != nil || restored.ID != first.ID {
		t.Fatalf("explicit resume did not restore the requested session: %#v, %v", restored, err)
	}
}

func TestCLIRejectsCompetingSessionFlags(t *testing.T) {
	if _, err := parseCLI([]string{"--session", "one", "--resume", "two", "--prompt", "hi"}, os.Stdin); err == nil {
		t.Fatal("expected conflicting session flags to fail")
	}
}

func TestParseCLIUsesFlagsBeforeEnvironment(t *testing.T) {
	t.Setenv("SUPREMO_PROVIDER", "openai")
	t.Setenv("SUPREMO_MODEL", "env-model")
	options, err := parseCLI([]string{"--prompt", "hello", "--provider", "groq", "--model", "flag-model"}, os.Stdin)
	if err != nil || options.prompt != "hello" || options.overrides.Provider != "groq" || options.overrides.Model != "flag-model" {
		t.Fatalf("options=%#v err=%v", options, err)
	}
}

func TestParseCLIReadsPipedPrompt(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteString("explain this project\n"); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	defer reader.Close()
	options, err := parseCLI(nil, reader)
	if err != nil || options.prompt != "explain this project" {
		t.Fatalf("options=%#v err=%v", options, err)
	}
}

func TestParseCLIServeIsExplicitAndDoesNotReadStdin(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	defer reader.Close()
	options, err := parseCLI([]string{"serve", "--listen", "127.0.0.1:9000"}, reader)
	if err != nil || !options.serve || options.listen != "127.0.0.1:9000" || options.prompt != "" {
		t.Fatalf("serve options=%#v err=%v", options, err)
	}
	if _, err := parseCLI([]string{"serve", "unexpected prompt"}, reader); err == nil {
		t.Fatal("serve must reject positional prompts")
	}
}
