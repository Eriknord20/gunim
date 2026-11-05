//go:build android

package android

import (
	"errors"
	"image"
	"image/color"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/driver/internal/compose"
	"github.com/marrasen/gunim/driver/internal/inbox"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
)

// errClosed is returned by a closed window.
var errClosed = errors.New("android: window closed")

// Window is the Android implementation of [driver.Window]: a rectangle
// of the one surface, drawn into a texture of its own.
type Window struct {
	d         *Driver
	in        *inbox.Inbox
	quit      chan struct{}
	presented chan driver.Frame
	closeOnce sync.Once

	parent                   *Window
	over, above, passthrough bool
	transparent              bool

	// The fields below are guarded by the driver's mu.

	// fills says the window covers the screen, as the first one does.
	// pos and size place any other, in logical pixels of the screen.
	fills  bool
	pos    geom.Point
	size   geom.Size
	hidden bool
	closed bool
	// stale says the window was hidden and has presented nothing since,
	// so its last frame is the popup's before, and it stays out of sight.
	stale bool
	// held keeps the window where its frame showed it, until its frame
	// at a new size comes.
	held compose.Held
	// next is the frame waiting for the render thread, and under the
	// colour under the window's frames.
	next  *frame
	under color.NRGBA
	// shot takes the pixels of the next frame drawn; see Shoot.
	shot func(*image.RGBA)
	// sharpAt is when the last frame, which drew a big mask stretched,
	// is to be drawn again, sharp, or the zero time; sharpen says it is
	// due.
	sharpAt time.Time
	sharpen bool

	// state is the text state the engine told last, when stateSet, with
	// the seq it went with, and editsIn the seq of the last keyboard edit
	// sent to the window.
	state    *input.TextState
	stateSet bool
	stateSeq uint64
	editsIn  uint64

	// takesFingers is what TakeFingers last said: each finger of a
	// touch that starts in the window is a finger of its own, and a
	// second one starts no pinch.
	takesFingers bool
	// keepsAwake is what KeepAwake last said.
	keepsAwake bool
	// excluded is the bands KeepEdgeSwipes last kept from the system's
	// edge gestures, in the window's logical pixels.
	excluded []geom.Rect

	// pane belongs to the render thread.
	pane compose.Pane
}

// frame is one frame for the render thread.
type frame struct {
	ops    []paint.Op
	damage []geom.Rect
}

func newWindow(d *Driver, o driver.Options) *Window {
	w := &Window{
		d:           d,
		quit:        make(chan struct{}),
		presented:   make(chan driver.Frame, 1),
		over:        o.Over,
		above:       o.Above,
		passthrough: o.Passthrough,
		transparent: o.Kind == driver.KindPopup,
	}
	w.in = inbox.New(w.quit)
	w.pane.Transparent = w.transparent
	w.pane.Sharp = func(at time.Time) { d.sharpenAt(w, at) }
	return w
}

// rectLocked returns where the window is on the screen, in logical
// pixels. It runs with the driver's mu held.
func (w *Window) rectLocked() geom.Rect {
	if w.fills {
		return w.d.screen()
	}
	return geom.Rect{Min: w.pos, Max: w.pos.Add(geom.Pt(w.size.W, w.size.H))}
}

// Presented implements [driver.Window].
func (w *Window) Presented() <-chan driver.Frame { return w.presented }

// Input implements [driver.Window].
func (w *Window) Input() <-chan any { return w.in.Out() }

// Present implements [driver.Window]. It leaves the frame for the
// render thread, which draws it with the next frame of the surface.
func (w *Window) Present(ops []paint.Op, damage []geom.Rect) error {
	w.d.mu.Lock()
	if w.closed {
		w.d.mu.Unlock()
		return errClosed
	}
	w.next = &frame{ops: ops, damage: damage}
	w.stale = false
	w.d.mu.Unlock()
	w.d.kick()
	return nil
}

// Size implements [driver.Window].
func (w *Window) Size() geom.Size {
	w.d.mu.Lock()
	defer w.d.mu.Unlock()
	return w.rectLocked().Size()
}

// Scale implements [driver.Window].
func (w *Window) Scale() float32 {
	w.d.mu.Lock()
	defer w.d.mu.Unlock()
	return w.d.density
}

// RefreshRate implements [driver.Window].
func (w *Window) RefreshRate() float64 {
	w.d.mu.Lock()
	defer w.d.mu.Unlock()
	return w.d.rate
}

// Shoot implements [driver.Shooter]: fn takes the window's next frame
// as the render thread draws it, at the screen's density.
func (w *Window) Shoot(fn func(*image.RGBA)) {
	w.d.mu.Lock()
	w.shot = fn
	w.d.mu.Unlock()
	w.d.kick()
}

// keepShot puts back a shot whose frame could not draw, for a later
// frame, unless a newer shot has taken its place.
func (w *Window) keepShot(fn func(*image.RGBA)) {
	w.d.mu.Lock()
	if w.shot == nil {
		w.shot = fn
	}
	w.d.mu.Unlock()
}

// ContentOrigin implements [driver.Positioner]: where the window lies
// on the screen, in device pixels, as it would with the windows slid
// down from over the keyboard.
func (w *Window) ContentOrigin() (image.Point, error) {
	w.d.mu.Lock()
	defer w.d.mu.Unlock()
	at, f := w.rectLocked().Min, float64(w.d.density)
	return image.Pt(int(math.Round(float64(at.X)*f)), int(math.Round(float64(at.Y)*f))), nil
}

// Clipboard implements [driver.Window].
func (w *Window) Clipboard() (string, error) { return clipboard(), nil }

// SetClipboard implements [driver.Window].
func (w *Window) SetClipboard(s string) error {
	setClipboard(s)
	return nil
}

// Close implements [driver.Window].
func (w *Window) Close() error {
	w.closeOnce.Do(func() {
		w.d.mu.Lock()
		w.closed = true
		w.next = nil
		w.d.mu.Unlock()
		w.d.closed(w)
		close(w.quit)
		w.in.Close()
	})
	return nil
}

// Place implements [driver.Placer]. A new size takes effect on the
// screen with the frame the engine presents next, which wakes the render
// thread.
func (w *Window) Place(anchor geom.Rect, size geom.Size) error {
	w.d.mu.Lock()
	defer w.d.mu.Unlock()
	if w.parent == nil || w.fills {
		return nil
	}
	was, resized := w.rectLocked(), size != w.size
	w.size = size
	w.d.placeLocked(w, anchor)
	if resized {
		w.held.Resized(was)
	} else {
		w.d.kick()
	}
	return nil
}

// PopupRoom implements [driver.PopupRoomer]: the room from anchor, in
// w's logical space, to the edges of the part of the screen that shows
// above the keyboard.
func (w *Window) PopupRoom(anchor geom.Rect) driver.Room {
	w.d.mu.Lock()
	defer w.d.mu.Unlock()
	return compose.PopupRoom(anchor.Add(w.rectLocked().Min), w.d.visibleLocked())
}

// ToScreen implements [driver.Screener]. The screen's coordinates are
// logical pixels here.
func (w *Window) ToScreen(p geom.Point) geom.Point {
	w.d.mu.Lock()
	defer w.d.mu.Unlock()
	return p.Add(w.rectLocked().Min)
}

// FromScreen implements [driver.Screener].
func (w *Window) FromScreen(p geom.Point) geom.Point {
	w.d.mu.Lock()
	defer w.d.mu.Unlock()
	return p.Sub(w.rectLocked().Min)
}

// Transparent implements [driver.Transparent]: a popup shows the
// windows under it where it paints nothing.
func (w *Window) Transparent() bool { return w.transparent }

// SetBackground implements [driver.Backgrounder].
func (w *Window) SetBackground(c color.NRGBA) {
	w.d.mu.Lock()
	w.under = c
	w.d.mu.Unlock()
}

// Hide implements [driver.Recycler].
func (w *Window) Hide() error {
	w.d.mu.Lock()
	w.hidden, w.stale = true, true
	w.d.excludeGesturesLocked()
	w.d.keepAwakeLocked()
	w.d.mu.Unlock()
	w.d.kick()
	return nil
}

// Show implements [driver.Recycler]. The window shows from its next
// frame, as the last one it drew was for the popup before.
func (w *Window) Show() error {
	w.d.mu.Lock()
	w.hidden = false
	w.d.excludeGesturesLocked()
	w.d.keepAwakeLocked()
	w.d.mu.Unlock()
	w.d.kick()
	return nil
}

// SetTextInput implements [driver.TextInputter]. The window takes the
// keyboard's input while it takes text, and the soft keyboard goes away
// as it stops; a tap brings the keyboard up, through ShowKeyboard.
func (w *Window) SetTextInput(active bool) {
	w.d.mu.Lock()
	if active {
		w.d.typing = w
		// The state told before text input turned on goes to the
		// keyboard now.
		if s, ok := w.stateToSendLocked(); ok {
			w.d.mu.Unlock()
			sendTextState(s, w.d.editSeqOf())
			return
		}
	} else if w.d.typing == w {
		w.d.typing = nil
		w.d.caretSet, w.d.boxSet = false, false
	} else {
		w.d.mu.Unlock()
		return
	}
	w.d.mu.Unlock()
	if !active {
		showKeyboard(false)
	}
}

// SetTextCaret implements [driver.CaretPlacer]. The driver keeps the
// caret above the soft keyboard: it slides the windows up just far
// enough to show it as the keyboard opens, and follows the caret as
// typing moves it. A caret that moves as its text scrolls stays where
// it goes. Before Android 11, which reports no keyboard as it
// slides, Android pans the window to the caret itself.
func (w *Window) SetTextCaret(r geom.Rect) {
	w.d.mu.Lock()
	at, f := w.rectLocked().Min, w.d.density
	w.d.caret, w.d.caretSet = r.Add(at), true
	w.d.aimLocked()
	w.d.mu.Unlock()
	w.d.kick()
	px := func(v float32) int { return int(math.Round(float64(v * f))) }
	sendCaret(px(r.Min.X+at.X), px(r.Min.Y+at.Y), px(r.Max.X+at.X), px(r.Max.Y+at.Y))
}

// SetTextBox implements [driver.TextBoxPlacer]: the slide keeps the
// whole text box above the keyboard where it fits.
func (w *Window) SetTextBox(r geom.Rect) {
	w.d.mu.Lock()
	w.d.box, w.d.boxSet = r.Add(w.rectLocked().Min), true
	w.d.aimLocked()
	w.d.mu.Unlock()
	w.d.kick()
}

// ShowKeyboard implements [driver.KeyboardShower].
func (w *Window) ShowKeyboard() { showKeyboard(true) }

// SetTextState implements [driver.TextStater]. Java keeps the copy,
// and decides from seq whether the state is behind the keyboard's
// edits.
//
// The keyboard numbers its edits for the whole application, and each
// window's engine counts the edits that reached it. So the driver keeps
// which window each edit went to: a window's state goes to the keyboard
// once the window has taken every edit sent to it, numbered with the
// keyboard's latest edit, and a state from a window the keyboard types
// elsewhere waits until text input turns on in it.
func (w *Window) SetTextState(s *input.TextState, seq uint64) {
	w.d.mu.Lock()
	if s != nil {
		c := *s
		s = &c
	}
	w.state, w.stateSet, w.stateSeq = s, true, seq
	send, ok := w.stateToSendLocked()
	w.d.mu.Unlock()
	if ok {
		sendTextState(send, w.d.editSeqOf())
	}
}

// stateToSendLocked returns the window's text state for the keyboard,
// and whether it is one to send: the window has the keyboard, or nothing
// does, and has taken every edit sent to it. It runs with the driver's
// mu held.
func (w *Window) stateToSendLocked() (*input.TextState, bool) {
	if !w.stateSet || w.d.typing != w && w.d.typing != nil || w.stateSeq < w.editsIn {
		return nil, false
	}
	return w.state, true
}

// SafeArea implements [driver.SafeAreaer]: a window filling the screen
// lies under the system's bars and the camera's cutout.
func (w *Window) SafeArea() geom.Insets {
	w.d.mu.Lock()
	defer w.d.mu.Unlock()
	if !w.fills {
		return geom.Insets{}
	}
	return w.d.safeLocked()
}

// KeyboardCover implements [driver.KeyboardCoverer]: how far up the
// soft keyboard reaches over a window filling the screen. Before
// Android 11 it stays 0, and Android pans the window to the caret
// itself.
func (w *Window) KeyboardCover() float32 {
	w.d.mu.Lock()
	defer w.d.mu.Unlock()
	if !w.fills || w.d.density <= 0 {
		return 0
	}
	return float32(w.d.keyboard) / w.d.density
}

// Buzz implements [driver.Buzzer]: the phone gives the short buzz of a
// long press.
func (w *Window) Buzz() { buzz() }

// Share implements [driver.Sharer] with Android's share sheet. The
// files go to the receiving application through gunim's provider of
// files, which lets it read each one it was handed.
func (w *Window) Share(s driver.Share) error { return share(s) }

// OpenLink implements [driver.LinkOpener] with a view intent, which
// opens the page in the browser the phone keeps for the web.
func (w *Window) OpenLink(url string) error { return openLink(url) }

// Vibrate implements [driver.Vibrator] with the phone's vibration motor.
func (w *Window) Vibrate(pattern ...time.Duration) error { return vibrate(pattern) }

// KeepAwake implements [driver.Awaker] with the activity's flag to keep
// the screen on, which Android honours only while the activity shows.
// The screen stays on while any open window that shows asks.
func (w *Window) KeepAwake(on bool) error {
	w.d.mu.Lock()
	defer w.d.mu.Unlock()
	if w.closed {
		return nil
	}
	w.keepsAwake = on
	w.d.keepAwakeLocked()
	return nil
}

// keepAwakeLocked hands Java whether any open window that shows asks for
// the screen to stay on, where that differs from the last it handed. It
// runs with mu held, so the calls reach Java in order; Java queues the
// change for its UI thread and returns at once.
func (d *Driver) keepAwakeLocked() {
	on := slices.ContainsFunc(d.windows, func(w *Window) bool { return !w.closed && !w.hidden && w.keepsAwake })
	if on == d.awakeSent {
		return
	}
	d.awakeSent = on
	keepAwake(on)
}

// TakeFingers implements [driver.FingerTaker]: each finger of a touch
// that starts in w is a finger of its own, and a second one starts no
// pinch. The view holds one setting for the whole app, so the driver
// hands it w's as each touch starts, and at once when the touch in
// progress is in w.
func (w *Window) TakeFingers(on bool) error {
	w.d.mu.Lock()
	defer w.d.mu.Unlock()
	if w.closed {
		return nil
	}
	w.takesFingers = on
	w.d.takeFingersLocked(w)
	return nil
}

// KeepEdgeSwipes implements [driver.EdgeSwipeKeeper] with the view's
// system gesture exclusion, from Android 10; before it back is a
// button, and the call changes nothing. The one view takes the bands of
// every open window that shows, so each window keeps its own.
func (w *Window) KeepEdgeSwipes(bands []geom.Rect) {
	w.d.mu.Lock()
	defer w.d.mu.Unlock()
	if w.closed {
		return
	}
	w.excluded = append(w.excluded[:0], bands...)
	w.d.excludeGesturesLocked()
}

// excludeGesturesLocked hands Java the rects every open window that
// shows keeps from the system's gestures, in device pixels, where they
// differ from the last it handed. It runs with mu held, so the calls
// reach Java in the order the windows made them; Java queues the change
// for its UI thread and returns at once.
func (d *Driver) excludeGesturesLocked() {
	px := func(v float32) int32 { return int32(math.Round(float64(v * d.density))) }
	var xy []int32
	for _, w := range d.windows {
		if w.closed || w.hidden {
			continue
		}
		at := w.rectLocked().Min
		for _, r := range w.excluded {
			r = r.Add(at)
			xy = append(xy, px(r.Min.X), px(r.Min.Y), px(r.Max.X), px(r.Max.Y))
		}
	}
	if slices.Equal(xy, d.excludedSent) {
		return
	}
	d.excludedSent = xy
	excludeGestures(xy)
}

// ChooseFiles implements [driver.FileChooser].
//
// Files go through the system's document picker. It hands over content
// addresses, so each file chosen is copied into the application's cache
// and the paths are those copies, which need no permission to read. The
// system may clear that cache when storage runs low, and gunim clears it
// as the application next starts, so read them soon and copy what should
// last. A file that fails to copy gives an error, along with the paths
// of those that copied.
//
// The picker narrows files by MIME type, and takes no title or starting
// folder. o.Filters' patterns of the form *.ext narrow it where Android
// knows a type for every extension. It shows every file otherwise, so
// a program with an extension of its own checks what comes back.
//
// Folders go through the system's chooser of folders, for a folder on
// the phone's storage or a card, which a program reads once it has the
// permission for what it reads, as [driver.PermissionMusic].
func (w *Window) ChooseFiles(o driver.ChooseOptions) ([]string, error) {
	if !o.Folders {
		return chooseFiles(o)
	}
	if p := chooseFolder(); p != "" {
		return []string{p}, nil
	}
	return nil, nil
}

var (
	_ driver.Placer          = (*Window)(nil)
	_ driver.PopupRoomer     = (*Window)(nil)
	_ driver.Screener        = (*Window)(nil)
	_ driver.Transparent     = (*Window)(nil)
	_ driver.Backgrounder    = (*Window)(nil)
	_ driver.Recycler        = (*Window)(nil)
	_ driver.TextInputter    = (*Window)(nil)
	_ driver.TextStater      = (*Window)(nil)
	_ driver.KeyboardShower  = (*Window)(nil)
	_ driver.CaretPlacer     = (*Window)(nil)
	_ driver.TextBoxPlacer   = (*Window)(nil)
	_ driver.Buzzer          = (*Window)(nil)
	_ driver.Sharer          = (*Window)(nil)
	_ driver.LinkOpener      = (*Window)(nil)
	_ driver.Vibrator        = (*Window)(nil)
	_ driver.Awaker          = (*Window)(nil)
	_ driver.FingerTaker     = (*Window)(nil)
	_ driver.EdgeSwipeKeeper = (*Window)(nil)
	_ driver.Compass         = (*Window)(nil)
	_ driver.SafeAreaer      = (*Window)(nil)
	_ driver.KeyboardCoverer = (*Window)(nil)
	_ driver.Shooter         = (*Window)(nil)
	_ driver.Positioner      = (*Window)(nil)
)
