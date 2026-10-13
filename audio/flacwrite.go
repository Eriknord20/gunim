package audio

import (
	"errors"
	"io"
	"math/rand/v2"

	"github.com/mewkiz/flac"
	"github.com/mewkiz/flac/frame"
	"github.com/mewkiz/flac/meta"
)

// flacBlock is how many frames each block of a FLAC file holds; the
// last block holds what is left.
const flacBlock = 4096

// A FLACWriter writes stereo sound to a FLAC file of 16 or 24 bit
// samples, compressed without loss: it rounds and dithers the sound as
// a [WAVWriter] of the same bits does, so the two files hold the same
// samples.
type FLACWriter struct {
	w           io.WriteSeeker
	rate        int
	bits        int
	dither      bool
	enc         *flac.Encoder
	left, right []int32
	rng         *rand.Rand
	// Clipped counts the samples written past full scale, which were
	// held to it.
	Clipped int64
	// Tags are written at the start of the file, as a Vorbis comment
	// players read, such as TITLE, ARTIST, ALBUM, TRACKNUMBER, DATE and
	// GENRE. The file starts with the first Write, or with Close, so
	// set them before. Empty values are left out.
	Tags []FLACTag
}

// A FLACTag is a tag of a FLAC file: its name and its value.
type FLACTag struct {
	Name, Value string
}

// NewFLACWriter starts a FLAC file of sound at rate, in samples of
// bits: 16 or 24. Close finishes it.
func NewFLACWriter(w io.WriteSeeker, rate, bits int, dither bool) (*FLACWriter, error) {
	if bits != 16 && bits != 24 {
		return nil, errors.New("audio: flac: 16 or 24 bits")
	}
	return &FLACWriter{w: w, rate: rate, bits: bits, dither: dither,
		left: make([]int32, 0, flacBlock), right: make([]int32, 0, flacBlock),
		rng: rand.New(rand.NewPCG(0x6775, 0x6e69))}, nil
}

// writeSeeker hands the encoder the file's Write and Seek alone, so
// the encoder's Close leaves the file open.
type writeSeeker struct{ io.WriteSeeker }

// start writes the file's header and tags, once.
func (w *FLACWriter) start() error {
	if w.enc != nil {
		return nil
	}
	info := &meta.StreamInfo{
		BlockSizeMin:  flacBlock,
		BlockSizeMax:  flacBlock,
		SampleRate:    uint32(w.rate),
		NChannels:     2,
		BitsPerSample: uint8(w.bits),
	}
	var blocks []*meta.Block
	comment := &meta.VorbisComment{Vendor: "gunim"}
	// size is the comment's length in bytes, as the block's header
	// holds it.
	size := 4 + len(comment.Vendor) + 4
	for _, t := range w.Tags {
		if t.Name != "" && t.Value != "" {
			comment.Tags = append(comment.Tags, [2]string{t.Name, t.Value})
			size += 4 + len(t.Name) + 1 + len(t.Value)
		}
	}
	if len(comment.Tags) > 0 {
		blocks = append(blocks, &meta.Block{
			Header: meta.Header{Type: meta.TypeVorbisComment, Length: int64(size)},
			Body:   comment,
		})
	}
	enc, err := flac.NewEncoder(writeSeeker{w.w}, info, blocks...)
	if err != nil {
		return err
	}
	enc.EnablePredictionAnalysis(true)
	w.enc = enc
	return nil
}

// Write writes frames, interleaved stereo, at the file's rate.
func (w *FLACWriter) Write(frames []float32) error {
	if err := w.start(); err != nil {
		return err
	}
	n := len(frames) / 2 * 2
	scale := float64(int64(1) << (w.bits - 1))
	for i := 0; i < n; i += 2 {
		l, lc := quantize(frames[i], scale, w.dither, w.rng)
		r, rc := quantize(frames[i+1], scale, w.dither, w.rng)
		if lc {
			w.Clipped++
		}
		if rc {
			w.Clipped++
		}
		w.left = append(w.left, l)
		w.right = append(w.right, r)
		if len(w.left) == flacBlock {
			if err := w.block(); err != nil {
				return err
			}
		}
	}
	return nil
}

// block writes the frames held as one block of the file, its left and
// right stored as their middle and their difference, which compress
// better for most sound.
func (w *FLACWriter) block() error {
	if len(w.left) == 0 {
		return nil
	}
	n := len(w.left)
	// Each subframe starts as verbatim, the encoder's cue to pick its
	// best prediction.
	verbatim := frame.SubHeader{Pred: frame.PredVerbatim}
	f := &frame.Frame{
		Header: frame.Header{
			HasFixedBlockSize: true,
			BlockSize:         uint16(n),
			SampleRate:        uint32(w.rate),
			Channels:          frame.ChannelsMidSide,
			BitsPerSample:     uint8(w.bits),
		},
		Subframes: []*frame.Subframe{
			{SubHeader: verbatim, Samples: w.left, NSamples: n},
			{SubHeader: verbatim, Samples: w.right, NSamples: n},
		},
	}
	if err := w.enc.WriteFrame(f); err != nil {
		return err
	}
	w.left, w.right = w.left[:0], w.right[:0]
	return nil
}

// Close writes the last block, and the file's length and checksum into
// its header. It leaves the file open.
func (w *FLACWriter) Close() error {
	if err := w.start(); err != nil {
		return err
	}
	if err := w.block(); err != nil {
		return err
	}
	end, err := w.w.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if err := w.enc.Close(); err != nil {
		return err
	}
	_, err = w.w.Seek(end, io.SeekStart)
	return err
}
