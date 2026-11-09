package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
)

// A WAV file is RIFF up to 4 GB. Past it, the sizes no longer fit in 32
// bits, and the file takes the RF64 form (BW64 is the same, by another
// name): RF64 in place of RIFF, and a ds64 chunk first, holding the
// file's sizes in 64 bits. A 32-bit size of 0xFFFFFFFF then means "see
// ds64".
//
// The headers gunim writes reserve the ds64 chunk's room as a JUNK
// chunk, which players skip. So a file turns RF64 by its header alone
// being rewritten, at the same length, and its samples never move.

// ErrWAVHeader is a WAV file whose header is cut short or makes no
// sense. It is an [ErrFormat] too.
var ErrWAVHeader = fmt.Errorf("%w: wav: the header is cut short or broken", ErrFormat)

// ErrSampleFormat is a WAV or AIFF file of samples in a format gunim
// does not read. Of WAV it reads integers of 8 to 32 bits and floats of
// 32; of AIFF integers of 1 to 32 bits and floats of 32 or 64, packed
// with no compression. Either in up to 64 channels. It is an
// [ErrFormat] too.
var ErrSampleFormat = fmt.Errorf("%w: the sample format is not supported", ErrFormat)

// wavMaxChannels is the most channels a WAV file gunim reads may have.
const wavMaxChannels = 64

// ds64Size is the body of a ds64 chunk with no table of other sizes,
// and so the body of the JUNK chunk that keeps its room.
const ds64Size = 28

// inDS64 is the 32-bit size an RF64 file gives where its ds64 chunk
// holds the size.
const inDS64 = math.MaxUint32

// wavLimit is the largest RIFF size a plain RIFF header tells; past it
// the header takes the RF64 form. A test makes it smaller.
var wavLimit int64 = inDS64 - 1

// A WAVSpec is how a WAV file holds its samples: at Rate, in Channels,
// each sample of Bits, a float where Float says and an integer
// otherwise.
type WAVSpec struct {
	Rate, Channels, Bits int
	Float                bool
}

// frameBytes is the size of a frame of s.
func (s WAVSpec) frameBytes() int64 { return int64(s.Channels * s.Bits / 8) }

// check reports whether gunim reads samples of s: [ErrSampleFormat] if
// not.
func (s WAVSpec) check() error {
	switch {
	case s.Float && s.Bits == 32:
	case !s.Float && s.Bits >= 8 && s.Bits <= 32 && s.Bits%8 == 0:
	default:
		kind := "integer"
		if s.Float {
			kind = "float"
		}
		return fmt.Errorf("%w: wav: %s samples of %d bits", ErrSampleFormat, kind, s.Bits)
	}
	if s.Channels > wavMaxChannels {
		return fmt.Errorf("%w: wav: %d channels, more than %d", ErrSampleFormat, s.Channels, wavMaxChannels)
	}
	if s.Channels < 1 || s.Rate < 1 {
		return fmt.Errorf("%w: %d channels at %d Hz", ErrWAVHeader, s.Channels, s.Rate)
	}
	return nil
}

// WAVHeaderSize is the size of the header [WAVHeader] writes for s, up
// to the first sample: the same for any length.
func WAVHeaderSize(s WAVSpec) int {
	// RIFF, WAVE, JUNK or ds64, fmt, data.
	n := 12 + 8 + ds64Size + 8 + 16 + 8
	if s.Float {
		// The fmt chunk's extra size, and the fact chunk, as formats
		// other than integer samples have.
		n += 2 + 12
	}
	return n
}

// WAVHeader is the header of a WAV file of frames of s: the bytes
// before its samples, which follow it interleaved, little-endian. Up to
// 4 GB it is RIFF, with a JUNK chunk keeping room for a ds64 chunk;
// past 4 GB it is RF64, with the ds64 chunk in the JUNK chunk's place.
// Both forms are [WAVHeaderSize] long, so a file written as it grows
// can have its header written over in place, as it passes 4 GB too.
//
// An odd number of bytes of samples is followed by a byte of padding,
// which the caller writes.
func WAVHeader(s WAVSpec, frames int64) []byte {
	return appendWAVHeader(nil, s, frames, 0)
}

// appendWAVHeader appends the header of a WAV file of frames of s,
// followed, after the samples and their padding, by after bytes of
// other chunks.
func appendWAVHeader(b []byte, s WAVSpec, frames, after int64) []byte {
	le := binary.LittleEndian
	head := int64(WAVHeaderSize(s))
	data := frames * s.frameBytes()
	riff := head - 8 + data + data&1 + after
	data32, frames32 := uint32(data), uint32(frames)
	if riff <= wavLimit {
		b = append(b, "RIFF"...)
		b = le.AppendUint32(b, uint32(riff))
		b = append(b, "WAVEJUNK"...)
		b = le.AppendUint32(b, ds64Size)
		b = append(b, make([]byte, ds64Size)...)
	} else {
		b = append(b, "RF64"...)
		b = le.AppendUint32(b, inDS64)
		b = append(b, "WAVEds64"...)
		b = le.AppendUint32(b, ds64Size)
		b = le.AppendUint64(b, uint64(riff))
		b = le.AppendUint64(b, uint64(data))
		b = le.AppendUint64(b, uint64(frames))
		// No table of other chunks' sizes.
		b = le.AppendUint32(b, 0)
		data32, frames32 = inDS64, inDS64
	}
	tag, fmtSize := uint16(wavPCM), uint32(16)
	if s.Float {
		tag, fmtSize = wavFloat, 18
	}
	b = append(b, "fmt "...)
	b = le.AppendUint32(b, fmtSize)
	b = le.AppendUint16(b, tag)
	b = le.AppendUint16(b, uint16(s.Channels))
	b = le.AppendUint32(b, uint32(s.Rate))
	b = le.AppendUint32(b, uint32(int64(s.Rate)*s.frameBytes()))
	b = le.AppendUint16(b, uint16(s.frameBytes()))
	b = le.AppendUint16(b, uint16(s.Bits))
	if s.Float {
		b = le.AppendUint16(b, 0)
		b = append(b, "fact"...)
		b = le.AppendUint32(b, 4)
		b = le.AppendUint32(b, frames32)
	}
	b = append(b, "data"...)
	return le.AppendUint32(b, data32)
}

// WAVLayout is how a WAV file holds its samples.
type WAVLayout struct {
	WAVSpec
	// Wide says the file is RF64 or BW64, with 64-bit sizes.
	Wide bool
	// Data is where the samples start in the file, and Frames how many
	// the header tells of.
	Data, Frames int64
}

// ReadWAVLayout reads the header of the WAV file r holds, RIFF, RF64 or
// BW64. A file of no WAV is [ErrFormat], a header cut short or broken
// is [ErrWAVHeader], and samples gunim does not read are
// [ErrSampleFormat]; an error reading r is returned as it is.
//
// A RIFF file whose data chunk's size is 0xFFFFFFFF, as a program
// writing to a stream leaves it, not knowing the length, runs to the
// end of the file, where r tells its size: as a [bytes.Reader],
// [io.SectionReader] or [os.File] does.
func ReadWAVLayout(r io.ReaderAt) (WAVLayout, error) {
	return layoutOfWAV(r, false, 0)
}

// RecoverWAVLayout reads the header of a WAV file that was written as
// it grew and left unfinished, as when a program stops mid-recording:
// one whose header may tell a length other than the file's, or that tore
// as it was written over, half RIFF and half RF64. It trusts where the
// header puts the samples and what it says they are, and takes the
// length from size, the file's size, as the whole frames it holds.
func RecoverWAVLayout(r io.ReaderAt, size int64) (WAVLayout, error) {
	return layoutOfWAV(r, true, size)
}

// layoutOfWAV reads the header of the WAV file r holds; where torn
// says the header may be, it takes the file's length from size.
func layoutOfWAV(r io.ReaderAt, torn bool, size int64) (WAVLayout, error) {
	var l WAVLayout
	haveFmt, haveData := false, false
	var bad error
	wide, err := chunksOfWAV(r, torn, func(c WAVChunk) bool {
		switch c.ID {
		case "fmt ":
			l.WAVSpec, bad = readWAVFmt(r, c)
			haveFmt = bad == nil
			return haveFmt
		case "data":
			if !haveFmt {
				bad = fmt.Errorf("%w: data before fmt", ErrWAVHeader)
				return false
			}
			l.Data, haveData = c.At, true
			n := c.Size
			if torn {
				n = size - c.At
			}
			l.Frames = max(0, n) / l.frameBytes()
			return false
		}
		return true
	})
	switch {
	case err != nil:
		return WAVLayout{}, err
	case bad != nil:
		return WAVLayout{}, bad
	case !haveData:
		return WAVLayout{}, fmt.Errorf("%w: no data chunk", ErrWAVHeader)
	}
	l.Wide = wide
	return l, nil
}

// readWAVFmt reads the fmt chunk c of r.
func readWAVFmt(r io.ReaderAt, c WAVChunk) (WAVSpec, error) {
	if c.Size < 16 {
		return WAVSpec{}, fmt.Errorf("%w: a fmt chunk of %d bytes", ErrWAVHeader, c.Size)
	}
	var f [26]byte
	if err := readWAVAt(r, f[:min(c.Size, 26)], c.At); err != nil {
		return WAVSpec{}, err
	}
	le := binary.LittleEndian
	tag := le.Uint16(f[0:])
	s := WAVSpec{
		Channels: int(le.Uint16(f[2:])),
		Rate:     int(le.Uint32(f[4:])),
		Bits:     int(le.Uint16(f[14:])),
	}
	if tag == wavExtensible && c.Size >= 26 {
		tag = le.Uint16(f[24:])
	}
	switch tag {
	case wavPCM:
	case wavFloat:
		s.Float = true
	default:
		return WAVSpec{}, fmt.Errorf("%w: wav: format %#x of %d bits", ErrSampleFormat, tag, s.Bits)
	}
	return s, s.check()
}

// A WAVChunk is a chunk of a WAV file: its four-letter ID, where its
// body starts in the file, and its body's size. For the data chunk of
// an RF64 or BW64 file, Size is the 64-bit size from the ds64 chunk.
type WAVChunk struct {
	ID       string
	At, Size int64
}

// WalkWAV calls fn with each chunk of the WAV file r holds, RIFF, RF64
// or BW64, in order, until fn returns false or the file ends. It reads
// the chunks' headers, and fn reads what it wants of their bodies from
// r. A file of no WAV is [ErrFormat] and an RF64 file with no sizes
// where it should have them [ErrWAVHeader]; an error reading r, but its
// end, is returned as it is.
//
// The data chunk of a RIFF file that has 0xFFFFFFFF as its size, as one
// written to a stream does, runs to the end of the file, where r tells
// its size, as for [ReadWAVLayout].
func WalkWAV(r io.ReaderAt, fn func(WAVChunk) bool) error {
	_, err := chunksOfWAV(r, false, fn)
	return err
}

// chunksOfWAV is WalkWAV, also reporting whether the file is wide, RF64 or
// BW64. Where torn says the header may have torn as it changed form,
// it takes RIFF, RF64 and BW64 alike, and a ds64 chunk's sizes or none.
func chunksOfWAV(r io.ReaderAt, torn bool, fn func(WAVChunk) bool) (wide bool, err error) {
	var riff [12]byte
	if err := readWAVAt(r, riff[:], 0); err != nil {
		if errors.Is(err, ErrWAVHeader) {
			return false, ErrFormat
		}
		return false, err
	}
	magic := string(riff[:4])
	wide = magic == "RF64" || magic == "BW64"
	if !wide && magic != "RIFF" || string(riff[8:]) != "WAVE" {
		return false, ErrFormat
	}
	le := binary.LittleEndian
	// dataSize is the data chunk's size from the ds64 chunk, -1 for none.
	dataSize := int64(-1)
	for at := int64(12); ; {
		var h [8]byte
		if err := readWAVAt(r, h[:], at); err != nil {
			if errors.Is(err, ErrWAVHeader) {
				// The file ends.
				return wide, nil
			}
			return wide, err
		}
		c := WAVChunk{ID: string(h[:4]), At: at + 8, Size: int64(le.Uint32(h[4:]))}
		switch {
		case c.ID == "ds64" && torn:
		case c.ID == "ds64":
			if !wide || at != 12 || c.Size < ds64Size {
				return wide, fmt.Errorf("%w: a ds64 chunk out of place", ErrWAVHeader)
			}
			var d [16]byte
			if err := readWAVAt(r, d[:], c.At); err != nil {
				return wide, err
			}
			dataSize = int64(le.Uint64(d[8:]))
			if dataSize < 0 {
				return wide, fmt.Errorf("%w: a data size past 2^63", ErrWAVHeader)
			}
		case c.ID == "data" && c.Size == inDS64 && wide && !torn:
			if dataSize < 0 {
				return wide, fmt.Errorf("%w: the data's size is in a ds64 chunk it lacks", ErrWAVHeader)
			}
			c.Size = dataSize
		case c.ID == "data" && c.Size == inDS64:
			// Written to a stream: the data runs to the end of the file.
			if n, ok := readerSize(r); ok {
				c.Size = max(0, n-c.At)
			}
		}
		if !fn(c) {
			return wide, nil
		}
		at = c.At + c.Size + c.Size&1
	}
}

// readWAVAt reads b from r at off; a file ending first is
// [ErrWAVHeader], and another error is returned as it is.
func readWAVAt(r io.ReaderAt, b []byte, off int64) error {
	n, err := r.ReadAt(b, off)
	if n == len(b) {
		return nil
	}
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return ErrWAVHeader
	}
	return err
}

// readerSize is the size of what r reads, where r tells it.
func readerSize(r io.ReaderAt) (int64, bool) {
	switch r := r.(type) {
	case interface{ Size() int64 }:
		n := r.Size()
		return n, n >= 0
	case interface{ Stat() (fs.FileInfo, error) }:
		if st, err := r.Stat(); err == nil {
			return st.Size(), true
		}
	}
	return 0, false
}

// seekReaderAt reads an io.ReadSeeker as an io.ReaderAt, moving where
// it stands.
type seekReaderAt struct{ r io.ReadSeeker }

// ReadAt reads b from s at off.
func (s seekReaderAt) ReadAt(b []byte, off int64) (int, error) {
	if _, err := s.r.Seek(off, io.SeekStart); err != nil {
		return 0, err
	}
	n, err := io.ReadFull(s.r, b)
	if errors.Is(err, io.ErrUnexpectedEOF) {
		err = io.EOF
	}
	return n, err
}

// Size is the size of what s reads, -1 where it seeks to no end.
func (s seekReaderAt) Size() int64 {
	n, err := s.r.Seek(0, io.SeekEnd)
	if err != nil {
		return -1
	}
	return n
}
