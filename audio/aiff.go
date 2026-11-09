package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

// An AIFF file is Apple's WAV: a FORM of chunks, big-endian. Its COMM
// chunk says how the samples are kept, its SSND chunk holds them. AIFF-C
// adds to COMM a compression type, which for the files music programs
// write is no compression at all: big-endian integers ("NONE", "twos"),
// little-endian ones ("sowt"), or floats ("fl32", "fl64").

// ErrAIFFHeader is an AIFF file whose header is cut short or makes no
// sense. It is an [ErrFormat] too.
var ErrAIFFHeader = fmt.Errorf("%w: aiff: the header is cut short or broken", ErrFormat)

// aiff decodes an AIFF or AIFF-C file of integer samples of 1 to 32
// bits, or float samples of 32 or 64, in up to 64 channels.
type aiff struct {
	r io.ReadSeeker
	// reader is r, as the io.Reader io.ReadFull takes: converted once,
	// as a conversion each read can allocate.
	reader   io.Reader
	channels int
	sampleHz int
	bits     int
	// width is the bytes a sample takes; little says they are
	// little-endian, and float that they are floats.
	width  int
	little bool
	float  bool
	// data and frames place the samples in the file; at is the next
	// frame.
	data   int64
	frames int64
	at     int64
	buf    []byte
}

func newAIFF(r io.ReadSeeker) (*aiff, error) {
	a, err := readAIFFHeader(seekReaderAt{r})
	if err != nil {
		return nil, err
	}
	if _, err := r.Seek(a.data, io.SeekStart); err != nil {
		return nil, err
	}
	a.r, a.reader = r, r
	// Room for the most a read takes, made now so reading makes nothing.
	a.buf = make([]byte, wavChunk*a.channels*a.width)
	return a, nil
}

// readAIFFHeader reads the chunks of the AIFF file r holds, which may
// come in any order, for its COMM and SSND chunks.
//
// Its frames are as many as the header tells of. A file cut short of
// them ends where it stops, as it is read. An SSND chunk whose size is
// 0 or 0xFFFFFFFF, as a program writing to a stream leaves it, runs to
// the end of the file, where r tells its size.
func readAIFFHeader(r io.ReaderAt) (*aiff, error) {
	var form [12]byte
	if err := readAIFFAt(r, form[:], 0); err != nil {
		if errors.Is(err, ErrAIFFHeader) {
			return nil, ErrFormat
		}
		return nil, err
	}
	kind := string(form[8:])
	if string(form[:4]) != "FORM" || kind != "AIFF" && kind != "AIFC" {
		return nil, ErrFormat
	}
	be := binary.BigEndian
	a := &aiff{}
	haveComm, haveSound := false, false
	// sound is the SSND chunk's samples' size.
	var sound int64
	for at := int64(12); !haveComm || !haveSound; {
		var h [8]byte
		if err := readAIFFAt(r, h[:], at); err != nil {
			switch {
			case !errors.Is(err, ErrAIFFHeader):
				return nil, err
			case !haveComm:
				return nil, fmt.Errorf("%w: no COMM chunk", ErrAIFFHeader)
			}
			return nil, fmt.Errorf("%w: no SSND chunk", ErrAIFFHeader)
		}
		id, body, size := string(h[:4]), at+8, int64(be.Uint32(h[4:]))
		switch id {
		case "COMM":
			if err := a.readComm(r, body, size, kind == "AIFC"); err != nil {
				return nil, err
			}
			haveComm = true
		case "SSND":
			var s [4]byte
			if err := readAIFFAt(r, s[:], body); err != nil {
				return nil, err
			}
			// The samples start after the chunk's offset and block
			// size, and the offset's bytes of padding.
			offset := int64(be.Uint32(s[:]))
			a.data, haveSound = body+8+offset, true
			if size == 0 || size == math.MaxUint32 {
				end, ok := readerSize(r)
				if !ok {
					return nil, fmt.Errorf("%w: an SSND chunk of no size", ErrAIFFHeader)
				}
				// The file ends with the samples.
				size = end - body
				sound = size - 8 - offset
				break
			}
			sound = size - 8 - offset
		}
		at = body + size + size&1
	}
	a.frames = min(a.frames, max(0, sound)/int64(a.channels*a.width))
	return a, nil
}

// readComm reads the COMM chunk of size at body, of an AIFF-C file where
// c says.
func (a *aiff) readComm(r io.ReaderAt, body, size int64, c bool) error {
	want := int64(18)
	if c {
		want = 22
	}
	if size < want {
		return fmt.Errorf("%w: a COMM chunk of %d bytes", ErrAIFFHeader, size)
	}
	var b [22]byte
	if err := readAIFFAt(r, b[:want], body); err != nil {
		return err
	}
	be := binary.BigEndian
	a.channels = int(int16(be.Uint16(b[0:])))
	a.frames = int64(be.Uint32(b[2:]))
	a.bits = int(int16(be.Uint16(b[6:])))
	rate := extended(b[8:18])
	if !(rate >= 1 && rate < 1<<31) {
		return fmt.Errorf("%w: a sample rate of %g Hz", ErrAIFFHeader, rate)
	}
	a.sampleHz = int(math.Round(rate))
	if a.channels < 1 {
		return fmt.Errorf("%w: %d channels", ErrAIFFHeader, a.channels)
	}
	if a.channels > wavMaxChannels {
		return fmt.Errorf("%w: aiff: %d channels, more than %d", ErrSampleFormat, a.channels, wavMaxChannels)
	}
	compression := "NONE"
	if c {
		compression = string(b[18:22])
	}
	switch compression {
	case "NONE", "twos", "in24", "in32":
	case "sowt", "23ni":
		a.little = true
	case "fl32", "FL32":
		a.float, a.bits = true, 32
	case "fl64", "FL64":
		a.float, a.bits = true, 64
	default:
		return fmt.Errorf("%w: aiff: compression %q", ErrSampleFormat, compression)
	}
	if !a.float && (a.bits < 1 || a.bits > 32) {
		return fmt.Errorf("%w: aiff: integer samples of %d bits", ErrSampleFormat, a.bits)
	}
	a.width = (a.bits + 7) / 8
	return nil
}

// extended returns the 80-bit IEEE extended float b holds, as AIFF keeps
// its sample rate.
func extended(b []byte) float64 {
	exp := int(binary.BigEndian.Uint16(b) & 0x7fff)
	mant := binary.BigEndian.Uint64(b[2:])
	if exp == 0 && mant == 0 {
		return 0
	}
	v := math.Ldexp(float64(mant), exp-16383-63)
	if b[0]&0x80 != 0 {
		v = -v
	}
	return v
}

// readAIFFAt reads b from r at off; a file ending first is
// [ErrAIFFHeader], and another error is returned as it is.
func readAIFFAt(r io.ReaderAt, b []byte, off int64) error {
	if err := readWAVAt(r, b, off); err != nil {
		if errors.Is(err, ErrWAVHeader) {
			return ErrAIFFHeader
		}
		return err
	}
	return nil
}

func (a *aiff) read(dst []float32) (int, error) {
	if a.at >= a.frames {
		return 0, io.EOF
	}
	frameBytes := a.width * a.channels
	n := min(int64(len(dst)/2), a.frames-a.at, wavChunk)
	b := a.buf[:int(n)*frameBytes]
	got, err := io.ReadFull(a.reader, b)
	frames := got / frameBytes
	for i := range frames {
		f := b[i*frameBytes:]
		l := a.sample(f)
		r := l
		if a.channels > 1 {
			r = a.sample(f[a.width:])
		}
		dst[2*i], dst[2*i+1] = l, r
	}
	a.at += int64(frames)
	if err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			a.frames = a.at
			return frames, io.EOF
		}
		return frames, err
	}
	if a.at >= a.frames {
		return frames, io.EOF
	}
	return frames, nil
}

// sample returns the sample b starts with, from -1 to 1.
func (a *aiff) sample(b []byte) float32 {
	if a.float {
		if a.bits == 64 {
			return float32(math.Float64frombits(binary.BigEndian.Uint64(b)))
		}
		return math.Float32frombits(binary.BigEndian.Uint32(b))
	}
	// An integer sample is signed and left-justified in its bytes, so
	// laid at the top of 32 bits it is in scale whatever its width.
	var v uint32
	for i := range a.width {
		k := i
		if a.little {
			k = a.width - 1 - i
		}
		v |= uint32(b[k]) << (24 - 8*i)
	}
	return float32(int32(v)) / (1 << 31)
}

func (a *aiff) seek(f int64) error {
	f = max(0, min(f, a.frames))
	if _, err := a.r.Seek(a.data+f*int64(a.channels*a.width), io.SeekStart); err != nil {
		return err
	}
	a.at = f
	return nil
}

func (a *aiff) len() int64 { return a.frames }
func (a *aiff) rate() int  { return a.sampleHz }
func (a *aiff) info() Format {
	return Format{Name: "AIFF", SampleRate: a.sampleHz, Channels: a.channels, Bits: a.bits}
}
