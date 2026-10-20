package filemanager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// The watch over the copies fetched to open, for changes made to them
// on this computer.
const (
	// copiesEvery is how often the hub looks at its copies, and
	// copiesSettle how long a change stays the same before it counts as
	// done: a program writes a file in several steps.
	copiesEvery  = 2 * time.Second
	copiesSettle = 2 * time.Second
	// heldRetry is how long an upload waits to read a copy again that a
	// program holds.
	heldRetry = time.Second
	// unsentMark is the file, beside a copy, that says it changed and
	// was not uploaded, which keeps it from being taken away.
	unsentMark = ".unsent"
)

// What to do with a copy changed on this computer, as the settings keep
// it: ask, the default, upload it, or leave it be.
const (
	uploadAsk    = "ask"
	uploadAlways = "always"
	uploadNever  = "never"
)

// watchCopies has the hub look at its copies every so often, for
// changes made to them on this computer, once it has some and a window
// to tell, until it has no window left.
func (h *Hub) watchCopies() {
	h.mu.Lock()
	c := h.copies
	if c == nil || h.watching || len(h.apps) == 0 {
		h.mu.Unlock()
		return
	}
	every := c.every
	if every == 0 {
		every = copiesEvery
	}
	if every < 0 {
		h.mu.Unlock()
		return
	}
	h.watching = true
	h.mu.Unlock()
	var done <-chan struct{}
	if h.ctx != nil {
		done = h.ctx.Done()
	}
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-done:
				h.stopWatching()
				return
			case now := <-t.C:
				if !h.checkCopies(now) {
					return
				}
			}
		}
	}()
}

func (h *Hub) stopWatching() {
	h.mu.Lock()
	h.watching = false
	h.mu.Unlock()
}

// checkCopies looks at the hub's copies at now, and hands each change
// that has settled, and was not offered yet, to a window on its file
// system: the one that fetched it, or another. A change no window can
// take waits for one. It reports false, and stops the watch, once the
// hub has no window left.
func (h *Hub) checkCopies(now time.Time) bool {
	h.mu.Lock()
	c := h.copies
	if len(h.apps) == 0 || c == nil {
		h.watching = false
		h.mu.Unlock()
		return false
	}
	apps := slices.Clone(h.apps)
	ids := make(map[*app]string, len(h.ids))
	for a, id := range h.ids {
		ids[a] = id
	}
	h.mu.Unlock()
	pick := func(owner *app, fsID string) *app {
		if owner != nil && slices.Contains(apps, owner) && ids[owner] == fsID {
			return owner
		}
		for _, a := range apps {
			if ids[a] == fsID {
				return a
			}
		}
		return nil
	}
	for _, d := range c.changed(now, pick) {
		go d.to.post(func() { d.to.edited(d.key) })
	}
	return true
}

// handed is a change handed to a window.
type handed struct {
	key copyKey
	to  *app
}

// changed looks at the copies at now, marks those that changed and were
// not uploaded, and returns the changes that have settled and were not
// offered yet, each handed to the window pick picks. A copy being
// replaced, so not there a moment, is looked at again next time.
func (c *openCopies) changed(now time.Time, pick func(owner *app, fsID string) *app) []handed {
	settle := c.settle
	if settle == 0 {
		settle = copiesSettle
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []handed
	for k, e := range c.kept {
		info, err := os.Stat(e.local)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		size, mod := info.Size(), info.ModTime()
		if size != e.seenSize || !mod.Equal(e.seenMod) {
			e.seenSize, e.seenMod, e.seenAt = size, mod, now
			continue
		}
		if now.Sub(e.seenAt) < settle || e.inSync(size, mod) {
			continue
		}
		e.mark()
		if e.with != nil || (size == e.askedSize && mod.Equal(e.askedMod)) {
			continue
		}
		a := pick(e.owner, k.fs)
		if a == nil {
			continue
		}
		e.with, e.askedSize, e.askedMod = a, size, mod
		out = append(out, handed{k, a})
	}
	return out
}

// mark leaves the mark of a change not uploaded beside the copy.
func (e *openCopy) mark() {
	if e.marked {
		return
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(e.local), unsentMark), nil, 0o600); err == nil {
		e.marked = true
	}
}

// unmark takes the mark away, once the copy is as uploaded.
func (e *openCopy) unmark() {
	if !e.marked {
		return
	}
	if err := os.Remove(filepath.Join(filepath.Dir(e.local), unsentMark)); err == nil || errors.Is(err, fs.ErrNotExist) {
		e.marked = false
	}
}

// get returns what is known of the copy of k.
func (c *openCopies) get(k copyKey) (openCopy, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.kept[k]
	if !ok {
		return openCopy{}, false
	}
	return *e, true
}

// release takes the change of k from window a, which is done with it:
// the change is not offered again, unless again is set, when it is
// offered once more.
func (c *openCopies) release(k copyKey, a *app, again bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.kept[k]; ok && e.with == a {
		e.with = nil
		if again {
			e.askedSize = -1
		}
	}
}

// releaseApp takes the changes window a had from it, as it closes, to
// be offered again in another.
func (c *openCopies) releaseApp(a *app) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.kept {
		if e.with == a {
			e.with, e.askedSize = nil, -1
		}
		if e.owner == a {
			e.owner = nil
		}
	}
}

// synced records that the copy at local of k was uploaded to dst, as
// it was when local says, and dst is now as remote says. A copy uploaded
// under another name follows it there. The change is done with, and the
// mark goes where the copy has not changed again since.
func (c *openCopies) synced(k copyKey, local string, li, remote fs.FileInfo, dst string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.kept[k]
	if !ok || e.local != local {
		return
	}
	e.size, e.mod = remote.Size(), remote.ModTime()
	e.localSize, e.localMod = li.Size(), li.ModTime()
	e.with, e.askedSize, e.askedMod = nil, li.Size(), li.ModTime()
	if dst != k.path {
		delete(c.kept, k)
		c.kept[copyKey{k.fs, dst}] = e
	}
	if info, err := os.Stat(e.local); err == nil && e.inSync(info.Size(), info.ModTime()) {
		e.unmark()
	}
}

// offerKey is the key of the notice that offers to upload the copy of k.
func offerKey(k copyKey) string { return "upload\n" + k.fs + "\n" + k.path }

// edited takes a change, settled, to the copy of k: it offers to upload
// it, or uploads it, or leaves it, as the settings say. A window turned
// to another file system since hands it back, for another.
func (a *app) edited(k copyKey) {
	c := a.hub.openCopies()
	if a.fs.ID() != k.fs {
		c.release(k, a, true)
		return
	}
	if a.archiveOf(k.path, false) != "" {
		// An archive is not written back to.
		c.release(k, a, false)
		return
	}
	switch a.prefs.UploadEdited {
	case uploadNever:
		c.release(k, a, false)
	case uploadAlways:
		a.upload(k)
	default:
		a.offerUpload(k)
	}
}

// offerUpload shows a notice that asks whether to upload the copy of k,
// changed on this computer, which stays until the user answers.
func (a *app) offerUpload(k copyKey) {
	key := offerKey(k)
	if a.uploads == nil {
		a.uploads = map[string]copyKey{}
	}
	a.uploads[key] = k
	a.patch(Notice{Key: key, Title: a.ps.Base(k.path) + " changed", Body: "Upload it to " + a.uploadWhere(k.path) + "?",
		Kind: "info", Buttons: []string{"Upload", "Not now"}, Check: "Don't ask again"})
}

// uploadWhere names where a file at path goes as it is uploaded: the
// file system, by its name, or the folder.
func (a *app) uploadWhere(path string) string {
	if w := a.where(); w != "" {
		return w
	}
	return a.ps.placeName(a.ps.Dir(path))
}

// uploadAnswered takes the answer to an offer to upload. With its box
// ticked, the answer stands for the changes to come too.
func (a *app) uploadAnswered(v NoticeAnswered) {
	k, ok := a.uploads[v.Key]
	if !ok {
		return
	}
	delete(a.uploads, v.Key)
	switch v.Button {
	case 0:
		if v.Checked {
			a.setUploadEdited(uploadAlways)
		}
		a.upload(k)
	case 1:
		if v.Checked {
			a.setUploadEdited(uploadNever)
		}
		a.hub.openCopies().release(k, a, false)
	default:
		a.hub.openCopies().release(k, a, false)
	}
}

// setUploadEdited keeps what to do with copies changed on this
// computer, in the settings and in every window that keeps its settings
// where this one does.
func (a *app) setUploadEdited(v string) {
	a.savePrefs(func(p *prefs) { p.UploadEdited = v })
	a.shell.UploadEdited = v
	a.publishShell()
	path := a.prefsPath
	a.hub.others(a, func(o *app) {
		if o.prefsPath == path {
			o.prefs.UploadEdited, o.shell.UploadEdited = v, v
			o.publishShell()
		}
	})
}

// upload uploads the copy of k to the file it was fetched from. Where
// that file changed meanwhile, it asks first whether to replace it or
// keep both.
func (a *app) upload(k copyKey) {
	c := a.hub.openCopies()
	e, ok := c.get(k)
	if !ok {
		return
	}
	if a.fs.ID() != k.fs {
		c.release(k, a, true)
		return
	}
	fsys, ps := a.fs, a.ps
	go func() {
		info, err := fsys.Stat(k.path)
		changed := err == nil && (info.Size() != e.size || !info.ModTime().Equal(e.mod))
		var alt string
		if changed {
			alt, err = freeName(fsys, k.path)
		}
		a.post(func() {
			if !changed || err != nil {
				// The upload says why it cannot go, where the server
				// cannot be reached.
				a.startUpload(k, k.path)
				return
			}
			name := ps.Base(k.path)
			a.ask(Confirm{Title: name + " also changed on " + a.uploadWhere(k.path),
				Body: "It changed there since you opened it. Replace it with your copy, or keep both, with yours as " +
					ps.Base(alt) + "?",
				OK: "Replace", Alt: "Keep Both"}, func(v Confirmed) {
				switch {
				case v.OK:
					a.startUpload(k, k.path)
				case v.Alt:
					a.startUpload(k, alt)
				default:
					c.release(k, a, false)
				}
			})
		})
	}()
}

// startUpload writes the copy of k to dst, as an operation of the
// window.
func (a *app) startUpload(k copyKey, dst string) {
	c := a.hub.openCopies()
	e, ok := c.get(k)
	if !ok {
		return
	}
	fsys, ps := a.fs, a.ps
	name, where := ps.Base(dst), a.uploadWhere(dst)
	id, ctx, _ := a.newOp("Uploading "+name+" to "+where, OpCopy)
	report := func(p progress) { a.post(func() { a.progressed(id, p) }) }
	held := func() {
		a.post(func() {
			if r, ok := a.ops.running[id]; ok && r.visible {
				a.patch(OpTick{ID: id, OpProgress: OpProgress{Unknown: true,
					Waiting: "Waiting for a program to let go of " + filepath.Base(e.local) + "…"}})
			}
		})
	}
	a.ops.wg.Go(func() {
		li, ri, err := uploadCopy(ctx, fsys, e.local, dst, report, held)
		stopped := ctx.Err() != nil
		a.post(func() { a.uploaded(id, k, e.local, dst, li, ri, err, stopped) })
	})
}

// uploaded takes the end of upload operation id, which wrote the copy at
// local of k to dst: li is the copy as it was read, ri dst as written,
// err why it stopped short, and stopped says the user stopped it. One
// that failed offers itself again, for the user to try once more.
func (a *app) uploaded(id int, k copyKey, local, dst string, li, ri fs.FileInfo, err error, stopped bool) {
	c := a.hub.openCopies()
	name := a.ps.Base(dst)
	r, ok := a.ops.running[id]
	if ok {
		delete(a.ops.running, id)
		r.cancel()
		a.dropDialogs(id)
	}
	n := Notice{Title: "Uploaded " + name, Body: "to " + a.uploadWhere(dst), Kind: "success"}
	carded := ok && a.endCard(r, err == nil, n)
	switch {
	case stopped || errors.Is(err, context.Canceled):
		c.release(k, a, false)
		a.patch(Notice{Title: "Stopped uploading " + name, Body: "The copy on this computer keeps the change.", Kind: "warning"})
	case err != nil:
		a.showError(ErrorBox{Title: "The upload stopped", Body: err.Error()})
		a.offerUpload(k)
	default:
		c.synced(k, local, li, ri, dst)
		if !carded {
			a.patch(n)
		}
		dir := a.ps.Dir(dst)
		if a.ps.Same(dir, a.nav.path) {
			a.relist()
		}
		a.touched(job{dest: dir})
	}
}

// errHeld is what reading a copy fails with while a program holds it.
var errHeld = errors.New("held by a program")

// heldReader reads a copy, and says a read that fails failed as the
// copy is held, so the upload tries again.
type heldReader struct{ r io.Reader }

func (h heldReader) Read(b []byte) (int, error) {
	n, err := h.r.Read(b)
	if err != nil && !errors.Is(err, io.EOF) {
		err = fmt.Errorf("%w: %w", errHeld, err)
	}
	return n, err
}

// upInfo is the copy as uploaded: its time, with the mode of the file
// it replaces.
type upInfo struct {
	fs.FileInfo
	mode fs.FileMode
}

// Mode is the mode of the file the copy replaces.
func (u upInfo) Mode() fs.FileMode { return u.mode }

// uploadCopy writes the file at local to dst on fsys, through a part
// file beside dst that it renames over it, as a copy within the window
// does. While a program holds local, so it cannot be read, as on
// Windows, it waits and tries again, and held hears it is waiting. It
// returns local as it was read and dst as it is then.
func uploadCopy(ctx context.Context, fsys FS, local, dst string, report func(progress), held func()) (li, ri fs.FileInfo, err error) {
	mode := fs.FileMode(0o644)
	if info, serr := fsys.Stat(dst); serr == nil {
		mode = info.Mode().Perm()
	}
	for {
		li, err = uploadOnce(ctx, fsys, local, dst, mode, report)
		if err == nil {
			ri, err = fsys.Stat(dst)
			if err != nil {
				err = fmt.Errorf("reading %s: %w", dst, err)
			}
			return li, ri, err
		}
		if !errors.Is(err, errHeld) || ctx.Err() != nil {
			return nil, nil, err
		}
		held()
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(heldRetry):
		}
	}
}

// uploadOnce tries the upload once.
func uploadOnce(ctx context.Context, fsys FS, local, dst string, mode fs.FileMode, report func(progress)) (fs.FileInfo, error) {
	in, err := os.Open(local)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("the copy on this computer is gone: %w", err)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errHeld, err)
	}
	defer func() { _ = in.Close() }()
	li, err := in.Stat()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errHeld, err)
	}
	r := &runner{ctx: ctx, env: env{fs: fsys, report: report},
		p: progress{itemsTotal: 1, bytesTotal: li.Size(), current: dst}}
	r.tell(true)
	if err := r.writeFile(heldReader{in}, local, dst, upInfo{li, mode}); err != nil {
		return nil, err
	}
	r.p.items = 1
	r.tell(true)
	return li, nil
}
