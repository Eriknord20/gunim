package filemanager

import (
	"errors"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// PathStyle is how a file system writes its paths. The window works on
// paths as strings, so it needs to know how to join them and take them
// apart without asking the file system.
type PathStyle uint8

// The styles of path.
const (
	// SystemPaths are paths as the system the program runs on writes
	// them: with '\' and drive letters on Windows, where case does not
	// tell names apart, and with '/' elsewhere.
	SystemPaths PathStyle = iota
	// SlashPaths are paths with '/' alone and no drive letters, where
	// case tells names apart, as a server over SFTP has them.
	SlashPaths
	// DrivePaths are the paths of a Windows machine reached over SFTP:
	// kept as SFTP writes them, /C:/Users, and shown and typed as Windows
	// writes them, C:\Users. Case does not tell names apart. / is the
	// list of the machine's drives.
	DrivePaths
)

// drivesName is what the window calls / in drive paths, the list of the
// machine's drives.
const drivesName = "Drives"

// errNotAbsolute says a path does not start at the top of the file
// system, which a path of a remote one has to.
var errNotAbsolute = errors.New("the path has to start with /")

// errNoDrive says a path typed for a Windows machine names no drive.
var errNoDrive = errors.New(`the path has to start with a drive, such as C:\`)

// slash reports whether s keeps its paths with '/' alone, as a server
// over SFTP has them.
func (s PathStyle) slash() bool { return s == SlashPaths || s == DrivePaths }

// Sep is the separator between the folders of a path.
func (s PathStyle) Sep() string {
	if s.slash() {
		return "/"
	}
	return string(filepath.Separator)
}

// Join joins elem into one path, and cleans it.
func (s PathStyle) Join(elem ...string) string {
	if s.slash() {
		return path.Join(elem...)
	}
	return filepath.Join(elem...)
}

// Dir is the folder the item at p is in: p with its last element taken
// off. The top of the file system is its own folder.
func (s PathStyle) Dir(p string) string {
	if s.slash() {
		return path.Dir(p)
	}
	return filepath.Dir(p)
}

// Base is the last element of p.
func (s PathStyle) Base(p string) string {
	if s.slash() {
		return path.Base(p)
	}
	return filepath.Base(p)
}

// Split splits p after its last separator, into a folder and a name.
func (s PathStyle) Split(p string) (dir, file string) {
	if s.slash() {
		return path.Split(p)
	}
	return filepath.Split(p)
}

// Clean returns the shortest path that names what p does.
func (s PathStyle) Clean(p string) string {
	if s.slash() {
		return path.Clean(p)
	}
	return filepath.Clean(p)
}

// Abs returns p as a path from the top of the file system. A system path
// is taken from the program's working folder; a slash path has to start
// at the top already, as there is no working folder to take it from.
func (s PathStyle) Abs(p string) (string, error) {
	switch s {
	case SystemPaths:
		return filepath.Abs(p)
	case DrivePaths:
		return absDrive(p)
	case SlashPaths:
	}
	if d, ok := drivePath(p); ok {
		p = d
	}
	if !strings.HasPrefix(p, "/") {
		return "", errNotAbsolute
	}
	return path.Clean(p), nil
}

// drivePath is a Windows path typed for a file system of slash paths,
// G:\Users or G:/Users, as SFTP writes it from a Windows machine:
// /G:/Users. A drive alone, G:, is its top. It is false for anything
// else.
func drivePath(p string) (string, bool) {
	if len(p) < 2 || p[1] != ':' || !isLetter(p[0]) {
		return "", false
	}
	if len(p) > 2 && p[2] != '\\' && p[2] != '/' {
		return "", false
	}
	rest := strings.ReplaceAll(p[2:], `\`, "/")
	if rest == "" {
		rest = "/"
	}
	return "/" + strings.ToUpper(p[:1]) + ":" + rest, true
}

// VolumeName is the drive or share p starts with, as C: on Windows, and
// empty where paths have none.
func (s PathStyle) VolumeName(p string) string {
	if s.slash() {
		return ""
	}
	return filepath.VolumeName(p)
}

// Same reports whether a and b name the same place.
func (s PathStyle) Same(a, b string) bool {
	a, b = s.Clean(a), s.Clean(b)
	if s.ignoresCase() {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// ignoresCase reports whether paths of style s that differ only in the
// case of their letters name the same place.
func (s PathStyle) ignoresCase() bool {
	return s == DrivePaths || s == SystemPaths && runtime.GOOS == "windows"
}

// Rel returns the path to target from base, as filepath.Rel does. Paths
// on different volumes, or of which one is not absolute in slash paths,
// have none.
func (s PathStyle) Rel(base, target string) (string, error) {
	if !s.slash() {
		return filepath.Rel(base, target)
	}
	if !strings.HasPrefix(base, "/") || !strings.HasPrefix(target, "/") {
		return "", errNotAbsolute
	}
	b, t := splitSlash(path.Clean(base)), splitSlash(path.Clean(target))
	n := 0
	for n < len(b) && n < len(t) && (b[n] == t[n] || s == DrivePaths && strings.EqualFold(b[n], t[n])) {
		n++
	}
	parts := make([]string, 0, len(b)-n+len(t)-n)
	for range len(b) - n {
		parts = append(parts, "..")
	}
	parts = append(parts, t[n:]...)
	if len(parts) == 0 {
		return ".", nil
	}
	return strings.Join(parts, "/"), nil
}

// splitSlash splits a clean slash path from the top into its names.
func splitSlash(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

// inside reports whether p is dir or lies inside it. Paths on different
// volumes are apart.
func (s PathStyle) inside(p, dir string) bool {
	rel, err := s.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+s.Sep())
}

// placeName is what the sidebar calls the folder at p: its name, or the
// whole path for the top of a file system or a drive.
func (s PathStyle) placeName(p string) string {
	if s == DrivePaths && path.Clean(p) == "/" {
		return drivesName
	}
	name := s.Base(p)
	if name == "." || name == s.Sep() || name == "" {
		return s.Show(p)
	}
	return name
}

// Show writes p as the user reads it, types it and copies it. A drive
// path, /C:/Users, shows as Windows writes it, C:\Users, and a path from
// a folder, Users/marcusj, with '\' between its names; / stays /.
// Other paths show as they are.
func (s PathStyle) Show(p string) string {
	if s != DrivePaths || p == "" || p == "/" {
		return p
	}
	if !strings.HasPrefix(p, "/") {
		return strings.ReplaceAll(p, "/", `\`)
	}
	c := path.Clean(p)
	if !isDrive(c) {
		return p
	}
	rest := c[3:]
	if rest == "" {
		rest = "/"
	}
	return c[1:3] + strings.ReplaceAll(rest, "/", `\`)
}

// isDrive reports whether the clean slash path p is a drive, /C:, or lies
// on one.
func isDrive(p string) bool {
	return len(p) >= 3 && p[0] == '/' && isLetter(p[1]) && p[2] == ':' && (len(p) == 3 || p[3] == '/')
}

// absDrive returns p, typed for a Windows machine, as SFTP writes it:
// C:\Users, c:/Users and /c:/Users are all /C:/Users. A long path's
// \\?\ goes. / is the list of the drives.
func absDrive(p string) (string, error) {
	p = strings.ReplaceAll(strings.TrimSpace(p), `\`, "/")
	for _, long := range []string{"//?/", "//./"} {
		p = strings.TrimPrefix(p, long)
	}
	if d, ok := drivePath(p); ok {
		p = d
	}
	if strings.HasPrefix(p, "//") || !strings.HasPrefix(p, "/") {
		return "", errNoDrive
	}
	p = path.Clean(p)
	if p == "/" {
		return p, nil
	}
	if !isDrive(p) {
		return "", errNoDrive
	}
	return "/" + strings.ToUpper(p[1:2]) + p[2:], nil
}

// isLetter reports whether c is a letter of a drive.
func isLetter(c byte) bool { return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' }

// windowsNames reports whether names follow Windows's rules, which keep
// some characters out of them.
func (s PathStyle) windowsNames() bool {
	return s == DrivePaths || s == SystemPaths && runtime.GOOS == "windows"
}
