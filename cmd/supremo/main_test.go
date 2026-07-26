package main

import "testing"

func TestVersionDefaultsToNonEmptyValue(t *testing.T) {
	if version == "" {
		t.Fatal("version must be set for --version output")
	}
}
