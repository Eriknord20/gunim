package audio

import (
	"bytes"
	"crypto/md5"
	"errors"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/mewkiz/flac"
	"github.com/mewkiz/flac/meta"
)

// flacTestSound is three blocks of a sine and a short fourth, louder
// than full scale at its peaks, in the left and a quieter one in the
// right.
func flacTestSound() []float32 {
	n := 3*flacBlock + 1234
	in := make([]float32, 2*n)
	for i := range n {
		in[2*i] = float32(1.1 * math.Sin(2*math.Pi*440*float64(i)/48000))
		in[2*i+1] = float32(0.3 * math.Sin(2*math.Pi*660*float64(i)/48000))
	}
	return in
}

// writeInPieces writes in to w in pieces of 1000 frames and closes it.
func writeInPieces(t *testing.T, w interface {
	Write([]float32) error
	Close() error
}, in []float32) {
	t.Helper()
	for len(in) > 0 {
		k := min(len(in), 2*1000)
		if err := w.Write(in[:k]); err != nil {
			t.Fatal(err)
		}
		in = in[k:]
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

// readBack decodes a file at its own rate and returns its format and
// frames.
func readBack(t *testing.T, b []byte) (format Format, out []float32) {
	t.Helper()
	src, format, err := DecodeNative(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]float32, 2*1500)
	for {
		n, err := src.Read(buf)
		out = append(out, buf[:2*n]...)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if src.Len() != int64(len(out)/2) {
		t.Fatalf("the file says %d frames and holds %d", src.Len(), len(out)/2)
	}
	return format, out
}

// TestAFLACHoldsTheSamplesOfAWAV writes the same sound to a FLAC file
// and a WAV file at 16 and 24 bits, with dither and without, and reads
// back the same samples from both, the same clipping counted, and the
// FLAC file the smaller.
func TestAFLACHoldsTheSamplesOfAWAV(t *testing.T) {
	in := flacTestSound()
	for _, bits := range []int{16, 24} {
		for _, dither := range []bool{false, true} {
			var ff, wf bufFile
			fw, err := NewFLACWriter(&ff, 48000, bits, dither)
			if err != nil {
				t.Fatal(err)
			}
			ww, err := NewWAVWriter(&wf, 48000, bits, dither)
			if err != nil {
				t.Fatal(err)
			}
			writeInPieces(t, fw, in)
			writeInPieces(t, ww, in)
			if fw.Clipped == 0 || fw.Clipped != ww.Clipped {
				t.Fatalf("%d bits, dither %v: %d clipped in the FLAC, %d in the WAV", bits, dither, fw.Clipped, ww.Clipped)
			}
			format, got := readBack(t, ff.b)
			if format.Name != "FLAC" || format.SampleRate != 48000 || format.Bits != bits || format.Channels != 2 {
				t.Fatalf("%d bits: read back as %+v", bits, format)
			}
			_, want := readBack(t, wf.b)
			if len(got) != len(in) || len(want) != len(in) {
				t.Fatalf("%d bits: %d samples in the FLAC, %d in the WAV, %d written", bits, len(got), len(want), len(in))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("%d bits, dither %v, sample %d: the FLAC holds %v, the WAV %v", bits, dither, i, got[i], want[i])
				}
			}
			if len(ff.b) >= len(wf.b)*3/4 {
				t.Fatalf("%d bits: the FLAC is %d bytes, the WAV %d", bits, len(ff.b), len(wf.b))
			}
		}
	}
}

// TestAFLACsHeaderHoldsItsLengthChecksumAndTags reads the header a
// FLAC file is closed with: its length, the checksum of its samples,
// and its tags.
func TestAFLACsHeaderHoldsItsLengthChecksumAndTags(t *testing.T) {
	in := flacTestSound()
	var f bufFile
	w, err := NewFLACWriter(&f, 48000, 24, false)
	if err != nil {
		t.Fatal(err)
	}
	w.Tags = []FLACTag{{"TITLE", "Night Engine"}, {"ARTIST", "The Oscillators"}, {"GENRE", ""}}
	writeInPieces(t, w, in)
	if f.pos != len(f.b) {
		t.Fatalf("Close left the file at %d of %d bytes", f.pos, len(f.b))
	}
	s, err := flac.Parse(bytes.NewReader(f.b))
	if err != nil {
		t.Fatal(err)
	}
	if s.Info.NSamples != uint64(len(in)/2) {
		t.Fatalf("the header says %d frames, %d were written", s.Info.NSamples, len(in)/2)
	}
	_, got := readBack(t, f.b)
	sum := md5.New()
	for _, v := range got {
		x := int32(math.Round(float64(v) * (1 << 23)))
		sum.Write([]byte{byte(x), byte(x >> 8), byte(x >> 16)})
	}
	if !bytes.Equal(sum.Sum(nil), s.Info.MD5sum[:]) {
		t.Fatal("the header's checksum differs from the samples'")
	}
	var tags [][2]string
	for _, b := range s.Blocks {
		if c, ok := b.Body.(*meta.VorbisComment); ok {
			tags = c.Tags
		}
	}
	want := [][2]string{{"TITLE", "Night Engine"}, {"ARTIST", "The Oscillators"}}
	if len(tags) != len(want) || tags[0] != want[0] || tags[1] != want[1] {
		t.Fatalf("the tags read back as %q, want %q", tags, want)
	}
}

// TestAFLACWriterTakesSixteenOrTwentyFourBits asks for other bits and
// gets an error.
func TestAFLACWriterTakesSixteenOrTwentyFourBits(t *testing.T) {
	for _, bits := range []int{8, 20, 32} {
		if _, err := NewFLACWriter(&bufFile{}, 48000, bits, false); err == nil {
			t.Fatalf("%d bits gave a writer", bits)
		}
	}
}

// TestAFLACWritersCloseLeavesTheFileOpen closes a FLACWriter on a file
// and finds the file open, at its end.
func TestAFLACWritersCloseLeavesTheFileOpen(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "open.flac"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	w, err := NewFLACWriter(f, 48000, 16, true)
	if err != nil {
		t.Fatal(err)
	}
	writeInPieces(t, w, flacTestSound())
	at, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		t.Fatalf("the file closed with the writer: %v", err)
	}
	st, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if at != st.Size() {
		t.Fatalf("Close left the file at %d of %d bytes", at, st.Size())
	}
}

// streamOnly is a writer that can't seek, and counts its closes.
type streamOnly struct {
	b      bytes.Buffer
	closed int
}

func (s *streamOnly) Write(p []byte) (int, error) { return s.b.Write(p) }
func (s *streamOnly) Close() error                { s.closed++; return nil }

// fullScaleInts is n frames of channels of bits: every channel starts
// with the loudest samples there are, each way, then holds a sine at
// full scale in its first channel and quieter sines with a little noise
// in the rest.
func fullScaleInts(n, channels, bits int) []int32 {
	hi := int32(1)<<(bits-1) - 1
	rng := rand.New(rand.NewPCG(1, 2))
	out := make([]int32, n*channels)
	for i := range n {
		for c := range channels {
			var v int32
			switch {
			case i < 6:
				v = []int32{hi, -hi - 1, -hi, hi, 0, -hi - 1}[(i+c)%6]
			case c == 0:
				v = int32(math.Round(float64(hi) * math.Sin(2*math.Pi*440*float64(i)/48000)))
			default:
				x := 0.9 * float64(hi) * math.Sin(2*math.Pi*float64(220*(c+1))*float64(i)/48000)
				v = int32(math.Round(x)) + int32(rng.IntN(256)) - 128
			}
			out[i*channels+c] = v
		}
	}
	return out
}

// TestAFLACStreamRoundTripsBitExactWithoutSeeking writes 24-bit mono,
// stereo and six channels, as whole samples and as floats without
// dither, to a writer that can't seek, and reads back the very samples
// written, the loudest ones too, with the header counting them.
func TestAFLACStreamRoundTripsBitExactWithoutSeeking(t *testing.T) {
	for _, channels := range []int{1, 2, 6} {
		for _, floats := range []bool{false, true} {
			n := 3*flacBlock + 1234
			in := fullScaleInts(n, channels, 24)
			var out streamOnly
			w, err := NewFLACStream(&out, 48000, 24, channels, int64(n), false)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < len(in); i += 1000 * channels {
				part := in[i:min(len(in), i+1000*channels)]
				if floats {
					f := make([]float32, len(part))
					for j, v := range part {
						f[j] = float32(v) / (1 << 23)
					}
					err = w.Write(f)
				} else {
					err = w.WriteInts(part)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			if w.Clipped != 0 || out.closed != 0 {
				t.Fatalf("%d channels: %d clipped, closed %d times", channels, w.Clipped, out.closed)
			}
			s, err := flac.Parse(bytes.NewReader(out.b.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			if s.Info.NSamples != uint64(n) || s.Info.MD5sum != [16]byte{} {
				t.Fatalf("%d channels: the header says %d frames and checksum %x", channels, s.Info.NSamples, s.Info.MD5sum)
			}
			got, err := DecodeFLACInts(bytes.NewReader(out.b.Bytes()), int64(n))
			if err != nil {
				t.Fatal(err)
			}
			if got.Rate != 48000 || got.Bits != 24 || got.Channels != channels || len(got.Samples) != len(in) {
				t.Fatalf("%d channels: read back %d Hz, %d bits, %d channels, %d samples", channels, got.Rate, got.Bits, got.Channels, len(got.Samples))
			}
			for i := range in {
				if got.Samples[i] != in[i] {
					t.Fatalf("%d channels, floats %v, sample %d: read %d, wrote %d", channels, floats, i, got.Samples[i], in[i])
				}
			}
			if len(out.b.Bytes()) >= n*channels*3 {
				t.Fatalf("%d channels: %d bytes for %d of raw samples", channels, len(out.b.Bytes()), n*channels*3)
			}
		}
	}
}

// TestAFLACStreamFlowsThroughAPipe writes a stereo stream into a pipe
// while the other end decodes it.
func TestAFLACStreamFlowsThroughAPipe(t *testing.T) {
	n := 2*flacBlock + 77
	in := fullScaleInts(n, 2, 24)
	r, pw := io.Pipe()
	go func() {
		w, err := NewFLACStream(pw, 44100, 24, 2, int64(n), false)
		if err == nil {
			err = w.WriteInts(in)
		}
		if err == nil {
			err = w.Close()
		}
		pw.CloseWithError(err)
	}()
	got, err := DecodeFLACInts(r, int64(n))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Samples) != len(in) {
		t.Fatalf("read %d samples, wrote %d", len(got.Samples), len(in))
	}
	for i := range in {
		if got.Samples[i] != in[i] {
			t.Fatalf("sample %d: read %d, wrote %d", i, got.Samples[i], in[i])
		}
	}
}

// TestAShortFLACStreamIsOneBlock writes streams of no frames, of fewer
// than 16 and of fewer than a block, at 16 bits, and reads them back
// as they were, from the decoder FLAC files are opened with too.
func TestAShortFLACStreamIsOneBlock(t *testing.T) {
	for _, n := range []int{0, 5, 100} {
		in := fullScaleInts(n, 2, 16)
		var out streamOnly
		w, err := NewFLACStream(&out, 48000, 16, 2, int64(n), false)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.WriteInts(in); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		got, err := DecodeFLACInts(bytes.NewReader(out.b.Bytes()), int64(n))
		if err != nil {
			t.Fatalf("%d frames: %v", n, err)
		}
		if len(got.Samples) != len(in) {
			t.Fatalf("%d frames: read %d samples", n, len(got.Samples))
		}
		for i := range in {
			if got.Samples[i] != in[i] {
				t.Fatalf("%d frames, sample %d: read %d, wrote %d", n, i, got.Samples[i], in[i])
			}
		}
		if n == 0 {
			continue
		}
		_, floats := readBack(t, out.b.Bytes())
		for i := range in {
			if want := float32(in[i]) / (1 << 15); floats[i] != want {
				t.Fatalf("%d frames, sample %d: opened as %v, want %v", n, i, floats[i], want)
			}
		}
	}
}

// TestAFLACStreamHoldsToItsCount writes more frames than a stream was
// started with, and fewer, and gets errors.
func TestAFLACStreamHoldsToItsCount(t *testing.T) {
	w, err := NewFLACStream(&streamOnly{}, 48000, 24, 1, 10, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.WriteInts(make([]int32, 11)); err == nil {
		t.Fatal("11 frames went into a stream of 10")
	}
	if err := w.WriteInts(make([]int32, 9)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err == nil {
		t.Fatal("a stream of 10 closed with 9")
	}
}

// TestAFLACStreamTakesItsChannelsAndSamples asks for no channels, nine,
// and samples past 24 bits, and gets errors.
func TestAFLACStreamTakesItsChannelsAndSamples(t *testing.T) {
	for _, ch := range []int{0, 9} {
		if _, err := NewFLACStream(&streamOnly{}, 48000, 24, ch, 0, false); err == nil {
			t.Fatalf("%d channels gave a stream", ch)
		}
	}
	w, err := NewFLACStream(&streamOnly{}, 48000, 24, 1, 2, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []int32{1 << 23, -1<<23 - 1} {
		if err := w.WriteInts([]int32{v}); err == nil {
			t.Fatalf("%d went into 24 bits", v)
		}
	}
}

// TestDecodeFLACIntsStopsAtItsCap reads a stream longer than the cap,
// and one whose header undercounts it, and gets errors.
func TestDecodeFLACIntsStopsAtItsCap(t *testing.T) {
	n := 2*flacBlock + 10
	var out streamOnly
	w, err := NewFLACStream(&out, 48000, 16, 1, int64(n), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.WriteInts(fullScaleInts(n, 1, 16)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeFLACInts(bytes.NewReader(out.b.Bytes()), int64(n-1)); err == nil {
		t.Fatal("a stream past the cap decoded")
	}
	// The header's count, 36 bits from byte 21, set to 1.
	b := bytes.Clone(out.b.Bytes())
	b[21] &^= 0x0f
	clear(b[22:26])
	b[25] = 1
	if _, err := DecodeFLACInts(bytes.NewReader(b), int64(n)); err == nil {
		t.Fatal("a stream past its header's count decoded")
	}
}
