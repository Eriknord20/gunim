package filemanager

import (
	"fmt"
	"slices"
	"strings"

	"github.com/marrasen/gunim"
)

// Open with: the programs the computer has for a file, offered in a
// submenu of the file's context menu, and the system's dialog to choose
// another. Only Windows tells them; elsewhere the item does not show.

// OpenWithAsked asks for the programs that open the file at Path, for the
// Open with submenu, as the context menu opens on the file.
type OpenWithAsked struct {
	Path string
}

// OpenWithApp is a program that opens a file: ID is how the system finds
// it again, and Name what the menu calls it.
type OpenWithApp struct {
	ID, Name string
}

// OpenWithMenu is a patch with the programs that open the file at Path,
// which fill the Open with submenu in place while the menu is open.
type OpenWithMenu struct {
	Path string
	Apps []OpenWithApp
}

// OpenWith asks to open the file at Path with the program ID, or with
// the one the user chooses in the system's dialog when ID is empty.
type OpenWith struct {
	Path, ID string
}

func init() {
	gunim.RegisterType[OpenWithAsked]("files.open-with-asked")
	gunim.RegisterType[OpenWithMenu]("files.open-with-menu")
	gunim.RegisterType[OpenWith]("files.open-with")
}

// The system's side of Open with, which tests replace.
var (
	// openWithWorks says the system offers its programs for a file.
	openWithWorks = sysOpenWithWorks
	// openWithApps returns the programs the system offers for files of
	// the extension ext, as ".txt", the one it opens them with first.
	openWithApps = sysOpenWithApps
	// openWithApp opens the file at path with the program id.
	openWithApp = sysOpenWithApp
	// openWithDialog shows the system's dialog to choose a program for the
	// file at path, which opens it with the one chosen.
	openWithDialog = sysOpenWithDialog
)

// openWithOn reports whether the window's files open with a program the
// user picks: the computer's own files, and those fetched to it.
func (a *app) openWithOn() bool {
	return openWithWorks() && (a.fs.ID() == "" || a.fetches())
}

// askedOpenWith finds the programs for the file at path, in the
// background, and sends them for the menu.
func (a *app) askedOpenWith(path string) {
	if !a.openWithOn() {
		return
	}
	ext := extOf(a.ps.Base(path))
	go func() {
		apps, err := openWithApps(ext)
		if err != nil {
			// The menu still offers the system's dialog.
			apps = nil
			a.post(func() { a.logLine("Finding the programs that open "+a.ps.Base(path), err.Error()) })
		}
		a.post(func() { a.patch(OpenWithMenu{Path: path, Apps: dedupApps(apps)}) })
	}()
}

// openWith opens the file at v.Path with the program v.ID, or with the
// one the user chooses in the system's dialog: the computer's own file at
// once, and one of another file system once a copy is fetched.
func (a *app) openWith(v OpenWith) {
	if !a.openWithOn() {
		a.fail("Files here cannot open with this computer's programs.")
		return
	}
	open := func(p string) error {
		if v.ID == "" {
			return openWithDialog(p)
		}
		return openWithApp(p, v.ID)
	}
	if a.fetches() {
		a.fetchToOpen([]string{v.Path}, open)
		return
	}
	go func() {
		if err := open(v.Path); err != nil {
			a.post(func() { a.fail(fmt.Sprintf("Opening %s: %v", a.ps.Show(v.Path), err)) })
		}
	}()
}

// extOf returns the extension of the file named name, as ".txt", or ""
// for a name with none. Windows takes all of ".gitignore" as one.
func extOf(name string) string {
	i := strings.LastIndexByte(name, '.')
	if i < 0 {
		return ""
	}
	return name[i:]
}

// dedupApps returns apps with each program once, in the order first met.
func dedupApps(apps []OpenWithApp) []OpenWithApp {
	var out []OpenWithApp
	for _, p := range apps {
		if p.ID == "" || p.Name == "" || slices.ContainsFunc(out, func(o OpenWithApp) bool { return o.ID == p.ID }) {
			continue
		}
		out = append(out, p)
	}
	return out
}
