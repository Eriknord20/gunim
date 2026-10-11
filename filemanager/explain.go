package filemanager

import (
	"errors"
	"io/fs"
	"os"
	"strings"
)

// explain says in a sentence what err, from an operation on the files of
// ps, means: the item it was about and why it failed, where that can be
// told. The error itself stays for the details under it.
func explain(err error, ps PathStyle) string {
	var item, did string
	var link *os.LinkError
	var path *fs.PathError
	switch {
	case errors.As(err, &link):
		// A copy is written beside its place and renamed into it: the
		// item is the one it was to become.
		item, did = ps.Base(link.New), "wasn't put in place"
	case errors.As(err, &path):
		item = ps.Base(path.Path)
		did = map[string]string{"open": "couldn't be opened", "read": "couldn't be read", "write": "couldn't be written",
			"remove": "couldn't be removed", "mkdir": "couldn't be made", "stat": "couldn't be looked at"}[path.Op]
		if did == "" {
			did = "couldn't be used"
		}
	}
	msg := strings.ToLower(err.Error())
	var why string
	switch {
	case strings.Contains(msg, "used by another process"), strings.Contains(msg, "sharing violation"):
		why = "it is open in another program. Close it there and try again."
	case errors.Is(err, fs.ErrPermission), strings.Contains(msg, "access is denied"):
		why = "access was denied. If it is a program, it may be running; it may also be open elsewhere, or read-only."
	case errors.Is(err, fs.ErrNotExist):
		why = "it is no longer there."
	case strings.Contains(msg, "no space"), strings.Contains(msg, "not enough space"), strings.Contains(msg, "disk full"):
		why = "the disk is full."
	case strings.Contains(msg, "name too long"), strings.Contains(msg, "filename or extension is too long"):
		why = "its name or path is too long here."
	}
	switch {
	case item != "" && why != "":
		return item + " " + did + ": " + why
	case item != "":
		return item + " " + did + "."
	case why != "":
		return strings.ToUpper(why[:1]) + why[1:]
	}
	return ""
}
