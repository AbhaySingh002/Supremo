package commands

import (
	"reflect"
	"testing"
)

func TestRegistryPreservesCommandSurface(t *testing.T) {
	want := []string{
		"/activity", "/approve", "/auth", "/batman", "/cancel", "/clear", "/config", "/context", "/copy", "/delete-session", "/deny", "/diff", "/doctor", "/dry-run", "/endpoint", "/exit", "/export", "/help", "/index", "/init", "/krypton", "/mode", "/model", "/models", "/new", "/plan", "/provider", "/providers", "/rename-session", "/reset", "/rewind", "/session", "/side", "/strict", "/superman", "/tasks", "/tools", "/usage", "/ux",
	}
	items := NewRegistry().List()
	got := make([]string, len(items))
	for index, item := range items {
		got[index] = item.Name
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commands = %#v", got)
	}
}

func TestRegistryParsesCanonicalIntents(t *testing.T) {
	tests := []struct {
		input string
		kind  Kind
		value string
	}{
		{"hello", "", ""},
		{"/plan inspect state", Plan, ""},
		{"/strict", ApprovalMode, "strict"},
		{"/batman", ApprovalMode, "batman"},
		{"/superman", ApprovalMode, "superman"},
		{"/session switch abc", Session, ""},
	}
	registry := NewRegistry()
	for _, test := range tests {
		intent, handled, err := registry.Parse(test.input)
		if err != nil {
			t.Fatalf("Parse(%q): %v", test.input, err)
		}
		if test.kind == "" {
			if handled {
				t.Fatalf("Parse(%q) handled plain input", test.input)
			}
			continue
		}
		if !handled || intent.Kind != test.kind || intent.Value != test.value {
			t.Fatalf("Parse(%q) = %#v, %t", test.input, intent, handled)
		}
	}
}

func TestRegistryRejectsInvalidArguments(t *testing.T) {
	for _, input := range []string{"/auth visible-key", "/diff now", "/context nope", "/index semantic maybe", "/unknown"} {
		if _, handled, err := NewRegistry().Parse(input); !handled || err == nil {
			t.Fatalf("Parse(%q) = handled %t, err %v", input, handled, err)
		}
	}
}
