package audio

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
)

// flacFewest is the shortest block a FLAC file's header may name.
const flacFewest = 16

// flacSizesEnd is where the block sizes in a FLAC file's header end: the
// signature, the block's header, and two sizes of two bytes each.
const flacSizesEnd = 12

// healFLACSizes raises the block sizes in b, a FLAC file's first bytes, to
// flacFewest where they are lower, and reports whether it changed them.
//
// The FLAC encoder names the shortest block it wrote as the file's minimum,
// the last among them, though FLAC leaves the last out of it. So a file whose
// last block was under 16 frames, about 1 in 270, has a header that readers
// refuse. The writer no longer makes such a file, and those made before still
// open with this.
func healFLACSizes(b []byte) bool {
	if len(b) < flacSizesEnd || string(b[:4]) != "fLaC" {
		return false
	}
	changed := false
	for _, at := range []int{8, 10} {
		if binary.BigEndian.Uint16(b[at:]) < flacFewest {
			binary.BigEndian.PutUint16(b[at:], flacFewest)
			changed = true
		}
	}
	return changed
}

// HealFLAC returns a reader of the FLAC stream r whose header readers accept,
// for a stream whose encoder named a last block under 16 frames as its minimum
// block size. What reading the start of r fails with, the reader returns.
func HealFLAC(r io.Reader) io.Reader {
	head := make([]byte, flacSizesEnd)
	n, err := io.ReadFull(r, head)
	head = head[:n]
	healFLACSizes(head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return io.MultiReader(bytes.NewReader(head), errorReader{err})
	}
	return io.MultiReader(bytes.NewReader(head), r)
}

// errorReader fails with its error.
type errorReader struct{ err error }

func (e errorReader) Read([]byte) (int, error) { return 0, e.err }

// healedFLAC reads a FLAC file with its first bytes put right: see
// [healFLACSizes].
type healedFLAC struct {
	io.ReadSeeker
	head [flacSizesEnd]byte
	pos  int64
}

// healFLACSeeker returns r or, where its header needs it, r with the header
// healed. It leaves r where it found it, which should be its start.
func healFLACSeeker(r io.ReadSeeker) (io.ReadSeeker, error) {
	start, err := r.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, err
	}
	h := &healedFLAC{ReadSeeker: r, pos: start}
	_, err = io.ReadFull(r, h.head[:])
	if _, serr := r.Seek(start, io.SeekStart); serr != nil {
		return nil, serr
	}
	if err != nil || start != 0 || !healFLACSizes(h.head[:]) {
		return r, nil
	}
	return h, nil
}

func (h *healedFLAC) Read(p []byte) (int, error) {
	n, err := h.ReadSeeker.Read(p)
	if h.pos < flacSizesEnd {
		copy(p[:n], h.head[h.pos:])
	}
	h.pos += int64(n)
	return n, err
}

func (h *healedFLAC) Seek(off int64, whence int) (int64, error) {
	pos, err := h.ReadSeeker.Seek(off, whence)
	if err == nil {
		h.pos = pos
	}
	return pos, err
}

// Close closes the file beneath, where it can be closed.
func (h *healedFLAC) Close() error {
	if c, ok := h.ReadSeeker.(io.Closer); ok {
		return c.Close()
	}
	return nil
}
