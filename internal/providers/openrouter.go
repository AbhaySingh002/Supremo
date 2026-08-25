package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	openrouter "github.com/OpenRouterTeam/go-sdk"
	"github.com/OpenRouterTeam/go-sdk/models/components"
	"github.com/OpenRouterTeam/go-sdk/models/sdkerrors"
	"github.com/OpenRouterTeam/go-sdk/optionalnullable"
	"github.com/OpenRouterTeam/go-sdk/retry"
)

const openRouterEndpoint = "https://openrouter.ai/api/v1"

// OpenRouterProvider uses the official OpenRouter SDK without streaming.
type OpenRouterProvider struct {
	client *openrouter.OpenRouter
	model  string
}

func NewOpenRouterProvider(_ context.Context, apiKey, model, endpoint string) (*OpenRouterProvider, error) {
	if endpoint == "" {
		endpoint = openRouterEndpoint
	}
	client := openrouter.New(
		openrouter.WithSecurity(apiKey),
		openrouter.WithServerURL(endpoint),
		openrouter.WithClient(&http.Client{Timeout: 60 * time.Second}),
		openrouter.WithRetryConfig(retry.Config{Strategy: "none"}),
	)
	return &OpenRouterProvider{client: client, model: model}, nil
}

func (p *OpenRouterProvider) chatRequest(prompt *models.Prompt) components.ChatRequest {
	stream := false
	request := components.ChatRequest{
		Model:    openrouter.Pointer(p.model),
		Messages: openRouterMessages(prompt),
		Stream:   &stream,
		Tools:    openRouterTools(prompt.ToolDefinitions),
	}
	if len(prompt.ActiveTools) > 0 {
		effort := components.ChatRequestReasoningEffortLow
		request.ReasoningEffort = optionalnullable.From(&effort)
	}
	return request
}

func openRouterTools(definitions []models.ToolDefinition) []components.ChatFunctionTool {
	result := make([]components.ChatFunctionTool, 0, len(definitions))
	for _, definition := range definitions {
		var parameters map[string]any
		if json.Unmarshal(definition.InputSchema, &parameters) != nil || parameters == nil {
			continue
		}
		description := definition.Description
		result = append(result, components.CreateChatFunctionToolChatFunctionToolFunction(components.ChatFunctionToolFunction{
			Type: components.ChatFunctionToolTypeFunction,
			Function: components.ChatFunctionToolFunctionFunction{
				Name:        definition.Name,
				Description: &description,
				Parameters:  parameters,
			},
		}))
	}
	return result
}

func openRouterMessages(prompt *models.Prompt) []components.ChatMessages {
	history := providerMessages(prompt)
	messages := make([]components.ChatMessages, 0, len(history)+1)
	if prompt.System != "" {
		messages = append(messages, components.CreateChatMessagesSystem(components.ChatSystemMessage{
			Content: components.CreateChatSystemMessageContentStr(prompt.System),
		}))
	}
	for _, message := range history {
		if message.Role == models.RoleAssistant {
			if calls := openRouterHistoryToolCalls(message.ToolCalls); len(calls) > 0 {
				var content *components.ChatAssistantMessageContent
				if message.Content != "" {
					c := components.CreateChatAssistantMessageContentStr(message.Content)
					content = &c
				}
				messages = append(messages, components.CreateChatMessagesAssistant(components.ChatAssistantMessage{
					Content:   optionalnullable.From(content),
					ToolCalls: calls,
				}))
				continue
			}
			content := components.CreateChatAssistantMessageContentStr(message.Content)
			messages = append(messages, components.CreateChatMessagesAssistant(components.ChatAssistantMessage{
				Content: optionalnullable.From(&content),
			}))
			continue
		}
		if message.Role == models.RoleTool && message.ToolCallID != "" {
			messages = append(messages, components.CreateChatMessagesTool(components.ChatToolMessage{
				Content:    components.CreateChatToolMessageContentStr(message.Content),
				ToolCallID: message.ToolCallID,
			}))
			continue
		}
		messages = append(messages, components.CreateChatMessagesUser(components.ChatUserMessage{
			Content: components.CreateChatUserMessageContentStr(message.Content),
		}))
	}
	return messages
}

func openRouterHistoryToolCalls(toolCalls []models.ToolCall) []components.ChatToolCall {
	calls := make([]components.ChatToolCall, 0, len(toolCalls))
	for _, call := range toolCalls {
		arguments, err := json.Marshal(call.Arguments)
		if err != nil || strings.TrimSpace(call.ID) == "" || strings.TrimSpace(call.Name) == "" || !json.Valid(arguments) {
			return nil
		}
		calls = append(calls, components.ChatToolCall{
			ID:   call.ID,
			Type: components.ChatToolCallTypeFunction,
			Function: components.ChatToolCallFunction{
				Name:      call.Name,
				Arguments: string(arguments),
			},
		})
	}
	return calls
}

func (p *OpenRouterProvider) Chat(ctx context.Context, prompt *models.Prompt) (*Completion, error) {
	request := p.chatRequest(prompt)
	response, err := p.client.Chat.Send(ctx, request, nil)
	if err != nil && request.ResponseFormat != nil && isUnsupportedStructuredOutput(normalizeOpenRouterError(err)) {
		format := components.CreateResponseFormatJSONObject(components.ChatFormatJSONObjectConfig{})
		request.ResponseFormat = &format
		response, err = p.client.Chat.Send(ctx, request, nil)
	}
	if err != nil {
		return nil, fmt.Errorf("openrouter execution: %w", normalizeOpenRouterError(err))
	}
	if response == nil || response.ChatResult == nil {
		return nil, &ProviderFailure{Code: FailureEmptyResponse, Message: "openrouter returned no chat result"}
	}
	if len(response.ChatResult.Choices) == 0 {
		return nil, &ProviderFailure{Code: FailureEmptyResponse, Message: "openrouter returned no completion choices"}
	}

	choice := response.ChatResult.Choices[0]
	finish := NormalizeFinishReason(openRouterFinishReason(choice.FinishReason))
	completion := &Completion{FinishReason: string(finish), Usage: openRouterUsage(response.ChatResult.Usage)}
	if completion.Truncated() {
		return completion, nil
	}
	if finish == FinishError {
		return nil, fmt.Errorf("openrouter execution: %w", openRouterStatusError(http.StatusBadGateway, "upstream model failed to generate a response"))
	}
	if len(choice.Message.ToolCalls) > 0 {
		calls, err := openRouterToolCalls(choice.Message.ToolCalls, prompt.ActiveTools)
		if err != nil {
			return nil, err
		}
		completion.ToolCalls = calls
		completion.Text = openRouterContent(choice.Message)
		if completion.FinishReason == "" || completion.FinishReason == string(FinishStop) {
			completion.FinishReason = string(FinishToolCalls)
		}
		return completion, nil
	}
	completion.Text = openRouterContent(choice.Message)
	if strings.TrimSpace(completion.Text) != "" {
		return completion, nil
	}
	if refusal, ok := choice.Message.Refusal.GetOrZero(); ok && strings.TrimSpace(refusal) != "" {
		return nil, &ProviderFailure{Code: FailureInvalidRequest, Message: fmt.Sprintf("openrouter refused the response: %s", refusal)}
	}
	if reasoning, ok := choice.Message.Reasoning.GetOrZero(); ok && strings.TrimSpace(reasoning) != "" || len(choice.Message.ReasoningDetails) > 0 {
		return nil, &ProviderFailure{Code: FailureEmptyResponse, Message: fmt.Sprintf("openrouter returned reasoning without final content (finish_reason=%s)", completion.FinishReason)}
	}
	return nil, &ProviderFailure{Code: FailureEmptyResponse, Message: fmt.Sprintf("openrouter returned empty final content (finish_reason=%s)", completion.FinishReason)}
}

func openRouterToolCalls(calls []components.ChatToolCall, activeTools []string) ([]models.ToolCall, error) {
	result := make([]models.ToolCall, 0, len(calls))
	for _, call := range calls {
		rawArgs := call.Function.Arguments
		if strings.TrimSpace(rawArgs) == "" {
			rawArgs = "{}"
		}
		arguments := json.RawMessage(rawArgs)
		id, synthetic := normalizeToolCallID(call.ID)
		result = append(result, models.ToolCall{
			ID:        id,
			Name:      canonicalToolName(call.Function.Name, activeTools),
			Arguments: arguments,
			Synthetic: synthetic,
		})
	}
	return result, nil
}

func openRouterContent(message components.ChatAssistantMessage) string {
	content, ok := message.Content.GetOrZero()
	if !ok {
		return ""
	}
	if content.Str != nil {
		return *content.Str
	}
	var text strings.Builder
	for _, item := range content.ArrayOfChatContentItems {
		if item.ChatContentText != nil {
			text.WriteString(item.ChatContentText.Text)
		}
	}
	return text.String()
}

func openRouterFinishReason(reason *components.ChatFinishReasonEnum) string {
	if reason == nil {
		return ""
	}
	return string(*reason)
}

func openRouterUsage(usage *components.ChatUsage) Usage {
	if usage == nil {
		return Usage{}
	}
	result := Usage{InputTokens: int(usage.PromptTokens), OutputTokens: int(usage.CompletionTokens)}
	if cost, ok := usage.Cost.GetOrZero(); ok {
		result.CostUSD = &cost
	}
	return result
}

func (p *OpenRouterProvider) FetchMetadata(ctx context.Context) (Metadata, error) {
	response, err := p.client.Models.List(ctx, nil)
	if err != nil {
		return Metadata{}, fmt.Errorf("list models: %w", normalizeOpenRouterError(err))
	}
	items := response.GetResult().Data
	metadata := Metadata{Models: make([]ModelInfo, 0, len(items)), FetchedAt: time.Now().UTC()}
	for _, item := range items {
		name := item.Name
		if name == "" {
			name = item.ID
		}
		contextLength := 0
		if item.ContextLength != nil {
			contextLength = int(*item.ContextLength)
		}
		info := ModelInfo{ID: item.ID, Name: name, ContextLength: contextLength}
		info.ModalitiesKnown = len(item.Architecture.InputModalities) > 0 || len(item.Architecture.OutputModalities) > 0
		for _, modality := range item.Architecture.InputModalities {
			info.AcceptsText = info.AcceptsText || string(modality) == "text"
		}
		for _, modality := range item.Architecture.OutputModalities {
			info.ProducesText = info.ProducesText || string(modality) == "text"
		}
		metadata.Models = append(metadata.Models, info)
	}
	if credits, err := p.client.Credits.GetCredits(ctx); err == nil && credits != nil {
		metadata.Account = &AccountInfo{TotalCredits: credits.Data.TotalCredits, TotalUsage: credits.Data.TotalUsage}
	}
	return metadata, nil
}

func normalizeOpenRouterError(err error) error {
	var apiError *sdkerrors.APIError
	if errors.As(err, &apiError) {
		return openRouterStatusError(apiError.StatusCode, apiError.Body)
	}
	status := 0
	switch {
	case errorAs[*sdkerrors.BadRequestResponseError](err):
		status = http.StatusBadRequest
	case errorAs[*sdkerrors.UnauthorizedResponseError](err):
		status = http.StatusUnauthorized
	case errorAs[*sdkerrors.PaymentRequiredResponseError](err):
		status = http.StatusPaymentRequired
	case errorAs[*sdkerrors.ForbiddenResponseError](err):
		status = http.StatusForbidden
	case errorAs[*sdkerrors.NotFoundResponseError](err):
		status = http.StatusNotFound
	case errorAs[*sdkerrors.RequestTimeoutResponseError](err):
		status = http.StatusRequestTimeout
	case errorAs[*sdkerrors.PayloadTooLargeResponseError](err):
		status = http.StatusRequestEntityTooLarge
	case errorAs[*sdkerrors.UnprocessableEntityResponseError](err):
		status = http.StatusUnprocessableEntity
	case errorAs[*sdkerrors.TooManyRequestsResponseError](err):
		status = http.StatusTooManyRequests
	case errorAs[*sdkerrors.InternalServerResponseError](err):
		status = http.StatusInternalServerError
	case errorAs[*sdkerrors.BadGatewayResponseError](err):
		status = http.StatusBadGateway
	case errorAs[*sdkerrors.ServiceUnavailableResponseError](err):
		status = http.StatusServiceUnavailable
	case errorAs[*sdkerrors.EdgeNetworkTimeoutResponseError](err):
		status = 524
	case errorAs[*sdkerrors.ProviderOverloadedResponseError](err):
		status = 529
	}
	if status == 0 {
		return err
	}
	return openRouterStatusError(status, err.Error())
}

func errorAs[T error](err error) bool {
	var target T
	return errors.As(err, &target)
}

func openRouterStatusError(code int, body string) error {
	return (&httpStatusError{code: code, status: fmt.Sprintf("%d %s", code, http.StatusText(code)), body: strings.TrimSpace(body)}).AsProviderFailure()
}
