package agent

import (
	"encoding/json"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
)

const (
	DefaultThresholdRatio = 0.80
	DefaultRetainRatio    = 0.16
	DefaultFallbackLimit  = 131_072
)

// EstimateTokens calculates estimated token count using Unicode runes ((runes + 3) / 4).
func EstimateTokens(value string) int {
	if value == "" {
		return 0
	}
	return (len([]rune(value)) + 3) / 4
}

// EstimateMessageTokens computes the token weight of a single model-visible message.
func EstimateMessageTokens(msg models.Message) int {
	tokens := 4 // base framing overhead
	tokens += EstimateTokens(msg.Content)
	tokens += EstimateTokens(msg.ToolCallID)
	tokens += EstimateTokens(msg.ToolName)
	for _, call := range msg.ToolCalls {
		tokens += EstimateTokens(call.ID)
		tokens += EstimateTokens(call.Name)
		tokens += EstimateTokens(string(call.Arguments))
		tokens += 4
	}
	return tokens
}

// NodeMeasurement represents the measured token weight of a single Surface node.
type NodeMeasurement struct {
	Seq    int64
	Tokens int
	Role   models.Role
	Msg    models.Message
}

// Measurement contains complete request pressure metrics.
type Measurement struct {
	HeaderTokens    int
	SurfaceTokens   int
	TotalTokens     int
	Nodes           []NodeMeasurement
	ContextLimit    int
	ThresholdTokens int
	RetainTokens    int
}

// IsPressured returns true if total request tokens reach or exceed the threshold.
func (m Measurement) IsPressured() bool {
	return m.TotalTokens >= m.ThresholdTokens
}

// TokenMeter calculates token pressure for a full request before sending to the model provider.
type TokenMeter interface {
	Measure(session *Session, prompt *models.Prompt, contextLimit int) Measurement
}

// DefaultTokenMeter is the canonical token pressure calculator.
type DefaultTokenMeter struct {
	ThresholdRatio float64
	RetainRatio    float64
	FallbackLimit  int
}

// NewDefaultTokenMeter creates a TokenMeter initialized with default ratios (0.80 threshold, 0.16 retain).
func NewDefaultTokenMeter() *DefaultTokenMeter {
	return &DefaultTokenMeter{
		ThresholdRatio: DefaultThresholdRatio,
		RetainRatio:    DefaultRetainRatio,
		FallbackLimit:  DefaultFallbackLimit,
	}
}

// Measure evaluates the header and surface messages to compute request pressure.
func (m *DefaultTokenMeter) Measure(session *Session, prompt *models.Prompt, contextLimit int) Measurement {
	limit := contextLimit
	if limit <= 0 {
		if m != nil && m.FallbackLimit > 0 {
			limit = m.FallbackLimit
		} else {
			limit = DefaultFallbackLimit
		}
	}

	threshRatio := DefaultThresholdRatio
	retainRatio := DefaultRetainRatio
	if m != nil {
		if m.ThresholdRatio > 0 {
			threshRatio = m.ThresholdRatio
		}
		if m.RetainRatio > 0 {
			retainRatio = m.RetainRatio
		}
	}

	headerTokens := 0
	if prompt != nil {
		headerTokens += EstimateTokens(prompt.System)
		for _, tool := range prompt.ToolDefinitions {
			raw, _ := json.Marshal(tool)
			headerTokens += EstimateTokens(string(raw)) + 4
		}
	}

	surfaceTokens := 0
	var nodes []NodeMeasurement
	if prompt != nil {
		for _, msg := range prompt.Messages {
			surfaceTokens += EstimateMessageTokens(msg)
		}
	}

	if session != nil {
		session.ensureSurface()
		for _, seq := range session.Nodes() {
			event, ok := session.eventBySeq(seq)
			if !ok {
				continue
			}
			msg, ok := deriveEventMessage(event)
			if !ok || msg == nil {
				continue
			}
			toks := EstimateMessageTokens(*msg)
			nodes = append(nodes, NodeMeasurement{
				Seq:    seq,
				Tokens: toks,
				Role:   msg.Role,
				Msg:    *msg,
			})
		}
	}
	if prompt == nil {
		for _, node := range nodes {
			surfaceTokens += node.Tokens
		}
	}

	total := headerTokens + surfaceTokens

	return Measurement{
		HeaderTokens:    headerTokens,
		SurfaceTokens:   surfaceTokens,
		TotalTokens:     total,
		Nodes:           nodes,
		ContextLimit:    limit,
		ThresholdTokens: int(float64(limit) * threshRatio),
		RetainTokens:    int(float64(limit) * retainRatio),
	}
}
