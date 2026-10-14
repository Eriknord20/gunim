package asio

import (
	"encoding/binary"
	"math"
	"testing"
)

// TestRoundedSoundComesThroughExactly writes sound rounded to 16 bits
// into every integer type of at least 16 bits, and reads each sample
// back as the same step.
func TestRoundedSoundComesThroughExactly(t *testing.T) {
	steps := []int32{-32768, -32767, -1234, -1, 0, 1, 77, 32766, 32767}
	frames := make([]float32, 2*len(steps))
	for i, k := range steps {
		frames[2*i] = float32(k) / 32768
		frames[2*i+1] = -frames[2*i]
	}
	for _, typ := range []sampleType{int16LSB, int24LSB, int32LSB, int32LSB16, int32LSB18, int32LSB20, int32LSB24} {
		bytes, bits := typ.size()
		dst := make([]byte, bytes*len(steps))
		write(dst, frames, 0, 2, typ)
		for i, k := range steps {
			var got int32
			switch bytes {
			case 2:
				got = int32(int16(binary.LittleEndian.Uint16(dst[2*i:])))
			case 3:
				got = int32(uint32(dst[3*i])|uint32(dst[3*i+1])<<8|uint32(dst[3*i+2])<<16) << 8 >> 8
			case 4:
				got = int32(binary.LittleEndian.Uint32(dst[4*i:]))
			}
			if want := k << (bits - 16); got != want {
				t.Errorf("type %d: step %d came out %d, want %d", typ, k, got, want)
			}
		}
	}
}

// TestFullScaleHolds writes past full scale, and reads the samples held
// at the top and bottom of the range.
func TestFullScaleHolds(t *testing.T) {
	frames := []float32{1.5, -1.5}
	dst := make([]byte, 2)
	write(dst, frames, 0, 2, int16LSB)
	if got := int16(binary.LittleEndian.Uint16(dst)); got != 32767 {
		t.Errorf("1.5 came out %d, want 32767", got)
	}
	write(dst, frames, 1, 2, int16LSB)
	if got := int16(binary.LittleEndian.Uint16(dst)); got != -32768 {
		t.Errorf("-1.5 came out %d, want -32768", got)
	}
}

// TestFloatsComeThroughAsTheyAre writes both float types and reads the
// samples back unchanged.
func TestFloatsComeThroughAsTheyAre(t *testing.T) {
	frames := []float32{0.123456789, -0.5, 1, -1}
	d32 := make([]byte, 8)
	write(d32, frames, 1, 2, float32LSB)
	if got := math.Float32frombits(binary.LittleEndian.Uint32(d32[4:])); got != -1 {
		t.Errorf("float32: got %v, want -1", got)
	}
	d64 := make([]byte, 16)
	write(d64, frames, 0, 2, float64LSB)
	if got := math.Float64frombits(binary.LittleEndian.Uint64(d64)); got != float64(float32(0.123456789)) {
		t.Errorf("float64: got %v", got)
	}
}

// TestMonoAverages folds two channels into the left one.
func TestMonoAverages(t *testing.T) {
	frames := []float32{1, 0, 0.5, -0.5}
	mono(frames)
	if frames[0] != 0.5 || frames[2] != 0 {
		t.Errorf("mono gave %v", frames)
	}
}

// TestNaNIsSilence writes a sample that is no number as silence.
func TestNaNIsSilence(t *testing.T) {
	nan := float32(math.NaN())
	dst := make([]byte, 4)
	write(dst, []float32{nan, nan}, 0, 2, int32LSB)
	if got := int32(binary.LittleEndian.Uint32(dst)); got != 0 {
		t.Errorf("NaN came out %d", got)
	}
}

// TestInputsReadBackWhatWasWritten writes rounded sound as every
// sample type, and reads it into one channel of three, as an input
// comes in.
func TestInputsReadBackWhatWasWritten(t *testing.T) {
	steps := []int32{-32768, -32767, -1234, -1, 0, 1, 77, 32766, 32767}
	frames := make([]float32, 2*len(steps))
	for i, k := range steps {
		frames[2*i] = float32(k) / 32768
	}
	for _, typ := range []sampleType{int16LSB, int24LSB, int32LSB, float32LSB, float64LSB, int32LSB16, int32LSB18, int32LSB20, int32LSB24} {
		bytes, _ := typ.size()
		src := make([]byte, bytes*len(steps))
		write(src, frames, 0, 2, typ)
		dst := make([]float32, 3*len(steps))
		for i := range dst {
			dst[i] = 9
		}
		read(dst, src, 1, 3, typ)
		for i := range steps {
			if got, want := dst[3*i+1], frames[2*i]; got != want {
				t.Errorf("type %d: frame %d read %v, want %v", typ, i, got, want)
			}
			if dst[3*i] != 9 || dst[3*i+2] != 9 {
				t.Errorf("type %d: frame %d wrote past its channel: %v", typ, i, dst[3*i:3*i+3])
			}
		}
	}
}

// TestWriteTakesItsChannelOfMany writes each channel of six, as many
// outputs take them, each as a sample type of its own, and reads each
// back as its own channel's sound.
func TestWriteTakesItsChannelOfMany(t *testing.T) {
	const chans, n = 6, 5
	types := []sampleType{float32LSB, int16LSB, int24LSB, int32LSB, float64LSB, int32LSB24}
	frames := make([]float32, chans*n)
	for i := range n {
		for ch := range chans {
			// A step of 16 bits, so every type holds it exactly.
			frames[i*chans+ch] = float32(100*ch+i) / 32768
		}
	}
	for ch, typ := range types {
		bytes, _ := typ.size()
		dst := make([]byte, bytes*n)
		write(dst, frames, ch, chans, typ)
		got := make([]float32, n)
		read(got, dst, 0, 1, typ)
		for i, v := range got {
			if want := frames[i*chans+ch]; v != want {
				t.Errorf("channel %d, type %d: frame %d came out %v, want %v", ch, typ, i, v, want)
			}
		}
	}
}
