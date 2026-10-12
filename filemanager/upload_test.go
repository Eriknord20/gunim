package filemanager

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marrasen/gunim"
)

// logFS is a bare file system that notes the files it makes and the
// renames it does, and fails to make any while broken, as a server
// gone away does.
type logFS struct {
	bareFS
	mu     sync.Mutex
	log    []string
	broken bool
}

func (l *logFS) Create(p string) (io.WriteCloser, error) {
	l.mu.Lock()
	broken := l.broken
	l.log = append(l.log, "create "+filepath.Base(p))
	l.mu.Unlock()
	if broken {
		return nil, errors.New("the connection is lost")
	}
	return l.bareFS.Create(p)
}

func (l *logFS) Rename(from, to string) error {
	l.mu.Lock()
	l.log = append(l.log, "rename "+filepath.Base(from)+" "+filepath.Base(to))
	l.mu.Unlock()
	return l.bareFS.Rename(from, to)
}

func (l *logFS) entries() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.log)
}

func (l *logFS) breaks(on bool) {
	l.mu.Lock()
	l.broken = on
	l.mu.Unlock()
}

// uploadHarness is a window on a remote file system that has fetched
// report.txt to open, with the watch over the copies stepped by hand.
type uploadHarness struct {
	*harness
	fs     *logFS
	local  string
	remote string
	key    copyKey
	now    time.Time
	// notices are those shown, where the toasts are not kept.
	notices func() []Notice
}

// newUploadHarness fetches report.txt to open. With toasts set the
// window shows its notices as toasts, and otherwise keeps them for the
// test.
func newUploadHarness(t *testing.T, toasts bool) *uploadHarness {
	t.Helper()
	lfs := &logFS{bareFS: bareFS{LocalFS()}}
	h, _, l := newFetchHarness(t, func(o *Options) { o.FS = lfs }, "report.txt")
	h.a.hub.copies.every = -1
	u := &uploadHarness{harness: h, fs: lfs, remote: filepath.Join(h.dir, "report.txt"), now: time.Now()}
	if !toasts {
		u.notices = notices(h)
	}
	h.until("the rows arrive", func() bool { return len(h.shown()) == 1 })
	h.activate("report.txt")
	h.until("the copy opens", func() bool { return len(l.all()) == 1 })
	h.idle()
	u.local = l.all()[0]
	u.key = copyKey{"elsewhere", u.remote}
	return u
}

// step moves the watch's clock on past the time a change takes to
// settle, and has it look at the copies.
func (u *uploadHarness) step() {
	u.now = u.now.Add(copiesSettle + time.Second)
	u.a.hub.checkCopies(u.now)
	for range 20 {
		u.pump()
		time.Sleep(time.Millisecond)
	}
}

// edit writes text to the copy, as a program saving it does.
func (u *uploadHarness) edit(text string) {
	u.t.Helper()
	if err := os.WriteFile(u.local, []byte(text), 0o600); err != nil {
		u.t.Fatal(err)
	}
}

// offers are the notices shown that ask to upload.
func (u *uploadHarness) offers() []Notice {
	var out []Notice
	for _, n := range u.notices() {
		if len(n.Buttons) > 0 {
			out = append(out, n)
		}
	}
	return out
}

// told reports whether a notice that does not ask has a title that
// starts with title.
func (u *uploadHarness) told(title string) bool {
	for _, n := range u.notices() {
		if len(n.Buttons) == 0 && strings.HasPrefix(n.Title, title) {
			return true
		}
	}
	return false
}

func (u *uploadHarness) answerOffer(button int, checked bool) {
	u.do(NoticeAnswered{Key: offerKey(u.key), Button: button, Checked: checked})
}

func TestAnEditedCopyIsOfferedOnceSettledAndUploadedThroughAPartFile(t *testing.T) {
	u := newUploadHarness(t, false)
	u.edit("the second draft")
	u.step()
	if n := len(u.offers()); n != 0 {
		t.Fatalf("a change just seen was offered %d times before it settled", n)
	}
	u.step()
	u.until("the change is offered", func() bool { return len(u.offers()) == 1 })
	u.step()
	u.step()
	o := u.offers()
	if len(o) != 1 {
		t.Fatalf("one change was offered %d times", len(o))
	}
	if o[0].Title != "report.txt changed" || !strings.HasPrefix(o[0].Body, "Upload it to ") ||
		!slices.Equal(o[0].Buttons, []string{"Upload", "Not now"}) || o[0].Check != "Don't ask again" {
		t.Fatalf("the offer is %+v", o[0])
	}

	before := len(u.fs.entries())
	u.answerOffer(0, false)
	u.until("the upload finishes", func() bool { return u.told("Uploaded report.txt") })
	u.idle()
	if got := contents(t, u.remote); got != "the second draft" {
		t.Fatalf("the remote file holds %q", got)
	}
	log := u.fs.entries()[before:]
	if len(log) != 2 || !strings.HasPrefix(log[0], "create .files-") || !strings.HasSuffix(log[0], ".part") ||
		log[1] != "rename "+strings.TrimPrefix(log[0], "create ")+" report.txt" {
		t.Fatalf("the upload did %v, want a part file renamed over the file", log)
	}
	info, err := os.Stat(u.remote)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := u.a.hub.copies.get(u.key)
	if !ok || e.size != info.Size() || !e.mod.Equal(info.ModTime()) {
		t.Fatalf("the copy records the remote file as %d bytes of %v, want %d of %v", e.size, e.mod, info.Size(), info.ModTime())
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(u.local), unsentMark)); err == nil {
		t.Fatal("the copy is still marked unsent once uploaded")
	}
	u.step()
	u.step()
	if n := len(u.offers()); n != 1 {
		t.Fatalf("an uploaded copy was offered again: %d offers", n)
	}
}

func TestNotNowLeavesTheRemoteFileUntilTheNextEdit(t *testing.T) {
	u := newUploadHarness(t, false)
	u.edit("draft two")
	u.step()
	u.step()
	u.until("the change is offered", func() bool { return len(u.offers()) == 1 })
	ops := u.a.ops.next
	u.answerOffer(1, false)
	u.step()
	u.step()
	if got := contents(t, u.remote); got != "report.txt" || u.a.ops.next != ops || len(u.offers()) != 1 {
		t.Fatalf("after Not now the remote holds %q, %d operations ran, %d offers", got, u.a.ops.next-ops, len(u.offers()))
	}
	if p, _ := loadPrefs(u.a.prefsPath); p.UploadEdited != "" {
		t.Fatalf("Not now alone kept %q", p.UploadEdited)
	}
	u.edit("draft three, longer")
	u.step()
	u.step()
	u.until("the next change is offered", func() bool { return len(u.offers()) == 2 })
}

func TestDontAskAgainWithUploadUploadsLaterEditsByThemselves(t *testing.T) {
	u := newUploadHarness(t, false)
	u.edit("draft two")
	u.step()
	u.step()
	u.until("the change is offered", func() bool { return len(u.offers()) == 1 })
	u.answerOffer(0, true)
	u.until("the upload finishes", func() bool { return contents(t, u.remote) == "draft two" })
	u.idle()
	if p, _ := loadPrefs(u.a.prefsPath); p.UploadEdited != uploadAlways {
		t.Fatalf("the settings keep %q", p.UploadEdited)
	}
	if u.a.shell.UploadEdited != uploadAlways {
		t.Fatalf("the window says %q", u.a.shell.UploadEdited)
	}
	if i := slices.Index(u.fileMenu(), "Upload edited copies: always"); i < 0 {
		t.Fatalf("the File menu %v offers no way back", u.fileMenu())
	}

	ops := u.a.ops.next
	u.edit("draft three, longer")
	u.step()
	u.step()
	u.until("the next change uploads", func() bool { return contents(t, u.remote) == "draft three, longer" })
	u.idle()
	if u.a.ops.next != ops+1 {
		t.Fatalf("the upload ran as %d operations", u.a.ops.next-ops)
	}
	if n := len(u.offers()); n != 1 {
		t.Fatalf("an upload by itself asked: %d offers", n)
	}
	u.until("a toast says it went", func() bool {
		n := 0
		for _, x := range u.notices() {
			if x.Title == "Uploaded report.txt" && x.Kind == "success" {
				n++
			}
		}
		return n == 2
	})

	// The File menu sets it back.
	u.do(Command{Name: CmdUploadAsk})
	if p, _ := loadPrefs(u.a.prefsPath); p.UploadEdited != uploadAsk {
		t.Fatalf("after Ask, the settings keep %q", p.UploadEdited)
	}
	u.edit("draft four, longer still")
	u.step()
	u.step()
	u.until("the change is offered again", func() bool { return len(u.offers()) == 2 })
}

func TestDontAskAgainWithNotNowStopsTheOffers(t *testing.T) {
	u := newUploadHarness(t, false)
	u.edit("draft two")
	u.step()
	u.step()
	u.until("the change is offered", func() bool { return len(u.offers()) == 1 })
	u.answerOffer(1, true)
	if p, _ := loadPrefs(u.a.prefsPath); p.UploadEdited != uploadNever {
		t.Fatalf("the settings keep %q", p.UploadEdited)
	}
	ops := u.a.ops.next
	u.edit("draft three, longer")
	u.step()
	u.step()
	u.step()
	if n := len(u.offers()); n != 1 || u.a.ops.next != ops || contents(t, u.remote) != "report.txt" {
		t.Fatalf("after Never: %d offers, %d operations, the remote holds %q", n, u.a.ops.next-ops, contents(t, u.remote))
	}
}

func TestARemoteFileChangedMeanwhileAsksAndKeepsBoth(t *testing.T) {
	u := newUploadHarness(t, false)
	u.edit("mine")
	u.step()
	u.step()
	u.until("the change is offered", func() bool { return len(u.offers()) == 1 })
	if err := os.WriteFile(u.remote, []byte("theirs, longer"), 0o644); err != nil {
		t.Fatal(err)
	}
	u.answerOffer(0, false)
	u.until("the question shows", func() bool { return len(u.a.ops.dialogs) == 1 })
	c, ok := u.a.ops.dialogs[0].state.(Confirm)
	if !ok || c.Title != "report.txt also changed on "+u.a.uploadWhere(u.remote) || c.OK != "Replace" || c.Alt != "Keep Both" ||
		!strings.Contains(c.Body, "report (2).txt") {
		t.Fatalf("the dialog is %+v", u.a.ops.dialogs[0].state)
	}
	u.answer(Confirmed{Token: c.Token, Alt: true})
	both := filepath.Join(u.dir, "report (2).txt")
	// On Windows a file being written cannot be opened, so a read that
	// fails is a copy not there yet.
	u.until("the copy goes up beside", func() bool {
		b, err := os.ReadFile(both)
		return err == nil && string(b) == "mine"
	})
	u.idle()
	if got := contents(t, u.remote); got != "theirs, longer" {
		t.Fatalf("Keep Both replaced the remote file with %q", got)
	}
	u.until("the window lists both", func() bool { return slices.Contains(u.shown(), "report (2).txt") })
	// The copy now goes with the file it went up as.
	u.edit("mine, again")
	u.step()
	u.step()
	u.until("the next change is offered", func() bool { return len(u.offers()) == 2 })
	if o := u.offers()[1]; o.Key != offerKey(copyKey{"elsewhere", both}) {
		t.Fatalf("the next offer is for %q", o.Key)
	}
}

func TestACopyReplacedByARenameIsNoticed(t *testing.T) {
	u := newUploadHarness(t, false)
	tmp := filepath.Join(filepath.Dir(u.local), "report.txt~save")
	if err := os.WriteFile(tmp, []byte("saved by rename"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, u.local); err != nil {
		t.Fatal(err)
	}
	u.step()
	u.step()
	u.until("the change is offered", func() bool { return len(u.offers()) == 1 })
	u.answerOffer(0, false)
	u.until("the upload finishes", func() bool { return contents(t, u.remote) == "saved by rename" })
}

func TestAFailedUploadSaysWhyAndOffersAgain(t *testing.T) {
	u := newUploadHarness(t, false)
	u.edit("draft two")
	u.step()
	u.step()
	u.until("the change is offered", func() bool { return len(u.offers()) == 1 })
	u.fs.breaks(true)
	u.answerOffer(0, false)
	u.until("the failure shows", func() bool { return len(u.a.ops.dialogs) == 1 })
	if e, ok := u.a.ops.dialogs[0].state.(ErrorBox); !ok || !strings.Contains(e.Body, "the connection is lost") {
		t.Fatalf("the dialog is %+v", u.a.ops.dialogs[0].state)
	}
	u.until("the offer shows again", func() bool { return len(u.offers()) == 2 })
	u.answer(DialogClosed{})
	u.fs.breaks(false)
	u.answerOffer(0, false)
	u.until("the second try goes up", func() bool { return contents(t, u.remote) == "draft two" })
}

func TestTheOfferIsAToastThatStaysAndTakesNoKeyboard(t *testing.T) {
	u := newUploadHarness(t, true)
	var before, after gunim.Node
	u.ui(func(_ *browser, ui *gunim.UI) { before = ui.Focused() })
	u.edit("draft two")
	u.step()
	u.step()
	u.until("the toast shows", func() bool { return u.b.toasts.Len() == 1 })
	// Long past an info toast's time.
	u.frames(600)
	u.ui(func(_ *browser, ui *gunim.UI) { after = ui.Focused() })
	if u.b.toasts.Len() != 1 {
		t.Fatalf("the offer went by itself: %d toasts", u.b.toasts.Len())
	}
	if before == nil || after != before {
		t.Fatalf("the keyboard was on %v, and is on %v with the offer showing", before, after)
	}
	if len(u.a.ops.dialogs) != 0 {
		t.Fatal("the offer came with a dialog")
	}
}

func TestACopyNotUploadedOutlivesTheHubAndTheSweep(t *testing.T) {
	u := newUploadHarness(t, false)
	u.edit("draft two")
	u.step()
	u.step()
	u.until("the change is offered", func() bool { return len(u.offers()) == 1 })
	hubDir := filepath.Dir(filepath.Dir(filepath.Dir(u.local)))
	other := filepath.Join(hubDir, "fs", "path", "plain.txt")
	if err := os.MkdirAll(filepath.Dir(other), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	u.a.hub.removeCopies()
	if got := contents(t, u.local); got != "draft two" {
		t.Fatalf("the hub's end left the copy holding %q", got)
	}
	if _, err := os.Stat(other); err == nil {
		t.Fatal("the hub's end left a copy with no change")
	}
	// A day later, the sweep of a hub that starts leaves it too.
	then := time.Now().Add(-copiesKept - time.Hour)
	if err := os.Chtimes(hubDir, then, then); err != nil {
		t.Fatal(err)
	}
	sweep(filepath.Dir(hubDir), time.Now().Add(-copiesKept))
	if got := contents(t, u.local); got != "draft two" {
		t.Fatalf("the sweep left the copy holding %q", got)
	}
	// Long after, it goes.
	then = time.Now().Add(-unsentKept - time.Hour)
	if err := os.Chtimes(hubDir, then, then); err != nil {
		t.Fatal(err)
	}
	sweep(filepath.Dir(hubDir), time.Now().Add(-copiesKept))
	if _, err := os.Stat(hubDir); err == nil {
		t.Fatal("a copy a month old stayed")
	}
}
