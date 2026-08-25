package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/api"
	"github.com/AbhaySingh002/supremo/internal/ui/plan"
	"github.com/AbhaySingh002/supremo/internal/ui/rendering"
)

func TestResponsiveHeaderBreakpoints(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := newTestModel(api.Session{ID: "responsive"}, ctx, cancel)
	model.workspaceInfo = workspaceState{branch: "main", changed: 3, ready: true}
	model.credentialReady = true
	model.inputTokens = 12000
	model.outputTokens = 4000
	model.contextLimit = 128000

	// 1. Narrow screen (< 60)
	model.width = 50
	narrowHeader := model.HeaderView()
	if !strings.Contains(narrowHeader, "SUPREMO") {
		t.Fatalf("expected title in narrow header:\n%s", narrowHeader)
	}
	if strings.Contains(narrowHeader, "main · 3 changed") {
		t.Fatal("narrow header should omit workspace details to prevent overflow")
	}

	// 2. Medium screen (70 cols)
	model.width = 70
	medHeader := model.HeaderView()
	if !strings.Contains(medHeader, "main · 3 changed") {
		t.Fatalf("medium header should include git status:\n%s", medHeader)
	}

	// 3. Wide screen (120 cols)
	model.width = 120
	wideHeader := model.HeaderView()
	if !strings.Contains(wideHeader, "16k/128k") {
		t.Fatalf("wide header should include token usage:\n%s", wideHeader)
	}
}

func TestPhaseBadgeIdentity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sess := &api.Session{ID: "phase-test"}
	model := newTestModel(*sess, ctx, cancel)

	// 1. Initial chat state
	if badge := model.PhaseBadge(); badge != "" {
		t.Fatalf("expected empty badge in default chat, got %q", badge)
	}

	// 2. Plan draft mode
	model.planDraft = true
	if badge := model.PhaseBadge(); !strings.Contains(badge, "plan mode") {
		t.Fatalf("expected plan mode badge, got %q", badge)
	}
	model.planDraft = false

	// 3. Plan Question mode
	req := api.QuestionRequest{Questions: []api.Question{{ID: "q1", Question: "Q?"}}}
	model.planQuestion = plan.NewPlanQuestionModel(req, rendering.NewStyles(), 80)
	model.surface = surfacePlanQuestion
	if badge := model.PhaseBadge(); !strings.Contains(badge, "plan: decision") {
		t.Fatalf("expected plan: decision badge, got %q", badge)
	}
	model.planQuestion = nil
	model.surface = surfaceNone

	// 4. Session PlanModeActive
	sess.PlanMode = true
	model.session = *sess
	if badge := model.PhaseBadge(); !strings.Contains(badge, "plan mode") {
		t.Fatalf("expected plan mode badge when session is in plan mode, got %q", badge)
	}
}
