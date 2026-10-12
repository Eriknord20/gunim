package plug

import "testing"

func TestATraceKeepsTheLeastOfEachRunForTheFramesPlayed(t *testing.T) {
	var tr Trace
	if _, ok := tr.Get(0); ok {
		t.Fatal("a trace of nothing played has a value")
	}
	for f := range int64(3 * TraceFrames) {
		v := float32(0)
		if f == TraceFrames+7 {
			v = -6
		}
		tr.Put(1000*TraceFrames+f, v)
	}
	if v, ok := tr.Get(1000*TraceFrames + TraceFrames + 100); !ok || v != -6 {
		t.Errorf("the run with -6 in it reads %v, %v", v, ok)
	}
	if v, ok := tr.Get(1000*TraceFrames + 5); !ok || v != 0 {
		t.Errorf("the run before reads %v, %v", v, ok)
	}
	if _, ok := tr.Get(1003 * TraceFrames); ok {
		t.Error("a run not yet played has a value")
	}
	// A run as long ago as the ring is round is forgotten, not misread.
	tr.Put((1000+traceRuns)*TraceFrames, -3)
	if _, ok := tr.Get(1000 * TraceFrames); ok {
		t.Error("a run a ring ago reads the value of the run now in its place")
	}
	if _, ok := tr.Get(-1); ok {
		t.Error("a frame before the first has a value")
	}
}
