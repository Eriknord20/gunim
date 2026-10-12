package filemanager

import (
	"errors"
	"io/fs"
	"os"
	"regexp"
	"strings"
)

// partName is the name a copy is written under before it is renamed into
// place: gunim's own, or kakel's, which keeps the name it is for.
var partName = regexp.MustCompile(`^\.(?:files-\d+\.part|(.+)\.\d+\.kakel-part)$`)

// explain says in a sentence what err, from an operation on the files of
// ps, means: the item it was about and why it failed, where that can be
// told. A copy still being written goes by the name it is for, or else
// by item, the one the operation was about where it was about one. The
// error itself stays for the details under it.
func explain(err error, ps PathStyle, item string) string {
	named, did := explainItem(err, ps, item)
	why := explainWhy(err)
	switch {
	case named != "" && why != "":
		return named + " " + did + ": " + why
	case named != "":
		return named + " " + did + "."
	case why != "":
		return strings.ToUpper(why[:1]) + why[1:]
	}
	return ""
}

// explainItem is the item err is about and what could not be done to it.
func explainItem(err error, ps PathStyle, item string) (name, did string) {
	var link *os.LinkError
	var path *fs.PathError
	switch {
	case errors.As(err, &link):
		// A copy is written beside its place and renamed into it: the
		// item is the one it was to become.
		name, did = ps.Base(link.New), "wasn't put in place"
	case errors.As(err, &path):
		name = ps.Base(path.Path)
		did = map[string]string{"open": "couldn't be opened", "read": "couldn't be read", "write": "couldn't be written",
			"remove": "couldn't be removed", "mkdir": "couldn't be made", "stat": "couldn't be looked at"}[path.Op]
		if did == "" {
			did = "couldn't be used"
		}
	default:
		return "", ""
	}
	if m := partName.FindStringSubmatch(name); m != nil {
		// A copy on its way: what it was writing.
		name, did = m[1], "couldn't be written"
		if name == "" {
			name = item
		}
		if name == "" {
			name = "A file"
		}
	}
	return name, did
}

// explainWhy says why err happened, where the system's words tell.
func explainWhy(err error) string {
	msg := strings.ToLower(err.Error())
	var why string
	switch {
	case strings.Contains(msg, "used by another process"), strings.Contains(msg, "sharing violation"):
		why = "it is open in another program. Close it there and try again."
	case errors.Is(err, fs.ErrPermission), strings.Contains(msg, "access is denied"):
		why = "access was denied. It may be read-only or open in another program, or, if it is a program, running."
	case errors.Is(err, fs.ErrNotExist):
		why = "it is no longer there."
	case strings.Contains(msg, "no space"), strings.Contains(msg, "not enough space"), strings.Contains(msg, "disk full"):
		why = "the disk is full."
	case strings.Contains(msg, "name too long"), strings.Contains(msg, "filename or extension is too long"):
		why = "its name or path is too long here."
	}
	return why
}
