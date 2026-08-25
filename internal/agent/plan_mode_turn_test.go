package agent_test

import (
	"context"
	"strings"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/agent"
	contextcompiler "github.com/AbhaySingh002/supremo/internal/context"
	"github.com/AbhaySingh002/supremo/internal/prompts"
	"github.com/AbhaySingh002/supremo/internal/protocol"
	"github.com/AbhaySingh002/supremo/internal/state"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

func TestPlanModeContextBuilderInjectsPolicy(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	store, err := state.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.CloseWorkspace(root) })

	session := &agent.Session{ID: "plan-mode-session", Name: "Plan Mode Session"}
	if err := session.Save(root); err != nil {
		t.Fatal(err)
	}

	builder, err := agent.NewRealContextBuilder(
		tools.NewRegistry(),
		contextcompiler.New(store, nil),
		func() int { return 32768 },
	)
	if err != nil {
		t.Fatal(err)
	}
	worker := agent.NewAgent(nil, nil, builder, nil, nil)

	ctx := context.Background()

	// 1. When Plan Mode is inactive, PlanModePolicy is not injected
	prompt, err := builder.Compile(ctx, agent.ContextRequest{
		Session:   session,
		Objective: "Build feature",
		Mode:      tools.ToolModeNormal,
		Profile:   protocol.Execution,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt.System, "You are in Plan Mode.") {
		t.Fatal("expected PlanModePolicy to be absent when Plan Mode is inactive")
	}

	// 2. Enable Plan Mode via durable event
	if err := worker.SetPlanMode(ctx, session, true); err != nil {
		t.Fatal(err)
	}
	if !session.PlanModeActive() {
		t.Fatal("expected session.PlanModeActive() to be true")
	}

	// 3. When Plan Mode is active, PlanModePolicy is injected into system prompt
	promptActive, err := builder.Compile(ctx, agent.ContextRequest{
		Session:   session,
		Objective: "Build feature",
		Mode:      tools.ToolModeNormal,
		Profile:   protocol.Execution,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(promptActive.System, "You are in Plan Mode.") {
		t.Fatal("expected PlanModePolicy in system prompt when Plan Mode is active")
	}
	if !strings.Contains(promptActive.System, prompts.PlanModePolicy) {
		t.Fatal("expected full PlanModePolicy text in system prompt")
	}
	if strings.Contains(promptActive.System, "Response Protocol") {
		t.Fatal("Plan Mode must not reintroduce a response envelope")
	}

	// 4. Disable Plan Mode via durable event
	if err := worker.SetPlanMode(ctx, session, false); err != nil {
		t.Fatal(err)
	}
	if session.PlanModeActive() {
		t.Fatal("expected session.PlanModeActive() to be false after disabling")
	}

	promptInactive, err := builder.Compile(ctx, agent.ContextRequest{
		Session:   session,
		Objective: "Build feature",
		Mode:      tools.ToolModeNormal,
		Profile:   protocol.Execution,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(promptInactive.System, "You are in Plan Mode.") {
		t.Fatal("expected PlanModePolicy to be absent after disabling Plan Mode")
	}
}
