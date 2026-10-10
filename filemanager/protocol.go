// Package filemanager is a file manager: a real one, which copies,
// moves, renames and trashes, built on gunim. A program opens it with
// [Serve], or with a [Hub] for more say over its windows.
//
// A window shows one file system, an [FS]: the computer's own, as
// [LocalFS] returns it, or one the program brings, such as a server's
// over SFTP. What only some file systems can do, such as a trash or
// free space, is in smaller interfaces the window looks for, and does
// without where they are missing. The program can give the sidebar's
// places and keep the favourites itself, through [Options], and both
// may be on other file systems than the window's, as with
// [AnyFSFavourites].
//
// A window copies and moves items only on its own file system. Items go
// between file systems, by a drop or a paste, only where the program
// carries them, through [Options.Transfer]; without it, such a drop is
// refused, and Paste offers only items of the window's own file system.
// A transfer runs as one of the window's operations: it shows in the
// progress panel with the window's own, the user can stop it, and it
// asks about names that clash through the window's dialog, by its
// [TransferProgress]. Through [Options.FSName] the program names each
// file system, as the window's title says first.
//
// A program that draws windows of its own can show a file manager in a
// pane of one, among panes of its own, with [Hub.NewPane], and move it
// from window to window as it likes; see [PaneHost].
//
// The window and the program are two halves that speak only in values.
// The protocol files hold the vocabulary, one file per area. The app
// files are the program half, which does all the disk work. The view
// files are the window half, which [RegisterViews] registers. A [Hub]
// joins them.
//
// example/files is a program built on it:
//
//	CGO_ENABLED=0 go run ./example/files
//	go run ./example/files -dir /some/folder
package filemanager

import "github.com/marrasen/gunim"

// browserID is the ID of the view that holds the whole window.
const browserID gunim.ID = "browser"

// Shell is the state of the window as a whole.
type Shell struct {
	Light       bool
	ShowHidden  bool
	ShowPreview bool
	// SystemIcons says items show the icons Windows does.
	SystemIcons bool
	// Sidebar is the sidebar's width.
	Sidebar float32
	// FS is the ID of the file system the window shows, which a drag
	// carries, and Paths how it writes paths.
	FS    string
	Paths PathStyle
	// NoTrash says the file system has no trash, so the key that trashes
	// deletes, after asking.
	NoTrash bool
	// Fetches says the file system's files open with this computer's
	// programs only by a copy fetched to it first, so its folders do not
	// open with them, and nothing of it shows in the system's file
	// manager.
	Fetches bool
	// Transfers says the program can copy and move items between file
	// systems, so the window takes drops from another.
	Transfers bool
	// PlaceMenu says the program adds items to the context menus of the
	// places, so a menu asks it for them as it opens.
	PlaceMenu bool
	// OpenWith says a file opens with a program the user picks from the
	// system's, so the context menu offers Open with.
	OpenWith bool
	// Name is what the title calls the program, and Files when empty.
	Name string
	// Where names the file system, as the title says first, and is empty
	// where the program gives it no name.
	Where string
	// Actions are the program's own items on the context menu of the
	// items selected.
	Actions []ItemAction
	// Hosted are the commands the program's own menus offer for a pane,
	// which the pane's menus leave out.
	Hosted []string
	// HostMenus says the program shows the pane's menus in its own.
	HostMenus bool
	// Pane says the file manager shows in a pane of a window the program
	// draws, which leaves the window's title, theme and zoom alone.
	Pane bool
	// UploadEdited says what happens to a file fetched to open that
	// changes on this computer: ask, always upload it, or never; empty
	// asks.
	UploadEdited string
}

// Crumb is one folder of the path bar.
type Crumb struct {
	Name, Path string
}

// Listing is the folder the main area shows. Gen changes each time its
// rows change, by a new listing, a sort or a filter.
type Listing struct {
	Gen    int
	Path   string
	Title  string
	Crumbs []Crumb
	// Total is how many rows show, and All how many entries the folder
	// holds.
	Total, All int
	Sort       SortBy
	Desc       bool
	Filter     string
	// Travel is the way the user went to get here: 1 into a folder or
	// forward, -1 up or back, and 0 elsewhere or nowhere.
	Travel  int
	Loading bool
	// Err says why the folder cannot be shown.
	Err                        string
	CanBack, CanForward, CanUp bool
}

// Row is one entry as the grid shows it.
type Row struct {
	Name   string
	Kind   Kind
	Dir    bool
	Hidden bool
	Broken bool
	// Online says the file's contents are online only, and Cloud how a
	// cloud provider keeps the item.
	Online bool
	Cloud  CloudState
	// IconKey names the icon Windows shows for the item, a [SystemIcon]
	// the window is sent, or is empty for the window's own.
	IconKey  string
	Size     string
	Modified string
	Type     string
	// Tint picks the colour of the row's mark.
	Tint Tint
}

// Tint is a group of kinds of file that share a colour.
type Tint uint8

// The groups of files.
const (
	TintOther Tint = iota
	TintFolder
	TintImage
	TintVideo
	TintAudio
	TintArchive
	TintDocument
	TintCode
	TintProgram
)

// RowBlock carries rows of listing Gen, from Start on.
type RowBlock struct {
	Gen, Start int
	Rows       []Row
}

// Selection sets the rows selected in listing Gen, as runs of rows, and
// the row the keyboard is on.
type Selection struct {
	Gen    int
	Runs   [][2]int
	Cursor int
	// Slide has the selection slide there, as it does for a key that
	// moves it, for a row found by the name typed.
	Slide bool
}

// Band is what the overview strip shows for a stretch of rows: the
// share of each tint, by Tint, and the first name.
type Band struct {
	Shares []float32
	First  string
}

// Bands is the overview strip of listing Gen.
type Bands struct {
	Gen   int
	Bands []Band
}

// Status is the status bar. Note is the program's, as [Window.SetNote]
// gives it, between the two.
type Status struct {
	Left, Note, Right string
}

// Banner is a line under the path bar, for an error that stopped
// something. An empty Text hides it.
type Banner struct {
	Seq  int
	Text string
}

// ItemAction is an item of the program's own on the context menu of the
// items selected: its label, and the ID the program knows it by. Files
// offers it for files alone, and One for one item alone.
type ItemAction struct {
	Label, ID  string
	Files, One bool
}

// ItemActed says the user picked the program's item ID on the context
// menu of the items at Paths.
type ItemActed struct {
	ID    string
	Paths []string
}

// BannerDismissed says the user dismissed banner Seq.
type BannerDismissed struct{ Seq int }

// NeedRows asks for the blocks of listing Gen that start at Starts.
type NeedRows struct {
	Gen    int
	Starts []int
}

// Selected says which rows of listing Gen are selected, and which row
// the keyboard is on.
type Selected struct {
	Gen    int
	Runs   [][2]int
	Cursor int
}

// Activated says a row of listing Gen was double clicked or had Enter
// pressed on it.
type Activated struct {
	Gen, Row int
}

// Typed says Text was typed at listing Gen, to go to the item it names.
type Typed struct {
	Gen  int
	Text string
}

// Navigate asks to show the folder at Path. Typed says the user typed
// it, as Windows writes it on a file system of drive paths, and maybe in
// another case than the folders have.
type Navigate struct {
	Path  string
	Typed bool
}

// Command asks for one of the commands of the menus and keys, by name.
type Command struct {
	Name string
}

// The commands.
const (
	CmdBack    = "back"
	CmdForward = "forward"
	CmdUp      = "up"
	CmdHome    = "home"
	CmdRefresh = "refresh"
	CmdOpen    = "open"
	CmdCopy    = "copy"
	CmdCut     = "cut"
	CmdPaste   = "paste"
	// CmdZip makes a zip of the items selected, in their folder, and
	// CmdPasteZip makes one of the items on the clipboard, in the folder
	// showing. Each asks for the zip's name first. CmdExtract extracts
	// the archive selected, a zip or a tar, into a new folder beside it,
	// asking for the folder's name first.
	CmdZip       = "zip"
	CmdExtract   = "extract"
	CmdPasteZip  = "pastezip"
	CmdTrash     = "trash"
	CmdDelete    = "delete"
	CmdRename    = "rename"
	CmdNewFolder = "newfolder"
	CmdUndo      = "undo"
	CmdPin       = "pin"
	CmdHidden    = "hidden"
	// CmdSystemIcons shows the icons Windows does, or the window's own.
	CmdSystemIcons = "systemicons"
	CmdPreview     = "preview"
	CmdTheme       = "theme"
	CmdCopyPath    = "copypath"
	CmdReveal      = "reveal"
	CmdCloseApp    = "close"
	CmdSortName    = "sort.name"
	CmdSortSize    = "sort.size"
	CmdSortTime    = "sort.time"
	CmdSortType    = "sort.type"
	CmdSelectNone  = "selectnone"
	// The commands that say what happens to a file fetched to open
	// that changes on this computer.
	CmdUploadAsk    = "upload." + uploadAsk
	CmdUploadAlways = "upload." + uploadAlways
	CmdUploadNever  = "upload." + uploadNever
)

// SortClicked says a column's title was clicked.
type SortClicked struct {
	Column int
}

// FilterChanged carries the filter field's text as it is typed.
type FilterChanged struct {
	Text string
}

// WindowFocused says the window has the keyboard back.
type WindowFocused struct{}

// FocusListing is a patch that gives the keyboard to the listing, as a
// dialog closes.
type FocusListing struct{}

// CloseAsked travels when the user asks to close the window.
type CloseAsked struct{}

// SidebarMoved carries the sidebar's width once its divider is let go.
type SidebarMoved struct {
	Width float32
}

func init() {
	gunim.RegisterType[Shell]("files.shell")
	gunim.RegisterType[BannerDismissed]("files.bannerdismissed")
	gunim.RegisterType[ItemActed]("files.itemacted")
	gunim.RegisterType[Listing]("files.listing")
	gunim.RegisterType[RowBlock]("files.rows")
	gunim.RegisterType[Selection]("files.selection")
	gunim.RegisterType[Bands]("files.bands")
	gunim.RegisterType[Status]("files.status")
	gunim.RegisterType[Banner]("files.banner")
	gunim.RegisterType[NeedRows]("files.need-rows")
	gunim.RegisterType[Selected]("files.selected")
	gunim.RegisterType[Activated]("files.activated")
	gunim.RegisterType[Typed]("files.typed")
	gunim.RegisterType[Navigate]("files.navigate")
	gunim.RegisterType[Command]("files.command")
	gunim.RegisterType[SortClicked]("files.sort")
	gunim.RegisterType[FilterChanged]("files.filter")
	gunim.RegisterType[WindowFocused]("files.focused")
	gunim.RegisterType[SidebarMoved]("files.sidebar-moved")
	gunim.RegisterType[CloseAsked]("files.close-asked")
	gunim.RegisterType[FocusListing]("files.focus-listing")
}
