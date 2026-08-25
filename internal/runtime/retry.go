package runtime

import (
	"errors"
	"fmt"
	"time"

	"github.com/AbhaySingh002/supremo/internal/parser/models"
	"github.com/AbhaySingh002/supremo/internal/providers"
)

// RetryEvent carries state about an attempt and the resulting error or completion.
type RetryEvent struct {
	Attempt        int
	Err            error
	RetriesEnabled bool
}

// RetryDecision specifies whether to retry and its backoff.
type RetryDecision struct {
	ShouldRetry     bool
	Delay           time.Duration
	ProgressMessage string
}

// RetryPolicy defines the decision contract for handling provider and validation errors during a step.
type RetryPolicy interface {
	Decide(event RetryEvent, prompt *models.Prompt) RetryDecision
}

// DefaultRetryPolicy implements canonical retry backoff and Retry-After parsing.
type DefaultRetryPolicy struct {
	Delays []time.Duration
}

// NewDefaultRetryPolicy creates a DefaultRetryPolicy with canonical backoff delays.
func NewDefaultRetryPolicy(delays ...time.Duration) *DefaultRetryPolicy {
	if len(delays) == 0 {
		delays = []time.Duration{
			1 * time.Second,
			5 * time.Second,
			10 * time.Second,
			30 * time.Second,
			60 * time.Second,
		}
	}
	return &DefaultRetryPolicy{
		Delays: delays,
	}
}

func (p *DefaultRetryPolicy) Decide(event RetryEvent, prompt *models.Prompt) RetryDecision {
	if event.Err == nil {
		return RetryDecision{}
	}

	// Only infrastructure failures retry. Model text and malformed tool
	// arguments are normal loop inputs, not reasons for a repair request.
	if !event.RetriesEnabled || !providers.IsTransient(event.Err) {
		return RetryDecision{ShouldRetry: false}
	}

	delays := p.Delays
	if len(delays) == 0 {
		delays = []time.Duration{1 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second, 60 * time.Second}
	}

	if event.Attempt >= len(delays) {
		return RetryDecision{ShouldRetry: false}
	}

	delay := delays[event.Attempt]
	var failure *providers.ProviderFailure
	if errors.As(event.Err, &failure) && failure.RetryAfter > 0 {
		delay = failure.RetryAfter
	}

	return RetryDecision{
		ShouldRetry:     true,
		Delay:           delay,
		ProgressMessage: "Provider is temporarily unavailable — retrying in " + RetryDelayLabel(delay) + "…",
	}
}

// RetryDelayLabel formats a duration for progress reporting.
func RetryDelayLabel(d time.Duration) string {
	if d%time.Minute == 0 {
		mins := int(d / time.Minute)
		if mins == 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", mins)
	}
	secs := int(d.Round(time.Second) / time.Second)
	if secs == 1 {
		return "1s"
	}
	return fmt.Sprintf("%ds", secs)
}
