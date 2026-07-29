//go:build !darwin

package ui

func nativeShiftPressed() bool {
	return false
}
