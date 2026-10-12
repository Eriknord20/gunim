package filemanager

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/widget"
)

// harness runs the app half against the real views in an offscreen
// window, on a folder in a temporary folder, with the trash there too.
type harness struct {
	t    *testing.T
	w    *gunim.Window
	a    *app
	b    *browser
	root string
	dir  string
}

// grab is a patch that hands the test the browser.
type grab struct{}

func newHarness(t *testing.T, spec ...string) *harness {
	t.Helper()
	return newHarnessWith(t, nil, spec...)
}

// newHarnessWith runs the app half with the options set changes. Where
// it sets a file system, the trash is the file system's own.
func newHarnessWith(t *testing.T, set func(o *Options), spec ...string) *harness {
	t.Helper()
	h := &harness{t: t, root: t.TempDir()}
	h.dir = filepath.Join(h.root, "dir")
	if err := os.MkdirAll(h.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	tree(t, h.dir, spec...)
	h.w = gunim.NewOffscreen(geom.Sz(1100, 700), &root{})
	RegisterViews(h.w)
	gunim.RegisterPatch(h.w, "browser", func(b *browser, _ grab, _ *gunim.UI) { h.b = b })
	ctx, cancel := context.WithCancel(context.Background())
	o := Options{Dir: h.dir, PrefsPath: filepath.Join(h.root, "prefs.json"), Poll: -1,
		defaults: func() []Favourite { return nil }}
	if set != nil {
		set(&o)
	}
	if o.FS == nil {
		o.trash = xdgTrash{dir: filepath.Join(h.root, "Trash")}
	}
	a, err := launch(ctx, h.w.Client(), o, nil)
	if err != nil {
		t.Fatal(err)
	}
	h.a = a
	t.Cleanup(func() {
		cancel()
		a.stopAll()
	})
	if err := h.w.Client().Patch(string(browserID), grab{}); err != nil {
		t.Fatal(err)
	}
	h.until("the folder shows", func() bool { return !a.nav.loading && h.b != nil && h.b.listing.cur != nil })
	h.frames(30)
	return h
}

// pump runs what is waiting on either side, and draws a frame.
func (h *harness) pump() {
	for {
		select {
		case fn := <-h.a.done:
			fn()
			continue
		case ev := <-h.w.Client().Intents():
			h.a.take(h.a.handlers, ev)
			continue
		default:
		}
		break
	}
	h.w.Frame(time.Second / 60)
}

func (h *harness) frames(n int) {
	for range n {
		h.pump()
	}
}

// until pumps until cond holds, and fails the test after five seconds.
func (h *harness) until(what string, cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			h.t.Fatalf("waited five seconds for this: %s", what)
		}
		h.pump()
		time.Sleep(time.Millisecond)
	}
}

// do runs an intent through the app half as the window would send it.
func (h *harness) do(in gunim.Intent) {
	h.a.handle(h.a.handlers, in)
	h.frames(2)
}

// answer closes the dialog showing, as its buttons do, and hands the
// app its answer.
func (h *harness) answer(in gunim.Intent) {
	h.t.Helper()
	if len(h.a.ops.dialogs) == 0 {
		h.t.Fatal("no dialog is showing")
	}
	if err := h.w.Client().Unmount(h.a.ops.dialogs[0].id); err != nil {
		h.t.Fatal(err)
	}
	h.do(in)
}

// pick selects the rows named, as clicks in the grid would.
func (h *harness) pick(names ...string) {
	h.t.Helper()
	var runs [][2]int
	for i, e := range h.a.nav.rows {
		if slices.Contains(names, e.Name) {
			runs = append(runs, [2]int{i, i + 1})
		}
	}
	if len(runs) != len(names) {
		h.t.Fatalf("found %d of the rows %v", len(runs), names)
	}
	h.do(Selected{Gen: h.a.nav.gen, Runs: runs, Cursor: runs[0][0]})
}

// shown returns the names the grid draws, in order.
func (h *harness) shown() []string {
	h.t.Helper()
	pg := h.b.listing.cur
	var out []string
	for i := range pg.grid.Rows() {
		r, ok := pg.view(i)
		if !ok {
			return nil
		}
		out = append(out, r.Name)
	}
	return out
}

// exists reports whether rel names something under the folder.
func (h *harness) exists(rel string) bool {
	_, err := os.Lstat(filepath.Join(h.dir, filepath.FromSlash(rel)))
	return err == nil
}

func TestTheGridShowsTheFolderFoldersFirst(t *testing.T) {
	h := newHarness(t, "b.txt", "a.txt", "zeta/", ".hidden")
	want := []string{"zeta", "a.txt", "b.txt"}
	h.until("the rows arrive", func() bool { return slices.Equal(h.shown(), want) })
	if got := h.b.status.left.Text; got != "3 items (1 hidden)" {
		t.Fatalf("the status bar says %q", got)
	}
}

func TestCountsKeepTheirThousandsApart(t *testing.T) {
	for n, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 100000: "100,000", 1234567: "1,234,567"} {
		if got := count(n); got != want {
			t.Fatalf("count(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestEditingThePathGoesThere(t *testing.T) {
	h := newHarness(t, "sub/x.txt")
	h.w.Input(input.KeyPress{Key: input.KeyL, Mods: input.ModControl})
	h.frames(2)
	if !h.b.path.addr.Editing() || h.b.path.addr.Text() != h.dir {
		t.Fatalf("Ctrl+L left the path bar editing %v with %q", h.b.path.addr.Editing(), h.b.path.addr.Text())
	}
	h.w.Input(input.KeyPress{Key: input.KeyEnd})
	h.w.Input(input.TextInput{Text: string(filepath.Separator) + "sub"})
	h.press(input.KeyEnter, 0)
	h.until("the typed folder opens", func() bool { return slices.Equal(h.shown(), []string{"x.txt"}) })
	if h.b.path.addr.Editing() {
		t.Fatal("the path bar is still editing after Enter")
	}
	h.w.Input(input.KeyPress{Key: input.KeyL, Mods: input.ModControl})
	h.w.Input(input.TextInput{Text: "nonsense"})
	h.w.Input(input.KeyPress{Key: input.KeyEscape})
	h.frames(2)
	if h.b.path.addr.Editing() || !SystemPaths.Same(h.a.nav.path, filepath.Join(h.dir, "sub")) {
		t.Fatal("Escape did not leave the path as it was")
	}
}

func TestTheFilterNarrowsTheRows(t *testing.T) {
	h := newHarness(t, "report.pdf", "notes.txt", "old-report.txt")
	h.do(FilterChanged{Text: "REPORT"})
	h.until("the filter applies", func() bool {
		return slices.Equal(h.shown(), []string{"old-report.txt", "report.pdf"})
	})
	h.do(FilterChanged{Text: ""})
	h.until("the filter clears", func() bool { return len(h.shown()) == 3 })
}

func TestSortingBySizeFromTheHeader(t *testing.T) {
	h := newHarness(t, "a.txt", "bbbbbbbbbbbb.txt", "cccccc.txt")
	h.do(SortClicked{Column: int(SortSize)})
	h.until("the rows sort by size", func() bool {
		return slices.Equal(h.shown(), []string{"a.txt", "cccccc.txt", "bbbbbbbbbbbb.txt"})
	})
	h.do(SortClicked{Column: int(SortSize)})
	h.until("a second click turns the order round", func() bool {
		return slices.Equal(h.shown(), []string{"bbbbbbbbbbbb.txt", "cccccc.txt", "a.txt"})
	})
}

func TestGoingIntoAFolderAndBack(t *testing.T) {
	h := newHarness(t, "inner/deep.txt", "top.txt")
	first := h.b.listing.cur
	h.do(Activated{Gen: h.a.nav.gen, Row: 0})
	h.until("the folder opens", func() bool { return slices.Equal(h.shown(), []string{"deep.txt"}) })
	if h.b.listing.cur == first || h.b.listing.cur == nil {
		t.Fatal("going into a folder kept the old page")
	}
	if h.b.listing.deck.top.travel != 1 {
		t.Fatalf("the new page came in with travel %d, want 1", h.b.listing.deck.top.travel)
	}
	h.do(Command{Name: CmdBack})
	h.until("back shows the folder again", func() bool { return slices.Equal(h.shown(), []string{"inner", "top.txt"}) })
	if h.b.listing.deck.top.travel != -1 {
		t.Fatalf("going back, the page came in with travel %d, want -1", h.b.listing.deck.top.travel)
	}
	h.do(Command{Name: CmdForward})
	h.until("forward goes in again", func() bool { return slices.Equal(h.shown(), []string{"deep.txt"}) })
	h.do(Command{Name: CmdUp})
	h.until("up comes out, with the folder left selected", func() bool {
		return len(h.shown()) == 2 && h.a.nav.sel["inner"]
	})
}

func TestBackspaceComesBackToTheFolderLeftSelected(t *testing.T) {
	h := newHarness(t, "one/two/deep.txt", "one/zz.txt", "three/t.txt", "z.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 3 })
	// Down from nothing selected goes to the first row: a folder each time.
	h.press(input.KeyDown, 0)
	h.press(input.KeyEnter, 0)
	h.until("Enter opens the folder", func() bool { return slices.Equal(h.shown(), []string{"two", "zz.txt"}) })
	h.press(input.KeyDown, 0)
	h.press(input.KeyEnter, 0)
	h.until("Enter opens the folder in it", func() bool { return slices.Equal(h.shown(), []string{"deep.txt"}) })
	h.press(input.KeyBackspace, 0)
	h.until("Backspace comes back with the folder left selected", func() bool {
		return slices.Contains(h.shown(), "two") && slices.Equal(h.selected(), []string{"two"}) && h.a.nav.cursor == "two"
	})
	h.press(input.KeyBackspace, 0)
	h.until("Backspace again comes back to the first folder left selected", func() bool {
		return slices.Contains(h.shown(), "one") && slices.Equal(h.selected(), []string{"one"}) && h.a.nav.cursor == "one"
	})
	h.press(input.KeyDown, 0)
	h.press(input.KeyEnter, 0)
	h.until("Down and Enter open the next folder", func() bool { return slices.Equal(h.shown(), []string{"t.txt"}) })
	h.do(Command{Name: CmdBack})
	h.until("back selects it in turn", func() bool { return h.a.nav.cursor == "three" })
	h.do(Command{Name: CmdForward})
	h.until("forward goes in again", func() bool { return slices.Equal(h.shown(), []string{"t.txt"}) })
}

func TestBackSelectsTheFolderThatLedWhereTheUserWas(t *testing.T) {
	h := newHarness(t, "one/two/deep.txt", "z.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 2 })
	h.do(Navigate{Path: filepath.Join(h.dir, "one", "two")})
	h.until("the folder opens", func() bool { return slices.Equal(h.shown(), []string{"deep.txt"}) })
	h.do(Command{Name: CmdBack})
	h.until("back selects the folder on the way", func() bool {
		return slices.Equal(h.selected(), []string{"one"}) && h.a.nav.cursor == "one"
	})
}

func TestDeleteKeyTrashesAndUndoRestores(t *testing.T) {
	h := newHarness(t, "keep.txt", "gone.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 2 })
	h.pick("gone.txt")
	h.w.Input(input.KeyPress{Key: input.KeyDelete})
	h.until("the file goes to the trash", func() bool { return !h.exists("gone.txt") })
	h.until("the listing drops it", func() bool { return slices.Equal(h.shown(), []string{"keep.txt"}) })
	if contents(t, filepath.Join(h.root, "Trash", "files", "gone.txt")) != "gone.txt" {
		t.Fatal("the file is not in the trash")
	}
	if n := h.b.toasts.Len(); n == 0 {
		t.Fatal("no toast says the file went to the trash")
	}
	h.do(Command{Name: CmdUndo})
	h.until("undo puts it back", func() bool { return h.exists("gone.txt") })
	h.until("the listing shows it again", func() bool { return len(h.shown()) == 2 })
}

func TestPasteAsksAboutAClash(t *testing.T) {
	h := newHarness(t, "a.txt", "sub/a.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 2 })
	h.pick("a.txt")
	h.do(Command{Name: CmdCopy})
	h.do(Navigate{Path: filepath.Join(h.dir, "sub")})
	h.until("the folder opens", func() bool { return slices.Equal(h.shown(), []string{"a.txt"}) })
	h.do(Command{Name: CmdPaste})
	h.until("the clash is asked about", func() bool { return len(h.a.ops.dialogs) == 1 })
	ask, ok := h.a.ops.dialogs[0].state.(ClashAsk)
	if !ok || ask.Name != "a.txt" || !ask.SameKind {
		t.Fatalf("the dialog asks %+v", h.a.ops.dialogs[0].state)
	}
	h.answer(ClashAnswered{Op: ask.Op, Choice: ChoiceKeepBoth})
	h.until("both are kept", func() bool { return slices.Equal(h.shown(), []string{"a (2).txt", "a.txt"}) })
	if contents(t, filepath.Join(h.dir, "sub", "a.txt")) != "sub/a.txt" {
		t.Fatal("keeping both overwrote the file there")
	}
}

// selected returns the names selected, in the order the rows go.
func (h *harness) selected() []string {
	var out []string
	for _, e := range h.a.nav.rows {
		if h.a.nav.sel[e.Name] {
			out = append(out, e.Name)
		}
	}
	return out
}

func TestPasteSelectsWhatCame(t *testing.T) {
	h := newHarness(t, "a.txt", "b.txt", "c.txt", "sub/a.txt", "sub/x.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 4 })
	h.pick("a.txt", "b.txt")
	h.do(Command{Name: CmdCopy})
	h.do(Navigate{Path: filepath.Join(h.dir, "sub")})
	h.until("the folder opens", func() bool { return slices.Equal(h.shown(), []string{"a.txt", "x.txt"}) })
	h.pick("x.txt")
	h.do(Command{Name: CmdPaste})
	h.until("the clash is asked about", func() bool { return len(h.a.ops.dialogs) == 1 })
	ask, ok := h.a.ops.dialogs[0].state.(ClashAsk)
	if !ok {
		t.Fatalf("the dialog asks %+v", h.a.ops.dialogs[0].state)
	}
	h.answer(ClashAnswered{Op: ask.Op, Choice: ChoiceKeepBoth})
	h.until("the copies are selected, and nothing else", func() bool {
		return slices.Equal(h.selected(), []string{"a (2).txt", "b.txt"})
	})
	if h.a.nav.cursor != "a (2).txt" {
		t.Fatalf("the keyboard is on %q, not the first copy", h.a.nav.cursor)
	}

	h.do(Command{Name: CmdUp})
	h.until("the folder above opens", func() bool { return len(h.shown()) == 4 })
	h.pick("c.txt")
	h.do(Command{Name: CmdCut})
	h.do(Navigate{Path: filepath.Join(h.dir, "sub")})
	h.until("the folder opens again", func() bool { return len(h.shown()) == 4 })
	h.do(Command{Name: CmdPaste})
	h.until("what was moved is selected", func() bool { return slices.Equal(h.selected(), []string{"c.txt"}) })
}

func TestRenameThroughThePrompt(t *testing.T) {
	h := newHarness(t, "old.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	h.pick("old.txt")
	h.w.Input(input.KeyPress{Key: input.KeyF2})
	h.until("the prompt shows", func() bool { return len(h.a.ops.dialogs) == 1 })
	p, ok := h.a.ops.dialogs[0].state.(Prompt)
	if !ok || p.Text != "old.txt" || p.Stem != 3 {
		t.Fatalf("the prompt is %+v", h.a.ops.dialogs[0].state)
	}
	h.answer(Prompted{Token: p.Token, Text: "new.txt", OK: true})
	h.until("the file is renamed and selected", func() bool {
		return slices.Equal(h.shown(), []string{"new.txt"}) && h.a.nav.sel["new.txt"]
	})
}

func TestPermanentDeleteAsksFirst(t *testing.T) {
	h := newHarness(t, "precious.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	h.pick("precious.txt")
	h.w.Input(input.KeyPress{Key: input.KeyDelete, Mods: input.ModShift})
	h.until("the question shows", func() bool { return len(h.a.ops.dialogs) == 1 })
	c, ok := h.a.ops.dialogs[0].state.(Confirm)
	if !ok {
		t.Fatalf("the dialog is %+v", h.a.ops.dialogs[0].state)
	}
	h.answer(Confirmed{Token: c.Token})
	h.frames(10)
	if !h.exists("precious.txt") {
		t.Fatal("a cancelled delete deleted the file")
	}
	h.w.Input(input.KeyPress{Key: input.KeyDelete, Mods: input.ModShift})
	h.until("the question shows again", func() bool { return len(h.a.ops.dialogs) == 1 })
	c, _ = h.a.ops.dialogs[0].state.(Confirm)
	h.answer(Confirmed{Token: c.Token, OK: true})
	h.until("the file is gone", func() bool { return !h.exists("precious.txt") })
	if _, err := os.Stat(filepath.Join(h.root, "Trash")); err == nil {
		t.Fatal("a permanent delete went through the trash")
	}
}

func TestANewFolderIsMadeAndSelected(t *testing.T) {
	h := newHarness(t, "New folder/")
	h.w.Input(input.KeyPress{Key: input.KeyN, Mods: input.ModControl | input.ModShift})
	h.until("the prompt shows", func() bool { return len(h.a.ops.dialogs) == 1 })
	p, _ := h.a.ops.dialogs[0].state.(Prompt)
	if p.Text != "New folder (2)" {
		t.Fatalf("the prompt offers %q, want a free name", p.Text)
	}
	h.answer(Prompted{Token: p.Token, Text: p.Text, OK: true})
	h.until("the folder is made and selected", func() bool { return h.exists("New folder (2)") && h.a.nav.sel["New folder (2)"] })
}

func TestThePollNoticesChanges(t *testing.T) {
	h := newHarness(t, "a.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	tree(t, h.dir, "b.txt")
	h.a.poll()
	h.until("the new file shows", func() bool { return slices.Equal(h.shown(), []string{"a.txt", "b.txt"}) })
	if err := os.RemoveAll(h.dir); err != nil {
		t.Fatal(err)
	}
	h.a.poll()
	h.until("the listing says the folder has gone", func() bool {
		return h.b.listing.cur.msg.Text != "" && h.a.nav.err != nil
	})
}

func TestAFolderThatCannotBeReadSaysWhy(t *testing.T) {
	h := newHarness(t)
	h.do(Navigate{Path: filepath.Join(h.dir, "missing")})
	h.until("the listing says why", func() bool { return h.b.listing.cur.msg.Text != "" })
	if h.a.nav.err == nil {
		t.Fatal("the app has no error for a missing folder")
	}
}

func TestPinningAddsAFavouriteAndSavesIt(t *testing.T) {
	h := newHarness(t, "work/")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	h.pick("work")
	h.w.Input(input.KeyPress{Key: input.KeyD, Mods: input.ModControl})
	h.until("the favourite shows", func() bool { return h.b.side.favs.Len() == 1 })
	p, err := loadPrefs(filepath.Join(h.root, "prefs.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.Favourites, []string{filepath.Join(h.dir, "work")}) {
		t.Fatalf("the saved favourites are %v", p.Favourites)
	}
}

// A folder with an entry that cannot be read still lists, with that
// entry shown by its name.
func TestAFolderListsPastAnEntryItCannotRead(t *testing.T) {
	h := newHarness(t, "a.txt")
	if err := os.Symlink("loop", filepath.Join(h.dir, "loop")); err != nil {
		t.Skipf("this system will not make a link here: %v", err)
	}
	h.a.poll()
	h.until("the folder lists with the link", func() bool { return slices.Equal(h.shown(), []string{"a.txt", "loop"}) })
	if h.a.nav.err != nil {
		t.Fatalf("the listing says %v", h.a.nav.err)
	}
}

func TestAClickOnARowSelectsItAndPreviewsIt(t *testing.T) {
	h := newHarness(t, "notes.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	r, ok := func() (geom.Rect, bool) {
		var r geom.Rect
		var ok bool
		type bounds struct{}
		gunim.RegisterPatch(h.w, "browser", func(b *browser, _ bounds, u *gunim.UI) {
			r, ok = u.Bounds(b.listing.cur.grid)
		})
		if err := h.w.Client().Patch(string(browserID), bounds{}); err != nil {
			t.Fatal(err)
		}
		h.frames(1)
		return r, ok
	}()
	if !ok {
		t.Fatal("the grid was not drawn")
	}
	at := r.Min.Add(geom.Pt(60, 26+11))
	h.w.Input(input.PointerDown{Pos: at, Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	h.w.Input(input.PointerUp{Pos: at, Button: input.ButtonPrimary, Time: time.Now()})
	h.until("the row is selected", func() bool { return h.a.nav.sel["notes.txt"] })
	h.until("the preview shows it", func() bool {
		return h.b.preview.cur != nil && h.a.preview.subject != "" && h.b.preview.seq == h.a.preview.seq
	})
}

// bounds returns where n was last drawn, in the window.
func (h *harness) bounds(n func(b *browser) gunim.Node) geom.Rect {
	h.t.Helper()
	type ask struct{}
	var r geom.Rect
	var ok bool
	gunim.RegisterPatch(h.w, "browser", func(b *browser, _ ask, u *gunim.UI) { r, ok = u.Bounds(n(b)) })
	if err := h.w.Client().Patch(string(browserID), ask{}); err != nil {
		h.t.Fatal(err)
	}
	h.frames(1)
	if !ok {
		h.t.Fatal("the node was not drawn")
	}
	return r
}

// click presses and lets go at p.
func (h *harness) click(p geom.Point) {
	h.w.Input(input.PointerDown{Pos: p, Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()})
	h.w.Input(input.PointerUp{Pos: p, Button: input.ButtonPrimary, Time: time.Now()})
	h.frames(2)
}

func TestAClickOnAFavouriteGoesThere(t *testing.T) {
	h := newHarness(t, "work/report.txt")
	h.do(Navigate{Path: filepath.Join(h.dir, "work")})
	h.do(Command{Name: CmdPin})
	h.do(Command{Name: CmdUp})
	h.until("the favourite shows and the folder is back", func() bool {
		return h.b.side.favs.Len() == 1 && len(h.shown()) == 1 && h.shown()[0] == "work"
	})
	h.frames(30)
	row := h.bounds(func(b *browser) gunim.Node {
		n, _ := b.side.favs.Row(widget.Key(filepath.Join(h.dir, "work")))
		return n
	})
	h.click(row.Center())
	h.until("the favourite opens", func() bool { return slices.Equal(h.shown(), []string{"report.txt"}) })
}

func TestAClickOnAFolderOfThePathGoesThere(t *testing.T) {
	h := newHarness(t, "a/b/c.txt")
	h.do(Navigate{Path: filepath.Join(h.dir, "a", "b")})
	h.until("the deep folder shows", func() bool { return slices.Equal(h.shown(), []string{"c.txt"}) })
	h.frames(30)
	places := h.places()
	parent := places[len(places)-2]
	if parent.Name != "a" {
		t.Fatalf("the place before the last is %q, want a", parent.Name)
	}
	h.click(geom.Pt(parent.Rect.Min.X+8, parent.Rect.Center().Y))
	h.until("the folder a opens", func() bool { return slices.Equal(h.shown(), []string{"b"}) })
	if h.b.path.addr.Editing() {
		t.Fatal("a click on a folder of the path started editing it")
	}
	// A click beside the folders, or on the folder showing, edits the path.
	bar := h.bounds(func(b *browser) gunim.Node { return b.path.addr })
	h.click(geom.Pt(bar.Max.X-10, bar.Center().Y))
	if !h.b.path.addr.Editing() {
		t.Fatal("a click beside the folders did not edit the path")
	}
}

// A name with letters outside ASCII has its stem counted in runes, as
// the text field selects, so the extension stays out of the selection.
func TestRenameSelectsTheStemOfANameBeyondASCII(t *testing.T) {
	h := newHarness(t, "Ålö.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	h.pick("Ålö.txt")
	h.w.Input(input.KeyPress{Key: input.KeyF2})
	h.until("the prompt shows", func() bool { return len(h.a.ops.dialogs) == 1 })
	p, ok := h.a.ops.dialogs[0].state.(Prompt)
	if !ok || p.Stem != 3 {
		t.Fatalf("the prompt is %+v, want a stem of 3 runes", h.a.ops.dialogs[0].state)
	}
}

// An answer that arrives after its operation stopped is dropped: an
// answer reaches only the dialog it was for.
func TestALateAnswerLeavesTheNextOperationsDialog(t *testing.T) {
	h := newHarness(t, "a.txt", "one/a.txt", "two/a.txt")
	src := []string{filepath.Join(h.dir, "a.txt")}
	h.a.startOp(job{kind: OpCopy, srcs: src, dest: filepath.Join(h.dir, "one")}, "first")
	first := h.a.ops.next
	h.until("the first clash is asked about", func() bool { return len(h.a.ops.dialogs) == 1 })
	h.a.startOp(job{kind: OpCopy, srcs: src, dest: filepath.Join(h.dir, "two")}, "second")
	h.until("the second clash waits", func() bool { return len(h.a.ops.dialogs) == 2 })
	h.do(CancelOp{ID: first})
	h.until("the first operation ends", func() bool { return len(h.a.ops.dialogs) == 1 })
	h.do(ClashAnswered{Op: first, Choice: ChoiceReplace, All: true})
	h.frames(10)
	if len(h.a.ops.dialogs) != 1 {
		t.Fatal("the late answer took the second operation's dialog")
	}
	if contents(t, filepath.Join(h.dir, "two", "a.txt")) != "two/a.txt" {
		t.Fatal("the late answer replaced the second operation's file")
	}
	h.do(Confirmed{Token: 99, OK: true})
	h.do(DialogClosed{})
	if len(h.a.ops.dialogs) != 1 {
		t.Fatal("an answer to another kind of dialog took the clash dialog")
	}
	ask, _ := h.a.ops.dialogs[0].state.(ClashAsk)
	h.answer(ClashAnswered{Op: ask.Op, Choice: ChoiceSkip})
	h.until("the second operation ends", func() bool { return len(h.a.ops.running) == 0 })
}

// An undo that stops partway leaves what it did not reach to undo later.
func TestAnUndoThatStopsKeepsTheRestUndoable(t *testing.T) {
	h := newHarness(t, "a.txt", "b.txt", "sub/")
	h.a.startOp(job{kind: OpMove, srcs: []string{filepath.Join(h.dir, "a.txt"), filepath.Join(h.dir, "b.txt")},
		dest: filepath.Join(h.dir, "sub")}, "Moving")
	h.until("the move finishes", func() bool { return len(h.a.ops.running) == 0 && len(h.a.ops.undo) == 1 })
	// A new a.txt stands where the first file would go back.
	tree(t, h.dir, "a.txt")
	h.do(Command{Name: CmdUndo})
	h.until("the undo stops", func() bool { return len(h.a.ops.running) == 0 && h.exists("b.txt") })
	if len(h.a.ops.undo) != 1 {
		t.Fatalf("after the undo stopped, %d operations can be undone, want 1", len(h.a.ops.undo))
	}
	h.do(Command{Name: CmdUndo})
	h.until("a second undo stops at the same file", func() bool { return len(h.a.ops.running) == 0 })
	if err := os.Remove(filepath.Join(h.dir, "a.txt")); err != nil {
		t.Fatal(err)
	}
	h.do(Command{Name: CmdUndo})
	h.until("the rest is undone", func() bool { return len(h.a.ops.running) == 0 && h.exists("a.txt") })
	if h.exists("sub/a.txt") || !h.exists("b.txt") || len(h.a.ops.undo) != 0 {
		t.Fatalf("after the last undo sub holds %v, and %d operations can be undone", names(t, filepath.Join(h.dir, "sub")),
			len(h.a.ops.undo))
	}
}
