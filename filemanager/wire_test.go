package filemanager

import (
	"image"
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/paint"
)

// TestWire checks that everything the two halves exchange would also
// cross a socket.
func TestWire(t *testing.T) {
	thumb := paint.NewImage(image.NewRGBA(image.Rect(0, 0, 2, 2)))
	err := gunim.CheckWire(
		Shell{Light: true, ShowPreview: true, Sidebar: 240},
		Listing{Gen: 3, Path: "/a/b", Title: "b", Crumbs: []Crumb{{Name: "/", Path: "/"}}, Total: 2, All: 3,
			Sort: SortSize, Desc: true, Filter: "x", Travel: -1, CanBack: true},
		RowBlock{Gen: 3, Start: 0, Rows: []Row{{Name: "a.txt", Kind: KindFile, Size: "4 bytes", Tint: TintDocument}}},
		Selection{Gen: 3, Runs: [][2]int{{0, 2}}, Cursor: 1},
		Bands{Gen: 3, Bands: []Band{{Shares: []float32{0.5, 0.5}, First: "a"}}},
		Status{Left: "2 items", Right: "1 GB free"},
		Banner{Seq: 1, Text: "no"},
		BannerDismissed{Seq: 1},
		ItemActed{ID: "view", Paths: []string{"/a"}},
		NeedRows{Gen: 3, Starts: []int{0, 256}},
		Selected{Gen: 3, Runs: [][2]int{{1, 2}}, Cursor: 1},
		Activated{Gen: 3, Row: 1},
		Navigate{Path: "/a"},
		Command{Name: CmdCopy},
		SortClicked{Column: 2},
		FilterChanged{Text: "re"},
		WindowFocused{},
		SidebarMoved{Width: 200},
		CloseAsked{},
		FocusListing{},
		Places{Places: []Place{{Name: "C:", Path: `C:\`, Kind: "drive", Free: 1, Total: 2}}, Current: "/",
			Favourites: []Place{{Name: "Work", Path: "/w", Kind: "favourite", Color: "teal", Icon: "star"}},
			Order:      []string{FavouritesSection, GroupSection("Servers")}, Collapsed: []string{FavouritesSection}},
		SectionsArranged{Order: []string{GroupSection("Servers"), FavouritesSection}},
		SectionCollapsed{ID: FavouritesSection, Collapsed: true},
		EditFavourite{FS: "box", Path: "/a"},
		RenameFavourite{Path: "/a"},
		FavouriteEdit{Token: 3, Name: "Work", Folder: "work", Color: "red", Icon: "star"},
		FavouriteEdited{Token: 3, OK: true, Name: "Job", Color: "blue", Icon: "code"},
		FavouritesReordered{Favourites: []FavouriteAt{{Path: "/a"}, {FS: "box", Path: "/b"}}},
		Unpin{FS: "box", Path: "/a"},
		Ops{Ops: []OpView{{ID: 1, Title: "Copying", Kind: OpMove, From: "Photos", CanPause: true,
			OpProgress: OpProgress{Done: 0.5, Items: 1, ItemsTotal: 2, Bytes: 5, BytesTotal: 10, File: "a.txt", Speed: 2, Left: 3}}}},
		OpTick{ID: 1, OpProgress: OpProgress{Done: 0.75, Paused: true, Waiting: "Waiting"}},
		PauseOp{ID: 1, Resume: true},
		Notice{Title: "Copied", Undo: 1},
		CancelOp{ID: 1},
		UndoOp{ID: 1},
		ClashAsk{Op: 1, Name: "a.txt", Where: "b", New: "Text", Old: "Text", SameKind: true, CanForAll: true},
		ClashAnswered{Op: 1, Choice: ChoiceKeepBoth, All: true},
		Confirm{Token: 1, Title: "Delete?", OK: "Delete"},
		Confirmed{Token: 1, OK: true},
		Prompt{Token: 2, Title: "Rename", Text: "a.txt", OK: "Rename", Stem: 1},
		Prompted{Token: 2, Text: "b.txt", OK: true},
		ErrorBox{Title: "Failed", Body: "why"},
		DialogClosed{},
		Preview{Seq: 1, Title: "a.png", Facts: []Fact{{Label: "Size", Value: "1 KB"}}, Image: thumb},
		Counted{Seq: 1, Items: "3 items", Size: "1 KB", Counting: true},
		RevealPath{Path: "/a"},
		PaletteQuery{Seq: 2, Text: "rep"},
		PaletteResults{Seq: 2, Hits: []PaletteHit{{Title: "report.txt", Detail: "docs", Key: "file:/a/report.txt",
			At: []int{0, 1, 2}}}, Status: "3 items"},
		PalettePicked{Key: "cmd:hidden", Ctrl: true},
		OpenPalette{Query: ">sort"},
		OpSpeed{ID: 1, Rate: 1 << 20, Done: 0.25},
		OpDone{ID: 1, OK: true},
	)
	if err != nil {
		t.Fatal(err)
	}
}
