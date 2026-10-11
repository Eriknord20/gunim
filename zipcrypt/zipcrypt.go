// Package zipcrypt reads and writes the entries of a zip archive that a
// password protects, alongside archive/zip, which reads and writes the
// archive itself.
//
// It reads both kinds of protection in common use: the old one of
// PKWARE's, which zip -P, Windows' Explorer and most tools make, and
// WinZip's AES, which 7-Zip and WinZip make. Its Writer writes AES alone,
// with a 256-bit key, as the old kind is weak enough to break in
// minutes.
//
// The names of the entries, and their sizes and times, are not
// protected, by either kind: only what they hold.
package zipcrypt

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
)

// ErrPassword is why an entry does not open: the password is not the
// one it was protected with.
var ErrPassword = errors.New("the password is wrong")

// ErrDamaged is why an entry read to its end does not check: it was
// damaged, or the password was wrong in a way its first bytes could not
// tell.
var ErrDamaged = errors.New("what the entry holds does not check: it is damaged, or the password is wrong")

// ErrUnsupported is why an entry protected in a way this does not read,
// or compressed in one, does not open.
var ErrUnsupported = errors.New("the entry is protected or compressed in a way that cannot be read here")

// The general purpose flags of an entry that say how it is protected.
const (
	flagEncrypted  = 0x1
	flagDescriptor = 0x8
	flagStrong     = 0x40
)

// methodAES is the method an entry WinZip's AES protects says it is
// compressed with; the method it really is comes in its extra field.
const methodAES = 99

// Encrypted reports whether a password protects f.
func Encrypted(f *zip.File) bool { return f.Flags&flagEncrypted != 0 }

// Open returns what f holds, decrypted with password and decompressed,
// or as f.Open does where f is not protected. It returns ErrPassword
// when the first bytes of f tell the password is wrong, which they do
// all but once in 256 times for the old protection, and once in 65536
// for AES; the rest of those times, the reader returns ErrDamaged at the
// end. f may be compressed with Store or Deflate.
func Open(f *zip.File, password string) (io.ReadCloser, error) {
	if !Encrypted(f) {
		return f.Open()
	}
	if f.Flags&flagStrong != 0 {
		return nil, fmt.Errorf("%w: PKWARE's strong encryption", ErrUnsupported)
	}
	raw, err := f.OpenRaw()
	if err != nil {
		return nil, err
	}
	if f.Method == methodAES {
		return openAES(f, raw, password)
	}
	return openLegacy(f, raw, password)
}

// decompress returns what in holds, compressed with method.
func decompress(method uint16, in io.Reader) (io.ReadCloser, error) {
	switch method {
	case zip.Store:
		return io.NopCloser(in), nil
	case zip.Deflate:
		return flateReader(in), nil
	}
	return nil, fmt.Errorf("%w: compression method %d", ErrUnsupported, method)
}
