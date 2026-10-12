package zipcrypt

import (
	"archive/zip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/binary"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
)

// WinZip's AES, as its specification "AES Encryption Information:
// Encryption Specification AE-1 and AE-2" sets out.
const (
	// aesExtraID is the ID of the extra field that says how an entry is
	// protected, and aesExtraLen the length of its data.
	aesExtraID  = 0x9901
	aesExtraLen = 7
	// aesIterations is how many times PBKDF2 hashes the password.
	aesIterations = 1000
	// aesMACLen is how much of the HMAC-SHA1 of what is stored follows
	// it, and aesCheckLen the length of the password check before it.
	aesMACLen   = 10
	aesCheckLen = 2
	// ae1 and ae2 are the two versions: AE-2 stores no CRC.
	ae1 = 1
	ae2 = 2
	// aes256 is the strength of a 256-bit key.
	aes256 = 3
)

// aesSpec is how an entry WinZip's AES protects is protected and
// compressed, as its extra field says.
type aesSpec struct {
	version  uint16
	strength byte
	method   uint16
}

// keyLen is the length of the key of strength s, and saltLen that of its
// salt: half the key's.
func (s aesSpec) keyLen() int  { return 8 + 8*int(s.strength) }
func (s aesSpec) saltLen() int { return s.keyLen() / 2 }

// aesSpecOf reads f's extra field for how it is protected.
func aesSpecOf(f *zip.File) (aesSpec, error) {
	extra := f.Extra
	for len(extra) >= 4 {
		id, size := binary.LittleEndian.Uint16(extra), int(binary.LittleEndian.Uint16(extra[2:]))
		extra = extra[4:]
		if size > len(extra) {
			break
		}
		if id == aesExtraID && size >= aesExtraLen && string(extra[2:4]) == "AE" {
			s := aesSpec{version: binary.LittleEndian.Uint16(extra), strength: extra[4], method: binary.LittleEndian.Uint16(extra[5:])}
			if s.strength < 1 || s.strength > 3 {
				return s, fmt.Errorf("%w: AES strength %d", ErrUnsupported, s.strength)
			}
			return s, nil
		}
		extra = extra[size:]
	}
	return aesSpec{}, fmt.Errorf("%w: AES without its extra field", ErrUnsupported)
}

// aesKeys derives the key to encrypt with, the key to sign with and the
// password check from password and salt, for keys keyLen bytes long.
func aesKeys(password string, salt []byte, keyLen int) (enc, mac, check []byte, err error) {
	all, err := pbkdf2.Key(sha1.New, password, salt, aesIterations, 2*keyLen+aesCheckLen)
	if err != nil {
		return nil, nil, nil, err
	}
	return all[:keyLen], all[keyLen : 2*keyLen], all[2*keyLen:], nil
}

// openAES opens f, which WinZip's AES protects, its bytes as they are
// stored coming from raw.
func openAES(f *zip.File, raw io.Reader, password string) (io.ReadCloser, error) {
	spec, err := aesSpecOf(f)
	if err != nil {
		return nil, err
	}
	saltLen := spec.saltLen()
	over := uint64(saltLen + aesCheckLen + aesMACLen)
	if f.CompressedSize64 < over {
		return nil, ErrDamaged
	}
	head := make([]byte, saltLen+aesCheckLen)
	if _, rerr := io.ReadFull(raw, head); rerr != nil {
		return nil, rerr
	}
	enc, macKey, check, err := aesKeys(password, head[:saltLen], spec.keyLen())
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare(check, head[saltLen:]) != 1 {
		return nil, ErrPassword
	}
	block, err := aes.NewCipher(enc)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha1.New, macKey)
	stored := io.LimitReader(raw, int64(f.CompressedSize64-over))
	body := &aesReader{in: stored, ctr: newWinZipCTR(block), mac: mac}
	out, err := decompress(spec.method, body)
	if err != nil {
		return nil, err
	}
	end := func() error {
		// What decompressing left unread is signed too.
		if _, err := io.Copy(io.Discard, body); err != nil {
			return err
		}
		var sum [aesMACLen]byte
		if _, err := io.ReadFull(raw, sum[:]); err != nil {
			return ErrDamaged
		}
		if !hmac.Equal(sum[:], mac.Sum(nil)[:aesMACLen]) {
			return ErrDamaged
		}
		return nil
	}
	return &checked{in: out, crc: crc32.NewIEEE(), want: f.CRC32, size: f.UncompressedSize64,
		noCRC: spec.version == ae2, end: end}, nil
}

// aesReader signs and decrypts what in reads.
type aesReader struct {
	in  io.Reader
	ctr *winZipCTR
	mac hash.Hash
}

func (r *aesReader) Read(p []byte) (int, error) {
	n, err := r.in.Read(p)
	r.mac.Write(p[:n])
	r.ctr.xor(p[:n])
	return n, err
}

// winZipCTR is AES in counter mode as WinZip counts: from 1, the
// counter's bytes little-endian, where crypto/cipher's count big-endian.
type winZipCTR struct {
	block   cipher.Block
	counter [aes.BlockSize]byte
	stream  [aes.BlockSize]byte
	used    int
}

func newWinZipCTR(block cipher.Block) *winZipCTR {
	return &winZipCTR{block: block, used: aes.BlockSize}
}

func (c *winZipCTR) xor(p []byte) {
	for i := range p {
		if c.used == aes.BlockSize {
			for j := range c.counter {
				c.counter[j]++
				if c.counter[j] != 0 {
					break
				}
			}
			c.block.Encrypt(c.stream[:], c.counter[:])
			c.used = 0
		}
		p[i] ^= c.stream[c.used]
		c.used++
	}
}
