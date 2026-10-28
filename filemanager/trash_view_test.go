package filemanager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/widget"
)

// newTrashHarness is a harness whose trash holds the items of spec in
// its folder, each moved there.
func newTrashHarness(t *testing.T, trashed []string, spec ...string) *harness {
	t.Helper()
	h := newUsageHarness(t, spec...)
	for _, rel := range trashed {
		if _, err := h.a.trash.Trash(filepath.Join(h.dir, filepath.FromSlash(rel))); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

// trashPage is the trash page showing, or nil.
func (h *harness) trashPage() *trashPage {
	var pg *trashPage
	h.ui(func(_ *browser, u *gunim.UI) { pg, _ = u.Mounted(h.a.bin.id).(*trashPage) })
	return pg
}

// openTrashPage opens the trash page, and waits for it to read the trash.
func (h *harness) openTrashPage(in gunim.Intent) *trashPage {
	h.t.Helper()
	h.do(in)
	h.until("the trash page has read the trash", func() bool {
		pg := h.trashPage()
		return pg != nil && !pg.state.Reading
	})
	return h.trashPage()
}

// trashShown are the names the trash page lists, in order.
func trashShown(pg *trashPage) []string {
	out := make([]string, 0, len(pg.rows))
	for _, r := range pg.rows {
		out = append(out, r.Name)
	}
	return out
}

// pickTrash selects the rows of the trash page named.
func (h *harness) pickTrash(pg *trashPage, names ...string) {
	h.ui(func(_ *browser, u *gunim.UI) {
		var sel [][2]int
		for i, r := range pg.rows {
			if slices.Contains(names, r.Name) {
				sel = append(sel, [2]int{i, i + 1})
			}
		}
		pg.grid.SetSelectedRows(sel, sel[0][0], u)
		pg.enable()
	})
}

// clickButton runs a button's click on page pg, as the pointer would.
func (h *harness) clickButton(pg *trashPage, click func(u *gunim.UI) gunim.Intent) {
	h.ui(func(_ *browser, u *gunim.UI) {
		if in := click(u); in != nil {
			u.Send(pg, in)
		}
	})
	h.frames(2)
}

func TestTheTrashPageListsWhatTheTrashHolds(t *testing.T) {
	h := newTrashHarness(t, []string{"a.txt", "sub"}, "a.txt", "sub/c.txt", "keep.txt")
	pg := h.openTrashPage(Command{Name: CmdTrashCan})
	if got := trashShown(pg); len(got) != 2 || !slices.Contains(got, "a.txt") || !slices.Contains(got, "sub") {
		t.Fatalf("the page lists %v", got)
	}
	for _, r := range pg.rows {
		if r.From != h.dir {
			t.Errorf("%s came from %q, want %q", r.Name, r.From, h.dir)
		}
		if r.Deleted == "" {
			t.Errorf("%s says nothing of when it was deleted", r.Name)
		}
		if r.Dir != (r.Name == "sub") || (r.Size == "") != r.Dir {
			t.Errorf("%s reads as dir %v, size %q", r.Name, r.Dir, r.Size)
		}
	}
	if !strings.HasPrefix(pg.foot.Text, "2 items") {
		t.Errorf("the foot says %q", pg.foot.Text)
	}
	// Nothing selected, nothing to restore or delete; the trash can empty.
	if !pg.restore.Disabled || !pg.remove.Disabled || pg.empty.Disabled {
		t.Errorf("restore %v, delete %v, empty %v disabled", pg.restore.Disabled, pg.remove.Disabled, pg.empty.Disabled)
	}
	if h.a.nav.path != h.dir {
		t.Errorf("the page moved the window to %s", h.a.nav.path)
	}
	h.press(input.KeyEscape, 0)
	h.until("the page has closed", func() bool { return !h.a.bin.open })
}

func TestTheTrashPageRestoresAndDeletes(t *testing.T) {
	h := newTrashHarness(t, []string{"a.txt", "b.txt", "sub"}, "a.txt", "b.txt", "sub/c.txt")
	pg := h.openTrashPage(Command{Name: CmdTrashCan})
	h.pickTrash(pg, "a.txt", "sub")
	h.clickButton(pg, pg.restore.OnClick)
	h.until("a.txt and sub are back", func() bool {
		return h.exists("a.txt") && h.exists("sub/c.txt") && slices.Equal(trashShown(pg), []string{"b.txt"}) && !pg.state.Busy
	})
	if pg.state.NoteErr || !strings.Contains(pg.state.Note, "restored") {
		t.Errorf("the page says %q", pg.state.Note)
	}
	// Delete asks first, and deletes for good.
	h.pickTrash(pg, "b.txt")
	h.press(input.KeyDelete, 0)
	h.until("the page asks", func() bool { return len(h.a.ops.dialogs) > 0 })
	c, ok := h.a.ops.dialogs[0].state.(Confirm)
	if !ok || !strings.Contains(c.Title, "b.txt") {
		t.Fatalf("the page asked %#v", h.a.ops.dialogs[0].state)
	}
	h.answer(Confirmed{Token: c.Token, OK: true})
	h.until("b.txt is gone for good", func() bool { return len(pg.rows) == 0 && !pg.state.Busy })
	if got := names(t, filepath.Join(h.root, "Trash", "files")); len(got) != 0 {
		t.Fatalf("the trash still keeps %v", got)
	}
	if h.exists("b.txt") {
		t.Fatal("the deleted file came back")
	}
	if !pg.empty.Disabled || pg.msg.Text != "The trash is empty." {
		t.Errorf("an empty trash says %q, with Empty disabled %v", pg.msg.Text, pg.empty.Disabled)
	}
}

func TestTheTrashPageSaysWhyAnItemDidNotRestore(t *testing.T) {
	h := newTrashHarness(t, []string{"a.txt"}, "a.txt")
	if err := os.WriteFile(filepath.Join(h.dir, "a.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	pg := h.openTrashPage(Command{Name: CmdTrashCan})
	h.pickTrash(pg, "a.txt")
	h.clickButton(pg, pg.restore.OnClick)
	h.until("the restore is done", func() bool { return pg.state.Note != "" && !pg.state.Busy })
	if !pg.state.NoteErr || !strings.Contains(pg.state.Note, "exists again") {
		t.Fatalf("the page says %q", pg.state.Note)
	}
	if got := trashShown(pg); !slices.Equal(got, []string{"a.txt"}) {
		t.Fatalf("the trash lists %v", got)
	}
}

func TestTheTrashPageEmptiesTheTrash(t *testing.T) {
	h := newTrashHarness(t, []string{"a.txt", "sub"}, "a.txt", "sub/c.txt")
	pg := h.openTrashPage(Command{Name: CmdTrashCan})
	h.clickButton(pg, pg.empty.OnClick)
	h.until("the page asks", func() bool { return len(h.a.ops.dialogs) > 0 })
	c := h.a.ops.dialogs[0].state.(Confirm)
	if !strings.Contains(c.Body, "2 items") {
		t.Errorf("the page asks %q", c.Body)
	}
	h.answer(Confirmed{Token: c.Token, OK: true})
	h.until("the trash is empty", func() bool { return len(pg.rows) == 0 && !pg.state.Busy && !pg.state.Reading })
	for _, sub := range []string{"files", "info"} {
		if got := names(t, filepath.Join(h.root, "Trash", sub)); len(got) != 0 {
			t.Fatalf("the trash's %s keeps %v", sub, got)
		}
	}
}

func TestTheTrashPathOpensThePageOverTheFolder(t *testing.T) {
	h := newTrashHarness(t, []string{"a.txt"}, "a.txt", "keep.txt")
	pg := h.openTrashPage(Navigate{Path: TrashPath})
	if got := trashShown(pg); !slices.Equal(got, []string{"a.txt"}) {
		t.Fatalf("the page lists %v", got)
	}
	if h.a.nav.path != h.dir {
		t.Fatalf("the trash path took the window to %q", h.a.nav.path)
	}
	// Disk usage takes the trash page's place.
	h.do(Command{Name: CmdUsage})
	h.until("Disk usage shows in place of the trash", func() bool { return h.a.usage.open && !h.a.bin.open })
}

// gatedTrash is a trash whose restores wait for the gate to open, each
// saying so on entered, where it is set.
type gatedTrash struct {
	xdgTrash
	gate    chan struct{}
	entered chan struct{}
}

func (g gatedTrash) RestoreItem(it TrashItem) error {
	if g.entered != nil {
		g.entered <- struct{}{}
	}
	<-g.gate
	return g.xdgTrash.RestoreItem(it)
}

func TestOpeningTheTrashAgainLetsARestoreFinish(t *testing.T) {
	h := newTrashHarness(t, []string{"a.txt", "b.txt"}, "a.txt", "b.txt")
	g := gatedTrash{xdgTrash: h.a.trash.(xdgTrash), gate: make(chan struct{})}
	// Opened before the harness stops the app, which waits for the
	// restore, so a test that fails early fails rather than hangs.
	var open sync.Once
	t.Cleanup(func() { open.Do(func() { close(g.gate) }) })
	h.a.trash = g
	pg := h.openTrashPage(Command{Name: CmdTrashCan})
	h.pickTrash(pg, "a.txt")
	h.clickButton(pg, pg.restore.OnClick)
	h.until("the restore runs", func() bool { return pg.state.Busy })
	// The sidebar's place, clicked while it runs.
	h.do(Navigate{Path: TrashPath})
	h.frames(5)
	if !h.a.bin.busy || !pg.state.Busy || !pg.restore.Disabled || !pg.remove.Disabled {
		t.Fatalf("opened again, the page is busy %v, restore disabled %v", pg.state.Busy, pg.restore.Disabled)
	}
	open.Do(func() { close(g.gate) })
	h.until("the restore ends, and the page says so", func() bool {
		return !pg.state.Busy && !pg.state.Reading && strings.Contains(pg.state.Note, "restored") &&
			slices.Equal(trashShown(pg), []string{"b.txt"})
	})
	if !h.exists("a.txt") {
		t.Fatal("a.txt did not come back")
	}
}

func TestATrashOnAnotherVolumeAsksToDeleteForGood(t *testing.T) {
	h := newTrashHarness(t, nil, "near.txt", "far.txt")
	h.a.trash = xdgTrash{fs: farFS{FS: LocalFS(), far: "far.txt"}, dir: filepath.Join(h.root, "Trash")}
	h.pick("near.txt", "far.txt")
	h.press(input.KeyDelete, 0)
	h.until("the window asks about the one the trash can't take", func() bool { return len(h.a.ops.dialogs) > 0 })
	c, ok := h.a.ops.dialogs[0].state.(Confirm)
	if !ok || c.Title != "Delete far.txt for good?" || !strings.Contains(c.Body, "another volume") {
		t.Fatalf("the window asked %#v", h.a.ops.dialogs[0].state)
	}
	if h.exists("near.txt") || !h.exists("far.txt") {
		t.Fatal("near.txt should be in the trash, and far.txt still here")
	}
	h.answer(Confirmed{Token: c.Token, OK: true})
	h.until("far.txt is deleted", func() bool { return !h.exists("far.txt") })
	if got := names(t, filepath.Join(h.root, "Trash", "files")); !slices.Equal(got, []string{"near.txt"}) {
		t.Fatalf("the trash holds %v", got)
	}
}

// farFS is a file system on which the item named far is on another
// volume than everything else, so a rename of it fails as one across
// volumes.
type farFS struct {
	FS
	far string
}

func (f farFS) Rename(from, to string) error {
	if filepath.Base(from) == f.far {
		return crossDevice{errors.New("not the same device")}
	}
	return f.FS.Rename(from, to)
}

func TestClosingTheWindowStopsARestoreAtTheNextItem(t *testing.T) {
	h := newTrashHarness(t, []string{"a.txt", "b.txt"}, "a.txt", "b.txt")
	g := gatedTrash{xdgTrash: h.a.trash.(xdgTrash), gate: make(chan struct{}), entered: make(chan struct{}, 2)}
	var open sync.Once
	t.Cleanup(func() { open.Do(func() { close(g.gate) }) })
	h.a.trash = g
	pg := h.openTrashPage(Command{Name: CmdTrashCan})
	h.pickTrash(pg, "a.txt", "b.txt")
	h.clickButton(pg, pg.restore.OnClick)
	h.until("the first restore is under way", func() bool { return len(g.entered) > 0 })
	// The first item is let go only once the window is closing.
	go func() {
		time.Sleep(100 * time.Millisecond)
		open.Do(func() { close(g.gate) })
	}()
	h.a.stopAll()
	if back := btoi(h.exists("a.txt")) + btoi(h.exists("b.txt")); back != 1 {
		t.Fatalf("%d items came back after the window closed, want the one under way", back)
	}
}

// btoi is 1 for true and 0 for false.
func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestTheSidebarEmptiesTheTrashWithNoPageOpen(t *testing.T) {
	h := newTrashHarness(t, []string{"a.txt", "sub"}, "a.txt", "sub/c.txt")
	got := notices(h)
	// As the sidebar's Empty trash… sends it.
	h.do(Command{Name: CmdEmptyTrash})
	h.until("the window asks", func() bool { return len(h.a.ops.dialogs) > 0 })
	c := h.a.ops.dialogs[0].state.(Confirm)
	if c.Title != "Empty the trash?" || !strings.Contains(c.Body, "Everything in the trash") {
		t.Fatalf("the window asks %+v", c)
	}
	h.answer(Confirmed{Token: c.Token, OK: true})
	h.until("the trash is empty, and a notice says so", func() bool {
		return !h.a.bin.busy && len(names(t, filepath.Join(h.root, "Trash", "files"))) == 0 &&
			slices.ContainsFunc(got(), func(n Notice) bool { return n.Title == "Emptied the trash" })
	})
	if h.a.bin.open {
		t.Fatal("emptying from the sidebar opened the trash page")
	}
}

func TestTheTitleBarLetsThePageTakeItsPresses(t *testing.T) {
	h := newTrashHarness(t, nil, "a.txt")
	captions := func() (title, menus []geom.Rect) {
		h.ui(func(b *browser, _ *gunim.UI) {
			title = b.title.head.name.CaptionRects(geom.Sz(400, 30))
			menus = b.title.bar.CaptionRects(geom.Sz(400, 30))
		})
		return title, menus
	}
	if title, menus := captions(); len(title) == 0 || len(menus) == 0 {
		t.Fatalf("with no page, the title bar moves the window by %v and %v", title, menus)
	}
	h.openTrashPage(Command{Name: CmdTrashCan})
	if title, menus := captions(); len(title) != 0 || len(menus) != 0 {
		t.Fatalf("under the trash page, the title bar still moves the window by %v and %v", title, menus)
	}
	h.press(input.KeyEscape, 0)
	h.until("the page has closed", func() bool { return !h.a.bin.open })
	if title, _ := captions(); len(title) == 0 {
		t.Fatal("once the page closed, the title moves the window no more")
	}
}

func TestADropOnTheTrashPlaceIsRefused(t *testing.T) {
	h := newHarnessWith(t, func(o *Options) {
		home := filepath.Dir(o.Dir)
		o.Places = func() ([]Place, error) {
			return []Place{{Name: "Home", Path: home, Kind: "home"}, {Name: "Trash", Path: TrashPath, Kind: "trash"}}, nil
		}
	}, "here.txt")
	outside := filepath.Join(h.root, "elsewhere")
	tree(t, outside, "x.txt")
	h.until("the trash shows in the sidebar", func() bool { _, ok := h.b.side.items[widget.Key(TrashPath)]; return ok })
	h.frames(10)
	h.w.Input(input.Drop{Pos: h.placeAt(widget.Key(TrashPath)), Paths: []string{filepath.Join(outside, "x.txt")}})
	h.frames(30)
	if len(h.a.ops.running) != 0 || len(h.a.ops.dialogs) != 0 {
		t.Fatalf("a drop on the trash started %d operations and %d dialogs", len(h.a.ops.running), len(h.a.ops.dialogs))
	}
	if _, err := os.Stat(TrashPath); err == nil {
		t.Fatal("a drop on the trash made a folder named trash:")
	}
	if contents(t, filepath.Join(outside, "x.txt")) != "x.txt" {
		t.Fatal("the dropped file changed")
	}
}

// unopenable is a trash the window cannot read.
type unopenable struct{ xdgTrash }

func (unopenable) TrashItems(context.Context) ([]TrashItem, error) {
	return nil, fmt.Errorf("this one: %w", errors.ErrUnsupported)
}

func TestATrashThatWillNotOpenCannotBeEmptied(t *testing.T) {
	h := newTrashHarness(t, nil, "a.txt")
	h.a.trash = unopenable{h.a.trash.(xdgTrash)}
	pg := h.openTrashPage(Command{Name: CmdTrashCan})
	if pg.state.Err == "" || !pg.empty.Disabled {
		t.Fatalf("the page says %q, with Empty disabled %v", pg.state.Err, pg.empty.Disabled)
	}
}

func TestAClickOnRestoreLeavesTheKeysOnTheItems(t *testing.T) {
	h := newTrashHarness(t, []string{"a.txt", "b.txt", "c.txt"}, "a.txt", "b.txt", "c.txt")
	pg := h.openTrashPage(Command{Name: CmdTrashCan})
	h.frames(30)
	var row, restore geom.Rect
	h.ui(func(_ *browser, u *gunim.UI) {
		i := slices.IndexFunc(pg.rows, func(r TrashRow) bool { return r.Name == "a.txt" })
		row, _ = pg.grid.RowRect(i)
		g, _ := u.Bounds(pg.grid)
		row = row.Add(g.Min)
		restore, _ = u.Bounds(pg.restore)
	})
	h.click(row.Center())
	h.click(restore.Center())
	h.until("a.txt is back", func() bool { return h.exists("a.txt") && len(pg.rows) == 2 && !pg.state.Busy })
	h.press(input.KeyA, input.ModControl)
	if got := len(pg.selectedKeys()); got != 2 {
		t.Fatalf("Ctrl+A after a click on Restore selected %d items, want 2", got)
	}
}

func TestGoTrashIsDimmedWhereThereIsNoTrash(t *testing.T) {
	h := newHarnessWith(t, onBareFS, "a.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	var dimmed, found bool
	for m, cmds := range h.b.title.cmds {
		if i := slices.Index(cmds, CmdTrashCan); i >= 0 {
			found, dimmed = true, h.b.title.bar.Menus[m].Items[i].Disabled
		}
	}
	if !found || !dimmed {
		t.Fatalf("Go › Trash is there %v, dimmed %v, on a file system with no trash", found, dimmed)
	}
}

func TestANarrowTrashPageLeavesItsTitleOut(t *testing.T) {
	h := newTrashHarness(t, nil, "a.txt")
	pg := h.openTrashPage(Command{Name: CmdTrashCan})
	h.frames(20)
	if pg.title.Text == "" {
		t.Fatal("a wide page shows no title")
	}
	h.w.Offscreen().Resize(geom.Sz(480, 500))
	h.frames(20)
	var title, restore geom.Rect
	h.ui(func(_ *browser, u *gunim.UI) { title, _ = u.Bounds(pg.title); restore, _ = u.Bounds(pg.restore) })
	if pg.title.Text != "" && title.Max.X > restore.Min.X {
		t.Fatalf("the title %v runs under Restore %v", title, restore)
	}
}

// slowList is a trash whose listing waits for the gate to open.
type slowList struct {
	xdgTrash
	gate chan struct{}
}

func (s slowList) TrashItems(ctx context.Context) ([]TrashItem, error) {
	<-s.gate
	return s.xdgTrash.TrashItems(ctx)
}

func TestEmptyWaitsForTheTrashToList(t *testing.T) {
	h := newTrashHarness(t, []string{"a.txt"}, "a.txt")
	slow := slowList{xdgTrash: h.a.trash.(xdgTrash), gate: make(chan struct{})}
	var open sync.Once
	t.Cleanup(func() { open.Do(func() { close(slow.gate) }) })
	h.a.trash = slow
	h.do(Command{Name: CmdTrashCan})
	h.until("the page shows, reading", func() bool { pg := h.trashPage(); return pg != nil && pg.state.Reading })
	pg := h.trashPage()
	if !pg.empty.Disabled {
		t.Fatal("Empty can be clicked before the trash has listed anything")
	}
	open.Do(func() { close(slow.gate) })
	h.until("the trash lists a.txt", func() bool { return !pg.state.Reading && len(pg.rows) == 1 })
	if pg.empty.Disabled {
		t.Fatal("Empty stays dimmed once the trash lists an item")
	}
}

func TestTheTrashStillOpensOnAFileSystemGoneBackTo(t *testing.T) {
	h := newTrashHarness(t, nil, "a.txt")
	// Back to a file system hands the window it as the window had it, with
	// its archives opened.
	h.a.showFS(h.a.fs, h.dir)
	h.frames(5)
	if _, wrapped := h.a.trash.(*archives); !wrapped {
		t.Fatalf("the window's trash is a %T, not one that opens archives", h.a.trash)
	}
	if _, ok := h.a.trashCan(); !ok || h.a.shell.NoTrashCan != !canOpen(LocalFS().(Trasher)) {
		t.Fatalf("gone back to, the trash opens %v, and Go › Trash is dimmed %v", ok, h.a.shell.NoTrashCan)
	}
}
