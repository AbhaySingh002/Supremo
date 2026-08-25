package tools

import (
	"context"
	"testing"
)

func TestDescriptorDrivesInspectionApprovalReadOnlyAndPlanning(t *testing.T) {
	registry := NewRegistry()
	inspect := catalogTool{name: "inspect_x", caps: CapabilityReadWorkspace}
	mutate := catalogTool{name: "mutate_x", caps: CapabilityWriteWorkspace}
	if err := registry.Register(inspect, ToolMetadata{
		CanonicalName: "inspect_x", Family: "filesystem", Access: ToolAccessRead,
		Inspection: true, PersistCallObservation: true, PlanningCore: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(mutate, ToolMetadata{
		CanonicalName: "mutate_x", Family: "filesystem", Access: ToolAccessWrite,
		SideEffect: ToolSideEffectWorkspace, RequiresApproval: true,
	}); err != nil {
		t.Fatal(err)
	}
	inspectDesc, err := registry.Descriptor("inspect_x")
	if err != nil {
		t.Fatal(err)
	}
	mutateDesc, err := registry.Descriptor("mutate_x")
	if err != nil {
		t.Fatal(err)
	}
	if !inspectDesc.Inspection || !inspectDesc.PersistCallObservation || !inspectDesc.PlanningSafe() || inspectDesc.RequiresApproval {
		t.Fatalf("inspect descriptor %#v", inspectDesc)
	}
	if mutateDesc.Inspection || mutateDesc.PlanningSafe() || !mutateDesc.RequiresApproval {
		t.Fatalf("mutate descriptor %#v", mutateDesc)
	}
	if RequiresApprovalInMode(ApprovalStrict, inspectDesc, nil) {
		t.Fatal("inspection must not require Strict approval")
	}
	if !RequiresApprovalInMode(ApprovalStrict, mutateDesc, nil) {
		t.Fatal("mutate must require Strict approval")
	}
	catalog, err := registry.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	route := catalog.Route(ToolRouteProfile{Mode: ToolModePlanning, ReadOnly: true, ResearchOnly: true, Objective: "inspect"})
	got := map[string]bool{}
	for _, c := range route.Candidates {
		got[c.Tool.Name] = true
	}
	if !got["inspect_x"] || got["mutate_x"] {
		t.Fatalf("read-only/planning route %#v", route)
	}
	manager := NewManager(registry)
	if _, err := manager.Execute(WithApprovalMode(WithReadOnly(context.Background()), ApprovalSuperman), "inspect_x", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Execute(WithApprovalMode(WithReadOnly(context.Background()), ApprovalSuperman), "mutate_x", map[string]any{}); err == nil {
		t.Fatal("read-only must reject mutate via descriptor Access/SideEffect")
	}
}
