package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/OpenRouterTeam/go-sdk/models/sdkerrors"
	"google.golang.org/genai"
)

type timeoutError struct{}

func (timeoutError) Error() string   { return "timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func TestIsTransient(t *testing.T) {
	for name, test := range map[string]struct {
		err  error
		want bool
	}{
		"http 429":        {err: &httpStatusError{code: http.StatusTooManyRequests}, want: true},
		"http 503":        {err: &httpStatusError{code: http.StatusServiceUnavailable}, want: true},
		"http 401":        {err: &httpStatusError{code: http.StatusUnauthorized}},
		"gemini 500":      {err: genai.APIError{Code: http.StatusInternalServerError}, want: true},
		"gemini 400":      {err: genai.APIError{Code: http.StatusBadRequest}},
		"network timeout": {err: timeoutError{}, want: true},
		"permanent":       {err: errors.New("invalid API key")},
	} {
		t.Run(name, func(t *testing.T) {
			if got := IsTransient(test.err); got != test.want {
				t.Fatalf("IsTransient(%v) = %t, want %t", test.err, got, test.want)
			}
		})
	}
}

func TestGeminiAPIErrorClassifiesCredentialsWithoutHidingBadRequests(t *testing.T) {
	invalidKey := fmt.Errorf("gemini execution: %w", genai.APIError{Code: http.StatusBadRequest, Status: "INVALID_ARGUMENT", Message: "API key not valid. Please pass a valid API key."})
	if code, status, message, ok := GeminiAPIError(invalidKey); !ok || code != http.StatusBadRequest || status != "INVALID_ARGUMENT" || !strings.Contains(message, "API key") {
		t.Fatalf("GeminiAPIError() = %d %q %q %t", code, status, message, ok)
	}
	if !IsAuthenticationError(invalidKey) {
		t.Fatal("explicit Gemini invalid-key response was not recognized")
	}

	invalidRequest := fmt.Errorf("gemini execution: %w", genai.APIError{Code: http.StatusBadRequest, Status: "INVALID_ARGUMENT", Message: "thinking_level is not supported by this model"})
	if IsAuthenticationError(invalidRequest) {
		t.Fatal("generic Gemini INVALID_ARGUMENT was misclassified as an API key failure")
	}
}

func TestOpenRouterRefreshAndRuntimeUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("authorization = %q", got)
		}
		switch r.URL.Path {
		case "/models":
			fmt.Fprint(w, `{"data":[{"id":"vendor/model","name":"Model","context_length":32768,"pricing":{"prompt":"0.000001","completion":"0.000002"}}],"links":{"next":null},"total_count":1}`)
		case "/credits":
			fmt.Fprint(w, `{"data":{"total_credits":10,"total_usage":2.5}}`)
		case "/chat/completions":
			fmt.Fprint(w, `{"id":"chat-1","model":"vendor/model","object":"chat.completion","created":1,"choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":4,"total_tokens":16,"cost":0.00002}}`)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	if err := SaveConfig(dir, &Config{ProviderName: "openrouter", Model: "vendor/model", Endpoint: server.URL}); err != nil {
		t.Fatal(err)
	}
	credentials := NewFileCredentialStore(dir)
	if err := credentials.SetAPIKey("openrouter", "test-key"); err != nil {
		t.Fatal(err)
	}
	manager := newBuiltinManager(t, dir, credentials)
	if err := manager.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := manager.RefreshMetadata(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := manager.ContextLimit(); got != 32768 {
		t.Fatalf("context limit = %d, want 32768", got)
	}
	completion, err := manager.Chat(context.Background(), &models.Prompt{Messages: []models.Message{{Role: models.RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if completion.Text != "done" || completion.Usage.InputTokens != 12 {
		t.Fatalf("unexpected completion: %#v", completion)
	}
	usage := manager.GetRuntimeConfig().Usage()
	if usage.OutputTokens != 4 || usage.CostUSD == nil || *usage.CostUSD != 0.00002 {
		t.Fatalf("unexpected runtime usage: %#v", usage)
	}
	if account := manager.GetRuntimeConfig().Metadata().Account; account == nil || account.TotalCredits-account.TotalUsage != 7.5 {
		t.Fatalf("unexpected account metadata: %#v", account)
	}

	secondManager := newBuiltinManager(t, dir, credentials)
	if err := secondManager.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := secondManager.ContextLimit(); got != 32768 {
		t.Fatalf("cached context limit = %d, want 32768", got)
	}
}

func TestOpenRouterProviderUsesPlainAssistantCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" {
			t.Fatalf("request = %s %s, want POST /chat/completions", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("authorization = %q", got)
		}
		var request struct {
			Model               string `json:"model"`
			MaxCompletionTokens *int64 `json:"max_completion_tokens"`
			Stream              *bool  `json:"stream"`
			ResponseFormat      struct {
				Type string `json:"type"`
			} `json:"response_format"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Model != "vendor/model" || request.MaxCompletionTokens != nil || request.Stream == nil || *request.Stream || request.ResponseFormat.Type != "" {
			t.Errorf("request = %#v", request)
		}
		want := []struct{ role, content string }{{"system", "system"}, {"user", "hi"}, {"assistant", "planner output"}, {"user", continuationInstruction}}
		if len(request.Messages) != len(want) {
			t.Fatalf("messages = %#v", request.Messages)
		}
		for index, message := range request.Messages {
			if message.Role != want[index].role || message.Content != want[index].content {
				t.Errorf("message %d = %#v, want %#v", index, message, want[index])
			}
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"chat-1","model":"vendor/model","object":"chat.completion","created":1,"choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5,"cost":0.25}}`)
	}))
	defer server.Close()

	provider, err := NewOpenRouterProvider(context.Background(), "test-key", "vendor/model", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	prompt := &models.Prompt{System: "system", OutputReserve: 42, Messages: []models.Message{{Role: models.RoleUser, Content: "hi"}, {Role: models.RoleAssistant, Content: "planner output"}}}
	completion, err := provider.Chat(context.Background(), prompt)
	if err != nil {
		t.Fatal(err)
	}
	if completion.Text != "done" || completion.FinishReason != "stop" || completion.Usage.InputTokens != 3 || completion.Usage.OutputTokens != 2 || completion.Usage.CostUSD == nil || *completion.Usage.CostUSD != 0.25 {
		t.Fatalf("completion = %#v", completion)
	}
	if len(prompt.Messages) != 2 {
		t.Fatalf("prompt history was mutated: %#v", prompt.Messages)
	}
}

func TestOpenRouterProviderDoesNotForceJSONAlongsideToolProtocol(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var request map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		_, hasFormat := request["response_format"]
		if string(request["reasoning_effort"]) != `"low"` {
			t.Fatalf("reasoning_effort = %s", request["reasoning_effort"])
		}
		var nativeTools []struct {
			Type     string `json:"type"`
			Function struct {
				Name       string         `json:"name"`
				Parameters map[string]any `json:"parameters"`
			} `json:"function"`
		}
		if json.Unmarshal(request["tools"], &nativeTools) != nil || len(nativeTools) != 1 || nativeTools[0].Type != "function" || nativeTools[0].Function.Name != "read_file" || nativeTools[0].Function.Parameters["type"] != "object" {
			t.Fatalf("tools = %s", request["tools"])
		}
		if requests == 1 && hasFormat {
			t.Fatalf("initial tool-capable request forced response_format: %s", request["response_format"])
		}
		if requests == 2 && hasFormat {
			t.Fatalf("tool-capable repair request forced response_format: %s", request["response_format"])
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"chat-1","model":"openai/gpt-oss-120b","object":"chat.completion","created":1,"choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call-1","type":"function","function":{"name":"repo_browser.read_file","arguments":"{\"path\":\"README.md\"}"}}]},"finish_reason":"tool_calls"}]}`)
	}))
	defer server.Close()

	provider, err := NewOpenRouterProvider(context.Background(), "test-key", "openai/gpt-oss-120b", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	toolDefinitions := []models.ToolDefinition{{Name: "read_file", Description: "Read a file", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)}}
	completion, err := provider.Chat(context.Background(), &models.Prompt{ActiveTools: []string{"read_file"}, ToolDefinitions: toolDefinitions})
	if err != nil {
		t.Fatal(err)
	}
	if completion.Text != "" || len(completion.ToolCalls) != 1 || completion.ToolCalls[0].ID != "call-1" || completion.ToolCalls[0].Name != "read_file" {
		t.Fatalf("completion = %#v", completion)
	}
	prompt := &models.Prompt{System: "system", ActiveTools: []string{"read_file"}, ToolDefinitions: toolDefinitions}
	if _, err := provider.Chat(context.Background(), prompt); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRouterProviderPreservesAssistantContentWithToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"chat-1","model":"google/gemini-1.5-flash","object":"chat.completion","created":1,"choices":[{"index":0,"message":{"role":"assistant","content":"{\"progress\":\"listing files\",\"next_goal\":\"read README\"}","tool_calls":[{"id":"call-1","type":"function","function":{"name":"search_file_name","arguments":"{\"path\":\".\",\"pattern\":\"*\"}"}}]},"finish_reason":"tool_calls"}]}`)
	}))
	defer server.Close()

	provider, err := NewOpenRouterProvider(context.Background(), "test-key", "google/gemini-1.5-flash", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	completion, err := provider.Chat(context.Background(), &models.Prompt{
		ActiveTools:     []string{"search_file_name"},
		ToolDefinitions: []models.ToolDefinition{{Name: "search_file_name", Description: "Find files", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if completion.Text == "" || len(completion.ToolCalls) != 1 || completion.ToolCalls[0].Name != "search_file_name" {
		t.Fatalf("expected content and tool calls, got %#v", completion)
	}
}

func TestOpenRouterProviderRestoresNativeToolConversationState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ResponseFormat json.RawMessage `json:"response_format"`
			Messages       []struct {
				Role       string `json:"role"`
				Content    string `json:"content"`
				ToolCallID string `json:"tool_call_id"`
				ToolCalls  []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if len(request.Messages) != 3 {
			t.Fatalf("messages = %#v", request.Messages)
		}
		assistant, tool := request.Messages[1], request.Messages[2]
		if assistant.Role != "assistant" || assistant.Content != "" || len(assistant.ToolCalls) != 1 || assistant.ToolCalls[0].Function.Name != "list_directory" || assistant.ToolCalls[0].Function.Arguments != `{"path":"."}` {
			t.Fatalf("assistant = %#v", assistant)
		}
		if tool.Role != "tool" || tool.ToolCallID == "" || tool.ToolCallID != assistant.ToolCalls[0].ID || tool.Content != `{"tool":"list_directory","result":{"status":"completed"}}` {
			t.Fatalf("tool = %#v", tool)
		}
		if len(request.ResponseFormat) != 0 {
			t.Fatal("tool follow-up unexpectedly enabled a JSON response format")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"chat-1","model":"vendor/model","object":"chat.completion","created":1,"choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	provider, err := NewOpenRouterProvider(context.Background(), "test-key", "vendor/model", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	prompt := &models.Prompt{System: "system", ActiveTools: []string{"list_directory"}, Messages: []models.Message{
		{Role: models.RoleAssistant, ToolCalls: []models.ToolCall{{ID: "call-1", Name: "list_directory", Arguments: json.RawMessage(`{"path":"."}`)}}},
		{Role: models.RoleTool, ToolCallID: "call-1", ToolName: "list_directory", Content: `{"tool":"list_directory","result":{"status":"completed"}}`},
	}}
	completion, err := provider.Chat(context.Background(), prompt)
	if err != nil || completion.Text != "done" {
		t.Fatalf("completion = %#v, err = %v", completion, err)
	}
}

func TestOpenRouterProviderHandlesTypedSDKContentAndEmptyResults(t *testing.T) {
	tests := []struct {
		name      string
		response  string
		want      string
		wantError string
		truncated bool
	}{
		{
			name:     "structured text",
			response: `{"id":"chat-1","model":"vendor/model","object":"chat.completion","created":1,"choices":[{"index":0,"message":{"role":"assistant","content":[{"type":"text","text":"part one"},{"type":"text","text":" and two"}]},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`,
			want:     "part one and two",
		},
		{
			name:      "missing choices",
			response:  `{"id":"chat-1","model":"vendor/model","object":"chat.completion","created":1,"choices":[]}`,
			wantError: "no completion choices",
		},
		{
			name:      "refusal",
			response:  `{"id":"chat-1","model":"vendor/model","object":"chat.completion","created":1,"choices":[{"index":0,"message":{"role":"assistant","content":null,"refusal":"blocked"},"finish_reason":"stop"}]}`,
			wantError: "refused the response: blocked",
		},
		{
			name:      "reasoning only",
			response:  `{"id":"chat-1","model":"vendor/model","object":"chat.completion","created":1,"choices":[{"index":0,"message":{"role":"assistant","content":null,"reasoning":"thinking"},"finish_reason":"stop"}]}`,
			wantError: "reasoning without final content",
		},
		{
			name:      "empty final",
			response:  `{"id":"chat-1","model":"vendor/model","object":"chat.completion","created":1,"choices":[{"index":0,"message":{"role":"assistant","content":null},"finish_reason":"stop"}]}`,
			wantError: "empty final content",
		},
		{
			name:      "token limit",
			response:  `{"id":"chat-1","model":"vendor/model","object":"chat.completion","created":1,"choices":[{"index":0,"message":{"role":"assistant","content":null,"reasoning":"unfinished"},"finish_reason":"length"}],"usage":{"prompt_tokens":3,"completion_tokens":42,"total_tokens":45}}`,
			truncated: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, test.response)
			}))
			defer server.Close()

			provider, err := NewOpenRouterProvider(context.Background(), "test-key", "vendor/model", server.URL)
			if err != nil {
				t.Fatal(err)
			}
			completion, err := provider.Chat(context.Background(), &models.Prompt{})
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("Chat() error = %v, want %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if completion.Text != test.want || completion.Truncated() != test.truncated {
				t.Fatalf("completion = %#v", completion)
			}
			if test.truncated && completion.Usage.OutputTokens != 42 {
				t.Fatalf("truncated usage = %#v", completion.Usage)
			}
		})
	}
}

func TestOpenRouterProviderPreservesNativeToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"chat-1","model":"openai/gpt-oss-120b","object":"chat.completion","created":1,"choices":[{"index":0,"message":{"role":"assistant","content":null,"reasoning":"inspect first","tool_calls":[{"id":"call-1","type":"function","function":{"name":"repo_browser.list_directory","arguments":"{\"path\":\".\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`)
	}))
	defer server.Close()

	provider, err := NewOpenRouterProvider(context.Background(), "test-key", "openai/gpt-oss-120b", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	completion, err := provider.Chat(context.Background(), &models.Prompt{ActiveTools: []string{"list_directory"}})
	if err != nil {
		t.Fatal(err)
	}
	if completion.Text != "" || len(completion.ToolCalls) != 1 || completion.ToolCalls[0].ID != "call-1" || completion.ToolCalls[0].Name != "list_directory" {
		t.Fatalf("native tool call = %#v", completion)
	}
	if completion.FinishReason != "tool_calls" {
		t.Fatalf("finish reason = %q", completion.FinishReason)
	}
}

func TestOpenRouterProviderPreservesMalformedNativeToolCallArguments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"chat-1","model":"vendor/model","object":"chat.completion","created":1,"choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call-1","type":"function","function":{"name":"read_file","arguments":"not-json"}}]},"finish_reason":"tool_calls"}]}`)
	}))
	defer server.Close()

	provider, err := NewOpenRouterProvider(context.Background(), "test-key", "vendor/model", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	completion, err := provider.Chat(context.Background(), &models.Prompt{ActiveTools: []string{"read_file"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(completion.ToolCalls) != 1 || string(completion.ToolCalls[0].Arguments) != "not-json" {
		t.Fatalf("completion = %#v", completion)
	}
}

func TestOpenRouterProviderClassifiesChoiceErrorAsTransient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"chat-1","model":"vendor/model","object":"chat.completion","created":1,"choices":[{"index":0,"message":{"role":"assistant","content":null,"reasoning":"partial"},"finish_reason":"error","error":{"code":502,"message":"upstream failed"}}]}`)
	}))
	defer server.Close()

	provider, err := NewOpenRouterProvider(context.Background(), "test-key", "vendor/model", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Chat(context.Background(), &models.Prompt{})
	if err == nil || !IsTransient(err) || !strings.Contains(err.Error(), "upstream model failed") {
		t.Fatalf("Chat() error = %v, transient = %t", err, IsTransient(err))
	}
}

func TestOpenRouterProviderDoesNotStreamAndClassifiesHTTPFailures(t *testing.T) {
	provider, err := NewOpenRouterProvider(context.Background(), "test-key", "vendor/model", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := any(provider).(StreamProvider); ok {
		t.Fatal("OpenRouter provider implements StreamProvider")
	}

	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusRequestEntityTooLarge, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				http.Error(w, "OpenRouter failure", status)
			}))
			defer server.Close()
			provider, err := NewOpenRouterProvider(context.Background(), "test-key", "vendor/model", server.URL)
			if err != nil {
				t.Fatal(err)
			}
			_, err = provider.Chat(context.Background(), &models.Prompt{})
			if err == nil {
				t.Fatal("Chat() error = nil")
			}
			if calls != 1 {
				t.Fatalf("SDK retried internally: calls = %d", calls)
			}
			if status == http.StatusUnauthorized || status == http.StatusForbidden {
				if !IsAuthenticationError(err) || IsTransient(err) {
					t.Fatalf("error classification = auth:%t transient:%t", IsAuthenticationError(err), IsTransient(err))
				}
			} else if status == http.StatusRequestEntityTooLarge {
				if !IsContextOverflow(err) || IsTransient(err) {
					t.Fatalf("error classification = context:%t transient:%t", IsContextOverflow(err), IsTransient(err))
				}
			} else if !IsTransient(err) {
				t.Fatalf("IsTransient(%v) = false", err)
			}
		})
	}
}

func TestOpenRouterTypedSDKErrorClassification(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		auth      bool
		transient bool
		context   bool
	}{
		{name: "unauthorized", err: &sdkerrors.UnauthorizedResponseError{}, auth: true},
		{name: "rate limited", err: &sdkerrors.TooManyRequestsResponseError{}, transient: true},
		{name: "overloaded", err: &sdkerrors.ProviderOverloadedResponseError{}, transient: true},
		{name: "context limit", err: sdkerrors.NewAPIError("bad request", http.StatusBadRequest, `{"error":{"message":"maximum context length exceeded"}}`, nil), context: true},
		{name: "too large", err: &sdkerrors.PayloadTooLargeResponseError{}, context: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := normalizeOpenRouterError(test.err)
			if IsAuthenticationError(err) != test.auth || IsTransient(err) != test.transient || IsContextOverflow(err) != test.context {
				t.Fatalf("classification = auth:%t transient:%t context:%t", IsAuthenticationError(err), IsTransient(err), IsContextOverflow(err))
			}
		})
	}
}

func TestOpenAICompatibleProviderDoesNotRequestJSONOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request openAIChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.ResponseFormat != nil {
			t.Fatalf("response format = %#v", request.ResponseFormat)
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"{}"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	provider, err := NewOpenAIProvider(context.Background(), "test-key", "test-model", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Chat(context.Background(), &models.Prompt{}); err != nil {
		t.Fatal(err)
	}
}

func TestGroqProviderStreamsChatCompletions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" {
			t.Fatalf("request = %s %s, want POST /chat/completions", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("authorization = %q", got)
		}
		if got := r.Header.Get("Accept"); got != "text/event-stream" {
			t.Errorf("accept = %q", got)
		}
		var request openAIChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Model != defaultGroqModel || !request.Stream {
			t.Errorf("request = %#v", request)
		}
		if len(request.Messages) != 2 || request.Messages[0].Role != "system" || request.Messages[0].Content != "system" || request.Messages[1].Role != "user" || request.Messages[1].Content != "hi" {
			t.Errorf("messages = %#v", request.Messages)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\" world\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	provider, err := NewGroqProvider(context.Background(), "test-key", "", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	var chunks []string
	assembler := NewAssistantAssembler(nil, func(chunk string) { chunks = append(chunks, chunk) })
	err = provider.Stream(context.Background(), &models.Prompt{System: "system", Messages: []models.Message{{Role: models.RoleUser, Content: "hi"}}}, assembler.Feed)
	if err != nil {
		t.Fatal(err)
	}
	completion, err := assembler.Assemble()
	if err != nil {
		t.Fatal(err)
	}
	if completion.Text != "Hello world" || completion.FinishReason != "stop" || completion.Usage != (Usage{InputTokens: 3, OutputTokens: 2}) {
		t.Fatalf("completion = %#v", completion)
	}
	if got := strings.Join(chunks, ""); got != completion.Text {
		t.Fatalf("chunks = %q, completion = %q", got, completion.Text)
	}
}

func TestNVIDIAProviderSendsNemotronThinkingOptions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" {
			t.Fatalf("request = %s %s, want POST /chat/completions", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("authorization = %q", got)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("accept = %q", got)
		}
		var request openAIChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Model != defaultNVIDIAModel || !request.Stream || request.Temperature == nil || *request.Temperature != 1 || request.TopP == nil || *request.TopP != 0.95 || request.MaxTokens == nil || *request.MaxTokens != 16384 || request.ReasoningBudget == nil || *request.ReasoningBudget != 16384 || request.ChatTemplateKwargs == nil || !request.ChatTemplateKwargs.EnableThinking {
			t.Errorf("request = %#v", request)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	provider, err := NewNVIDIAProvider(context.Background(), "test-key", "", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	assembler := NewAssistantAssembler(nil, nil)
	err = provider.Stream(context.Background(), &models.Prompt{Messages: []models.Message{{Role: models.RoleUser, Content: "hi"}}}, assembler.Feed)
	if err != nil {
		t.Fatal(err)
	}
	completion, err := assembler.Assemble()
	if err != nil {
		t.Fatal(err)
	}
	if completion.Text != "done" || completion.FinishReason != "stop" {
		t.Fatalf("completion = %#v", completion)
	}
}

func TestMistralProviderUsesConversationsAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("authorization = %q", got)
		}
		switch r.URL.Path {
		case "/conversations":
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", r.Method)
			}
			var request mistralConversationRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if request.Model != defaultMistralModel || request.Instructions != "system" || len(request.Inputs) != 3 || request.CompletionArgs.Temperature != 0 || request.CompletionArgs.MaxTokens != mistralMaxTokens || request.CompletionArgs.ResponseFormat != nil || request.Store {
				t.Errorf("request = %#v", request)
			}
			fmt.Fprint(w, `{"conversation_id":"conversation","outputs":[{"content":"done"},{"content":[{"type":"text","text":" now"}]}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`)
		case "/models":
			fmt.Fprint(w, `[{"id":"mistral-medium-latest","max_context_length":32768}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider, err := NewMistralProvider(context.Background(), "test-key", "", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := provider.FetchMetadata(context.Background())
	if err != nil || len(metadata.Models) != 1 || metadata.Models[0] != (ModelInfo{ID: defaultMistralModel, Name: defaultMistralModel, ContextLength: 32768}) {
		t.Fatalf("metadata = %#v, %v", metadata, err)
	}
	completion, err := provider.Chat(context.Background(), &models.Prompt{System: "system", Messages: []models.Message{{Role: models.RoleUser, Content: "hi"}, {Role: models.RoleAssistant, Content: "hello"}}})
	if err != nil {
		t.Fatal(err)
	}
	if completion.Text != "done now" || completion.Usage != (Usage{InputTokens: 3, OutputTokens: 2}) {
		t.Fatalf("completion = %#v", completion)
	}
}

func TestMistralProviderDetectsOutputLimitWithoutJSONOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request mistralConversationRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.CompletionArgs.Temperature != 0 || request.CompletionArgs.MaxTokens != mistralMaxTokens || request.CompletionArgs.ResponseFormat != nil {
			t.Errorf("request = %#v", request)
		}
		fmt.Fprintf(w, `{"outputs":[{"type":"tool.execution","content":"ignored"},{"type":"message.output","content":"{\"description\":\"plan\"}"}],"usage":{"prompt_tokens":3,"completion_tokens":%d}}`, mistralMaxTokens)
	}))
	defer server.Close()

	provider, err := NewMistralProvider(context.Background(), "test-key", "", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	completion, err := provider.Chat(context.Background(), &models.Prompt{})
	if err != nil {
		t.Fatal(err)
	}
	if completion.Text != `{"description":"plan"}` || completion.FinishReason != string(FinishMaxTokens) || !completion.Truncated() || completion.Usage != (Usage{InputTokens: 3, OutputTokens: mistralMaxTokens}) {
		t.Fatalf("completion = %#v", completion)
	}
}

func TestMistralProviderKeepsNativeCallsStructuredAcrossHistory(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var request mistralConversationRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if requests == 1 {
			if len(request.Tools) != 1 || request.Tools[0].Function.Name != "read_file" {
				t.Fatalf("native tools = %#v", request.Tools)
			}
			fmt.Fprint(w, `{"outputs":[{"type":"function.call","tool_call_id":"mistral-call-1","name":"read_file","arguments":{"path":"README.md"}}]}`)
			return
		}
		encoded, _ := json.Marshal(request.Inputs)
		if !bytes.Contains(encoded, []byte(`"type":"function.call"`)) || !bytes.Contains(encoded, []byte(`"tool_call_id":"mistral-call-1"`)) || !bytes.Contains(encoded, []byte(`"type":"function.result"`)) || request.CompletionArgs.ResponseFormat != nil {
			t.Fatalf("native history = %s format=%#v", encoded, request.CompletionArgs.ResponseFormat)
		}
		fmt.Fprint(w, `{"outputs":[{"type":"message.output","content":"done"}]}`)
	}))
	defer server.Close()

	provider, err := NewMistralProvider(context.Background(), "test-key", "model", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	schema := json.RawMessage(`{"type":"object"}`)
	definition := models.ToolDefinition{Name: "read_file", InputSchema: schema}
	completion, err := provider.Chat(context.Background(), &models.Prompt{ActiveTools: []string{"read_file"}, ToolDefinitions: []models.ToolDefinition{definition}})
	if err != nil || len(completion.ToolCalls) != 1 || completion.ToolCalls[0].ID != "mistral-call-1" {
		t.Fatalf("native completion = %#v err=%v", completion, err)
	}
	completion, err = provider.Chat(context.Background(), &models.Prompt{ActiveTools: []string{"read_file"}, ToolDefinitions: []models.ToolDefinition{definition}, Messages: []models.Message{
		{Role: models.RoleAssistant, ToolCalls: completion.ToolCalls},
		{Role: models.RoleTool, ToolCallID: "mistral-call-1", ToolName: "read_file", Content: "contents"},
	}})
	if err != nil || completion.Text != "done" {
		t.Fatalf("final completion = %#v err=%v", completion, err)
	}
}

func TestAnthropicProviderUsesMessagesAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != "test-key" {
			t.Errorf("x-api-key = %q", got)
		}
		switch r.URL.Path {
		case "/models":
			fmt.Fprint(w, `{"data":[{"id":"claude-test","display_name":"Claude Test"}]}`)
		case "/messages":
			var request struct {
				Messages []struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"messages"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if len(request.Messages) != 3 || request.Messages[2].Role != "user" || request.Messages[2].Content != continuationInstruction {
				t.Errorf("messages = %#v", request.Messages)
			}
			fmt.Fprint(w, `{"content":[{"type":"text","text":"done"},{"type":"text","text":" now"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":2}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider, err := NewAnthropicProvider(context.Background(), "test-key", "claude-test", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := provider.FetchMetadata(context.Background())
	if err != nil || len(metadata.Models) != 1 || metadata.Models[0].ID != "claude-test" {
		t.Fatalf("unexpected metadata: %#v, %v", metadata, err)
	}
	completion, err := provider.Chat(context.Background(), &models.Prompt{System: "system", Messages: []models.Message{{Role: models.RoleUser, Content: "hi"}, {Role: models.RoleAssistant, Content: "planned"}}})
	if err != nil {
		t.Fatal(err)
	}
	if completion.Text != "done now" || completion.Usage.InputTokens != 3 || completion.Usage.OutputTokens != 2 {
		t.Fatalf("unexpected completion: %#v", completion)
	}
}

func TestAnthropicProviderKeepsNativeCallIDsAcrossHistory(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var request map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if requests == 1 {
			if !bytes.Contains(request["tools"], []byte(`"name":"read_file"`)) {
				t.Fatalf("tools = %s", request["tools"])
			}
			fmt.Fprint(w, `{"content":[{"type":"tool_use","id":"anthropic-call-1","name":"read_file","input":{"path":"README.md"}}],"stop_reason":"tool_use"}`)
			return
		}
		if !bytes.Contains(request["messages"], []byte(`"id":"anthropic-call-1"`)) || !bytes.Contains(request["messages"], []byte(`"tool_use_id":"anthropic-call-1"`)) {
			t.Fatalf("native history = %s", request["messages"])
		}
		fmt.Fprint(w, `{"content":[{"type":"text","text":"done"}],"stop_reason":"end_turn"}`)
	}))
	defer server.Close()

	provider, err := NewAnthropicProvider(context.Background(), "test-key", "model", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	definition := models.ToolDefinition{Name: "read_file", InputSchema: json.RawMessage(`{"type":"object"}`)}
	completion, err := provider.Chat(context.Background(), &models.Prompt{ActiveTools: []string{"read_file"}, ToolDefinitions: []models.ToolDefinition{definition}})
	if err != nil || len(completion.ToolCalls) != 1 || completion.ToolCalls[0].ID != "anthropic-call-1" {
		t.Fatalf("native completion = %#v err=%v", completion, err)
	}
	completion, err = provider.Chat(context.Background(), &models.Prompt{ActiveTools: []string{"read_file"}, ToolDefinitions: []models.ToolDefinition{definition}, Messages: []models.Message{
		{Role: models.RoleAssistant, ToolCalls: completion.ToolCalls},
		{Role: models.RoleTool, ToolCallID: "anthropic-call-1", ToolName: "read_file", Content: "contents"},
	}})
	if err != nil || completion.Text != "done" {
		t.Fatalf("final completion = %#v err=%v", completion, err)
	}
}

func TestAssistantContentAndToolCallsSurviveHistoryMapping(t *testing.T) {
	prompt := &models.Prompt{
		ActiveTools: []string{"read_file"},
		ToolDefinitions: []models.ToolDefinition{
			{Name: "read_file", InputSchema: json.RawMessage(`{"type":"object"}`)},
		},
		Messages: []models.Message{
			{
				Role:      models.RoleAssistant,
				Content:   `{"progress":"Observing page.tsx","next_goal":"Inspect legacy-app.js"}`,
				ToolCalls: []models.ToolCall{{ID: "call-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"legacy-app.js"}`)}},
			},
			{
				Role:       models.RoleTool,
				ToolCallID: "call-1",
				ToolName:   "read_file",
				Content:    `{"status":"completed"}`,
			},
		},
	}

	// 1. OpenAI
	openAIMsgs := openAIChatMessages(prompt)
	if len(openAIMsgs) != 2 {
		t.Fatalf("expected 2 OpenAI messages, got %d", len(openAIMsgs))
	}
	if openAIMsgs[0].Content == nil || openAIMsgs[0].Content != `{"progress":"Observing page.tsx","next_goal":"Inspect legacy-app.js"}` {
		t.Fatalf("OpenAI assistant message dropped content: %#v", openAIMsgs[0].Content)
	}
	if len(openAIMsgs[0].ToolCalls) != 1 {
		t.Fatalf("OpenAI assistant message missing tool call: %#v", openAIMsgs[0].ToolCalls)
	}

	// 2. OpenRouter
	openRouterMsgs := openRouterMessages(prompt)
	if len(openRouterMsgs) != 2 {
		t.Fatalf("expected 2 OpenRouter messages, got %d", len(openRouterMsgs))
	}
	if openRouterMsgs[0].ChatAssistantMessage == nil || openRouterMsgs[0].ChatAssistantMessage.Content == nil {
		t.Fatalf("OpenRouter assistant message missing content: %#v", openRouterMsgs[0])
	}
	if len(openRouterMsgs[0].ChatAssistantMessage.ToolCalls) != 1 {
		t.Fatalf("OpenRouter assistant message missing tool calls: %#v", openRouterMsgs[0])
	}

	// 3. Mistral
	mistralIn := mistralInputs(prompt)
	if len(mistralIn) < 3 {
		t.Fatalf("expected assistant content, function call, and result in Mistral, got %d items", len(mistralIn))
	}

	// 4. Gemini
	geminiContents, _ := geminiRequest(prompt, "gemini-2.5-pro")
	if len(geminiContents) != 2 || len(geminiContents[0].Parts) < 2 {
		t.Fatalf("expected Gemini Content to contain text part and function call part, got %#v", geminiContents)
	}
}
