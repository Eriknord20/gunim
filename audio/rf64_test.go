package audio

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"testing"
)

// overLimit is a length in frames of stereo float past 4 GB of samples.
const overLimit = int64(1)<<29 + 1000

// sparse reads as head, then tail at tailAt, with zeros between and size
// bytes in all: a file of gigabytes without the gigabytes.
type sparse struct {
	head, tail []byte
	tailAt     int64
	size       int64
}

func (s sparse) ReadAt(b []byte, off int64) (int, error) {
	n := 0
	for ; n < len(b) && off+int64(n) < s.size; n++ {
		i := off + int64(n)
		switch {
		case i < int64(len(s.head)):
			b[n] = s.head[i]
		case i >= s.tailAt && i < s.tailAt+int64(len(s.tail)):
			b[n] = s.tail[i-s.tailAt]
		default:
			b[n] = 0
		}
	}
	if n < len(b) {
		return n, io.EOF
	}
	return n, nil
}

func (s sparse) Size() int64 { return s.size }

var stereoFloat = WAVSpec{Rate: 48000, Channels: 2, Bits: 32, Float: true}

// floats is samples as a float WAV file holds them.
func floats(samples ...float32) []byte {
	var b []byte
	for _, v := range samples {
		b = binary.LittleEndian.AppendUint32(b, math.Float32bits(v))
	}
	return b
}

func TestAWAVHeaderPast4GBTakesTheRF64FormAtTheSameLength(t *testing.T) {
	small := WAVHeader(stereoFloat, 1000)
	big := WAVHeader(stereoFloat, overLimit)
	if len(small) != 94 || len(big) != 94 || WAVHeaderSize(stereoFloat) != 94 {
		t.Fatalf("the headers are %d and %d bytes, want 94", len(small), len(big))
	}
	if n := len(WAVHeader(WAVSpec{Rate: 44100, Channels: 1, Bits: 16}, 3)); n != 80 ||
		WAVHeaderSize(WAVSpec{Bits: 16}) != 80 {
		t.Fatalf("an integer header is %d bytes, want 80", n)
	}
	if string(small[:4]) != "RIFF" || string(small[12:16]) != "JUNK" {
		t.Fatalf("a small header starts %q", small[:16])
	}
	if string(big[:4]) != "RF64" || string(big[12:16]) != "ds64" {
		t.Fatalf("a big header starts %q", big[:16])
	}
	if string(small[48:52]) != "fmt " || string(big[48:52]) != "fmt " || string(big[86:90]) != "data" {
		t.Fatalf("the chunks moved: %q", big)
	}
	l, err := ReadWAVLayout(sparse{head: big, size: 94 + overLimit*8})
	if err != nil || !l.Wide || l.Data != 94 || l.Frames != overLimit || l.WAVSpec != stereoFloat {
		t.Fatalf("the big header reads as %+v, %v", l, err)
	}
	l, err = ReadWAVLayout(bytes.NewReader(append(small, make([]byte, 8000)...)))
	if err != nil || l.Wide || l.Data != 94 || l.Frames != 1000 {
		t.Fatalf("the small header reads as %+v, %v", l, err)
	}
}

// TestAFloatWAVHeaderIsTheOneHejDAWWrites keeps the header byte for byte
// what Hej DAW!'s media files have, as it names them by a hash of their
// bytes.
func TestAFloatWAVHeaderIsTheOneHejDAWWrites(t *testing.T) {
	le := binary.LittleEndian
	var want []byte
	want = append(want, "RIFF"...)
	want = le.AppendUint32(want, 86+3*8)
	want = append(want, "WAVEJUNK"...)
	want = le.AppendUint32(want, 28)
	want = append(want, make([]byte, 28)...)
	want = append(want, "fmt "...)
	want = le.AppendUint32(want, 18)
	want = le.AppendUint16(want, 3)
	want = le.AppendUint16(want, 2)
	want = le.AppendUint32(want, 48000)
	want = le.AppendUint32(want, 48000*8)
	want = le.AppendUint16(want, 8)
	want = le.AppendUint16(want, 32)
	want = le.AppendUint16(want, 0)
	want = append(want, "fact"...)
	want = le.AppendUint32(want, 4)
	want = le.AppendUint32(want, 3)
	want = append(want, "data"...)
	want = le.AppendUint32(want, 24)
	if got := WAVHeader(stereoFloat, 3); !bytes.Equal(got, want) {
		t.Fatalf("the header is\n%q, want\n%q", got, want)
	}
}

func TestAWAVPast4GBDecodesItsWholeLength(t *testing.T) {
	head := append(WAVHeader(stereoFloat, overLimit), floats(0.5, -0.5)...)
	size := 94 + overLimit*8
	tail := floats(0.25, -0.25)
	r := io.NewSectionReader(sparse{head: head, tail: tail, tailAt: size - 8, size: size}, 0, size)
	src, format, err := DecodeNative(r)
	if err != nil {
		t.Fatal(err)
	}
	if src.Len() != overLimit || format.Channels != 2 || format.Bits != 32 {
		t.Fatalf("decoded %d frames of %+v", src.Len(), format)
	}
	dst := make([]float32, 2)
	if n, _ := src.Read(dst); n != 1 || dst[0] != 0.5 || dst[1] != -0.5 {
		t.Fatalf("the first frame reads %d: %v", n, dst)
	}
	if err := src.SeekFrame(overLimit - 1); err != nil {
		t.Fatal(err)
	}
	if n, err := src.Read(dst); n != 1 || !errors.Is(err, io.EOF) || dst[0] != 0.25 || dst[1] != -0.25 {
		t.Fatalf("the last frame reads %d, %v: %v", n, err, dst)
	}
}

// withLimit has WAV headers turn RF64 past limit bytes, not 4 GB, until
// the test ends.
func withLimit(t *testing.T, limit int64) {
	was := wavLimit
	wavLimit = limit
	t.Cleanup(func() { wavLimit = was })
}

func TestAWAVWriterTurnsRF64PastTheLimitAndReadsBack(t *testing.T) {
	samples := []float32{0.5, -0.5, 0.25, -0.25, 1, -1}
	for _, c := range []struct {
		name  string
		limit int64
		form  string
	}{
		{"under", math.MaxUint32 - 1, "RIFF"},
		{"past", 100, "RF64"},
	} {
		t.Run(c.name, func(t *testing.T) {
			withLimit(t, c.limit)
			var f seekBuffer
			w, err := NewWAVWriter(&f, 48000, 32, false)
			if err != nil {
				t.Fatal(err)
			}
			w.Tags = []WAVTag{{"INAM", "Long Take"}}
			if err := w.Write(samples); err != nil {
				t.Fatal(err)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			b := f.Bytes()
			if string(b[:4]) != c.form {
				t.Fatalf("the file starts %q, want %s", b[:4], c.form)
			}
			if c.form == "RIFF" && string(b[12:16]) != "JUNK" {
				t.Fatalf("a RIFF file has %q where the JUNK chunk goes", b[12:16])
			}
			if c.form == "RF64" {
				if riff := binary.LittleEndian.Uint64(b[20:]); int(riff) != len(b)-8 {
					t.Fatalf("the ds64 RIFF size is %d, the file %d bytes after it", riff, len(b)-8)
				}
			}
			if !bytes.Contains(b, []byte("Long Take")) {
				t.Fatal("the tags are gone")
			}
			src, format, err := DecodeNative(bytes.NewReader(b))
			if err != nil {
				t.Fatal(err)
			}
			got := make([]float32, 16)
			n, _ := src.Read(got)
			if n != 3 || src.Len() != 3 || format.SampleRate != 48000 {
				t.Fatalf("read %d of %d frames", n, src.Len())
			}
			for i, v := range samples {
				if got[i] != v {
					t.Fatalf("sample %d is %v, want %v", i, got[i], v)
				}
			}
		})
	}
}

func TestAWAVWriterPast4GBWritesTheSizesInDS64(t *testing.T) {
	var f seekBuffer
	w, err := NewWAVWriter(&f, 48000, 24, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Write(make([]float32, 4)); err != nil {
		t.Fatal(err)
	}
	// Past 4 GB without the gigabytes: the writer counts as long.
	w.frames = 2 * overLimit
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	b := f.Bytes()
	le := binary.LittleEndian
	data := 2 * overLimit * 6
	if string(b[:4]) != "RF64" || le.Uint32(b[4:]) != math.MaxUint32 || string(b[12:16]) != "ds64" {
		t.Fatalf("the header starts %q", b[:16])
	}
	if riff, d, n := le.Uint64(b[20:]), le.Uint64(b[28:]), le.Uint64(b[36:]); int64(riff) != 72+data ||
		int64(d) != data || int64(n) != 2*overLimit {
		t.Fatalf("ds64 holds %d, %d, %d", riff, d, n)
	}
	if le.Uint32(b[76:]) != math.MaxUint32 {
		t.Fatalf("the data chunk's own size is %d, want 0xFFFFFFFF", le.Uint32(b[76:]))
	}
	l, err := ReadWAVLayout(sparse{head: b, size: 80 + data})
	if err != nil || !l.Wide || l.Frames != 2*overLimit || l.Bits != 24 || l.Data != 80 {
		t.Fatalf("the header reads as %+v, %v", l, err)
	}
}

func TestAWAVHeaderTornAsItTurnedRF64Recovers(t *testing.T) {
	samples := floats(0.5, -0.5, 0.25, -0.25)
	big := WAVHeader(stereoFloat, overLimit)
	small := WAVHeader(stereoFloat, 0)
	for _, c := range []struct {
		name string
		head []byte
		// strict says reading it as a whole header fails.
		strict bool
	}{
		// The ds64 chunk got written, RF64 in place of RIFF didn't.
		{"ds64 under RIFF", append([]byte("RIFF"), big[4:]...), true},
		// RF64 got written, the ds64 chunk didn't.
		{"JUNK under RF64", append(append([]byte(nil), big[:8]...), small[8:]...), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := append(append([]byte(nil), c.head...), samples...)
			if _, err := ReadWAVLayout(bytes.NewReader(b)); c.strict && !errors.Is(err, ErrWAVHeader) {
				t.Fatalf("the torn header read strictly gave %v", err)
			}
			l, err := RecoverWAVLayout(bytes.NewReader(b), int64(len(b)))
			if err != nil || l.Data != 94 || l.Frames != 2 || l.WAVSpec != stereoFloat {
				t.Fatalf("the torn header recovers as %+v, %v", l, err)
			}
		})
	}
	// A recording cut off within its header has nothing to recover.
	if _, err := RecoverWAVLayout(bytes.NewReader(small[:60]), 60); !errors.Is(err, ErrWAVHeader) {
		t.Fatalf("a header cut short recovers with %v", err)
	}
}

func TestAWAVHeaderCutShortIsErrWAVHeader(t *testing.T) {
	b := append(WAVHeader(stereoFloat, 2), floats(0.5, -0.5, 0.25, -0.25)...)
	for _, n := range []int{12, 30, 60, 93} {
		if _, err := ReadWAVLayout(bytes.NewReader(b[:n])); !errors.Is(err, ErrWAVHeader) || !errors.Is(err, ErrFormat) {
			t.Fatalf("%d bytes read with %v", n, err)
		}
		if _, err := Decode(bytes.NewReader(b[:n])); !errors.Is(err, ErrWAVHeader) {
			t.Fatalf("%d bytes decoded with %v", n, err)
		}
	}
	if _, err := ReadWAVLayout(bytes.NewReader(b[:8])); !errors.Is(err, ErrFormat) || errors.Is(err, ErrWAVHeader) {
		t.Fatalf("8 bytes read with %v", err)
	}
}

func TestAStreamedRIFFRunsToTheEndOfTheFile(t *testing.T) {
	s := WAVSpec{Rate: 44100, Channels: 1, Bits: 16}
	b := WAVHeader(s, 0)
	// A program writing to a stream knows no sizes.
	binary.LittleEndian.PutUint32(b[4:], math.MaxUint32)
	binary.LittleEndian.PutUint32(b[76:], math.MaxUint32)
	b = append(b, 0, 0x40, 0, 0xc0, 0, 0x20)
	l, err := ReadWAVLayout(bytes.NewReader(b))
	if err != nil || l.Wide || l.Frames != 3 {
		t.Fatalf("the streamed file reads as %+v, %v", l, err)
	}
	src, _, err := DecodeNative(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	dst := make([]float32, 16)
	n, err := src.Read(dst)
	if src.Len() != 3 || n != 3 || !errors.Is(err, io.EOF) || dst[0] != 0.5 || dst[2] != -0.5 || dst[4] != 0.25 {
		t.Fatalf("decoded %d of %d frames, %v: %v", n, src.Len(), err, dst[:6])
	}
}

// otherWAV is a wide WAV file starting magic, as another program may
// write it: the ds64 chunk first, a fmt chunk of samples in format tag,
// and the data's size in the ds64 chunk alone. Where tag is the
// extensible format, sub is the samples' own.
func otherWAV(magic string, tag, sub uint16, channels, bits int, frames int64, samples []byte) []byte {
	le := binary.LittleEndian
	data := frames * int64(channels*bits/8)
	b := append([]byte(magic), le.AppendUint32(nil, math.MaxUint32)...)
	b = append(b, "WAVEds64"...)
	b = le.AppendUint32(b, ds64Size)
	b = le.AppendUint64(b, uint64(4+36+24+8+data))
	b = le.AppendUint64(b, uint64(data))
	b = le.AppendUint64(b, uint64(frames))
	b = le.AppendUint32(b, 0)
	b = append(b, "fmt "...)
	fmtSize := uint32(16)
	if tag == wavExtensible {
		fmtSize = 40
	}
	b = le.AppendUint32(b, fmtSize)
	b = le.AppendUint16(b, tag)
	b = le.AppendUint16(b, uint16(channels))
	b = le.AppendUint32(b, 48000)
	b = le.AppendUint32(b, uint32(48000*channels*bits/8))
	b = le.AppendUint16(b, uint16(channels*bits/8))
	b = le.AppendUint16(b, uint16(bits))
	if tag == wavExtensible {
		b = le.AppendUint16(b, 22)
		b = le.AppendUint16(b, uint16(bits))
		b = le.AppendUint32(b, 3)
		b = le.AppendUint16(b, sub)
		b = append(b, make([]byte, 14)...)
	}
	b = append(b, "data"...)
	b = le.AppendUint32(b, math.MaxUint32)
	return append(b, samples...)
}

func TestRF64AndBW64DecodeEachSampleFormat(t *testing.T) {
	for _, c := range []struct {
		name     string
		magic    string
		tag, sub uint16
		bits     int
		samples  []byte
	}{
		{"8-bit", "RF64", wavPCM, 0, 8, []byte{192, 64}},
		{"extensible 24-bit", "RF64", wavExtensible, wavPCM, 24, []byte{0, 0, 0x40, 0, 0, 0xc0}},
		{"32-bit", "RF64", wavPCM, 0, 32, []byte{0, 0, 0, 0x40, 0, 0, 0, 0xc0}},
		{"float", "RF64", wavFloat, 0, 32, floats(0.5, -0.5)},
		{"BW64", "BW64", wavPCM, 0, 16, []byte{0, 0x40, 0, 0xc0}},
	} {
		t.Run(c.name, func(t *testing.T) {
			src, format, err := DecodeNative(bytes.NewReader(otherWAV(c.magic, c.tag, c.sub, 1, c.bits, 2, c.samples)))
			if err != nil {
				t.Fatal(err)
			}
			dst := make([]float32, 8)
			n, err := src.Read(dst)
			if n != 2 || !errors.Is(err, io.EOF) || format.Bits != c.bits ||
				dst[0] != 0.5 || dst[1] != 0.5 || dst[2] != -0.5 || dst[3] != -0.5 {
				t.Fatalf("read %d frames %v, %v", n, dst[:4], err)
			}
		})
	}
}

func TestAnRF64ShorterThanItsHeaderTellsEndsWhereItStops(t *testing.T) {
	src, err := Decode(bytes.NewReader(otherWAV("RF64", wavPCM, 0, 1, 16, 4, []byte{0, 0x40, 0, 0xc0})))
	if err != nil {
		t.Fatal(err)
	}
	if n, err := src.Read(make([]float32, 16)); src.Len() != 2 || n == 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("reading a short file gave %d frames, %v, and a length of %d", n, err, src.Len())
	}
}

func TestAWAVGunimDoesNotReadIsRefusedPlainly(t *testing.T) {
	big := WAVHeader(stereoFloat, overLimit)
	noDS64 := append(append(append([]byte(nil), big[:12]...), WAVHeader(stereoFloat, 0)[12:48]...), big[48:]...)
	for _, c := range []struct {
		name string
		b    []byte
		want error
	}{
		{"65 channels", otherWAV("RF64", wavPCM, 0, 65, 16, 1, nil), ErrSampleFormat},
		{"MP3 samples", otherWAV("RF64", 0x55, 0, 1, 16, 1, nil), ErrSampleFormat},
		{"12-bit", otherWAV("RF64", wavPCM, 0, 1, 12, 1, nil), ErrSampleFormat},
		{"64-bit float", otherWAV("BW64", wavFloat, 0, 1, 64, 1, nil), ErrSampleFormat},
		// Its size is in a ds64 chunk it doesn't have.
		{"no ds64", noDS64, ErrWAVHeader},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Decode(bytes.NewReader(c.b)); !errors.Is(err, c.want) || !errors.Is(err, ErrFormat) {
				t.Fatalf("decoding gave %v, want %v", err, c.want)
			}
		})
	}
}

func TestWalkWAVGivesTheDataSizeFromDS64(t *testing.T) {
	data := overLimit * 8
	head := WAVHeader(stereoFloat, overLimit)
	// A cue chunk after the gigabytes of samples.
	cue := append([]byte("cue "), binary.LittleEndian.AppendUint32(nil, 4)...)
	cue = binary.LittleEndian.AppendUint32(cue, 0)
	r := sparse{head: head, tail: cue, tailAt: 94 + data, size: 94 + data + int64(len(cue))}
	var got []WAVChunk
	if err := WalkWAV(r, func(c WAVChunk) bool { got = append(got, c); return true }); err != nil {
		t.Fatal(err)
	}
	want := []WAVChunk{{"ds64", 20, 28}, {"fmt ", 56, 18}, {"fact", 82, 4}, {"data", 94, data}, {"cue ", 102 + data, 4}}
	if len(got) != len(want) {
		t.Fatalf("walked %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("chunk %d is %+v, want %+v", i, got[i], want[i])
		}
	}
	// It stops where fn says.
	n := 0
	if err := WalkWAV(r, func(c WAVChunk) bool { n++; return c.ID != "fmt " }); err != nil || n != 2 {
		t.Fatalf("walked %d chunks, %v", n, err)
	}
	if err := WalkWAV(bytes.NewReader([]byte("OggS and more")), func(WAVChunk) bool { return true }); !errors.Is(err, ErrFormat) {
		t.Fatalf("walking no WAV gave %v", err)
	}
}

// failing fails every read past its first bytes, as a disk can.
type failing struct{ b []byte }

var errDisk = errors.New("the disk failed")

func (f failing) ReadAt(b []byte, off int64) (int, error) {
	if off+int64(len(b)) > int64(len(f.b)) {
		return 0, errDisk
	}
	return copy(b, f.b[off:]), nil
}

func TestAWAVHeaderReadErrorIsReturnedAsItIs(t *testing.T) {
	b := WAVHeader(stereoFloat, 2)
	if _, err := ReadWAVLayout(failing{b[:40]}); !errors.Is(err, errDisk) || errors.Is(err, ErrFormat) {
		t.Fatalf("a failing disk gave %v", err)
	}
}
