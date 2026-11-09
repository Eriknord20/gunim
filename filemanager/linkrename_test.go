//go:build !windows

package filemanager

import (
	"errors"
	"os"
	"testing"
)

func TestALinkRenameLeavesToTheCallerWhatItCannotMove(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "a.txt", "d/x.txt")
	if err := os.Symlink("a.txt", at(root, "ln")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"d", "ln"} {
		if err := linkRename(at(root, name), at(root, name+"2")); !errors.Is(err, errors.ErrUnsupported) {
			t.Errorf("a link rename of %s made %v", name, err)
		}
		if _, err := os.Lstat(at(root, name)); err != nil {
			t.Errorf("%s moved, or went: %v", name, err)
		}
	}
	if err := linkRename(at(root, "a.txt"), at(root, "b.txt")); err != nil || contents(t, at(root, "b.txt")) != "a.txt" {
		t.Fatalf("a link rename of a file made %v", err)
	}
}
