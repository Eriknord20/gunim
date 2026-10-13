package capture

import (
	"testing"
	"time"
)

// TestWindowsInputsListAndRecord lists the system's inputs, and records
// the default one for a moment where there is one: blocks come in, of
// the channels it has. A machine without inputs, as a build server,
// lists none.
func TestWindowsInputsListAndRecord(t *testing.T) {
	devs, err := Devices()
	if err != nil {
		t.Fatal(err)
	}
	def := false
	for _, d := range devs {
		t.Logf("%+v", d)
		def = def || d.Default
	}
	if !def {
		t.Skip("this machine has no default input")
	}
	in, err := Open(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	t.Logf("recording %+v", in.Info())
	buf := make([]float32, 4096*in.Info().Channels)
	got := 0
	deadline := time.Now().Add(2 * time.Second)
	for got < in.Info().Rate/10 && time.Now().Before(deadline) {
		b := in.Read(buf)
		if b.Len() == 0 {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		if b.Frame != int64(got) {
			t.Fatalf("a block came as frame %d after %d frames", b.Frame, got)
		}
		got += b.Len()
	}
	if got < in.Info().Rate/10 {
		t.Fatalf("recorded %d frames in 2 seconds", got)
	}
	if err := in.Err(); err != nil {
		t.Fatal(err)
	}
}
