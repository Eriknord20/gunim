package filemanager

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/input"
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
	seq := h.a.usage.seq
	h.do(UsageAct{Seq: seq, Key: "deep/notes.md", Act: UsageShow})
	h.until("the folder of the item shows, with it selected", func() bool {
		return h.a.nav.path == filepath.Join(h.dir, "work", "deep") && slices.Equal(h.selected(), []string{"notes.md"})
	})
	if h.a.usage.open || h.a.usage.scan != nil {
		t.Error("the page stayed open, or kept what it read")
	}
	h.until("the page has gone", func() bool { return h.usagePage() == nil })
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
