package asio

import (
	"encoding/binary"
	"math"
)

// sampleType is how a driver's channel takes its samples.
type sampleType int32

// The sample types of ASIO drivers on Windows, little-endian. The
// Int32LSBn types hold n bits in the low end of 32.
const (
	int16LSB   sampleType = 16
	int24LSB   sampleType = 17
	int32LSB   sampleType = 18
	float32LSB sampleType = 19
	float64LSB sampleType = 20
	int32LSB16 sampleType = 24
	int32LSB18 sampleType = 25
	int32LSB20 sampleType = 26
	int32LSB24 sampleType = 27
)

// size returns how many bytes a sample of t takes, and the bits of
// resolution it holds; zero for a type the package plays none of.
func (t sampleType) size() (bytes, bits int) {
	switch t {
	case int16LSB:
		return 2, 16
	case int24LSB:
		return 3, 24
	case int32LSB:
		return 4, 32
	case float32LSB:
		return 4, 32
	case float64LSB:
		return 8, 64
	case int32LSB16:
		return 4, 16
	case int32LSB18:
		return 4, 18
	case int32LSB20:
		return 4, 20
	case int32LSB24:
		return 4, 24
	}
	return 0, 0
}

// float says t holds floating point samples.
func (t sampleType) float() bool { return t == float32LSB || t == float64LSB }

// write writes channel ch of frames, chans interleaved channels a
// frame, into dst as samples of t, one a frame. An integer sample is
// the float times full scale, 2^(bits-1), rounded and held within
// range: a sound already rounded to as many bits, or fewer, comes
// through exactly.
func write(dst []byte, frames []float32, ch, chans int, t sampleType) {
	n := len(frames) / chans
	bytes, bits := t.size()
	if t.float() {
		for i := range n {
			s := frames[i*chans+ch]
			if t == float32LSB {
				binary.LittleEndian.PutUint32(dst[4*i:], math.Float32bits(s))
			} else {
				binary.LittleEndian.PutUint64(dst[8*i:], math.Float64bits(float64(s)))
			}
		}
		return
	}
	scale := math.Ldexp(1, bits-1)
	for i := range n {
		v := math.Round(float64(frames[i*chans+ch]) * scale)
		v = max(-scale, min(v, scale-1))
		if v != v {
			// Not a number: silence.
			v = 0
		}
		x := int32(v)
		at := dst[bytes*i:]
		switch bytes {
		case 2:
			binary.LittleEndian.PutUint16(at, uint16(int16(x)))
		case 3:
			at[0], at[1], at[2] = byte(x), byte(x>>8), byte(x>>16)
		case 4:
			binary.LittleEndian.PutUint32(at, uint32(x))
		}
	}
}

// mono averages frames' two channels into the left one, for a driver
// of one output.
func mono(frames []float32) {
	for i := 0; i+1 < len(frames); i += 2 {
		frames[i] = (frames[i] + frames[i+1]) / 2
	}
}

// read reads one channel's samples of t from src into channel ch of
// dst, frames of chans interleaved channels, one sample a frame. An
// integer sample is divided by full scale, 2^(bits-1), so a sample
// write wrote reads back as the float it was given, within range.
func read(dst []float32, src []byte, ch, chans int, t sampleType) {
	n := len(dst) / chans
	bytes, bits := t.size()
	if t == float32LSB {
		for i := range n {
			dst[i*chans+ch] = math.Float32frombits(binary.LittleEndian.Uint32(src[4*i:]))
		}
		return
	}
	if t == float64LSB {
		for i := range n {
			dst[i*chans+ch] = float32(math.Float64frombits(binary.LittleEndian.Uint64(src[8*i:])))
		}
		return
	}
	scale := float32(1 / math.Ldexp(1, bits-1))
	for i := range n {
		at := src[bytes*i:]
		var x int32
		switch bytes {
		case 2:
			x = int32(int16(binary.LittleEndian.Uint16(at)))
		case 3:
			x = int32(uint32(at[0])|uint32(at[1])<<8|uint32(at[2])<<16) << 8 >> 8
		case 4:
			x = int32(binary.LittleEndian.Uint32(at))
		}
		dst[i*chans+ch] = float32(x) * scale
	}
}
