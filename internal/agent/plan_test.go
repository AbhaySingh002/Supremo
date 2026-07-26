package agent

import "testing"

func TestSessionPlanLifecycle(t *testing.T) {
	root := t.TempDir()
	session, err := LoadOrCreateSession(root, "test-session")
	if err != nil {
		t.Fatal(err)
	}
	plan := &Plan{
		ID:          "feature-plan",
		Description: "Ship the feature",
		Steps: []Step{
			{ID: "inspect", Description: "Inspect the code", Status: StepPending},
			{ID: "test", Description: "Run tests", Tool: "run_tests", Status: StepPending},
		},
	}
	if err := session.SetPlan(root, plan); err != nil {
		t.Fatal(err)
	}
	if err := plan.UpdateStep("inspect", StepCompleted, "reviewed"); err != nil {
		t.Fatal(err)
	}
	if err := SavePlan(root, plan); err != nil {
		t.Fatal(err)
	}

	resumed, err := LoadOrCreateSession(root, "test-session")
	if err != nil {
		t.Fatal(err)
	}
	active, err := resumed.ActivePlan(root)
	if err != nil {
		t.Fatal(err)
	}
	if active == nil || active.Steps[0].Status != StepCompleted || active.Steps[0].Result != "reviewed" {
		t.Fatalf("unexpected restored plan: %#v", active)
	}
}
