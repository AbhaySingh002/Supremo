package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

func openTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	t.Setenv("SUPREMO_DATA_DIR", t.TempDir())
	root := t.TempDir()
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = CloseWorkspace(root) })
	return store, root
}

func TestRestartRecoveryPreservesSessionsMessagesAndOrdering(t *testing.T) {
	ctx := context.Background()
	store, root := openTestStore(t)
	data := json.RawMessage(`{"id":"chat","name":"Chat"}`)
	if _, err := store.SaveSession(ctx, SessionInput{ID: "chat", Name: "Chat", Data: data}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendMessage(ctx, MessageInput{ID: "first", SessionID: "chat", Role: "user", Parts: []MessagePartInput{{Kind: "text", Text: "first"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendMessage(ctx, MessageInput{ID: "second", SessionID: "chat", Role: "assistant", Parts: []MessagePartInput{{Kind: "text", Text: "second"}}}); err != nil {
		t.Fatal(err)
	}
	if err := CloseWorkspace(root); err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.Session(ctx, "chat")
	if err != nil || session.Name != "Chat" {
		t.Fatalf("session after restart = %#v, %v", session, err)
	}
	messages, err := store.Messages(ctx, "chat", false)
	if err != nil || len(messages) != 2 || messages[0].Sequence != 1 || messages[1].Parts[0].Text != "second" {
		t.Fatalf("messages after restart = %#v, %v", messages, err)
	}
}

func TestEventsAreOrderedAndIdempotent(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	first, err := store.AppendEvent(ctx, EventInput{Type: "session.created", IdempotencyKey: "create:chat", Payload: map[string]string{"id": "chat"}})
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(first.ID) {
		t.Fatalf("event ID is not UUIDv4: %q", first.ID)
	}
	again, err := store.AppendEvent(ctx, EventInput{Type: "session.created", IdempotencyKey: "create:chat", Payload: map[string]string{"id": "chat"}})
	if err != nil || again.Sequence != first.Sequence {
		t.Fatalf("idempotent event = %#v, %v", again, err)
	}
	second, err := store.AppendEvent(ctx, EventInput{Type: "session.resumed"})
	if err != nil || second.Sequence <= first.Sequence {
		t.Fatalf("ordered event = %#v, %v", second, err)
	}
	events, err := store.Events(ctx, EventQuery{})
	if err != nil || len(events) != 2 {
		t.Fatalf("events = %#v, %v", events, err)
	}
}

func TestAppendMessageCommitsRelatedEventsAtomically(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	if _, err := store.SaveSession(ctx, SessionInput{ID: "chat", Name: "Chat", Data: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	before, err := store.Events(ctx, EventQuery{SessionID: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendMessage(ctx, MessageInput{
		ID: "atomic", SessionID: "chat", Role: "user", Parts: []MessagePartInput{{Kind: "text", Text: "hello"}},
		RelatedEvents: []EventInput{{SessionID: "chat", Type: "user/message", PayloadVersion: 2, Payload: json.RawMessage(`{"type":"user/message","version":2}`)}},
	}); err != nil {
		t.Fatal(err)
	}
	after, err := store.Events(ctx, EventQuery{SessionID: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before)+2 || after[len(after)-2].Type != "user.message.created" || after[len(after)-1].Type != "user/message" {
		t.Fatalf("atomic event pair = %#v", after)
	}

	if _, err := store.AppendMessage(ctx, MessageInput{
		ID: "rolled-back", SessionID: "chat", Role: "user", Parts: []MessagePartInput{{Kind: "text", Text: "nope"}},
		RelatedEvents: []EventInput{{SessionID: "chat"}},
	}); err == nil {
		t.Fatal("expected invalid related event to roll back the message")
	}
	messages, err := store.Messages(ctx, "chat", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].ID != "atomic" {
		t.Fatalf("rolled-back message leaked into projection: %#v", messages)
	}
	finalEvents, err := store.Events(ctx, EventQuery{SessionID: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	if len(finalEvents) != len(after) {
		t.Fatalf("rolled-back events leaked: before=%d after=%d", len(after), len(finalEvents))
	}
}

func TestSaveSessionCommitsRelatedEventsAtomically(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	if _, err := store.SaveSession(ctx, SessionInput{
		ID: "child", Name: "Child", Data: json.RawMessage(`{"id":"child"}`),
		RelatedEvents: []EventInput{
			{Type: "subagent/descriptor", PayloadVersion: 2, Payload: json.RawMessage(`{"type":"subagent/descriptor","version":2}`)},
			{Type: "subagent/message.queued", PayloadVersion: 2, Payload: json.RawMessage(`{"type":"subagent/message.queued","version":2}`)},
		},
	}); err != nil {
		t.Fatal(err)
	}
	events, err := store.Events(ctx, EventQuery{SessionID: "child"})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].Type != "session.created" || events[1].Type != "subagent/descriptor" || events[2].Type != "subagent/message.queued" {
		t.Fatalf("atomic child creation events = %#v", events)
	}

	if _, err := store.SaveSession(ctx, SessionInput{
		ID: "rolled-back-child", Name: "Child",
		RelatedEvents: []EventInput{{SessionID: "other", Type: "subagent/descriptor"}},
	}); err == nil {
		t.Fatal("expected mismatched related event to roll back child creation")
	}
	if _, err := store.Session(ctx, "rolled-back-child"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("rolled-back child exists: %v", err)
	}
}

func TestOptimisticSessionVersionsRejectStaleWrites(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	first, err := store.SaveSession(ctx, SessionInput{ID: "chat", Name: "First", Data: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveSession(ctx, SessionInput{ID: "chat", Name: "Second", Data: json.RawMessage(`{}`), ExpectedVersion: first.Version}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveSession(ctx, SessionInput{ID: "chat", Name: "Stale", Data: json.RawMessage(`{}`), ExpectedVersion: first.Version}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale session write = %v, want %v", err, ErrConflict)
	}
	session, err := store.Session(ctx, "chat")
	if err != nil || session.Name != "Second" || session.Version != 2 {
		t.Fatalf("conflicted session changed projection: %#v, %v", session, err)
	}
}

func TestRebuildRestoresSessionMessageAndWorkspaceProjections(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	if _, err := store.SaveSession(ctx, SessionInput{ID: "chat", Name: "Chat", Data: json.RawMessage(`{"id":"chat"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendMessage(ctx, MessageInput{ID: "message", SessionID: "chat", Role: "user", Parts: []MessagePartInput{{Kind: "text", Text: "keep"}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.ArchiveMessages(ctx, "chat"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetWorkspaceMemory(ctx, "durable context"); err != nil {
		t.Fatal(err)
	}
	if err := store.ArchiveSession(ctx, "chat"); err != nil {
		t.Fatal(err)
	}
	if err := store.RebuildCurrentState(ctx); err != nil {
		t.Fatal(err)
	}
	session, err := store.Session(ctx, "chat")
	if err != nil || session.Status != "archived" {
		t.Fatalf("rebuilt session = %#v, %v", session, err)
	}
	messages, err := store.Messages(ctx, "chat", true)
	if err != nil || len(messages) != 1 || messages[0].State != "archived" || messages[0].Parts[0].Text != "keep" {
		t.Fatalf("rebuilt messages = %#v, %v", messages, err)
	}
	memory, err := store.WorkspaceMemory(ctx)
	if err != nil || memory != "durable context" {
		t.Fatalf("rebuilt workspace memory = %q, %v", memory, err)
	}
}

func TestClaimsDocumentsAndFilesKeepHistoricalVersions(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	claim, err := store.CreateClaim(ctx, ClaimInput{ID: "requirement-v1", Kind: "requirement", Statement: "Use SQLite"})
	if err != nil {
		t.Fatal(err)
	}
	newer, err := store.SupersedeClaim(ctx, claim.ID, ClaimInput{ID: "requirement-v2", Statement: "Use SQLite with WAL"})
	if err != nil || newer.SupersedesID != claim.ID {
		t.Fatalf("superseded claim = %#v, %v", newer, err)
	}
	current, err := store.Claims(ctx, "requirement", false)
	if err != nil || len(current) != 1 || current[0].ID != newer.ID {
		t.Fatalf("current claims = %#v, %v", current, err)
	}
	if _, err := store.SaveDocument(ctx, DocumentInput{ID: "decision", Kind: "decision", Status: "accepted", Payload: json.RawMessage(`{"value":"first"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveDocument(ctx, DocumentInput{ID: "decision", Kind: "decision", Status: "superseded", Payload: json.RawMessage(`{"value":"second"}`), ExpectedVersion: 1}); err != nil {
		t.Fatal(err)
	}
	if err := store.RebuildCurrentState(ctx); err != nil {
		t.Fatal(err)
	}
	decision, err := store.Document(ctx, "decision", "decision")
	if err != nil || decision.Version != 2 || string(decision.Payload) != `{"value":"second"}` {
		t.Fatalf("rebuilt decision = %#v, %v", decision, err)
	}
	for _, data := range [][]byte{[]byte("A"), []byte("B"), []byte("C")} {
		if _, err := store.ObserveFile(ctx, FileObservation{Path: "main.go", Data: data}); err != nil {
			t.Fatal(err)
		}
	}
	versions, err := store.FileVersions(ctx, "main.go")
	if err != nil || len(versions) != 3 || versions[2].Hash == versions[1].Hash {
		t.Fatalf("file versions = %#v, %v", versions, err)
	}
	if err := store.RenameFile(ctx, FileRename{OldPath: "main.go", NewPath: "cmd/main.go"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ObserveFile(ctx, FileObservation{Path: "cmd/main.go", Data: []byte("D")}); err != nil {
		t.Fatal(err)
	}
	versions, err = store.FileVersions(ctx, "cmd/main.go")
	if err != nil || len(versions) != 4 || versions[3].FileID != versions[0].FileID {
		t.Fatalf("renamed file lineage = %#v, %v", versions, err)
	}
}

func TestArtifactsDeduplicateAndFailedMutationLeavesNoProjection(t *testing.T) {
	store, root := openTestStore(t)
	ctx := context.Background()
	first, err := store.PutArtifact(ctx, ArtifactInput{Data: []byte("same"), ContentType: "text/plain", Origin: "test"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.PutArtifact(ctx, ArtifactInput{Data: []byte("same"), ContentType: "text/plain", Origin: "test"})
	if err != nil || first.Hash != second.Hash {
		t.Fatalf("deduplicated artifact = %#v, %v", second, err)
	}
	if _, err := os.Stat(filepath.Join(store.objects, first.Hash[:2], first.Hash)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".supremo")); !os.IsNotExist(err) {
		t.Fatal("expected no .supremo folder in repository root")
	}
	if _, err := store.SaveDocument(ctx, DocumentInput{ID: "bad", Kind: "decision", Payload: json.RawMessage("not-json")}); err == nil {
		t.Fatal("invalid document payload was accepted")
	}
	if _, err := store.Document(ctx, "decision", "bad"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("failed mutation left a projection: %v", err)
	}
}

func TestDeleteDocumentRemovesVersionsAndRecordsAuditEvent(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	first, err := store.SaveDocument(ctx, DocumentInput{ID: "plan", Kind: "task", SessionID: "chat", Payload: json.RawMessage(`{"objective":"first"}`)})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.SaveDocument(ctx, DocumentInput{ID: "plan", Kind: "task", SessionID: "chat", Payload: json.RawMessage(`{"objective":"second"}`), ExpectedVersion: first.Version})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteDocument(ctx, DocumentDeleteInput{ID: "plan", Kind: "task"}); err == nil {
		t.Fatal("delete without a current version was accepted")
	}
	if err := store.DeleteDocument(ctx, DocumentDeleteInput{ID: "plan", Kind: "task", ExpectedVersion: first.Version}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale delete = %v, want %v", err, ErrConflict)
	}
	if _, err := store.Document(ctx, "task", "plan"); err != nil {
		t.Fatalf("stale delete removed document: %v", err)
	}
	if err := store.DeleteDocument(ctx, DocumentDeleteInput{ID: "plan", Kind: "task", ExpectedVersion: second.Version, Event: EventInput{Type: "plan.deleted"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Document(ctx, "task", "plan"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted document = %v", err)
	}
	if documents, err := store.Documents(ctx, "task", "chat"); err != nil || len(documents) != 0 {
		t.Fatalf("remaining documents = %#v, %v", documents, err)
	}
	events, err := store.Events(ctx, EventQuery{SessionID: "chat", Type: "plan.deleted"})
	if err != nil || len(events) != 1 {
		t.Fatalf("delete event = %#v, %v", events, err)
	}
}

func BenchmarkAppendEvents(b *testing.B) {
	root := b.TempDir()
	store, err := Open(root)
	if err != nil {
		b.Fatal(err)
	}
	defer CloseWorkspace(root)
	ctx := context.Background()
	for index := 0; b.Loop(); index++ {
		if _, err := store.AppendEvent(ctx, EventInput{Type: "benchmark.event", IdempotencyKey: "benchmark-" + strconv.Itoa(index)}); err != nil {
			b.Fatal(err)
		}
	}
}
