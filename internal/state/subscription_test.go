package state

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestSubscriptionPublishesOnlyCommittedEventsInOrder(t *testing.T) {
	store, _ := openTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := store.SaveSession(ctx, SessionInput{ID: "chat", Name: "Chat", Data: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	cursor, _ := store.Cursor(ctx)
	sub, err := store.SubscribeEvents(ctx, EventQuery{SessionID: "chat", After: cursor}, 8, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	if _, err := store.AppendMessage(ctx, MessageInput{
		ID: "message", SessionID: "chat", Role: "user", Parts: []MessagePartInput{{Kind: "text", Text: "hello"}},
		RelatedEvents: []EventInput{{Type: "user/message", Payload: map[string]string{"content": "hello"}}},
	}); err != nil {
		t.Fatal(err)
	}
	first, second := <-sub.Events, <-sub.Events
	if first.Type != "user.message.created" || second.Type != "user/message" || first.Sequence >= second.Sequence {
		t.Fatalf("events out of order: %#v %#v", first, second)
	}
	if _, err := store.AppendMessage(ctx, MessageInput{
		ID: "rollback", SessionID: "chat", Role: "user", Parts: []MessagePartInput{{Kind: "text", Text: "no"}},
		RelatedEvents: []EventInput{{Type: ""}},
	}); err == nil {
		t.Fatal("expected invalid related event to roll back")
	}
	select {
	case event := <-sub.Events:
		t.Fatalf("rolled-back event was published: %#v", event)
	case <-time.After(30 * time.Millisecond):
	}
}

func TestSlowSubscriptionRequiresResync(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	sub, err := store.SubscribeEvents(ctx, EventQuery{}, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	if _, err := store.SaveSession(ctx, SessionInput{ID: "chat", Name: "Chat", Data: json.RawMessage(`{}`), RelatedEvents: []EventInput{{Type: "extra", Payload: map[string]bool{"ok": true}}}}); err != nil {
		t.Fatal(err)
	}
	for range sub.Events {
	}
	if !errors.Is(sub.Err(), ErrResyncRequired) {
		t.Fatalf("subscription error = %v", sub.Err())
	}
}

func TestSessionSnapshotUsesOneCursorBoundary(t *testing.T) {
	store, _ := openTestStore(t)
	ctx := context.Background()
	if _, err := store.SaveSession(ctx, SessionInput{ID: "chat", Name: "Chat", Data: json.RawMessage(`{"id":"chat"}`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendMessage(ctx, MessageInput{ID: "message", SessionID: "chat", Role: "assistant", Parts: []MessagePartInput{{Kind: "text", Text: "done"}}}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.SessionSnapshot(ctx, "chat")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 1 || snapshot.Messages[0].Parts[0].Text != "done" || len(snapshot.Events) != 2 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	for _, event := range snapshot.Events {
		if event.Sequence > snapshot.Cursor {
			t.Fatalf("event %d is newer than cursor %d", event.Sequence, snapshot.Cursor)
		}
	}
}
