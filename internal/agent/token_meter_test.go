package agent

import (
	"context"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/state"
)

func TestTokenMeterEstimation(t *testing.T) {
	if tok := EstimateTokens(""); tok != 0 {
		t.Errorf("expected 0 for empty string, got %d", tok)
	}
	if tok := EstimateTokens("hello world"); tok != 3 { // 11 runes -> (11+3)/4 = 3
		t.Errorf("expected 3 tokens for 'hello world', got %d", tok)
	}

	msg := models.Message{
		Role:    models.RoleUser,
		Content: "Hello from test user",
	}
	tokMsg := EstimateMessageTokens(msg)
	if tokMsg <= 0 {
		t.Errorf("expected positive token count for message, got %d", tokMsg)
	}
}

func TestTokenMeterMeasure(t *testing.T) {
	root := t.TempDir()
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = state.CloseWorkspace(root) }()

	session := &Session{ID: "meter-test", Provider: "mock", Model: "m1"}
	_ = session.AttachSurface(context.Background(), store)

	// Append user message
	uEvent := SessionEvent{
		Seq:     0,
		Type:    EventUserMessage,
		Message: models.Message{Role: models.RoleUser, Content: "Task instructions"},
	}
	_ = session.applyEvent(uEvent)

	meter := NewDefaultTokenMeter()
	prompt := &models.Prompt{
		System:   "System instructions here",
		Messages: session.DeriveMessages(),
	}

	measurement := meter.Measure(session, prompt, 1000)
	if measurement.ContextLimit != 1000 {
		t.Errorf("expected ContextLimit 1000, got %d", measurement.ContextLimit)
	}
	if measurement.ThresholdTokens != 800 {
		t.Errorf("expected ThresholdTokens 800 (80%%), got %d", measurement.ThresholdTokens)
	}
	if measurement.RetainTokens != 160 {
		t.Errorf("expected RetainTokens 160 (16%%), got %d", measurement.RetainTokens)
	}
	if len(measurement.Nodes) != 1 {
		t.Errorf("expected 1 node, got %d", len(measurement.Nodes))
	}
	if measurement.HeaderTokens <= 0 || measurement.SurfaceTokens <= 0 {
		t.Errorf("expected positive HeaderTokens and SurfaceTokens, got %#v", measurement)
	}
	if measurement.TotalTokens != measurement.HeaderTokens+measurement.SurfaceTokens {
		t.Errorf("expected TotalTokens = HeaderTokens + SurfaceTokens, got %d vs %d", measurement.TotalTokens, measurement.HeaderTokens+measurement.SurfaceTokens)
	}
}

func TestTokenMeterCountsFrozenEnvelopeWithoutMetadataDoubleCount(t *testing.T) {
	prompt := &models.Prompt{
		System:   "12345678",
		Messages: []models.Message{{Role: models.RoleUser, Content: "abcdefgh"}},
		Metadata: models.PromptMetadata{Sections: []models.PromptSection{{Name: "already-rendered", Tokens: 100_000}}},
	}
	measurement := NewDefaultTokenMeter().Measure(nil, prompt, 1_000_000)
	want := EstimateTokens(prompt.System) + EstimateMessageTokens(prompt.Messages[0])
	if measurement.TotalTokens != want {
		t.Fatalf("exact envelope tokens = %d, want %d", measurement.TotalTokens, want)
	}
}
