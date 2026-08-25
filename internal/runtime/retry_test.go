package runtime

import (
	"testing"
	"time"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/providers"
)

func TestDefaultRetryPolicyRetriesOnlyTransientFailures(t *testing.T) {
	policy := NewDefaultRetryPolicy(time.Second)
	if got := policy.Decide(RetryEvent{Err: &providers.ProviderFailure{Code: providers.FailureRateLimit, RetryAfter: 3 * time.Second}, RetriesEnabled: true}, &models.Prompt{}); !got.ShouldRetry || got.Delay != 3*time.Second {
		t.Fatalf("rate-limit decision=%#v", got)
	}
	if got := policy.Decide(RetryEvent{Err: &providers.ProviderFailure{Code: providers.FailureEmptyResponse}, RetriesEnabled: true}, &models.Prompt{}); got.ShouldRetry {
		t.Fatalf("empty model response must not trigger repair: %#v", got)
	}
}
