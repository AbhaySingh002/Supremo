package app

import (
	"reflect"
	"testing"
)

func TestBuildProviderRegistryRegistersBuiltins(t *testing.T) {
	registry, err := buildProviderRegistry()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"anthropic", "gemini", "groq", "mistral", "nvidia", "openai", "openai-compatible", "openrouter"}
	if got := registry.RegisteredTypes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("RegisteredTypes() = %v, want %v", got, want)
	}
}
