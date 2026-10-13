package audio

import (
	"bytes"
	"crypto/md5"
	"errors"
	"io"
	"math"
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
