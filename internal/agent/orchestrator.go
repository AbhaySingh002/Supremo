package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/AbhaySingh002/supremo/internal/models"
	"github.com/AbhaySingh002/supremo/internal/prompts"
)

type auditVerdict struct {
	Approved   bool     `json:"approved"`
	Reason     string   `json:"reason"`
	RetrySteps []string `json:"retry_steps"`
}

func (a *Agent) runPlanMode(ctx context.Context, session *Session, userInput string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	if err := a.memory.Append(ctx, session.ID, models.Message{Role: models.RoleUser, Content: userInput}); err != nil {
		return "", fmt.Errorf("append user message: %w", err)
	}

	a.report("Planning…")
	// Keep the previous checkpoint active until a replacement has validated and saved.
	plannerSession := *session
	plannerSession.CurrentPlanID = ""
	plan, err := a.plan(ctx, &plannerSession, userInput)
	if err != nil {
		return "", err
	}
	if err := session.SetPlan(a.workspace, plan); err != nil {
		return "", err
	}
	if err := a.build(ctx, session, plan, nil); err != nil {
		return "", err
	}

	return a.finishPlan(ctx, session, plan)
}

// ResumePlan continues pending and failed steps from the active checkpoint.
func (a *Agent) ResumePlan(ctx context.Context, session *Session) (string, error) {
	ctx, cancel := a.taskContext(ctx, session)
	defer cancel()
	plan, err := session.ActivePlan(a.workspace)
	if err != nil {
		return "", err
	}
	if plan == nil {
		return "", fmt.Errorf("no active plan")
	}
	a.report("Resuming plan " + plan.ID + "…")
	if err := a.build(ctx, session, plan, nil); err != nil {
		return "", err
	}
	return a.finishPlan(ctx, session, plan)
}

func (a *Agent) finishPlan(ctx context.Context, session *Session, plan *Plan) (string, error) {
	a.report("Auditing…")
	verdict, err := a.audit(ctx, session)
	if err != nil {
		return "", err
	}
	if !verdict.Approved && len(verdict.RetrySteps) > 0 {
		retry := make(map[string]bool, len(verdict.RetrySteps))
		for _, id := range verdict.RetrySteps {
			retry[id] = true
		}
		if err := a.build(ctx, session, plan, retry); err != nil {
			return "", err
		}
		a.report("Auditing retry…")
		verdict, err = a.audit(ctx, session)
		if err != nil {
			return "", err
		}
	}

	if verdict.Approved {
		return fmt.Sprintf("Plan %s approved: %s", plan.ID, verdict.Reason), nil
	}
	return fmt.Sprintf("Plan %s needs attention: %s", plan.ID, verdict.Reason), nil
}

func (a *Agent) plan(ctx context.Context, session *Session, userInput string) (*Plan, error) {
	prompt, err := a.contextBuilder.Build(ctx, session, userInput, &State{MaxIterations: 1})
	if err != nil {
		return nil, fmt.Errorf("build planner context: %w", err)
	}
	prompt.System += "\n\n" + prompts.PlanMode()
	completion, err := a.provider.Chat(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("planner request: %w", err)
	}
	if completion == nil {
		return nil, fmt.Errorf("planner returned no completion")
	}
	if err := a.memory.Append(ctx, session.ID, models.Message{Role: models.RoleAssistant, Content: completion.Raw}); err != nil {
		return nil, err
	}

	var plan Plan
	if err := a.decodeJSON(completion.Raw, &plan); err != nil {
		return nil, fmt.Errorf("parse planner response: %w", err)
	}
	plan.ID = fmt.Sprintf("plan-%d", time.Now().UnixNano())
	if err := a.validatePlan(&plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

func (a *Agent) build(ctx context.Context, session *Session, plan *Plan, retry map[string]bool) error {
	for i := range plan.Steps {
		if err := ctx.Err(); err != nil {
			return err
		}
		step := &plan.Steps[i]
		if retry == nil && step.Status != StepPending && step.Status != StepFailed {
			continue
		}
		if retry != nil && (!retry[step.ID] || step.Status != StepFailed) {
			continue
		}
		if err := a.persistStep(session, plan, step.ID, StepInProgress, ""); err != nil {
			return err
		}
		a.report("Step " + step.ID + ": " + step.Description)

		result, err := a.toolManager.Execute(ctx, step.Tool, step.Arguments)
		observation := NewObservation(step.Tool, result, err)
		status := StepCompleted
		if err != nil || !observation.Success {
			status = StepFailed
		}
		if err := a.persistStep(session, plan, step.ID, status, truncateTokens(observation.Output, 250)); err != nil {
			return err
		}
		if err := a.memory.Append(ctx, session.ID, models.Message{Role: models.RoleTool, Content: observation.Output}); err != nil {
			return err
		}
		a.report("Step " + step.ID + ": " + status)
		if status == StepFailed {
			return nil
		}
	}
	return nil
}

func (a *Agent) audit(ctx context.Context, session *Session) (*auditVerdict, error) {
	prompt, err := a.contextBuilder.Build(ctx, session, "", &State{MaxIterations: 1})
	if err != nil {
		return nil, fmt.Errorf("build auditor context: %w", err)
	}
	prompt.System += "\n\n" + prompts.Audit()
	completion, err := a.provider.Chat(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("auditor request: %w", err)
	}
	if completion == nil {
		return nil, fmt.Errorf("auditor returned no completion")
	}
	if err := a.memory.Append(ctx, session.ID, models.Message{Role: models.RoleAssistant, Content: completion.Raw}); err != nil {
		return nil, err
	}

	var verdict auditVerdict
	if err := a.decodeJSON(completion.Raw, &verdict); err != nil {
		return nil, fmt.Errorf("parse auditor response: %w", err)
	}
	if strings.TrimSpace(verdict.Reason) == "" {
		return nil, fmt.Errorf("auditor response is missing a reason")
	}
	plan, err := session.ActivePlan(a.workspace)
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, fmt.Errorf("auditor has no active plan")
	}
	if verdict.Approved && len(verdict.RetrySteps) > 0 {
		return nil, fmt.Errorf("approved auditor response cannot request retries")
	}
	for _, id := range verdict.RetrySteps {
		if !failedStep(plan, id) {
			return nil, fmt.Errorf("auditor requested non-failed step %q", id)
		}
	}
	return &verdict, nil
}

func (a *Agent) persistStep(session *Session, plan *Plan, id, status, result string) error {
	if err := plan.UpdateStep(id, status, result); err != nil {
		return err
	}
	if err := SavePlan(a.workspace, plan); err != nil {
		return err
	}
	return session.Save(a.workspace)
}

func (a *Agent) validatePlan(plan *Plan) error {
	if strings.TrimSpace(plan.Description) == "" || len(plan.Steps) == 0 {
		return fmt.Errorf("planner returned an empty plan")
	}
	seen := make(map[string]bool, len(plan.Steps))
	for _, step := range plan.Steps {
		if !validPlanID(step.ID) || seen[step.ID] || step.Tool == "" || step.Arguments == nil || step.Status != StepPending {
			return fmt.Errorf("planner returned an invalid step %q", step.ID)
		}
		if !a.toolManager.Has(step.Tool) {
			return fmt.Errorf("planner selected unknown tool %q", step.Tool)
		}
		seen[step.ID] = true
	}
	return nil
}

func (a *Agent) decodeJSON(raw string, target any) error {
	content := strings.TrimSpace(raw)
	if content == "" {
		return fmt.Errorf("expected JSON response")
	}
	decoder := json.NewDecoder(bytes.NewBufferString(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected JSON value after response")
		}
		return fmt.Errorf("unexpected content after JSON response: %w", err)
	}
	if _, ok := target.(*auditVerdict); ok {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(content), &fields); err != nil {
			return err
		}
		for _, name := range []string{"approved", "reason", "retry_steps"} {
			if _, ok := fields[name]; !ok {
				return fmt.Errorf("auditor response is missing %q", name)
			}
		}
	}
	return nil
}

func failedStep(plan *Plan, id string) bool {
	for _, step := range plan.Steps {
		if step.ID == id {
			return step.Status == StepFailed
		}
	}
	return false
}
