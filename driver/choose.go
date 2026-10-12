package driver

import "errors"

// ChooseOptions describes a dialog for choosing files or folders to open.
type ChooseOptions struct {
	Title string
	// Folders chooses folders instead of files.
	Folders bool
	// Multiple lets more than one be chosen.
	Multiple bool
	// Folder is the folder the dialog opens in. Empty, or a folder that
	// is not there, leaves that to the system.
	Folder string
	// Filters narrow the files shown, the first chosen to begin with.
	// None shows every file.
	Filters []FileFilter
}

// FileFilter is one choice of files to show, such as "Logs" for
// "*.log" and "*.jsonl".
type FileFilter struct {
	Name     string
	Patterns []string
}

// A FileChooser is a [Window] that can show the system's dialog for
// choosing files or folders to open, owned by the window. ChooseFiles
// blocks until the dialog closes, and returns nothing when it was
// cancelled. It may be called from any goroutine but the main one.
type FileChooser interface {
	ChooseFiles(o ChooseOptions) ([]string, error)
}

// ErrNoChooser is returned where the platform has no file dialog gunim
// can show.
var ErrNoChooser = errors.New("driver: no file dialog on this platform")

// SaveOptions describes a dialog for choosing where to save a file.
type SaveOptions struct {
	Title string
	// Name is the file name the dialog starts with.
	Name string
	// Folder is the folder the dialog opens in, as the folder of the
	// file being saved again under another name. Empty, or a folder
	// that is not there, leaves that to the system.
	Folder string
	// Filters offer kinds of file to save as, the first chosen to begin
	// with. None offers every file.
	Filters []FileFilter
}

// A FileSaver is a [Window] that can show the system's dialog for choosing
// where to save a file, owned by the window. SaveFile blocks until the
// dialog closes, and returns "" when it was cancelled. The dialog asks
// before it lets an existing file be chosen. It may be called from any
// goroutine but the main one.
type FileSaver interface {
	SaveFile(o SaveOptions) (string, error)
}
