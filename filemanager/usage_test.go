package filemanager

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/widget"
)

// scanAll scans root on fsys to the end.
func scanAll(t *testing.T, fsys FS, root string) *scanner {
	t.Helper()
	s := newScanner(fsys, root)
	done := make(chan struct{})
	s.run(context.Background(), func() { close(done) })
	<-done
	return s
}

// usageFS is the computer's own files, with made-up ways of keeping them:
// a file named on-… online only, loc-… on this device in a cloud, and
// pin-… always kept here; a folder named other… on another drive; and a
// folder named bad that cannot be read.
type usageFS struct{ FS }

func (u usageFS) Cloud(_ string, info fs.FileInfo) CloudState {
	switch name := info.Name(); {
	case info.IsDir():
		return CloudFolder
	case strings.HasPrefix(name, "on-"):
		return CloudOnline
	case strings.HasPrefix(name, "loc-"):
		return CloudLocal
	case strings.HasPrefix(name, "pin-"):
		return CloudPinned
	}
	return CloudNone
}

func (u usageFS) Volume(path string) (string, error) {
	if strings.HasPrefix(filepath.Base(path), "other") {
		return "other", nil
	}
	return "main", nil
}

func (u usageFS) ReadDir(ctx context.Context, dir string) ([]fs.DirEntry, error) {
	if filepath.Base(dir) == "bad" {
		return nil, errors.New("no way in")
	}
	return u.FS.ReadDir(ctx, dir)
}

func TestDiskUsageAddsUpWhatAFolderHolds(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "a/x.txt", "a/b/y.go", "z.png", "empty/")
	s := scanAll(t, LocalFS(), root)
	u := s.snapshot("", nil, MeasureAll)
	want := int64(len("a/x.txt") + len("a/b/y.go") + len("z.png"))
	if u.Total.Size != want || u.Total.Files != 3 || u.Total.Folders != 3 {
		t.Errorf("the folder adds up to %d bytes in %d files and %d folders, want %d in 3 and 3",
			u.Total.Size, u.Total.Files, u.Total.Folders, want)
	}
	if u.Reading || u.Stopped != "" || u.Errs != 0 {
		t.Errorf("the reading ended reading %v, stopped %q, with %d errors", u.Reading, u.Stopped, u.Errs)
	}
	a := u.Items[slices.IndexFunc(u.Items, func(it UsageItem) bool { return it.Key == "a" })]
	if a.Size != int64(len("a/x.txt")+len("a/b/y.go")) || a.Files != 2 || a.Folders != 1 {
		t.Errorf("folder a adds up to %d bytes in %d files and %d folders", a.Size, a.Files, a.Folders)
	}
	// Largest first under each folder.
	for i, it := range u.Items {
		if i > 0 && it.Parent == u.Items[i-1].Parent && it.Rest == 0 && it.Size > u.Items[i-1].Size {
			t.Errorf("%s, of %d bytes, comes after %s, of %d", it.Key, it.Size, u.Items[i-1].Key, u.Items[i-1].Size)
		}
	}
	exts := make([]string, 0, len(u.Types))
	for _, ty := range u.Types {
		exts = append(exts, ty.Ext)
	}
	slices.Sort(exts)
	if !slices.Equal(exts, []string{"go", "png", "txt"}) {
		t.Errorf("the types are %v", exts)
	}
}

func TestDiskUsageTellsTheCloudsFilesApart(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "docs/on-a.txt", "docs/loc-b.txt", "docs/pin-c.txt", "docs/d.txt", "on-e.txt")
	s := scanAll(t, usageFS{LocalFS()}, root)
	u := s.snapshot("", nil, MeasureAll)
	tot := u.Total
	online := int64(len("docs/on-a.txt") + len("on-e.txt"))
	if tot.Online != online || tot.Local != int64(len("docs/loc-b.txt")) || tot.Pinned != int64(len("docs/pin-c.txt")) {
		t.Errorf("online %d, on this device %d, always kept %d; want %d, %d, %d", tot.Online, tot.Local, tot.Pinned,
			online, len("docs/loc-b.txt"), len("docs/pin-c.txt"))
	}
	if !u.Cloud {
		t.Error("the state does not say some files are in a cloud")
	}
	// Sized by what is on this device, the folder of most leads.
	dev := s.snapshot("", nil, MeasureDevice)
	if dev.Items[1].Key != "docs" {
		t.Errorf("sized by what is on this device, %q comes first", dev.Items[1].Key)
	}
	on := s.snapshot("", nil, MeasureOnline)
	if e, ok := itemOf(on, "on-e.txt"); on.Items[1].Key != "docs" || !ok || measureOf(e, MeasureOnline) != int64(len("on-e.txt")) {
		t.Errorf("sized by what is online, the items are %v", keysOf(on.Items))
	}
}

func TestDiskUsageLeavesOtherDrivesAndCountsWhatCannotBeRead(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "here.txt", "other-drive/big.bin", "bad/x.txt")
	s := scanAll(t, usageFS{LocalFS()}, root)
	u := s.snapshot("", nil, MeasureAll)
	if u.Total.Size != int64(len("here.txt")) {
		t.Errorf("the folder adds up to %d bytes, want only here.txt's %d", u.Total.Size, len("here.txt"))
	}
	other, _ := itemOf(u, "other-drive")
	if other.Skipped == "" || other.Size != 0 {
		t.Errorf("the folder on another drive was read: %+v", other)
	}
	bad, _ := itemOf(u, "bad")
	if bad.Err == "" || u.Errs != 1 || !strings.Contains(u.FirstErr, "no way in") {
		t.Errorf("the folder that cannot be read says %q, and the state %d errors, the first %q", bad.Err, u.Errs, u.FirstErr)
	}
}

func TestDiskUsageSendsSmallItemsTogetherAndAllOfAFolderOpen(t *testing.T) {
	root := t.TempDir()
	spec := make([]string, 0, 51)
	spec = append(spec, "big/"+strings.Repeat("x", 200))
	for i := range 50 {
		spec = append(spec, "many/f"+string(rune('a'+i%26))+string(rune('a'+i/26))+".txt")
	}
	tree(t, root, spec...)
	// The big file, in bytes, dwarfs the small ones, which are each too
	// small to send, but not all together.
	if err := os.WriteFile(filepath.Join(root, "big", strings.Repeat("x", 200)), make([]byte, 2_000_000), 0o644); err != nil {
		t.Fatal(err)
	}
	s := scanAll(t, LocalFS(), root)
	u := s.snapshot("", nil, MeasureAll)
	rest, ok := itemOf(u, "many\x00rest")
	if !ok {
		t.Fatalf("the small files of many are not sent together: %v", keysOf(u.Items))
	}
	many, _ := itemOf(u, "many")
	if rest.Rest+countKids(u, "many") != 50 || rest.Size+sumKids(u, "many") != many.Size {
		t.Errorf("many's items sent add up to %d items and %d bytes, of %d and %d", rest.Rest+countKids(u, "many"),
			rest.Size+sumKids(u, "many"), 50, many.Size)
	}
	open := s.snapshot("", map[string]bool{"many": true}, MeasureAll)
	if _, ok := itemOf(open, "many\x00rest"); ok || countKids(open, "many") != 50 {
		t.Errorf("open, many sends %d of its 50 items one by one", countKids(open, "many"))
	}
	// In focus, the folder's own items, and the crumbs to it.
	in := s.snapshot("many", nil, MeasureAll)
	if in.Items[0].Key != "many" || in.Items[0].Parent != -1 || len(in.Crumbs) != 2 || in.Crumbs[1].Path != "many" {
		t.Errorf("with many in focus, the first item is %q and the crumbs %v", in.Items[0].Key, in.Crumbs)
	}
}

func TestDiskUsageStopsWhenAsked(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "a/b/c/d.txt")
	s := newScanner(LocalFS(), root)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	s.run(ctx, func() { close(done) })
	<-done
	if u := s.snapshot("", nil, MeasureAll); u.Reading || u.Stopped == "" {
		t.Errorf("stopped before it began, the state says reading %v, stopped %q", u.Reading, u.Stopped)
	}
}

// itemOf is the item of key in u.
func itemOf(u Usage, key string) (UsageItem, bool) {
	i := slices.IndexFunc(u.Items, func(it UsageItem) bool { return it.Key == key })
	if i < 0 {
		return UsageItem{}, false
	}
	return u.Items[i], true
}

// keysOf are the keys of items.
func keysOf(items []UsageItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Key)
	}
	return out
}

// countKids and sumKids count the items of the folder of key sent one by
// one, and add up their bytes.
func countKids(u Usage, key string) int {
	n := 0
	for _, it := range u.Items {
		if it.Parent >= 0 && u.Items[it.Parent].Key == key && it.Rest == 0 {
			n++
		}
	}
	return n
}

func sumKids(u Usage, key string) int64 {
	var n int64
	for _, it := range u.Items {
		if it.Parent >= 0 && u.Items[it.Parent].Key == key && it.Rest == 0 {
			n += it.Size
		}
	}
	return n
}

// newUsageHarness is a harness whose tests look into the window.
func newUsageHarness(t *testing.T, spec ...string) *harness {
	h := newHarness(t, spec...)
	gunim.RegisterPatch(h.w, "browser", func(b *browser, p inUI, u *gunim.UI) { p.fn(b, u) })
	return h
}

// usagePage is the Disk usage page showing, or nil.
func (h *harness) usagePage() *usagePage {
	var pg *usagePage
	h.ui(func(_ *browser, u *gunim.UI) { pg, _ = u.Mounted(h.a.usage.id).(*usagePage) })
	return pg
}

func TestDiskUsageOpensOnTheFolderSelectedAndShowsAnItemInTheFiles(t *testing.T) {
	h := newUsageHarness(t, "work/report.txt", "work/deep/notes.md", "other.txt")
	h.pick("work")
	h.do(Command{Name: CmdUsage})
	h.until("the page has read the folder", func() bool {
		pg := h.usagePage()
		return pg != nil && !pg.state.Reading && pg.state.Total.Files == 2
	})
	if pg := h.usagePage(); pg.state.Root != filepath.Join(h.dir, "work") {
		t.Fatalf("the page reads %s, want the folder selected", pg.state.Root)
	}
	// The keys of the files act on the page, not on the files behind it.
	h.press(input.KeyDelete, 0)
	if !h.exists("work/report.txt") || len(h.a.ops.dialogs) > 0 {
		t.Fatal("Delete on the page went to the files behind it")
	}
	// Show in Files opens the item's folder in another window, or tab,
	// with it selected, and the page stays.
	opened := make(chan Options, 1)
	h.a.hub.open = func(o Options) error { opened <- o; return nil }
	h.do(UsageAct{Seq: h.a.usage.seq, Key: "deep/notes.md", Act: UsageShow})
	select {
	case o := <-opened:
		if o.Dir != filepath.Join(h.dir, "work", "deep") || o.Select != "notes.md" {
			t.Errorf("Show in Files opened %s with %q selected", o.Dir, o.Select)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Show in Files opened nothing")
	}
	if !h.a.usage.open || h.usagePage() == nil {
		t.Error("Show in Files closed the page")
	}
}

func TestDiskUsageDeletesItemsAndCountsThemGone(t *testing.T) {
	h := newUsageHarness(t, "big.bin", "sub/small.txt", "keep.txt")
	if err := os.WriteFile(filepath.Join(h.dir, "big.bin"), make([]byte, 5000), 0o644); err != nil {
		t.Fatal(err)
	}
	h.do(Command{Name: CmdUsage})
	h.until("the page has read the folder", func() bool { pg := h.usagePage(); return pg != nil && !pg.state.Reading })
	pg := h.usagePage()
	// Down to the largest, and Delete moves it to the trash.
	h.press(input.KeyDown, 0)
	if pg.sel != "big.bin" {
		t.Fatalf("Down selected %q", pg.sel)
	}
	h.press(input.KeyDelete, 0)
	h.until("big.bin went to the trash, and the page counts it gone", func() bool {
		_, shown := itemOf(pg.state, "big.bin")
		return !h.exists("big.bin") && !shown && pg.state.Total.Files == 2
	})
	if pg.state.Total.Size != int64(len("sub/small.txt")+len("keep.txt")) {
		t.Errorf("the folder adds up to %d bytes", pg.state.Total.Size)
	}
	// Shift+Delete asks first, and deletes for good.
	pg.sel = "sub"
	h.press(input.KeyDelete, input.ModShift)
	h.until("the page asks", func() bool { return len(h.a.ops.dialogs) > 0 })
	c, ok := h.a.ops.dialogs[0].state.(Confirm)
	if !ok {
		t.Fatalf("the page asked %#v", h.a.ops.dialogs[0].state)
	}
	h.answer(Confirmed{Token: c.Token, OK: true})
	h.until("sub is gone, from the disk and the page", func() bool {
		_, shown := itemOf(pg.state, "sub")
		return !h.exists("sub") && !shown && pg.state.Total.Files == 1 && pg.state.Total.Folders == 0
	})
	// The folder read itself is not for deleting from here.
	if items, _ := pg.menuItems(""); slices.ContainsFunc(items, func(it widget.MenuItem) bool { return it.Label == "Delete permanently" }) {
		t.Error("the folder read offers to delete itself")
	}
}

func TestDiskUsageClosesOnEscapeAndOpensOnTheFolderShowing(t *testing.T) {
	h := newUsageHarness(t, "a.txt", "sub/b.txt")
	h.do(Command{Name: CmdUsage})
	h.until("the page shows", func() bool { pg := h.usagePage(); return pg != nil && !pg.state.Reading })
	if got := h.usagePage().state.Root; got != h.dir {
		t.Fatalf("with nothing selected the page reads %s, want the folder showing", got)
	}
	h.press(input.KeyEscape, 0)
	h.until("the page has closed", func() bool { return !h.a.usage.open })
}

func TestDiskUsageTreeWorksFromTheKeyboard(t *testing.T) {
	h := newUsageHarness(t, "big/a.bin", "big/sub/b.bin", "small.txt")
	if err := os.WriteFile(filepath.Join(h.dir, "big", "a.bin"), make([]byte, 4000), 0o644); err != nil {
		t.Fatal(err)
	}
	h.do(Command{Name: CmdUsage})
	h.until("the page has read the folder", func() bool { pg := h.usagePage(); return pg != nil && !pg.state.Reading })
	pg := h.usagePage()
	// Down from the folder read goes to its largest item, and Right opens it.
	h.press(input.KeyDown, 0)
	if pg.sel != "big" {
		t.Fatalf("Down selected %q, want big", pg.sel)
	}
	h.press(input.KeyRight, 0)
	h.until("the tree opened big", func() bool { return h.a.usage.folders["big"] && pg.open["big"] })
	h.until("big's items show in the tree", func() bool { _, ok := pg.list.at["big/a.bin"]; return ok })
	// Enter shows it in the boxes, and Backspace comes back out.
	h.press(input.KeyEnter, 0)
	h.until("the boxes show big", func() bool { return pg.state.Focus == "big" })
	if len(pg.state.Crumbs) != 2 || pg.state.Items[0].Key != "big" {
		t.Errorf("in big, the crumbs are %v and the first item %q", pg.state.Crumbs, pg.state.Items[0].Key)
	}
	h.press(input.KeyBackspace, 0)
	h.until("the boxes show the folder read again", func() bool { return pg.state.Focus == "" })
	if pg.sel != "big" {
		t.Errorf("back out, %q is selected, want big, the folder come out of", pg.sel)
	}
}

// stuckFS is the computer's own files, whose folder named stuck takes
// until let closes to read, whatever its context says.
type stuckFS struct {
	FS
	let chan struct{}
}

func (s stuckFS) ReadDir(ctx context.Context, dir string) ([]fs.DirEntry, error) {
	if filepath.Base(dir) == "stuck" {
		<-s.let
	}
	return s.FS.ReadDir(ctx, dir)
}

func TestDiskUsageSaysItStoppedThoughAReadHangs(t *testing.T) {
	root := t.TempDir()
	tree(t, root, "stuck/a.txt", "b.txt")
	let := make(chan struct{})
	defer close(let)
	s := newScanner(stuckFS{LocalFS(), let}, root)
	if u := s.snapshot("", nil, MeasureAll); u.Took != "0.0 s" {
		t.Errorf("before it starts, the reading has taken %s", u.Took)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go s.run(ctx, func() {})
	deadline := time.Now().Add(5 * time.Second)
	for s.snapshot("", nil, MeasureAll).Total.Files == 0 {
		if time.Now().After(deadline) {
			t.Fatal("b.txt was never read")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	for s.snapshot("", nil, MeasureAll).Reading {
		if time.Now().After(deadline) {
			t.Fatal("stopped, the page still says it is reading while a folder hangs")
		}
		time.Sleep(time.Millisecond)
	}
	if u := s.snapshot("", nil, MeasureAll); u.Stopped == "" {
		t.Error("stopped, the page does not say so")
	}
}

func TestDiskUsageKeepsTheKeysOfTheFilesFromThem(t *testing.T) {
	h := newUsageHarness(t, "a.txt")
	h.do(Command{Name: CmdUsage})
	h.until("the page shows", func() bool { pg := h.usagePage(); return pg != nil && !pg.state.Reading })
	for _, k := range []struct {
		key  input.Key
		mods input.Mods
	}{{input.KeyN, input.ModControl | input.ModShift}, {input.KeyC, input.ModControl}, {input.KeySpace, 0},
		{input.KeyMenu, 0}, {input.KeyF2, 0}, {input.KeyEnter, input.ModAlt}} {
		h.press(k.key, k.mods)
	}
	h.frames(10)
	if len(h.a.ops.dialogs) > 0 || h.a.viewer.open || h.a.reading.open {
		t.Error("a key on the page acted on the files behind it")
	}
	if h.usagePage() == nil || !h.a.usage.open {
		t.Error("the page closed")
	}
}

// keeperFS is usageFS that remembers what it was asked to keep on this
// device or free, and fails for an item named nope.
type keeperFS struct {
	usageFS
	asked chan string
}

func (k keeperFS) CanKeepOnDevice() bool { return true }

func (k keeperFS) KeepOnDevice(_ context.Context, path string, keep bool) error {
	k.asked <- fmt.Sprintf("%s %v", filepath.Base(path), keep)
	if filepath.Base(path) == "nope" {
		return errors.New("the provider said no")
	}
	return nil
}

func TestDiskUsageFreesUpSpaceAndKeepsOnTheDevice(t *testing.T) {
	asked := make(chan string, 8)
	h := newHarnessWith(t, func(o *Options) { o.FS = keeperFS{usageFS{LocalFS()}, asked} },
		"docs/loc-a.txt", "docs/pin-b.txt", "docs/on-c.txt", "nope/loc-d.txt", "plain.txt", "loc-e.txt")
	gunim.RegisterPatch(h.w, "browser", func(b *browser, p inUI, u *gunim.UI) { p.fn(b, u) })
	h.do(Command{Name: CmdUsage})
	h.until("the page has read the folder", func() bool { pg := h.usagePage(); return pg != nil && !pg.state.Reading })
	pg := h.usagePage()
	if !pg.state.CanKeep {
		t.Fatal("the page does not offer to free up space")
	}
	items, _ := pg.menuItems("docs")
	labels := make([]string, 0, len(items))
	for _, it := range items {
		labels = append(labels, it.Label)
	}
	if !slices.Contains(labels, "Free up space") || !slices.Contains(labels, "Always keep on this device") {
		t.Errorf("docs's menu is %v", labels)
	}
	if items, _ := pg.menuItems("plain.txt"); slices.ContainsFunc(items, func(it widget.MenuItem) bool { return it.Label == "Free up space" }) {
		t.Error("a file in no cloud offers to free up its space")
	}
	before := pg.state.Total
	h.do(UsageAct{Seq: pg.state.Seq, Key: "docs", Act: UsageFree})
	if got := <-asked; got != "docs false" {
		t.Errorf("the file system was asked %q", got)
	}
	h.until("the page says the space was freed", func() bool { return strings.HasPrefix(pg.state.Note, "Freed up") })
	docs, _ := itemOf(pg.state, "docs")
	if docs.Local != 0 || docs.Pinned != 0 || docs.Online != docs.Size {
		t.Errorf("freed, docs has %d bytes here, %d kept, %d online of %d", docs.Local, docs.Pinned, docs.Online, docs.Size)
	}
	if tot := pg.state.Total; tot.Online != before.Online+before.Local+before.Pinned-int64(len("nope/loc-d.txt")+len("loc-e.txt")) {
		t.Errorf("the total online went from %d to %d", before.Online, tot.Online)
	}
	// Kept, a file on this device is pinned; one online only stays so
	// until it has downloaded.
	h.do(UsageAct{Seq: pg.state.Seq, Key: "loc-e.txt", Act: UsageKeep})
	<-asked
	h.until("the page says the file is kept", func() bool { return strings.HasPrefix(pg.state.Note, "Kept") })
	if e, _ := itemOf(pg.state, "loc-e.txt"); e.Cloud != CloudPinned {
		t.Errorf("kept, the file's state is %v", e.Cloud)
	}
	h.do(UsageAct{Seq: pg.state.Seq, Key: "docs", Act: UsageKeep})
	<-asked
	h.until("the page says docs is kept", func() bool { return strings.HasPrefix(pg.state.Note, "Kept") && pg.state.Note != "" })
	if c, _ := itemOf(pg.state, "docs/on-c.txt"); c.Cloud != CloudOnline {
		t.Errorf("kept but not downloaded, an online file's state is %v", c.Cloud)
	}
	h.do(UsageAct{Seq: pg.state.Seq, Key: "nope", Act: UsageFree})
	<-asked
	h.until("the page says it failed, and has read the folder again", func() bool {
		pg = h.usagePage()
		return pg != nil && pg.state.NoteErr && !pg.state.Reading
	})
	if n, _ := itemOf(pg.state, "nope"); n.Local == 0 {
		t.Error("a folder the provider would not free shows as freed")
	}
}

// unkeptFS is keeperFS on a machine that cannot keep items on its device.
type unkeptFS struct{ keeperFS }

func (unkeptFS) CanKeepOnDevice() bool { return false }

func TestDiskUsageOffersToFreeUpSpaceOnlyWhereItCan(t *testing.T) {
	h := newHarnessWith(t, func(o *Options) { o.FS = unkeptFS{keeperFS{usageFS{LocalFS()}, nil}} }, "docs/loc-a.txt")
	gunim.RegisterPatch(h.w, "browser", func(b *browser, p inUI, u *gunim.UI) { p.fn(b, u) })
	h.do(Command{Name: CmdUsage})
	h.until("the page has read the folder", func() bool { pg := h.usagePage(); return pg != nil && !pg.state.Reading })
	pg := h.usagePage()
	if items, _ := pg.menuItems("docs"); pg.state.CanKeep || slices.ContainsFunc(items, func(it widget.MenuItem) bool { return it.Label == "Free up space" }) {
		t.Error("a machine that cannot keep items on its device offers to free up space")
	}
}

func TestDiskUsageScrollsADeepBoxClickedToTheMiddleOfTheTree(t *testing.T) {
	spec := make([]string, 0, 80)
	for i := range 70 {
		spec = append(spec, fmt.Sprintf("f%02d.txt", i))
	}
	spec = append(spec, "z/a/b/c/d/deep.txt")
	h := newUsageHarness(t, spec...)
	// The deep file the largest, so its folders come first, and its row
	// lands far down once the many files of each folder open above it.
	for i, name := range []string{"z/a/b/c/d/deep.txt", "z/x.bin", "z/a/x.bin", "z/a/b/x.bin", "z/a/b/c/x.bin"} {
		if err := os.WriteFile(filepath.Join(h.dir, filepath.FromSlash(name)), make([]byte, 4000-i*10), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h.do(Command{Name: CmdUsage})
	h.until("the page has read the folder", func() bool { pg := h.usagePage(); return pg != nil && !pg.state.Reading })
	pg := h.usagePage()
	h.ui(func(_ *browser, u *gunim.UI) { pg.pickBox("z/a/b/c/d/deep.txt", u) })
	h.frames(90)
	l := pg.list
	i, ok := l.at["z/a/b/c/d/deep.txt"]
	if !ok {
		t.Fatal("the deep file has no row")
	}
	view := l.box.H - usageTitlesH - 4
	mid := float32(i)*usageRowH + usageRowH/2 - l.list.Offset()
	if mid < view/2-usageRowH || mid > view/2+usageRowH {
		t.Errorf("the row, %d down, is at %.0f in a tree %.0f high, not in the middle", i, mid, view)
	}
	// A click on a row leaves the tree where it is.
	off := l.list.Offset()
	h.ui(func(_ *browser, u *gunim.UI) { pg.pickRow(string(l.keys[0]), u) })
	h.frames(30)
	if l.list.Offset() != off {
		t.Errorf("a click on a row scrolled the tree from %.0f to %.0f", off, l.list.Offset())
	}
}
