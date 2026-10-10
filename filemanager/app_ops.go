package filemanager

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/marrasen/gunim"
)

// showAfter is how long an operation runs before the progress panel
// shows it, so a quick rename does not flash the panel.
const showAfter = 250 * time.Millisecond

// opsState is the running operations, what finished ones did, the
// app's own clipboard of paths, and the dialogs waiting to be shown.
type opsState struct {
	next    int
	running map[int]*opRun
	// cheering holds the operations that went well and show so a moment
	// in the panel.
	cheering map[int]*opRun
	// records holds what finished operations did, by their ID, and undo
	// their IDs in the order Ctrl+Z takes them back.
	records map[int]*finished
	undo    []int
	// clip is what Paste would paste of the window's own file system,
	// and cut whether it was cut. away is the clipboard of another file
	// system, newer than clip, which Paste hands to the program instead.
	clip []string
	cut  bool
	away clipboard
	// limit holds copies to that many bytes a second, for the demo.
	limit float64
	// dialogs are the dialogs to show, the first showing.
	dialogs []*dialog
	shown   int
	tokens  int
	// wg counts the operations' goroutines.
	wg sync.WaitGroup
}

func (o *opsState) init() {
	o.running = map[int]*opRun{}
	o.cheering = map[int]*opRun{}
	o.records = map[int]*finished{}
}

// opRun is an operation running.
type opRun struct {
	id      int
	title   string
	kind    OpKind
	cancel  context.CancelFunc
	visible bool
	last    progress
	meter   speedometer
	// undoes is the finished operation an undo reverses.
	undoes int
}

// finished is what a finished operation did, and how to say it.
type finished struct {
	title string
	rec   record
}

// dialog is a dialog to show, and what to do with its answer.
type dialog struct {
	id     gunim.ID
	view   string
	state  any
	op     int
	answer func(in gunim.Intent)
}

// handleOps takes the intents about file operations and their dialogs.
func (a *app) handleOps(in gunim.Intent) bool {
	switch v := in.(type) {
	case CancelOp:
		if r, ok := a.ops.running[v.ID]; ok {
			r.cancel()
		}
	case UndoOp:
		a.undo(v.ID)
	case ClashAnswered, Confirmed, Prompted, DialogClosed, FavouriteEdited:
		a.answered(in)
	case NoticeAnswered:
		a.uploadAnswered(v)
	case Command:
		return a.opsCommand(v.Name)
	default:
		return false
	}
	return true
}

func (a *app) opsCommand(name string) bool {
	here := a.nav.path
	switch name {
	case CmdCopy, CmdCut:
		paths := a.selectedPaths()
		if len(paths) == 0 {
			return true
		}
		a.ops.clip, a.ops.cut = paths, name == CmdCut
		a.clipChanged()
		verb := "copy"
		if a.ops.cut {
			verb = "move"
		}
		a.patch(Notice{Title: plural(len(paths), "item") + " ready to " + verb, Body: "Go to a folder and paste with Ctrl+V.",
			Kind: "info"})
	case CmdPaste:
		if here == "" {
			return true
		}
		if c := a.ops.away; len(c.paths) > 0 {
			if c.cut {
				// A cut is pasted once: not again, by a window that had
				// not heard it was.
				pasted := !a.hub.clearClip(c)
				a.syncClip()
				if pasted {
					return true
				}
				a.hub.others(a, func(o *app) { o.syncClip() })
			}
			a.transfer(c.fs, c.ps, c.paths, a.fs.ID(), here, c.cut)
			return true
		}
		if len(a.ops.clip) == 0 {
			return true
		}
		srcs := slices.Clone(a.ops.clip)
		if a.ops.cut {
			// Once, here too: a window elsewhere may have pasted it.
			a.hub.mu.Lock()
			kept := a.hub.clips[a.fs.ID()]
			a.hub.mu.Unlock()
			if !kept.cut || !slices.Equal(kept.paths, srcs) || !a.hub.clearClip(kept) {
				a.syncClip()
				return true
			}
			a.syncClip()
			a.hub.others(a, func(o *app) { o.syncClip() })
			a.startOp(job{kind: OpMove, srcs: srcs, dest: here}, "Moving "+a.what(srcs)+" to "+a.ps.placeName(here))
		} else {
			a.startOp(job{kind: OpCopy, srcs: srcs, dest: here}, "Copying "+a.what(srcs)+" to "+a.ps.placeName(here))
		}
	case CmdTrash:
		if a.trash == nil {
			// Without a trash, the key that trashes deletes, and asks first.
			return a.opsCommand(CmdDelete)
		}
		if paths := a.selectedPaths(); len(paths) > 0 {
			a.startOp(job{kind: OpTrash, srcs: paths}, "Moving "+a.what(paths)+" to the trash")
		}
	case CmdDelete:
		paths := a.selectedPaths()
		if len(paths) == 0 {
			return true
		}
		a.confirm(Confirm{Title: "Delete " + a.what(paths) + " for good?",
			Body: "They will not go to the trash, and this cannot be undone.", OK: "Delete"},
			func() { a.startOp(job{kind: OpDelete, srcs: paths}, "Deleting "+a.what(paths)) })
	case CmdRename:
		es := a.selectedEntries()
		if len(es) != 1 {
			return true
		}
		src := a.ps.Join(here, es[0].Name)
		stem := utf8.RuneCountInString(es[0].Name)
		if !es[0].Dir {
			stem = utf8.RuneCountInString(strings.TrimSuffix(es[0].Name, filepath.Ext(es[0].Name)))
		}
		a.prompt(Prompt{Title: "Rename", Text: es[0].Name, OK: "Rename", Stem: stem}, func(name string) {
			a.nav.pick = []string{name}
			a.startOp(job{kind: OpRename, srcs: []string{src}, name: name}, "Renaming "+es[0].Name)
		})
	case CmdNewFolder:
		if here == "" {
			return true
		}
		name, err := freeName(a.fs, a.ps.Join(here, "New folder"))
		if err != nil {
			a.fail("Looking for a free name: " + err.Error())
			return true
		}
		base := a.ps.Base(name)
		a.prompt(Prompt{Title: "New folder", Text: base, OK: "Make", Stem: utf8.RuneCountInString(base)}, func(name string) {
			a.nav.pick = []string{name}
			a.startOp(job{kind: OpNewFolder, dest: here, name: name}, "Making "+name)
		})
	case CmdZip:
		if paths := a.selectedPaths(); len(paths) > 0 && here != "" {
			a.askZip(a.fs.ID(), a.ps, paths, here)
		}
	case CmdExtract:
		paths := a.selectedPaths()
		if len(paths) != 1 || here == "" || !isArchive(a.ps.Base(paths[0])) {
			a.patch(Notice{Title: "Select a zip or tar archive to extract", Kind: "info"})
			return true
		}
		a.askExtract(paths[0])
	case CmdPasteZip:
		if here == "" {
			return true
		}
		// A zip copies: what was cut stays where it is, and on the
		// clipboard.
		if c := a.ops.away; len(c.paths) > 0 {
			a.askZip(c.fs, c.ps, slices.Clone(c.paths), here)
		} else if len(a.ops.clip) > 0 {
			a.askZip(a.fs.ID(), a.ps, slices.Clone(a.ops.clip), here)
		}
	case CmdUndo:
		if n := len(a.ops.undo); n > 0 {
			a.undo(a.ops.undo[n-1])
		}
	default:
		return false
	}
	return true
}

// askZip asks for the name of a zip of the items at paths, on the file
// system of ID fs whose paths are written as ps, and makes it in the
// folder dest of the window's: itself, or through the program from
// another file system.
func (a *app) askZip(fs string, ps PathStyle, paths []string, dest string) {
	name, err := freeName(a.fs, a.ps.Join(dest, zipName(ps, paths)))
	if err != nil {
		a.fail("Looking for a free name: " + err.Error())
		return
	}
	base := a.ps.Base(name)
	stem := utf8.RuneCountInString(strings.TrimSuffix(base, zipExt))
	a.prompt(Prompt{Title: "Create zip", Text: base, OK: "Create", Stem: stem}, func(name string) {
		name = withZipExt(name)
		what := whatIn(ps, paths)
		if fs != a.fs.ID() {
			if a.opts.Transfer == nil {
				a.fail("A zip of items elsewhere cannot be made here.")
				return
			}
			a.startTransfer(Transfer{FromFS: fs, Paths: paths, ToFS: a.fs.ID(), Into: dest, Zip: name}, ps)
			return
		}
		a.nav.pick = []string{name}
		a.startOp(job{kind: OpZip, srcs: paths, dest: dest, name: name}, "Zipping "+what+" to "+name)
	})
}

// what says how many items paths are, or names the one.
func (a *app) what(paths []string) string { return whatIn(a.ps, paths) }

// whatIn says how many items paths, written as ps, are, or names the one.
func whatIn(ps PathStyle, paths []string) string {
	if len(paths) == 1 {
		return ps.Base(paths[0])
	}
	return plural(len(paths), "item")
}

// newOp counts a new operation running, of kind and called title, with
// its progress in the panel once it has run a moment. It returns the
// operation's ID, and the context it runs in, which cancel ends.
func (a *app) newOp(title string, kind OpKind) (id int, ctx context.Context, cancel context.CancelFunc) {
	a.ops.next++
	id = a.ops.next
	ctx, cancel = context.WithCancel(a.ctx)
	a.ops.running[id] = &opRun{id: id, title: title, kind: kind, cancel: cancel}
	time.AfterFunc(showAfter, func() {
		a.post(func() {
			if r, ok := a.ops.running[id]; ok {
				r.visible = true
				a.publishOps()
			}
		})
	})
	return id, ctx, cancel
}

// startOp runs j in the background, with its progress in the panel once
// it has run a moment.
func (a *app) startOp(j job, title string) {
	id, ctx, _ := a.newOp(title, j.kind)
	e := env{
		fs:     a.fs,
		trash:  a.trash,
		ask:    func(ctx context.Context, c clash) (answer, error) { return a.askClash(ctx, id, c) },
		report: func(p progress) { a.post(func() { a.progressed(id, p) }) },
		limit:  a.ops.limit,
	}
	a.ops.wg.Go(func() {
		rec, err := runJob(ctx, j, e)
		a.post(func() { a.finish(id, j, rec, err) })
	})
}

// cheer keeps finished operation id in the panel while it shows it went
// well.
func (a *app) cheer(id int, r *opRun) {
	a.ops.cheering[id] = r
	time.AfterFunc(cheerFor, func() {
		a.post(func() {
			delete(a.ops.cheering, id)
			a.publishOps()
		})
	})
}

// progressed takes how far operation id has got.
func (a *app) progressed(id int, p progress) {
	r, ok := a.ops.running[id]
	if !ok {
		return
	}
	now := time.Now()
	sampled := r.meter.add(now, p.bytes)
	r.last = p
	if r.visible {
		a.patch(r.tick(a.ps, now))
		if sampled && p.bytesTotal > 0 {
			a.patch(OpSpeed{ID: id, Rate: r.meter.rate, Left: r.meter.left(now, p.bytes, p.bytesTotal), File: p.current})
		}
	}
}

// tick is how the panel shows r at now, with paths of style ps.
func (r *opRun) tick(ps PathStyle, now time.Time) OpTick {
	p := r.last
	t := OpTick{ID: r.id}
	switch {
	case p.bytesTotal > 0:
		t.Done = float32(float64(p.bytes) / float64(p.bytesTotal))
		t.Detail = humanBytes(p.bytes) + " of " + humanBytes(p.bytesTotal)
		if speed := r.meter.shown; speed > 0 {
			t.Detail += "  ·  " + humanBytes(int64(speed)) + "/s"
			if left := r.meter.left(now, p.bytes, p.bytesTotal); left >= 1 {
				t.Detail += fmt.Sprintf("  ·  %s left", (time.Duration(left) * time.Second).Round(time.Second))
			}
		}
	case p.itemsTotal > 0:
		t.Done = float32(p.items) / float32(p.itemsTotal)
		t.Detail = count(p.items) + " of " + plural(p.itemsTotal, "item")
	default:
		t.Unknown = true
		t.Detail = "Counting…"
	}
	if p.current != "" {
		t.Detail = ps.Base(p.current) + "  ·  " + t.Detail
	}
	return t
}

func (a *app) publishOps() {
	var s Ops
	ids := make([]int, 0, len(a.ops.running))
	for id := range a.ops.running {
		ids = append(ids, id)
	}
	for id := range a.ops.cheering {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		r, ok := a.ops.running[id]
		if !ok {
			r = a.ops.cheering[id]
		}
		if !r.visible {
			continue
		}
		t := r.tick(a.ps, time.Now())
		if !ok {
			t = OpTick{ID: id, Done: 1, Detail: "Done"}
		}
		s.Ops = append(s.Ops, OpView{ID: id, Title: r.title, Done: t.Done, Unknown: t.Unknown, Detail: t.Detail})
	}
	a.patch(s)
}

// finish takes the end of operation id.
func (a *app) finish(id int, j job, rec record, err error) {
	r, ok := a.ops.running[id]
	if !ok {
		return
	}
	delete(a.ops.running, id)
	r.cancel()
	a.dropDialogs(id)
	if j.kind == OpUndo {
		a.undone(r.undoes, rec)
	}
	if r.visible {
		a.patch(OpDone{ID: id, OK: err == nil})
		if err == nil {
			r.title = a.doneTitle(j, rec)
			a.cheer(id, r)
		}
		a.publishOps()
	}
	done := plural(len(rec.steps), "item")
	keep := undoable(rec, a.trash != nil)
	if keep {
		a.ops.records[id] = &finished{title: r.title, rec: rec}
		a.ops.undo = append(a.ops.undo, id)
	}
	undo := 0
	if keep {
		undo = id
	}
	switch {
	case j.kind == OpTrash && len(rec.steps) == 0 && errors.Is(err, errors.ErrUnsupported):
		// The file system turned out to have no trash: from now on the
		// key that trashes deletes, and this time it asks to.
		a.trashless()
		paths := j.srcs
		a.confirm(Confirm{Title: "Delete " + a.what(paths) + " for good?",
			Body: "There is no trash here, so they cannot come back.", OK: "Delete"},
			func() { a.startOp(job{kind: OpDelete, srcs: paths}, "Deleting "+a.what(paths)) })
	case err == nil && j.kind == OpUndo:
		a.patch(Notice{Title: "Undone", Body: r.title, Kind: "success"})
	case err == nil:
		n := Notice{Title: a.doneTitle(j, rec), Undo: undo, Kind: "success"}
		switch {
		case rec.replaced > 0:
			n.Body = plural(rec.replaced, "file") + " replaced, which undo cannot bring back."
		case j.kind == OpTrash && !restorable(rec):
			// Undo would only say so; the toast says it now.
			n.Undo = 0
			n.Body = "Restore from the system's trash to bring them back."
		}
		a.patch(n)
	case errors.Is(err, context.Canceled):
		a.patch(Notice{Title: "Stopped: " + r.title, Body: done + " done before it stopped.", Undo: undo, Kind: "warning"})
	case errors.Is(err, errStopped):
		a.patch(Notice{Title: "Stopped: " + r.title, Body: done + " done before it stopped.", Undo: undo, Kind: "warning"})
	default:
		body := err.Error()
		if len(rec.steps) > 0 {
			body += "\n\n" + done + " done before this."
		}
		a.showError(ErrorBox{Title: failedTitle(j), Body: body})
		if keep {
			a.patch(Notice{Title: "Partly done: " + r.title, Undo: undo, Kind: "warning"})
		}
	}
	if j.kind == OpRename || j.kind == OpNewFolder || j.kind == OpZip || j.kind == OpExtract {
		if err != nil {
			a.nav.pick = nil
		}
	}
	if (j.kind == OpCopy || j.kind == OpMove || j.kind == OpZip || j.kind == OpExtract) && len(rec.landed) > 0 && a.ps.Same(j.dest, a.nav.path) {
		// What a paste or a drop brought into the folder showing is
		// selected, so the user sees what came.
		a.nav.pick = rec.landed
	}
	a.touched(j)
	a.relist()
}

// restorable reports whether the app can take what a trash record holds
// back out of the trash.
func restorable(rec record) bool {
	for _, s := range rec.steps {
		if s.to == "" {
			return false
		}
	}
	return true
}

// doneTitle says what a finished job did.
func (a *app) doneTitle(j job, rec record) string {
	n := len(rec.steps) + rec.replaced
	switch j.kind {
	case OpCopy:
		return "Copied " + plural(n, "item") + " to " + a.ps.placeName(j.dest)
	case OpMove:
		return "Moved " + plural(n, "item") + " to " + a.ps.placeName(j.dest)
	case OpTrash:
		return "Moved " + plural(n, "item") + " to the trash"
	case OpDelete:
		return "Deleted " + plural(n, "item") + " for good"
	case OpRename:
		return "Renamed to " + j.name
	case OpNewFolder:
		return "Made " + j.name
	case OpZip:
		return "Made " + j.name
	case OpExtract:
		return "Extracted to " + j.name
	case OpUndo:
		return "Undone"
	}
	return "Done"
}

// failedTitle names a job that failed.
func failedTitle(j job) string {
	return map[OpKind]string{
		OpCopy: "The copy stopped", OpMove: "The move stopped", OpTrash: "Moving to the trash stopped",
		OpDelete: "Deleting stopped", OpRename: "The rename failed", OpNewFolder: "The folder was not made",
		OpUndo: "Undo stopped", OpZip: "The zip was not made", OpExtract: "The archive was not extracted",
	}[j.kind]
}

// undo reverses finished operation id. Its record stays while the undo
// runs, off the list Ctrl+Z takes from.
func (a *app) undo(id int) {
	f, ok := a.ops.records[id]
	if !ok || !slices.Contains(a.ops.undo, id) {
		return
	}
	a.ops.undo = slices.DeleteFunc(a.ops.undo, func(v int) bool { return v == id })
	rec := f.rec
	a.startOp(job{kind: OpUndo, undo: &rec}, "Undoing: "+f.title)
	a.ops.running[a.ops.next].undoes = id
}

// undone takes the end of the undo of finished operation id: it drops
// the record, or keeps the steps the undo left, to undo later.
func (a *app) undone(id int, rec record) {
	f, ok := a.ops.records[id]
	if !ok {
		return
	}
	if len(rec.left) == 0 {
		delete(a.ops.records, id)
		return
	}
	f.rec.steps = rec.left
	a.ops.undo = append(a.ops.undo, id)
}

// askClash asks the user about clash c of operation op, and waits for the
// answer or the end of ctx.
func (a *app) askClash(ctx context.Context, op int, c clash) (answer, error) {
	reply := make(chan ClashAnswered, 1)
	a.post(func() {
		ask := ClashAsk{Op: op, Name: a.ps.Base(c.dst), Where: a.ps.placeName(a.ps.Dir(c.dst)),
			New: c.from, Old: describe(a.fs, c.dst), SameKind: c.sameKind, CanForAll: true}
		if ask.New == "" {
			ask.New = describe(a.fs, c.src)
		}
		a.showDialog(&dialog{view: "clash", state: ask, op: op, answer: func(in gunim.Intent) {
			if v, ok := in.(ClashAnswered); ok {
				reply <- v
				return
			}
			reply <- ClashAnswered{Stop: true}
		}})
	})
	select {
	case v := <-reply:
		if v.Stop {
			return answer{}, errStopped
		}
		return answer{choice: choice(v.Choice), all: v.All}, nil
	case <-ctx.Done():
		return answer{}, ctx.Err()
	}
}

// describe says what is at path on fsys, for a clash dialog.
func describe(fsys FS, path string) string {
	e, err := statEntry(fsys, path)
	if err != nil {
		return "cannot be read: " + err.Error()
	}
	s := e.Type
	if !e.Dir {
		s += ", " + humanBytes(e.Size)
	}
	return s + ", modified " + e.Mod.Format("2006-01-02 15:04")
}

// confirm asks c, and runs yes once the user agrees.
func (a *app) confirm(c Confirm, yes func()) {
	a.ask(c, func(v Confirmed) {
		if v.OK {
			yes()
		}
	})
}

// ask asks c, and hands got the answer, which is neither OK nor Alt
// where the user cancelled.
func (a *app) ask(c Confirm, got func(v Confirmed)) {
	a.ops.tokens++
	c.Token = a.ops.tokens
	a.showDialog(&dialog{view: "confirm", state: c, answer: func(in gunim.Intent) {
		v, _ := in.(Confirmed)
		got(v)
	}})
}

// prompt asks for a name, and runs got with it once the user gives one.
func (a *app) prompt(p Prompt, got func(name string)) {
	a.ops.tokens++
	p.Token, p.Paths = a.ops.tokens, a.ps
	a.showDialog(&dialog{view: "prompt", state: p, answer: func(in gunim.Intent) {
		if v, ok := in.(Prompted); ok && v.OK {
			got(v.Text)
		}
	}})
}

// showError says an operation failed, in a dialog, and to Options.Log.
func (a *app) showError(e ErrorBox) {
	a.logShown(e)
	a.showDialog(&dialog{view: "error", state: e, answer: func(gunim.Intent) {}})
}

// showDialog queues d, and shows it once the dialogs before it are
// answered.
func (a *app) showDialog(d *dialog) {
	a.ops.shown++
	d.id = a.ids.dialog(a.ops.shown)
	a.ops.dialogs = append(a.ops.dialogs, d)
	if len(a.ops.dialogs) == 1 {
		a.mountDialog(d)
	}
}

func (a *app) mountDialog(d *dialog) {
	// The picture viewer goes over the browser, and would hide the
	// dialog, which asks what matters more.
	a.closeViewer()
	// The dialog shows over the browser only, which in a pane leaves the
	// rest of the window working. There it takes the keyboard only from
	// the pane, as it comes; a window's takes it at once.
	a.send(a.c.Mount(a.ids.browser(), d.id, d.view, d.state))
	if a.pane == nil {
		a.send(a.c.Focus(d.id))
	}
}

// answered hands a dialog's answer to the dialog showing, and shows the
// next. An answer meant for another dialog, as one that arrives after
// its operation ended, is dropped.
func (a *app) answered(in gunim.Intent) {
	if len(a.ops.dialogs) == 0 || !answers(in, a.ops.dialogs[0].state) {
		return
	}
	d := a.ops.dialogs[0]
	a.ops.dialogs = a.ops.dialogs[1:]
	d.answer(in)
	if len(a.ops.dialogs) > 0 {
		a.mountDialog(a.ops.dialogs[0])
		return
	}
	a.patch(FocusListing{})
}

// answers reports whether in is an answer to the dialog with state: of
// its kind, and with its operation or token.
func answers(in gunim.Intent, state any) bool {
	switch v := in.(type) {
	case ClashAnswered:
		s, ok := state.(ClashAsk)
		return ok && s.Op == v.Op
	case Confirmed:
		s, ok := state.(Confirm)
		return ok && s.Token == v.Token
	case Prompted:
		s, ok := state.(Prompt)
		return ok && s.Token == v.Token
	case PropsApplied:
		s, ok := state.(Props)
		return ok && s.Token == v.Token
	case FavouriteEdited:
		s, ok := state.(FavouriteEdit)
		return ok && s.Token == v.Token
	case DialogClosed:
		switch state.(type) {
		case ErrorBox, Props:
			return true
		}
	}
	return false
}

// dropDialogs takes away the dialogs of operation op, which has ended.
func (a *app) dropDialogs(op int) {
	if len(a.ops.dialogs) == 0 {
		return
	}
	first := a.ops.dialogs[0]
	a.ops.dialogs = slices.DeleteFunc(a.ops.dialogs, func(d *dialog) bool { return d.op == op && op != 0 })
	if first.op == op && op != 0 {
		a.send(a.c.Unmount(first.id))
		if len(a.ops.dialogs) > 0 {
			a.mountDialog(a.ops.dialogs[0])
		} else {
			a.patch(FocusListing{})
		}
	}
}
