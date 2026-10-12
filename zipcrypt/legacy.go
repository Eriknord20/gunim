package zipcrypt

import (
	"archive/zip"
	"compress/flate"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
)

// legacyHeader is how many bytes of random data and check come before
// what an entry the old protection protects holds.
const legacyHeader = 12

// legacyKeys are the three keys of PKWARE's old protection, which every
// byte decrypted moves on.
type legacyKeys [3]uint32

func newLegacyKeys(password string) *legacyKeys {
	k := &legacyKeys{0x12345678, 0x23456789, 0x34567890}
	for i := range len(password) {
		k.update(password[i])
	}
	return k
}

func (k *legacyKeys) update(b byte) {
	k[0] = crc32.IEEETable[byte(k[0])^b] ^ k[0]>>8
	k[1] = (k[1]+k[0]&0xff)*134775813 + 1
	k[2] = crc32.IEEETable[byte(k[2])^byte(k[1]>>24)] ^ k[2]>>8
}

// stream is the byte the keys hide the next one behind.
func (k *legacyKeys) stream() byte {
	t := k[2] | 2
	return byte((t * (t ^ 1)) >> 8)
}

func (k *legacyKeys) decrypt(p []byte) {
	for i, c := range p {
		p[i] = c ^ k.stream()
		k.update(p[i])
	}
}

// openLegacy opens f, which the old protection protects, its bytes as
// they are stored coming from raw.
func openLegacy(f *zip.File, raw io.Reader, password string) (io.ReadCloser, error) {
	if f.CompressedSize64 < legacyHeader {
		return nil, ErrDamaged
	}
	var stored [legacyHeader]byte
	if _, err := io.ReadFull(raw, stored[:]); err != nil {
		return nil, err
	}
	// The last byte of the header is the high byte of the CRC, or of the
	// time where a data descriptor carries the CRC after the data.
	check := byte(f.CRC32 >> 24)
	if f.Flags&flagDescriptor != 0 {
		check = byte(f.ModifiedTime >> 8)
	}
	// Each way the password may be written whose check byte matches; a
	// wrong one matches once in 256 times.
	var passed []*legacyKeys
	for _, form := range legacyForms(password) {
		k, head := newLegacyKeys(form), stored
		k.decrypt(head[:])
		if head[legacyHeader-1] == check {
			passed = append(passed, k)
		}
	}
	switch len(passed) {
	case 0:
		return nil, ErrPassword
	case 1:
		return legacyBody(f, raw, passed[0])
	}
	// Told apart by reading the entry through with each, from the start,
	// for the one whose CRC checks. The way the right one is written
	// always matches, so one does, unless the entry is damaged.
	for _, k := range passed {
		if legacyChecks(f, *k) {
			return legacyBody(f, raw, k)
		}
	}
	return nil, ErrDamaged
}

// legacyBody returns what f holds, decrypted with keys, its bytes after
// the header coming from raw.
func legacyBody(f *zip.File, raw io.Reader, keys *legacyKeys) (io.ReadCloser, error) {
	body := &legacyReader{in: io.LimitReader(raw, int64(f.CompressedSize64-legacyHeader)), keys: keys}
	out, err := decompress(f.Method, body)
	if err != nil {
		return nil, err
	}
	return &checked{in: out, crc: crc32.NewIEEE(), want: f.CRC32, size: f.UncompressedSize64}, nil
}

// legacyChecks reports whether f, read through with keys as they are
// once its header is decrypted, holds what its CRC says.
func legacyChecks(f *zip.File, keys legacyKeys) bool {
	raw, err := f.OpenRaw()
	if err != nil {
		return false
	}
	if _, cerr := io.CopyN(io.Discard, raw, legacyHeader); cerr != nil {
		return false
	}
	rc, err := legacyBody(f, raw, &keys)
	if err != nil {
		return false
	}
	_, err = io.Copy(io.Discard, rc)
	return errors.Join(err, rc.Close()) == nil
}

// legacyReader decrypts what in reads.
type legacyReader struct {
	in   io.Reader
	keys *legacyKeys
}

func (r *legacyReader) Read(p []byte) (int, error) {
	n, err := r.in.Read(p)
	r.keys.decrypt(p[:n])
	return n, err
}

// checked reads in, and at its end checks the CRC of what it read, and
// its size, against want and size.
type checked struct {
	in   io.ReadCloser
	crc  hash.Hash32
	want uint32
	size uint64
	read uint64
	// noCRC leaves the CRC unchecked, for an entry that does not store
	// one.
	noCRC bool
	// end, when set, checks more once in is read to its end.
	end func() error
}

func (c *checked) Read(p []byte) (int, error) {
	n, err := c.in.Read(p)
	c.crc.Write(p[:n])
	c.read += uint64(n)
	// What does not decompress was damaged, or decrypted with a wrong
	// password that passed the check before it.
	var corrupt flate.CorruptInputError
	if errors.As(err, &corrupt) || errors.Is(err, io.ErrUnexpectedEOF) {
		return n, fmt.Errorf("%w: %w", ErrDamaged, err)
	}
	if err == io.EOF {
		if c.read != c.size || !c.noCRC && c.crc.Sum32() != c.want {
			return n, ErrDamaged
		}
		if c.end != nil {
			if eerr := c.end(); eerr != nil {
				return n, eerr
			}
		}
	}
	return n, err
}

// Close closes what checked reads.
func (c *checked) Close() error { return c.in.Close() }

// flateReader returns what in holds, compressed with Deflate.
func flateReader(in io.Reader) io.ReadCloser { return flate.NewReader(in) }
