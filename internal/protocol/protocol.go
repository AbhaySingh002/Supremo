// Package protocol names the small set of runtime prompt profiles. Provider
// tool calls and ordinary assistant text are the response protocol.
package protocol

import "fmt"

type Profile string

const (
	Conversational Profile = "conversational"
	Execution      Profile = "execution"
	SideAnswer     Profile = "side_answer"
)

func Valid(profile Profile) bool {
	switch profile {
	case "", Conversational, Execution, SideAnswer:
		return true
	default:
		return false
	}
}

func Validate(profile Profile) error {
	if !Valid(profile) {
		return fmt.Errorf("unknown prompt profile %q", profile)
	}
	return nil
}

func SWEProfile(profile Profile) bool { return profile == Execution }
