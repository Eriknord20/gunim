package audio

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

// TestQuantizerRoundsAsTheWAVWriter rounds sound to 16 and 24 bits
// with a Quantizer and with a WAVWriter, undithered, and finds the
// same samples.
func TestQuantizerRoundsAsTheWAVWriter(t *testing.T) {
	frames := make([]float32, 2*1000)
	for i := range frames {
		frames[i] = float32(1.2 * math.Sin(float64(i)*0.0123))
	}
	for _, bits := range []int{16, 24} {
		var f seekBuffer
		w, err := NewWAVWriter(&f, 44100, bits, false)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Write(frames); err != nil {
			t.Fatal(err)
		}
		q := Quantizer{Bits: bits}
		got := append([]float32(nil), frames...)
		q.Process(got)
		data := f.Bytes()[WAVHeaderSize(WAVSpec{Bits: bits}):]
		scale := math.Ldexp(1, bits-1)
		for i, v := range got {
			var x int32
			if bits == 16 {
				x = int32(int16(binary.LittleEndian.Uint16(data[2*i:])))
			} else {
				x = int32(uint32(data[3*i])|uint32(data[3*i+1])<<8|uint32(data[3*i+2])<<16) << 8 >> 8
			}
			if float64(v)*scale != float64(x) {
				t.Fatalf("%d bits, sample %d: the quantizer gave %v, the file %d", bits, i, float64(v)*scale, x)
			}
		}
	}
}

// TestDitheredSamplesKeepToTheSteps rounds with dither and finds every
// sample on a 16-bit step, within two steps of the sound.
func TestDitheredSamplesKeepToTheSteps(t *testing.T) {
	frames := make([]float32, 2*1000)
	for i := range frames {
		frames[i] = float32(0.001 * math.Sin(float64(i)*0.05))
	}
	got := append([]float32(nil), frames...)
	q := Quantizer{Bits: 16, Dither: true}
	q.Process(got)
	moved := false
	for i, v := range got {
		step := float64(v) * 32768
		if step != math.Round(step) {
			t.Fatalf("sample %d, %v, is off the steps", i, step)
		}
		if math.Abs(step-float64(frames[i])*32768) > 2 {
			t.Fatalf("sample %d moved %v steps", i, step-float64(frames[i])*32768)
		}
		if math.Round(float64(frames[i])*32768) != step {
			moved = true
		}
	}
	if !moved {
		t.Error("dither moved no sample from where rounding alone puts it")
	}
}

// seekBuffer is a WriteSeeker in memory.
type seekBuffer struct {
	b   []byte
	pos int
}

func (s *seekBuffer) Write(p []byte) (int, error) {
	if need := s.pos + len(p); need > len(s.b) {
		s.b = append(s.b, make([]byte, need-len(s.b))...)
	}
	copy(s.b[s.pos:], p)
	s.pos += len(p)
	return len(p), nil
}

func (s *seekBuffer) Seek(off int64, whence int) (int64, error) {
	switch whence {
	case 0:
		s.pos = int(off)
	case 1:
		s.pos += int(off)
	case 2:
		s.pos = len(s.b) + int(off)
	}
	return int64(s.pos), nil
}

func (s *seekBuffer) Bytes() []byte { return bytes.Clone(s.b) }
