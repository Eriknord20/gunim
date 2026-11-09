package audio

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
)

// ErrFormat is returned by [Decode] for a sound in no format it reads.
var ErrFormat = errors.New("audio: unknown format")

// Extensions are the file extensions of the sounds [Decode] reads, in
// lower case, each with its dot, for a file picker's patterns. A program
// tells the files it takes by them, so they must not be changed.
var Extensions = []string{".wav", ".aif", ".aiff", ".aifc", ".mp3", ".ogg", ".oga", ".flac"}

// IsSoundFile reports whether name ends in one of [Extensions], in any
// case: whether it names a sound [Decode] reads, going by its name.
func IsSoundFile(name string) bool {
	return slices.Contains(Extensions, strings.ToLower(filepath.Ext(name)))
}

// Decode returns a source playing the sound r holds: WAV (RIFF, or RF64
// and BW64 past 4 GB), AIFF (or AIFF-C), MP3, Ogg Vorbis or FLAC, told
// apart by how it starts. The source plays at
// [SampleRate], resampling a sound recorded at another, and in stereo:
// a mono sound plays in both speakers, and a sound of more channels
// plays its first two.
//
// The source reads r as it plays, so r must stay open until it is done
// with.
func Decode(r io.ReadSeeker) (Seeker, error) {
	d, err := openDecoder(r)
	if err != nil {
		return nil, err
	}
	if d.rate() == SampleRate {
		return direct{d}, nil
	}
	return newResampler(d, SampleRate), nil
}

// DecodeNative returns a source playing the sound r holds at the rate
// it was recorded at, in stereo, and its format: for work that must keep
// the sound's own rate, as mastering does, where a sound resampled
// would come out changed. [Resample] brings such a source to the rate
// of the mixer playing it, where the two differ.
func DecodeNative(r io.ReadSeeker) (Seeker, Format, error) {
	d, err := openDecoder(r)
	if err != nil {
		return nil, Format{}, err
	}
	return direct{d}, d.info(), nil
}

// Resample returns src, a sound at rate from, played at rate to. Where
// the two are the same it returns src itself, every sample as it is.
func Resample(src Seeker, from, to int) Seeker {
	if from == to {
		return src
	}
	return newResampler(seekerDecoder{src, from}, to)
}

// seekerDecoder is a source at a rate of its own, as a decoder.
type seekerDecoder struct {
	s  Seeker
	hz int
}

func (d seekerDecoder) read(dst []float32) (int, error) { return d.s.Read(dst) }
func (d seekerDecoder) seek(f int64) error              { return d.s.SeekFrame(f) }
func (d seekerDecoder) len() int64                      { return d.s.Len() }
func (d seekerDecoder) rate() int                       { return d.hz }
func (d seekerDecoder) info() Format                    { return Format{SampleRate: d.hz} }

// openDecoder reads how r starts and returns the decoder of its format.
func openDecoder(r io.ReadSeeker) (decoder, error) {
	var head [12]byte
	n, err := io.ReadFull(r, head[:])
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	if _, err = r.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	var d decoder
	h := head[:n]
	switch {
	case len(h) >= 12 && string(h[8:12]) == "WAVE" &&
		(bytes.HasPrefix(h, []byte("RIFF")) || bytes.HasPrefix(h, []byte("RF64")) || bytes.HasPrefix(h, []byte("BW64"))):
		d, err = newWAV(r)
	case len(h) >= 12 && bytes.HasPrefix(h, []byte("FORM")) &&
		(string(h[8:12]) == "AIFF" || string(h[8:12]) == "AIFC"):
		d, err = newAIFF(r)
	case bytes.HasPrefix(h, []byte("OggS")):
		d, err = newVorbis(r)
	case bytes.HasPrefix(h, []byte("fLaC")):
		d, err = newFLAC(r)
	case bytes.HasPrefix(h, []byte("ID3")) || len(h) >= 2 && h[0] == 0xff && h[1]&0xe0 == 0xe0:
		d, err = newMP3(r)
	default:
		return nil, ErrFormat
	}
	if err != nil {
		return nil, err
	}
	if d.rate() <= 0 {
		return nil, fmt.Errorf("audio: sample rate %d", d.rate())
	}
	return d, nil
}

// Format is what a sound is stored as.
type Format struct {
	// Name names the format: "MP3", "FLAC", "Ogg Vorbis", "WAV" or
	// "AIFF".
	Name string
	// SampleRate is the rate it was recorded at, in hertz, before it
	// is resampled to play.
	SampleRate int
	// Channels and Bits are how many channels it has and how many bits
	// a sample, where the format says: zero where it does not, as MP3
	// and Ogg Vorbis keep no bit depth.
	Channels, Bits int
}

// FormatOf returns the format of a sound from [Decode], and false for
// another source.
func FormatOf(s Source) (Format, bool) {
	switch s := s.(type) {
	case direct:
		return s.d.info(), true
	case *resampler:
		return s.d.info(), true
	}
	return Format{}, false
}

// toStereo writes frames of ch channels from src, which holds whole
// frames, into dst as stereo, and returns how many frames it wrote.
func toStereo(dst, src []float32, ch int) int {
	n := min(len(dst)/2, len(src)/ch)
	for i := range n {
		if ch == 1 {
			dst[2*i], dst[2*i+1] = src[i], src[i]
		} else {
			dst[2*i], dst[2*i+1] = src[ch*i], src[ch*i+1]
		}
	}
	return n
}
