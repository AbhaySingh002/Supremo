package logging

import (
	"strings"
	"testing"
)

func TestRedactSecrets(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "bearer token",
			input:    "Authorization: Bearer secret_token_1234567890abcdef",
			expected: "Authorization: Bearer [REDACTED]",
		},
		{
			name:     "openai sk key",
			input:    "Request failed with key sk-1234567890abcdef1234567890",
			expected: "Request failed with key sk-[REDACTED]",
		},
		{
			name:     "api key query param",
			input:    "https://api.example.com/v1?api_key=my_super_secret_key&mode=chat",
			expected: "https://api.example.com/v1?api_key=[REDACTED]&mode=chat",
		},
		{
			name:     "password field",
			input:    "Connecting with password: secret_password_value",
			expected: "Connecting with password=[REDACTED]",
		},
		{
			name:     "clean string",
			input:    "Session started normally for workspace /home/user/project",
			expected: "Session started normally for workspace /home/user/project",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := Redact(tc.input)
			if !strings.Contains(result, "[REDACTED]") && strings.Contains(tc.expected, "[REDACTED]") {
				t.Errorf("expected redaction in %q, got %q", tc.input, result)
			}
			if strings.Contains(result, "secret_token_1234567890abcdef") || strings.Contains(result, "1234567890abcdef1234567890") || strings.Contains(result, "my_super_secret_key") || strings.Contains(result, "secret_password_value") {
				t.Errorf("leaked sensitive data in result: %q", result)
			}
		})
	}
}
