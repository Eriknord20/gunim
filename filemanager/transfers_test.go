package filemanager

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/widget"
)

// transfers keeps the transfers a window hands its program.
type transfers struct {
	mu  sync.Mutex
	got []Transfer
}

func (ts *transfers) take(_ context.Context, _ *Window, t Transfer, _ *TransferProgress) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.got = append(ts.got, t)
	return nil
}

func (ts *transfers) list() []Transfer {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return slices.Clone(ts.got)
}

// count waits until h's program has had n transfers, and returns them.
func (ts *transfers) count(h *harness, n int) []Transfer {
	h.t.Helper()
	h.until("the program has the transfers", func() bool { return len(ts.list()) >= n })
	h.frames(5)
	got := ts.list()
	if len(got) != n {
		h.t.Fatalf("the program has %d transfers, want %d: %+v", len(got), n, got)
	}
	return got
}

// dropHarness opens a window on a folder of spec, as newDndHarness does,
// with the options set changes.
func dropHarness(t *testing.T, set func(o *Options), spec ...string) *harness {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "dir")
	tree(t, dir, spec...)
	return openHarnessWith(t, nil, root, dir, set)
}

func sameTransfer(a, b Transfer) bool {
	return a.FromFS == b.FromFS && slices.Equal(a.Paths, b.Paths) && a.ToFS == b.ToFS && a.Into == b.Into && a.Move == b.Move
}

func TestDropPlansFromAnotherFileSystemCopyOrMove(t *testing.T) {
	d := FileDrag{Paths: []string{"/b/x.txt"}, FS: server, Volume: "one"}
	for _, c := range []struct {
		mods input.Mods
		text string
		eff  widget.DropEffect
	}{
		{0, "Copy to b", widget.DropCopy},
		{input.ModControl, "Copy to b", widget.DropCopy},
		{input.ModShift, "Move to b", widget.DropMove},
	} {
		// The path is in the folder by its name, but on another file
		// system, and on the same volume by its name.
		plan, hint, ok := dropPlan(SlashPaths, "", true, d, "/b", "one", "", c.mods)
		if !ok || hint.Text != c.text || hint.Effect != c.eff {
			t.Fatalf("with %v the drop says %q, effect %v, ok %v", c.mods, hint.Text, hint.Effect, ok)
		}
		if plan.FS != server || plan.Into != "/b" || plan.Copy != (c.eff == widget.DropCopy) || !slices.Equal(plan.Paths, d.Paths) {
			t.Fatalf("with %v the plan is %+v", c.mods, plan)
		}
	}
	if _, hint, ok := dropPlan(SlashPaths, "", true, d, "/b", "", "denied", 0); ok || hint.Text != "Cannot read b" {
		t.Fatalf("a folder whose volume cannot be read says %q", hint.Text)
	}
	if _, hint, ok := dropPlan(SlashPaths, "", false, d, "/b", "", "", 0); ok || hint.Text != "Cannot drop from another file system" {
		t.Fatalf("without transfers the drop says %q", hint.Text)
	}
}

func TestADropFromAnotherFileSystemGoesToTheProgram(t *testing.T) {
	var ts transfers
	h := dropHarness(t, func(o *Options) { o.Transfer = ts.take }, "sub/", "here.txt")
	h.until("the window knows it can transfer", func() bool { return h.b.shell.Transfers })
	drag := FileDrag{Paths: []string{"/srv/x.txt", "/srv/y.txt"}, Dirs: []bool{false, false}, FS: server}
	h.w.Input(input.Drop{Pos: h.point("sub"), Data: drag})
	got := ts.count(h, 1)
	want := Transfer{FromFS: server, Paths: drag.Paths, ToFS: "", Into: filepath.Join(h.dir, "sub")}
	if !sameTransfer(got[0], want) {
		t.Fatalf("the drop hands over %+v, want %+v", got[0], want)
	}
	h.w.Input(input.Drop{Pos: h.point("here.txt"), Data: drag, Mods: input.ModShift})
	got = ts.count(h, 2)
	want.Into, want.Move = h.dir, true
	if !sameTransfer(got[1], want) {
		t.Fatalf("the drop with Shift hands over %+v, want %+v", got[1], want)
	}
	h.until("the transfers end", func() bool { return len(h.a.ops.running) == 0 })
	if len(h.a.ops.undo) != 0 {
		t.Fatal("a transfer may be undone")
	}
}

func TestFilesFromAnotherProgramGoToTheProgramOnAFileSystemElsewhere(t *testing.T) {
	var ts transfers
	h := dropHarness(t, func(o *Options) {
		onBareFS(o)
		o.Transfer = ts.take
	}, "sub/")
	h.until("the window knows it can transfer", func() bool { return h.b.shell.Transfers })
	x, y := filepath.Join(h.root, "a", "x.txt"), filepath.Join(h.root, "b", "y.txt")
	h.w.Input(input.Drop{Pos: h.point("sub"), Paths: []string{x, y}})
	got := ts.count(h, 2)
	into := filepath.Join(h.dir, "sub")
	for _, p := range []string{x, y} {
		// One transfer for each folder the files are in, which run side
		// by side.
		want := Transfer{FromFS: "", Paths: []string{p}, ToFS: "elsewhere", Into: into}
		if !slices.ContainsFunc(got, func(t Transfer) bool { return sameTransfer(t, want) }) {
			t.Fatalf("no transfer is %+v: %+v", want, got)
		}
	}
}

func TestADropFromAnotherFileSystemIsRefusedWithoutTransfers(t *testing.T) {
	h := dropHarness(t, onBareFS, "sub/")
	h.w.Input(input.Drop{Pos: h.point("sub"), Paths: []string{filepath.Join(h.root, "x.txt")}})
	h.frames(10)
	if h.b.shell.Transfers || len(h.a.ops.running) != 0 || h.exists("sub/x.txt") {
		t.Fatal("a drop from another file system was taken without transfers")
	}
}

// twoFileSystems opens a window on the computer's own file system and
// one elsewhere, in one hub, each with its transfers kept in ts.
func twoFileSystems(t *testing.T, ts *transfers) (local, other *harness) {
	t.Helper()
	root := t.TempDir()
	l, o := filepath.Join(root, "local"), filepath.Join(root, "other")
	tree(t, l, "a.txt")
	tree(t, o, "b.txt", "sub/")
	hub := &Hub{}
	local = openHarnessWith(t, hub, root, l, func(op *Options) { op.Transfer = ts.take })
	other = openHarnessWith(t, hub, root, o, func(op *Options) {
		onBareFS(op)
		op.Transfer = ts.take
	})
	return local, other
}

func TestPasteFromAnotherFileSystemGoesToTheProgram(t *testing.T) {
	var ts transfers
	local, other := twoFileSystems(t, &ts)
	a := filepath.Join(local.dir, "a.txt")
	local.choose("a.txt")
	local.do(Command{Name: CmdCopy})
	other.until("the window elsewhere offers Paste", func() bool { return other.b.dnd.clip.Count == 1 && !other.b.dnd.clip.Cut })
	other.do(Command{Name: CmdPaste})
	got := ts.count(other, 1)
	want := Transfer{FromFS: "", Paths: []string{a}, ToFS: "elsewhere", Into: other.dir}
	if !sameTransfer(got[0], want) {
		t.Fatalf("the paste hands over %+v, want %+v", got[0], want)
	}
	if other.b.dnd.clip.Count != 1 || len(local.a.ops.clip) != 1 {
		t.Fatal("pasting a copy emptied the clipboard")
	}
	other.until("what came is selected", func() bool { return other.a.nav.sel["a.txt"] })

	local.do(Command{Name: CmdCut})
	other.until("the window elsewhere offers the cut", func() bool { return other.b.dnd.clip.Count == 1 && other.b.dnd.clip.Cut })
	other.do(Command{Name: CmdPaste})
	got = ts.count(other, 2)
	want.Move = true
	if !sameTransfer(got[1], want) {
		t.Fatalf("the paste of the cut hands over %+v, want %+v", got[1], want)
	}
	other.until("the window elsewhere has nothing to paste", func() bool { return other.b.dnd.clip.Count == 0 })
	local.until("the cut is gone where it was cut", func() bool { return len(local.a.ops.clip) == 0 && local.b.dnd.clip.Count == 0 })
	other.do(Command{Name: CmdPaste})
	// A transfer runs on a goroutine of its own: time for a wrong one to
	// come.
	other.frames(2)
	time.Sleep(100 * time.Millisecond)
	if len(ts.list()) != 2 || len(other.a.ops.undo) != 0 || len(local.a.ops.undo) != 0 {
		t.Fatal("the cut was pasted twice, or a transfer may be undone")
	}
}

func TestTheMostRecentClipboardIsPasted(t *testing.T) {
	var ts transfers
	local, other := twoFileSystems(t, &ts)
	other.choose("b.txt")
	other.do(Command{Name: CmdCopy})
	local.choose("a.txt")
	local.do(Command{Name: CmdCopy})
	other.until("the window elsewhere offers the newer copy", func() bool { return len(other.a.ops.away.paths) == 1 })
	other.do(Command{Name: CmdPaste})
	got := ts.count(other, 1)
	if want := []string{filepath.Join(local.dir, "a.txt")}; !slices.Equal(got[0].Paths, want) {
		t.Fatalf("the paste hands over %v, want %v", got[0].Paths, want)
	}
	// The local window still pastes its own copy, the most recent.
	if len(local.a.ops.away.paths) != 0 || len(local.a.ops.clip) != 1 {
		t.Fatalf("the local window holds %v and %v away", local.a.ops.clip, local.a.ops.away.paths)
	}
	// The transfer ends on a goroutine of its own, and selects what came,
	// in place of a selection made before it ends.
	other.until("what came is selected", func() bool { return other.a.nav.sel["a.txt"] })

	// A newer copy on the window's own file system pastes within it.
	other.choose("b.txt")
	other.do(Command{Name: CmdCopy})
	other.do(Navigate{Path: filepath.Join(other.dir, "sub")})
	other.until("the window elsewhere shows sub", func() bool {
		return !other.a.nav.loading && SystemPaths.Same(other.a.nav.path, filepath.Join(other.dir, "sub"))
	})
	other.do(Command{Name: CmdPaste})
	other.until("the paste copies within the file system", func() bool { return other.exists("sub/b.txt") })
	other.idle()
	if len(ts.list()) != 1 {
		t.Fatalf("the paste within the file system went to the program: %+v", ts.list())
	}
	local.until("the local window offers the copy elsewhere", func() bool { return len(local.a.ops.away.paths) == 1 })
}

func TestTransfersPassOverTheWire(t *testing.T) {
	err := gunim.CheckWire(
		Shell{FS: server, Paths: SlashPaths, Transfers: true, Where: "Picard"},
		DropFiles{Paths: []string{"/a/x"}, Into: "/b", FS: server},
	)
	if err != nil {
		t.Fatal(err)
	}
}

// A cut is cleared once: a window that had not heard it was pasted
// finds it gone, and does not paste it again.
func TestACutIsClearedOnce(t *testing.T) {
	h := &Hub{}
	h.seq++
	c := clipboard{fs: "server", paths: []string{"/a"}, cut: true, seq: h.seq}
	h.clips = map[string]clipboard{"server": c}
	h.last = c
	if !h.clearClip(c) {
		t.Fatal("the cut was not there to clear")
	}
	if h.clearClip(c) {
		t.Fatal("the cut was cleared twice")
	}
	if len(h.last.paths) != 0 || len(h.clips["server"].paths) != 0 {
		t.Fatalf("the cut is still kept: %+v, %+v", h.last, h.clips)
	}
}

// held is a transfer the program holds until the test lets it end.
type held struct {
	ctx   context.Context
	t     Transfer
	p     *TransferProgress
	reply chan error
}

// holder is a program whose transfers wait for the test.
type holder struct{ calls chan *held }

func newHolder() *holder { return &holder{calls: make(chan *held, 8)} }

func (ho *holder) transfer(ctx context.Context, _ *Window, t Transfer, p *TransferProgress) error {
	c := &held{ctx: ctx, t: t, p: p, reply: make(chan error, 1)}
	ho.calls <- c
	select {
	case err := <-c.reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// next waits until h's program holds a transfer, and returns it.
func (ho *holder) next(h *harness) *held {
	h.t.Helper()
	var c *held
	h.until("the program has a transfer", func() bool {
		select {
		case c = <-ho.calls:
			return true
		default:
			return false
		}
	})
	return c
}

// notices keeps the notices h's window shows, in place of its toasts.
func notices(h *harness) func() []Notice {
	var mu sync.Mutex
	var got []Notice
	gunim.RegisterPatch(h.w, "browser", func(_ *browser, n Notice, _ *gunim.UI) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, n)
	})
	return func() []Notice {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(got)
	}
}

// heldDrop opens a window whose program holds its transfers, drops x.txt
// of the server on its folder sub, and returns the transfer held.
func heldDrop(t *testing.T, mods input.Mods) (*harness, *held, func() []Notice) {
	t.Helper()
	ho := newHolder()
	h := dropHarness(t, func(o *Options) { o.Transfer = ho.transfer }, "sub/a.txt")
	got := notices(h)
	h.until("the window knows it can transfer", func() bool { return h.b.shell.Transfers })
	drag := FileDrag{Paths: []string{"/srv/x.txt"}, Dirs: []bool{false}, FS: server}
	h.w.Input(input.Drop{Pos: h.point("sub"), Data: drag, Mods: mods})
	return h, ho.next(h), got
}

func TestATransferRunsAsAnOperationWithItsProgress(t *testing.T) {
	h, c, got := heldDrop(t, 0)
	if len(h.a.ops.running) != 1 {
		t.Fatalf("%d operations run, want the transfer", len(h.a.ops.running))
	}
	id := h.a.ops.next
	r := h.a.ops.running[id]
	if r.title != "Copying x.txt to sub" {
		t.Fatalf("the transfer is called %q", r.title)
	}
	c.p.Report(50, 100, 0, 1, "/srv/x.txt")
	h.until("the panel shows the transfer", func() bool {
		_, shown := widget.RowOf[*opRow](h.b.ops.list, widget.Key(strconv.Itoa(id)))
		return shown && r.visible && r.last.bytes == 50
	})
	if tick := r.tick(h.a.ps, time.Now()); tick.Done != 0.5 || !strings.HasPrefix(tick.Detail, "x.txt  ·  50 bytes of 100 bytes") {
		t.Fatalf("the transfer shows %+v", tick)
	}
	h.do(Command{Name: CmdCloseApp})
	h.until("closing asks to stop the transfer", func() bool { return len(h.a.ops.dialogs) == 1 })
	if c, ok := h.a.ops.dialogs[0].state.(Confirm); !ok || c.Title != "Stop 1 operation and close?" {
		t.Fatalf("closing asks %+v", h.a.ops.dialogs[0].state)
	}
	h.answer(Confirmed{Token: h.a.ops.tokens, OK: false})

	h.do(CancelOp{ID: id})
	h.until("the transfer stops", func() bool { return len(h.a.ops.running) == 0 })
	if c.ctx.Err() == nil {
		t.Fatal("stopping the transfer did not end its context")
	}
	h.until("a notice says it stopped", func() bool { return len(got()) == 1 })
	if n := got()[0]; n.Title != "Stopped: Copying x.txt to sub" || n.Kind != "warning" || n.Undo != 0 {
		t.Fatalf("the notice is %+v", n)
	}
}

func TestATransferThatGoesWellSaysSoWithoutUndo(t *testing.T) {
	h, c, got := heldDrop(t, input.ModShift)
	if r := h.a.ops.running[h.a.ops.next]; r == nil || r.title != "Moving x.txt to sub" {
		t.Fatalf("the transfer runs as %+v", r)
	}
	// The listing reads the folder again once the transfer is done, not
	// at the next poll.
	tree(t, h.dir, "new.txt")
	c.reply <- nil
	h.until("the transfer ends", func() bool { return len(h.a.ops.running) == 0 })
	h.until("a notice says it went well", func() bool { return len(got()) == 1 })
	if n := got()[0]; n.Title != "Moved x.txt to sub" || n.Kind != "success" || n.Undo != 0 {
		t.Fatalf("the notice is %+v", n)
	}
	h.until("the folder is read again", func() bool { return slices.Contains(h.shown(), "new.txt") })
	if len(h.a.ops.undo) != 0 {
		t.Fatal("the transfer may be undone")
	}
}

func TestATransferThatFailsSaysWhy(t *testing.T) {
	h, c, got := heldDrop(t, 0)
	c.reply <- errors.New("the server went away")
	h.until("the failure shows", func() bool { return len(h.a.ops.dialogs) == 1 })
	e, ok := h.a.ops.dialogs[0].state.(ErrorBox)
	if !ok || e.Title != "The copy stopped" || e.Body != "the server went away" {
		t.Fatalf("the failure shows %+v", h.a.ops.dialogs[0].state)
	}
	if len(h.a.ops.running) != 0 || len(got()) != 0 {
		t.Fatalf("the transfer runs on, or says %+v", got())
	}
}

// clashed is what a transfer's Clash returned.
type clashed struct {
	choice Choice
	all    bool
	err    error
}

func TestATransferAsksAboutAClashThroughTheWindow(t *testing.T) {
	h, c, _ := heldDrop(t, 0)
	id := h.a.ops.next
	dst := filepath.Join(h.dir, "sub", "a.txt")
	ask := func() chan clashed {
		res := make(chan clashed, 1)
		go func() {
			ch, all, err := c.p.Clash(c.ctx, dst, "12 KB, modified 2 Oct 2026", true)
			res <- clashed{ch, all, err}
		}()
		h.until("the clash is asked about", func() bool { return len(h.a.ops.dialogs) == 1 })
		return res
	}
	res := ask()
	a, ok := h.a.ops.dialogs[0].state.(ClashAsk)
	want := ClashAsk{Op: id, Name: "a.txt", Where: "sub", New: "12 KB, modified 2 Oct 2026", Old: describe(h.a.fs, dst),
		SameKind: true, CanForAll: true}
	if !ok || a != want {
		t.Fatalf("the dialog asks %+v, want %+v", h.a.ops.dialogs[0].state, want)
	}
	h.answer(ClashAnswered{Op: id, Choice: ChoiceKeepBoth, All: true})
	if r := <-res; r != (clashed{ChoiceKeepBoth, true, nil}) {
		t.Fatalf("Clash returned %+v", r)
	}

	res = ask()
	h.answer(ClashAnswered{Op: id, Stop: true})
	if r := <-res; r.err == nil {
		t.Fatal("Stop at the dialog returned no error")
	}
	h.until("the transfer stops", func() bool { return len(h.a.ops.running) == 0 })
	if c.ctx.Err() == nil {
		t.Fatal("Stop at the dialog did not end the transfer's context")
	}
}

func TestATransferProgressOfNoWindowIsSafe(t *testing.T) {
	for _, p := range []*TransferProgress{nil, {}} {
		p.Report(1, 2, 3, 4, "x")
		if _, _, err := p.Clash(context.Background(), "/a", "", true); err == nil {
			t.Fatal("a progress of no window answered a clash")
		}
	}
}

func TestTheTitleNamesTheFileSystemFirst(t *testing.T) {
	for _, c := range []struct {
		where, folder, want string
	}{
		{"", "", "Files"},
		{"Picard", "", "Picard — Files"},
		{"", "Documents", "Documents — Files"},
		{"Picard", "Documents", "Picard — Documents — Files"},
	} {
		b := &browser{shell: Shell{Where: c.where}}
		tb := newTitleBar(b)
		tb.setShell(b.shell, nil)
		if c.folder != "" {
			tb.setListing(Listing{Title: c.folder}, nil)
		}
		if tb.head.name.Title != c.want {
			t.Fatalf("the title is %q, want %q", tb.head.name.Title, c.want)
		}
	}
}

func TestTheFileSystemsNameFollowsRefresh(t *testing.T) {
	var mu sync.Mutex
	name := "This computer"
	h := dropHarness(t, func(o *Options) {
		o.FSName = func(fs string) string {
			mu.Lock()
			defer mu.Unlock()
			if fs != "" {
				return ""
			}
			return name
		}
	})
	h.until("the title names the file system", func() bool { return h.b.title.head.name.Title == "This computer — dir — Files" })
	mu.Lock()
	name = "Workstation"
	mu.Unlock()
	h.a.hub.Refresh()
	h.until("the title takes the new name", func() bool { return h.b.title.head.name.Title == "Workstation — dir — Files" })
	mu.Lock()
	name = ""
	mu.Unlock()
	h.a.hub.Refresh()
	h.until("the title drops the name", func() bool { return h.b.title.head.name.Title == "dir — Files" })
}

func TestTheFileSystemsNameFollowsShow(t *testing.T) {
	h := dropHarness(t, func(o *Options) {
		o.FSName = func(fs string) string { return map[string]string{"": "This computer", "elsewhere": "Picard"}[fs] }
	})
	h.until("the title names the file system", func() bool { return h.b.title.head.name.Title == "This computer — dir — Files" })
	h.a.showFS(bareFS{LocalFS()}, h.dir)
	h.until("the title names the other", func() bool { return h.b.title.head.name.Title == "Picard — dir — Files" })
}
