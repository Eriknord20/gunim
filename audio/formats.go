package audio

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"

	"github.com/hajimehoshi/go-mp3"
	"github.com/jfreymuth/oggvorbis"
	"github.com/mewkiz/flac"
)

// mp3Decoder decodes MP3, which go-mp3 gives as 16-bit stereo. Where
// the encoder says how much silence it added at either end, as LAME
// does in the file's first frame, the decoder leaves it out, so the
// tracks of an album play on without a gap.
type mp3Decoder struct {
	d   *mp3.Decoder
	buf []byte
	// skip is how many frames go-mp3 gives before the sound starts, and
	// total how many the sound has, or -1 when the file does not say.
	// at is the next frame, counted from the sound's start.
	skip, total, at int64
}

// mp3DecoderDelay is how many samples an MP3 decoder's filters delay
// the sound by.
const mp3DecoderDelay = 529

func newMP3(r io.ReadSeeker) (*mp3Decoder, error) {
	skip, total := mp3Gapless(r)
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	d, err := mp3.NewDecoder(r)
	if err != nil {
		return nil, err
	}
	m := &mp3Decoder{d: d, skip: skip, total: total}
	if skip > 0 {
		if err := m.seek(0); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// mp3Gapless reads the encoder's delay and padding from an MP3's first
// frame, a Xing or Info frame with LAME's tag, and returns how many
// frames to skip and how many the sound has. It returns 0 and -1 for a
// file with no such frame.
func mp3Gapless(r io.ReadSeeker) (skip, total int64) {
	var id3 [10]byte
	start := int64(0)
	if _, err := io.ReadFull(r, id3[:]); err == nil && string(id3[:3]) == "ID3" {
		size := int64(id3[6]&0x7f)<<21 | int64(id3[7]&0x7f)<<14 | int64(id3[8]&0x7f)<<7 | int64(id3[9]&0x7f)
		start = 10 + size
		if id3[5]&0x10 != 0 {
			start += 10 // a footer
		}
	}
	if _, err := r.Seek(start, io.SeekStart); err != nil {
		return 0, -1
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(r, head)
	head = head[:n]
	if len(head) < 4 || head[0] != 0xff || head[1]&0xe0 != 0xe0 {
		return 0, -1
	}
	// MPEG-1 frames hold 1152 samples, MPEG-2 and 2.5 frames 576.
	spf := int64(1152)
	if head[1]&0x18 != 0x18 {
		spf = 576
	}
	i := bytes.Index(head, []byte("Xing"))
	if i < 0 {
		i = bytes.Index(head, []byte("Info"))
	}
	if i < 0 || i+8 > len(head) {
		return 0, -1
	}
	flags := binary.BigEndian.Uint32(head[i+4:])
	p := i + 8
	frames := int64(-1)
	if flags&1 != 0 && p+4 <= len(head) {
		frames = int64(binary.BigEndian.Uint32(head[p:]))
		p += 4
	}
	if flags&2 != 0 {
		p += 4
	}
	if flags&4 != 0 {
		p += 100
	}
	if flags&8 != 0 {
		p += 4
	}
	// The tag frame itself decodes as a frame of silence.
	skip = spf + mp3DecoderDelay
	if p+24 > len(head) || frames < 0 {
		return skip, -1
	}
	tag := head[p+21 : p+24]
	delay := int64(tag[0])<<4 | int64(tag[1])>>4
	padding := int64(tag[1]&0x0f)<<8 | int64(tag[2])
	total = frames*spf - delay - padding
	if total <= 0 {
		return spf + mp3DecoderDelay, -1
	}
	return skip + delay, total
}

func (m *mp3Decoder) read(dst []float32) (int, error) {
	want := int64(min(len(dst)/2, 4096))
	if m.total >= 0 {
		want = min(want, m.total-m.at)
		if want <= 0 {
			return 0, io.EOF
		}
	}
	if cap(m.buf) < int(want)*4 {
		m.buf = make([]byte, want*4)
	}
	b := m.buf[:want*4]
	got, err := io.ReadFull(m.d, b)
	frames := got / 4
	for i := range frames {
		dst[2*i] = float32(int16(binary.LittleEndian.Uint16(b[4*i:]))) / (1 << 15)
		dst[2*i+1] = float32(int16(binary.LittleEndian.Uint16(b[4*i+2:]))) / (1 << 15)
	}
	m.at += int64(frames)
	if errors.Is(err, io.ErrUnexpectedEOF) {
		err = io.EOF
	}
	if err == nil && m.total >= 0 && m.at >= m.total {
		err = io.EOF
	}
	return frames, err
}

func (m *mp3Decoder) seek(f int64) error {
	if _, err := m.d.Seek((f+m.skip)*4, io.SeekStart); err != nil {
		return err
	}
	m.at = f
	return nil
}

func (m *mp3Decoder) len() int64 {
	if m.total >= 0 {
		return m.total
	}
	if l := m.d.Length(); l >= 0 {
		return max(0, l/4-m.skip)
	}
	return -1
}

func (m *mp3Decoder) rate() int { return m.d.SampleRate() }

func (m *mp3Decoder) info() Format { return Format{Name: "MP3", SampleRate: m.rate()} }

// vorbisDecoder decodes Ogg Vorbis.
type vorbisDecoder struct {
	r   *oggvorbis.Reader
	buf []float32
}

func newVorbis(r io.ReadSeeker) (*vorbisDecoder, error) {
	v, err := oggvorbis.NewReader(r)
	if err != nil {
		return nil, err
	}
	return &vorbisDecoder{r: v}, nil
}

func (v *vorbisDecoder) read(dst []float32) (int, error) {
	ch := v.r.Channels()
	want := min(len(dst)/2, 4096) * ch
	if cap(v.buf) < want {
		v.buf = make([]float32, want)
	}
	got, err := v.r.Read(v.buf[:want])
	return toStereo(dst, v.buf[:got/ch*ch], ch), err
}

func (v *vorbisDecoder) seek(f int64) error { return v.r.SetPosition(f) }
func (v *vorbisDecoder) len() int64         { return v.r.Length() }
func (v *vorbisDecoder) rate() int          { return v.r.SampleRate() }
func (v *vorbisDecoder) info() Format {
	return Format{Name: "Ogg Vorbis", SampleRate: v.rate(), Channels: v.r.Channels()}
}

// flacDecoder decodes FLAC a frame of the file at a time.
type flacDecoder struct {
	s *flac.Stream
	// pending holds the frames of the file's last frame not yet read,
	// as stereo, from at.
	pending []float32
	at      int
	scale   float32
}

func newFLAC(r io.ReadSeeker) (*flacDecoder, error) {
	r, err := healFLACSeeker(r)
	if err != nil {
		return nil, err
	}
	s, err := flac.NewSeek(r)
	if err != nil {
		return nil, err
	}
	bits := max(1, int(s.Info.BitsPerSample))
	return &flacDecoder{s: s, scale: 1 / float32(int64(1)<<(bits-1))}, nil
}

func (d *flacDecoder) read(dst []float32) (int, error) {
	if d.at >= len(d.pending)/2 {
		f, err := d.s.ParseNext()
		if err != nil {
			return 0, err
		}
		n := int(f.BlockSize)
		if cap(d.pending) < 2*n {
			d.pending = make([]float32, 2*n)
		}
		d.pending, d.at = d.pending[:2*n], 0
		left := f.Subframes[0].Samples
		right := left
		if len(f.Subframes) > 1 {
			right = f.Subframes[1].Samples
		}
		for i := range n {
			d.pending[2*i] = float32(left[i]) * d.scale
			d.pending[2*i+1] = float32(right[i]) * d.scale
		}
	}
	n := copy(dst[:len(dst)/2*2], d.pending[2*d.at:]) / 2
	d.at += n
	return n, nil
}

func (d *flacDecoder) seek(f int64) error {
	got, err := d.s.Seek(uint64(max(0, f)))
	if err != nil {
		return err
	}
	// The file seeks to the start of the frame holding f; the frames
	// before f in it are skipped.
	d.pending, d.at = d.pending[:0], 0
	skip := int(f - int64(got))
	var buf [2 * 1024]float32
	for skip > 0 {
		n, err := d.read(buf[:2*min(skip, 1024)])
		if err != nil {
			return err
		}
		skip -= n
	}
	return nil
}

func (d *flacDecoder) len() int64 {
	if d.s.Info.NSamples == 0 {
		return -1
	}
	return int64(d.s.Info.NSamples)
}

func (d *flacDecoder) rate() int { return int(d.s.Info.SampleRate) }

func (d *flacDecoder) info() Format {
	return Format{Name: "FLAC", SampleRate: d.rate(), Channels: int(d.s.Info.NChannels), Bits: int(d.s.Info.BitsPerSample)}
}
