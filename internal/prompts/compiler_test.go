package prompts

import (
	"strings"
	"testing"

	"github.com/AbhaySingh002/supremo/internal/protocol"
)

func TestProductionProfilesCompileWithoutResponseEnvelope(t *testing.T) {
	for _, profile := range []protocol.Profile{protocol.Conversational, protocol.Execution, protocol.SideAnswer} {
		compiled, err := Compile(profile)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"final_answer", "need_context", "need_tool", "Response Protocol", "one JSON object"} {
			if strings.Contains(compiled.Control, forbidden) {
				t.Fatalf("%s still contains %q", profile, forbidden)
			}
		}
	}
}

func TestPlanModePolicyContainsCoreRules(t *testing.T) {
	for _, required := range []string{"Plan Mode", "ask_user_question", "exit_plan_mode", "Do not implement"} {
		if !strings.Contains(PlanModePolicy, required) {
			t.Fatalf("PlanModePolicy missing %q", required)
		}
	}
}
