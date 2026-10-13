package filemanager

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/widget"
)

// awayHarness opens a window on a bare file system elsewhere, with its
// transfers kept in ts, whose sidebar has a folder of the computer's
// own, desk, as a favourite, and a server not reached yet, with no
// folder known, as a place.
func awayHarness(t *testing.T, ts *transfers) (h *harness, desk string) {
	t.Helper()
	root := t.TempDir()
	dir, desk := filepath.Join(root, "dir"), filepath.Join(root, "Desk")
	tree(t, dir, "a.txt", "b.txt", "sub/")
	tree(t, desk, "old.txt")
	store := newAnyStore()
	store.favs = []Favourite{{Path: desk}}
	h = openHarnessWith(t, nil, root, dir, func(o *Options) {
		onBareFS(o)
		o.Favourites = store
		o.Transfer = ts.take
		o.Places = func() ([]Place, error) {
			return []Place{{Name: "Box", Kind: "home", Group: "Servers", FS: server}}, nil
		}
	})
	h.until("the favourite and the place show", func() bool {
		return h.b.side.favs.Len() == 1 && slices.ContainsFunc(h.a.places, func(p Place) bool { return p.Name == "Box" })
	})
	h.until("the window knows it can transfer", func() bool { return h.b.shell.Transfers })
	h.frames(30)
	return h, desk
}

// sideHint returns what the sidebar says of the drop under the drag.
func (h *harness) sideHint() widget.DropSpot {
	var spot widget.DropSpot
	h.ui(func(b *browser, _ *gunim.UI) { spot, _ = b.dnd.side.Over() })
	return spot
}

func TestARemoteItemDroppedOnALocalFavouriteGoesToTheProgram(t *testing.T) {
	var ts transfers
	h, desk := awayHarness(t, &ts)
	h.choose("a.txt")
	h.script("drag-start:a.txt", "drag-over:Desk")
	if spot := h.sideHint(); spot.Refused || spot.Hint != (widget.DropHint{Text: "Copy to Desk", Effect: widget.DropCopy}) {
		t.Fatalf("over the favourite elsewhere the drop says %v, refused %v", spot.Hint, spot.Refused)
	}
	h.script("drop")
	got := ts.count(h, 1)
	want := Transfer{FromFS: "elsewhere", Paths: []string{filepath.Join(h.dir, "a.txt")}, ToFS: "", Into: desk}
	if !sameTransfer(got[0], want) {
		t.Fatalf("the drop hands over %+v, want %+v", got[0], want)
	}
	h.idle()

	// Shift moves.
	h.choose("b.txt")
	h.script("drag-start:b.txt", "drag-over:Desk+shift")
	if spot := h.sideHint(); spot.Hint != (widget.DropHint{Text: "Move to Desk", Effect: widget.DropMove}) {
		t.Fatalf("with Shift the drop says %v", spot.Hint)
	}
	h.script("drop:+shift")
	got = ts.count(h, 2)
	want = Transfer{FromFS: "elsewhere", Paths: []string{filepath.Join(h.dir, "b.txt")}, ToFS: "", Into: desk, Move: true}
	if !sameTransfer(got[1], want) {
		t.Fatalf("the drop with Shift hands over %+v, want %+v", got[1], want)
	}
	h.idle()

	// Files from another program, of the computer's own, go there too.
	outside := filepath.Join(h.root, "outside.txt")
	h.w.Input(input.Drop{Pos: h.point("Desk"), Paths: []string{outside}})
	got = ts.count(h, 3)
	want = Transfer{FromFS: "", Paths: []string{outside}, ToFS: "", Into: desk}
	if !sameTransfer(got[2], want) {
		t.Fatalf("the drop from another program hands over %+v, want %+v", got[2], want)
	}
	h.idle()
}

func TestAPlaceWithNoFolderYetTakesNoDrop(t *testing.T) {
	var ts transfers
	h, _ := awayHarness(t, &ts)
	h.choose("a.txt")
	h.script("drag-start:a.txt", "drag-over:Box")
	spot := h.sideHint()
	if hint, _ := spot.Hint.(widget.DropHint); !spot.Refused || !strings.Contains(hint.Text, "Box") {
		t.Fatalf("over a place with no folder the drop says %v, refused %v", spot.Hint, spot.Refused)
	}
	h.script("drop")
	h.frames(10)
	if len(ts.list()) != 0 {
		t.Fatalf("a drop on a place with no folder handed over %+v", ts.list())
	}
}

func TestADropOnAFavouriteElsewhereIsRefusedWithoutTransfers(t *testing.T) {
	d := FileDrag{Paths: []string{"/srv/a.txt"}, FS: server, Style: SlashPaths}
	tgt := dropTarget{fs: "", ps: SystemPaths, dir: "/home/me/Desktop", name: "Desktop"}
	if _, hint, ok := planDrop(tgt, false, d, 0); ok || hint.Text != "Cannot drop on another file system" {
		t.Fatalf("without transfers the drop says %q, ok %v", hint.Text, ok)
	}
	// Items of the favourite's own file system, from a window elsewhere,
	// go through the program, which knows them.
	d = FileDrag{Paths: []string{"/home/me/a.txt"}, Style: SlashPaths}
	tgt.ps = SlashPaths
	plan, hint, ok := planDrop(tgt, true, d, 0)
	if !ok || !plan.Away || !plan.Copy || plan.To != "" || hint.Text != "Copy to Desktop" {
		t.Fatalf("the drop plans %+v, saying %q", plan, hint.Text)
	}
	d.Paths = []string{"/home/me/Desktop/a.txt"}
	if _, hint, ok := planDrop(tgt, true, d, 0); ok || hint.Text != "Already in Desktop" {
		t.Fatalf("an item already there says %q", hint.Text)
	}
}

// dragFetchOf returns the fetch of the drag going on in h's window.
func (h *harness) dragFetchOf() (f dragFetch, ok bool) {
	h.ui(func(b *browser, _ *gunim.UI) {
		if p, found := b.dnd.fetches[b.dnd.dragging]; found {
			f, ok = *p, true
		}
	})
	return f, ok
}

// startDragOf presses on the row name and moves a little, which starts a
// drag of the rows selected.
func (h *harness) startDragOf(name string) {
	h.t.Helper()
	at := h.point(name)
	h.w.Input(input.PointerMove{Pos: at})
	h.w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1})
	h.w.Input(input.PointerMove{Pos: at.Add(geom.Pt(12, 6))})
	h.frames(3)
}

// dragOutAndWait carries the drag far past the window's edge, and waits
// for it to go out to other programs.
func (h *harness) dragOutAndWait() []string {
	h.t.Helper()
	before := len(h.w.Offscreen().DraggedOut())
	h.w.Input(input.PointerMove{Pos: geom.Pt(-400, -400)})
	h.until("the drag goes out", func() bool { return len(h.w.Offscreen().DraggedOut()) > before })
	out := h.w.Offscreen().DraggedOut()
	if len(out) != before+1 {
		h.t.Fatalf("handed out %v, want one drag more", out)
	}
	return out[before]
}

// dropOut ends the drag that went out to other programs, as one taking
// it does.
func (h *harness) dropOut() {
	h.w.Input(driver.DragOutEnded{Taken: true})
	h.frames(5)
}

func TestARemoteFileDraggedOutIsFetchedFirst(t *testing.T) {
	h, base, _ := newFetchHarness(t, onBareFS, "a.txt", "b.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 2 })
	h.choose("a.txt", "b.txt")
	h.startDragOf("a.txt")
	// The fetch starts with the drag, before it leaves the window.
	h.until("the copies are fetched", func() bool {
		f, ok := h.dragFetchOf()
		return ok && f.done
	})
	if got := filesIn(t, base); len(got) != 2 {
		t.Fatalf("the drag fetched %v", got)
	}
	if len(h.w.Offscreen().DraggedOut()) != 0 {
		t.Fatal("the drag went out before it left the window")
	}
	out := h.dragOutAndWait()
	if len(out) != 2 {
		t.Fatalf("handed out %v, want the two copies", out)
	}
	for i, name := range []string{"a.txt", "b.txt"} {
		if filepath.Base(out[i]) != name || !strings.HasPrefix(out[i], base+string(filepath.Separator)) {
			t.Fatalf("handed out %s for %s", out[i], name)
		}
		if got := contents(t, out[i]); got != name {
			t.Fatalf("the copy of %s holds %q", name, got)
		}
	}
	h.dropOut()
	if _, ok := h.dragFetchOf(); ok {
		t.Fatal("the fetch outlived its drag")
	}
	h.idle()
	if len(h.a.dnd.fetches) != 0 {
		t.Fatal("the app keeps a fetch that ended")
	}

	// Dragged again unchanged, the copies are taken as they are.
	before := h.a.ops.next
	h.choose("a.txt")
	h.startDragOf("a.txt")
	out = h.dragOutAndWait()
	if len(out) != 1 || filepath.Base(out[0]) != "a.txt" || len(filesIn(t, base)) != 2 {
		t.Fatalf("handed out %v, with copies %v", out, filesIn(t, base))
	}
	h.idle()
	if h.a.ops.next != before+1 {
		t.Fatalf("the second drag ran %d operations", h.a.ops.next-before)
	}
	h.dropOut()
}

func TestARemoteFolderDraggedOutIsFetchedWithAllItHolds(t *testing.T) {
	h, base, _ := newFetchHarness(t, onBareFS, "sub/b.txt", "sub/deep/c.txt", "sub/empty/")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	h.choose("sub")
	h.startDragOf("sub")
	out := h.dragOutAndWait()
	if len(out) != 1 || filepath.Base(out[0]) != "sub" || !strings.HasPrefix(out[0], base) {
		t.Fatalf("handed out %v", out)
	}
	if got := contents(t, filepath.Join(out[0], "b.txt")); got != "sub/b.txt" {
		t.Fatalf("b.txt holds %q", got)
	}
	if got := contents(t, filepath.Join(out[0], "deep", "c.txt")); got != "sub/deep/c.txt" {
		t.Fatalf("deep/c.txt holds %q", got)
	}
	if info, err := os.Stat(filepath.Join(out[0], "empty")); err != nil || !info.IsDir() {
		t.Fatal("the empty folder was left behind")
	}
	h.dropOut()
	h.idle()

	// The folder changes: its copy follows, and what went goes.
	if err := os.Remove(filepath.Join(h.dir, "sub", "b.txt")); err != nil {
		t.Fatal(err)
	}
	tree(t, h.dir, "sub/new.txt")
	h.choose("sub")
	h.startDragOf("sub")
	again := h.dragOutAndWait()
	if len(again) != 1 || again[0] != out[0] {
		t.Fatalf("the second drag handed out %v, want %v", again, out)
	}
	if exists(filepath.Join(out[0], "b.txt")) || contents(t, filepath.Join(out[0], "new.txt")) != "sub/new.txt" {
		t.Fatal("the folder's copy did not follow the folder")
	}
	h.dropOut()
	h.idle()
}

func TestADragTooBigToFetchIsRefused(t *testing.T) {
	was := dragOutMax
	dragOutMax = 4
	t.Cleanup(func() { dragOutMax = was })
	h, base, _ := newFetchHarness(t, onBareFS, "a.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	h.choose("a.txt")
	h.startDragOf("a.txt")
	h.until("the fetch gives up", func() bool {
		f, ok := h.dragFetchOf()
		return ok && f.done
	})
	f, _ := h.dragFetchOf()
	_, err := f.export()
	var ee *gunim.ExportError
	if !errors.As(err, &ee) || ee.Wait || ee.Hint != (widget.DropHint{Text: "Too big to drag out"}) {
		t.Fatalf("a drag too big says %v", err)
	}
	h.w.Input(input.PointerMove{Pos: geom.Pt(-400, -400)})
	h.frames(10)
	if len(h.w.Offscreen().DraggedOut()) != 0 || len(filesIn(t, base)) != 0 {
		t.Fatal("a drag too big was fetched or handed out")
	}
	h.w.Input(input.PointerUp{Pos: geom.Pt(-400, -400), Button: input.ButtonPrimary})
	h.idle()
}

func TestADragOutWaitsForItsFilesWithoutHoldingTheWindow(t *testing.T) {
	release := make(chan struct{})
	h, _, _ := newFetchHarness(t, func(o *Options) { o.FS = gateFS{bareFS{LocalFS()}, release} })
	open := openOnCleanup(t, release)
	if err := os.WriteFile(filepath.Join(h.dir, "film.mkv"), make([]byte, 3*copyBuffer), 0o644); err != nil {
		t.Fatal(err)
	}
	h.do(Command{Name: CmdRefresh})
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	h.choose("film.mkv")
	h.startDragOf("film.mkv")
	h.w.Input(input.PointerMove{Pos: geom.Pt(-400, -400)})
	// The window goes on drawing while the file is on its way.
	h.frames(30)
	if len(h.w.Offscreen().DraggedOut()) != 0 {
		t.Fatal("the drag went out before its file was here")
	}
	f, ok := h.dragFetchOf()
	if !ok || f.done {
		t.Fatal("the fetch is not on its way")
	}
	// It says how far the fetch has got, in words and on a bar.
	h.until("the fetch says how big it is", func() bool {
		f, ok = h.dragFetchOf()
		return ok && f.total == 3*copyBuffer
	})
	_, err := f.export()
	var ee *gunim.ExportError
	if !errors.As(err, &ee) || !ee.Wait {
		t.Fatalf("a drag waiting for its file says %v", err)
	}
	want := widget.DropHint{Text: "Fetching… " + humanBytes(f.bytes) + " of " + humanBytes(3*copyBuffer), Effect: widget.DropCopy,
		Bar: true, Progress: float32(f.bytes) / float32(3*copyBuffer)}
	if ee.Hint != want {
		t.Fatalf("a drag waiting for its file says %+v, want %+v", ee.Hint, want)
	}
	open()
	out := h.dragOutAndWait()
	if len(out) != 1 || filepath.Base(out[0]) != "film.mkv" {
		t.Fatalf("handed out %v", out)
	}
	if info, err := os.Stat(out[0]); err != nil || info.Size() != 3*copyBuffer {
		t.Fatal("the file went out before it was all here")
	}
	h.dropOut()
	h.idle()
}

func TestALocalDragFetchesNothing(t *testing.T) {
	h := newDndHarness(t, "a.txt")
	h.choose("a.txt")
	h.startDragOf("a.txt")
	if _, ok := h.dragFetchOf(); ok || len(h.a.ops.running) != 0 {
		t.Fatal("a drag of the computer's own files fetches them")
	}
	out := h.dragOutAndWait()
	if !slices.Equal(out, []string{filepath.Join(h.dir, "a.txt")}) {
		t.Fatalf("handed out %v", out)
	}
}

func TestEndingADragStopsItsFetch(t *testing.T) {
	release := make(chan struct{})
	h, base, _ := newFetchHarness(t, func(o *Options) { o.FS = gateFS{bareFS{LocalFS()}, release} })
	open := openOnCleanup(t, release)
	if err := os.WriteFile(filepath.Join(h.dir, "film.mkv"), make([]byte, 3*copyBuffer), 0o644); err != nil {
		t.Fatal(err)
	}
	h.do(Command{Name: CmdRefresh})
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	h.choose("film.mkv")
	h.startDragOf("film.mkv")
	h.until("the fetch runs", func() bool { return len(h.a.dnd.fetches) == 1 })
	// Let go inside the window: the drag needs no copy.
	h.w.Input(input.PointerUp{Pos: h.point("film.mkv"), Button: input.ButtonPrimary})
	// The end reaches the program, which stops the fetch where it waits.
	h.frames(10)
	open()
	h.until("the fetch stops", func() bool { return len(h.a.dnd.fetches) == 0 })
	h.idle()
	if got := filesIn(t, base); len(got) != 0 {
		t.Fatalf("a stopped fetch left %v", got)
	}
}
