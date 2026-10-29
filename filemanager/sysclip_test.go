package filemanager

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
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
	emptied bool
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

func (c *fakeClip) outside(fn func(outsideFiles) error) error {
	c.mu.Lock()
	of := fakeFiles{list: slices.Clone(c.list), data: c.data, broken: c.broken}
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
	list   []outsideItem
	data   map[string]string
	broken map[string]bool
}

func (f fakeFiles) items() []outsideItem { return f.list }

func (f fakeFiles) open(i int) (io.ReadCloser, error) {
	if f.broken[f.list[i].name] {
		return nil, errors.New("the stream broke")
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
	c.holdPaths(true, filepath.Join(h.root, "gone.txt"))
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
		return slices.ContainsFunc(got(), func(n Notice) bool { return strings.Contains(n.Title, "moved already") })
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
	c.holdPaths(true, filepath.Join(other, "cut.txt"), filepath.Join(other, "gone.txt"))
	h.do(Command{Name: CmdPaste})
	h.until("the clash is asked about", func() bool { return len(h.a.ops.dialogs) == 1 })
	ask := h.a.ops.dialogs[0].state.(ClashAsk)
	h.answer(ClashAnswered{Op: ask.Op, Choice: ChoiceSkip})
	h.until("the move fails on what is gone", func() bool {
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
		return slices.ContainsFunc(got(), func(n Notice) bool { return strings.Contains(n.Title, "moved already") })
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

// lstatCounter counts the Lstat calls made of the file system it holds.
type lstatCounter struct {
	FS
	lstats int
}

func (c *lstatCounter) Lstat(p string) (fs.FileInfo, error) {
	c.lstats++
	return c.FS.Lstat(p)
}

func TestAPasteLooksOnlyWhereANameRepeats(t *testing.T) {
	items := make([]outsideItem, 1, 51)
	items[0] = outsideItem{name: "X", dir: true, size: -1}
	data := map[string]string{}
	for i := range 50 {
		n := fmt.Sprintf("X/%d.txt", i)
		items = append(items, outsideItem{name: n, size: 1})
		data[n] = "x"
	}
	c := &fakeClip{}
	c.holdContents(items, data)
	fsys := &lstatCounter{FS: LocalFS()}
	if _, err := runJob(context.Background(), job{kind: OpCopy, srcs: []string{"X"}, dest: t.TempDir(), outside: true},
		env{fs: fsys, outside: c.outside}); err != nil {
		t.Fatal(err)
	}
	if fsys.lstats > 5 {
		t.Fatalf("a paste of 50 files into a folder it made looked %d times", fsys.lstats)
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
