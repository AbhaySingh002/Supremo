//go:build darwin

package ui

import "github.com/ebitengine/purego"

const coreGraphicsPath = "/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics"

var cgEventSourceFlagsState func(int32) uint64

func init() {
	coreGraphics, err := purego.Dlopen(coreGraphicsPath, purego.RTLD_LAZY)
	if err == nil {
		purego.RegisterLibFunc(&cgEventSourceFlagsState, coreGraphics, "CGEventSourceFlagsState")
	}
}

func nativeShiftPressed() bool {
	const shiftMask = uint64(1 << 17)
	return cgEventSourceFlagsState != nil && cgEventSourceFlagsState(0)&shiftMask != 0
}
