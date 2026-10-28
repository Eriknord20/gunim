package filemanager

import "github.com/marrasen/gunim"

// CmdTrashCan opens the trash of the file system the window shows, Go ›
// Trash: a page over the browser that lists what the trash holds, where
// the user puts items back where they came from, deletes them for good,
// or empties the trash. The file system needs to be a [TrashCan].
const CmdTrashCan = "trash-can"

// CmdEmptyTrash empties the trash of the file system the window shows,
// after asking: all it holds is deleted for good.
const CmdEmptyTrash = "empty-trash"

// TrashPath is the path of the trash, as the sidebar's place for it has
// it: going to it opens the trash page over the folder showing, and a
// window opened on it opens on the home folder with the page over it. A
// program that asks a window to [Window.Show] it, as for a Visit, opens
// the trash of that file system.
const TrashPath = "trash:"

// TrashRow is an item of the trash as the page shows it.
type TrashRow struct {
	// Key names the item, as [TrashItem.ID] does.
	Key  string
	Name string
	// From is the folder it came from, as the window shows paths, and
	// Path the whole path it came from.
	From, Path string
	// Deleted says when it went to the trash, and DeletedUnix is that in
	// seconds, 0 where the trash does not say, to sort by.
	Deleted     string
	DeletedUnix int64
	// Size is a file's size in words, and Bytes in bytes, -1 for a
	// folder.
	Size  string
	Bytes int64
	Dir   bool
	Tint  Tint
}

// Trash is the state of the trash page.
type Trash struct {
	// Seq names the page, and Gen each state of it.
	Seq, Gen int
	// Title is what the system calls its trash, as Recycle Bin, and Where
	// names the machine or the file system it is on, if the program names
	// it.
	Title, Where string
	Rows         []TrashRow
	// RowsGen changes when Rows do, so the page sorts only rows it has
	// not.
	RowsGen int
	// Reading says the trash is being read, and Err why it could not be.
	Reading bool
	Err     string
	// Busy says the page is putting items back or deleting them, which it
	// does one thing at a time.
	Busy bool
	// Note says what came of the last thing the page was asked to do, and
	// NoteErr that it failed.
	Note    string
	NoteErr bool
	// Take says the page takes the keyboard as it shows, as it does when
	// the user opens it, and not when it moves with a pane to another
	// window.
	Take bool
}

// TrashAct asks page Seq to do Act to the items of Keys.
type TrashAct struct {
	Seq  int
	Keys []string
	Act  string
}

// The acts of [TrashAct].
const (
	// TrashRestore puts the items back where they came from.
	TrashRestore = "restore"
	// TrashDelete deletes the items for good, after asking.
	TrashDelete = "delete"
	// TrashEmpty deletes all the trash holds for good, after asking; Keys
	// is ignored.
	TrashEmpty = "empty"
	// TrashRead reads the trash again; Keys is ignored.
	TrashRead = "read"
	// TrashShow shows the folder the first item came from in another
	// window, or another tab where the program opens them so.
	TrashShow = "show"
)

// TrashClosed says page Seq closed.
type TrashClosed struct{ Seq int }

func init() {
	gunim.RegisterType[Trash]("files.trash")
	gunim.RegisterType[TrashAct]("files.trash-act")
	gunim.RegisterType[TrashClosed]("files.trash-closed")
}
