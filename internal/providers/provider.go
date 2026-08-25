package providers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"google.golang.org/genai"
)

// FinishReason represents canonical completion termination causes.
type FinishReason string

const (
	FinishStop      FinishReason = "stop"
	FinishToolCalls FinishReason = "tool_calls"
	FinishMaxTokens FinishReason = "max_tokens"
	FinishError     FinishReason = "error"
	FinishAborted   FinishReason = "aborted"
)

// NormalizeFinishReason normalizes vendor-specific finish reasons into canonical FinishReason.
func NormalizeFinishReason(reason string) FinishReason {
	r := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(reason), "-", "_"))
	switch {
	case r == "" || r == "stop" || r == "end_turn" || r == "stop_sequence":
		return FinishStop
	case r == "tool_calls" || r == "tool_use" || r == "function_call":
		return FinishToolCalls
	case strings.Contains(r, "length") || strings.Contains(r, "max_token") || strings.Contains(r, "token_limit"):
		return FinishMaxTokens
	case r == "error":
		return FinishError
	case r == "aborted" || r == "cancelled" || r == "canceled":
		return FinishAborted
	default:
		return FinishReason(r)
	}
}

// FailureCode is the closed taxonomy of canonical provider failure categories.
type FailureCode string

const (
	FailureAuth                  FailureCode = "AUTH"
	FailureInvalidCredential     FailureCode = "INVALID_CREDENTIAL"
	FailureRateLimit             FailureCode = "RATE_LIMIT"
	FailureQuota                 FailureCode = "QUOTA"
	FailureContextWindowExceeded FailureCode = "CONTEXT_WINDOW_EXCEEDED"
	FailureEmptyResponse         FailureCode = "EMPTY_RESPONSE"
	FailureServer                FailureCode = "SERVER"
	FailureTimeout               FailureCode = "TIMEOUT"
	FailureTransport             FailureCode = "TRANSPORT"
	FailureAborted               FailureCode = "ABORTED"
	FailureInvalidRequest        FailureCode = "INVALID_REQUEST"
	FailureUnsupportedContent    FailureCode = "UNSUPPORTED_CONTENT"
)

// ProviderFailure represents a normalized provider error.
type ProviderFailure struct {
	Code       FailureCode   `json:"code"`
	Message    string        `json:"message"`
	Status     int           `json:"status,omitempty"`
	RetryAfter time.Duration `json:"retry_after,omitempty"`
	RequestID  string        `json:"request_id,omitempty"`
	Err        error         `json:"-"`
}

func (e *ProviderFailure) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		if e.Code != "" {
			return fmt.Sprintf("%s: %s", e.Code, e.Message)
		}
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return string(e.Code)
}

func (e *ProviderFailure) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *ProviderFailure) ErrorClass() string {
	if e == nil {
		return "PROVIDER_ERROR"
	}
	if e.Code == FailureEmptyResponse {
		return "PROTOCOL_ERROR"
	}
	return string(e.Code)
}

func normalizeToolCallID(id string) (string, bool) {
	if strings.TrimSpace(id) != "" {
		return id, false
	}
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "supremo-tool-call", true
	}
	return "supremo-" + hex.EncodeToString(value[:]), true
}

func canonicalToolName(name string, activeTools []string) string {
	for _, active := range activeTools {
		if name == active {
			return name
		}
	}
	suffix := name
	if index := strings.LastIndexByte(name, '.'); index >= 0 {
		suffix = name[index+1:]
	}
	for _, active := range activeTools {
		if suffix == active {
			return active
		}
	}
	return name
}

// Truncated reports finish reasons that indicate the provider stopped before
// the requested response could be complete.
func (c *Completion) Truncated() bool {
	if c == nil {
		return false
	}
	if c.FinishReason == string(FinishMaxTokens) {
		return true
	}
	reason := strings.ToLower(strings.ReplaceAll(c.FinishReason, "-", "_"))
	return strings.Contains(reason, "length") || strings.Contains(reason, "max_token") || strings.Contains(reason, "token_limit")
}

// Completion represents a structured response from an LLM provider.
type Completion struct {
	Text         string            `json:"text"`
	ToolCalls    []models.ToolCall `json:"tool_calls,omitempty"`
	FinishReason string            `json:"finish_reason"`
	Usage        Usage             `json:"usage"`
	// SourceEventSeqs is agent-owned diagnostic provenance. Provider adapters
	// do not read or write it, and it is never sent back to a model.
	SourceEventSeqs []int64 `json:"-"`
}

// Usage is the provider-reported cost and token usage for one completion.
type Usage struct {
	InputTokens  int      `json:"input_tokens"`
	OutputTokens int      `json:"output_tokens"`
	CostUSD      *float64 `json:"cost_usd,omitempty"`
}

// MalformedOutputError marks a completed provider response that contained no
// executable or user-visible answer.
type MalformedOutputError struct{ Reason string }

func (e *MalformedOutputError) Error() string      { return e.Reason }
func (e *MalformedOutputError) ErrorClass() string { return "PROTOCOL_ERROR" }

func IsMalformedOutput(err error) bool {
	if err == nil {
		return false
	}
	var malformed *MalformedOutputError
	if errors.As(err, &malformed) {
		return true
	}
	var failure *ProviderFailure
	if errors.As(err, &failure) && failure.Code == FailureEmptyResponse {
		return true
	}
	return false
}

// ModelInfo is the provider metadata needed to choose and size a model at runtime.
type ModelInfo struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	ContextLength   int    `json:"context_length,omitempty"`
	ModalitiesKnown bool   `json:"modalities_known,omitempty"`
	AcceptsText     bool   `json:"accepts_text,omitempty"`
	ProducesText    bool   `json:"produces_text,omitempty"`
}

// AccountInfo is intentionally optional: not every provider exposes a balance to a normal API key.
type AccountInfo struct {
	TotalCredits float64 `json:"total_credits"`
	TotalUsage   float64 `json:"total_usage"`
}

// Metadata is cached locally so normal startup never needs a provider request.
type Metadata struct {
	Models    []ModelInfo  `json:"models"`
	Account   *AccountInfo `json:"account,omitempty"`
	FetchedAt time.Time    `json:"fetched_at"`
}

// Provider defines the interface that all LLM adapters must implement.
type Provider interface {
	Chat(ctx context.Context, prompt *models.Prompt) (*Completion, error)
}

// StreamProvider is implemented by providers that can normalize their wire
// stream into ordered provider-neutral events. It never assembles a completion.
type StreamProvider interface {
	Stream(ctx context.Context, prompt *models.Prompt, receive func(StreamEvent) error) error
}

const continuationInstruction = "Continue with the task described by the system instructions."

// providerMessages returns a request-only copy of prompt history for adapters.
// Chat APIs treat a trailing assistant message as a prefill, while Supremo can
// end DeriveMessages on a completed assistant turn (for example a plan phase
// with no new user text). Append a synthetic user turn on the wire only. This
// must not mutate prompt.Messages or Session history, and it does not run when
// the last role is tool.
func providerMessages(prompt *models.Prompt) []models.Message {
	messages := append([]models.Message(nil), prompt.Messages...)
	if len(messages) > 0 && messages[len(messages)-1].Role == models.RoleAssistant {
		messages = append(messages, models.Message{Role: models.RoleUser, Content: continuationInstruction})
	}
	return messages
}

// MetadataProvider is implemented when a provider can list its available models.
type MetadataProvider interface {
	FetchMetadata(ctx context.Context) (Metadata, error)
}

// GeminiAPIError exposes the Google SDK's structured error after provider
// wrappers add request context. Callers must not infer an authentication
// failure from a generic INVALID_ARGUMENT response.
func GeminiAPIError(err error) (code int, status, message string, ok bool) {
	var apiErr genai.APIError
	if !errors.As(err, &apiErr) {
		return 0, "", "", false
	}
	return apiErr.Code, apiErr.Status, apiErr.Message, true
}

// IsAuthenticationError recognizes a real credential rejection. Gemini may
// use HTTP 400 for an invalid key, so its server message is required before
// treating that status as authentication rather than a malformed request.
func IsAuthenticationError(err error) bool {
	if err == nil {
		return false
	}
	var failure *ProviderFailure
	if errors.As(err, &failure) && (failure.Code == FailureAuth || failure.Code == FailureInvalidCredential) {
		return true
	}
	if code, _, message, ok := GeminiAPIError(err); ok {
		if code == 401 || code == 403 {
			return true
		}
		message = strings.ToLower(message)
		return code == 400 && strings.Contains(message, "api key") &&
			(strings.Contains(message, "not valid") || strings.Contains(message, "invalid"))
	}
	var status *httpStatusError
	if errors.As(err, &status) {
		return status.code == 401 || status.code == 403
	}
	return false
}

// IsTransient reports provider failures that are safe to retry before any
// response has been accepted.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var failure *ProviderFailure
	if errors.As(err, &failure) {
		return failure.Code == FailureRateLimit || failure.Code == FailureServer || failure.Code == FailureTimeout || failure.Code == FailureTransport
	}
	var status *httpStatusError
	if errors.As(err, &status) {
		return status.code == 429 || status.code >= 500 && status.code <= 599
	}
	var gemini genai.APIError
	if errors.As(err, &gemini) {
		return gemini.Code == 429 || gemini.Code >= 500 && gemini.Code <= 599
	}
	var network net.Error
	return errors.As(err, &network) && network.Timeout()
}
