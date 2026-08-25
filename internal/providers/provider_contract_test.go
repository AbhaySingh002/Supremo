package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
)

func TestProviderContractTextCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":"Hello world"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`)
	}))
	defer server.Close()

	provider, err := NewOpenAIProvider(context.Background(), "test-key", "gpt-4o", server.URL)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	prompt := &models.Prompt{
		System:   "You are an assistant.",
		Messages: []models.Message{{Role: models.RoleUser, Content: "hi"}},
	}

	comp, err := provider.Chat(context.Background(), prompt)
	if err != nil {
		t.Fatalf("unexpected Chat error: %v", err)
	}
	if comp.Text != "Hello world" {
		t.Fatalf("Text = %q, want %q", comp.Text, "Hello world")
	}
	if comp.FinishReason != string(FinishStop) {
		t.Fatalf("FinishReason = %q, want %q", comp.FinishReason, FinishStop)
	}
	if comp.Usage.InputTokens != 10 || comp.Usage.OutputTokens != 5 {
		t.Fatalf("Usage = %#v, want 10 in / 5 out", comp.Usage)
	}
}

func TestProviderContractToolOnlyAssistantResponse(t *testing.T) {
	tests := []struct {
		name        string
		rawResponse string
	}{
		{
			name: "content null",
			rawResponse: `{
				"choices": [{
					"message": {
						"role": "assistant",
						"content": null,
						"tool_calls": [{
							"id": "call_search_1",
							"type": "function",
							"function": {
								"name": "search_file_name",
								"arguments": "{\"path\":\".\",\"pattern\":\"*\"}"
							}
						}]
					},
					"finish_reason": "tool_calls"
				}],
				"usage": {"prompt_tokens": 15, "completion_tokens": 8}
			}`,
		},
		{
			name: "content empty string",
			rawResponse: `{
				"choices": [{
					"message": {
						"role": "assistant",
						"content": "",
						"tool_calls": [{
							"id": "call_search_1",
							"type": "function",
							"function": {
								"name": "search_file_name",
								"arguments": "{\"path\":\".\",\"pattern\":\"*\"}"
							}
						}]
					},
					"finish_reason": "stop"
				}],
				"usage": {"prompt_tokens": 15, "completion_tokens": 8}
			}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintln(w, tt.rawResponse)
			}))
			defer server.Close()

			provider, err := NewOpenAIProvider(context.Background(), "test-key", "gpt-4o", server.URL)
			if err != nil {
				t.Fatalf("failed to create provider: %v", err)
			}

			prompt := &models.Prompt{
				ActiveTools: []string{"search_file_name"},
				ToolDefinitions: []models.ToolDefinition{{
					Name:        "search_file_name",
					Description: "Search files",
					InputSchema: json.RawMessage(`{"type":"object"}`),
				}},
				Messages: []models.Message{{Role: models.RoleUser, Content: "find files"}},
			}

			comp, err := provider.Chat(context.Background(), prompt)
			if err != nil {
				t.Fatalf("tool-only response was rejected as an error: %v", err)
			}
			if comp.Text != "" {
				t.Fatalf("expected empty Text, got %q", comp.Text)
			}
			if len(comp.ToolCalls) != 1 {
				t.Fatalf("expected 1 tool call, got %d", len(comp.ToolCalls))
			}
			tc := comp.ToolCalls[0]
			if tc.ID != "call_search_1" || tc.Name != "search_file_name" {
				t.Fatalf("unexpected ToolCall: %#v", tc)
			}
			var args map[string]string
			if err := json.Unmarshal(tc.Arguments, &args); err != nil || args["pattern"] != "*" {
				t.Fatalf("arguments parse failed or mismatched: %s", string(tc.Arguments))
			}
		})
	}
}

func TestProviderContractMultipleToolCallsPreserveOrderAndID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"choices": [{
				"message": {
					"role": "assistant",
					"content": "Reading files",
					"tool_calls": [
						{"id": "call_1", "type": "function", "function": {"name": "read_file", "arguments": "{\"path\":\"a.txt\"}"}},
						{"id": "call_2", "type": "function", "function": {"name": "read_file", "arguments": "{\"path\":\"b.txt\"}"}},
						{"id": "call_3", "type": "function", "function": {"name": "grep_search", "arguments": "{\"query\":\"fn\"}"}}
					]
				},
				"finish_reason": "tool_calls"
			}]
		}`)
	}))
	defer server.Close()

	provider, err := NewOpenAIProvider(context.Background(), "test-key", "gpt-4o", server.URL)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	prompt := &models.Prompt{
		ActiveTools: []string{"read_file", "grep_search"},
		Messages:    []models.Message{{Role: models.RoleUser, Content: "read files"}},
	}

	comp, err := provider.Chat(context.Background(), prompt)
	if err != nil {
		t.Fatalf("unexpected Chat error: %v", err)
	}
	if len(comp.ToolCalls) != 3 {
		t.Fatalf("expected 3 tool calls, got %d", len(comp.ToolCalls))
	}
	expected := []struct {
		id   string
		name string
	}{
		{"call_1", "read_file"},
		{"call_2", "read_file"},
		{"call_3", "grep_search"},
	}
	for i, exp := range expected {
		if comp.ToolCalls[i].ID != exp.id || comp.ToolCalls[i].Name != exp.name {
			t.Fatalf("ToolCall[%d] = %#v, want id=%s name=%s", i, comp.ToolCalls[i], exp.id, exp.name)
		}
	}
}

func TestProviderContractToolResultSerialization(t *testing.T) {
	var capturedRequest openAIChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&capturedRequest); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	provider, err := NewOpenAIProvider(context.Background(), "test-key", "gpt-4o", server.URL)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	prompt := &models.Prompt{
		ActiveTools: []string{"read_file"},
		Messages: []models.Message{
			{Role: models.RoleUser, Content: "read a.txt"},
			{
				Role:      models.RoleAssistant,
				Content:   "",
				ToolCalls: []models.ToolCall{{ID: "call_orig_123", Name: "read_file", Arguments: json.RawMessage(`{"path":"a.txt"}`)}},
			},
			{
				Role:       models.RoleTool,
				Content:    "file contents of a.txt",
				ToolCallID: "call_orig_123",
				ToolName:   "read_file",
			},
		},
	}

	_, err = provider.Chat(context.Background(), prompt)
	if err != nil {
		t.Fatalf("unexpected Chat error: %v", err)
	}

	// Verify the serialized request
	if len(capturedRequest.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(capturedRequest.Messages))
	}
	asstMsg := capturedRequest.Messages[1]
	if asstMsg.Role != "assistant" || len(asstMsg.ToolCalls) != 1 || asstMsg.ToolCalls[0].ID != "call_orig_123" {
		t.Fatalf("unexpected assistant message serialization: %#v", asstMsg)
	}
	toolMsg := capturedRequest.Messages[2]
	if toolMsg.Role != "tool" || toolMsg.ToolCallID != "call_orig_123" || toolMsg.Content != "file contents of a.txt" {
		t.Fatalf("unexpected tool result message serialization: %#v", toolMsg)
	}
}

func TestProviderContractEmptyResponseBecomesFailureEmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":""},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	provider, err := NewOpenAIProvider(context.Background(), "test-key", "gpt-4o", server.URL)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	prompt := &models.Prompt{Messages: []models.Message{{Role: models.RoleUser, Content: "hi"}}}
	_, err = provider.Chat(context.Background(), prompt)
	if err == nil {
		t.Fatal("expected empty response error, got nil")
	}
	if !IsMalformedOutput(err) {
		t.Fatalf("expected IsMalformedOutput to be true for empty response: %v", err)
	}
	var failure *ProviderFailure
	if !errors.As(err, &failure) || failure.Code != FailureEmptyResponse {
		t.Fatalf("expected FailureEmptyResponse, got %v", err)
	}
}

func TestProviderContractStreamingReasoningAndText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected flusher")
		}
		chunks := []string{
			`data: {"choices":[{"delta":{"reasoning_content":"Thinking step 1"}}]}`,
			`data: {"choices":[{"delta":{"reasoning_content":"Thinking step 2"}}]}`,
			`data: {"choices":[{"delta":{"content":"Answer"}}]}`,
			`data: {"choices":[{"delta":{"content":" part 2"}}],"finish_reason":"stop","usage":{"prompt_tokens":8,"completion_tokens":4}}`,
			`data: [DONE]`,
		}
		for _, chunk := range chunks {
			fmt.Fprintln(w, chunk)
			flusher.Flush()
		}
	}))
	defer server.Close()

	provider, err := NewOpenAIProvider(context.Background(), "test-key", "gpt-4o", server.URL)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	var textSeen strings.Builder
	assembler := NewAssistantAssembler(nil, func(delta string) { textSeen.WriteString(delta) })
	err = provider.Stream(context.Background(), &models.Prompt{
		Messages: []models.Message{{Role: models.RoleUser, Content: "hello"}},
	}, func(event StreamEvent) error {
		return assembler.Feed(event)
	})
	if err != nil {
		t.Fatalf("unexpected stream error: %v", err)
	}
	comp, err := assembler.Assemble()
	if err != nil {
		t.Fatalf("unexpected assembly error: %v", err)
	}
	if comp.Text != "Answer part 2" {
		t.Fatalf("assembled Text = %q, want %q", comp.Text, "Answer part 2")
	}
	if textSeen.String() != "Answer part 2" {
		t.Fatalf("streamed text listener received %q, want %q", textSeen.String(), "Answer part 2")
	}
	if comp.FinishReason != string(FinishStop) {
		t.Fatalf("finish reason = %q", comp.FinishReason)
	}
}

func TestProviderContractMaxTokensTruncationDiscardsIncompleteTool(t *testing.T) {
	assembler := NewAssistantAssembler([]string{"read_file"}, nil)
	_ = assembler.Feed(StreamEvent{Type: StreamEventTextDelta, TextDelta: "Working on it"})
	_ = assembler.Feed(StreamEvent{
		Type: StreamEventToolCallDelta,
		ToolCall: &ToolCallDelta{
			Index:          0,
			ID:             "call_1",
			Name:           "read_file",
			ArgumentsDelta: `{"path": "incomplete`,
		},
	})
	_ = assembler.Feed(StreamEvent{Type: StreamEventFinish, FinishReason: FinishMaxTokens})

	comp, err := assembler.Assemble()
	if err != nil {
		t.Fatalf("unexpected assemble error: %v", err)
	}
	if comp.Text != "Working on it" {
		t.Fatalf("Text = %q, want %q", comp.Text, "Working on it")
	}
	if len(comp.ToolCalls) != 0 {
		t.Fatalf("expected incomplete tool call to be discarded on FinishMaxTokens, got %d calls", len(comp.ToolCalls))
	}
	if !comp.Truncated() {
		t.Fatal("expected Truncated() to be true")
	}
}

func TestProviderContractCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		fmt.Fprintf(w, `{"choices":[{"message":{"content":"late"}}]}`)
	}))
	defer server.Close()

	provider, err := NewOpenAIProvider(context.Background(), "test-key", "gpt-4o", server.URL)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err = provider.Chat(ctx, &models.Prompt{Messages: []models.Message{{Role: models.RoleUser, Content: "hi"}}})
	if err == nil {
		t.Fatal("expected error on cancelled context, got nil")
	}
	var failure *ProviderFailure
	if errors.As(err, &failure) && failure.Code == FailureAborted {
		// Valid canonical failure code
	} else if !strings.Contains(err.Error(), "canceled") && !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("unexpected cancellation error: %v", err)
	}
}

func TestProviderContractRateLimitAndRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprintf(w, `{"error":{"message":"Rate limit exceeded"}}`)
	}))
	defer server.Close()

	provider, err := NewOpenAIProvider(context.Background(), "test-key", "gpt-4o", server.URL)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	_, err = provider.Chat(context.Background(), &models.Prompt{Messages: []models.Message{{Role: models.RoleUser, Content: "hi"}}})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !IsTransient(err) {
		t.Fatalf("expected 429 to be classified as transient: %v", err)
	}
	var failure *ProviderFailure
	if errors.As(err, &failure) {
		if failure.Code != FailureRateLimit {
			t.Fatalf("Code = %v, want FailureRateLimit", failure.Code)
		}
		if failure.RetryAfter != 3*time.Second {
			t.Fatalf("RetryAfter = %v, want 3s", failure.RetryAfter)
		}
	}
}

func TestProviderContractContextWindowExceeded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"error":{"message":"This model's maximum context length is 128000 tokens. However, your messages resulted in 130000 tokens."}}`)
	}))
	defer server.Close()

	provider, err := NewOpenAIProvider(context.Background(), "test-key", "gpt-4o", server.URL)
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	_, err = provider.Chat(context.Background(), &models.Prompt{Messages: []models.Message{{Role: models.RoleUser, Content: "big prompt"}}})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !IsContextOverflow(err) {
		t.Fatalf("expected IsContextOverflow to be true for context length error: %v", err)
	}
}

func TestProviderContractCrossProviderReplay(t *testing.T) {
	// Step 1: Simulated response from Adapter A (e.g. Gemini / OpenAI)
	// generates ToolCall call_replay_99
	toolCall := models.ToolCall{
		ID:        "call_replay_99",
		Name:      "read_file",
		Arguments: json.RawMessage(`{"path":"main.go"}`),
	}
	toolResult := models.Message{
		Role:       models.RoleTool,
		Content:    "package main\nfunc main() {}",
		ToolCallID: "call_replay_99",
		ToolName:   "read_file",
	}

	prompt := &models.Prompt{
		ActiveTools: []string{"read_file"},
		ToolDefinitions: []models.ToolDefinition{{
			Name:        "read_file",
			Description: "Read file contents",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}},
		Messages: []models.Message{
			{Role: models.RoleUser, Content: "check main.go"},
			{Role: models.RoleAssistant, ToolCalls: []models.ToolCall{toolCall}},
			toolResult,
		},
	}

	// Step 2: Serialize history through OpenAI adapter
	openAIMsgs := openAIChatMessages(prompt)
	if len(openAIMsgs) != 3 {
		t.Fatalf("OpenAI messages length = %d, want 3", len(openAIMsgs))
	}
	if openAIMsgs[1].ToolCalls[0].ID != "call_replay_99" {
		t.Fatalf("OpenAI tool call ID lost: %q", openAIMsgs[1].ToolCalls[0].ID)
	}
	if openAIMsgs[2].ToolCallID != "call_replay_99" {
		t.Fatalf("OpenAI tool result ToolCallID lost: %q", openAIMsgs[2].ToolCallID)
	}

	// Step 3: Serialize same history through Anthropic provider
	anthropicMsgs := make([]any, 0)
	for _, msg := range providerMessages(prompt) {
		if msg.Role == models.RoleTool {
			anthropicMsgs = append(anthropicMsgs, map[string]any{
				"type":        "tool_result",
				"tool_use_id": msg.ToolCallID,
				"content":     msg.Content,
			})
		}
	}
	if len(anthropicMsgs) != 1 {
		t.Fatalf("Anthropic tool results length = %d, want 1", len(anthropicMsgs))
	}
	toolResultBlock := anthropicMsgs[0].(map[string]any)
	if toolResultBlock["tool_use_id"] != "call_replay_99" {
		t.Fatalf("Anthropic tool_use_id lost: %v", toolResultBlock["tool_use_id"])
	}

	// Step 4: Serialize same history through Gemini provider
	geminiContents, _ := geminiRequest(prompt, "gemini-3.6-flash")
	if len(geminiContents) != 3 {
		t.Fatalf("Gemini contents length = %d, want 3", len(geminiContents))
	}
	if geminiContents[1].Parts[0].FunctionCall.ID != "call_replay_99" {
		t.Fatalf("Gemini function call ID lost: %q", geminiContents[1].Parts[0].FunctionCall.ID)
	}
	if geminiContents[2].Parts[0].FunctionResponse.ID != "call_replay_99" {
		t.Fatalf("Gemini function response ID lost: %q", geminiContents[2].Parts[0].FunctionResponse.ID)
	}
}

func toolOnlyPrompt() *models.Prompt {
	return &models.Prompt{
		ActiveTools: []string{"search_file_name"},
		ToolDefinitions: []models.ToolDefinition{{
			Name:        "search_file_name",
			Description: "Search files",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}},
		Messages: []models.Message{{Role: models.RoleUser, Content: "find files"}},
	}
}

func assertToolOnlyCompletion(t *testing.T, comp *Completion, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("tool-only response rejected: %v", err)
	}
	if IsMalformedOutput(err) {
		t.Fatalf("tool-only classified as malformed: %v", err)
	}
	if comp.Text != "" || len(comp.ToolCalls) != 1 || comp.ToolCalls[0].ID != "call_search_1" || comp.ToolCalls[0].Name != "search_file_name" {
		t.Fatalf("completion=%#v", comp)
	}
}

func TestProviderContractOpenAIStreamToolOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_search_1","type":"function","function":{"name":"search_file_name","arguments":"{\"path\":\".\",\"pattern\":\"*\"}"}}]}}]}`)
		fmt.Fprintln(w, `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":4,"completion_tokens":6}}`)
		fmt.Fprintln(w, `data: [DONE]`)
	}))
	defer server.Close()
	provider, err := NewOpenAIProvider(context.Background(), "test-key", "gpt-4o", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	assembler := NewAssistantAssembler([]string{"search_file_name"}, nil)
	err = provider.Stream(context.Background(), toolOnlyPrompt(), assembler.Feed)
	comp, assembleErr := assembler.Assemble()
	if err == nil {
		err = assembleErr
	}
	assertToolOnlyCompletion(t, comp, err)
}

func TestProviderContractAnthropicToolOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"content":[{"type":"tool_use","id":"call_search_1","name":"search_file_name","input":{"path":".","pattern":"*"}}],"stop_reason":"tool_use","usage":{"input_tokens":4,"output_tokens":6}}`)
	}))
	defer server.Close()
	provider, err := NewAnthropicProvider(context.Background(), "test-key", "claude-test", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	comp, err := provider.Chat(context.Background(), toolOnlyPrompt())
	assertToolOnlyCompletion(t, comp, err)
}

func TestProviderContractMistralToolOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"outputs":[{"type":"function.call","tool_call_id":"call_search_1","name":"search_file_name","arguments":{"path":".","pattern":"*"}}],"usage":{"prompt_tokens":4,"completion_tokens":6}}`)
	}))
	defer server.Close()
	provider, err := NewMistralProvider(context.Background(), "test-key", "model", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	comp, err := provider.Chat(context.Background(), toolOnlyPrompt())
	assertToolOnlyCompletion(t, comp, err)
}

func TestProviderContractOpenRouterToolOnlyNullContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"chat-1","model":"vendor/model","object":"chat.completion","created":1,"choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_search_1","type":"function","function":{"name":"search_file_name","arguments":"{\"path\":\".\",\"pattern\":\"*\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":4,"completion_tokens":6,"total_tokens":10}}`)
	}))
	defer server.Close()
	provider, err := NewOpenRouterProvider(context.Background(), "test-key", "vendor/model", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	comp, err := provider.Chat(context.Background(), toolOnlyPrompt())
	assertToolOnlyCompletion(t, comp, err)
}
