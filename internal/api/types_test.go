package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestVersionOneReceiptAndEventFixtures(t *testing.T) {
	response, err := json.Marshal(RPCResponse{V: Version, ID: "req-1", OK: true, Result: Receipt{
		Accepted: true, RunID: "run-1", MessageID: "message-1", AcceptedCursor: 9,
	}})
	if err != nil {
		t.Fatal(err)
	}
	wantResponse := `{"v":1,"id":"req-1","ok":true,"result":{"accepted":true,"run_id":"run-1","message_id":"message-1","accepted_cursor":9}}`
	if string(response) != wantResponse {
		t.Fatalf("receipt fixture = %s", response)
	}
	event, err := json.Marshal(Event{V: Version, Cursor: 10, EventID: "event-10", Type: "run/end", Durable: true,
		Time: time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC), SessionID: "chat", RunID: "run-1", Data: json.RawMessage(`{"status":"completed"}`)})
	if err != nil {
		t.Fatal(err)
	}
	wantEvent := `{"v":1,"cursor":10,"event_id":"event-10","type":"run/end","durable":true,"ignorable":false,"time":"2026-08-25T12:00:00Z","session_id":"chat","run_id":"run-1","data":{"status":"completed"}}`
	if string(event) != wantEvent {
		t.Fatalf("event fixture = %s", event)
	}
}

func TestFrontendResponsesCannotSerializeCredentials(t *testing.T) {
	value := InitializeResult{APIVersion: Version, Provider: "openai", Endpoint: "http://localhost", CredentialReady: true, Providers: []Provider{{ID: "openai", Configured: true}}}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"api_key", "embedding_key", "credential_provider"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("response exposed credential field %q: %s", forbidden, data)
		}
	}
}
