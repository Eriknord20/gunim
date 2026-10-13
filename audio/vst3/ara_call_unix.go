//go:build !windows

package vst3

import "github.com/ebitengine/purego"

// callProgress calls the host's notifyAudioSourceAnalysisProgress,
// whose fourth argument is a float.
func callProgress(model iface, src uintptr, state int32, v float32) {
	var g func(ctl, src uintptr, state int32, v float32)
	purego.RegisterFunc(&g, *ptr[uintptr](model.table + uintptr(ptrSize)))
	g(model.ref, src, state, v)
}

// callLocate calls the host's requestSetPlaybackPosition.
func callLocate(pb iface, at float64) {
	var g func(ctl uintptr, at float64)
	purego.RegisterFunc(&g, *ptr[uintptr](pb.table + uintptr(3*ptrSize)))
	g(pb.ref, at)
}

// callCycle calls the host's requestSetCycleRange.
func callCycle(pb iface, at, length float64) {
	var g func(ctl uintptr, at, length float64)
	purego.RegisterFunc(&g, *ptr[uintptr](pb.table + uintptr(4*ptrSize)))
	g(pb.ref, at, length)
}
