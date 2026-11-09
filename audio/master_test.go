package audio

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestASineMeasuresTheSameAtFortyFourPointOne(t *testing.T) {
	const rate = 44100
	n := rate * 5
	frames := make([]float32, 2*n)
	for i := range n {
		v := float32(0.1 * math.Sin(2*math.Pi*1000*float64(i)/rate))
		frames[2*i], frames[2*i+1] = v, v
	}
	m := NewLoudnessMeter(rate)
	m.Write(frames)
	if got, _ := m.Integrated(); math.Abs(got+20) > 0.1 {
		t.Fatalf("a 1 kHz sine at -20 dBFS, at 44.1 kHz, measured %.2f LUFS, want -20", got)
	}
	if mo, st := m.Momentary(), m.ShortTerm(); math.Abs(mo+20) > 0.1 || math.Abs(st+20) > 0.1 {
		t.Fatalf("momentary %.2f and short-term %.2f, want -20 both", mo, st)
	}
}

func TestATruePeakFindsWhatFallsBetweenTheSamples(t *testing.T) {
	// A sine at a quarter of the rate, half a sample off: every sample
	// lands at 0.707 of its height, and its peaks between them.
	n := 4000
	frames := make([]float32, 2*n)
	for i := range n {
		v := float32(0.9 * math.Sin(math.Pi/2*float64(i)+math.Pi/4))
		frames[2*i], frames[2*i+1] = v, v
	}
	var m TruePeakMeter
	m.Write(frames)
	if db := 20 * math.Log10(m.Peak()/0.9); math.Abs(db) > 0.3 {
		t.Fatalf("the true peak is %.3f, %.2f dB from the sine's 0.9; its samples peak at 0.636", m.Peak(), db)
	}
}

// bufFile is an in-memory io.WriteSeeker.
type bufFile struct {
	b   []byte
	pos int
}

func (f *bufFile) Write(p []byte) (int, error) {
	if need := f.pos + len(p); need > len(f.b) {
		f.b = append(f.b, make([]byte, need-len(f.b))...)
	}
	copy(f.b[f.pos:], p)
	f.pos += len(p)
	return len(p), nil
}

func (f *bufFile) Seek(off int64, whence int) (int64, error) {
	switch whence {
	case 0:
		f.pos = int(off)
	case 1:
		f.pos += int(off)
	case 2:
		f.pos = len(f.b) + int(off)
	}
	return int64(f.pos), nil
}

func TestAWAVWrittenReadsBackAtItsRateWithDitherAnLSBDeep(t *testing.T) {
	for _, bits := range []int{16, 24, 32} {
		var f bufFile
		w, err := NewWAVWriter(&f, 44100, bits, true)
		if err != nil {
			t.Fatal(err)
		}
		n := 44100
		in := make([]float32, 2*n)
		for i := range n {
			v := float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/44100))
			in[2*i], in[2*i+1] = v, -v
		}
		if err = w.Write(in); err != nil {
			t.Fatal(err)
		}
		if err = w.Close(); err != nil {
			t.Fatal(err)
		}
		src, format, err := DecodeNative(bytes.NewReader(f.b))
		if err != nil {
			t.Fatalf("%d bits: %v", bits, err)
		}
		if format.SampleRate != 44100 || format.Bits != bits || src.Len() != int64(n) {
			t.Fatalf("%d bits: read back as %+v, %d frames", bits, format, src.Len())
		}
		out := make([]float32, 2*n)
		got, _ := src.Read(out)
		lsb := math.Pow(2, -float64(bits-1))
		var worst float64
		for i := range 2 * got {
			worst = max(worst, math.Abs(float64(out[i]-in[i])))
		}
		// Rounding is half an LSB off at most; dither adds up to one more.
		if bits != 32 && worst > 1.6*lsb || bits == 32 && worst != 0 {
			t.Fatalf("%d bits: a sample came back %.3g off, %.2f LSB", bits, worst, worst/lsb)
		}
	}
}

func TestDitherIsTriangularNoiseAroundAnLSB(t *testing.T) {
	var f bufFile
	w, _ := NewWAVWriter(&f, 44100, 16, true)
	if err := w.Write(make([]float32, 2*44100)); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	src, _, _ := DecodeNative(bytes.NewReader(f.b))
	out := make([]float32, 2*44100)
	n, _ := src.Read(out)
	var sum float64
	for _, v := range out[:2*n] {
		sum += float64(v) * float64(v)
	}
	rms := math.Sqrt(sum/float64(2*n)) / math.Pow(2, -15)
	// Silence dithered, rounded: about 0.6 of an LSB.
	if rms < 0.4 || rms > 0.8 {
		t.Fatalf("dithered silence is %.2f LSB, want about 0.6", rms)
	}
}

func TestAWAVsTagsFollowItsSoundAndAreCounted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tagged.wav")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWAVWriter(f, 44100, 16, false)
	if err != nil {
		t.Fatal(err)
	}
	w.Tags = []WAVTag{{"INAM", "Night Engine"}, {"IART", "The Oscillators"}, {"IGNR", ""}, {"ITRK", "1"}}
	if err = w.Write(make([]float32, 2*441)); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	b, _ := os.ReadFile(path)
	if got := binary.LittleEndian.Uint32(b[4:]); int(got) != len(b)-8 {
		t.Fatalf("the RIFF size is %d, the file %d bytes after it", got, len(b)-8)
	}
	tail := string(b[WAVHeaderSize(WAVSpec{Bits: 16})+441*4:])
	for _, want := range []string{"LIST", "INFO", "INAM", "Night Engine\x00", "IART", "The Oscillators\x00", "ITRK"} {
		if !strings.Contains(tail, want) {
			t.Fatalf("the tags after the sound lack %q: %q", want, tail)
		}
	}
	if strings.Contains(tail, "IGNR") {
		t.Fatal("an empty tag was written")
	}
	// The file still reads, its sound as long as was written.
	r, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	src, _, err := DecodeNative(r)
	if err != nil || src.Len() != 441 {
		t.Fatalf("read back, the file is %d frames (%v)", src.Len(), err)
	}
}
