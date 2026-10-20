package audio

import (
	"errors"
	"fmt"
	"io"
	"math/rand/v2"

	"github.com/mewkiz/flac"
	"github.com/mewkiz/flac/frame"
	"github.com/mewkiz/flac/meta"
)

// flacBlock is how many frames each block of a FLAC file holds; the
// last block holds what is left.
const flacBlock = 4096

// flacMaxFrames is one more than the most frames a FLAC header can
// count: it holds the count in 36 bits.
const flacMaxFrames = int64(1) << 36

// A FLACWriter writes sound to a FLAC file of 16 or 24 bit samples,
// compressed without loss: it rounds and dithers the sound as a
// [WAVWriter] of the same bits does, so the two files hold the same
// samples. [NewFLACWriter] writes stereo to a file it can seek in, and
// [NewFLACStream] writes 1 to 8 channels to any writer.
type FLACWriter struct {
	// enc writes to w, wrapped so the encoder can neither close it nor
	// seek in it unless seek is set.
	w        io.Writer
	seek     io.WriteSeeker
	rate     int
	bits     int
	channels int
	// frames is how many frames the header says, set up front, or -1
	// when Close writes it.
	frames  int64
	written int64
	dither  bool
	enc     *flac.Encoder
	// held holds each channel's samples until a block is full.
	held [][]int32
	rng  *rand.Rand
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

// NewFLACWriter starts a stereo FLAC file of sound at rate, in samples
// of bits: 16 or 24. Close finishes it, and seeks back to write the
// file's length and the checksum of its samples into its header.
func NewFLACWriter(w io.WriteSeeker, rate, bits int, dither bool) (*FLACWriter, error) {
	f, err := startFLAC(writeSeeker{w}, rate, bits, 2, -1, dither)
	if err != nil {
		return nil, err
	}
	f.seek = w
	return f, nil
}

// NewFLACStream starts a FLAC stream of sound at rate, in samples of
// bits: 16 or 24, with 1 to 8 channels. It never seeks, so w can be a
// pipe or a network connection. The header holds frames, the count of
// frames to come, and an empty checksum, which FLAC allows. Close fails
// if a different count was written, and leaves w open.
func NewFLACStream(w io.Writer, rate, bits, channels int, frames int64, dither bool) (*FLACWriter, error) {
	if channels < 1 || channels > 8 {
		return nil, errors.New("audio: flac: 1 to 8 channels")
	}
	if frames < 0 || frames >= flacMaxFrames {
		return nil, fmt.Errorf("audio: flac: %d frames", frames)
	}
	return startFLAC(writer{w}, rate, bits, channels, frames, dither)
}

// startFLAC makes a writer of the parts both kinds share.
func startFLAC(w io.Writer, rate, bits, channels int, frames int64, dither bool) (*FLACWriter, error) {
	if bits != 16 && bits != 24 {
		return nil, errors.New("audio: flac: 16 or 24 bits")
	}
	held := make([][]int32, channels)
	for c := range held {
		held[c] = make([]int32, 0, flacBlock)
	}
	return &FLACWriter{w: w, rate: rate, bits: bits, channels: channels,
		frames: frames, dither: dither, held: held,
		rng: rand.New(rand.NewPCG(0x6775, 0x6e69))}, nil
}

// writeSeeker hands the encoder the file's Write and Seek alone, so
// the encoder's Close leaves the file open.
type writeSeeker struct{ io.WriteSeeker }

// writer hands the encoder a writer's Write alone, so the encoder's
// Close neither seeks in it nor closes it.
type writer struct{ io.Writer }

// start writes the file's header and tags, once.
func (w *FLACWriter) start() error {
	if w.enc != nil {
		return nil
	}
	info := &meta.StreamInfo{
		BlockSizeMin:  flacBlock,
		BlockSizeMax:  flacBlock,
		SampleRate:    uint32(w.rate),
		NChannels:     uint8(w.channels),
		BitsPerSample: uint8(w.bits),
	}
	if w.frames >= 0 {
		info.NSamples = uint64(w.frames)
		// A stream shorter than a block is one block, of its length,
		// which may be no shorter than 16 frames.
		if w.frames < flacBlock {
			n := uint16(max(w.frames, 16))
			info.BlockSizeMin, info.BlockSizeMax = n, n
		}
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
	enc, err := flac.NewEncoder(w.w, info, blocks...)
	if err != nil {
		return err
	}
	enc.EnablePredictionAnalysis(true)
	w.enc = enc
	return nil
}

// Write writes frames, interleaved, at the file's rate. Frames past
// the count a stream was started with are an error.
func (w *FLACWriter) Write(frames []float32) error {
	if err := w.start(); err != nil {
		return err
	}
	ch := w.channels
	n := len(frames) / ch * ch
	if err := w.room(n / ch); err != nil {
		return err
	}
	scale := float64(int64(1) << (w.bits - 1))
	for i := 0; i < n; i += ch {
		for c := range ch {
			v, clipped := quantize(frames[i+c], scale, w.dither, w.rng)
			if clipped {
				w.Clipped++
			}
			w.held[c] = append(w.held[c], v)
		}
		if err := w.hold(); err != nil {
			return err
		}
	}
	return nil
}

// WriteInts writes frames, interleaved, as whole samples of the file's
// bits, as they are: from -2^(bits-1) to 2^(bits-1)-1. A sample past
// those is an error, as are frames past the count a stream was started
// with.
func (w *FLACWriter) WriteInts(frames []int32) error {
	if err := w.start(); err != nil {
		return err
	}
	ch := w.channels
	n := len(frames) / ch * ch
	hi := int32(1)<<(w.bits-1) - 1
	for _, v := range frames[:n] {
		if v > hi || v < -hi-1 {
			return fmt.Errorf("audio: flac: sample %d is past %d bits", v, w.bits)
		}
	}
	if err := w.room(n / ch); err != nil {
		return err
	}
	for i := 0; i < n; i += ch {
		for c := range ch {
			w.held[c] = append(w.held[c], frames[i+c])
		}
		if err := w.hold(); err != nil {
			return err
		}
	}
	return nil
}

// room fails if n more frames would pass the count a stream was
// started with, and counts them otherwise.
func (w *FLACWriter) room(n int) error {
	if w.frames >= 0 && w.written+int64(n) > w.frames {
		return fmt.Errorf("audio: flac: %d frames past the %d the stream was started with", w.written+int64(n)-w.frames, w.frames)
	}
	w.written += int64(n)
	return nil
}

// hold writes a block once a block's frames are held.
func (w *FLACWriter) hold() error {
	if len(w.held[0]) == flacBlock {
		return w.block()
	}
	return nil
}

// block writes the frames held as one block of the file. Stereo is
// stored as its middle and its difference, which compress better for
// most sound; other channels are stored each on its own.
func (w *FLACWriter) block() error {
	n := len(w.held[0])
	if n == 0 {
		return nil
	}
	// Mono to eight channels are the first eight of FLAC's channel
	// layouts, in order.
	layout := frame.Channels(w.channels - 1)
	if w.channels == 2 {
		layout = frame.ChannelsMidSide
	}
	f := &frame.Frame{
		Header: frame.Header{
			HasFixedBlockSize: true,
			BlockSize:         uint16(n),
			SampleRate:        uint32(w.rate),
			Channels:          layout,
			BitsPerSample:     uint8(w.bits),
		},
	}
	for c := range w.held {
		// Each subframe starts as verbatim, the encoder's cue to pick
		// its best prediction.
		f.Subframes = append(f.Subframes, &frame.Subframe{
			SubHeader: frame.SubHeader{Pred: frame.PredVerbatim},
			Samples:   w.held[c], NSamples: n,
		})
	}
	if err := w.enc.WriteFrame(f); err != nil {
		return err
	}
	for c := range w.held {
		w.held[c] = w.held[c][:0]
	}
	return nil
}

// Close writes the last block. A file from [NewFLACWriter] then gets
// its length and checksum written into its header. Close leaves the
// file or stream open.
func (w *FLACWriter) Close() error {
	if err := w.start(); err != nil {
		return err
	}
	if err := w.block(); err != nil {
		return err
	}
	if w.seek == nil {
		if err := w.enc.Close(); err != nil {
			return err
		}
		if w.written != w.frames {
			return fmt.Errorf("audio: flac: %d frames written to a stream started with %d", w.written, w.frames)
		}
		return nil
	}
	end, err := w.seek.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if err := w.enc.Close(); err != nil {
		return err
	}
	_, err = w.seek.Seek(end, io.SeekStart)
	return err
}

// FLACInts is the sound of a FLAC stream as whole samples.
type FLACInts struct {
	Rate, Bits, Channels int
	// Samples are the stream's frames interleaved, each sample a whole
	// number of Bits.
	Samples []int32
}

// DecodeFLACInts reads a whole FLAC stream from r, which need not
// seek, and returns its samples as they are stored. It fails past
// maxFrames, so a stream can make it hold no more than the caller allows,
// and when the stream holds other than the frames its header counts.
// It may read past the stream's end in r.
func DecodeFLACInts(r io.Reader, maxFrames int64) (FLACInts, error) {
	s, err := flac.New(r)
	if err != nil {
		return FLACInts{}, err
	}
	info := s.Info
	out := FLACInts{Rate: int(info.SampleRate), Bits: int(info.BitsPerSample), Channels: int(info.NChannels)}
	want := int64(info.NSamples)
	if want > maxFrames {
		return FLACInts{}, fmt.Errorf("audio: flac: %d frames, past %d", want, maxFrames)
	}
	// The count can only be trusted as far as the stream bears it out,
	// so hold no more than a few blocks before it does.
	out.Samples = make([]int32, 0, min(want, 16*flacBlock)*int64(out.Channels))
	var n int64
	for {
		f, err := s.ParseNext()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return FLACInts{}, err
		}
		if len(f.Subframes) != out.Channels || int(f.BitsPerSample) != out.Bits {
			return FLACInts{}, errors.New("audio: flac: a block differs from the header")
		}
		k := f.Subframes[0].NSamples
		if n += int64(k); n > maxFrames {
			return FLACInts{}, fmt.Errorf("audio: flac: past %d frames", maxFrames)
		}
		for i := range k {
			for _, sub := range f.Subframes {
				out.Samples = append(out.Samples, sub.Samples[i])
			}
		}
	}
	if want != 0 && n != want {
		return FLACInts{}, fmt.Errorf("audio: flac: %d frames, the header says %d", n, want)
	}
	return out, nil
}
