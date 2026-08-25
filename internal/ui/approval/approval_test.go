package approval_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	zone "github.com/lrstanley/bubblezone"

	"github.com/AbhaySingh002/supremo/internal/ui/approval"
	"github.com/AbhaySingh002/supremo/internal/ui/rendering"
)

func TestApprovalModelAllowDenyEditAuto(t *testing.T) {
	zone.NewGlobal()
	st := rendering.NewStyles()
	model := approval.NewApprovalModel("execute_command", `execute_command {"command":"rm","-rf","tmp"}`, st)

	// 1. View rendering
	view := model.View(80, 20)
	if !strings.Contains(view, "Approval required") || !strings.Contains(view, "Run shell command?") || !strings.Contains(view, "y/enter allow") {
		t.Fatalf("unexpected approval view:\n%s", view)
	}

	// 2. Test 'y' emits allow
	_, cmd := model.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if cmd == nil {
		t.Fatal("expected command on 'y'")
	}
	msg := cmd()
	act, ok := msg.(approval.ApprovalActionMsg)
	if !ok || act.Action != "approve" {
		t.Fatalf("expected approve action, got %#v", msg)
	}

	// 3. Test 'n' emits deny
	_, cmd = model.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if cmd == nil {
		t.Fatal("expected command on 'n'")
	}
	msg = cmd()
	act, ok = msg.(approval.ApprovalActionMsg)
	if !ok || act.Action != "deny" {
		t.Fatalf("expected deny action, got %#v", msg)
	}

	// 4. Test 'a' emits auto-approve
	_, cmd = model.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if cmd == nil {
		t.Fatal("expected command on 'a'")
	}
	msg = cmd()
	act, ok = msg.(approval.ApprovalActionMsg)
	if !ok || act.Action != "auto" {
		t.Fatalf("expected auto action, got %#v", msg)
	}

	// 5. Test 'e' enters editing mode
	model, cmd = model.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	if !model.IsEditing() {
		t.Fatal("expected approval to be in editing mode after 'e'")
	}

	// In editing mode, View shows textarea
	editView := model.View(80, 20)
	if !strings.Contains(editView, "Edit JSON arguments") {
		t.Fatalf("expected editing view prompt:\n%s", editView)
	}

	// Esc cancels edit mode
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if model.IsEditing() {
		t.Fatal("expected Esc to cancel editing mode")
	}

	model, _ = model.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	_, cmd = model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected edited arguments command")
	}
	act, ok = cmd().(approval.ApprovalActionMsg)
	if !ok || act.Action != "edit" {
		t.Fatalf("expected edit action, got %#v", act)
	}
}

func TestApprovalBodyScrollsWithoutHidingActions(t *testing.T) {
	values := make(map[string]any)
	for i := 0; i < 30; i++ {
		values[fmt.Sprintf("field_%02d", i)] = strings.Repeat("value", 8)
	}
	arguments, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	model := approval.NewApprovalModel("execute_command", string(arguments), rendering.NewStyles())
	view := model.View(60, 12)
	if height := lipgloss.Height(view); height > 12 {
		t.Fatalf("approval height = %d, want <= 12\n%s", height, view)
	}
	if !strings.Contains(view, "y allow") || !strings.Contains(view, "n deny") {
		t.Fatalf("approval actions were clipped:\n%s", view)
	}
	model, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	if scrolled := model.View(60, 12); !strings.Contains(scrolled, "field 29") {
		t.Fatalf("approval body did not scroll to the end:\n%s", scrolled)
	}
}
