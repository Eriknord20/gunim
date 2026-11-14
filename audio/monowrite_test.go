package audio

import (
	"bytes"
	"math"
	"testing"
)

// monoTestSound is n frames of one channel: a sine louder than full
// scale at its peaks.
func monoTestSound(n int) []float32 {
	in := make([]float32, n)
	for i := range in {
		in[i] = float32(1.1 * math.Sin(2*math.Pi*440*float64(i)/48000))
	}
	return in
}

// writeMonoInPieces writes in to w in pieces of 1000 frames and closes
// it.
func writeMonoInPieces(t *testing.T, w interface {
	Write([]float32) error
	Close() error
}, in []float32) {
	t.Helper()
	for len(in) > 0 {
		k := min(len(in), 1000)
		if err := w.Write(in[:k]); err != nil {
			t.Fatal(err)
		}
		in = in[k:]
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

// checkMono reads a written file back and checks it holds one channel
// of the frames in, each within an LSB and a half of bits, integer
// samples clipped to full scale.
func checkMono(t *testing.T, name string, b []byte, in []float32, bits int) {
	t.Helper()
	src, format, err := DecodeNative(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if format.Channels != 1 || format.Bits != bits || format.SampleRate != 48000 {
		t.Fatalf("%s: read back as %+v, want 1 channel of %d bits at 48000 Hz", name, format, bits)
	}
	if src.Len() != int64(len(in)) {
		t.Fatalf("%s: %d frames, want %d", name, src.Len(), len(in))
	}
	out := make([]float32, 2*len(in))
	n := 0
	for n < len(in) {
		k, err := src.Read(out[2*n:])
		n += k
		if err != nil || k == 0 {
			break
		}
	}
	if n != len(in) {
		t.Fatalf("%s: read %d frames, want %d", name, n, len(in))
	}
	lsb := 1.5 / float64(int64(1)<<(bits-1))
	if bits == 32 {
		lsb = 1e-7
	}
	for i, v := range in {
		// Integer samples clip to full scale; float ones keep it all.
		want := float64(v)
		if bits != 32 {
			want = math.Max(-1, math.Min(1, want))
		}
		// A mono file plays the same in both channels.
		l, r := float64(out[2*i]), float64(out[2*i+1])
		if math.Abs(l-want) > lsb || l != r {
			t.Fatalf("%s: frame %d reads %v, %v; want %v", name, i, l, r, want)
		}
	}
}

// TestAMonoWAVHoldsOneChannel writes one channel to a WAV file at each
// depth, and reads back a one-channel file of the same samples, with
// the clipping counted.
func TestAMonoWAVHoldsOneChannel(t *testing.T) {
	in := monoTestSound(3*48000/10 + 7)
	for _, bits := range []int{16, 24, 32} {
		var f bufFile
		w, err := NewWAVWriterChannels(&f, 48000, bits, 1, false)
		if err != nil {
			t.Fatal(err)
		}
		w.Tags = []WAVTag{{ID: "INAM", Value: "Mono"}}
		writeMonoInPieces(t, w, in)
		checkMono(t, "wav", f.b, in, bits)
		if bits != 32 && w.Clipped == 0 {
			t.Fatalf("%d bits: no samples counted as clipped", bits)
		}
	}
}

// TestAMonoFLACHoldsOneChannelWithoutACountUpFront writes one channel
// to a FLAC file of unknown length, and reads back a one-channel file
// whose header counts its frames and checks out, with the samples
// exactly as a WAV of the same depth holds them.
func TestAMonoFLACHoldsOneChannelWithoutACountUpFront(t *testing.T) {
	for _, n := range []int{3*flacBlock + 1234, flacBlock, 10} {
		in := monoTestSound(n)
		for _, bits := range []int{16, 24} {
			var f bufFile
			w, err := NewFLACWriterChannels(&f, 48000, bits, 1, true)
			if err != nil {
				t.Fatal(err)
			}
			w.Tags = []FLACTag{{Name: "TITLE", Value: "Mono"}}
			writeMonoInPieces(t, w, in)
			checkMono(t, "flac", f.b, in, bits)

			var wf bufFile
			ww, err := NewWAVWriterChannels(&wf, 48000, bits, 1, true)
			if err != nil {
				t.Fatal(err)
			}
			writeMonoInPieces(t, ww, in)
			if w.Clipped != ww.Clipped {
				t.Fatalf("%d frames, %d bits: FLAC clipped %d, WAV %d", n, bits, w.Clipped, ww.Clipped)
			}
			ints, err := DecodeFLACInts(bytes.NewReader(f.b), int64(n))
			if err != nil {
				t.Fatalf("%d frames, %d bits: %v", n, bits, err)
			}
			if ints.Channels != 1 || len(ints.Samples) != n {
				t.Fatalf("%d frames, %d bits: %d channels, %d samples", n, bits, ints.Channels, len(ints.Samples))
			}
			// The WAV's samples follow its header, little-endian.
			data := wf.b[len(wf.b)-n*bits/8:]
			for i, v := range ints.Samples {
				var x int32
				if bits == 16 {
					x = int32(int16(uint16(data[2*i]) | uint16(data[2*i+1])<<8))
				} else {
					x = int32(uint32(data[3*i])<<8|uint32(data[3*i+1])<<16|uint32(data[3*i+2])<<24) >> 8
				}
				if v != x {
					t.Fatalf("%d frames, %d bits: sample %d is %d in the FLAC, %d in the WAV", n, bits, i, v, x)
				}
			}
		}
	}
}

// TestTheWritersTakeOnlyChannelsTheyCanWrite refuses a WAV of other
// than 1 or 2 channels and a FLAC of other than 1 to 8.
func TestTheWritersTakeOnlyChannelsTheyCanWrite(t *testing.T) {
	for _, c := range []int{0, 3} {
		var f bufFile
		if _, err := NewWAVWriterChannels(&f, 48000, 16, c, false); err == nil {
			t.Fatalf("a WAV of %d channels was taken", c)
		}
	}
	for _, c := range []int{0, 9} {
		var f bufFile
		if _, err := NewFLACWriterChannels(&f, 48000, 16, c, false); err == nil {
			t.Fatalf("a FLAC of %d channels was taken", c)
		}
	}
	for c := 1; c <= 8; c++ {
		var f bufFile
		if _, err := NewFLACWriterChannels(&f, 48000, 16, c, false); err != nil {
			t.Fatalf("a FLAC of %d channels: %v", c, err)
		}
	}
}
