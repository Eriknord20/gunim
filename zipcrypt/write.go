package zipcrypt

import (
	"archive/zip"
	"compress/flate"
	"crypto/aes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"hash"
	"io"
	"math"
	"slices"
	"strings"
	"unicode/utf8"
)

// Writer adds entries to a zip.Writer, protected with a password by
// WinZip's AES with a 256-bit key, compressed with Deflate. It writes
// AE-2, which stores no CRC: a CRC in the clear would tell what a small
// file holds, as a PIN, to anyone who tried every one.
//
// An entry ends at the next Create, or at Close, which the Writer's
// entries need in place of the zip.Writer's own.
type Writer struct {
	zw       *zip.Writer
	password string
	open     *entryWriter
}

// NewWriter returns a Writer that adds entries to zw protected with
// password. With an empty password, it protects nothing, and its
// entries go in as zw.CreateHeader puts them.
func NewWriter(zw *zip.Writer, password string) *Writer {
	return &Writer{zw: zw, password: password}
}

// Create adds an entry to the zip as zip.Writer.CreateHeader does, and
// returns the writer of what it holds, which is protected. A folder,
// whose name ends in a slash, holds nothing, and goes in as it is.
func (w *Writer) Create(fh *zip.FileHeader) (io.Writer, error) {
	if err := w.end(); err != nil {
		return nil, err
	}
	if w.password == "" || strings.HasSuffix(fh.Name, "/") {
		return w.zw.CreateHeader(fh)
	}
	protect(fh)
	raw, err := w.zw.CreateRaw(fh)
	if err != nil {
		return nil, err
	}
	stored := &countWriter{w: raw}
	w.open = &entryWriter{fh: fh, stored: stored, aes: &aesWriter{out: stored, password: w.password}}
	return w.open, nil
}

// Close ends the last entry, and closes the zip.Writer.
func (w *Writer) Close() error {
	return errors.Join(w.end(), w.zw.Close())
}

// end ends the entry being written, if there is one.
func (w *Writer) end() error {
	e := w.open
	if e == nil {
		return nil
	}
	w.open = nil
	return e.end()
}

// protect sets fh up for an entry WinZip's AES protects, which
// zip.Writer.CreateRaw writes as fh says, where CreateHeader would set
// it up itself.
func protect(fh *zip.FileHeader) {
	// As CreateHeader says a name is in UTF-8, and when it was changed.
	if !fh.NonUTF8 && (!isASCII(fh.Name) || !isASCII(fh.Comment)) && utf8.ValidString(fh.Name) && utf8.ValidString(fh.Comment) {
		fh.Flags |= flagUTF8
	}
	if !fh.Modified.IsZero() {
		t := fh.Modified
		// The MS-DOS time every reader knows, which CreateRaw writes as
		// it is, where CreateHeader works it out.
		fh.ModifiedDate = uint16(max(t.Year()-1980, 0)<<9 | int(t.Month())<<5 | t.Day()) //nolint:staticcheck // written by CreateRaw
		fh.ModifiedTime = uint16(t.Hour()<<11 | t.Minute()<<5 | t.Second()/2)            //nolint:staticcheck // written by CreateRaw
		var stamp [9]byte
		binary.LittleEndian.PutUint16(stamp[0:], extTimeID)
		binary.LittleEndian.PutUint16(stamp[2:], 5)
		stamp[4] = 1
		binary.LittleEndian.PutUint32(stamp[5:], uint32(t.Unix()))
		fh.Extra = append(fh.Extra, stamp[:]...)
	}
	fh.Method = methodAES
	fh.Flags |= flagEncrypted | flagDescriptor
	// AES needs a reader of version 5.1, as WinZip's specification says.
	fh.CreatorVersion = fh.CreatorVersion&0xff00 | aesVersion
	fh.ReaderVersion = aesVersion
	fh.CRC32, fh.CompressedSize64, fh.UncompressedSize64 = 0, 0, 0
	var extra [4 + aesExtraLen]byte
	binary.LittleEndian.PutUint16(extra[0:], aesExtraID)
	binary.LittleEndian.PutUint16(extra[2:], aesExtraLen)
	binary.LittleEndian.PutUint16(extra[4:], ae2)
	copy(extra[6:], "AE")
	extra[8] = aes256
	binary.LittleEndian.PutUint16(extra[9:], zip.Deflate)
	fh.Extra = append(fh.Extra, extra[:]...)
}

// The flag that says an entry's name is in UTF-8, the ID of the extra
// field of the time it was changed, and the version of the format AES
// needs.
const (
	flagUTF8   = 0x800
	extTimeID  = 0x5455
	aesVersion = 51
)

func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// entryWriter takes what an entry holds, and at its end tells its header
// how big it was, which zip.Writer then writes after it and in the
// central directory.
type entryWriter struct {
	fh     *zip.FileHeader
	stored *countWriter
	aes    *aesWriter
	size   uint64
}

func (e *entryWriter) Write(p []byte) (int, error) {
	n, err := e.aes.Write(p)
	e.size += uint64(n)
	return n, err
}

func (e *entryWriter) end() error {
	if err := e.aes.Close(); err != nil {
		return err
	}
	fh := e.fh
	fh.CompressedSize64, fh.UncompressedSize64 = e.stored.n, e.size
	// The 32-bit sizes are written too, as zip.Writer sets them for its
	// own entries.
	if fh.CompressedSize64 > math.MaxUint32 || fh.UncompressedSize64 > math.MaxUint32 {
		fh.CompressedSize, fh.UncompressedSize = math.MaxUint32, math.MaxUint32 //nolint:staticcheck // written by zip.Writer
	} else {
		fh.CompressedSize, fh.UncompressedSize = uint32(fh.CompressedSize64), uint32(fh.UncompressedSize64) //nolint:staticcheck // written by zip.Writer
	}
	return nil
}

// countWriter counts what it writes to w.
type countWriter struct {
	w io.Writer
	n uint64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += uint64(n)
	return n, err
}

// aesWriter compresses what is written to it, and encrypts and signs it
// into out, after the salt and the password check, which it writes with
// the first write, or the close.
type aesWriter struct {
	out      io.Writer
	password string
	flate    *flate.Writer
	ctr      *winZipCTR
	mac      hash.Hash
	started  bool
	closed   bool
	err      error
}

// start writes the salt and the password check.
func (w *aesWriter) start() error {
	if w.started || w.err != nil {
		return w.err
	}
	w.started = true
	spec := aesSpec{strength: aes256}
	salt := make([]byte, spec.saltLen())
	if _, err := rand.Read(salt); err != nil {
		w.err = err
		return err
	}
	enc, macKey, check, err := aesKeys(w.password, salt, spec.keyLen())
	if err != nil {
		w.err = err
		return err
	}
	block, err := aes.NewCipher(enc)
	if err != nil {
		w.err = err
		return err
	}
	w.ctr, w.mac = newWinZipCTR(block), hmac.New(sha1.New, macKey)
	if _, err := w.out.Write(slices.Concat(salt, check)); err != nil {
		w.err = err
		return err
	}
	w.flate, w.err = flate.NewWriter(sealer{w}, flate.DefaultCompression)
	return w.err
}

func (w *aesWriter) Write(p []byte) (int, error) {
	if err := w.start(); err != nil {
		return 0, err
	}
	return w.flate.Write(p)
}

// Close ends the compressed stream and writes the signature after it.
func (w *aesWriter) Close() error {
	if err := w.start(); err != nil {
		return err
	}
	if w.closed {
		return errors.New("zipcrypt: closed twice")
	}
	w.closed = true
	if err := w.flate.Close(); err != nil {
		return err
	}
	_, err := w.out.Write(w.mac.Sum(nil)[:aesMACLen])
	return err
}

// sealer encrypts and signs what the compressor writes, into the
// writer's out.
type sealer struct{ w *aesWriter }

func (s sealer) Write(p []byte) (int, error) {
	buf := make([]byte, len(p))
	copy(buf, p)
	s.w.ctr.xor(buf)
	s.w.mac.Write(buf)
	return s.w.out.Write(buf)
}
