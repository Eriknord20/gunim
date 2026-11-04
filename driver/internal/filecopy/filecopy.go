// Package filecopy copies files another application hands over into the
// application's cache, under the name they came with. Android hands
// over open files and names from outside the program, so it treats each
// name as untrusted. It holds no Android calls, so its tests run
// anywhere.
package filecopy

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode"
)

// maxName is the most bytes of a copy's name, under the 255 file systems
// allow.
const maxName = 200

// A copy may take no more than MaxCopy, and must leave reserve free on
// the disk, as what it reads comes from another application, which could
// send without end.
const (
	MaxCopy = 16 << 30
	reserve = 256 << 20
)

// A File is one file to copy: an open file descriptor, which Take
// closes, or -1 for one the system failed to open, and the name it came
// with.
type File struct {
	FD   int
	Name string
}

// A Cache makes copies in folders of their own under one folder, so two
// files of one name both keep it.
type Cache struct {
	dir string
	n   atomic.Int64
	// free is the bytes free on the disk holding a folder, and whether it
	// can tell.
	free func(dir string) (int64, bool)
}

// New returns a Cache in dir, and clears what an earlier run left there.
func New(dir string) *Cache {
	_ = os.RemoveAll(dir)
	return &Cache{dir: dir, free: freeSpace}
}

// Take copies files and returns the paths of the copies in their order.
// With exts, a list of extensions as .hej, it takes only files whose
// name ends in one of them, whatever their case. The error tells of each
// file left behind; the paths hold the rest.
func (c *Cache) Take(files []File, exts []string) ([]string, error) {
	var paths []string
	var errs []error
	for _, f := range files {
		name := SafeName(f.Name)
		if f.FD < 0 {
			errs = append(errs, fmt.Errorf("%s: the system failed to open it", name))
			continue
		}
		file := os.NewFile(uintptr(f.FD), name)
		if exts != nil && !Matches(name, exts) {
			_ = file.Close()
			errs = append(errs, fmt.Errorf("%s: its extension is outside %s", name, strings.Join(exts, ", ")))
			continue
		}
		p, err := c.copy(file, name)
		_ = file.Close()
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		paths = append(paths, p)
	}
	return paths, errors.Join(errs...)
}

// copy writes r to a new folder under the cache, and leaves nothing
// behind where it fails.
func (c *Cache) copy(r io.Reader, name string) (string, error) {
	dir := filepath.Join(c.dir, strconv.FormatInt(c.n.Add(1), 10))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	p := filepath.Join(dir, name)
	w, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		limit := int64(MaxCopy)
		if free, ok := c.free(dir); ok {
			limit = min(limit, free-reserve)
		}
		var n int64
		n, err = io.Copy(w, io.LimitReader(r, max(limit, 0)+1))
		if err == nil && n > limit {
			err = fmt.Errorf("it is too big: there is room for %d bytes", max(limit, 0))
		}
		if cerr := w.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return p, nil
}

// SafeName returns name as one file name inside a folder. A name comes
// from the application handing the file over, which may make it climb
// out of the folder, so only its last part counts. Control characters
// go, a long name shortens and keeps its extension, and a name with
// nothing left becomes "file".
func SafeName(name string) string {
	name = strings.ToValidUTF8(name, "_")
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return "file"
	}
	if len(name) > maxName {
		ext := filepath.Ext(name)
		if len(ext) > maxName/2 {
			ext = ""
		}
		stem := name[:maxName-len(ext)]
		name = strings.ToValidUTF8(stem, "") + ext
	}
	return name
}

// Matches reports whether name ends in one of exts, whatever its case.
func Matches(name string, exts []string) bool {
	name = strings.ToLower(name)
	for _, e := range exts {
		if e != "" && strings.HasSuffix(name, strings.ToLower(e)) {
			return true
		}
	}
	return false
}
