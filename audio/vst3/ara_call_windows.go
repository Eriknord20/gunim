package vst3

import "math"

// callProgress calls the host's notifyAudioSourceAnalysisProgress,
// whose fourth argument is a float: the system's call puts each of the
// first four arguments in its XMM register too.
func callProgress(model iface, src uintptr, state int32, v float32) {
	_, _ = nativeCall(*ptr[uintptr](model.table + uintptr(ptrSize)), model.ref, src, uintptr(uint32(state)),
		uintptr(math.Float32bits(v)))
}

// callLocate calls the host's requestSetPlaybackPosition.
func callLocate(pb iface, at float64) {
	_, _ = nativeCall(*ptr[uintptr](pb.table + uintptr(3*ptrSize)), pb.ref, uintptr(math.Float64bits(at)))
}

// callCycle calls the host's requestSetCycleRange.
func callCycle(pb iface, at, length float64) {
	_, _ = nativeCall(*ptr[uintptr](pb.table + uintptr(4*ptrSize)), pb.ref, uintptr(math.Float64bits(at)),
		uintptr(math.Float64bits(length)))
}
