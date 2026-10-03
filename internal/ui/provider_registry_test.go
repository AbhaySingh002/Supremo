package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/api"
	"github.com/AbhaySingh002/supremo/internal/ui/selectors"
)

func TestProviderSelectorUsesRegisteredChoices(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := newTestModel(api.Session{ID: "provider-registry"}, ctx, cancel)
	model.providers = []api.Provider{{ID: "custom", Name: "Custom Provider", Configured: true}, {ID: "endpoint-only", Name: "Endpoint Only"}}
	model.openProviderSelector()
	selected, ok := model.providerSelector.Selected()
	if !ok || selected.ID != "custom" || selected.Name != "Custom Provider" {
		t.Fatalf("selector = %#v, found=%t", selected, ok)
	}
	if view := model.providerSelector.View().Content; view == "" || !strings.Contains(view, "Endpoint Only") {
		t.Fatalf("provider selector view = %q", view)
	}
}

func TestProviderSelectorAddsCustomOpenAICompatibleChoice(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := newTestModel(api.Session{ID: "custom-provider"}, ctx, cancel)
	model.providers = []api.Provider{{ID: "openai", Name: "OpenAI", Configured: true}}
	model.openProviderSelector()
	if view := model.providerSelector.View().Content; !strings.Contains(view, "Custom OpenAI-compatible") {
		t.Fatalf("provider selector is missing custom choice: %q", view)
	}
	updated, cmd := model.Update(selectors.ProviderSelectedMsg{ID: customProviderID})
	model = updated.(Model)
	if cmd == nil || model.surface != surfaceCredential || model.credential == nil || !model.credential.custom || model.credential.step != credentialName {
		t.Fatalf("custom credential flow = %#v", model.credential)
	}
}

func TestCustomCredentialSetupBuildsNamedOpenAICompatibleRoute(t *testing.T) {
	setup := newCustomCredentialSetup(newTestModel(api.Session{}, context.Background(), func() {}).styles)
	setup.name.SetValue("Ollama")
	setup.endpoint.SetValue("http://localhost:11434/v1")
	setup.model.SetValue("llama3.2")
	setup.step = credentialModel
	cmd := setup.submit()
	if cmd == nil {
		t.Fatal("custom credential setup did not submit")
	}
	msg, ok := cmd().(credentialSubmittedMsg)
	if !ok {
		t.Fatalf("credential message = %T", cmd())
	}
	if msg.provider != "openai-compatible:ollama" || msg.endpoint != "http://localhost:11434/v1" || msg.model != "llama3.2" || msg.openModels {
		t.Fatalf("custom credential message = %#v", msg)
	}
}

func TestCustomCredentialEditPreFillsValues(t *testing.T) {
	styles := newTestModel(api.Session{}, context.Background(), func() {}).styles
	provider := api.Provider{
		ID:       "openai-compatible:omniroute",
		Name:     "omniroute (Custom)",
		Endpoint: "https://api.omniroute.io/v1",
		Models:   []api.Model{{ID: "claude-3-7-sonnet"}},
		Custom:   true,
	}

	setup := newCustomCredentialEditSetup(provider, styles)
	if setup.oldProvider != "openai-compatible:omniroute" {
		t.Fatalf("expected oldProvider openai-compatible:omniroute, got %q", setup.oldProvider)
	}
	if setup.name.Value() != "omniroute" {
		t.Fatalf("expected name omniroute, got %q", setup.name.Value())
	}
	if setup.endpoint.Value() != "https://api.omniroute.io/v1" {
		t.Fatalf("expected endpoint https://api.omniroute.io/v1, got %q", setup.endpoint.Value())
	}
	if setup.model.Value() != "claude-3-7-sonnet" {
		t.Fatalf("expected model claude-3-7-sonnet, got %q", setup.model.Value())
	}

	// Submit with empty key (should keep existing key)
	setup.step = credentialModel
	cmd := setup.submit()
	if cmd == nil {
		t.Fatal("expected submit command")
	}
	msg, ok := cmd().(credentialSubmittedMsg)
	if !ok {
		t.Fatalf("unexpected msg: %T", cmd())
	}
	if msg.oldProvider != "openai-compatible:omniroute" || msg.provider != "openai-compatible:omniroute" {
		t.Fatalf("expected provider match, got: %#v", msg)
	}
}

func TestProviderDeleteCommand(t *testing.T) {
	client := &behaviorClient{}
	ctx := context.Background()
	model := New(client, ".", "chat", Options{})

	cmd := executeCommandCmd(ctx, client, model.registry, api.Session{ID: "session-1"}, "/provider delete omniroute", 1)
	if cmd == nil {
		t.Fatal("expected command from executeCommandCmd")
	}
	result := cmd().(commandResultMsg)
	if result.err != nil {
		t.Fatalf("execute error: %v", result.err)
	}
	if client.deleted.Provider != "openai-compatible:omniroute" {
		t.Fatalf("expected deleted provider openai-compatible:omniroute, got %q", client.deleted.Provider)
	}
	if !strings.Contains(result.output, "Deleted custom provider omniroute") {
		t.Fatalf("unexpected output: %q", result.output)
	}
}

func TestCustomProviderRetainedInOptionsWhenNotActive(t *testing.T) {
	model := newTestModel(api.Session{}, context.Background(), func() {})
	model.provider = "gemini" // Active provider is gemini!
	model.providers = []api.Provider{
		{ID: "gemini", Name: "Google Gemini", Configured: true},
		{ID: "openai-compatible:omniroute", Name: "omniroute (Custom)", Configured: true, Endpoint: "http://localhost:8000/v1", Custom: true},
	}

	options := model.providerOptions()
	found := false
	for _, opt := range options {
		if opt.ID == "openai-compatible:omniroute" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("custom provider omniroute was not retained in provider options when active provider was gemini! Options: %#v", options)
	}
}
