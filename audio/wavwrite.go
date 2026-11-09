package audio

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"math/rand/v2"
)

// A WAVWriter writes stereo sound to a WAV file: 16 or 24 bit integer
// samples, or 32 bit float. Its header is [WAVHeader]'s: the file is
// RIFF, with a JUNK chunk keeping room for a ds64 chunk, and turns RF64
// as it closes where it passed 4 GB. Dither, for integer samples, adds noise of
// a triangular distribution an LSB either way before each sample is
// rounded, so a quiet fade's last bits fade into a soft hiss rather
// than break into distortion, as rounding alone does.
type WAVWriter struct {
	w      io.WriteSeeker
	rate   int
	bits   int
	dither bool
	frames int64
	buf    []byte
	rng    *rand.Rand
	// Clipped counts the samples written past full scale, which were
	// held to it.
	Clipped int64
	// Tags are written after the sound as the file closes, as a LIST
	// INFO chunk players read: the track's name as INAM, its artist as
	// IART, its album as IPRD, its number as ITRK, its year as ICRD, its
	// genre as IGNR. Empty values are left out.
	Tags []WAVTag
	// extra is the size of the chunks after the sound.
	extra int64
}

// A WAVTag is a tag of a WAV file: its four-letter ID and its value.
type WAVTag struct {
	ID, Value string
}

// NewWAVWriter starts a WAV file of sound at rate, in samples of bits:
// 16, 24, or 32 for float. Close finishes it.
func NewWAVWriter(w io.WriteSeeker, rate, bits int, dither bool) (*WAVWriter, error) {
	if bits != 16 && bits != 24 && bits != 32 {
		return nil, errors.New("audio: wav: 16, 24 or 32 bits")
	}
	ww := &WAVWriter{w: w, rate: rate, bits: bits, dither: dither && bits != 32,
		rng: rand.New(rand.NewPCG(0x6775, 0x6e69))}
	if err := ww.header(); err != nil {
		return nil, err
	}
	return ww, nil
}

// header writes the file's header, with the sizes of the frames so far.
func (w *WAVWriter) header() error {
	if _, err := w.w.Seek(0, io.SeekStart); err != nil {
		return err
	}
	s := WAVSpec{Rate: w.rate, Channels: 2, Bits: w.bits, Float: w.bits == 32}
	w.buf = appendWAVHeader(w.buf[:0], s, w.frames, w.extra)
	_, err := w.w.Write(w.buf)
	return err
}

// Write writes frames, interleaved stereo, at the file's rate.
func (w *WAVWriter) Write(frames []float32) error {
	bytes := w.bits / 8
	n := len(frames) / 2 * 2
	w.buf = w.buf[:0]
	// Full scale is 2^(bits-1), as readers divide by.
	scale := float64(int64(1) << (w.bits - 1))
	for _, s := range frames[:n] {
		if w.bits == 32 {
			w.buf = binary.LittleEndian.AppendUint32(w.buf, math.Float32bits(s))
			continue
		}
		x, clipped := quantize(s, scale, w.dither, w.rng)
		if clipped {
			w.Clipped++
		}
		switch bytes {
		case 2:
			w.buf = binary.LittleEndian.AppendUint16(w.buf, uint16(int16(x)))
		case 3:
			w.buf = append(w.buf, byte(x), byte(x>>8), byte(x>>16))
		}
	}
	if _, err := w.w.Write(w.buf); err != nil {
		return err
	}
	w.frames += int64(n / 2)
	return nil
}

// Close writes the tags, and the file's sizes into its header: RIFF up
// to 4 GB, RF64 past it. It leaves the file open.
func (w *WAVWriter) Close() error {
	if list := w.list(); len(list) > 0 {
		if _, err := w.w.Write(list); err != nil {
			return err
		}
		w.extra = int64(len(list))
	}
	end, err := w.w.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if headErr := w.header(); headErr != nil {
		return headErr
	}
	_, err = w.w.Seek(end, io.SeekStart)
	return err
}

// list is the LIST INFO chunk of the tags, or nothing for none.
func (w *WAVWriter) list() []byte {
	var body []byte
	for _, t := range w.Tags {
		if len(t.ID) != 4 || t.Value == "" {
			continue
		}
		v := append([]byte(t.Value), 0)
		body = append(body, t.ID...)
		body = binary.LittleEndian.AppendUint32(body, uint32(len(v)))
		body = append(body, v...)
		if len(v)%2 == 1 {
			body = append(body, 0)
		}
	}
	if len(body) == 0 {
		return nil
	}
	out := []byte("LIST")
	out = binary.LittleEndian.AppendUint32(out, uint32(4+len(body)))
	out = append(out, "INFO"...)
	return append(out, body...)
}
