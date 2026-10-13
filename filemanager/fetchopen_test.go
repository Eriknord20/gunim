package filemanager

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marrasen/gunim"
)

// launches records what the system was asked to open and to reveal.
type launches struct {
	mu             sync.Mutex
	opened, showed []string
}

func (l *launches) open(p string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.opened = append(l.opened, p)
	return nil
}

func (l *launches) reveal(p string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.showed = append(l.showed, p)
	return nil
}

func (l *launches) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.opened)
}

// newFetchHarness runs a window on the file system set gives, with the
// copies fetched to open kept in a folder of the test's, which it
// returns, and the system's programs standing in for by l.
func newFetchHarness(t *testing.T, set func(o *Options), spec ...string) (h *harness, base string, l *launches) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	tree(t, dir, spec...)
	h = openHarnessWith(t, nil, root, dir, set)
	base = t.TempDir()
	h.a.hub.copies = &openCopies{base: base}
	l = &launches{}
	h.w.Offscreen().SetLauncher(l.open, l.reveal)
	return h, base, l
}

// activate opens the row named, as a double click does.
func (h *harness) activate(name string) {
	h.t.Helper()
	i := slices.IndexFunc(h.a.nav.rows, func(e entry) bool { return e.Name == name })
	if i < 0 {
		h.t.Fatalf("no row %s", name)
	}
	h.do(Activated{Gen: h.a.nav.gen, Row: i})
}

// filesIn returns the files under dir, by their paths.
func filesIn(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestARemoteFileIsFetchedToOpenAndFetchedAgainOnlyOnceChanged(t *testing.T) {
	h, base, l := newFetchHarness(t, onBareFS, "report.pdf")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	before := h.a.ops.next
	h.activate("report.pdf")
	h.until("the copy opens", func() bool { return len(l.all()) == 1 })
	h.idle()
	if h.a.ops.next != before+1 {
		t.Fatalf("the fetch ran as %d operations", h.a.ops.next-before)
	}
	local := l.all()[0]
	if filepath.Base(local) != "report.pdf" {
		t.Fatalf("the copy is called %s", filepath.Base(local))
	}
	if !strings.HasPrefix(local, base+string(filepath.Separator)) {
		t.Fatalf("the copy is at %s, outside %s", local, base)
	}
	if got := contents(t, local); got != "report.pdf" {
		t.Fatalf("the copy holds %q", got)
	}

	// Unchanged, the file opens from its copy at once.
	h.activate("report.pdf")
	h.until("the copy opens again", func() bool { return len(l.all()) == 2 })
	if h.a.ops.next != before+1 || l.all()[1] != local {
		t.Fatalf("an unchanged file was fetched again: %d operations, opened %v", h.a.ops.next-before, l.all())
	}

	// Changed, it is fetched again.
	remote := filepath.Join(h.dir, "report.pdf")
	if err := os.WriteFile(remote, []byte("the second draft"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(remote, later, later); err != nil {
		t.Fatal(err)
	}
	h.activate("report.pdf")
	h.until("the new copy opens", func() bool { return len(l.all()) == 3 })
	h.idle()
	if h.a.ops.next != before+2 {
		t.Fatalf("a changed file was fetched in %d operations in all", h.a.ops.next-before)
	}
	if got := contents(t, l.all()[2]); got != "the second draft" {
		t.Fatalf("the new copy holds %q", got)
	}
	if got := filesIn(t, base); len(got) != 1 {
		t.Fatalf("the copies are %v; the old one should have gone", got)
	}
	h.a.hub.removeCopies()
	if got := filesIn(t, base); len(got) != 0 {
		t.Fatalf("the hub left %v as it ended", got)
	}
}

func TestOpenWithSystemFetchesTheRemoteFilesSelected(t *testing.T) {
	h, _, l := newFetchHarness(t, onBareFS, "a.txt", "b.txt", "sub/")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 3 })
	h.choose("a.txt", "b.txt")
	h.do(Command{Name: CmdOpenSystem})
	h.until("both open", func() bool { return len(l.all()) == 2 })
	h.idle()
	names := make([]string, 0, 2)
	for _, p := range l.all() {
		names = append(names, contents(t, p))
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"a.txt", "b.txt"}) {
		t.Fatalf("the copies opened hold %v", names)
	}
	// A folder is not fetched.
	h.choose("sub")
	h.do(Command{Name: CmdOpenSystem})
	h.until("the banner says why", func() bool { return strings.Contains(h.b.banner.label.Text, "Folders here cannot open") })
	if len(l.all()) != 2 {
		t.Fatalf("a folder opened: %v", l.all())
	}
}

func TestARemoteFolderStillOpensInTheWindow(t *testing.T) {
	h, _, l := newFetchHarness(t, onBareFS, "sub/x.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	h.activate("sub")
	h.until("the folder opens", func() bool { return slices.Equal(h.shown(), []string{"x.txt"}) })
	if len(l.all()) != 0 || h.a.ops.next != 0 {
		t.Fatalf("activating a folder opened %v in %d operations", l.all(), h.a.ops.next)
	}
}

func TestALargeRemoteFileAsksBeforeItIsFetched(t *testing.T) {
	h, base, l := newFetchHarness(t, onBareFS)
	f, err := os.Create(filepath.Join(h.dir, "big.iso"))
	if err != nil {
		t.Fatal(err)
	}
	// A sparse file takes no room on the disk.
	if err := f.Truncate(fetchAsk + 1<<20); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	h.do(Command{Name: CmdRefresh})
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	h.activate("big.iso")
	h.until("the question shows", func() bool { return len(h.a.ops.dialogs) == 1 })
	c, ok := h.a.ops.dialogs[0].state.(Confirm)
	if !ok || c.Title != "Fetch "+humanBytes(fetchAsk+1<<20)+" to open it?" || !strings.Contains(c.Body, "big.iso") {
		t.Fatalf("the dialog is %+v", h.a.ops.dialogs[0].state)
	}
	h.answer(Confirmed{Token: c.Token, OK: false})
	h.frames(10)
	if len(h.a.ops.running) != 0 || len(l.all()) != 0 || len(filesIn(t, base)) != 0 {
		t.Fatalf("a fetch said no to ran: %d operations, opened %v", len(h.a.ops.running), l.all())
	}
}

// gateFS is a bare file system whose files give their first megabyte at
// once, and the rest once release closes.
type gateFS struct {
	bareFS
	release chan struct{}
}

func (g gateFS) Open(p string) (io.ReadSeekCloser, error) {
	f, err := g.bareFS.Open(p)
	if err != nil {
		return nil, err
	}
	return &gated{ReadSeekCloser: f, release: g.release}, nil
}

// openOnCleanup returns a func that closes gate once, and closes it as
// the test ends, if it has not: a test that fails before it opens the
// gate leaves no read held there, which would hold the window's
// goroutines, and its cleanup, until the run times out. Call it after
// the harness is made, so its cleanup runs first.
func openOnCleanup(t *testing.T, gate chan struct{}) func() {
	open := sync.OnceFunc(func() { close(gate) })
	t.Cleanup(open)
	return open
}

type gated struct {
	io.ReadSeekCloser
	release chan struct{}
	reads   int
}

func (g *gated) Read(b []byte) (int, error) {
	g.reads++
	if g.reads > 1 {
		<-g.release
	}
	return g.ReadSeekCloser.Read(b)
}

func TestStoppingAFetchToOpenLeavesNothingBehind(t *testing.T) {
	release := make(chan struct{})
	h, base, l := newFetchHarness(t, func(o *Options) { o.FS = gateFS{bareFS{LocalFS()}, release} })
	open := openOnCleanup(t, release)
	if err := os.WriteFile(filepath.Join(h.dir, "film.mkv"), make([]byte, 3*copyBuffer), 0o644); err != nil {
		t.Fatal(err)
	}
	h.do(Command{Name: CmdRefresh})
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	h.activate("film.mkv")
	h.until("the fetch runs", func() bool { return len(h.a.ops.running) == 1 })
	var id int
	var title string
	for i, r := range h.a.ops.running {
		id, title = i, r.title
	}
	if title != "Fetching film.mkv to open" {
		t.Fatalf("the operation is called %q", title)
	}
	h.until("the part file is written", func() bool { return len(filesIn(t, base)) == 1 })
	h.do(CancelOp{ID: id})
	open()
	h.idle()
	if got := filesIn(t, base); len(got) != 0 {
		t.Fatalf("a stopped fetch left %v", got)
	}
	if len(l.all()) != 0 {
		t.Fatalf("a stopped fetch opened %v", l.all())
	}
}

// fileMenu returns the items of the title bar's File menu.
func (h *harness) fileMenu() []string {
	var items []string
	h.ui(func(b *browser, _ *gunim.UI) { items = labelsOf(b.title.bar.Menus[0].Items) })
	return items
}

// rowMenu opens the context menu of the row named, and returns its items
// and which are dimmed.
func (h *harness) rowMenu(name string) (items []string, off []bool) {
	h.choose(name)
	h.script("menu:" + name)
	h.ui(func(b *browser, _ *gunim.UI) {
		m := b.listing.cur.menu
		items, off = labelsOf(m.Items()), disabledOf(m.Items())
	})
	return items, off
}

// previewLinks returns the links under the path in the preview pane.
func (h *harness) previewLinks() []string {
	var out []string
	h.ui(func(b *browser, _ *gunim.UI) {
		if b.preview.cur == nil {
			return
		}
		for _, l := range b.preview.cur.links {
			out = append(out, l.Text)
		}
	})
	return out
}

func TestShowInTheSystemFileManagerIsOnlyOfferedWhereItWorks(t *testing.T) {
	const reveal = "Show in system file manager"
	for _, remote := range []bool{false, true} {
		var set func(o *Options)
		if remote {
			set = onBareFS
		}
		h, _, _ := newFetchHarness(t, set, "a.txt", "sub/")
		h.until("the rows arrive", func() bool { return len(h.shown()) == 2 })
		if got := slices.Contains(h.fileMenu(), reveal); got == remote {
			t.Fatalf("remote %v: the File menu %v", remote, h.fileMenu())
		}
		items, off := h.rowMenu("sub")
		if got := slices.Contains(items, reveal); got == remote {
			t.Fatalf("remote %v: the row's menu is %v", remote, items)
		}
		if i := slices.Index(items, "Open with system"); i < 0 || off[i] != remote {
			t.Fatalf("remote %v: Open with system on a folder is dimmed %v", remote, off[i])
		}
		items, off = h.rowMenu("a.txt")
		if i := slices.Index(items, "Open with system"); i < 0 || off[i] {
			t.Fatalf("remote %v: Open with system on a file is dimmed", remote)
		}
		h.until("the preview shows the file", func() bool { return slices.Contains(h.previewLinks(), "Copy path") })
		if got := slices.Contains(h.previewLinks(), reveal); got == remote {
			t.Fatalf("remote %v: the preview's links are %v", remote, h.previewLinks())
		}
	}
}

func TestWithoutRemovesAnItemAndTheLinesLeftOver(t *testing.T) {
	items := []menuItem{{"-", "", ""}, {"A", "", "a"}, {"-", "", ""}, {"B", "", "b"}, {"-", "", ""}, {"C", "", "c"}, {"-", "", ""}}
	got := make([]string, 0, 3)
	for _, it := range without(items, "b") {
		got = append(got, it.label)
	}
	if want := []string{"A", "-", "C"}; !slices.Equal(got, want) {
		t.Fatalf("without gives %v, want %v", got, want)
	}
}

func TestAHubThatStartsTakesAwayCopiesADayOld(t *testing.T) {
	base := t.TempDir()
	old, fresh := filepath.Join(base, "hub-old"), filepath.Join(base, "hub-fresh")
	for _, d := range []string{old, fresh} {
		if err := os.MkdirAll(filepath.Join(d, "x"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "x", "a.txt"), []byte("a"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	then := time.Now().Add(-copiesKept - time.Hour)
	if err := os.Chtimes(old, then, then); err != nil {
		t.Fatal(err)
	}
	c := &openCopies{base: base}
	dir, err := c.folder()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("the copies of a day ago stayed")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("the copies of another hub running went")
	}
	if filepath.Dir(dir) != base {
		t.Fatalf("the hub's folder is %s", dir)
	}
}

// The program's own actions show on the menu of the items selected,
// dimmed where they do not apply, and a pick hands it the items.
func TestTheProgramsActionsShowOnTheItemsMenu(t *testing.T) {
	got := make(chan []string, 1)
	h := newMenuHarness(t, func(o *Options) {
		o.ItemActions = []ItemAction{{Label: "View here", ID: "view", Files: true, One: true}}
		o.ItemAction = func(_ *Window, _ string, paths []string, id string) {
			if id == "view" {
				got <- paths
			}
		}
	}, "a.txt", "sub/")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 2 })
	items, off := h.rowMenu("sub")
	if i := slices.Index(items, "View here"); i < 0 || !off[i] {
		t.Fatalf("on a folder, the menu is %v, dimmed %v", items, off)
	}
	items, off = h.rowMenu("a.txt")
	i := slices.Index(items, "View here")
	if i < 0 || off[i] {
		t.Fatalf("on a file, the menu is %v, dimmed %v", items, off)
	}
	h.ui(func(b *browser, u *gunim.UI) { b.listing.cur.menu.OnPick(i, u) })
	h.frames(2)
	select {
	case paths := <-got:
		if len(paths) != 1 || paths[0] != filepath.Join(h.dir, "a.txt") {
			t.Fatalf("the action was handed %q", paths)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the action never reached the program")
	}
}
