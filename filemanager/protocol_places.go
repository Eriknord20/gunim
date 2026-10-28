package filemanager

import "github.com/marrasen/gunim"

// Place is a folder the sidebar offers: one of the user's, a volume or a
// favourite.
type Place struct {
	Name, Path string
	// Kind is home, desktop, documents, downloads, pictures, drive,
	// favourite or trash, and picks the colour of the place's mark. The
	// trash's Path is TrashPath.
	Kind string
	// Free and Total are a volume's space in bytes, when it has one.
	Free, Total uint64
	// Err says why the place cannot be read.
	Err string
	// Note is a line under the name, such as whether a server is
	// connected. Err and the space show in its place.
	Note string
	// Group is the heading the place shows under, such as the name of
	// a computer, and empty for the first, which is Places. A group
	// starts where the group of the place before it ends.
	Group string
	// FS is the ID of the file system the place is on, empty for the
	// computer's own. A click on a place on another file system than
	// the window's asks for a Visit.
	FS string
	// Lit draws the place's icon green, as a machine connected is.
	Lit bool
	// Color and Icon are a favourite's colour and icon, by name, as
	// Favourite has them.
	Color, Icon string
}

// PlaceItem is an item of the program's own in a place's context menu.
type PlaceItem struct {
	Label, ID string
}

// PlaceMenuAsked asks the program for its items in the context menu of
// Place, which opens once PlaceMenuItems with the same Seq answers.
type PlaceMenuAsked struct {
	Seq   int
	Place Place
}

// PlaceMenuItems are the program's items in the context menu asked for
// as Seq.
type PlaceMenuItems struct {
	Seq   int
	Items []PlaceItem
}

// PlaceCommanded says the user picked the program's item ID in the
// context menu of Place.
type PlaceCommanded struct {
	Place Place
	ID    string
}

// Places is the sidebar: the places the system has, the user's
// favourites, and the folder showing.
//
// The sidebar shows them in sections, each under a heading the user can
// click to close or open it, and drag to put the sections in another
// order: one section for each group of places, in the order the groups
// first come, and one for the favourites. A section's ID is
// FavouritesSection for the favourites, and from GroupSection for a
// group. Unless Order says otherwise, the first group comes first, then
// the favourites, then the other groups.
type Places struct {
	Places     []Place
	Favourites []Place
	Current    string
	// Order is the order of the sections, by ID, as the user left them.
	// A section it does not name comes after those it does, and an ID
	// of a section not shown is passed by.
	Order []string
	// Collapsed are the IDs of the sections the user closed, which show
	// their heading alone.
	Collapsed []string
}

// FavouritesSection is the ID of the sidebar's section of favourites.
const FavouritesSection = "favourites"

// GroupSection returns the ID of the sidebar's section of the places of
// group.
func GroupSection(group string) string { return "group:" + group }

// SectionsArranged carries the order of the sidebar's sections, by ID,
// as a drag or the menu of a heading left them.
type SectionsArranged struct {
	Order []string
}

// SectionCollapsed closes the sidebar's section of ID, or opens it again
// when Collapsed is false.
type SectionCollapsed struct {
	ID        string
	Collapsed bool
}

// FavouritesReordered carries the favourites in the order a drag left
// them in.
type FavouritesReordered struct {
	Favourites []FavouriteAt
}

// FavouriteAt names a favourite: the folder at Path on the file system
// of ID FS.
type FavouriteAt struct {
	FS, Path string
}

// Unpin takes the favourite at Path on the file system of ID FS off the
// sidebar.
type Unpin struct {
	FS, Path string
}

// Visit asks to go to the folder at Path on the file system of ID FS,
// which is not the one the window shows. NewWindow asks for it in a
// window of its own, as Ctrl held as the Visit is sent does too.
type Visit struct {
	FS, Path  string
	NewWindow bool
}

func init() {
	gunim.RegisterType[Places]("files.places")
	gunim.RegisterType[FavouritesReordered]("files.favourites-reordered")
	gunim.RegisterType[SectionsArranged]("files.sections-arranged")
	gunim.RegisterType[SectionCollapsed]("files.section-collapsed")
	gunim.RegisterType[Unpin]("files.unpin")
	gunim.RegisterType[Visit]("files.visit")
	gunim.RegisterType[PlaceMenuAsked]("files.place-menu-asked")
	gunim.RegisterType[PlaceMenuItems]("files.place-menu-items")
	gunim.RegisterType[PlaceCommanded]("files.place-commanded")
}
