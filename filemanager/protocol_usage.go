package filemanager

import "github.com/marrasen/gunim"

// CmdUsage opens Disk usage on the folder selected, or on the folder
// showing when no one folder is selected: a page over the browser that
// reads all the folder holds and shows what takes the room, as boxes
// sized by it and as a tree of folders. It counts the files a cloud
// provider keeps online apart from those on this device.
const CmdUsage = "usage"

// UsageAsked asks for Disk usage on the folder at Path, as a place's
// context menu in the sidebar does.
type UsageAsked struct {
	Path string
}

// Measure is what Disk usage sizes items by.
type Measure uint8

// The measures.
const (
	// MeasureAll sizes items by all their files.
	MeasureAll Measure = iota
	// MeasureDevice sizes them by the files on this device: all but those
	// a cloud provider keeps online only.
	MeasureDevice
	// MeasureOnline sizes them by the files a cloud provider keeps online
	// only, which take no room here.
	MeasureOnline
)

// ColourBy is what colours the boxes of Disk usage.
type ColourBy uint8

// The colourings.
const (
	// ColourType colours a file by its type, as its mark in the listing.
	ColourType ColourBy = iota
	// ColourCloud colours a file by how a cloud provider keeps it: online
	// only, on this device, always kept here, or in no cloud at all.
	ColourCloud
)

// UsageItem is one item Disk usage shows: a file, a folder, or the many
// small items of a folder taken together.
type UsageItem struct {
	// Key names the item under the folder read: the names of the folders
	// on the way and its own, joined by slashes, and "" for that folder.
	Key  string
	Name string
	// Parent is the index of the item's folder among the items, and -1
	// for the first, the folder in focus.
	Parent int32
	Dir    bool
	// Rest, when above 0, says the item stands for that many items of its
	// folder too small to show one by one.
	Rest int
	// Size is the bytes of all the item's files; Online those of them a
	// cloud provider keeps online only; Local those it has on this device
	// and may free again; Pinned those it always keeps here.
	Size, Online, Local, Pinned int64
	// Files and Folders count what a folder holds, all the way down.
	Files, Folders int
	// Tint is a file's group of types, and a folder's largest.
	Tint  Tint
	Cloud CloudState
	// More says the folder holds items not sent, being small, which
	// [UsageOpened] asks for.
	More bool
	// Reading says folders under it are still to be read.
	Reading bool
	// Err says why the folder could not be read, and Skipped why it was
	// not, as one on another drive.
	Err, Skipped string
}

// UsageType is the files of one type, as Disk usage sums them up.
type UsageType struct {
	// Ext is the extension, in lower case and with no dot, or "" for the
	// files with none, and Label how it reads.
	Ext, Label string
	Tint       Tint
	// Others says the row stands for all the types not listed.
	Others               bool
	Size, Online, Device int64
	Files                int
}

// Usage is the state of the Disk usage page.
type Usage struct {
	// Seq names the page, and Gen each state of it.
	Seq, Gen int
	// Root is the whole path of the folder read, and Where names the
	// machine or the file system it is on, if the program names it.
	Root, Where string
	// Paths is how the file system writes its paths.
	Paths PathStyle
	// Focus is the key of the folder the boxes show, and Crumbs its
	// folders from Root down.
	Focus  string
	Crumbs []Crumb
	// Total is Root's item, whatever is in focus.
	Total UsageItem
	// Items are the folder in focus first, then those under it, each
	// after its folder, its items largest first.
	Items []UsageItem
	Types []UsageType
	// Free and Space are the bytes free on the drive and in all, where
	// the file system says.
	Free, Space uint64
	// Reading says the folder is still being read, and Took how long it
	// took or has taken so far.
	Reading bool
	Took    string
	// Stopped says why reading stopped before the end.
	Stopped string
	// Errs counts the folders that could not be read, and FirstErr says
	// of the first.
	Errs     int
	FirstErr string
	// Cloud says some files are with a cloud provider, so the page offers
	// what concerns them.
	Cloud   bool
	Measure Measure
	Colour  ColourBy
	// Trash says the file system has a trash, which UsageTrash moves items
	// to.
	Trash bool
	// CanKeep says the file system can tell a cloud provider which items
	// to keep on this device, so the page offers to free up space.
	CanKeep bool
	// Note says what came of the last thing the page was asked to do, and
	// NoteErr that it failed.
	Note    string
	NoteErr bool
	// Take says the page takes the keyboard as it shows, as it does when
	// the user opens it, and not when it moves with a pane to another
	// window.
	Take bool
}

// UsageFocus asks for the boxes of page Seq to show the folder Key.
type UsageFocus struct {
	Seq int
	Key string
}

// UsageOpened says the tree of page Seq opened folder Key, or shut it,
// so its items are sent, small ones too.
type UsageOpened struct {
	Seq  int
	Key  string
	Open bool
}

// UsageSet asks page Seq to measure and colour items another way.
type UsageSet struct {
	Seq     int
	Measure Measure
	Colour  ColourBy
}

// UsageAct asks page Seq to do Act to item Key.
type UsageAct struct {
	Seq int
	Key string
	Act string
}

// The acts of [UsageAct].
const (
	// UsageShow shows the item selected in its folder in another window,
	// or another tab where the program opens them so, and leaves the page
	// as it is.
	UsageShow = "show"
	// UsageOpen opens a file with its program.
	UsageOpen = "open"
	// UsageRead reads the folder again; Key is ignored.
	UsageRead = "read"
	// UsageStop stops reading; Key is ignored.
	UsageStop = "stop"
	// UsageFree has the cloud provider free the space of the item, and
	// all a folder holds, keeping them online only, and UsageKeep has it
	// always keep them on this device. [Usage.CanKeep] says whether they
	// can.
	UsageFree = "free"
	UsageKeep = "keep"
	// UsageTrash moves the item to the trash, or where there is none
	// deletes it after asking, and UsageDelete deletes it for good after
	// asking. The page counts it gone once it is.
	UsageTrash  = "trash"
	UsageDelete = "delete"
)

// UsageClosed says page Seq closed.
type UsageClosed struct{ Seq int }

func init() {
	gunim.RegisterType[Usage]("files.usage")
	gunim.RegisterType[UsageAsked]("files.usage-asked")
	gunim.RegisterType[UsageFocus]("files.usage-focus")
	gunim.RegisterType[UsageOpened]("files.usage-opened")
	gunim.RegisterType[UsageSet]("files.usage-set")
	gunim.RegisterType[UsageAct]("files.usage-act")
	gunim.RegisterType[UsageClosed]("files.usage-closed")
}
