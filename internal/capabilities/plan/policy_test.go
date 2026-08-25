package plan_test

import (
	"context"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/capabilities/plan"
	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/runtime"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

func TestGuardAllowsInspectionAndBlocksMutationInPlanMode(t *testing.T) {
	active := true
	guard := plan.NewGuard(func(ctx context.Context, sessionID string) bool {
		return active
	})

	ctx := context.Background()

	// 1. Read-only tool is allowed
	readDesc := tools.ToolDescriptor{
		Name:       "read_file",
		Access:     tools.ToolAccessRead,
		SideEffect: tools.ToolSideEffectNone,
		Inspection: true,
	}
	dec, err := guard.BeforeTool(runtime.BeforeToolEvent{
		Context:    ctx,
		SessionID:  "s1",
		Descriptor: readDesc,
		Call:       models.ToolCall{Name: "read_file"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if dec.Result != nil {
		t.Fatalf("expected read_file to be allowed in Plan Mode, got denial: %#v", dec.Result)
	}

	// 2. Planning tools (ask_user_question and exit_plan_mode) are allowed
	for _, toolName := range []string{"ask_user_question", "exit_plan_mode"} {
		dec, err := guard.BeforeTool(runtime.BeforeToolEvent{
			Context:    ctx,
			SessionID:  "s1",
			Descriptor: tools.ToolDescriptor{Name: toolName},
			Call:       models.ToolCall{Name: toolName},
		})
		if err != nil {
			t.Fatal(err)
		}
		if dec.Result != nil {
			t.Fatalf("expected %s to be allowed in Plan Mode, got denial: %#v", toolName, dec.Result)
		}
	}

	// 3. Mutating write tool is denied with ToolStatusDenied
	writeDesc := tools.ToolDescriptor{
		Name:       "write_file",
		Access:     tools.ToolAccessWrite,
		SideEffect: tools.ToolSideEffectWorkspace,
	}
	dec, err = guard.BeforeTool(runtime.BeforeToolEvent{
		Context:    ctx,
		SessionID:  "s1",
		Descriptor: writeDesc,
		Call:       models.ToolCall{Name: "write_file"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if dec.Result == nil {
		t.Fatal("expected write_file to be blocked in Plan Mode")
	}
	if dec.Result.Status != tools.ToolStatusDenied || dec.Result.Success {
		t.Fatalf("expected ToolStatusDenied, got status=%s success=%v", dec.Result.Status, dec.Result.Success)
	}

	// 4. When Plan Mode is inactive, all tools are allowed
	active = false
	dec, err = guard.BeforeTool(runtime.BeforeToolEvent{
		Context:    ctx,
		SessionID:  "s1",
		Descriptor: writeDesc,
		Call:       models.ToolCall{Name: "write_file"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if dec.Result != nil {
		t.Fatalf("expected write_file to be allowed when Plan Mode is inactive, got: %#v", dec.Result)
	}
}
