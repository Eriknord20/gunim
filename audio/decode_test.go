package audio

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"runtime"
	"testing"
)

// readAll reads src to its end.
func readAll(t *testing.T, src Source) []float32 {
	t.Helper()
	c, err := ReadClip(src)
	if err != nil {
		t.Fatal(err)
	}
	return c.Samples()
}

// pitch returns the pitch of channel ch of frames, from how often it
// crosses zero going up, over the middle half.
func pitch(frames []float32, ch int) float64 {
	n := len(frames) / 2
	from, to := n/4, 3*n/4
	ups := 0
	first, last := -1, -1
	for i := from + 1; i < to; i++ {
		if frames[2*(i-1)+ch] < 0 && frames[2*i+ch] >= 0 {
			if first < 0 {
				first = i
			}
			last = i
			ups++
		}
	}
	if ups < 2 {
		return 0
	}
	return float64(ups-1) * SampleRate / float64(last-first)
}

func near(got, want, tol float64) bool { return math.Abs(got-want) <= tol }

func TestEachFormatPlaysItsPitchesInTheirChannelsAtTheMixersRate(t *testing.T) {
	for _, name := range []string{"tone.mp3", "tone.ogg", "tone.flac"} {
		t.Run(name, func(t *testing.T) {
			f, err := os.Open("testdata/" + name)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = f.Close() }()
			s, err := Decode(f)
			if err != nil {
				t.Fatal(err)
			}
			frames := readAll(t, s)
			// Half a second: the silence an encoder adds at either end
			// left out.
			if n := len(frames) / 2; !near(float64(n), SampleRate/2, 2) {
				t.Errorf("%d frames, want about %d", n, SampleRate/2)
			}
			if l := s.Len(); l >= 0 && !near(float64(l), float64(len(frames)/2), 2) {
				t.Errorf("Len %d, want about %d", l, SampleRate/2)
			}
			// The sound starts at once, with no encoder's silence
			// before it.
			first := 0
			for first < len(frames)/2 && math.Abs(float64(frames[2*first])) < 0.02 {
				first++
			}
			if first > 8 {
				t.Errorf("the sound starts at frame %d, want at once", first)
			}
			if p := pitch(frames, 0); !near(p, 440, 3) {
				t.Errorf("left at %.1f Hz, want 440", p)
			}
			if p := pitch(frames, 1); !near(p, 880, 4) {
				t.Errorf("right at %.1f Hz, want 880", p)
			}
		})
	}
}

func TestSeekingPlaysOnFromThere(t *testing.T) {
	for _, name := range []string{"tone.mp3", "tone.ogg", "tone.flac"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile("testdata/" + name)
			if err != nil {
				t.Fatal(err)
			}
			s, err := Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			whole := readAll(t, s)
			if err := s.SeekFrame(SampleRate / 4); err != nil {
				t.Fatal(err)
			}
			rest := readAll(t, s)
			want := len(whole)/2 - SampleRate/4
			if !near(float64(len(rest)/2), float64(want), 2) {
				t.Errorf("%d frames after seeking to the middle, want %d", len(rest)/2, want)
			}
			// The frames after the seek are the frames from there,
			// once an MP3's frames, which overlap, have had one to
			// lean on.
			var worst float64
			for i := 3000; i < min(len(rest)/2, 6000); i++ {
				worst = max(worst, math.Abs(float64(rest[2*i]-whole[2*(SampleRate/4+i)])))
			}
			if worst > 0.02 {
				t.Errorf("after a seek the sound differs from the same place played through by %.3f", worst)
			}
		})
	}
}

// wavFile returns a WAV file of frames, at rate, with ch channels of
// bits bits, float when float is set, and an odd-sized chunk before
// the samples.
func wavFile(rate, ch, bits int, float bool, frames [][]float64) []byte {
	var data bytes.Buffer
	for _, f := range frames {
		for c := range ch {
			v := f[c]
			switch {
			case float:
				_ = binary.Write(&data, binary.LittleEndian, float32(v))
			case bits == 8:
				data.WriteByte(byte(int(v*127) + 128))
			case bits == 16:
				_ = binary.Write(&data, binary.LittleEndian, int16(v*32767))
			case bits == 24:
				x := int32(v * 8388607)
				data.Write([]byte{byte(x), byte(x >> 8), byte(x >> 16)})
			default:
				_ = binary.Write(&data, binary.LittleEndian, int32(v*2147483647))
			}
		}
	}
	format := uint16(1)
	if float {
		format = 3
	}
	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(4+8+16+8+3+1+8+data.Len()))
	b.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), format, uint16(ch), uint32(rate),
		uint32(rate * ch * bits / 8), uint16(ch * bits / 8), uint16(bits)} {
		_ = binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("LIST")
	_ = binary.Write(&b, binary.LittleEndian, uint32(3))
	b.Write([]byte{1, 2, 3, 0})
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(data.Len()))
	b.Write(data.Bytes())
	return b.Bytes()
}

func TestWAVReadsEachSampleFormat(t *testing.T) {
	frames := [][]float64{{0.5, -0.25}, {-0.5, 0.25}, {0, 1}}
	for _, c := range []struct {
		name     string
		ch, bits int
		float    bool
		tol      float64
	}{
		{"8-bit", 2, 8, false, 0.01},
		{"16-bit", 2, 16, false, 1e-4},
		{"24-bit", 2, 24, false, 1e-6},
		{"32-bit", 2, 32, false, 1e-6},
		{"float", 2, 32, true, 1e-7},
		{"mono", 1, 16, false, 1e-4},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, err := Decode(bytes.NewReader(wavFile(SampleRate, c.ch, c.bits, c.float, frames)))
			if err != nil {
				t.Fatal(err)
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

func TestAWAVAtAnotherRateKeepsItsPitchAndLength(t *testing.T) {
	const rate = 22050
	frames := make([][]float64, rate)
	for i := range frames {
		v := 0.8 * math.Sin(2*math.Pi*1000*float64(i)/rate)
		frames[i] = []float64{v, -v}
	}
	s, err := Decode(bytes.NewReader(wavFile(rate, 2, 16, false, frames)))
	if err != nil {
		t.Fatal(err)
	}
	if s.Len() != SampleRate {
		t.Errorf("Len %d, want a second, %d", s.Len(), SampleRate)
	}
	got := readAll(t, s)
	if n := len(got) / 2; !near(float64(n), SampleRate, 3) {
		t.Errorf("%d frames, want a second, %d", n, SampleRate)
	}
	if p := pitch(got, 0); !near(p, 1000, 2) {
		t.Errorf("at %.1f Hz, want 1000", p)
	}
	// The curve between the file's frames stays a sine: no sample lies
	// far from the sine at its time.
	var worst float64
	for i := 10; i < len(got)/2-10; i++ {
		want := 0.8 * math.Sin(2*math.Pi*1000*float64(i)/SampleRate)
		worst = max(worst, math.Abs(float64(got[2*i])-want))
	}
	if worst > 0.01 {
		t.Errorf("resampled sine strays from the sine by %.4f", worst)
	}
}

func TestDecodeRefusesWhatItCannotRead(t *testing.T) {
	_, err := Decode(bytes.NewReader([]byte("hello, this is text")))
	if !errors.Is(err, ErrFormat) {
		t.Errorf("Decode of text: %v, want ErrFormat", err)
	}
	_, err = Decode(bytes.NewReader(nil))
	if !errors.Is(err, ErrFormat) && !errors.Is(err, io.EOF) {
		t.Errorf("Decode of nothing: %v", err)
	}
}

// TestReadingAWAVMakesNothing reads a WAV in reads of every size,
// counting every allocation, as reading may run where sound is made.
func TestReadingAWAVMakesNothing(t *testing.T) {
	if raceOn {
		t.Skip("the race detector allocates of its own")
	}
	frames := make([][]float64, 30000)
	for i := range frames {
		frames[i] = []float64{0.1, -0.1}
	}
	s, _, err := DecodeNative(bytes.NewReader(wavFile(SampleRate, 2, 24, false, frames)))
	if err != nil {
		t.Fatal(err)
	}
	dst := make([]float32, 2*8192)
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for _, n := range []int{100, 1024, 4096, 8192, 7} {
		if _, err := s.Read(dst[:2*n]); err != nil {
			t.Fatal(err)
		}
	}
	runtime.ReadMemStats(&after)
	if n := after.Mallocs - before.Mallocs; n != 0 {
		t.Fatalf("reading made %d things", n)
	}
}
