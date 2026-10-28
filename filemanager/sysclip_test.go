package filemanager

import (
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"
)

// TestMain keeps the tests off the system's own clipboard, which holds
// whatever the person running them last copied.
func TestMain(m *testing.M) {
	systemClip = &fakeClip{}
	os.Exit(m.Run())
}

// fakeClip is a system clipboard a test fills: with paths, or with
// items held as contents, each file's contents in data by its name.
type fakeClip struct {
	mu      sync.Mutex
	n       uint32
	paths   []string
	cut     bool
	list    []outsideItem
	data    map[string]string
	emptied bool
}

// holdPaths puts paths on c, cut where cut is set, as Explorer does.
func (c *fakeClip) holdPaths(cut bool, paths ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	c.paths, c.cut, c.list, c.data = paths, cut, nil, nil
}

// holdContents puts items on c as contents, as Remote Desktop does.
func (c *fakeClip) holdContents(list []outsideItem, data map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	c.paths, c.cut, c.list, c.data = nil, false, list, data
}

func (c *fakeClip) seq() uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

func (c *fakeClip) hasFiles() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.paths) > 0 || len(c.list) > 0
}

func (c *fakeClip) files() (systemFiles, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	sf := systemFiles{paths: slices.Clone(c.paths), cut: c.cut, seq: c.n}
	if len(c.paths) == 0 {
		sf.names = topNames(c.list)
	}
	return sf, nil
}

func (c *fakeClip) outside(fn func(outsideFiles) error) error {
	c.mu.Lock()
	of := fakeFiles{list: slices.Clone(c.list), data: c.data}
	c.mu.Unlock()
	return fn(of)
}

func (c *fakeClip) empty(seq uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if seq == c.n {
		c.n++
		c.paths, c.list, c.emptied = nil, nil, true
	}
}

type fakeFiles struct {
	list []outsideItem
	data map[string]string
}

func (f fakeFiles) items() []outsideItem { return f.list }

func (f fakeFiles) open(i int) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(f.data[f.list[i].name])), nil
}

// useClip gives the test a clipboard of its own, and the next tests
// back an empty one.
func useClip(t *testing.T) *fakeClip {
	c := &fakeClip{}
	systemClip = c
	t.Cleanup(func() { systemClip = &fakeClip{} })
	return c
}

// descriptor writes one FILEDESCRIPTORW, as Windows lays it out.
func descriptor(name string, dir bool, size int64, mod time.Time) []byte {
	d := make([]byte, fileDescriptorSize)
	flags := uint32(fdAttributes | fdWritesTime)
	if !dir {
		flags |= fdFileSize
	}
	binary.LittleEndian.PutUint32(d, flags)
	if dir {
		binary.LittleEndian.PutUint32(d[36:], attrDirectory)
	}
	binary.LittleEndian.PutUint64(d[56:], uint64(mod.UnixNano()/100+116444736000000000))
	binary.LittleEndian.PutUint32(d[64:], uint32(size>>32))
	binary.LittleEndian.PutUint32(d[68:], uint32(size))
	for i, u := range utf16.Encode([]rune(name)) {
		binary.LittleEndian.PutUint16(d[72+2*i:], u)
	}
	return d
}

func TestTheFilesHeldAsContentsAreReadFromTheirDescriptors(t *testing.T) {
	mod := time.Date(2026, 10, 28, 9, 30, 0, 0, time.UTC)
	b := binary.LittleEndian.AppendUint32(nil, 3)
	b = append(b, descriptor("Reports", true, 0, mod)...)
	b = append(b, descriptor(`Reports\q3.xlsx`, false, 5<<32+7, mod)...)
	b = append(b, descriptor("notes.txt", false, 12, mod)...)
	items, err := fileDescriptors(b)
	if err != nil {
		t.Fatal(err)
	}
	want := []outsideItem{
		{name: "Reports", dir: true, size: -1, mod: mod},
		{name: "Reports/q3.xlsx", size: 5<<32 + 7, mod: mod},
		{name: "notes.txt", size: 12, mod: mod},
	}
	if len(items) != len(want) {
		t.Fatalf("read %+v", items)
	}
	for i := range want {
		if items[i].name != want[i].name || items[i].dir != want[i].dir || items[i].size != want[i].size || !items[i].mod.Equal(mod) {
			t.Fatalf("item %d is %+v, not %+v", i, items[i], want[i])
		}
	}
	if got := topNames(items); !slices.Equal(got, []string{"Reports", "notes.txt"}) {
		t.Fatalf("the items at the top are %q", got)
	}
	if _, err := fileDescriptors(b[:len(b)-1]); err == nil {
		t.Fatal("a list cut short reads")
	}
}

func TestPasteCopiesFilesHeldAsContents(t *testing.T) {
	c := useClip(t)
	h := newHarness(t, "keep.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	mod := time.Date(2025, 3, 4, 5, 6, 0, 0, time.Local)
	c.holdContents([]outsideItem{
		{name: "a.txt", size: 3, mod: mod},
		{name: "sub", dir: true, size: -1, mod: mod},
		{name: "sub/b.txt", size: -1},
		// A folder the program leaves out, listing only what it holds.
		{name: "deep/er/c.txt", size: 1},
	}, map[string]string{"a.txt": "aaa", "sub/b.txt": "bee", "deep/er/c.txt": "c"})
	h.do(Command{Name: CmdPaste})
	h.until("what was held arrives", func() bool {
		return slices.Equal(h.shown(), []string{"deep", "sub", "a.txt", "keep.txt"})
	})
	for rel, want := range map[string]string{"a.txt": "aaa", "sub/b.txt": "bee", "deep/er/c.txt": "c"} {
		if got := contents(t, filepath.Join(h.dir, filepath.FromSlash(rel))); got != want {
			t.Fatalf("%s holds %q, not %q", rel, got, want)
		}
	}
	if info, err := os.Stat(filepath.Join(h.dir, "sub")); err != nil || !info.ModTime().Equal(mod) {
		t.Fatalf("the folder's time is not the one held: %v", err)
	}
	h.until("what came is selected", func() bool { return slices.Equal(h.selected(), []string{"deep", "sub", "a.txt"}) })
	h.do(Command{Name: CmdUndo})
	h.until("undo takes it away", func() bool { return slices.Equal(h.shown(), []string{"keep.txt"}) })
}

func TestAPastedItemThatLeadsOutOfTheFolderIsRefused(t *testing.T) {
	c := useClip(t)
	h := newHarness(t, "keep.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	c.holdContents([]outsideItem{{name: "ok/../../evil.txt", size: 4}}, map[string]string{"ok/../../evil.txt": "evil"})
	h.do(Command{Name: CmdPaste})
	h.until("the failure shows", func() bool { return len(h.a.ops.dialogs) == 1 })
	if e, ok := h.a.ops.dialogs[0].state.(ErrorBox); !ok || !strings.Contains(e.Detail+e.Body, "cannot be written here") {
		t.Fatalf("the failure shows %+v", h.a.ops.dialogs[0].state)
	}
	if contents(t, filepath.Join(h.root, "evil.txt")) != "missing" || h.exists("ok") {
		t.Fatal("the paste wrote something")
	}
}

func TestAPastedFileThatClashesIsAskedAbout(t *testing.T) {
	c := useClip(t)
	h := newHarness(t, "a.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	c.holdContents([]outsideItem{{name: "a.txt", size: -1}}, map[string]string{"a.txt": "new"})
	h.do(Command{Name: CmdPaste})
	h.until("the clash is asked about", func() bool { return len(h.a.ops.dialogs) == 1 })
	ask, ok := h.a.ops.dialogs[0].state.(ClashAsk)
	if !ok || ask.Name != "a.txt" || !ask.SameKind || ask.New != "On the clipboard" {
		t.Fatalf("the dialog asks %+v", h.a.ops.dialogs[0].state)
	}
	h.answer(ClashAnswered{Op: ask.Op, Choice: ChoiceKeepBoth})
	h.until("both are kept", func() bool { return slices.Equal(h.shown(), []string{"a (2).txt", "a.txt"}) })
	if contents(t, filepath.Join(h.dir, "a.txt")) != "a.txt" || contents(t, filepath.Join(h.dir, "a (2).txt")) != "new" {
		t.Fatal("keeping both lost one")
	}
}

func TestPasteTakesPathsAnotherProgramCopied(t *testing.T) {
	c := useClip(t)
	h := newHarness(t, "here.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	other := filepath.Join(h.root, "other")
	tree(t, other, "copied.txt", "cut.txt")
	c.holdPaths(false, filepath.Join(other, "copied.txt"))
	h.do(Command{Name: CmdPaste})
	h.until("the copy arrives", func() bool { return slices.Contains(h.shown(), "copied.txt") })
	if contents(t, filepath.Join(other, "copied.txt")) != "copied.txt" {
		t.Fatal("the copy took the file away")
	}

	c.holdPaths(true, filepath.Join(other, "cut.txt"))
	h.do(Command{Name: CmdPaste})
	h.until("the move arrives", func() bool { return slices.Contains(h.shown(), "cut.txt") })
	if contents(t, filepath.Join(other, "cut.txt")) != "missing" {
		t.Fatal("what was cut stayed where it was")
	}
	if !c.emptied || c.hasFiles() {
		t.Fatal("what was cut stays on the clipboard, to paste again")
	}
}

func TestPasteTakesWhicheverWasCopiedLast(t *testing.T) {
	c := useClip(t)
	h := newHarness(t, "mine.txt", "sub/")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 2 })
	other := filepath.Join(h.root, "other")
	tree(t, other, "theirs.txt")
	c.holdPaths(false, filepath.Join(other, "theirs.txt"))
	h.pick("mine.txt")
	h.do(Command{Name: CmdCopy})
	h.do(Navigate{Path: filepath.Join(h.dir, "sub")})
	h.until("the folder opens", func() bool { return len(h.shown()) == 0 })
	h.do(Command{Name: CmdPaste})
	h.until("the window's own copy arrives", func() bool { return slices.Equal(h.shown(), []string{"mine.txt"}) })

	c.holdPaths(false, filepath.Join(other, "theirs.txt"))
	h.do(Command{Name: CmdPaste})
	h.until("the other program's copy arrives", func() bool {
		return slices.Equal(h.shown(), []string{"mine.txt", "theirs.txt"})
	})
}
