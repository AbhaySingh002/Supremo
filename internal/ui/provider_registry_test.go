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
