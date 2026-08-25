package agent

import (
	"encoding/json"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
)

func TestFrozenProviderEnvelopeIgnoresTraceMetadataAndCompactsSchemas(t *testing.T) {
	makePrompt := func(requestID string) *models.Prompt {
		return &models.Prompt{
			System:   "system",
			Messages: []models.Message{{Role: models.RoleUser, Content: "hello", TaskID: "local-task", TurnProgress: &models.TurnProgress{PhaseState: "local"}}},
			ToolDefinitions: []models.ToolDefinition{
				{Name: "zeta", Description: "z", InputSchema: json.RawMessage("{ \n \t\"type\" : \"object\" }")},
				{Name: "alpha", Description: "a", InputSchema: json.RawMessage("{\"type\": \"object\"}")},
			},
			ManifestID: requestID,
			Request:    &models.AgentRequest{RequestID: requestID, TurnID: requestID},
		}
	}
	first, second := makePrompt("trace-a"), makePrompt("trace-b")
	if err := freezeProviderRequest(first); err != nil {
		t.Fatal(err)
	}
	if err := freezeProviderRequest(second); err != nil {
		t.Fatal(err)
	}
	if first.RequestDigest != second.RequestDigest || string(first.FrozenEnvelope) != string(second.FrozenEnvelope) {
		t.Fatalf("trace metadata changed envelope digest: %s != %s", first.RequestDigest, second.RequestDigest)
	}
	if first.ToolDefinitions[0].Name != "alpha" || string(first.ToolDefinitions[0].InputSchema) != `{"type":"object"}` || first.Messages[0].TaskID != "" || first.Messages[0].TurnProgress != nil {
		t.Fatalf("envelope was not normalized: prompt=%#v", first)
	}
}
