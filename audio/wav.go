package audio

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
)

// wav decodes a WAV file, RIFF, RF64 or BW64, of integer samples of 8
// to 32 bits, or float samples of 32, in up to 64 channels.
type wav struct {
	r io.ReadSeeker
	// reader is r, as the io.Reader io.ReadFull takes: converted once,
	// as a conversion each read can allocate.
	reader   io.Reader
	channels int
	sampleHz int
	bits     int
	float    bool
	// data and frames place the samples in the file; at is the next
	// frame.
	data   int64
	frames int64
	at     int64
	buf    []byte
}

// wavChunk is the most frames a read of a WAV file takes at once.
const wavChunk = 4096

const (
	wavPCM        = 1
	wavFloat      = 3
	wavExtensible = 0xfffe
)

func newWAV(r io.ReadSeeker) (*wav, error) {
	l, err := ReadWAVLayout(seekReaderAt{r})
	if err != nil {
		return nil, err
	}
	if _, err := r.Seek(l.Data, io.SeekStart); err != nil {
		return nil, err
	}
	return &wav{r: r, reader: r, channels: l.Channels, sampleHz: l.Rate, bits: l.Bits, float: l.Float,
		data: l.Data, frames: l.Frames,
		// Room for the most a read takes, made now so reading makes
		// nothing.
		buf: make([]byte, wavChunk*l.Channels*l.Bits/8)}, nil
}

func (w *wav) read(dst []float32) (int, error) {
	if w.at >= w.frames {
		return 0, io.EOF
	}
	bytesPer := w.bits / 8
	frameBytes := bytesPer * w.channels
	n := min(int64(len(dst)/2), w.frames-w.at, wavChunk)
	b := w.buf[:int(n)*frameBytes]
	got, err := io.ReadFull(w.reader, b)
	frames := got / frameBytes
	for i := range frames {
		f := b[i*frameBytes:]
		l := w.sample(f)
		r := l
		if w.channels > 1 {
			r = w.sample(f[bytesPer:])
		}
		dst[2*i], dst[2*i+1] = l, r
	}
	w.at += int64(frames)
	if err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			// A file cut short ends where it stops.
			w.frames = w.at
			return frames, io.EOF
		}
		return frames, err
	}
	if w.at >= w.frames {
		return frames, io.EOF
	}
	return frames, nil
}

// sample returns the sample b starts with, from -1 to 1.
func (w *wav) sample(b []byte) float32 {
	if w.float {
		return math.Float32frombits(binary.LittleEndian.Uint32(b))
	}
	switch w.bits {
	case 8:
		return (float32(b[0]) - 128) / 128
	case 16:
		return float32(int16(binary.LittleEndian.Uint16(b))) / (1 << 15)
	case 24:
		v := int32(uint32(b[0])<<8|uint32(b[1])<<16|uint32(b[2])<<24) >> 8
		return float32(v) / (1 << 23)
	}
	return float32(int32(binary.LittleEndian.Uint32(b))) / (1 << 31)
}

func (w *wav) seek(f int64) error {
	f = max(0, min(f, w.frames))
	if _, err := w.r.Seek(w.data+f*int64(w.channels*w.bits/8), io.SeekStart); err != nil {
		return err
	}
	w.at = f
	return nil
}

func (w *wav) len() int64 { return w.frames }
func (w *wav) rate() int  { return w.sampleHz }
func (w *wav) info() Format {
	return Format{Name: "WAV", SampleRate: w.sampleHz, Channels: w.channels, Bits: w.bits}
}
