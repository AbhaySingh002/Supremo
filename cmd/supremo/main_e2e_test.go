package main

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCLIProviderRuntimeEndToEnd(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer e2e-key" {
			t.Errorf("authorization = %q", got)
		}
		switch r.URL.Path {
		case "/models":
			fmt.Fprint(w, `{"data":[{"id":"fake-model","context_length":2048}]}`)
		case "/chat/completions":
			fmt.Fprint(w, `{"choices":[{"message":{"content":"<final_answer>CLI e2e complete</final_answer>"},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":5,"cost":0.00003}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	bin := filepath.Join(t.TempDir(), "supremo")
	build := exec.Command("go", "build", "-o", bin, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}

	state := t.TempDir()
	command := exec.Command(bin)
	command.Dir = state
	command.Env = append(os.Environ(), "HOME="+state, "USERPROFILE="+state)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = command.Stdout
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}

	var transcript strings.Builder
	var transcriptMu sync.Mutex
	complete := make(chan struct{})
	var completeOnce sync.Once
	go func() {
		scanner := bufio.NewScanner(output)
		for scanner.Scan() {
			line := scanner.Text()
			transcriptMu.Lock()
			transcript.WriteString(line)
			transcript.WriteByte('\n')
			transcriptMu.Unlock()
			if strings.Contains(line, "CLI e2e complete") {
				completeOnce.Do(func() { close(complete) })
			}
		}
		if err := scanner.Err(); err != nil {
			t.Errorf("scanner error: %v", err)
		}
	}()

	if _, err := io.WriteString(stdin, "/provider openai-compatible:e2e "+server.URL+"\n/auth e2e-key\n/model fake-model\nhello\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-complete:
	case <-time.After(15 * time.Second):
		_ = command.Process.Kill()
		transcriptMu.Lock()
		defer transcriptMu.Unlock()
		t.Fatalf("CLI did not finish task:\n%s", transcript.String())
	}
	if _, err := io.WriteString(stdin, "/usage\n/exit\n"); err != nil {
		t.Fatal(err)
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	transcriptMu.Lock()
	defer transcriptMu.Unlock()
	for _, want := range []string{"Provider updated to openai-compatible:e2e.", "API key updated and provider metadata cached.", "CLI e2e complete", "Runtime usage: input 20, output 5, cost $0.000030", "Selected model context: 2048 tokens"} {
		if !strings.Contains(transcript.String(), want) {
			t.Fatalf("CLI output missing %q:\n%s", want, transcript.String())
		}
	}
}
