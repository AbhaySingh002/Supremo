package providers

import "testing"

func TestRuntimeCredentialConfigured(t *testing.T) {
	for _, test := range []struct {
		name string
		key  string
		want bool
	}{
		{name: "missing", key: "", want: false},
		{name: "placeholder", key: defaultGeminiAPIKey, want: false},
		{name: "padded placeholder", key: " " + defaultGeminiAPIKey + " ", want: false},
		{name: "real key", key: "configured-key", want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime := NewRuntimeConfig("gemini", "gemini-2.5-flash", "", test.key, nil)
			if got := runtime.CredentialConfigured(); got != test.want {
				t.Fatalf("CredentialConfigured() = %v, want %v", got, test.want)
			}
		})
	}
}
