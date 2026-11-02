//go:build ((darwin && !ios) || (linux && !android)) && (amd64 || arm64) && !race

package audiounit

import "testing"

// What the pump calls each tick, and the mixer each time it asks for
// the frame heard, allocates nothing.
func TestPumpingAllocatesNothing(t *testing.T) {
	f := newSystem(speakers, mic)
	cl := openFake(t, f, Config{Record: true})
	frames := ramp(0, bufFrames)
	in := [][]float32{make([]float32, bufFrames), make([]float32, bufFrames)}
	dst := make([]float32, 2*bufFrames)
	cl.Write(frames)
	f.render(t, bufFrames, 2, start)
	f.record(t, in, bufFrames, start, 0)
	allocs := testing.AllocsPerRun(100, func() {
		cl.Write(frames)
		_, _ = cl.Heard()
		_, _, _ = cl.Read(dst)
		_ = cl.Poll()
	})
	if allocs != 0 {
		t.Errorf("a tick of the pump allocates %v times", allocs)
	}
}
