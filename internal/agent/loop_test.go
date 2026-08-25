package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AbhaySingh002/supremo/internal/logging"
	"github.com/AbhaySingh002/supremo/internal/parser"
	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/providers"
	"github.com/AbhaySingh002/supremo/internal/runtime"
	"github.com/AbhaySingh002/supremo/internal/state"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

type retryTestProvider struct {
	results []retryTestResult
	calls   int
	prompts []*models.Prompt
}

type retryTestResult struct {
	completion *providers.Completion
	err        error
}

type oneRetryPolicy struct{ calls int }

func (p *oneRetryPolicy) Decide(event runtime.RetryEvent, _ *models.Prompt) runtime.RetryDecision {
	p.calls++
	return runtime.RetryDecision{ShouldRetry: event.Attempt == 0}
}

func (p *retryTestProvider) Chat(_ context.Context, prompt *models.Prompt) (*providers.Completion, error) {
	p.prompts = append(p.prompts, prompt)
	result := p.results[p.calls]
	p.calls++
	return result.completion, result.err
}

func TestCompleteWithRetryUsesProviderBackoffOnly(t *testing.T) {
	provider := &retryTestProvider{results: []retryTestResult{
		{err: &net.DNSError{IsTimeout: true}},
		{err: &net.DNSError{IsTimeout: true}},
		{completion: &providers.Completion{Text: "done"}},
	}}
	var delays []time.Duration
	worker := &Agent{provider: provider, retryWait: func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		return nil
	}}
	completion, err := worker.completeWithRetry(context.Background(), &Session{}, &models.Prompt{}, false, nil)
	if err != nil || completion == nil || completion.Text != "done" || provider.calls != 3 {
		t.Fatalf("completion=%#v calls=%d err=%v", completion, provider.calls, err)
	}
	if len(delays) != 2 || delays[0] != time.Second || delays[1] != 5*time.Second {
		t.Fatalf("delays=%v", delays)
	}
}

func TestCompleteWithRetryUsesConfiguredPolicy(t *testing.T) {
	provider := &retryTestProvider{results: []retryTestResult{
		{err: &providers.ProviderFailure{Code: providers.FailureServer, Message: "temporary"}},
		{completion: &providers.Completion{Text: "done"}},
	}}
	policy := &oneRetryPolicy{}
	worker := &Agent{provider: provider, retryPolicy: policy, retryWait: func(context.Context, time.Duration) error { return nil }}
	completion, err := worker.completeWithRetry(context.Background(), &Session{}, &models.Prompt{}, false, nil)
	if err != nil || completion == nil || completion.Text != "done" || provider.calls != 2 || policy.calls != 1 {
		t.Fatalf("completion=%#v provider_calls=%d policy_calls=%d err=%v", completion, provider.calls, policy.calls, err)
	}
}

func TestPlainTextCompletionNeedsOneRequest(t *testing.T) {
	provider := &retryTestProvider{results: []retryTestResult{{completion: &providers.Completion{Text: "Hello! How can I help you today?"}}}}
	worker := &Agent{provider: provider}
	completion, err := worker.completeWithRetry(context.Background(), &Session{}, &models.Prompt{}, false, func(completion *providers.Completion) error {
		_, err := worker.parseCompletion(&models.Prompt{}, completion)
		return err
	})
	if err != nil || completion == nil || provider.calls != 1 {
		t.Fatalf("completion=%#v calls=%d err=%v", completion, provider.calls, err)
	}
}

func TestCompleteWithRetryHonorsDisabledAndCancellation(t *testing.T) {
	disabled := false
	provider := &retryTestProvider{results: []retryTestResult{{err: &net.DNSError{IsTimeout: true}}}}
	worker := &Agent{provider: provider}
	_, err := worker.completeWithRetry(context.Background(), &Session{Features: &FeatureConfig{Retry: RetryConfig{Response: &disabled}}}, &models.Prompt{}, false, nil)
	if !errors.Is(err, ErrProviderUnavailable) || provider.calls != 1 {
		t.Fatalf("disabled retry calls=%d err=%v", provider.calls, err)
	}

	provider = &retryTestProvider{results: []retryTestResult{{err: &net.DNSError{IsTimeout: true}}}}
	ctx, cancel := context.WithCancel(context.Background())
	worker = &Agent{provider: provider, retryWait: func(ctx context.Context, _ time.Duration) error {
		cancel()
		<-ctx.Done()
		return ctx.Err()
	}}
	_, err = worker.completeWithRetry(ctx, &Session{}, &models.Prompt{}, false, nil)
	if !errors.Is(err, context.Canceled) || provider.calls != 1 {
		t.Fatalf("canceled retry calls=%d err=%v", provider.calls, err)
	}
}

func TestParseCompletionAcceptsPlainTextAndToolOnly(t *testing.T) {
	worker := &Agent{}
	prompt := &models.Prompt{ActiveTools: []string{"read_file"}}
	parsed, err := worker.parseCompletion(prompt, &providers.Completion{
		Text:      "Inspecting README",
		ToolCalls: []models.ToolCall{{ID: "call-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"README.md"}`)}},
	})
	if err != nil || len(parsed.ToolCalls) != 1 || parsed.ToolCalls[0].ID != "call-1" {
		t.Fatalf("parsed=%#v err=%v", parsed, err)
	}
	toolOnly, err := worker.parseCompletion(prompt, &providers.Completion{
		ToolCalls: []models.ToolCall{{ID: "call-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"README.md"}`)}},
	})
	if err != nil || len(toolOnly.ToolCalls) != 1 || toolOnly.TurnProgress != nil {
		t.Fatalf("tool-only native call should be valid: parsed=%#v err=%v", toolOnly, err)
	}
	if _, err := worker.parseCompletion(prompt, &providers.Completion{}); err == nil {
		t.Fatal("empty completion was accepted")
	}
}

func TestToolObservationExposesArtifactBeforeTruncatedPreview(t *testing.T) {
	observation := NewObservation("read_file", &tools.ToolResult{
		Status:     tools.ToolStatusCompleted,
		Success:    true,
		ArtifactID: "canonical-artifact",
		Preview:    strings.Repeat("content", 2_000),
	}, nil)
	artifact := strings.Index(observation.Output, `"artifact_id": "canonical-artifact"`)
	preview := strings.Index(observation.Output, `"preview":`)
	if artifact < 0 || preview < 0 || artifact > preview {
		t.Fatalf("artifact must precede preview: %s", observation.Output[:min(len(observation.Output), 300)])
	}
}

func TestTaskContextHasNoDeadlineAndStillCancels(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := &Agent{ephemeral: true}
	ctx, release, err := worker.taskContext(parent, &Session{ID: "task-context"})
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("task context retained an automatic deadline")
	}
	cancel()
	select {
	case <-ctx.Done():
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatalf("cancellation error = %v", ctx.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("task context did not propagate cancellation")
	}
}

func TestLoadSessionIgnoresRetiredBudgetFields(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.CloseWorkspace(root) })
	now := time.Now().UTC()
	_, err = store.SaveSession(context.Background(), state.SessionInput{
		ID:        "legacy-budget",
		Name:      "Legacy budget",
		CreatedAt: now,
		UpdatedAt: now,
		Data: json.RawMessage(`{
			"id":"legacy-budget",
			"name":"Legacy budget",
			"budget":{"max_tool_calls_total":1,"max_retry_budget":1}
		}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	session, err := LoadSession(root, "legacy-budget")
	if err != nil {
		t.Fatalf("load legacy budget session: %v", err)
	}
	if session.ID != "legacy-budget" || session.Name != "Legacy budget" {
		t.Fatalf("session=%#v", session)
	}
	if err := session.Save(root); err != nil {
		t.Fatalf("save legacy budget session: %v", err)
	}
	saved, err := store.Session(context.Background(), "legacy-budget")
	if err != nil || !json.Valid(saved.Data) || !strings.Contains(string(saved.Data), `"budget"`) {
		t.Fatalf("saved=%q err=%v", saved.Data, err)
	}
}

func TestNewSessionDefaultsToBatman(t *testing.T) {
	root := t.TempDir()
	session, err := NewSession(root)
	if err != nil || session.ApprovalMode != tools.ApprovalBatman {
		t.Fatalf("new session=%#v err=%v", session, err)
	}
	restored, err := LoadSession(root, session.ID)
	if err != nil || restored.ApprovalMode != tools.ApprovalBatman {
		t.Fatalf("restored session=%#v err=%v", restored, err)
	}
}

func TestExactModelRequestResponseLogging(t *testing.T) {
	prompt := &models.Prompt{
		System: "System instruction with api_key=sk-1234567890abcdef1234567890",
		Messages: []models.Message{
			{Role: models.RoleUser, Content: "Hello world"},
			{Role: models.RoleAssistant, Content: "Thinking", ToolCalls: []models.ToolCall{{ID: "call-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"main.go"}`)}}, TurnProgress: &models.TurnProgress{Progress: "Starting"}},
			{Role: models.RoleTool, Content: "package main", ToolCallID: "call-1", ToolName: "read_file"},
		},
		ActiveTools: []string{"read_file"},
		ToolDefinitions: []models.ToolDefinition{
			{Name: "read_file", Description: "Read a file", InputSchema: json.RawMessage(`{"type":"object"}`)},
		},
		Request: &models.AgentRequest{
			RequestID: "req-1",
			SessionID: "sess-1",
			TaskID:    "task-1",
			TurnID:    "3",
			Profile:   "execution",
			Budget:    models.RequestBudget{ContextLimit: 128000, OutputReserve: 2048, EstimatedUsed: 400},
			Sections: []models.ContextSection{{
				ID: "document:working_memory:task-1", Layer: "L2", Kind: "working_memory",
				Authority: "runtime", Provenance: "source=working_memory", Freshness: "current",
				Content: "known fact", EstimatedTokens: 4, SourceHash: "abc", ArtifactID: "art-1", SelectionReason: "hard_pinned",
			}},
			Rejected: []models.ContextRejection{{ID: "repository:stale.go", Reason: "stale", Signals: map[string]float64{"freshness": 0}}},
			Interactions: []models.Interaction{{
				ID: "i1", Sequence: 1,
				Assistant:   models.Message{Role: models.RoleAssistant, Content: "Thinking", ToolCalls: []models.ToolCall{{ID: "call-1", Name: "read_file"}}},
				ToolResults: []models.Message{{Role: models.RoleTool, ToolCallID: "call-1", ToolName: "read_file", Content: "package main"}},
			}},
		},
	}
	completion := &providers.Completion{
		Text:         `{"progress":"Read main.go","next_goal":"Edit main.go"}`,
		ToolCalls:    []models.ToolCall{{ID: "call-2", Name: "write_file", Arguments: json.RawMessage(`{"path":"main.go"}`)}},
		FinishReason: "stop",
		Usage:        providers.Usage{InputTokens: 100, OutputTokens: 50},
	}
	session := &Session{
		ID:       "sess-1",
		Provider: "test-provider",
		Model:    "test-model",
	}

	agent := &Agent{}
	agent.logExactModelRequest(session, prompt, false)
	logExactModelResponse(completion, &parser.Response{
		TurnProgress: &models.TurnProgress{Progress: "Read main.go", NextGoal: "Edit main.go", EvidenceUsed: []string{"art-1"}},
		ToolCalls:    completion.ToolCalls,
	})
	LogToolExecution(ToolExecutionLogParams{
		ToolName: "write_file", ToolCallID: "call-2", RawArguments: `{"path":"main.go"}`,
		ExecutionMode: "physical", ObservationID: "obs-1", SourceHash: "def", ArtifactID: "art-2",
		Success: true, Duration: time.Millisecond, Mutations: []string{"file:main.go"}, FreshnessInvalidation: []string{"main.go"},
	})
	LogStateTransition(StateTransitionLogParams{
		SessionID: session.ID, TaskID: "task-1", TurnSequence: 3,
		WorkingMemory:        &WorkingMemory{KnownRepositoryFacts: []string{"main.go exists"}},
		CurrentFocus:         &CurrentFocus{Established: "Read main.go", NextGoal: "Edit main.go"},
		RepositoryChanges:    []string{"file:main.go"},
		NextRequestReadiness: `Next turn conditioned on unresolved goal: "Edit main.go"`,
	})

	if logging.IsEnabled() {
		root := t.TempDir()
		cleanup := logging.Init(root)
		defer cleanup()
		agent.logExactModelRequest(session, prompt, false)
		cleanup()
		data, err := os.ReadFile(filepath.Join(root, ".supremo-dev", "logs", "supremo-debug.log"))
		if err != nil {
			t.Fatalf("expected debug log: %v", err)
		}
		content := string(data)
		for _, want := range []string{"TurnID:", "3", "REJECTED CANDIDATES", "stale", "[REDACTED]"} {
			if !strings.Contains(content, want) {
				t.Errorf("expected %q in debug log, got:\n%s", want, content)
			}
		}
		if strings.Contains(content, "sk-1234567890abcdef1234567890") {
			t.Errorf("leaked api key in debug log:\n%s", content)
		}
	}
}
