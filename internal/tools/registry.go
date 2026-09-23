package tools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ToolMode identifies the agent phase that may receive a tool schema. It is
// prompt routing only; Manager remains the authority for execution policy.
type ToolMode string

const (
	ToolModeNormal    ToolMode = "normal"
	ToolModePlanning  ToolMode = "planning"
	ToolModeExecution ToolMode = "execution"
	ToolModeAudit     ToolMode = "audit"
	ToolModeExplore   ToolMode = "explore"
	ToolModeSide      ToolMode = "side_question"
)

type ToolAccess string

const (
	ToolAccessRead        ToolAccess = "read"
	ToolAccessWrite       ToolAccess = "write"
	ToolAccessDestructive ToolAccess = "destructive"
)

type ToolSideEffect string

const (
	ToolSideEffectNone      ToolSideEffect = "none"
	ToolSideEffectWorkspace ToolSideEffect = "workspace"
	ToolSideEffectProcess   ToolSideEffect = "process"
	ToolSideEffectNetwork   ToolSideEffect = "network"
)

// ToolMetadata is registry-owned, declarative routing data. Implementations
// keep their small execution interface; a future external registration can
// provide this data without teaching the tool about prompt construction.
type ToolMetadata struct {
	CanonicalName          string
	Family                 string
	CapabilityTags         []string
	Access                 ToolAccess
	SideEffect             ToolSideEffect
	SupportedModes         []ToolMode
	Inspection             bool
	PersistCallObservation bool
	RequiresApproval       bool
	BatmanManifest         bool
	ParallelSafe           bool
}

// ToolDescriptor is the validated, prompt-safe view of one registered tool.
// JSON schemas are bytes rather than rendered prompt fragments, so no caller
// has to parse generated text to select a schema.
type ToolDescriptor struct {
	Name                   string          `json:"name"`
	Description            string          `json:"description"`
	Family                 string          `json:"family"`
	CapabilityTags         []string        `json:"capability_tags"`
	InputSchema            json.RawMessage `json:"input_schema"`
	Access                 ToolAccess      `json:"access"`
	SideEffect             ToolSideEffect  `json:"side_effect"`
	SupportedModes         []ToolMode      `json:"supported_modes"`
	Inspection             bool            `json:"inspection,omitempty"`
	PersistCallObservation bool            `json:"persist_call_observation,omitempty"`
	RequiresApproval       bool            `json:"requires_approval,omitempty"`
	BatmanManifest         bool            `json:"batman_manifest,omitempty"`
	ParallelSafe           bool            `json:"parallel_safe,omitempty"`
	SchemaTokens           int             `json:"schema_tokens"`
}

// QuietActivity reports tools that should not become transcript activity rows.
func (t ToolDescriptor) QuietActivity() bool {
	return t.Access == ToolAccessRead && !t.RequiresApproval && t.SideEffect != ToolSideEffectNetwork
}

// ToolCatalog is the complete structured registry snapshot. Rendering is a
// final provider-facing step; schema selection operates on descriptors.
type ToolCatalog struct{ Tools []ToolDescriptor }

func (c ToolCatalog) Descriptor(name string) (ToolDescriptor, bool) {
	for _, tool := range c.Tools {
		if tool.Name == name {
			return tool, true
		}
	}
	return ToolDescriptor{}, false
}

// PlanningSafe is the hard execution boundary for repository blueprinting.
func (t ToolDescriptor) PlanningSafe() bool {
	return t.Access == ToolAccessRead && t.SideEffect == ToolSideEffectNone
}

func RenderToolSchemaWithPolicy(tool ToolDescriptor, policy string) string {
	return fmt.Sprintf("## %s\n\n%s\n\nCapability: %s. Family: %s. Access: %s. Policy: %s.\n\nArguments:\n```json\n%s\n```\n\nResult envelope: `status`, `preview`, `artifact_id`, diagnostics, affected entities, and retryability.\n", tool.Name, tool.Description, strings.Join(tool.CapabilityTags, ", "), tool.Family, tool.Access, policy, tool.InputSchema)
}

type ToolRouteProfile struct {
	Mode         ToolMode
	ReadOnly     bool
	ResearchOnly bool
	ApprovalMode ApprovalMode
	DryRun       bool
}

type ToolRouteCandidate struct {
	Tool   ToolDescriptor
	Reason string
	Policy string
}

type ToolRouteRejection struct {
	Name   string
	Reason string
}

type ToolRoute struct {
	Available            int
	Eligible             int
	EligibleSchemaTokens int
	Candidates           []ToolRouteCandidate
	Rejected             []ToolRouteRejection
}

// Route deterministically exposes every tool eligible for the current profile.
// It chooses schemas for the next prompt; Manager remains the execution authority.
func (c ToolCatalog) Route(profile ToolRouteProfile) ToolRoute {
	route := ToolRoute{Available: len(c.Tools)}
	if profile.Mode == ToolModeSide {
		return route
	}
	for _, tool := range c.Tools {
		if !toolEligible(tool, profile) {
			continue
		}
		route.Eligible++
		route.EligibleSchemaTokens += tool.SchemaTokens
		policy := ApprovalPolicyLabel(profile.ApprovalMode, tool)
		if profile.DryRun && tool.RequiresApproval {
			policy = "dry run: execution is simulated"
		}
		route.Candidates = append(route.Candidates, ToolRouteCandidate{Tool: tool, Reason: string(profile.Mode), Policy: policy})
	}
	sort.Slice(route.Candidates, func(i, j int) bool {
		return route.Candidates[i].Tool.Name < route.Candidates[j].Tool.Name
	})
	return route
}

func toolAllowsMode(tool ToolDescriptor, mode ToolMode) bool {
	for _, allowed := range tool.SupportedModes {
		if allowed == mode {
			return true
		}
	}
	return false
}

func toolEligible(tool ToolDescriptor, profile ToolRouteProfile) bool {
	if !toolAllowsMode(tool, profile.Mode) {
		return false
	}
	if profile.ReadOnly && !tool.PlanningSafe() {
		return false
	}
	return !profile.ResearchOnly || tool.PlanningSafe()
}

// Registry manages executable tools and their immutable descriptors.
type Registry struct{ tools map[string]registeredTool }

type registeredTool struct {
	tool       Tool
	descriptor ToolDescriptor
}

func NewRegistry() *Registry { return &Registry{tools: make(map[string]registeredTool)} }

// Register accepts an optional metadata override for future external tools.
// Built-ins receive complete deterministic defaults, so every registry entry
// has the metadata required by routing.
func (r *Registry) Register(tool Tool, metadata ...ToolMetadata) error {
	if tool == nil || strings.TrimSpace(tool.Name()) == "" {
		return fmt.Errorf("tool name is required")
	}
	if _, exists := r.tools[tool.Name()]; exists {
		return fmt.Errorf("%s already exists", tool.Name())
	}
	meta := defaultToolMetadata(tool)
	if len(metadata) > 0 {
		meta = mergeMetadata(meta, metadata[0])
	}
	descriptor, err := descriptorFor(tool, meta)
	if err != nil {
		return err
	}
	r.tools[tool.Name()] = registeredTool{tool: tool, descriptor: descriptor}
	return nil
}

func (r *Registry) Get(name string) (Tool, error) {
	registered, ok := r.tools[name]
	if !ok {
		return nil, ErrToolNotFound
	}
	return registered.tool, nil
}

// Descriptor returns the validated policy metadata used by routing and the
// execution boundary. Tool implementations cannot opt themselves out later.
func (r *Registry) Descriptor(name string) (ToolDescriptor, error) {
	registered, ok := r.tools[name]
	if !ok {
		return ToolDescriptor{}, ErrToolNotFound
	}
	return registered.descriptor, nil
}

func (r *Registry) All() []Tool {
	all := make([]Tool, 0, len(r.tools))
	for _, registered := range r.tools {
		all = append(all, registered.tool)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name() < all[j].Name() })
	return all
}

func (r *Registry) Catalog() (ToolCatalog, error) {
	if r == nil {
		return ToolCatalog{}, nil
	}
	catalog := ToolCatalog{Tools: make([]ToolDescriptor, 0, len(r.tools))}
	for _, registered := range r.tools {
		catalog.Tools = append(catalog.Tools, registered.descriptor)
	}
	sort.Slice(catalog.Tools, func(i, j int) bool { return catalog.Tools[i].Name < catalog.Tools[j].Name })
	return catalog, nil
}

func descriptorFor(tool Tool, meta ToolMetadata) (ToolDescriptor, error) {
	input, err := json.MarshalIndent(tool.Schema(), "", "  ")
	if err != nil {
		return ToolDescriptor{}, fmt.Errorf("marshal JSON schema for tool %q: %w", tool.Name(), err)
	}
	var schema map[string]any
	if json.Unmarshal(input, &schema) == nil && schema["type"] == "object" {
		schema["additionalProperties"] = false
		input, err = json.MarshalIndent(schema, "", "  ")
		if err != nil {
			return ToolDescriptor{}, fmt.Errorf("marshal strict JSON schema for tool %q: %w", tool.Name(), err)
		}
	}
	if meta.CanonicalName != tool.Name() {
		return ToolDescriptor{}, fmt.Errorf("tool metadata name %q does not match %q", meta.CanonicalName, tool.Name())
	}
	if meta.Family == "" || len(meta.CapabilityTags) == 0 {
		return ToolDescriptor{}, fmt.Errorf("tool %q requires family and capability metadata", tool.Name())
	}
	return ToolDescriptor{Name: tool.Name(), Description: tool.Description(), Family: meta.Family, CapabilityTags: append([]string(nil), meta.CapabilityTags...), InputSchema: input,
		Access: meta.Access, SideEffect: meta.SideEffect, SupportedModes: append([]ToolMode(nil), meta.SupportedModes...),
		Inspection: meta.Inspection, PersistCallObservation: meta.PersistCallObservation, RequiresApproval: meta.RequiresApproval, BatmanManifest: meta.BatmanManifest,
		ParallelSafe: meta.ParallelSafe,
		SchemaTokens: estimateSchemaTokens(input)}, nil
}

func mergeMetadata(base, override ToolMetadata) ToolMetadata {
	if override.CanonicalName != "" {
		base.CanonicalName = override.CanonicalName
	}
	if override.Family != "" {
		base.Family = override.Family
	}
	if len(override.CapabilityTags) > 0 {
		base.CapabilityTags = override.CapabilityTags
	}
	if override.Access != "" {
		base.Access = override.Access
	}
	if override.SideEffect != "" {
		base.SideEffect = override.SideEffect
	}
	if len(override.SupportedModes) > 0 {
		base.SupportedModes = override.SupportedModes
	}
	if override.ParallelSafe {
		base.ParallelSafe = true
	}
	if override.Family != "" {
		base.Inspection = override.Inspection
		base.PersistCallObservation = override.PersistCallObservation
		base.RequiresApproval = override.RequiresApproval
		base.BatmanManifest = override.BatmanManifest
		base.ParallelSafe = override.ParallelSafe
	}
	return base
}

func defaultToolMetadata(tool Tool) ToolMetadata {
	capabilities := tool.Capabilities()
	meta := ToolMetadata{CanonicalName: tool.Name(), Family: "workspace", CapabilityTags: []string{"workspace"}, Access: ToolAccessRead,
		SideEffect: ToolSideEffectNone, SupportedModes: []ToolMode{ToolModeNormal, ToolModeExecution, ToolModePlanning, ToolModeAudit, ToolModeExplore}}
	if capabilities&CapabilityWriteWorkspace != 0 {
		meta.Access, meta.SideEffect, meta.RequiresApproval = ToolAccessWrite, ToolSideEffectWorkspace, true
	}
	if capabilities&CapabilityExecuteProcess != 0 {
		meta.SideEffect = ToolSideEffectProcess
		if capabilities&CapabilityReadWorkspace == 0 || capabilities&CapabilityWriteWorkspace != 0 {
			meta.RequiresApproval = true
			meta.BatmanManifest = false
			if meta.Access == ToolAccessRead {
				meta.Access = ToolAccessDestructive
			}
		}
	}
	if capabilities&CapabilityUseNetwork != 0 {
		meta.SideEffect = ToolSideEffectNetwork
	}
	meta.Inspection = capabilities.InspectionOnly()
	meta.PersistCallObservation = meta.Inspection
	return meta
}

func estimateSchemaTokens(schema []byte) int { return (len([]rune(string(schema))) + 3) / 4 }
