package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/api"
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
