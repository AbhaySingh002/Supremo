package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
)

type providerEnvelope struct {
	System   string                  `json:"system"`
	Messages []models.Message        `json:"messages"`
	Tools    []models.ToolDefinition `json:"tools"`
}

func freezeProviderRequest(prompt *models.Prompt) error {
	if prompt == nil {
		return fmt.Errorf("provider prompt is required")
	}
	messages := make([]models.Message, len(prompt.Messages))
	for i, message := range prompt.Messages {
		message.TaskID = ""
		message.TurnProgress = nil
		message.ToolCalls = append([]models.ToolCall(nil), message.ToolCalls...)
		for j := range message.ToolCalls {
			message.ToolCalls[j].Arguments = append(json.RawMessage(nil), message.ToolCalls[j].Arguments...)
			message.ToolCalls[j].ProviderMetadata = append(json.RawMessage(nil), message.ToolCalls[j].ProviderMetadata...)
			message.ToolCalls[j].Synthetic = false
		}
		messages[i] = message
	}
	definitions := append([]models.ToolDefinition(nil), prompt.ToolDefinitions...)
	for i := range definitions {
		definitions[i].InputSchema = append(json.RawMessage(nil), definitions[i].InputSchema...)
		if len(definitions[i].InputSchema) > 0 {
			var compact bytes.Buffer
			if err := json.Compact(&compact, definitions[i].InputSchema); err != nil {
				return fmt.Errorf("compact tool schema %s: %w", definitions[i].Name, err)
			}
			definitions[i].InputSchema = append(json.RawMessage(nil), compact.Bytes()...)
		}
	}
	sort.SliceStable(definitions, func(i, j int) bool { return definitions[i].Name < definitions[j].Name })
	sort.Strings(prompt.ActiveTools)

	envelope := providerEnvelope{System: prompt.System, Messages: messages, Tools: definitions}
	data, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("encode provider envelope: %w", err)
	}
	header, err := json.Marshal(struct {
		System string                  `json:"system"`
		Tools  []models.ToolDefinition `json:"tools"`
	}{System: envelope.System, Tools: envelope.Tools})
	if err != nil {
		return fmt.Errorf("encode provider header: %w", err)
	}
	toolSchemas, err := json.Marshal(envelope.Tools)
	if err != nil {
		return fmt.Errorf("encode tool schemas: %w", err)
	}
	prompt.Messages = messages
	prompt.ToolDefinitions = definitions
	prompt.FrozenEnvelope = append([]byte(nil), data...)
	prompt.RequestDigest = sha256Digest(data)
	prompt.HeaderDigest = sha256Digest(header)
	prompt.SystemDigest = sha256Digest([]byte(envelope.System))
	prompt.ToolSchemaDigest = sha256Digest(toolSchemas)
	return nil
}

func sha256Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
