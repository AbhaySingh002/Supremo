package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/AbhaySingh002/supremo/internal/storage"
)

const (
	StepPending    = "pending"
	StepInProgress = "in_progress"
	StepCompleted  = "completed"
	StepFailed     = "failed"
)

// Plan is the durable task checklist for a session.
type Plan struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Steps       []Step `json:"steps"`
}

// Step is one executable unit of a Plan.
type Step struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Tool        string `json:"tool,omitempty"`
	Arguments   any    `json:"arguments,omitempty"`
	Status      string `json:"status"`
	Result      string `json:"result,omitempty"`
}

// SavePlan atomically persists a plan under .session/plans.
func SavePlan(root string, plan *Plan) error {
	if plan == nil || !validPlanID(plan.ID) {
		return fmt.Errorf("plan must have a safe ID")
	}
	for _, step := range plan.Steps {
		if step.ID == "" || !validStepStatus(step.Status) {
			return fmt.Errorf("plan step %q has an invalid ID or status", step.ID)
		}
	}

	dir := filepath.Join(root, ".session", "plans")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	return storage.WriteFileAtomic(filepath.Join(dir, plan.ID+".json"), data, 0600)
}

// LoadPlan reads a persisted plan by ID.
func LoadPlan(root, id string) (*Plan, error) {
	if !validPlanID(id) {
		return nil, fmt.Errorf("invalid plan ID")
	}
	data, err := os.ReadFile(filepath.Join(root, ".session", "plans", id+".json"))
	if err != nil {
		return nil, err
	}
	var plan Plan
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, fmt.Errorf("load plan: %w", err)
	}
	return &plan, nil
}

// UpdateStep records a step result and validates its state transition input.
func (p *Plan) UpdateStep(id, status, result string) error {
	if !validStepStatus(status) {
		return fmt.Errorf("invalid step status %q", status)
	}
	for i := range p.Steps {
		if p.Steps[i].ID == id {
			p.Steps[i].Status = status
			p.Steps[i].Result = result
			return nil
		}
	}
	return fmt.Errorf("plan step %q not found", id)
}

// Context renders a compact checklist for the system prompt.
func (p *Plan) Context() string {
	var out strings.Builder
	fmt.Fprintf(&out, "# Active Plan: %s\n", p.Description)
	for _, step := range p.Steps {
		mark := " "
		switch step.Status {
		case StepCompleted:
			mark = "x"
		case StepInProgress:
			mark = ">"
		case StepFailed:
			mark = "!"
		}
		fmt.Fprintf(&out, "- [%s] %s", mark, step.Description)
		if step.Tool != "" {
			fmt.Fprintf(&out, " (%s)", step.Tool)
		}
		out.WriteByte('\n')
	}
	return out.String()
}

func validPlanID(id string) bool {
	return id != "" && safeSessionID(id) == id
}

func validStepStatus(status string) bool {
	return status == StepPending || status == StepInProgress || status == StepCompleted || status == StepFailed
}
