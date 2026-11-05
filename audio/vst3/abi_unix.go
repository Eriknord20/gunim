//go:build !windows && !js

package vst3

// comCompatible says the platform lays IDs out as COM GUIDs, as Windows
// does; elsewhere the bytes run as the numbers read.
const comCompatible = false

// The result codes, as the platforms other than Windows number them.
const (
	resultNoInterface    = int32(-1)
	resultInvalid        = int32(2)
	resultNotImplemented = int32(3)
)
