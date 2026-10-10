package filemanager

import (
	"context"
	"errors"
	"slices"
	"strings"
)

// TransferProgress is how a transfer reports to the window it runs in.
// Its methods may be called from any goroutine. A zero or nil
// TransferProgress is of no window, so a Transfer can be tried without
// one: Report does nothing, and Clash returns an error.
type TransferProgress struct {
	a *app
	// id is the operation the transfer runs as, and cancel stops it.
	id     int
	cancel context.CancelFunc
}

// errNoWindow is what Clash returns for a TransferProgress of no window.
var errNoWindow = errors.New("files: no window to ask about a name that clashes")

// Report says how far the transfer has got: bytes of bytesTotal and
// items of itemsTotal, a total zero while it is not known, and the item
// it is at, a path on either file system or a name.
func (p *TransferProgress) Report(bytes, bytesTotal int64, items, itemsTotal int, current string) {
	if p == nil || p.a == nil {
		return
	}
	pr := progress{items: items, itemsTotal: itemsTotal, bytes: bytes, bytesTotal: bytesTotal, current: lastName(current)}
	a, id := p.a, p.id
	a.post(func() { a.progressed(id, pr) })
}

// Clash asks the user what to do about an item going to dst, a path on
// the file system the items go to, where one of its name already is. coming
// describes the item on its way, as the dialog shows it (for example
// "12 KB, modified 2 Oct 2026"), and sameKind says both are folders or
// both are not, which Replace needs. It returns the answer, and whether
// it is for every later clash of the transfer; an error when the user
// stops the transfer, or ctx ends.
func (p *TransferProgress) Clash(ctx context.Context, dst, coming string, sameKind bool) (Choice, bool, error) {
	if p == nil || p.a == nil {
		return 0, false, errNoWindow
	}
	if coming == "" {
		coming = "on its way here"
	}
	ans, err := p.a.askClash(ctx, p.id, clash{dst: dst, sameKind: sameKind, from: coming})
	if err != nil {
		if errors.Is(err, errStopped) {
			// Stop at the dialog stops the whole transfer.
			p.cancel()
			err = errTransferStopped
		}
		return 0, false, err
	}
	return Choice(ans.choice), ans.all, nil
}

// errTransferStopped is what Clash returns when the user stops the
// transfer at its dialog.
var errTransferStopped = errors.New("files: the transfer was stopped")

// lastName is the last name of path, which may be written with either
// kind of slash, as it may be of another file system than the window's.
func lastName(path string) string {
	path = strings.TrimRight(path, `/\`)
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[i+1:]
	}
	return path
}

// transfer has the program copy the items at paths on the file system of
// ID from, which writes them as ps, into the folder into on the file
// system of ID to, or move them there with move: one transfer for each
// folder they are in, each an operation of the window.
func (a *app) transfer(from string, ps PathStyle, paths []string, to, into string, move bool) {
	var ts []Transfer
	for _, p := range paths {
		i := slices.IndexFunc(ts, func(t Transfer) bool { return ps.Same(ps.Dir(t.Paths[0]), ps.Dir(p)) })
		if i < 0 {
			ts = append(ts, Transfer{FromFS: from, ToFS: to, Into: into, Move: move})
			i = len(ts) - 1
		}
		ts[i].Paths = append(ts[i].Paths, p)
	}
	for _, t := range ts {
		a.startTransfer(t, ps)
	}
}

// placeLabel names the folder at path on the file system of ID fs, as
// the window calls it: by the place or the favourite it is, where it is
// one on another file system, or else by its own name.
func (a *app) placeLabel(fs, path string) string {
	if fs == a.fs.ID() {
		return a.ps.placeName(path)
	}
	for _, p := range a.places {
		if p.FS == fs && p.Path == path && p.Name != "" {
			return p.Name
		}
	}
	return a.favName(fs, path)
}

// startTransfer runs t through the program in the background, as an
// operation of the window, with paths of the file system it comes from
// written as ps.
func (a *app) startTransfer(t Transfer, ps PathStyle) {
	what, where := whatIn(ps, t.Paths), a.placeLabel(t.ToFS, t.Into)
	title, done, kind := "Copying "+what+" to "+where, "Copied "+what+" to "+where, OpCopy
	switch {
	case t.Zip != "":
		title, done, kind = "Zipping "+what+" to "+t.Zip, "Made "+t.Zip, OpZip
	case t.Move:
		title, done, kind = "Moving "+what+" to "+where, "Moved "+what+" to "+where, OpMove
	}
	id, ctx, cancel := a.newOp(title, kind)
	p := &TransferProgress{a: a, id: id, cancel: cancel}
	do, w := a.opts.Transfer, a.win
	a.ops.wg.Go(func() {
		err := do(ctx, w, t, p)
		stopped := ctx.Err() != nil
		a.post(func() { a.transferred(id, t, ps, done, err, stopped) })
	})
}

// transferred takes the end of transfer t, which ran as operation id,
// with done what it says once it went well; stopped says the user
// stopped it, or the window closed.
func (a *app) transferred(id int, t Transfer, ps PathStyle, done string, err error, stopped bool) {
	r, ok := a.ops.running[id]
	if !ok {
		return
	}
	delete(a.ops.running, id)
	r.cancel()
	a.dropDialogs(id)
	if r.visible {
		a.patch(OpDone{ID: id, OK: err == nil})
		if err == nil {
			r.title = done
			a.cheer(id, r)
		}
		a.publishOps()
	}
	switch {
	case err == nil:
		a.patch(Notice{Title: done, Kind: "success"})
	case stopped || errors.Is(err, errTransferStopped) || errors.Is(err, context.Canceled):
		n := Notice{Title: "Stopped: " + r.title, Kind: "warning"}
		if r.last.items > 0 {
			n.Body = plural(r.last.items, "item") + " done before it stopped."
		}
		a.patch(n)
	default:
		a.showError(ErrorBox{Title: failedTitle(job{kind: r.kind}), Body: err.Error()})
	}
	if t.ToFS == a.fs.ID() && a.ps.Same(t.Into, a.nav.path) {
		// What came is selected, by the names it had: the program does
		// not say which it gave another name, or skipped.
		a.nav.pick = nil
		for _, p := range t.Paths {
			a.nav.pick = append(a.nav.pick, ps.Base(p))
		}
		if t.Zip != "" {
			a.nav.pick = []string{t.Zip}
		}
	}
	a.relist()
	// Other windows showing a folder the items left or went to read it
	// again too.
	from := ps.Dir(t.Paths[0])
	a.hub.others(a, func(o *app) {
		switch id := o.fs.ID(); {
		case id == t.ToFS && o.ps.Same(t.Into, o.nav.path),
			t.Move && id == t.FromFS && o.ps.Same(from, o.nav.path):
			o.relist()
		}
	})
}
