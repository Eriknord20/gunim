package filemanager

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"testing"
)

// An error is said in words: the item, and why where that is known.
func TestAnErrorIsSaidInWords(t *testing.T) {
	denied := errors.New("Access is denied.")
	for _, c := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("Local: %w", &os.LinkError{Op: "rename", Old: "/d/.app.exe.2.kakel-part", New: "/d/app.exe", Err: denied}),
			"app.exe wasn't put in place: access was denied"},
		{&fs.PathError{Op: "open", Path: "/srv/x.txt", Err: fs.ErrPermission}, "x.txt couldn't be opened: access was denied"},
		{&fs.PathError{Op: "open", Path: "/srv/gone.txt", Err: fs.ErrNotExist}, "gone.txt couldn't be opened: it is no longer there"},
		{&fs.PathError{Op: "write", Path: "/srv/big.iso", Err: errors.New("no space left on device")}, "big.iso couldn't be written: the disk is full"},
		{errors.New("The process cannot access the file because it is being used by another process."), "It is open in another program"},
		{&fs.PathError{Op: "chmod", Path: "/srv/odd", Err: errors.New("odd")}, "odd couldn't be used."},
		{&fs.PathError{Op: "open", Path: "/srv/.report.pdf.3.kakel-part", Err: fs.ErrPermission}, "report.pdf couldn't be written: access was denied"},
		{&fs.PathError{Op: "open", Path: "/srv/.files-123.part", Err: fs.ErrPermission}, "A file couldn't be written: access was denied"},
	} {
		if got := explain(c.err, SlashPaths, ""); !strings.HasPrefix(got, c.want) {
			t.Errorf("%v is said %q, want it to start %q", c.err, got, c.want)
		}
	}
	if got := explain(errors.New("something odd"), SlashPaths, ""); got != "" {
		t.Errorf("an error with nothing to tell is said %q", got)
	}
}

// A copy on its way, written under a name of its own, goes by the item
// the operation was about.
func TestAPartGoesByTheItemCopied(t *testing.T) {
	err := &fs.PathError{Op: "open", Path: "/srv/.files-123.part", Err: fs.ErrPermission}
	if got := explain(err, SlashPaths, "a.txt"); !strings.HasPrefix(got, "a.txt couldn't be written") {
		t.Fatalf("the part is said %q", got)
	}
}
