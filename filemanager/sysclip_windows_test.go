//go:build windows && (amd64 || arm64)

package filemanager

import (
	"context"
	"crypto/sha256"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// TestTheSystemClipboardHandsOverItsFiles reads the files on the system
// clipboard as Paste does, and says what they are. It runs with
// GUNIM_CLIPBOARD=1, after copying files: in Explorer, or in a Remote
// Desktop session, for files held as contents.
func TestTheSystemClipboardHandsOverItsFiles(t *testing.T) {
	if os.Getenv("GUNIM_CLIPBOARD") != "1" {
		t.Skip("set GUNIM_CLIPBOARD=1, after copying files, to read them off the system clipboard")
	}
	var c osClip
	if !c.hasFiles() {
		t.Fatal("the clipboard holds no files")
	}
	sf, err := c.files()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("seq %d, cut %v, paths %q, names %q", sf.seq, sf.cut, sf.paths, sf.names)
	if len(sf.paths) > 0 {
		return
	}
	err = c.outside(func(of outsideFiles) error {
		for i, it := range of.items() {
			if it.dir {
				t.Logf("%d: folder %s, changed %v", i, it.name, it.mod)
				continue
			}
			in, err := of.open(i)
			if err != nil {
				return err
			}
			h := sha256.New()
			n, err := io.Copy(h, in)
			if err := in.Close(); err != nil {
				return err
			}
			if err != nil {
				return err
			}
			t.Logf("%d: %s, %d bytes read of %d said, sha256 %x, changed %v", i, it.name, n, it.size, h.Sum(nil), it.mod)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	rec, err := runJob(context.Background(), job{kind: OpCopy, srcs: sf.names, dest: dest, outside: true},
		env{fs: LocalFS(), outside: c.outside})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("pasted %q into %s", rec.landed, dest)
	err = filepath.WalkDir(dest, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		t.Logf("%s: %d bytes, sha256 %x", p, len(b), sha256.Sum256(b))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
