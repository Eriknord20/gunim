package filemanager

import (
	"cmp"
	"context"
	"errors"
	"runtime"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/marrasen/gunim"
)

// trashCanState is the trash page: whether it shows, what it read of the
// trash, and what it is doing.
type trashCanState struct {
	open bool
	// opened counts the pages opened, for each one's ID, and seq the trash
	// pages asked for, each a Seq; reads counts the readings, so only the
	// last one's answer is taken.
	opened, seq, gen, reads int
	id                      gunim.ID
	can                     TrashCan
	// items are what the trash holds, the latest deleted first, and rows
	// them as the page shows them, made again as rowsGen counts.
	items   []TrashItem
	rows    []TrashRow
	rowsGen int
	reading bool
	err     string
	// busy says items are being put back or deleted, which goes on while
	// the page closes and opens again, and stops a second act until it
	// ends.
	busy bool
	// note says what came of the last act, and noteErr that it failed.
	note    string
	noteErr bool
	// cancel stops the reading, and acting the act running.
	cancel, acting context.CancelFunc
	// last is the state sent last, which a pane coming to a window shows.
	last Trash
}

// stop stops the reading.
func (s *trashCanState) stop() {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
}

// end stops the reading and the act running, as the window closes, which
// waits for the act.
func (s *trashCanState) end() {
	s.stop()
	if s.acting != nil {
		s.acting()
		s.acting = nil
	}
}

// handleTrashCan takes the intents of the trash page.
func (a *app) handleTrashCan(in gunim.Intent) bool {
	s := &a.bin
	switch v := in.(type) {
	case Command:
		switch v.Name {
		case CmdTrashCan:
			a.openTrashCan()
		case CmdEmptyTrash:
			a.emptyTrash()
		default:
			return false
		}
	case TrashAct:
		if v.Seq == s.seq && s.open {
			a.trashAct(v.Keys, v.Act)
		}
	case TrashClosed:
		if v.Seq == s.seq && s.open {
			a.closeTrashCan(false)
		}
	default:
		return false
	}
	return true
}

// openTrashCan shows the trash page, over the folder showing, and reads
// the trash. Where the page shows already, it reads the trash again, and
// what it is doing goes on.
func (a *app) openTrashCan() {
	can, ok := a.trashCan()
	if !ok {
		a.fail("There is no trash to open here.")
		return
	}
	s := &a.bin
	if s.open {
		if !s.busy {
			a.readTrashCan()
		}
		a.publishTrashCan()
		return
	}
	a.closeViewer()
	a.closeUsage(true)
	s.seq++
	s.can, s.err = can, ""
	a.setTrashItems(nil)
	s.note, s.noteErr = "", false
	a.readTrashCan()
	s.open = true
	s.opened++
	s.id = a.ids.trashCan(s.opened)
	a.shell.TrashCan = s.id
	a.publishShell()
	s.last = a.trashCanState()
	s.last.Take = len(a.ops.dialogs) == 0
	a.mountTrashCan(true)
	s.last.Take = false
}

// trashCan is the trash of the window's file system, where the window can
// open it.
func (a *app) trashCan() (TrashCan, bool) {
	c, ok := a.trash.(TrashCan)
	return c, ok
}

// canOpen reports whether the window can open the trash t. The
// computer's own is one on every system, and opens only where the system
// has a trash the window knows.
func canOpen(t Trasher) bool {
	if _, ok := t.(localFS); ok {
		_, err := systemTrashCan()
		return err == nil
	}
	_, ok := t.(TrashCan)
	return ok
}

// mountTrashCan shows the page over the browser, under any dialog asking,
// and with focus gives it the keyboard.
func (a *app) mountTrashCan(focus bool) {
	a.send(a.c.Mount(a.ids.browser(), a.bin.id, "trash-can", a.bin.last))
	if len(a.ops.dialogs) > 0 && focus {
		// The dialog goes over it again, as it asks what matters more.
		a.send(a.c.Unmount(a.ops.dialogs[0].id))
		a.mountDialog(a.ops.dialogs[0])
	}
}

// readTrashCan reads what the trash holds, in the background.
func (a *app) readTrashCan() {
	s := &a.bin
	s.stop()
	ctx, cancel := context.WithCancel(a.ctx)
	s.cancel = cancel
	s.reading = true
	s.reads++
	reads, can := s.reads, s.can
	go func() {
		items, err := can.TrashItems(ctx)
		a.post(func() {
			cancel()
			if reads != s.reads || !s.open {
				return
			}
			s.cancel, s.reading = nil, false
			switch {
			case errors.Is(err, errors.ErrUnsupported):
				s.err = "The " + trashNoun(a.trashTitle()) + " cannot be opened here. " + upperFirst(err.Error()) + "."
				a.setTrashItems(nil)
			case err != nil:
				s.err = "Reading the trash: " + err.Error()
				a.setTrashItems(nil)
				a.logLine("Reading the trash", err.Error())
			default:
				s.err = ""
				slices.SortFunc(items, byDeleted)
				a.setTrashItems(items)
			}
			a.publishTrashCan()
		})
	}()
}

// byDeleted orders trash items the latest deleted first, then by name.
func byDeleted(a, b TrashItem) int {
	if c := b.Deleted.Compare(a.Deleted); c != 0 {
		return c
	}
	return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
}

// trashTitle is what the system calls the trash of the window's file
// system: the computer's own on Windows is the Recycle Bin.
func (a *app) trashTitle() string {
	if _, ok := a.trash.(localFS); ok && runtime.GOOS == "windows" {
		return "Recycle Bin"
	}
	return "Trash"
}

// trashNoun is the trash titled title, as a sentence names it: the
// Recycle Bin keeps its capitals.
func trashNoun(title string) string {
	if title == "Trash" {
		return "trash"
	}
	return title
}

// trashCanState is the page's state as it is now.
func (a *app) trashCanState() Trash {
	s := &a.bin
	s.gen++
	return Trash{Seq: s.seq, Gen: s.gen, Title: a.trashTitle(), Where: a.shell.Where, Rows: s.rows, RowsGen: s.rowsGen,
		Reading: s.reading, Err: s.err, Busy: s.busy, Note: s.note, NoteErr: s.noteErr}
}

// setTrashItems takes items as what the trash holds, and makes the rows
// the page shows of them.
func (a *app) setTrashItems(items []TrashItem) {
	s := &a.bin
	s.items = items
	s.rowsGen++
	s.rows = make([]TrashRow, len(items))
	for i, it := range items {
		r := TrashRow{Key: it.ID, Name: it.Name, From: a.ps.Show(a.ps.Dir(it.Original)), Path: a.ps.Show(it.Original),
			Bytes: it.Size, Dir: it.Dir, Tint: tintOf(entry{Name: it.Name, Dir: it.Dir})}
		if !it.Deleted.IsZero() {
			r.Deleted, r.DeletedUnix = it.Deleted.Format("2006-01-02 15:04"), it.Deleted.Unix()
		}
		if !it.Dir {
			r.Size = humanBytes(it.Size)
		}
		s.rows[i] = r
	}
}

// publishTrashCan sends the page its state.
func (a *app) publishTrashCan() {
	s := &a.bin
	if !s.open {
		return
	}
	s.last = a.trashCanState()
	a.send(a.c.Update(s.id, s.last))
}

// closeTrashCan closes the page. unmount takes it away, where the page
// has not closed itself.
func (a *app) closeTrashCan(unmount bool) {
	s := &a.bin
	if !s.open {
		return
	}
	s.open = false
	s.stop()
	s.can, s.last = nil, Trash{}
	a.setTrashItems(nil)
	a.shell.TrashCan = ""
	a.publishShell()
	if unmount {
		a.send(a.c.Unmount(s.id))
	}
	a.patch(FocusListing{})
}

// trashAct does what the page asks to the items of keys.
func (a *app) trashAct(keys []string, act string) {
	s := &a.bin
	switch act {
	case TrashRead:
		a.readTrashCan()
		a.publishTrashCan()
		return
	case TrashEmpty:
		a.emptyTrash()
		return
	}
	var items []TrashItem
	asked := keySet(keys)
	for _, it := range s.items {
		if asked[it.ID] {
			items = append(items, it)
		}
	}
	if len(items) == 0 {
		return
	}
	can := s.can
	switch act {
	case TrashShow:
		a.openWindowOn(a.ps.Dir(items[0].Original), "")
	case TrashRestore:
		a.trashRun(items, "Restoring", "restored", func(_ context.Context, it TrashItem) error { return can.RestoreItem(it) })
	case TrashDelete:
		a.confirm(Confirm{Title: "Delete " + trashWhat(items) + " for good?", Body: "This cannot be undone.", OK: "Delete"},
			func() { a.trashRun(items, "Deleting", "deleted for good", can.PurgeItem) })
	}
}

// trashWhat names the one item of items, or says how many there are.
func trashWhat(items []TrashItem) string {
	if len(items) == 1 {
		return items[0].Name
	}
	return plural(len(items), "item")
}

// trashRun does do to each of items in the background, one at a time,
// and says on the page what came of it, in the words of doing and done.
// An item that fails is passed by, and the window closing stops it at the
// next item, as closing waits for it. One thing at a time: a second asked
// for while the first runs is refused.
func (a *app) trashRun(items []TrashItem, doing, done string, do func(ctx context.Context, it TrashItem) error) {
	s := &a.bin
	if s.busy {
		a.waitForLast()
		return
	}
	s.busy = true
	s.note, s.noteErr = doing+" "+trashWhat(items)+"…", false
	a.publishTrashCan()
	seq := s.seq
	ctx, cancel := context.WithCancel(a.ctx)
	s.acting = cancel
	a.ops.wg.Go(func() {
		defer cancel()
		var failed []string
		var first error
		for _, it := range items {
			if ctx.Err() != nil {
				break
			}
			if err := do(ctx, it); err != nil {
				failed = append(failed, it.Name)
				first = cmp.Or(first, err)
			}
		}
		a.post(func() {
			if first != nil {
				a.logLine(doing+" "+trashWhat(items)+" from the trash", first.Error())
			}
			s.busy, s.acting = false, nil
			if !s.open {
				return
			}
			worked := len(items) - len(failed)
			switch {
			case seq != s.seq:
				// The page closed and opened again since: it says only
				// what the trash holds now.
			case first == nil:
				s.note, s.noteErr = upperFirst(trashWhat(items))+" "+done+".", false
				if len(items) == 1 {
					s.note = "“" + items[0].Name + "” " + done + "."
				}
			case worked == 0:
				s.note, s.noteErr = upperFirst(trashWhat(items))+" could not be "+done+": "+first.Error(), true
				if len(items) == 1 {
					s.note = "“" + items[0].Name + "” could not be " + done + ": " + first.Error()
				}
			default:
				s.note, s.noteErr = plural(worked, "item")+" "+done+", but "+plural(len(failed), "item")+
					" could not be, the first “"+failed[0]+"”: "+first.Error(), true
			}
			a.readTrashCan()
			// The folder showing may have an item back.
			a.poll()
			a.publishTrashCan()
		})
	})
}

// upperFirst is s with its first letter in upper case.
func upperFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[n:]
}

// emptyTrash deletes all the trash holds for good, after asking, in the
// background, and says so on the trash page where it shows, and as a
// notice where it does not.
func (a *app) emptyTrash() {
	s := &a.bin
	can, ok := s.can, s.open
	if !ok {
		can, ok = a.trashCan()
	}
	if !ok {
		a.fail("There is no trash to empty here.")
		return
	}
	if s.busy {
		a.waitForLast()
		return
	}
	title := a.trashTitle()
	body := "Everything in the " + trashNoun(title) + " will be deleted for good."
	if s.open && !s.reading && s.err == "" {
		body = "The " + plural(len(s.items), "item") + " in the " + trashNoun(title) + " will be deleted for good."
	}
	a.confirm(Confirm{Title: "Empty the " + trashNoun(title) + "?", Body: body, OK: "Empty"}, func() {
		if s.busy {
			a.waitForLast()
			return
		}
		s.busy = true
		seq := s.seq
		if s.open {
			s.note, s.noteErr = "Emptying the "+trashNoun(title)+"…", false
			a.publishTrashCan()
		}
		ctx, cancel := context.WithCancel(a.ctx)
		s.acting = cancel
		a.ops.wg.Go(func() {
			defer cancel()
			err := can.EmptyTrash(ctx)
			a.post(func() {
				s.busy, s.acting = false, nil
				if err != nil {
					a.logLine("Emptying the trash", err.Error())
				}
				if !s.open || seq != s.seq {
					// The page that asked is gone: a notice says how it went,
					// and a page open since reads the trash again.
					if err != nil {
						a.fail("Couldn't empty the " + trashNoun(title) + ": " + err.Error())
					} else {
						a.patch(Notice{Title: "Emptied the " + trashNoun(title), Kind: "success"})
					}
					if s.open {
						a.readTrashCan()
						a.publishTrashCan()
					}
					return
				}
				s.note, s.noteErr = "Emptied the "+trashNoun(title)+".", false
				if err != nil {
					s.note, s.noteErr = "Couldn't empty all of the "+trashNoun(title)+": "+err.Error(), true
				}
				a.readTrashCan()
				a.publishTrashCan()
			})
		})
	})
}

// waitForLast says the trash is busy with the last act, on the page
// where it shows, and as a notice where it does not.
func (a *app) waitForLast() {
	s := &a.bin
	if !s.open {
		a.patch(Notice{Title: "Wait until the trash is done with the last one", Kind: "warning"})
		return
	}
	s.note, s.noteErr = "Wait until the last one is done.", true
	a.publishTrashCan()
}
