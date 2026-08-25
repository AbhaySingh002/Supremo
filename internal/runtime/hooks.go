package runtime

import (
	"context"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/tools"
)

// UserInputKind identifies a genuine human-originated turn, not synthetic NextStep text.
type UserInputKind string

const (
	UserInputRun          UserInputKind = "run"
	UserInputFollowup     UserInputKind = "followup"
	UserInputSteer        UserInputKind = "steer"
	UserInputSideQuestion UserInputKind = "side_question"
)

type UserInputEvent struct {
	Kind UserInputKind
}

type BeforeToolEvent struct {
	Context    context.Context
	SessionID  string
	TaskID     string
	Call       models.ToolCall
	Descriptor tools.ToolDescriptor
}

// BeforeToolDecision: Result == nil continues physical dispatch; non-nil skips it.
type BeforeToolDecision struct {
	Result *tools.ToolResult
	Reused bool
}

type AfterToolEvent struct {
	Context    context.Context
	SessionID  string
	TaskID     string
	Call       models.ToolCall
	Result     *tools.ToolResult
	Outcome    tools.ToolOutcomeClass
	Reused     bool
	Descriptor tools.ToolDescriptor
}

type AfterToolDecision struct {
	NextStep []models.Message
}

type BeforeToolObserver interface {
	BeforeTool(BeforeToolEvent) (BeforeToolDecision, error)
}

type AfterToolObserver interface {
	AfterTool(AfterToolEvent) (AfterToolDecision, error)
}

type UserInputObserver interface {
	OnUserInput(UserInputEvent)
}

// HookSet is the ordered, capability-agnostic lifecycle table injected into Agent.
type HookSet struct {
	beforeTool []BeforeToolObserver
	afterTool  []AfterToolObserver
	userInput  []UserInputObserver
}

func NewHookSet() *HookSet { return &HookSet{} }

func (h *HookSet) AddBeforeTool(obs BeforeToolObserver) {
	if h == nil || obs == nil {
		return
	}
	h.beforeTool = append(h.beforeTool, obs)
}

func (h *HookSet) AddAfterTool(obs AfterToolObserver) {
	if h == nil || obs == nil {
		return
	}
	h.afterTool = append(h.afterTool, obs)
}

func (h *HookSet) AddUserInput(obs UserInputObserver) {
	if h == nil || obs == nil {
		return
	}
	h.userInput = append(h.userInput, obs)
}

func (h *HookSet) NotifyUserInput(kind UserInputKind) {
	if h == nil {
		return
	}
	event := UserInputEvent{Kind: kind}
	for _, obs := range h.userInput {
		obs.OnUserInput(event)
	}
}

func (h *HookSet) RunBeforeTool(event BeforeToolEvent) (BeforeToolDecision, error) {
	if h == nil {
		return BeforeToolDecision{}, nil
	}
	for _, obs := range h.beforeTool {
		decision, err := obs.BeforeTool(event)
		if err != nil {
			return BeforeToolDecision{}, err
		}
		if decision.Result != nil {
			return decision, nil
		}
	}
	return BeforeToolDecision{}, nil
}

func (h *HookSet) RunAfterTool(event AfterToolEvent) (AfterToolDecision, error) {
	var out AfterToolDecision
	if h == nil {
		return out, nil
	}
	for _, obs := range h.afterTool {
		decision, err := obs.AfterTool(event)
		if err != nil {
			return out, err
		}
		out.NextStep = append(out.NextStep, decision.NextStep...)
	}
	return out, nil
}
