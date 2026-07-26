package main

import "testing"

func TestActiveTaskOnlyAllowsControlCommands(t *testing.T) {
	for _, input := range []string{"/plan status", "/plan show", "/dry-run", "another task"} {
		if isActiveControl(input) {
			t.Fatalf("active task accepted %q", input)
		}
	}
	for _, input := range []string{"/approve", "/deny later", "/cancel", "/exit", "/help"} {
		if !isActiveControl(input) {
			t.Fatalf("active task rejected %q", input)
		}
	}
}
