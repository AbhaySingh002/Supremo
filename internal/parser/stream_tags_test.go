package parser

import (
	"strings"
	"testing"
)

func TestStreamTagParserExtractsThoughtsAndStatus(t *testing.T) {
	type event struct {
		tag      string
		delta    string
		complete bool
	}
	var events []event
	parser := NewStreamTagParser(func(tag, delta string, complete bool) {
		events = append(events, event{tag: tag, delta: delta, complete: complete})
	})

	chunks := []string{
		"Hello! <status>Inspecting ",
		"git repo</status> Now let me think: ",
		"<thought>Analyzing git status",
		" and branch state</thought> Here is the answer.",
	}

	for _, chunk := range chunks {
		parser.Feed(chunk)
	}
	parser.Flush()

	var thoughts strings.Builder
	var status strings.Builder
	var messages strings.Builder
	thoughtDone := false
	statusDone := false

	for _, ev := range events {
		if ev.complete {
			if ev.tag == "thought" {
				thoughtDone = true
			}
			if ev.tag == "status" {
				statusDone = true
			}
			continue
		}
		switch ev.tag {
		case "thought":
			thoughts.WriteString(ev.delta)
		case "status":
			status.WriteString(ev.delta)
		case "message":
			messages.WriteString(ev.delta)
		}
	}

	if status.String() != "Inspecting git repo" || !statusDone {
		t.Fatalf("status = %q, done = %t", status.String(), statusDone)
	}
	if thoughts.String() != "Analyzing git status and branch state" || !thoughtDone {
		t.Fatalf("thoughts = %q, done = %t", thoughts.String(), thoughtDone)
	}
	expectedMessage := "Hello!  Now let me think:  Here is the answer."
	if messages.String() != expectedMessage {
		t.Fatalf("messages = %q, want %q", messages.String(), expectedMessage)
	}
}

func TestStreamTagParserPassesThroughUnknownTags(t *testing.T) {
	var collected strings.Builder
	parser := NewStreamTagParser(func(tag, delta string, complete bool) {
		if !complete && tag == "message" {
			collected.WriteString(delta)
		}
	})

	parser.Feed("Here is <div>custom html</div> text")
	parser.Flush()

	if collected.String() != "Here is <div>custom html</div> text" {
		t.Fatalf("collected = %q", collected.String())
	}
}

func TestStreamTagParserHandlesThinkingAlias(t *testing.T) {
	var thought strings.Builder
	done := false
	parser := NewStreamTagParser(func(tag, delta string, complete bool) {
		if complete && tag == "thought" {
			done = true
			return
		}
		if tag == "thought" {
			thought.WriteString(delta)
		}
	})

	parser.Feed("<thinking>Deep reasoning</thinking>")
	parser.Flush()

	if thought.String() != "Deep reasoning" || !done {
		t.Fatalf("thought = %q, done = %t", thought.String(), done)
	}
}
