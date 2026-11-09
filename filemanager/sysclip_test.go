package filemanager

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	iofs "io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// TestMain keeps the tests off the system's own clipboard, which holds
// whatever the person running them last copied.
func TestMain(m *testing.M) {
	systemClip = &fakeClip{}
	os.Exit(m.Run())
}

// fakeClip is a system clipboard a test fills: with paths, or with
// items held as contents, each file's contents in data by its name, and
// broken the files whose contents cannot be read.
type fakeClip struct {
	mu      sync.Mutex
	n       uint32
	paths   []string
	cut     bool
	list    []outsideItem
	data    map[string]string
	broken  map[string]bool
	opened  map[string]func()
	emptied bool
}

// onOpen calls fn as the file called name is opened, once.
func (c *fakeClip) onOpen(name string, fn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.opened = map[string]func(){name: fn}
}

// breakFiles makes the contents of the files called names unreadable, or
// all readable again where there are none.
func (c *fakeClip) breakFiles(names ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.broken = map[string]bool{}
	for _, n := range names {
		c.broken[n] = true
	}
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

func (c *fakeClip) outside(seq uint32, fn func(outsideFiles) error) error {
	if err := stillHeld(seq, c.seq()); err != nil {
		return err
	}
	c.mu.Lock()
	of := fakeFiles{c: c, seq: seq, list: slices.Clone(c.list), data: c.data, broken: c.broken}
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
	c      *fakeClip
	seq    uint32
	list   []outsideItem
	data   map[string]string
	broken map[string]bool
}

func (f fakeFiles) items() []outsideItem { return f.list }

func (f fakeFiles) open(i int) (io.ReadCloser, error) {
	if err := stillHeld(f.seq, f.c.seq()); err != nil {
		return nil, err
	}
	if f.broken[f.list[i].name] {
		return nil, errors.New("the stream broke")
	}
	f.c.mu.Lock()
	fn := f.c.opened[f.list[i].name]
	delete(f.c.opened, f.list[i].name)
	f.c.mu.Unlock()
	if fn != nil {
		defer fn()
	}
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

func TestAFolderSkippedAfterItsItemsTakesThemWithIt(t *testing.T) {
	c := useClip(t)
	h := newHarness(t, "sub/")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	// The clipboard lists the folder's file before the folder.
	c.holdContents([]outsideItem{{name: "sub/b.txt", size: 3}, {name: "sub", dir: true, size: -1}},
		map[string]string{"sub/b.txt": "bee"})
	h.do(Command{Name: CmdPaste})
	h.until("the clash is asked about", func() bool { return len(h.a.ops.dialogs) == 1 })
	ask, ok := h.a.ops.dialogs[0].state.(ClashAsk)
	if !ok || ask.Name != "sub" {
		t.Fatalf("the dialog asks %+v", h.a.ops.dialogs[0].state)
	}
	h.answer(ClashAnswered{Op: ask.Op, Choice: ChoiceSkip})
	h.until("the paste ends", func() bool { return len(h.a.ops.running) == 0 })
	if len(h.a.ops.dialogs) > 0 {
		t.Fatalf("the paste shows %+v", h.a.ops.dialogs[0].state)
	}
	if h.exists("sub/b.txt") || h.exists("b.txt") || contents(t, "b.txt") != "missing" {
		t.Fatal("the file of the folder skipped was written")
	}
}

func TestAFileHeldWithItemsInsideIsRefused(t *testing.T) {
	c := useClip(t)
	h := newHarness(t, "keep.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	c.holdContents([]outsideItem{{name: "x", size: 1}, {name: "x/y", size: 1}}, map[string]string{"x": "x", "x/y": "y"})
	h.do(Command{Name: CmdPaste})
	h.until("the failure shows", func() bool { return len(h.a.ops.dialogs) == 1 })
	if e, ok := h.a.ops.dialogs[0].state.(ErrorBox); !ok || !strings.Contains(e.Detail+e.Body, "as a file") {
		t.Fatalf("the failure shows %+v", h.a.ops.dialogs[0].state)
	}
	if h.exists("y") || contents(t, "y") != "missing" {
		t.Fatal("the item inside the file was written outside it")
	}
}

func TestAPasteIntoAFolderAlreadyThereCanBeUndone(t *testing.T) {
	c := useClip(t)
	h := newHarness(t, "Reports/old.txt", "Reports/same.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	got := notices(h)
	c.holdContents([]outsideItem{
		{name: "Reports", dir: true, size: -1},
		{name: "Reports/new.txt", size: 3},
		{name: "Reports/same.txt", size: 3},
	}, map[string]string{"Reports/new.txt": "new", "Reports/same.txt": "now"})
	h.do(Command{Name: CmdPaste})
	for _, name := range []string{"Reports", "same.txt"} {
		h.until("the clash about "+name+" is asked", func() bool { return len(h.a.ops.dialogs) == 1 })
		ask, ok := h.a.ops.dialogs[0].state.(ClashAsk)
		if !ok || ask.Name != name {
			t.Fatalf("the dialog asks %+v", h.a.ops.dialogs[0].state)
		}
		h.answer(ClashAnswered{Op: ask.Op, Choice: ChoiceReplace})
	}
	h.until("the paste ends", func() bool { return len(h.a.ops.running) == 0 })
	if contents(t, at(h.dir, "Reports/new.txt")) != "new" || contents(t, at(h.dir, "Reports/same.txt")) != "now" {
		t.Fatal("the paste did not merge the folders")
	}
	if !slices.ContainsFunc(got(), func(n Notice) bool { return strings.Contains(n.Body, "1 file replaced") }) {
		t.Fatalf("no notice says a file was replaced: %+v", got())
	}
	h.do(Command{Name: CmdUndo})
	h.until("undo takes the new file away", func() bool { return !h.exists("Reports/new.txt") })
	if contents(t, at(h.dir, "Reports/old.txt")) != "Reports/old.txt" {
		t.Fatal("undo took away what was there before")
	}
}

func TestAFolderSpelledTwoWaysGoesToOnePlace(t *testing.T) {
	c := useClip(t)
	h := newHarness(t, "keep.txt")
	if !h.a.ps.ignoresCase() {
		t.Skip("the folder's paths tell names apart by case")
	}
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	c.holdContents([]outsideItem{{name: "Reports", dir: true, size: -1}, {name: "reports/q3.txt", size: 2}},
		map[string]string{"reports/q3.txt": "q3"})
	h.do(Command{Name: CmdPaste})
	h.until("the folder arrives", func() bool { return slices.Equal(h.shown(), []string{"Reports", "keep.txt"}) })
	h.until("the paste ends", func() bool { return len(h.a.ops.running) == 0 })
	if len(h.a.ops.dialogs) > 0 {
		t.Fatalf("the paste shows %+v", h.a.ops.dialogs[0].state)
	}
	if got := names(t, filepath.Join(h.dir, "Reports")); !slices.Equal(got, []string{"q3.txt"}) {
		t.Fatalf("Reports holds %q", got)
	}
}

func TestACutThatDoesNotMoveStaysOnTheClipboard(t *testing.T) {
	c := useClip(t)
	h := newHarness(t, "here.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	// The folder above this one cannot go into it.
	c.holdPaths(true, h.root)
	h.do(Command{Name: CmdPaste})
	h.until("the failure shows", func() bool { return len(h.a.ops.dialogs) == 1 })
	if c.emptied || !c.hasFiles() {
		t.Fatal("a cut that did not move was taken off the clipboard")
	}
}

func TestACutFromAnotherProgramIsPastedOnce(t *testing.T) {
	c := useClip(t)
	h := newHarness(t, "cut.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	got := notices(h)
	other := filepath.Join(h.root, "other")
	tree(t, other, "cut.txt")
	c.holdPaths(true, filepath.Join(other, "cut.txt"))
	h.do(Command{Name: CmdPaste})
	// The clash holds the move open, and the clipboard keeps the cut.
	h.until("the clash is asked about", func() bool { return len(h.a.ops.dialogs) == 1 })
	h.do(Command{Name: CmdPaste})
	h.until("the second paste is turned away", func() bool {
		return slices.ContainsFunc(got(), func(n Notice) bool { return strings.Contains(n.Title, "moving, or has moved") })
	})
	if len(h.a.ops.running) != 1 {
		t.Fatalf("one cut started %d moves", len(h.a.ops.running))
	}
	ask := h.a.ops.dialogs[0].state.(ClashAsk)
	h.answer(ClashAnswered{Op: ask.Op, Choice: ChoiceReplace})
	h.until("the move ends", func() bool { return len(h.a.ops.running) == 0 })
	if contents(t, filepath.Join(other, "cut.txt")) != "missing" || !c.emptied {
		t.Fatal("the cut did not move, or stayed on the clipboard")
	}
}

func TestAFolderWhoseFileFailedIsPastedAgainOnRetry(t *testing.T) {
	c := useClip(t)
	h := newHarness(t, "keep.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	c.holdContents([]outsideItem{
		{name: "Photos", dir: true, size: -1},
		{name: "Photos/a.jpg", size: 1},
		{name: "Photos/b.jpg", size: 1},
		{name: "z.txt", size: 1},
	}, map[string]string{"Photos/a.jpg": "a", "Photos/b.jpg": "b", "z.txt": "z"})
	c.breakFiles("Photos/b.jpg")
	h.do(Command{Name: CmdPaste})
	h.until("the failure shows", func() bool { return len(h.a.ops.dialogs) == 1 })
	if e, ok := h.a.ops.dialogs[0].state.(ErrorBox); !ok || !e.Retry {
		t.Fatalf("the failure shows %+v, with nothing to try again", h.a.ops.dialogs[0].state)
	}
	c.breakFiles()
	h.answer(DialogClosed{Retry: true})
	// The retry finds the folder there, merges with it, and keeps a.jpg.
	answers := map[string]Choice{"Photos": ChoiceReplace, "a.jpg": ChoiceSkip}
	h.until("the retry ends", func() bool {
		if len(h.a.ops.dialogs) == 1 {
			ask, ok := h.a.ops.dialogs[0].state.(ClashAsk)
			if !ok {
				t.Fatalf("the retry shows %+v", h.a.ops.dialogs[0].state)
			}
			h.answer(ClashAnswered{Op: ask.Op, Choice: answers[ask.Name]})
		}
		return len(h.a.ops.running) == 0 && h.exists("Photos/b.jpg")
	})
	if contents(t, at(h.dir, "Photos/b.jpg")) != "b" || contents(t, at(h.dir, "z.txt")) != "z" {
		t.Fatal("the retry did not paste what the failure left out")
	}
}

func TestNamesTheFolderTakesForOneAreAskedAbout(t *testing.T) {
	seconds := []string{"Linux/README"}
	if runtime.GOOS == "windows" {
		// A program on a system that tells these apart may hold both.
		seconds = append(seconds, "Linux/readme", "Linux/README.")
	}
	for _, second := range seconds {
		t.Run(second, func(t *testing.T) {
			c := useClip(t)
			h := newHarness(t, "keep.txt")
			h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
			c.holdContents([]outsideItem{
				{name: "Linux", dir: true, size: -1},
				{name: "Linux/README", size: 5},
				{name: second, size: 5},
			}, map[string]string{"Linux/README": "upper", second: "lower"})
			h.do(Command{Name: CmdPaste})
			h.until("the clash is asked about", func() bool { return len(h.a.ops.dialogs) == 1 })
			ask, ok := h.a.ops.dialogs[0].state.(ClashAsk)
			if !ok || !strings.EqualFold(strings.TrimSuffix(ask.Name, "."), "README") {
				t.Fatalf("the dialog asks %+v", h.a.ops.dialogs[0].state)
			}
			h.answer(ClashAnswered{Op: ask.Op, Choice: ChoiceKeepBoth})
			h.until("the paste ends", func() bool { return len(h.a.ops.running) == 0 })
			if got := names(t, filepath.Join(h.dir, "Linux")); len(got) != 2 {
				t.Fatalf("Linux holds %q, not both files", got)
			}
		})
	}
}

func TestAFolderListedTwiceGoesToOnePlace(t *testing.T) {
	seconds := []string{"X/Sub"}
	if runtime.GOOS == "windows" {
		seconds = append(seconds, "X/sub", "X/Sub.")
	}
	for _, second := range seconds {
		t.Run(second, func(t *testing.T) {
			c := useClip(t)
			h := newHarness(t, "keep.txt")
			h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
			c.holdContents([]outsideItem{
				{name: "X", dir: true, size: -1},
				{name: "X/Sub", dir: true, size: -1},
				{name: "X/Sub/a.txt", size: 1},
				{name: second, dir: true, size: -1},
				{name: second + "/b.txt", size: 1},
			}, map[string]string{"X/Sub/a.txt": "a", second + "/b.txt": "b"})
			h.do(Command{Name: CmdPaste})
			h.until("the folder arrives", func() bool { return slices.Equal(h.shown(), []string{"X", "keep.txt"}) })
			h.until("the paste ends", func() bool { return len(h.a.ops.running) == 0 })
			if len(h.a.ops.dialogs) > 0 {
				t.Fatalf("the paste shows %+v", h.a.ops.dialogs[0].state)
			}
			if got := names(t, filepath.Join(h.dir, "X", "Sub")); !slices.Equal(got, []string{"a.txt", "b.txt"}) {
				t.Fatalf("X/Sub holds %q", got)
			}
		})
	}
}

func TestACutWhoseMoveStoppedCanBePastedAgain(t *testing.T) {
	c := useClip(t)
	h := newHarness(t, "cut.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	other := filepath.Join(h.root, "other")
	tree(t, other, "cut.txt")
	c.holdPaths(true, filepath.Join(other, "cut.txt"))
	for range 2 {
		h.do(Command{Name: CmdPaste})
		h.until("the clash is asked about", func() bool { return len(h.a.ops.dialogs) == 1 })
		ask := h.a.ops.dialogs[0].state.(ClashAsk)
		h.answer(ClashAnswered{Op: ask.Op, Stop: true})
		h.until("the move stops", func() bool { return len(h.a.ops.running) == 0 })
		if contents(t, filepath.Join(other, "cut.txt")) != "cut.txt" || c.emptied {
			t.Fatal("a move that was stopped moved the cut, or took it off the clipboard")
		}
	}
}

func TestARetryAndAPasteDoNotMoveACutAtOnce(t *testing.T) {
	c := useClip(t)
	h := newHarness(t, "cut.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	got := notices(h)
	other := filepath.Join(h.root, "other")
	tree(t, other, "cut.txt")
	// The folder above this one cannot go into it.
	c.holdPaths(true, filepath.Join(other, "cut.txt"), h.root)
	h.do(Command{Name: CmdPaste})
	h.until("the clash is asked about", func() bool { return len(h.a.ops.dialogs) == 1 })
	ask := h.a.ops.dialogs[0].state.(ClashAsk)
	h.answer(ClashAnswered{Op: ask.Op, Choice: ChoiceSkip})
	h.until("the move fails on the folder above", func() bool {
		e, ok := h.a.ops.dialogs, len(h.a.ops.dialogs) == 1
		return ok && e[0].state.(ErrorBox).Retry
	})
	// A second paste while the error waits runs, and holds the cut.
	h.do(Command{Name: CmdPaste})
	h.until("the second paste asks", func() bool { return len(h.a.ops.dialogs) == 2 })
	box := h.a.ops.dialogs[0]
	if _, ok := box.state.(ErrorBox); !ok {
		t.Fatalf("the first dialog is %+v", box.state)
	}
	if err := h.w.Client().Unmount(box.id); err != nil {
		t.Fatal(err)
	}
	h.do(DialogClosed{Retry: true})
	h.until("the retry is turned away", func() bool {
		return slices.ContainsFunc(got(), func(n Notice) bool { return strings.Contains(n.Title, "moving, or has moved") })
	})
	if len(h.a.ops.running) != 1 {
		t.Fatalf("one cut has %d moves running", len(h.a.ops.running))
	}
}

func TestAnAnswerForAllIsNotOneForClashesWithinTheClipboard(t *testing.T) {
	c := useClip(t)
	h := newHarness(t, "a.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	c.holdContents([]outsideItem{
		{name: "a.txt", size: 3},
		{name: "Linux", dir: true, size: -1},
		{name: "Linux/README", size: 5},
		{name: "Linux/README", size: 5},
	}, map[string]string{"a.txt": "new", "Linux/README": "upper"})
	h.do(Command{Name: CmdPaste})
	h.until("the clash about a.txt is asked", func() bool { return len(h.a.ops.dialogs) == 1 })
	ask := h.a.ops.dialogs[0].state.(ClashAsk)
	h.answer(ClashAnswered{Op: ask.Op, Choice: ChoiceReplace, All: true})
	h.until("the clash within the clipboard is asked", func() bool { return len(h.a.ops.dialogs) == 1 })
	if ask, ok := h.a.ops.dialogs[0].state.(ClashAsk); !ok || ask.Name != "README" {
		t.Fatalf("the dialog asks %+v", h.a.ops.dialogs[0].state)
	}
}

func TestAFolderAtTheTopSpelledTwoWaysGoesToOnePlace(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the folder's paths tell these names apart")
	}
	for _, second := range []string{"X.", "X "} {
		t.Run(second, func(t *testing.T) {
			c := useClip(t)
			h := newHarness(t, "keep.txt")
			h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
			c.holdContents([]outsideItem{
				{name: "X", dir: true, size: -1},
				{name: "X/a.txt", size: 1},
				{name: second, dir: true, size: -1},
				{name: second + "/b.txt", size: 1},
			}, map[string]string{"X/a.txt": "a", second + "/b.txt": "b"})
			h.do(Command{Name: CmdPaste})
			h.until("the folder arrives", func() bool { return slices.Equal(h.shown(), []string{"X", "keep.txt"}) })
			h.until("the paste ends", func() bool { return len(h.a.ops.running) == 0 })
			if len(h.a.ops.dialogs) > 0 {
				t.Fatalf("the paste shows %+v", h.a.ops.dialogs[0].state)
			}
			if got := names(t, filepath.Join(h.dir, "X")); !slices.Equal(got, []string{"a.txt", "b.txt"}) {
				t.Fatalf("X holds %q", got)
			}
		})
	}
}

func TestACopyKeptBesideAnotherIsAskedAboutByALaterItem(t *testing.T) {
	c := useClip(t)
	h := newHarness(t, "keep.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	// A program may list a name twice, and then the name Keep both gives.
	c.holdContents([]outsideItem{
		{name: "D", dir: true, size: -1},
		{name: "D/x.txt", size: 1},
		{name: "D/x.txt", size: 1},
		{name: "D/x (2).txt", size: 1},
	}, map[string]string{"D/x.txt": "1", "D/x (2).txt": "3"})
	h.do(Command{Name: CmdPaste})
	for _, name := range []string{"x.txt", "x (2).txt"} {
		h.until("the clash about "+name+" is asked", func() bool { return len(h.a.ops.dialogs) == 1 })
		ask, ok := h.a.ops.dialogs[0].state.(ClashAsk)
		if !ok || ask.Name != name {
			t.Fatalf("the dialog asks %+v", h.a.ops.dialogs[0].state)
		}
		h.answer(ClashAnswered{Op: ask.Op, Choice: ChoiceKeepBoth})
	}
	h.until("the paste ends", func() bool { return len(h.a.ops.running) == 0 })
	if got := names(t, filepath.Join(h.dir, "D")); len(got) != 3 {
		t.Fatalf("D holds %q, not all three files", got)
	}
}

func TestAShortNameOfAFileThePasteWroteIsAskedAbout(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows gives files short names")
	}
	probe := t.TempDir()
	tree(t, probe, "longfilename.txt")
	if _, err := os.Lstat(filepath.Join(probe, "LONGFI~1.TXT")); err != nil {
		t.Skip("this volume gives files no short names")
	}
	c := useClip(t)
	h := newHarness(t, "keep.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	c.holdContents([]outsideItem{
		{name: "D", dir: true, size: -1},
		{name: "D/longfilename.txt", size: 4},
		{name: "D/LONGFI~1.TXT", size: 5},
	}, map[string]string{"D/longfilename.txt": "long", "D/LONGFI~1.TXT": "short"})
	h.do(Command{Name: CmdPaste})
	h.until("the clash is asked about", func() bool { return len(h.a.ops.dialogs) == 1 })
	if _, ok := h.a.ops.dialogs[0].state.(ClashAsk); !ok {
		t.Fatalf("the dialog asks %+v", h.a.ops.dialogs[0].state)
	}
}

func TestAPasteReadBeforeACutMovedDoesNotMoveItAgain(t *testing.T) {
	c := useClip(t)
	h := newHarness(t, "here.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	got := notices(h)
	other := filepath.Join(h.root, "other")
	tree(t, other, "cut.txt")
	c.holdPaths(true, filepath.Join(other, "cut.txt"))
	// A held Ctrl+V reads the clipboard, and is taken only once the move
	// has ended and emptied it.
	stale, err := c.files()
	if err != nil {
		t.Fatal(err)
	}
	h.do(Command{Name: CmdPaste})
	h.until("the cut moves", func() bool { return h.exists("cut.txt") && len(h.a.ops.running) == 0 })
	h.a.pastedSystem(h.a.fs.ID(), h.dir, stale, nil)
	h.until("the late paste is turned away", func() bool {
		return slices.ContainsFunc(got(), func(n Notice) bool { return strings.Contains(n.Title, "moving, or has moved") })
	})
	if len(h.a.ops.running) > 0 || len(h.a.ops.dialogs) > 0 {
		t.Fatal("the late paste moved the cut again")
	}
}

func TestARetryOfAnOldCutLeavesTheCutThatMovesHeld(t *testing.T) {
	c := useClip(t)
	h := newHarness(t, "t.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	got := notices(h)
	other := filepath.Join(h.root, "other")
	tree(t, other, "u.txt", "t.txt")
	// The folder above this one cannot go into it.
	c.holdPaths(true, filepath.Join(other, "u.txt"), h.root)
	h.do(Command{Name: CmdPaste})
	h.until("the old cut fails", func() bool { return len(h.a.ops.dialogs) == 1 })
	c.holdPaths(true, filepath.Join(other, "t.txt"))
	h.do(Command{Name: CmdPaste})
	h.until("the new cut's clash is asked", func() bool { return len(h.a.ops.dialogs) == 2 })
	box := h.a.ops.dialogs[0]
	if e, ok := box.state.(ErrorBox); !ok || !e.Retry {
		t.Fatalf("the first dialog is %+v", box.state)
	}
	if err := h.w.Client().Unmount(box.id); err != nil {
		t.Fatal(err)
	}
	h.do(DialogClosed{Retry: true})
	h.until("the retry fails too", func() bool {
		_, ok := h.a.ops.dialogs[len(h.a.ops.dialogs)-1].state.(ErrorBox)
		return len(h.a.ops.running) == 1 && ok
	})
	h.do(Command{Name: CmdPaste})
	h.until("the new cut's second paste is turned away", func() bool {
		return slices.ContainsFunc(got(), func(n Notice) bool { return strings.Contains(n.Title, "moving, or has moved") })
	})
	if len(h.a.ops.running) != 1 {
		t.Fatalf("the new cut has %d moves running", len(h.a.ops.running))
	}
}

func TestACutMovedWhereTheSystemGivesNoSeqLeavesTheClipboard(t *testing.T) {
	c := useClip(t)
	h := newHarness(t, "here.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	other := filepath.Join(h.root, "other")
	tree(t, other, "cut.txt")
	c.holdPaths(true, filepath.Join(other, "cut.txt"))
	c.mu.Lock()
	c.n = 0
	c.mu.Unlock()
	h.do(Command{Name: CmdPaste})
	h.until("the cut moves", func() bool { return h.exists("cut.txt") && len(h.a.ops.running) == 0 })
	if c.hasFiles() {
		t.Fatal("what was cut and moved stays on the clipboard, to paste again")
	}
}

func TestAClashBetweenTwoItemsAtTheTopHasItsOwnAnswer(t *testing.T) {
	c := useClip(t)
	h := newHarness(t, "a.txt", "b.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 2 })
	c.holdContents([]outsideItem{
		{name: "b.txt", size: 3},
		{name: "a.txt", size: 3},
		{name: "a.txt", size: 3},
	}, map[string]string{"a.txt": "new", "b.txt": "new"})
	h.do(Command{Name: CmdPaste})
	h.until("the clash about b.txt is asked", func() bool { return len(h.a.ops.dialogs) == 1 })
	ask := h.a.ops.dialogs[0].state.(ClashAsk)
	h.answer(ClashAnswered{Op: ask.Op, Choice: ChoiceReplace, All: true})
	h.until("the clash between the two a.txt is asked", func() bool { return len(h.a.ops.dialogs) == 1 })
	if ask, ok := h.a.ops.dialogs[0].state.(ClashAsk); !ok || ask.Name != "a.txt" {
		t.Fatalf("the dialog asks %+v", h.a.ops.dialogs[0].state)
	}
}

func TestACutPastedElsewhereLeavesTheClipboardWithoutTheWindow(t *testing.T) {
	c := useClip(t)
	ho := newHolder()
	h := newHarnessWith(t, func(o *Options) { onSlashFS(o); o.Transfer = ho.transfer }, "here.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	got := notices(h)
	other := filepath.Join(h.root, "other")
	tree(t, other, "cut.txt")
	c.holdPaths(true, filepath.Join(other, "cut.txt"))
	h.do(Command{Name: CmdPaste})
	call := ho.next(h)
	h.do(Command{Name: CmdPaste})
	h.until("the second paste is turned away", func() bool {
		return slices.ContainsFunc(got(), func(n Notice) bool { return strings.Contains(n.Title, "moving, or has moved") })
	})
	call.reply <- nil
	// The window does not take the end here, as one that closed would not.
	for deadline := time.Now().Add(5 * time.Second); c.hasFiles(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the clipboard keeps the cut until the window takes the end of its move")
		}
	}
}

func TestAKeptCopyLeavesTheNameOfWhatWasThereToBeAskedAbout(t *testing.T) {
	c := useClip(t)
	h := newHarness(t, "a.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	c.holdContents([]outsideItem{
		{name: "b.txt", size: 1},
		{name: "b.txt", size: 1},
		{name: "a.txt", size: 1},
		{name: "a.txt", size: 1},
	}, map[string]string{"a.txt": "2", "b.txt": "b"})
	h.do(Command{Name: CmdPaste})
	// Replace all for the clash between the two b.txt, and keep both for
	// a.txt and the one that was there: the second a.txt clashes with
	// that one again, and is asked about.
	h.until("the clash between the two b.txt is asked", func() bool { return len(h.a.ops.dialogs) == 1 })
	ask := h.a.ops.dialogs[0].state.(ClashAsk)
	h.answer(ClashAnswered{Op: ask.Op, Choice: ChoiceReplace, All: true})
	for range 2 {
		h.until("the clash about a.txt is asked", func() bool { return len(h.a.ops.dialogs) == 1 })
		ask := h.a.ops.dialogs[0].state.(ClashAsk)
		if ask.Name != "a.txt" {
			t.Fatalf("the dialog asks about %s", ask.Name)
		}
		h.answer(ClashAnswered{Op: ask.Op, Choice: ChoiceKeepBoth})
	}
	h.until("the paste ends", func() bool { return len(h.a.ops.running) == 0 })
	if contents(t, filepath.Join(h.dir, "a.txt")) != "a.txt" {
		t.Fatal("the a.txt that was there was replaced")
	}
}

func TestACutWhoseItemsAreAllSkippedStaysOnTheClipboard(t *testing.T) {
	c := useClip(t)
	h := newHarness(t, "cut.txt", "sub/")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 2 })
	other := filepath.Join(h.root, "other")
	tree(t, other, "cut.txt")
	c.holdPaths(true, filepath.Join(other, "cut.txt"))
	h.do(Command{Name: CmdPaste})
	h.until("the clash is asked about", func() bool { return len(h.a.ops.dialogs) == 1 })
	ask := h.a.ops.dialogs[0].state.(ClashAsk)
	h.answer(ClashAnswered{Op: ask.Op, Choice: ChoiceSkip})
	h.until("the move ends", func() bool { return len(h.a.ops.running) == 0 })
	if !c.hasFiles() {
		t.Fatal("a cut that moved nothing left the clipboard")
	}
	h.do(Navigate{Path: filepath.Join(h.dir, "sub")})
	h.until("the folder opens", func() bool { return len(h.shown()) == 0 })
	h.do(Command{Name: CmdPaste})
	h.until("the cut moves after all", func() bool { return h.exists("sub/cut.txt") })
}

func TestACutPastedWhereItIsStaysOnTheClipboard(t *testing.T) {
	c := useClip(t)
	h := newHarness(t, "cut.txt", "sub/")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 2 })
	got := notices(h)
	c.holdPaths(true, filepath.Join(h.dir, "cut.txt"))
	h.do(Command{Name: CmdPaste})
	h.until("the move ends", func() bool { return len(got()) > 0 && len(h.a.ops.running) == 0 })
	if !c.hasFiles() {
		t.Fatal("a cut pasted where it is left the clipboard")
	}
	h.do(Navigate{Path: filepath.Join(h.dir, "sub")})
	h.until("the folder opens", func() bool { return len(h.shown()) == 0 })
	h.do(Command{Name: CmdPaste})
	h.until("the cut moves after all", func() bool { return h.exists("sub/cut.txt") })
}

func TestACutPastedAgainMovesWhatAStoppedMoveLeft(t *testing.T) {
	c := useClip(t)
	h := newHarness(t, "b.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	other := filepath.Join(h.root, "other")
	tree(t, other, "a.txt", "b.txt", "c.txt")
	if err := os.WriteFile(filepath.Join(other, "b.txt"), []byte("cut b"), 0o644); err != nil {
		t.Fatal(err)
	}
	c.holdPaths(true, filepath.Join(other, "a.txt"), filepath.Join(other, "b.txt"), filepath.Join(other, "c.txt"))
	for i, choice := range []ClashAnswered{{Stop: true}, {Choice: ChoiceReplace}} {
		h.do(Command{Name: CmdPaste})
		h.until("the clash about b.txt is asked", func() bool { return len(h.a.ops.dialogs) == 1 })
		ask, ok := h.a.ops.dialogs[0].state.(ClashAsk)
		if !ok {
			t.Fatalf("paste %d shows %+v", i+1, h.a.ops.dialogs[0].state)
		}
		choice.Op = ask.Op
		h.answer(choice)
		h.until("the move ends", func() bool { return len(h.a.ops.running) == 0 })
		if i == 0 && (!h.exists("a.txt") || h.exists("c.txt")) {
			t.Fatal("the stopped move did not stop where it was stopped")
		}
	}
	if len(h.a.ops.dialogs) > 0 {
		t.Fatalf("the second paste shows %+v", h.a.ops.dialogs[0].state)
	}
	if contents(t, filepath.Join(h.dir, "b.txt")) != "cut b" || !h.exists("c.txt") {
		t.Fatal("the second paste did not move what the first left")
	}
}

// noSameFS is a slash file system that cannot tell two files for one,
// as one reached by SFTP may not.
type noSameFS struct{ slashFS }

func (noSameFS) SameFile(_, _ iofs.FileInfo) bool { return false }

func TestAClashBetweenItemsTheFileSystemTakesForOneIsAskedAbout(t *testing.T) {
	probe := t.TempDir()
	tree(t, probe, "A.txt")
	if _, err := os.Lstat(filepath.Join(probe, "a.txt")); err != nil {
		t.Skip("this file system tells names apart by case")
	}
	for name, on := range map[string]func(o *Options){
		"slash paths":                     onSlashFS,
		"slash paths, files not compared": func(o *Options) { o.FS, o.Dir = noSameFS{slashFS{root: o.Dir}}, "/" },
	} {
		t.Run(name, func(t *testing.T) {
			c := useClip(t)
			h := newHarnessWith(t, on, "x.txt")
			h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
			c.holdContents([]outsideItem{
				{name: "x.txt", size: 1},
				{name: "A.txt", size: 5},
				{name: "a.txt", size: 5},
			}, map[string]string{"x.txt": "X", "A.txt": "upper", "a.txt": "lower"})
			h.do(Command{Name: CmdPaste})
			h.until("the clash about x.txt is asked", func() bool { return len(h.a.ops.dialogs) == 1 })
			ask := h.a.ops.dialogs[0].state.(ClashAsk)
			h.answer(ClashAnswered{Op: ask.Op, Choice: ChoiceReplace, All: true})
			h.until("the clash about a.txt is asked", func() bool { return len(h.a.ops.dialogs) == 1 })
			if ask, ok := h.a.ops.dialogs[0].state.(ClashAsk); !ok || ask.Name != "a.txt" {
				t.Fatalf("the dialog asks %+v", h.a.ops.dialogs[0].state)
			}
		})
	}
}

func TestAClashThatMayBeWithAFileThereBeforeIsAskedAbout(t *testing.T) {
	probe := t.TempDir()
	tree(t, probe, "A.txt")
	if _, err := os.Lstat(filepath.Join(probe, "a.txt")); err == nil {
		t.Skip("this file system takes names that differ in case for one")
	}
	c := useClip(t)
	h := newHarness(t, "x.txt", "a.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 2 })
	// The paste writes A.txt beside the a.txt there before. The a.txt it
	// holds clashes with that one, which the paste cannot tell from one
	// it wrote under another spelling: it asks, whatever the answer for
	// all, and keeps the file there.
	c.holdContents([]outsideItem{
		{name: "A.txt", size: 5},
		{name: "x.txt", size: 1},
		{name: "a.txt", size: 5},
	}, map[string]string{"A.txt": "upper", "x.txt": "X", "a.txt": "lower"})
	h.do(Command{Name: CmdPaste})
	h.until("the clash about x.txt is asked", func() bool { return len(h.a.ops.dialogs) == 1 })
	ask := h.a.ops.dialogs[0].state.(ClashAsk)
	h.answer(ClashAnswered{Op: ask.Op, Choice: ChoiceReplace, All: true})
	h.until("the clash about a.txt is asked", func() bool { return len(h.a.ops.dialogs) == 1 })
	ask, ok := h.a.ops.dialogs[0].state.(ClashAsk)
	if !ok || ask.Name != "a.txt" {
		t.Fatalf("the dialog asks %+v", h.a.ops.dialogs[0].state)
	}
	h.answer(ClashAnswered{Op: ask.Op, Choice: ChoiceSkip})
	h.until("the paste ends", func() bool { return len(h.a.ops.running) == 0 })
	if contents(t, filepath.Join(h.dir, "a.txt")) != "a.txt" {
		t.Fatal("the a.txt there before was replaced")
	}
}

func TestAClashUnderASecondSpellingOfAMergedFolderIsAskedAbout(t *testing.T) {
	probe := t.TempDir()
	tree(t, probe, "A.txt")
	if _, err := os.Lstat(filepath.Join(probe, "a.txt")); err != nil {
		t.Skip("this file system tells names apart by case")
	}
	c := useClip(t)
	h := newHarnessWith(t, onSlashFS, "y.txt", "Docs/")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 2 })
	c.holdContents([]outsideItem{
		{name: "y.txt", size: 1},
		{name: "docs", dir: true, size: -1},
		{name: "docs/x.txt", size: 5},
		{name: "DOCS", dir: true, size: -1},
		{name: "DOCS/x.txt", size: 5},
	}, map[string]string{"y.txt": "Y", "docs/x.txt": "first", "DOCS/x.txt": "secnd"})
	h.do(Command{Name: CmdPaste})
	h.until("the clash about y.txt is asked", func() bool { return len(h.a.ops.dialogs) == 1 })
	ask := h.a.ops.dialogs[0].state.(ClashAsk)
	h.answer(ClashAnswered{Op: ask.Op, Choice: ChoiceReplace, All: true})
	h.until("the clash about x.txt is asked", func() bool { return len(h.a.ops.dialogs) == 1 })
	if ask, ok := h.a.ops.dialogs[0].state.(ClashAsk); !ok || ask.Name != "x.txt" {
		t.Fatalf("the dialog asks %+v", h.a.ops.dialogs[0].state)
	}
}

func TestAClashThatMayBeEitherIsAskedAboutAlone(t *testing.T) {
	probe := t.TempDir()
	tree(t, probe, "A.txt")
	if _, err := os.Lstat(filepath.Join(probe, "a.txt")); err != nil {
		t.Skip("this file system tells names apart by case")
	}
	c := useClip(t)
	h := newHarnessWith(t, onSlashFS, "keep.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	c.holdContents([]outsideItem{
		{name: "A.txt", size: 1},
		{name: "a.txt", size: 1},
		{name: "B.txt", size: 1},
		{name: "b.txt", size: 1},
	}, map[string]string{"A.txt": "A", "a.txt": "a", "B.txt": "B", "b.txt": "b"})
	h.do(Command{Name: CmdPaste})
	for _, name := range []string{"a.txt", "b.txt"} {
		h.until("the clash about "+name+" is asked", func() bool { return len(h.a.ops.dialogs) == 1 })
		ask, ok := h.a.ops.dialogs[0].state.(ClashAsk)
		if !ok || ask.Name != name || ask.CanForAll {
			t.Fatalf("the dialog asks %+v, not about %s alone", h.a.ops.dialogs[0].state, name)
		}
		h.answer(ClashAnswered{Op: ask.Op, Choice: ChoiceSkip, All: true})
	}
	h.until("the paste ends", func() bool { return len(h.a.ops.running) == 0 })
	if contents(t, filepath.Join(h.dir, "A.txt")) != "A" || contents(t, filepath.Join(h.dir, "B.txt")) != "B" {
		t.Fatal("a skipped item replaced the one it clashed with")
	}
}

func TestAnAnswerForAllWithinTheClipboardIsNotOneForAClashThatMayBeEither(t *testing.T) {
	probe := t.TempDir()
	tree(t, probe, "A.txt")
	if _, err := os.Lstat(filepath.Join(probe, "a.txt")); err == nil {
		t.Skip("this file system takes names that differ in case for one")
	}
	c := useClip(t)
	h := newHarness(t, "a.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	c.holdContents([]outsideItem{
		{name: "img.png", size: 1},
		{name: "img.png", size: 1},
		{name: "A.txt", size: 1},
		{name: "a.txt", size: 1},
	}, map[string]string{"img.png": "i", "A.txt": "A", "a.txt": "n"})
	h.do(Command{Name: CmdPaste})
	h.until("the clash between the two img.png is asked", func() bool { return len(h.a.ops.dialogs) == 1 })
	ask := h.a.ops.dialogs[0].state.(ClashAsk)
	h.answer(ClashAnswered{Op: ask.Op, Choice: ChoiceReplace, All: true})
	h.until("the clash about a.txt is asked", func() bool { return len(h.a.ops.dialogs) == 1 })
	if ask, ok := h.a.ops.dialogs[0].state.(ClashAsk); !ok || ask.Name != "a.txt" {
		t.Fatalf("the dialog asks %+v", h.a.ops.dialogs[0].state)
	}
}

func TestALooseKeyTakesWhatAnyFileSystemMayTakeForOneNameForOne(t *testing.T) {
	for _, spellings := range [][]string{
		// The two Unicode forms of a name, in either case.
		{"/d/Caf\u00e9.txt", "/d/Cafe\u0301.txt", "/D/CAF\u00c9.TXT", "/d/CAFE\u0301.txt"},
		{"C:\\d\\Caf\u00e9.txt", "C:\\d\\Cafe\u0301.txt", "c:\\D\\CAF\u00c9.TXT"},
		// Letters composed in one case only.
		{"/d/J\u030c.txt", "/d/\u01f0.txt", "/d/j\u030c.txt"},
		// What lower-casing joins, which the key must not keep apart, and
		// the decomposed form of that same letter.
		{"/d/\u0130.txt", "/d/I.txt", "/d/i.txt", "/d/I\u0307.txt", "/d/i\u0307.txt"},
		// Full case folding.
		{"/d/Stra\u00dfe.txt", "/d/STRASSE.txt"},
		// What Windows drops from the end of a name.
		{"/d/a.txt", "/d/A.txt. "},
		{"C:\\d\\a.txt", "C:\\D\\A.txt. "},
	} {
		for _, s := range spellings[1:] {
			if looseKey(s) != looseKey(spellings[0]) {
				t.Errorf("%q is %q, and %q is %q", s, looseKey(s), spellings[0], looseKey(spellings[0]))
			}
		}
	}
	if looseKey("/d/a.txt") == looseKey("/e/a.txt") || looseKey("/d/a.txt") == looseKey("/d/b.txt") {
		t.Error("two names no file system takes for one have one key")
	}
}

func TestALooseKeyTakesEveryLetterInEitherUnicodeFormForOne(t *testing.T) {
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if !utf8.ValidRune(r) {
			continue
		}
		// Alone, and before a mark a Mac may order differently.
		for _, s := range []string{string(r), string(r) + "\u0323"} {
			if d := norm.NFD.String(s); looseKey(d) != looseKey(s) {
				t.Errorf("%+q is %+q, and its decomposed form is %+q", s, looseKey(s), looseKey(d))
			}
		}
	}
}

func TestAFileThereBeforeInTheOtherUnicodeFormIsKept(t *testing.T) {
	composed, decomposed := "Caf\u00e9.txt", "Cafe\u0301.txt"
	probe := t.TempDir()
	tree(t, probe, composed)
	if _, err := os.Lstat(filepath.Join(probe, decomposed)); err == nil {
		t.Skip("this file system takes the two Unicode forms of a name for one")
	}
	c := useClip(t)
	h := newHarness(t, "x.txt", decomposed)
	h.until("the rows arrive", func() bool { return len(h.shown()) == 2 })
	c.holdContents([]outsideItem{
		{name: "x.txt", size: 1},
		{name: "Caf\u00e9.txt", size: 3},
		{name: decomposed, size: 3},
	}, map[string]string{"x.txt": "X", "Caf\u00e9.txt": "new", decomposed: "new"})
	h.do(Command{Name: CmdPaste})
	h.until("the clash about x.txt is asked", func() bool { return len(h.a.ops.dialogs) == 1 })
	ask := h.a.ops.dialogs[0].state.(ClashAsk)
	h.answer(ClashAnswered{Op: ask.Op, Choice: ChoiceReplace, All: true})
	// The key joins the two forms this file system keeps apart, so the
	// clash with the file there is asked about alone, and Replace all
	// reaches it not.
	h.until("the clash about the file there is asked", func() bool { return len(h.a.ops.dialogs) == 1 })
	ask, ok := h.a.ops.dialogs[0].state.(ClashAsk)
	if !ok || ask.CanForAll {
		t.Fatalf("the dialog asks %+v, not about the file there alone", h.a.ops.dialogs[0].state)
	}
	h.answer(ClashAnswered{Op: ask.Op, Choice: ChoiceSkip})
	h.until("the paste ends", func() bool { return h.exists("Caf\u00e9.txt") && len(h.a.ops.running) == 0 })
	if contents(t, filepath.Join(h.dir, decomposed)) != decomposed {
		t.Fatal("the file there before was replaced")
	}
}

func TestAnAnswerToAClashAskedAboutAloneHoldsForNoOther(t *testing.T) {
	probe := t.TempDir()
	tree(t, probe, "A.txt")
	if _, err := os.Lstat(filepath.Join(probe, "a.txt")); err != nil {
		t.Skip("this file system tells names apart by case")
	}
	c := useClip(t)
	h := newHarnessWith(t, onSlashFS, "y.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	c.holdContents([]outsideItem{
		{name: "A.txt", size: 1},
		{name: "a.txt", size: 1},
		{name: "y.txt", size: 1},
	}, map[string]string{"A.txt": "A", "a.txt": "a", "y.txt": "new"})
	h.do(Command{Name: CmdPaste})
	h.until("the clash about a.txt is asked", func() bool { return len(h.a.ops.dialogs) == 1 })
	ask := h.a.ops.dialogs[0].state.(ClashAsk)
	// A reply that says all, as a client might send though none is on
	// offer.
	h.answer(ClashAnswered{Op: ask.Op, Choice: ChoiceReplace, All: true})
	h.until("the clash about y.txt is asked", func() bool { return len(h.a.ops.dialogs) == 1 })
	if ask, ok := h.a.ops.dialogs[0].state.(ClashAsk); !ok || ask.Name != "y.txt" {
		t.Fatalf("the dialog asks %+v", h.a.ops.dialogs[0].state)
	}
}

func TestAFileWhoseLengthIsNotTheOneTheClipboardSaysIsNotWritten(t *testing.T) {
	for _, tc := range []struct {
		name, holds string
		size        int64
		says        string
	}{
		{"cut short", "abc", 10, "ended after 3 of its 10 bytes"},
		{"too long", "abc", 2, "holds more than the 2 bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := useClip(t)
			h := newHarness(t, "a.txt")
			h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
			c.holdContents([]outsideItem{{name: "a.txt", size: tc.size}}, map[string]string{"a.txt": tc.holds})
			h.do(Command{Name: CmdPaste})
			// Replacing the file there, so a short file would take its place.
			h.until("the clash is asked about", func() bool { return len(h.a.ops.dialogs) == 1 })
			ask := h.a.ops.dialogs[0].state.(ClashAsk)
			h.answer(ClashAnswered{Op: ask.Op, Choice: ChoiceReplace})
			h.until("the failure shows", func() bool { return len(h.a.ops.dialogs) == 1 })
			if e, ok := h.a.ops.dialogs[0].state.(ErrorBox); !ok || !strings.Contains(e.Detail+e.Body, tc.says) {
				t.Fatalf("the paste shows %+v", h.a.ops.dialogs[0].state)
			}
			if contents(t, filepath.Join(h.dir, "a.txt")) != "a.txt" {
				t.Fatal("the file there was replaced by one of the wrong length")
			}
		})
	}
}

func TestARetryAfterTheClipboardChangedPastesNothingNew(t *testing.T) {
	c := useClip(t)
	h := newHarness(t, "keep.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	c.holdContents([]outsideItem{{name: "report.txt", size: 3}}, map[string]string{"report.txt": "old"})
	c.breakFiles("report.txt")
	h.do(Command{Name: CmdPaste})
	h.until("the failure shows", func() bool { return len(h.a.ops.dialogs) == 1 })
	if e, ok := h.a.ops.dialogs[0].state.(ErrorBox); !ok || !e.Retry {
		t.Fatalf("the failure shows %+v", h.a.ops.dialogs[0].state)
	}
	// Another file of the same name is copied before the user retries.
	c.holdContents([]outsideItem{{name: "report.txt", size: 3}}, map[string]string{"report.txt": "new"})
	h.answer(DialogClosed{Retry: true})
	h.until("the retry fails", func() bool { return len(h.a.ops.dialogs) == 1 })
	if e, ok := h.a.ops.dialogs[0].state.(ErrorBox); !ok || !strings.Contains(e.Detail+e.Body, "clipboard changed") {
		t.Fatalf("the retry shows %+v", h.a.ops.dialogs[0].state)
	}
	if h.exists("report.txt") {
		t.Fatal("the retry pasted what the clipboard holds now")
	}
	if e := h.a.ops.dialogs[0].state.(ErrorBox); e.Retry {
		t.Fatal("a retry is offered that would refuse again")
	}
}

func TestAPasteOfWhatTheClipboardNoLongerHoldsPastesNothing(t *testing.T) {
	c := useClip(t)
	h := newHarness(t, "keep.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	c.holdContents([]outsideItem{{name: "report.txt", size: 3}}, map[string]string{"report.txt": "old"})
	read, err := c.files()
	if err != nil {
		t.Fatal(err)
	}
	// Another copy lands between reading the list and pasting the files.
	c.holdContents([]outsideItem{{name: "report.txt", size: 3}}, map[string]string{"report.txt": "new"})
	h.a.pastedSystem(h.a.fs.ID(), h.dir, read, nil)
	h.until("the failure shows", func() bool { return len(h.a.ops.dialogs) == 1 })
	if e, ok := h.a.ops.dialogs[0].state.(ErrorBox); !ok || !strings.Contains(e.Detail+e.Body, "clipboard changed") {
		t.Fatalf("the paste shows %+v", h.a.ops.dialogs[0].state)
	}
	if h.exists("report.txt") {
		t.Fatal("the paste wrote what the clipboard holds now")
	}
}

func TestAPasteStopsWhereTheClipboardChangesBetweenItsFiles(t *testing.T) {
	c := useClip(t)
	h := newHarness(t, "keep.txt")
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	list := []outsideItem{{name: "a.txt", size: 1}, {name: "b.txt", size: 1}}
	c.holdContents(list, map[string]string{"a.txt": "a", "b.txt": "b"})
	// Another copy of files of the same names lands as a.txt is read.
	c.onOpen("a.txt", func() { c.holdContents(list, map[string]string{"a.txt": "A", "b.txt": "B"}) })
	h.do(Command{Name: CmdPaste})
	h.until("the failure shows", func() bool { return len(h.a.ops.dialogs) == 1 })
	if e, ok := h.a.ops.dialogs[0].state.(ErrorBox); !ok || !strings.Contains(e.Body, "Paste again") || e.Retry {
		t.Fatalf("the paste shows %+v", h.a.ops.dialogs[0].state)
	}
	if h.exists("b.txt") {
		t.Fatal("the paste wrote a file of the copy that came between")
	}
}

func TestAChangedClipboardIsSaidSoWhateverTheFileIsCalled(t *testing.T) {
	err := fmt.Errorf("reading Access is denied.txt from the clipboard: %w", errClipboardChanged)
	if why := explainWhy(err); !strings.Contains(why, "clipboard changed") {
		t.Fatalf("a changed clipboard is explained as %q", why)
	}
}

// failingReader hands over n bytes, and err with them.
type failingReader struct {
	n   int
	err error
}

func (f failingReader) Read([]byte) (int, error) { return f.n, f.err }

func TestASizedFileThatFailsSaysWhyBeforeHowLongItRan(t *testing.T) {
	r := &sizedReader{r: failingReader{n: 64 << 10, err: errClipboardChanged}, size: 10}
	if _, err := r.Read(make([]byte, 64<<10)); !errors.Is(err, errClipboardChanged) {
		t.Fatalf("a read that ran past its size as the clipboard changed says %v", err)
	}
}
