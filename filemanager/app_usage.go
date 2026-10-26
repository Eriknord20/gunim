package filemanager

import (
	"context"
	"strings"
	"time"

	"github.com/marrasen/gunim"
)

// usageEvery is how often Disk usage shows what has been read, while it
// reads.
const usageEvery = 400 * time.Millisecond

// usageState is the Disk usage page: whether it shows, the folder it
// reads, and how the user looks at it.
type usageState struct {
	open bool
	// opened counts the pages opened, for each one's ID, and the scans
	// started, for each one's Seq.
	opened int
	id     gunim.ID
	seq    int
	gen    int
	root   string
	scan   *scanner
	cancel context.CancelFunc
	// focus is the key of the folder the boxes show, and shut the folders
	// the tree has open, by key.
	focus   string
	folders map[string]bool
	measure Measure
	colour  ColourBy
	// free and space are the bytes free and in all on the drive.
	free, space uint64
	// last is the state sent last, which a pane coming to a window shows.
	last Usage
	// note says what came of the last act, and noteErr that it failed.
	note    string
	noteErr bool
	// acting stops the cloud provider being told what to keep, while it
	// is, as when the page closes.
	acting context.CancelFunc
}

// stopActing stops telling the cloud provider what to keep.
func (s *usageState) stopActing() {
	if s.acting != nil {
		s.acting()
		s.acting = nil
	}
}

// stop stops the reading.
func (s *usageState) stop() {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
}

// handleUsage takes the intents of Disk usage.
func (a *app) handleUsage(in gunim.Intent) bool {
	s := &a.usage
	switch v := in.(type) {
	case Command:
		if v.Name != CmdUsage {
			return false
		}
		a.openUsage(a.usageTarget())
	case UsageAsked:
		a.openUsage(v.Path)
	case UsageFocus:
		if v.Seq == s.seq && s.open {
			s.focus = v.Key
			a.publishUsage()
		}
	case UsageOpened:
		if v.Seq == s.seq && s.open {
			if v.Open {
				s.folders[v.Key] = true
			} else {
				delete(s.folders, v.Key)
			}
			a.publishUsage()
		}
	case UsageSet:
		if v.Seq == s.seq && s.open {
			s.measure, s.colour = v.Measure, v.Colour
			a.publishUsage()
		}
	case UsageAct:
		if v.Seq == s.seq && s.open {
			a.usageAct(v.Key, v.Act)
		}
	case UsageClosed:
		if v.Seq == s.seq && s.open {
			a.closeUsage(false)
		}
	default:
		return false
	}
	return true
}

// usageTarget is the folder Disk usage reads: the one folder selected,
// or else the folder showing.
func (a *app) usageTarget() string {
	if es := a.selectedEntries(); len(es) == 1 && es[0].Kind == KindFolder {
		return a.ps.Join(a.nav.path, es[0].Name)
	}
	return a.nav.path
}

// openUsage shows Disk usage, reading root.
func (a *app) openUsage(root string) {
	if root == "" {
		return
	}
	s := &a.usage
	a.closeViewer()
	s.stop()
	s.seq++
	s.gen = 0
	if root != s.root {
		s.stopActing()
	}
	s.root, s.focus, s.folders = root, "", map[string]bool{}
	s.note, s.noteErr = "", false
	s.free, s.space = 0, 0
	fsys := a.fs
	if a.nav.archive == "" || !a.ps.inside(root, a.nav.archive) {
		// Outside an archive, the file system itself, so a zip on the way
		// counts as the file it is, and no path is looked up in one.
		fsys = unwrapFS(fsys)
	}
	s.scan = newScanner(fsys, root)
	ctx, cancel := context.WithCancel(a.ctx)
	s.cancel = cancel
	seq, scan := s.seq, s.scan
	go scan.run(ctx, func() { a.post(func() { a.usageRead(seq) }) })
	go func() {
		t := time.NewTicker(usageEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				a.post(func() {
					if seq == a.usage.seq && a.usage.open {
						a.publishUsage()
					}
				})
			}
		}
	}()
	if sr, ok := optional[SpaceReporter](fsys); ok {
		go func() {
			free, total, err := sr.Space(root)
			if err != nil {
				return
			}
			a.post(func() {
				if seq == a.usage.seq {
					a.usage.free, a.usage.space = free, total
					a.publishUsage()
				}
			})
		}()
	}
	if !s.open {
		s.open = true
		s.opened++
		s.id = a.ids.usage(s.opened)
		a.shell.Usage = s.id
		a.publishShell()
		s.last = a.usageState()
		s.last.Take = len(a.ops.dialogs) == 0
		a.mountUsage(true)
		s.last.Take = false
		return
	}
	a.publishUsage()
}

// mountUsage shows the page over the browser, under any dialog asking,
// and with focus gives it the keyboard.
func (a *app) mountUsage(focus bool) {
	a.send(a.c.Mount(a.ids.browser(), a.usage.id, "usage", a.usage.last))
	if len(a.ops.dialogs) > 0 {
		// The dialog goes over it again, as it asks what matters more.
		if focus {
			a.send(a.c.Unmount(a.ops.dialogs[0].id))
			a.mountDialog(a.ops.dialogs[0])
		}
		return
	}
}

// usageRead takes word that the reading of scan seq has ended.
func (a *app) usageRead(seq int) {
	s := &a.usage
	if seq != s.seq || !s.open {
		return
	}
	// Stops the ticking too.
	s.stop()
	if st := s.scan; st != nil {
		st.mu.Lock()
		errs, first := st.errs, st.first
		st.mu.Unlock()
		if errs > 0 {
			a.logLine("Reading the disk usage of "+a.ps.Show(s.root), plural(errs, "folder")+" could not be read, the first "+first)
		}
	}
	a.publishUsage()
}

// usageState is the page's state as it is now.
func (a *app) usageState() Usage {
	s := &a.usage
	u := s.scan.snapshot(s.focus, s.folders, s.measure)
	s.gen++
	u.Seq, u.Gen = s.seq, s.gen
	u.Root, u.Paths = s.root, a.ps
	u.Where = a.shell.Where
	u.Free, u.Space = s.free, s.space
	u.Measure, u.Colour = s.measure, s.colour
	u.CanKeep = s.scan.keeper != nil
	u.Trash = a.trash != nil
	u.Note, u.NoteErr = s.note, s.noteErr
	if !u.Cloud {
		u.Measure = MeasureAll
	}
	s.focus = u.Focus
	return u
}

// publishUsage sends the page its state.
func (a *app) publishUsage() {
	s := &a.usage
	if !s.open || s.scan == nil {
		return
	}
	s.last = a.usageState()
	a.send(a.c.Update(s.id, s.last))
}

// usageStep runs a step of a script that changes how Disk usage looks.
func (a *app) usageStep(verb, arg string) {
	s := &a.usage
	if !s.open {
		return
	}
	switch verb {
	case "usage-by":
		s.measure = map[string]Measure{"all": MeasureAll, "device": MeasureDevice, "online": MeasureOnline}[arg]
	case "usage-colour":
		s.colour = map[string]ColourBy{"type": ColourType, "cloud": ColourCloud}[arg]
	case "usage-focus":
		s.focus = arg
	}
	a.publishUsage()
}

// usageAct does what the page asks to item key.
func (a *app) usageAct(key, act string) {
	s := &a.usage
	key, _, rest := strings.Cut(key, "\x00")
	switch act {
	case UsageRead:
		// The folder in focus and those open stay, where they still are.
		focus, folders := s.focus, s.folders
		a.openUsage(s.root)
		s.focus, s.folders = focus, folders
		a.publishUsage()
		return
	case UsageStop:
		s.stop()
		a.publishUsage()
		return
	}
	path := s.root
	if key != "" {
		path = a.ps.Join(append([]string{s.root}, strings.Split(key, "/")...)...)
	}
	switch act {
	case UsageShow:
		dir, name := a.ps.Dir(path), a.ps.Base(path)
		if rest || key == "" {
			dir, name = path, ""
		}
		a.openWindowOn(dir, name)
	case UsageOpen:
		if rest {
			return
		}
		a.openFiles([]string{path})
	case UsageTrash, UsageDelete:
		if s.scan.reading() {
			s.note, s.noteErr = "Wait until the folder is read to delete from it.", true
			a.publishUsage()
			return
		}
		if !rest && key != "" {
			a.usageRemove(key, path, act == UsageDelete)
		}
	case UsageFree, UsageKeep:
		if !rest {
			a.keepOnDevice(key, path, act == UsageKeep)
		}
	}
}

// keepOnDevice has the cloud provider keep the item of key, at path, on
// this device with keep, or free its space without, in the background,
// and says so on the page. One at a time: two at once would mark the same
// files both ways.
func (a *app) keepOnDevice(key, path string, keep bool) {
	s := &a.usage
	k := s.scan.keeper
	if k == nil {
		return
	}
	if s.acting != nil {
		s.note, s.noteErr = "Wait for the cloud provider to be told of the last one first.", true
		a.publishUsage()
		return
	}
	name := a.ps.Show(path)
	what := "Freeing up the space of " + name
	if keep {
		what = "Keeping " + name + " on this device"
	}
	s.note, s.noteErr = what+"…", false
	a.publishUsage()
	ctx, cancel := context.WithCancel(a.ctx)
	s.acting = cancel
	seq, scan := s.seq, s.scan
	go func() {
		err := k.KeepOnDevice(ctx, path, keep)
		a.post(func() {
			stopped := ctx.Err() != nil
			cancel()
			if err != nil && !stopped {
				// Told the log whatever the page shows now.
				a.logLine(what, err.Error())
			}
			if seq != s.seq || !s.open || stopped {
				return
			}
			s.acting = nil
			switch {
			case err != nil:
				s.note, s.noteErr = "Could not free up all the space of "+name+": "+err.Error(), true
				if keep {
					s.note = "Could not keep all of " + name + " on this device: " + err.Error()
				}
				// Some were marked: what each is now, read again.
				note := s.note
				a.usageAct("", UsageRead)
				s.note, s.noteErr = note, true
			case keep:
				scan.mark(key, true)
				s.note, s.noteErr = "Kept "+name+" on this device. The cloud provider downloads what is online only in its own time.", false
			default:
				scan.mark(key, false)
				s.note, s.noteErr = "Freed up the space of "+name+". The cloud provider takes the files off this device in its own time.", false
			}
			a.publishUsage()
		})
	}()
}

// closeUsage closes the page, and drops what it read. unmount takes it
// away, where the page has not closed itself.
func (a *app) closeUsage(unmount bool) {
	s := &a.usage
	if !s.open {
		return
	}
	s.open = false
	s.stop()
	s.stopActing()
	s.scan, s.folders, s.last = nil, nil, Usage{}
	a.shell.Usage = ""
	a.publishShell()
	if unmount {
		a.send(a.c.Unmount(s.id))
	}
	a.patch(FocusListing{})
}

// usageRemove moves the item of key, at path, to the trash, or deletes
// it for good with forGood or where there is no trash, after asking, as
// the window's own Delete keys do. Once it is gone, the page counts it
// gone: what a folder holds and the drive's room, read again only where
// asked.
func (a *app) usageRemove(key, path string, forGood bool) {
	s := &a.usage
	seq, scan := s.seq, s.scan
	gone := func() {
		if seq != s.seq || !s.open {
			return
		}
		scan.remove(key)
		a.publishUsage()
	}
	paths := []string{path}
	if !forGood && a.trash != nil {
		a.startOp(job{kind: OpTrash, srcs: paths, worked: gone}, "Moving "+a.what(paths)+" to the trash")
		return
	}
	body := "It will not go to the trash, and this cannot be undone."
	if !forGood {
		body = "There is no trash here, so it cannot come back."
	}
	a.confirm(Confirm{Title: "Delete " + a.what(paths) + " for good?", Body: body, OK: "Delete"},
		func() { a.startOp(job{kind: OpDelete, srcs: paths, worked: gone}, "Deleting "+a.what(paths)) })
}
