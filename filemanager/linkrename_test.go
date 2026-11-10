//go:build !windows

package filemanager

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"syscall"
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

func TestOnlyALinkTheSystemWontMakeIsMovedAnotherWay(t *testing.T) {
	link := func(e error) error { return &os.LinkError{Op: "link", Old: "a", New: "b", Err: e} }
	for _, c := range []struct {
		err  error
		want bool
	}{
		{link(syscall.EPERM), true},
		{link(syscall.EOPNOTSUPP), true},
		{link(syscall.EMLINK), true},
		{link(syscall.EIO), false},
		{link(syscall.ENOSPC), false},
		{link(syscall.ENOENT), false},
		{link(syscall.EEXIST), false},
		{fmt.Errorf("wrapped: %w", fs.ErrExist), false},
	} {
		if got := cannotLink(c.err); got != c.want {
			t.Errorf("a link that fails with %v is moved another way: %v, want %v", c.err, got, c.want)
		}
	}
}

func TestALinkRenameWhoseOldNameWentAnywayKeepsTheNew(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "a.txt")
	unlink = func(name string) error {
		if err := os.Remove(name); err != nil {
			return err
		}
		return &fs.PathError{Op: "remove", Path: name, Err: syscall.ENOENT}
	}
	t.Cleanup(func() { unlink = os.Remove })
	if err := linkRename(at(root, "a.txt"), at(root, "b.txt")); err != nil {
		t.Fatalf("a link rename whose old name went anyway failed: %v", err)
	}
	if _, err := os.Lstat(at(root, "b.txt")); err != nil {
		t.Fatalf("its only name went too: %v", err)
	}
}

func TestALinkRenameThatCannotUndoItselfSaysBoth(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "a.txt")
	unlink = func(name string) error { return &fs.PathError{Op: "remove", Path: name, Err: syscall.EIO} }
	t.Cleanup(func() { unlink = os.Remove })
	err := linkRename(at(root, "a.txt"), at(root, "b.txt"))
	if err == nil || strings.Count(err.Error(), "input/output error") != 2 {
		t.Fatalf("a link rename that could remove neither name said %v", err)
	}
	if errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("a link rename that moved the file asked for another way: %v", err)
	}
}

func TestALinkRenameWhoseOldFolderWentTakesTheNewNameOffAgain(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "old/a.txt")
	unlink = func(name string) error {
		if name == at(root, "b.txt") {
			return os.Remove(name)
		}
		if err := os.Rename(at(root, "old"), at(root, "moved")); err != nil {
			return err
		}
		return &fs.PathError{Op: "remove", Path: name, Err: syscall.ENOENT}
	}
	t.Cleanup(func() { unlink = os.Remove })
	if err := linkRename(at(root, "old/a.txt"), at(root, "b.txt")); err == nil {
		t.Fatal("a link rename whose old name moved away with its folder said it was done")
	}
	if _, err := os.Lstat(at(root, "b.txt")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the file kept its new name beside the old one: %v", err)
	}
	if _, err := os.Lstat(at(root, "moved/a.txt")); err != nil {
		t.Fatalf("the file's old name went: %v", err)
	}
}
