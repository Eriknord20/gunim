package audio

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// shortLast writes a FLAC file of n frames, to a file it can seek in.
func shortLast(t *testing.T, n int) (file []byte, samples []int32) {
	t.Helper()
	in := make([]int32, 2*n)
	for i := range in {
		in[i] = int32(i%20000) - 10000
	}
	var f bufFile
	w, err := NewFLACWriter(&f, 48000, 16, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.WriteInts(in); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if f.pos != len(f.b) {
		t.Fatalf("%d frames: Close left the file at %d of %d bytes", n, f.pos, len(f.b))
	}
	return f.b, in
}

// readsAll reads b both ways the toolkit reads FLAC, and wants in.
func readsAll(t *testing.T, name string, b []byte, in []int32) {
	t.Helper()
	got, err := DecodeFLACInts(bytes.NewReader(b), int64(len(in)/2))
	if err != nil {
		t.Fatalf("%s: read whole: %v", name, err)
	}
	if len(got.Samples) != len(in) {
		t.Fatalf("%s: read %d samples of %d", name, len(got.Samples), len(in))
	}
	for i := range in {
		if got.Samples[i] != in[i] {
			t.Fatalf("%s: sample %d is %d, wrote %d", name, i, got.Samples[i], in[i])
		}
	}
	_, floats := readBack(t, b)
	if len(floats) != len(in) {
		t.Fatalf("%s: opened %d samples of %d", name, len(floats), len(in))
	}
	for i := range in {
		if want := float32(in[i]) / (1 << 15); floats[i] != want {
			t.Fatalf("%s: sample %d opened as %v, want %v", name, i, floats[i], want)
		}
	}
}

// A last block under 16 frames, which the encoder would name as the file's
// minimum, leaves a file that opens, its minimum the blocks before it.
func TestAShortLastBlockLeavesAFLACThatOpens(t *testing.T) {
	for _, tc := range []struct{ frames, size int }{
		{flacBlock + 1, flacBlock},
		{flacBlock + 6, flacBlock},
		{flacBlock + 15, flacBlock},
		{flacBlock + 16, flacBlock},
		{3*flacBlock + 6, flacBlock},
		{2 * flacBlock, flacBlock},
		{100, 100},
		{6, flacFewest},
	} {
		b, in := shortLast(t, tc.frames)
		if got := int(binary.BigEndian.Uint16(b[8:])); got != tc.size {
			t.Errorf("%d frames: the header's minimum block is %d, want %d", tc.frames, got, tc.size)
		}
		if got := int(binary.BigEndian.Uint16(b[10:])); got != tc.size {
			t.Errorf("%d frames: the header's maximum block is %d, want %d", tc.frames, got, tc.size)
		}
		readsAll(t, "written", b, in)
	}
}

// A file an older writer left, its header naming a last block under 16 frames,
// still opens, whichever way it is read.
func TestAFLACNamingAShortLastBlockStillOpens(t *testing.T) {
	b, in := shortLast(t, flacBlock+6)
	old := bytes.Clone(b)
	binary.BigEndian.PutUint16(old[8:], 6)
	readsAll(t, "a short minimum", old, in)
	// A short file, both of whose sizes fall short.
	b, in = shortLast(t, 6)
	old = bytes.Clone(b)
	binary.BigEndian.PutUint16(old[8:], 6)
	binary.BigEndian.PutUint16(old[10:], 6)
	readsAll(t, "both short", old, in)
}

func TestHealingLeavesWhatIsNoFLACAlone(t *testing.T) {
	in := []byte("RIFF\x00\x00\x00\x00\x00\x06\x00\x06rest")
	if healFLACSizes(bytes.Clone(in)) {
		t.Fatal("healed what is no FLAC")
	}
	var out bytes.Buffer
	if _, err := out.ReadFrom(HealFLAC(bytes.NewReader([]byte("fLa")))); err != nil || out.String() != "fLa" {
		t.Fatalf("a stream too short to heal reads %q, %v", out.String(), err)
	}
}
