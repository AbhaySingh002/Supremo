package subagent

import (
	"context"
	"errors"

	"github.com/AbhaySingh002/supremo/internal/agent"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

type Start struct{ Manager *agent.SubagentManager }

func (*Start) Name() string { return "subagent" }
func (*Start) Description() string {
	return "Delegate a standalone task to an isolated child session. Background execution is the default; use wait_agent, send_message, list_agents, and interrupt_agent with the returned agent_id. Choose local_read for inspection or execution to inherit the parent approval policy."
}
func (*Start) Capabilities() tools.CapabilitySet { return tools.CapabilityUseNetwork }
func (*Start) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"label":             map[string]any{"type": "string", "description": "Short durable label for the child"},
			"prompt":            map[string]any{"type": "string", "description": "Complete standalone task; the child does not inherit parent conversation history"},
			"scope":             map[string]any{"type": "string", "enum": []string{"local_read", "execution"}},
			"run_in_background": map[string]any{"type": "boolean", "description": "Return after durable queue acceptance; defaults to true"},
		},
		"required": []string{"label", "prompt", "scope"},
	}
}
func (t *Start) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	if t == nil || t.Manager == nil {
		return nil, errors.New("subagent manager is unavailable")
	}
	var args struct {
		Label           string `json:"label"`
		Prompt          string `json:"prompt"`
		Scope           string `json:"scope"`
		RunInBackground *bool  `json:"run_in_background,omitempty"`
	}
	if err := tools.ParseInput(input, &args); err != nil {
		return nil, err
	}
	run, err := t.Manager.Start(ctx, agent.SubagentRequest{
		ParentSessionID: callerSession(ctx), Label: args.Label, Prompt: args.Prompt,
		Scope: agent.SubagentScope(args.Scope), RunInBackground: args.RunInBackground,
	})
	if err != nil {
		return nil, err
	}
	message := "started subagent " + run.AgentID
	if args.RunInBackground != nil && !*args.RunInBackground {
		message = run.Output
	}
	return tools.BuildSerializedToolResult(true, message, run), nil
}

type List struct{ Manager *agent.SubagentManager }

func (*List) Name() string { return "list_agents" }
func (*List) Description() string {
	return "List direct child agents or the complete descendant tree with durable identity and live status."
}
func (*List) Capabilities() tools.CapabilitySet { return tools.CapabilityReadWorkspace }
func (*List) Schema() any {
	return map[string]any{"type": "object", "properties": map[string]any{"scope": map[string]any{"type": "string", "enum": []string{"children", "descendants"}}}}
}
func (t *List) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	var args struct {
		Scope string `json:"scope,omitempty"`
	}
	if err := tools.ParseInput(input, &args); err != nil {
		return nil, err
	}
	items, err := t.Manager.List(ctx, callerSession(ctx), args.Scope == "descendants")
	if err != nil {
		return nil, err
	}
	return tools.BuildSerializedToolResult(true, "listed child agents", items), nil
}

type Send struct{ Manager *agent.SubagentManager }

func (*Send) Name() string { return "send_message" }
func (*Send) Description() string {
	return "Queue a message as the next FIFO turn for a direct child agent. This returns acceptance, not the child reply."
}
func (*Send) Capabilities() tools.CapabilitySet { return tools.CapabilityUseNetwork }
func (*Send) Schema() any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"agent_id": map[string]any{"type": "string"}, "message": map[string]any{"type": "string"},
	}, "required": []string{"agent_id", "message"}}
}
func (t *Send) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	var args struct {
		AgentID string `json:"agent_id"`
		Message string `json:"message"`
	}
	if err := tools.ParseInput(input, &args); err != nil {
		return nil, err
	}
	run, err := t.Manager.Send(ctx, callerSession(ctx), args.AgentID, args.Message)
	if err != nil {
		return nil, err
	}
	return tools.BuildSerializedToolResult(true, "message queued for subagent "+args.AgentID, run), nil
}

type Wait struct{ Manager *agent.SubagentManager }

func (*Wait) Name() string { return "wait_agent" }
func (*Wait) Description() string {
	return "Wait for a direct child's queued run, or read its latest durable result when already settled."
}
func (*Wait) Capabilities() tools.CapabilitySet { return tools.CapabilityUseNetwork }
func (*Wait) Schema() any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"agent_id": map[string]any{"type": "string"}, "message_id": map[string]any{"type": "string"},
	}, "required": []string{"agent_id"}}
}
func (t *Wait) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	var args struct {
		AgentID   string `json:"agent_id"`
		MessageID string `json:"message_id,omitempty"`
	}
	if err := tools.ParseInput(input, &args); err != nil {
		return nil, err
	}
	run, err := t.Manager.Wait(ctx, callerSession(ctx), args.AgentID, args.MessageID)
	if err != nil {
		return nil, err
	}
	return tools.BuildSerializedToolResult(true, run.Output, run), nil
}

type Interrupt struct{ Manager *agent.SubagentManager }

func (*Interrupt) Name() string { return "interrupt_agent" }
func (*Interrupt) Description() string {
	return "Interrupt a descendant agent's current turn without deleting its durable session or later queued work."
}
func (*Interrupt) Capabilities() tools.CapabilitySet { return tools.CapabilityUseNetwork }
func (*Interrupt) Schema() any {
	return map[string]any{"type": "object", "properties": map[string]any{"agent_id": map[string]any{"type": "string"}}, "required": []string{"agent_id"}}
}
func (t *Interrupt) Execute(ctx context.Context, input any) (*tools.ToolResult, error) {
	var args struct {
		AgentID string `json:"agent_id"`
	}
	if err := tools.ParseInput(input, &args); err != nil {
		return nil, err
	}
	if err := t.Manager.Interrupt(ctx, callerSession(ctx), args.AgentID); err != nil {
		return nil, err
	}
	return tools.BuildSerializedToolResult(true, "interrupt requested for subagent "+args.AgentID, map[string]any{"agent_id": args.AgentID}), nil
}

func callerSession(ctx context.Context) string {
	return tools.ProgressScopeFromContext(ctx).SessionID
}
