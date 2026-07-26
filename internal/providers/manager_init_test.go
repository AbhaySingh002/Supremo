package providers

import (
	"context"
	"strings"
	"testing"
)

func TestManagerInitializeRejectsUnsupportedProvider(t *testing.T) {
	dir := t.TempDir()
	if err := SaveConfig(dir, &Config{ProviderName: "other", Model: "model"}); err != nil {
		t.Fatal(err)
	}
	err := NewManager(dir, NewFileCredentialStore(dir)).Initialize(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unsupported provider") {
		t.Fatalf("unexpected initialization error: %v", err)
	}
}
