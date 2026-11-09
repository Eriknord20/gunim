package audio

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"slices"
	"testing"
)

// putExtended writes v as the 80-bit extended float AIFF keeps its rate
// in.
func putExtended(v float64) []byte {
	frac, exp := math.Frexp(v)
	b := make([]byte, 10)
	binary.BigEndian.PutUint16(b, uint16(exp-1+16383))
	binary.BigEndian.PutUint64(b[2:], uint64(frac*(1<<64)))
	return b
}

// aiffFile returns an AIFF file of frames, at rate, with ch channels of
// bits bits, in compression: "" for plain AIFF, or an AIFF-C type. An
// odd-sized chunk comes before the samples, and the SSND chunk keeps
// offset bytes before them.
func aiffFile(rate, ch, bits int, compression string, offset int, frames [][]float64) []byte {
	be := binary.BigEndian
	var data bytes.Buffer
	data.Write(make([]byte, offset))
	width := (bits + 7) / 8
	for _, f := range frames {
		for c := range ch {
			v := f[c]
			var s []byte
			switch compression {
			case "fl32":
				s = be.AppendUint32(nil, math.Float32bits(float32(v)))
			case "fl64":
				s = be.AppendUint64(nil, math.Float64bits(v))
			default:
				// Left-justified in its bytes, big-endian.
				x := uint32(int32(math.Round(v*float64(int64(1)<<(bits-1)-1)))) << (32 - bits)
				for i := range width {
					s = append(s, byte(x>>(24-8*i)))
				}
				if compression == "sowt" {
					for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
						s[i], s[j] = s[j], s[i]
					}
				}
			}
			data.Write(s)
		}
	}
	var comm bytes.Buffer
	_ = binary.Write(&comm, be, int16(ch))
	_ = binary.Write(&comm, be, uint32(len(frames)))
	_ = binary.Write(&comm, be, int16(bits))
	comm.Write(putExtended(float64(rate)))
	kind := "AIFF"
	if compression != "" {
		kind = "AIFC"
		comm.WriteString(compression)
		comm.Write([]byte{0, 0}) // an empty name, padded
	}
	var body bytes.Buffer
	body.WriteString(kind)
	chunk := func(id string, b []byte) {
		body.WriteString(id)
		_ = binary.Write(&body, be, uint32(len(b)))
		body.Write(b)
		if len(b)%2 == 1 {
			body.WriteByte(0)
		}
	}
	chunk("COMM", comm.Bytes())
	chunk("NAME", []byte("odd"))
	ssnd := be.AppendUint32(nil, uint32(offset))
	ssnd = be.AppendUint32(ssnd, 0)
	chunk("SSND", append(ssnd, data.Bytes()...))
	var b bytes.Buffer
	b.WriteString("FORM")
	_ = binary.Write(&b, be, uint32(body.Len()))
	b.Write(body.Bytes())
	return b.Bytes()
}

func TestAIFFReadsEachSampleFormat(t *testing.T) {
	frames := [][]float64{{0.5, -0.25}, {-0.5, 0.25}, {0, 0.75}}
	for _, c := range []struct {
		name        string
		ch, bits    int
		compression string
		tol         float64
	}{
		{"8-bit", 2, 8, "", 0.01},
		{"12-bit", 2, 12, "", 1e-3},
		{"16-bit", 2, 16, "", 1e-4},
		{"24-bit", 2, 24, "", 1e-6},
		{"32-bit", 2, 32, "", 1e-6},
		{"AIFF-C NONE", 2, 24, "NONE", 1e-6},
		{"AIFF-C sowt", 2, 16, "sowt", 1e-4},
		{"AIFF-C sowt 24-bit", 2, 24, "sowt", 1e-6},
		{"AIFF-C fl32", 2, 32, "fl32", 1e-7},
		{"AIFF-C fl64", 2, 64, "fl64", 1e-7},
		{"mono", 1, 16, "", 1e-4},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, format, err := DecodeNative(bytes.NewReader(aiffFile(48000, c.ch, c.bits, c.compression, 3, frames)))
			if err != nil {
				t.Fatal(err)
			}
			if format.Name != "AIFF" || format.SampleRate != 48000 || format.Channels != c.ch || format.Bits != c.bits {
				t.Errorf("format %+v", format)
			}
			got := readAll(t, s)
			if len(got) != 2*len(frames) {
				t.Fatalf("%d frames, want %d", len(got)/2, len(frames))
			}
			for i, f := range frames {
				r := f[1]
				if c.ch == 1 {
					r = f[0]
				}
				if !near(float64(got[2*i]), f[0], c.tol) || !near(float64(got[2*i+1]), r, c.tol) {
					t.Errorf("frame %d is %v, %v; want %v, %v", i, got[2*i], got[2*i+1], f[0], r)
				}
			}
		})
	}
}

func TestAnAIFFCutShortPlaysWhatItHolds(t *testing.T) {
	frames := make([][]float64, 100)
	for i := range frames {
		frames[i] = []float64{0.5, -0.5}
	}
	b := aiffFile(SampleRate, 2, 16, "", 0, frames)
	// 40 frames and a half go missing.
	s, err := Decode(bytes.NewReader(b[:len(b)-40*4-2]))
	if err != nil {
		t.Fatal(err)
	}
	if s.Len() != 100 {
		t.Errorf("Len %d before reading, want the 100 the header tells of", s.Len())
	}
	if n := len(readAll(t, s)) / 2; n != 59 {
		t.Errorf("%d frames, want the 59 whole frames left", n)
	}
	if s.Len() != 59 {
		t.Errorf("Len %d after reading, want 59", s.Len())
	}
}

func TestAIFFChunksComeInAnyOrder(t *testing.T) {
	frames := [][]float64{{0.5, -0.5}, {0.25, -0.25}}
	b := aiffFile(SampleRate, 2, 16, "", 0, frames)
	// Move COMM, the first chunk, 8 and 18 bytes, to the end.
	comm := slices.Clone(b[12 : 12+26])
	moved := append(append(slices.Clone(b[:12]), b[12+26:]...), comm...)
	s, err := Decode(bytes.NewReader(moved))
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, s); len(got) != 4 || !near(float64(got[2]), 0.25, 1e-4) {
		t.Errorf("read %v", got)
	}
}

func TestAnAIFFStreamedRunsToTheEndOfTheFile(t *testing.T) {
	frames := [][]float64{{0.5, -0.5}, {0.25, -0.25}, {0, 0}}
	for _, size := range []uint32{0, math.MaxUint32} {
		b := aiffFile(SampleRate, 2, 16, "", 0, frames)
		ssnd := bytes.Index(b, []byte("SSND"))
		binary.BigEndian.PutUint32(b[ssnd+4:], size)
		s, err := Decode(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		if n := len(readAll(t, s)) / 2; n != 3 {
			t.Errorf("SSND of size %#x: %d frames, want 3", size, n)
		}
	}
}

func TestIsSoundFileGoesByTheName(t *testing.T) {
	for name, want := range map[string]bool{
		"loop.AIF": true, "a.aiff": true, "x.Wav": true, "y.oga": true,
		"notes.txt": false, "aif": false, "song.hejsong": false,
	} {
		if IsSoundFile(name) != want {
			t.Errorf("IsSoundFile(%q) is %v", name, !want)
		}
	}
}

func TestAIFFRefusesWhatItCannotRead(t *testing.T) {
	frames := [][]float64{{0, 0}}
	ulaw := aiffFile(SampleRate, 2, 16, "ulaw", 0, frames)
	if _, err := Decode(bytes.NewReader(ulaw)); !errors.Is(err, ErrSampleFormat) || !errors.Is(err, ErrFormat) {
		t.Errorf("µ-law: %v, want ErrSampleFormat", err)
	}
	whole := aiffFile(SampleRate, 2, 16, "", 0, frames)
	for _, n := range []int{14, 30, 40} {
		if _, err := Decode(bytes.NewReader(whole[:n])); !errors.Is(err, ErrAIFFHeader) || !errors.Is(err, ErrFormat) {
			t.Errorf("cut to %d bytes: %v, want ErrAIFFHeader", n, err)
		}
	}
}

// TestReadingAnAIFFMakesNothing reads an AIFF in reads of every size,
// counting every allocation, as reading may run where sound is made.
func TestReadingAnAIFFMakesNothing(t *testing.T) {
	if raceOn {
		t.Skip("the race detector allocates of its own")
	}
	frames := make([][]float64, 30000)
	for i := range frames {
		frames[i] = []float64{0.1, -0.1}
	}
	s, _, err := DecodeNative(bytes.NewReader(aiffFile(SampleRate, 2, 24, "", 0, frames)))
	if err != nil {
		t.Fatal(err)
	}
	dst := make([]float32, 2*8192)
	n := allocsUnder(t, "audio.(*aiff).read", func() {
		for _, n := range []int{100, 1024, 4096, 8192, 7} {
			if _, err := s.Read(dst[:2*n]); err != nil {
				t.Fatal(err)
			}
		}
	})
	if n != 0 {
		t.Fatalf("reading made %d things", n)
	}
}

// failingReader fails every read past its first from bytes.
type failingReader struct {
	*bytes.Reader
	from int64
}

func (f failingReader) Read(b []byte) (int, error) {
	if at, _ := f.Seek(0, io.SeekCurrent); at >= f.from {
		return 0, errDisk
	}
	return f.Reader.Read(b)
}

func TestAnAIFFReadErrorIsNoFormatError(t *testing.T) {
	b := aiffFile(SampleRate, 2, 16, "", 0, [][]float64{{0, 0}})
	_, err := Decode(failingReader{bytes.NewReader(b), 12 + 26})
	if !errors.Is(err, errDisk) || errors.Is(err, ErrFormat) {
		t.Errorf("Decode: %v, want the read's error", err)
	}
}
