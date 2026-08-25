package repeat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/runtime"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

func TestCanonicalJSONAndFingerprint(t *testing.T) {
	tests := []struct {
		name string
		raw  any
		want string
	}{
		{"nil", nil, `{}`},
		{"empty", "", `{}`},
		{"null", "null", `null`},
		{"nested key order", `{"b":{"y":2,"x":1},"a":1}`, `{"a":1,"b":{"x":1,"y":2}}`},
		{"array order", `[2,1]`, `[2,1]`},
		{"large integer", `{"id":1234567890123456789}`, `{"id":1234567890123456789}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := CanonicalJSON(test.raw); got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}

	first := models.ToolCall{ID: "one", Name: "read_file", Arguments: json.RawMessage(`{"path":"main.go","offset":10}`)}
	second := models.ToolCall{
		ID:               "two",
		Name:             "read_file",
		Arguments:        json.RawMessage(`{"offset":10,"path":"main.go"}`),
		Synthetic:        true,
		ProviderMetadata: json.RawMessage(`{"signature":"ignored"}`),
	}
	if got, want := ToolCallFingerprint(first), `read_file:{"offset":10,"path":"main.go"}`; got != want || got != ToolCallFingerprint(second) {
		t.Fatalf("fingerprints got %q and %q, want %q", got, ToolCallFingerprint(second), want)
	}
}

func TestValidateThresholds(t *testing.T) {
	for _, thresholds := range [][]int{{2, 3}, {3, 5, 8}} {
		if err := ValidateThresholds(thresholds); err != nil {
			t.Fatalf("valid thresholds %v: %v", thresholds, err)
		}
	}
	for _, thresholds := range [][]int{{}, {1, 3}, {3, 3}, {5, 3}} {
		if err := ValidateThresholds(thresholds); err == nil {
			t.Fatalf("invalid thresholds accepted: %v", thresholds)
		}
	}
}

func TestGuardTracksConsecutiveCallsAndResets(t *testing.T) {
	guard := New(Config{Thresholds: []int{3, 5}})
	readA := models.ToolCall{Name: "read_file", Arguments: json.RawMessage(`{"path":"a.go"}`)}
	readB := models.ToolCall{Name: "read_file", Arguments: json.RawMessage(`{"path":"b.go"}`)}
	grep := models.ToolCall{Name: "grep_search", Arguments: json.RawMessage(`{"pattern":"foo"}`)}

	if guard.Observe(readA) != nil || guard.Observe(readA) != nil {
		t.Fatal("reminder emitted below first threshold")
	}
	if reminder := guard.Observe(readA); reminder == nil || !strings.Contains(reminder.Content, "You are repeating") {
		t.Fatalf("first threshold reminder = %#v", reminder)
	}
	if guard.Observe(readA) != nil {
		t.Fatal("reminder emitted between configured thresholds")
	}
	if reminder := guard.Observe(readA); reminder == nil || !strings.Contains(reminder.Content, "Consecutive calls: 5") {
		t.Fatalf("later threshold reminder = %#v", reminder)
	}
	guard.Observe(readB)
	if chain := guard.Chain(); chain.Count != 1 || !strings.Contains(chain.Key, `"path":"b.go"`) {
		t.Fatalf("argument change did not reset chain: %#v", chain)
	}
	guard.Observe(grep)
	if chain := guard.Chain(); chain.Count != 1 || !strings.HasPrefix(chain.Key, "grep_search:") {
		t.Fatalf("tool change did not reset chain: %#v", chain)
	}
	guard.Reset()
	if guard.Chain() != nil {
		t.Fatalf("reset left chain: %#v", guard.Chain())
	}
}

func TestGuardExclusionsAndArgumentPreview(t *testing.T) {
	guard := New(Config{Thresholds: []int{2, 3}, Exclude: []string{"wait_*"}, ArgumentsPreviewLen: 50})
	longPath := "very/long/path/to/some/nested/deeply/located/file_with_large_name.go"
	read := models.ToolCall{Name: "read_file", Arguments: json.RawMessage(`{"path":"` + longPath + `","offset":100}`)}
	wait := models.ToolCall{Name: "wait_polling", Arguments: json.RawMessage(`{"seconds":5}`)}

	guard.Observe(read)
	if reminder := guard.Observe(wait); reminder != nil || guard.Chain().Count != 1 {
		t.Fatalf("excluded tool changed active chain: reminder=%#v chain=%#v", reminder, guard.Chain())
	}
	guard.Observe(read)
	reminder := guard.Observe(read)
	if reminder == nil || !strings.Contains(reminder.Content, "… (+") {
		t.Fatalf("long arguments were not summarized: %#v", reminder)
	}
	if !strings.Contains(guard.Chain().Key, longPath) {
		t.Fatalf("fingerprint was truncated: %s", guard.Chain().Key)
	}
}

func TestAfterToolAndUserInputHooks(t *testing.T) {
	guard := New(Config{Thresholds: []int{2}})
	call := models.ToolCall{Name: "read_file", Arguments: json.RawMessage(`{"path":"a.go"}`)}
	event := runtime.AfterToolEvent{Call: call, Result: &tools.ToolResult{Success: true}}
	first, firstErr := guard.AfterTool(event)
	second, secondErr := guard.AfterTool(event)
	if firstErr != nil || secondErr != nil || len(first.NextStep) != 0 || len(second.NextStep) != 1 {
		t.Fatalf("first=%#v second=%#v errors=%v/%v", first, second, firstErr, secondErr)
	}
	guard.OnUserInput(runtime.UserInputEvent{Kind: runtime.UserInputRun})
	if guard.Chain() != nil {
		t.Fatalf("user input left chain: %#v", guard.Chain())
	}
}
