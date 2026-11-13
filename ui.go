package gunim

import (
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
)

// Main starts the platform event loop and runs fn alongside it.
//
// Call it from main, on the main goroutine: X11, Win32 and Cocoa all
// insist that windows are created and events pumped there, and no
// amount of Go can wish that away. fn runs on its own goroutine and
// opens windows through the [App] it is given. Main returns when fn
// returns, when the last window closes, or when ctx is cancelled.
//
//	func main() {
//	    err := gunim.Main(context.Background(), func(a *gunim.App) error {
//	        w, err := a.NewWindow(gunim.WindowOptions{Title: "hello"})
//	        if err != nil {
//	            return err
//	        }
//	        for range w.Client().Intents() {
//	        }
//	        return nil
//	    })
//	    if err != nil {
//	        log.Fatal(err)
//	    }
//	}
//
// The error Main returns is the one fn returned, joined with any error
// the platform event loop ended on.
//
// Once the last window closes, Main waits for fn to return, for up to
// five seconds, so the work fn does as it ends, as saving what it keeps
// or letting go of what it loaded, finishes before the process exits.
func Main(ctx context.Context, fn func(*App) error) error {
	drv, err := openDriver()
	if err != nil {
		return fmt.Errorf("gunim: open display: %w", err)
	}
	return runApp(ctx, drv, fn)
}

// MainWith is [Main] on a driver the program opened itself, as
// driver/kms's with options of its own:
//
//	d, err := kms.Open(kms.Options{Rotate: 90})
//	if err != nil {
//	    log.Fatal(err)
//	}
//	err = gunim.MainWith(ctx, d, fn)
//
// It runs fn as Main does, and the driver's Run gives back what the driver
// took as it returns. The call uses the driver up: it cannot run again.
// ctx ends it as it ends Main, as does the last window closing; driver/kms
// also ends it on SIGHUP, SIGINT and SIGTERM, which it catches itself.
func MainWith(ctx context.Context, drv driver.Driver, fn func(*App) error) error {
	return runApp(ctx, drv, fn)
}

// runApp is Main after the display is open, split out so a test can
// hand it a driver of its own.
func runApp(ctx context.Context, drv driver.Driver, fn func(*App) error) error {
	app := &App{drv: drv}
	app.windows.stacker, _ = drv.(driver.Stacker)
	app.windows.coverer, _ = drv.(driver.Coverer)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// fn's result goes into errc before cancel, so a Run that returns
	// because fn finished always finds it there.
	errc := make(chan error, 1)
	runErr := drv.Run(ctx, func() {
		go func() {
			errc <- fn(app)
			cancel()
		}()
	})
	// The last window closed, or ctx ended: fn hears it, from its
	// windows' clients and from ctx, and finishes what it does as it
	// ends before the process exits, unless it takes too long.
	cancel()
	grace := time.NewTimer(fnGrace)
	defer grace.Stop()
	var err error
	select {
	case err = <-errc:
	case <-grace.C:
		return runErr
	}
	// The windows finish closing too, in the same grace, so what they
	// keep as they close, as a PlaceFile, is written before the process
	// exits.
	closed := make(chan struct{})
	go func() {
		app.loops.Wait()
		close(closed)
	}()
	select {
	case <-closed:
	case <-grace.C:
	}
	return errors.Join(err, runErr)
}

// fnGrace is how long Main waits for fn to end once the event loop
// has.
var fnGrace = 5 * time.Second

// An App is the process's connection to the display server. It is the
// only way to open a window.
type App struct {
	drv driver.Driver
	// cues plays the cues of every window of the app.
	cues atomic.Pointer[cuePlayer]
	// windows is the open windows, for drags between them.
	windows windows
	// loops counts the windows' goroutines still running.
	loops sync.WaitGroup
	// held is the clock the windows keep time by, or nil for the wall clock; see [App.Hold].
	held atomic.Pointer[HeldClock]
}

// NowPlaying is what a media application plays; see [App.SetNowPlaying].
type NowPlaying = driver.NowPlaying

// SetNowPlaying shows what the application plays in the system's media
// controls: a phone's lock screen and quick settings, the media panel
// of GNOME and KDE through MPRIS, and the panel Windows opens beside
// the volume. On a phone it keeps the application running while it
// plays unseen, as the system would stop it otherwise; on Android that
// needs the application built with gunimapk's -permissions playback,
// and without it SetNowPlaying does nothing there. The controls'
// buttons, and a keyboard's media keys, arrive at the main window, the
// one used last, as the media keys of package input,
// [input.KeyMediaPlayPause] and the rest, which the application handles
// as it would any key; a move along their bar arrives as
// [input.MediaSeek]. Call it as what plays changes: a track, playing or
// paused, a seek. nil takes the controls away. Where the system has no
// such controls, as macOS for now, it does nothing.
func (a *App) SetNowPlaying(np *NowPlaying) error {
	if p, ok := a.drv.(driver.NowPlayer); ok {
		return p.SetNowPlaying(np)
	}
	return nil
}

// Permitted reports whether the application has permission p. Where the
// system has no such permissions, as a desktop's, it has.
func (a *App) Permitted(p driver.Permission) bool {
	if pm, ok := a.drv.(driver.Permitter); ok {
		return pm.Permitted(p)
	}
	return true
}

// Ask asks the user for permission p, as a phone asks with a prompt of
// the system's, and returns whether it is granted. It blocks until the
// user answers, so call it from a goroutine of the application's, as
// the application half's own; where p is granted, or the system has no
// such permissions, it returns true at once. A user who refused p for
// good is not asked again, and Ask returns false at once.
func (a *App) Ask(p driver.Permission) bool {
	if pm, ok := a.drv.(driver.Permitter); ok {
		return pm.Permitted(p) || pm.Ask(p)
	}
	return true
}

// UserFolder returns the folder the user keeps things of kind f in, as
// their Music folder, or "" where there is none: on a phone the shared
// one, which needs [App.Ask] for a permission to read.
func (a *App) UserFolder(f driver.UserFolder) string {
	if ff, ok := a.drv.(driver.FolderFinder); ok {
		return ff.UserFolder(f)
	}
	return ""
}

// Monitors lists the attached displays, so an application can put a
// window on a chosen one.
func (a *App) Monitors() []driver.Monitor { return a.drv.Monitors() }

// PointerMonitor returns the monitor the pointer is on, for a window to
// open where the user is looking, as an installer started with a
// double click does: give it as [WindowOptions.Monitor] and the window
// opens centred on it. Where the system cannot say where the pointer
// is, it returns the primary monitor; false when there is no monitor.
func (a *App) PointerMonitor() (driver.Monitor, bool) {
	ms := a.Monitors()
	if pd, ok := a.drv.(driver.Pointer); ok {
		if p, ok := pd.PointerOnScreen(); ok {
			for _, m := range ms {
				if m.Bounds.Contains(p) {
					return m, true
				}
			}
		}
	}
	for _, m := range ms {
		if m.Primary {
			return m, true
		}
	}
	if len(ms) > 0 {
		return ms[0], true
	}
	return driver.Monitor{}, false
}

// FocusedBounds returns where on the screen the application's window that
// last had the keyboard is, in screen coordinates as [driver.Monitor]
// gives them, for something to open where the user is working: false
// when no window of it has had the keyboard yet.
func (a *App) FocusedBounds() (geom.Rect, bool) {
	a.windows.mu.Lock()
	var last *Window
	var at uint64
	for w, n := range a.windows.focusedAt {
		if n > at {
			last, at = w, n
		}
	}
	a.windows.mu.Unlock()
	if last == nil {
		return geom.Rect{}, false
	}
	sc, ok := last.dw.(driver.Screener)
	if !ok {
		return geom.Rect{}, false
	}
	return geom.Rect{Min: sc.ToScreen(geom.Point{}), Max: sc.ToScreen(last.dw.Size().Point())}, true
}

// Tray is an icon in the system tray with a menu; see [driver.Tray].
type Tray = driver.Tray

// TrayItem is a line of a tray icon's menu; see [driver.TrayItem].
type TrayItem = driver.TrayItem

// ErrNoTray says the platform has no tray to show an icon in.
var ErrNoTray = driver.ErrNoTray

// SetTray shows t in the system tray, in place of the icon shown before;
// a Tray with no Icon takes it away. It returns ErrNoTray, or why the
// icon could not be shown, where it cannot be.
func (a *App) SetTray(t Tray) error {
	tr, ok := a.drv.(driver.Trayer)
	if !ok {
		return ErrNoTray
	}
	return tr.SetTray(t)
}

// TrayNotify shows a message from the application's tray icon, as the
// system shows one, against the icon already there rather than one of
// its own. It returns ErrNoTray where the platform shows none so.
func (a *App) TrayNotify(title, body string) error {
	tn, ok := a.drv.(driver.TrayNotifier)
	if !ok {
		return ErrNoTray
	}
	return tn.TrayNotify(title, body)
}

// TrayRect returns the tray icon's rectangle in screen coordinates, for a [driver.KindFlyout] window to open beside
// as [WindowOptions.Beside]. It fails where no icon is up, or the system does not say: only Windows does.
func (a *App) TrayRect() (geom.Rect, error) {
	tl, ok := a.drv.(driver.TrayLocator)
	if !ok {
		return geom.Rect{}, ErrNoTray
	}
	return tl.TrayRect()
}

// SystemLook is what the user chose in the system's settings for how applications look and move; see
// [driver.SystemLook].
type SystemLook = driver.SystemLook

// ContrastColors are the colours of a contrast theme; see [driver.ContrastColors].
type ContrastColors = driver.ContrastColors

// ErrNoSystemLook says the platform has no look settings to read. Only Windows' are read so far.
var ErrNoSystemLook = driver.ErrNoSystemLook

// SystemLook reads the user's settings for light or dark, fewer animations, text size and a contrast theme.
// [github.com/marrasen/gunim/widget.ForLook] turns them into a theme.
func (a *App) SystemLook() (SystemLook, error) {
	lr, ok := a.drv.(driver.LookReader)
	if !ok {
		return SystemLook{}, ErrNoSystemLook
	}
	return lr.SystemLook()
}

// WatchSystemLook calls fn, on a goroutine of gunim's, each time the user changes one of the settings
// [App.SystemLook] reads, until stop is called. fn gets an error when the settings could not be read again. stop
// returns once a call to fn in progress has returned, and no call comes after, so call it from outside fn.
func (a *App) WatchSystemLook(fn func(SystemLook, error)) (stop func(), err error) {
	lr, ok := a.drv.(driver.LookReader)
	if !ok {
		return func() {}, ErrNoSystemLook
	}
	return lr.WatchSystemLook(fn)
}

// HotKey is a key that reaches the application from any program; see
// [driver.HotKey].
type HotKey = driver.HotKey

// ErrHotKeyTaken says another program holds a key already, and
// ErrNoHotKeys that the platform gives an application none.
var (
	ErrHotKeyTaken = driver.ErrHotKeyTaken
	ErrNoHotKeys   = driver.ErrNoHotKeys
)

// RegisterHotKey calls fn, on a goroutine of gunim's, each time k is
// pressed, whatever program has the keyboard, until release is called.
// It returns ErrHotKeyTaken when another program has the key, and
// ErrNoHotKeys where the platform gives an application none.
func (a *App) RegisterHotKey(k HotKey, fn func()) (release func(), err error) {
	h, ok := a.drv.(driver.HotKeyer)
	if !ok {
		return func() {}, ErrNoHotKeys
	}
	return h.RegisterHotKey(k, fn)
}

// StayOpen keeps the application running after its last window
// closes, as one that lives in the tray does, until it is turned off
// again with no window open, or the application's context ends. It is
// off to begin with: the last window closing ends the application. The
// function Main runs ending ends the application whatever this says.
func (a *App) StayOpen(on bool) {
	if s, ok := a.drv.(driver.StayOpener); ok {
		s.StayOpen(on)
	}
}

// WindowOptions describes a window to open.
type WindowOptions struct {
	Title   string
	Size    geom.Size
	Monitor *driver.Monitor
	// Kind selects an ordinary window, a tool window or a free-floating
	// popup. A popup is a real window in the display server, so a menu
	// or a dragged-out modal can leave its parent's bounds.
	Kind driver.Kind
	// Parent and Anchor place a popup or utility window relative to an
	// existing one.
	Parent *Window
	Anchor geom.Point
	// Beside, for a [driver.KindFlyout], is the rectangle in screen coordinates the flyout opens beside: the tray
	// icon's, from [App.TrayRect]. Margin is the room round the card the application draws in it, left for the card's
	// shadow. Empty, the flyout opens in the bottom right corner of the primary monitor's work area.
	Beside geom.Rect
	Margin float32
	// Owned makes an ordinary window belong to Parent, as a palette or a plugin's window does: it stays in front of
	// Parent, and Windows keeps it off the taskbar. It is an ordinary window in every other way: it moves to any
	// monitor, has its title bar, and opens at Place where that is set, or else at Anchor in Parent's space. Windows
	// and X11 do so; macOS leaves it an ordinary window.
	Owned bool
	// Root is the node at the top of the tree. A nil Root gets an empty
	// [Box], and views can be mounted into it later through
	// [Client.Mount].
	Root Node
	// Icons are the window's icon at several sizes, for the title bar
	// and the taskbar. None leaves the system's own.
	Icons []image.Image
	// Class is what the window is to the desktop on Linux: X11's WM_CLASS,
	// which a menu entry's StartupWMClass names, so the taskbar groups an
	// application's windows under it. Empty goes by the window's title.
	Class string
	// AskToClose, when set, is sent to the application when the user
	// asks to close the window, in place of closing it. The application
	// closes the window with [Client.Close] once it has decided, such as
	// after asking whether to stop what is still running. When nil, the
	// window closes at once.
	AskToClose Intent
	// SystemFrame keeps the system's title bar and frame. Without it a window is chromeless: it gets the title bar
	// registered with [RegisterTitleBar], which package widget provides, and a node of the application's that is a
	// [Caption] takes that bar's place. The system goes on moving, snapping, sizing and maximizing the window. With no
	// title bar registered, or where a system keeps its own, as macOS does for now, the window keeps the system's.
	SystemFrame bool
	// Border is the thin line round a chromeless window's edge, where the platform draws one, as Windows 11 does: a
	// colour of the application's own, or none. Its zero value is the system's; see [UI.SetBorder].
	Border driver.Border
	// Corner is the radius, in logical pixels, of a chromeless window's corners where gunim draws its shadow, as on
	// Windows 11. Zero is the system's, 8, and a negative one cuts none. Where the system draws the shadow, as without
	// DXGI, it keeps its own corners; [driver.Outliner] reports the radius the window has.
	Corner float32
	// Instant opens and closes a chromeless window at once. Without it the window grows a little and fades in as it
	// opens, over [ArriveTime], and fades out as it closes, the way [Client.Leave] takes it away. A window with the
	// system's frame comes and goes as the system animates it.
	Instant bool
	// Text says how the window draws text: greyscale or on the panel's
	// subpixels, and hinted or not. Its zero value follows the system's
	// settings.
	Text text.Rendering
	// Zoom draws the content that many times larger, as [UI.SetZoom] does; zero is 1.
	Zoom float32
	// ZoomKeys zooms the window with Ctrl and +, - or 0, and with Ctrl and the wheel, and reports each change as
	// [Zoomed].
	ZoomKeys bool
	// Place opens an ordinary window where [Window.Placement] said it was, such as when the application last closed,
	// in place of Size and Monitor. A placement no attached monitor can show the title bar of is centred on the
	// primary monitor instead, and one larger than its monitor's work area shrinks to fit; see
	// [driver.FitPlacement]. A maximized placement opens maximized, and goes back to its bounds when restored.
	Place *driver.Placement
	// PlaceFile keeps the window's place in this file between runs, so the window opens where the user left it. The
	// window opens where the file says, unless Place is set, and the file is written as the window closes, with where
	// the window was then, as [Window.Placement] reports it. A file missing or unreadable opens the window at Size, as
	// on the first run.
	PlaceFile string
	// TitleBar is the title bar a chromeless window gets, in place of the one registered with [RegisterTitleBar].
	TitleBar TitleBar
	// UnderTitleBar lets the application draw the whole of a chromeless window, with the engine's title bar over its
	// top, as an application on a phone draws under the status bar. The bar's height is added to [Frame.Safe] at the
	// top, so the application keeps its content clear of the bar and lets its background run under it. The bar draws
	// its title and buttons, and leaves its own fill out; see [Frame.UnderTitleBar].
	UnderTitleBar bool
	// Pinned opens the window kept above other windows; see [UI.SetPinned].
	Pinned bool
	// Fixed keeps the window at the size it opens at, as for a dialog sized to its question: the user cannot size it
	// by its edges, maximize it, or snap it to a half or the whole of the screen, as Windows does on a drag to the
	// screen's edge or with the Windows key and an arrow. A maximized Place opens at its bounds instead. Where a
	// window manager tiles every window, it may still size the window as it will.
	Fixed bool
	// MinSize is the least size of an ordinary or a tool window, in logical pixels, as Size is: the user cannot size
	// it smaller, and a window that would open smaller, at Size or at a saved place, opens at MinSize. Zero sets none,
	// and a negative side counts as zero. [UI.SetMinSize] changes it while the window is open, where it says which
	// platforms keep it.
	MinSize geom.Size
	// Hidden opens the window without showing it, as for an application
	// that starts in the tray and may close it unseen.
	Hidden bool
	// Theme is the theme the window draws in from its first frame, until the application names another with
	// [Client.SetTheme], which comes a frame or more later: for a window that must look right from the start, as a
	// see-through one, which would otherwise show the default theme's opaque background as it opens. A theme with no
	// name leaves the default.
	Theme theme.Theme
	// DragFromBehind lets a drag start from the window while another
	// window is in front of it, as from Explorer's windows: a press on
	// the window's content leaves it where it is, a drag from the press
	// runs with the window left behind, and a click brings the window to
	// the front as the button comes up. Only Windows does so. On X11 the
	// window manager raises and focuses a window on a click, and on
	// macOS the system does, so the window comes to the front on the
	// press there, as without it. The title bar and the edges bring the
	// window to the front on a press as ever.
	DragFromBehind bool
	// NoColorManagement leaves the window's colours unconverted. Without it, on a display whose colours are not
	// sRGB's, such as a wide gamut monitor, each frame is converted from sRGB to the display's ICC profile, so colours
	// and pictures look as they do in a browser, not oversaturated. [Client.ColorProfile] says what the window does.
	// GUNIM_COLOR=off turns it off for every window, for debugging.
	NoColorManagement bool
}

// NewWindow opens a window and starts its UI goroutine.
func (a *App) NewWindow(o WindowOptions) (*Window, error) {
	if o.Size == (geom.Size{}) {
		o.Size = geom.Sz(800, 600)
	}
	if o.Place == nil && o.PlaceFile != "" {
		o.Place = readPlace(o.PlaceFile)
	}
	do := driver.Options{
		Title: o.Title, Size: o.Size, Monitor: o.Monitor,
		Kind: o.Kind, Anchor: geom.Rect{Min: o.Anchor, Max: o.Anchor}, Icons: o.Icons, Class: o.Class, Owned: o.Owned && o.Parent != nil,
		Chromeless: !o.SystemFrame && (newTitleBar != nil || o.TitleBar != nil), Border: o.Border, Corner: o.Corner, Text: o.Text, Place: o.Place,
		Hidden: o.Hidden, DragFromBehind: o.DragFromBehind, Fixed: o.Fixed, NoColorManagement: o.NoColorManagement,
		MinSize: noNegative(o.MinSize),
	}
	if o.Parent != nil {
		do.Parent = o.Parent.dw
	}
	if o.Kind == driver.KindFlyout {
		do.Anchor, do.Margin, do.Chromeless = o.Beside, o.Margin, false
	}
	dw, err := a.drv.NewWindow(do)
	if err != nil {
		return nil, fmt.Errorf("gunim: open window %q: %w", o.Title, err)
	}

	w := newWindow(dw, o.Root)
	w.setUp(o)
	w.placeFile = o.PlaceFile
	w.ui.underBar = o.UnderTitleBar
	w.ui.fixed = o.Fixed
	w.ui.minSize = noNegative(o.MinSize)
	w.ui.startChrome(o.TitleBar)
	if o.Pinned {
		if err := w.ui.SetPinned(true); err != nil {
			return nil, errors.Join(err, dw.Close())
		}
	}
	animated := w.ui.chrome != nil && !o.Instant
	w.ui.arriving, w.ui.animated = animated, animated
	w.open = a.drv.NewWindow
	w.app = a
	// Opened while the clock is held, the window keeps its time from its first frame. Hold and Release change
	// the clock under the same lock.
	a.windows.addHeld(w, &a.held)
	a.loops.Add(1)
	go w.loop()
	return w, nil
}

// A Window is one on-screen window and the UI goroutine that drives it.
//
// Everything inside a window — the tree, the focus, what the pointer is
// over — belongs to that goroutine. Application code sends commands in
// through [Window.Client] and hears back on [Client.Intents]. Because the
// goroutine alone decides when a node dies, the engine can keep a
// removed dialog alive for as long as its exit animation needs.
type Window struct {
	dw   driver.Window
	ui   *UI
	out  chan Envelope
	done chan struct{}
	// wake nudges the loop when a command arrives. It holds one token,
	// because one nudge is as good as a hundred.
	wake chan struct{}
	// open opens a popup's window, and popupIn carries the popups'
	// events to the UI goroutine.
	open    func(driver.Options) (driver.Window, error)
	popupIn chan popupEvent
	// shots carries Client.Shot's requests, and injected input from
	// Client.Input.
	shots    chan shotRequest
	injected chan input.Event
	// accesses carries Client.Access's requests.
	accesses chan chan *access.Tree
	// leave asks the window to animate out and close, and fade to fade
	// out, or back in, and stay open; see Client.Fade.
	leave chan struct{}
	fade  chan bool
	// placeFile is where the window's place is kept as it closes; see
	// [WindowOptions.PlaceFile].
	placeFile string
	// title is the window's title, which a screen reader reads for it.
	title string
	// askToClose is sent to the application when the user asks to close
	// the window, and nil closes it at once.
	askToClose Intent
	// app is the application the window belongs to, and dragIn carries
	// drags from its other windows.
	app    *App
	dragIn chan dragMsg
	// hidden says the window is put away, minimized or its application
	// in the background, and covered that it is out of sight while open:
	// under other windows, on another desktop, or on a screen that is
	// off or locked. Either way it draws no frames. resumed says it has
	// just come back in sight, so the next frame's delta is a refresh,
	// not the time away.
	hidden, covered, resumed bool
	// cues plays the window's cues, where set; else the app's do.
	cues atomic.Pointer[cuePlayer]
	// blends is whether the last popup's window blended with what is
	// behind it, the guess for the next one.
	blends bool
	// keepStrays says the window keeps its strays, and strays holds them.
	keepStrays bool
	strays     []Stray
	// alarm wakes the loop for the next timer; see wait.
	alarm *time.Timer
	// caughtUp is when the window, out of sight, last applied the commands queued for it; see catchUp.
	caughtUp time.Time

	// unshown counts the frames handed to the driver and not yet shown,
	// and unreleased those whose ops it may still read; a driver that is
	// no [driver.Releaser] lets them go as it shows them. shown is when
	// the last one was, and due is when the frame most recently drawn is
	// predicted to be. All belong to the UI goroutine.
	unshown, unreleased int
	// building says a held frame that paints is being built: its
	// commands, its timers and its painting. A frame owed painted
	// meanwhile is a second frame painted before the driver is asked
	// again whether it has let one go.
	building bool
	shown    time.Time
	due      time.Time
	// dues holds the due time of each frame handed and not yet shown,
	// oldest first. late is how long after its due time a frame lights
	// the screen, smoothed over the frames shown, and lateKnown says a
	// frame has been shown to tell it; see presentAt.
	dues      []time.Time
	late      time.Duration
	lateKnown bool
	// sent is when the frame in flight was handed to the driver, for GUNIM_DEBUG_POINTER to say when one is slow.
	sent time.Time
	// uiTimes sums how long frames take to build, for GUNIM_DEBUG_FRAMES.
	uiTimes uiTimes

	closeOnce sync.Once
	// framing is held by each call that drives an offscreen window, as [Window.Frame] and
	// [Window.Input], and by Close as it closes the popups. popupsOwed says a Close has yet to close
	// them, as such a call was in flight.
	framing    sync.Mutex
	popupsOwed atomic.Bool
	// driveDepth counts the calls driving the window that are in flight, one inside another. Only the
	// goroutine that drives the window reads or writes it.
	driveDepth int
	// clock is the synthetic frame time used by [Window.Frame], so an
	// offscreen window steps at whatever rate the caller chooses.
	clock time.Time
	// held is the clock the window keeps time by, or nil for the wall
	// clock; see [HeldClock]. steps carries Advance's frames to the loop,
	// and step is the one waiting for the loop to be free to draw it.
	held  atomic.Pointer[HeldClock]
	steps chan heldStep
	step  *heldStep
	// heldBy is the clock the loop keeps time by, as it last took it up from held; see followClock.
	heldBy *HeldClock
	// popupWait is when a held frame began to wait for a popup to show
	// the frame before, or zero; see heldFrame.
	popupWait time.Time

	// inMu guards the inbound queue, which application goroutines fill
	// and the UI goroutine drains once a frame.
	inMu sync.Mutex
	// pending is the batch the next frame will apply, and barrier is
	// where coalescing may start: everything before it was queued ahead
	// of a command that changes what exists.
	pending []Command
	barrier int
	stats   windowStats

	mu     sync.RWMutex
	views  map[string]*view
	themes map[string]theme.Theme
	// err holds whatever ended the window. The UI goroutine writes it
	// before closing events, and [Window.Err] reads it afterwards, so
	// closing the channel carries the handover.
	err error
}

// newWindow builds a window and its tree, leaving the UI goroutine to
// the caller, so tests can drive the engine a frame at a time.
func newWindow(dw driver.Window, root Node) *Window {
	if root == nil {
		root = &Box{}
	}
	w := &Window{
		dw:    dw,
		out:   make(chan Envelope, 256),
		done:  make(chan struct{}),
		wake:  make(chan struct{}, 1),
		clock: time.Now(),
		open: func(o driver.Options) (driver.Window, error) {
			ow := driver.Offscreen(o.Size)
			p, ok := o.Parent.(*driver.OffscreenWindow)
			if ok && o.Kind == driver.KindPopup {
				ow.Attach(p, o.Over, o.Above)
			}
			_ = ow.Place(o.Anchor, o.Size)
			// A screen reader reading a window reads its popups.
			if ok && p.AccessWanted() {
				ow.ListenForAccess()
			}
			return ow, nil
		},
		popupIn:  make(chan popupEvent, 16),
		shots:    make(chan shotRequest),
		leave:    make(chan struct{}, 1),
		fade:     make(chan bool, 1),
		injected: make(chan input.Event),
		accesses: make(chan chan *access.Tree),
		dragIn:   make(chan dragMsg, 64),
		steps:    make(chan heldStep),
		blends:   true,
	}
	rootState := &state{node: root, presence: Present, id: Root}
	w.ui = &UI{
		w:      w,
		root:   rootState,
		index:  map[Node]*state{root: rootState},
		ids:    map[ID]*state{Root: rootState},
		topics: map[string][]*state{},
		theme:  theme.NewLive(theme.Make("default")),
		zoom:   1,
	}
	w.ui.noteHeadingNode(rootState)
	w.ui.noteEdgeSwipeNode(rootState)
	return w
}

// Client returns the application's handle to this window.
//
// Everything the application can do goes through it, and everything it
// can say is a plain value. In one process those values cross as they
// are; a socket transport encodes them with [MarshalCommand] and
// leaves both sides as they are.
func (w *Window) Client() Client { return Client{w: w} }

// ErrWindowClosed is returned by [Client] methods once the window has
// gone. Match it with [errors.Is].
var ErrWindowClosed = errors.New("gunim: window is closed")

// A Client is the application side of a window.
//
// Its methods queue commands and return straight away, so application
// code never waits on a frame and never runs on the goroutine that
// draws one.
type Client struct{ w *Window }

// Send queues a command and returns at once.
//
// It never blocks on the window. Push state as fast as your application
// produces it: the queue coalesces superseded [Publish] commands as
// they arrive, and the window applies whatever is left once per display
// refresh. Backpressure from a slow frame would land on the goroutine
// doing the real work, which is the opposite of what this split is for.
//
// Send hands the command over, state and all; see [Command] for what
// that asks of you. It is safe from any goroutine.
func (c Client) Send(cmd Command) error {
	w := c.w
	select {
	case <-w.done:
		return ErrWindowClosed
	default:
	}

	w.inMu.Lock()
	w.queueLocked(cmd)
	w.inMu.Unlock()

	select {
	case w.wake <- struct{}{}:
	default:
	}
	return nil
}

// Mount builds the named view with the given state and inserts it under
// parent, where it animates in.
//
// watch names the topics the view follows, so one [Client.Publish]
// reaches every view showing the same data.
func (c Client) Mount(parent, id ID, view string, state any, watch ...string) error {
	return c.Send(Mount{Parent: parent, ID: id, View: view, Watch: watch, State: state})
}

// Update hands fresh state to one mounted view. It is [Client.Publish]
// to the topic named after the view's own ID.
func (c Client) Update(id ID, state any) error {
	return c.Send(Update{ID: id, State: state})
}

// Publish hands fresh state to every view watching key.
//
// This is where an aprot refresh trigger lands: the handler fires the
// trigger, the transport pushes the result, and every view showing that
// data animates the difference.
func (c Client) Publish(key string, state any) error {
	return c.Send(Publish{Key: key, State: state})
}

// Patch hands a typed partial change to every view watching key.
//
// Use it when a value moved and the shape stayed put, so the change
// lands as a spring retargeting rather than as a reconciled list. The
// views that care need a [RegisterPatch] handler for p's type.
func (c Client) Patch(key string, p any) error {
	if p == nil {
		return errors.New("gunim: Patch needs a value")
	}
	return c.Send(Patch{Key: key, Data: p})
}

// SetTheme switches the window to the theme registered under name, and
// every themed value animates to it.
func (c Client) SetTheme(name string) error { return c.Send(SetTheme{Theme: name}) }

// SetZoom draws the window's content z times larger, as [UI.SetZoom] does.
func (c Client) SetZoom(z float32) error { return c.Send(SetZoom{Zoom: z}) }

// Unmount starts a view's exit and returns at once, with the view still
// on screen animating away.
func (c Client) Unmount(id ID) error { return c.Send(Unmount{ID: id}) }

// Focus moves keyboard focus. An empty id drops it.
func (c Client) Focus(id ID) error { return c.Send(Focus{ID: id}) }

// Intents returns the window's outbound stream. It closes when the
// window does, so ranging over it is a reasonable main loop.
func (c Client) Intents() <-chan Envelope { return c.w.out }

// Err returns the error that ended the window. Read it once Intents has
// closed.
func (c Client) Err() error { return c.w.err }

// Close shuts the window down.
func (c Client) Close() { c.w.Close() }

// KeyboardAway reports whether the window has given the keyboard to another, as the system last said: a node that
// shows the keyboard is with it, as a caret does, shows nothing while it is away. A node focused meanwhile hears
// [input.WindowFocusGained] when it comes back, as the focused node does.
func (u *UI) KeyboardAway() bool { return u.keyboardAway }

// ToFront brings the window to the front with the keyboard, shown again
// first if it was minimized, where the platform can. It may be called
// from any goroutine.
func (c Client) ToFront() {
	if f, ok := c.w.dw.(driver.Fronter); ok {
		f.ToFront()
	}
}

// Leave closes the window the way an application quitting does: its
// content shrinks a little and fades, over [LeaveTime], and then the
// window closes, as [Client.Close] does. Input is ignored meanwhile, and
// popups close at once. Where the window can show what is behind it, it
// fades into the desktop; elsewhere into the dark.
func (c Client) Leave() {
	select {
	case c.w.leave <- struct{}{}:
	default:
	}
}

// Fade fades the window out, as [Client.Leave] does but without
// shrinking, and keeps it open:
// once it has gone it is off the screen and out of the pointer's way,
// and where the system has a task bar its button stays there. Fade with
// out unset brings it back, fading in, from as far as it had gone. Input
// is ignored while it is out or on its way, and its popups close as it
// starts. It draws no frames while it is out.
//
// It is for a window that steps aside for a while, as a program's
// windows do while an overview of them shows.
func (c Client) Fade(out bool) {
	for {
		select {
		case c.w.fade <- out:
			return
		default:
			// The last word counts: an earlier one not yet read goes.
			select {
			case <-c.w.fade:
			default:
			}
		}
	}
}

// LeaveTime is how long a window takes to leave, and ArriveTime how
// long one takes to come in, unless opened with [WindowOptions.Instant].
const (
	LeaveTime  = 220 * time.Millisecond
	ArriveTime = 220 * time.Millisecond
)

// Input hands the window an input event, as its driver would: a key, a
// click, text. It is for a tool that drives a window through a script,
// such as a screenshot taker. Positions are in window space. A pointer
// event that lands on an open popup, such as a menu, goes to the popup,
// as the user's would: a script can click a menu's items. It waits for
// the window to take the event, or for ctx to end.
func (c Client) Input(ctx context.Context, ev input.Event) error {
	select {
	case c.w.injected <- ev:
		return nil
	case <-c.w.done:
		return ErrWindowClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ChooseFiles shows the system's dialog for choosing files or folders to
// open, owned by the window, and returns what was chosen, or nothing when
// the dialog was cancelled. A relative o.Folder is taken from the
// program's working folder, as [filepath.Abs] takes it. It blocks until
// the dialog closes or ctx ends; an ended ctx leaves the dialog open.
func (c Client) ChooseFiles(ctx context.Context, o driver.ChooseOptions) ([]string, error) {
	fc, ok := c.w.dw.(driver.FileChooser)
	if !ok {
		return nil, driver.ErrNoChooser
	}
	o.Folder = absFolder(o.Folder)
	return awaitDialog(ctx, c, func() ([]string, error) { return fc.ChooseFiles(o) })
}

// SaveFile shows the system's dialog for choosing where to save a file,
// owned by the window, and returns the path chosen, or "" when the dialog
// was cancelled. The dialog asks before it lets an existing file be
// chosen. A relative o.Folder is taken from the program's working
// folder, as [filepath.Abs] takes it. It blocks until the dialog closes
// or ctx ends; an ended ctx leaves the dialog open.
func (c Client) SaveFile(ctx context.Context, o driver.SaveOptions) (string, error) {
	fs, ok := c.w.dw.(driver.FileSaver)
	if !ok {
		return "", driver.ErrNoChooser
	}
	o.Folder = absFolder(o.Folder)
	return awaitDialog(ctx, c, func() (string, error) { return fs.SaveFile(o) })
}

// absFolder is folder as a whole path, for the system's dialogs, which
// each read a relative one their own way; an empty folder stays empty.
func absFolder(folder string) string {
	if folder == "" {
		return ""
	}
	if abs, err := filepath.Abs(folder); err == nil {
		return abs
	}
	return folder
}

// Open opens the file or folder at path with the program the system keeps
// for it, as a double click in the system's file manager does. It returns
// once the system has taken the request, and [driver.ErrNoLauncher] where
// gunim cannot ask. That can take a moment, so call it off a loop that other
// work waits on.
func (c Client) Open(path string) error {
	l, ok := c.w.dw.(driver.Launcher)
	if !ok {
		return driver.ErrNoLauncher
	}
	return l.Open(path)
}

// OpenLink opens the web page at url in the system's browser, as a link
// in another application does. It takes http and https addresses alone,
// so a link is never a way to start a program. It returns once the
// system has taken the request, and [driver.ErrNoLinkOpener] where gunim
// cannot ask.
func (c Client) OpenLink(url string) error {
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return fmt.Errorf("gunim: open link %q: only http and https links open", url)
	}
	lo, ok := c.w.dw.(driver.LinkOpener)
	if !ok {
		return driver.ErrNoLinkOpener
	}
	return lo.OpenLink(url)
}

// Reveal shows the file or folder at path in the system's file manager,
// selected where the system can. It returns [driver.ErrNoLauncher] where
// gunim cannot ask.
func (c Client) Reveal(path string) error {
	l, ok := c.w.dw.(driver.Launcher)
	if !ok {
		return driver.ErrNoLauncher
	}
	return l.Reveal(path)
}

// Share hands s, text, files or both, to other applications through the
// system's share sheet, shown over the window, as a phone's Share button
// does. It returns once the system has taken it, and
// [driver.ErrNoSharer] where there is no sheet to show, as on a desktop
// for now; [Client.CanShare] says which, so an application shows its
// Share button only where it works. The files must stay where they are
// while the receiving application reads them.
func (c Client) Share(s driver.Share) error {
	sh, ok := c.w.dw.(driver.Sharer)
	if !ok {
		return driver.ErrNoSharer
	}
	for _, p := range s.Paths {
		if _, err := os.Stat(p); err != nil {
			return err
		}
	}
	return sh.Share(s)
}

// CanShare reports whether [Client.Share] can show a share sheet here.
func (c Client) CanShare() bool {
	_, ok := c.w.dw.(driver.Sharer)
	return ok
}

// Vibrate runs the device's vibration motor in pattern: on for the first
// duration, off for the next, on for the one after, and so on, as the
// web's navigator.vibrate does. Vibrate(200*time.Millisecond) gives one
// buzz of a fifth of a second. A call stops any pattern still running,
// and Vibrate() only stops it. It returns at once, and
// [driver.ErrNoVibrator] where the device has no motor gunim can run,
// as a desktop.
func (c Client) Vibrate(pattern ...time.Duration) error {
	v, ok := c.w.dw.(driver.Vibrator)
	if !ok {
		return driver.ErrNoVibrator
	}
	return v.Vibrate(pattern...)
}

// KeepAwake says whether the screen stays on while the window shows, as
// a recording's or a video's must with no finger on the glass. It lasts
// until a call with false or the window's closing, and the application
// going to the background lets the screen sleep meanwhile. It returns at
// once, and [driver.ErrNoAwaker] where gunim cannot keep the screen on,
// as on a desktop now. It is safe from any goroutine.
func (c Client) KeepAwake(on bool) error {
	a, ok := c.w.dw.(driver.Awaker)
	if !ok {
		return driver.ErrNoAwaker
	}
	return a.KeepAwake(on)
}

// TakeFingers says whether the window takes more than one finger at
// once on a touch screen, as a game steered with one thumb while the
// other taps does. On, every finger of a touch that starts in the window
// arrives as [input.Finger] at the node it came down on, and a second
// finger makes a finger of its own in place of a pinch. The first finger
// drives the pointer as well, so a button still takes a tap. A node that
// takes a FingerDown holds that finger until its FingerUp, and the
// pointer's events stop at it meanwhile: a node inside it, as a button
// on a game's board, still takes the pointer, and the node and those
// around it hear the finger only as Fingers, so a game reads every thumb
// alike. Off, as a window starts, a second finger pinches. Each window
// keeps its own setting, which a touch follows from the window it
// started in. It returns
// [driver.ErrNoFingers] where the window has no touch screen gunim reads
// more than one finger of. It is safe from any goroutine.
func (c Client) TakeFingers(on bool) error {
	f, ok := c.w.dw.(driver.FingerTaker)
	if !ok {
		return driver.ErrNoFingers
	}
	return f.TakeFingers(on)
}

// SetClipboardImage puts the picture png, a PNG file's bytes, on the system clipboard in place of what it held, as
// copying a picture in another program does. It returns once the clipboard holds it, and
// [driver.ErrNoClipboardImage] where the platform cannot put a picture there. It is safe from any goroutine.
func (c Client) SetClipboardImage(png []byte) error { return setClipboardImage(c.w.dw, png) }

// setClipboardImage puts png on dw's clipboard, where dw can.
func setClipboardImage(dw driver.Window, png []byte) error {
	ic, ok := dw.(driver.ImageClipboard)
	if !ok {
		return driver.ErrNoClipboardImage
	}
	return ic.SetClipboardImage(png)
}

// awaitDialog runs show, which shows a dialog, and waits for its answer,
// the window to close, or ctx to end.
func awaitDialog[T any](ctx context.Context, c Client, show func() (T, error)) (T, error) {
	type answer struct {
		v   T
		err error
	}
	got := make(chan answer, 1)
	go func() {
		v, err := show()
		got <- answer{v, err}
	}()
	var zero T
	select {
	case r := <-got:
		return r.v, r.err
	case <-c.w.done:
		return zero, ErrWindowClosed
	case <-ctx.Done():
		return zero, ctx.Err()
	}
}

// NewOffscreen returns a window backed by no display, with the frame
// loop left to the caller.
//
// Drive it with [Window.Frame] and read the result from
// [Window.Offscreen]. It is how a test steps an interface a frame at a
// time, and how a build machine renders one with no display attached.
// It keeps its strays, for a test to check; see [Window.Strays] and the
// gunimtest package.
func NewOffscreen(size geom.Size, root Node) *Window {
	w := newWindow(driver.Offscreen(size), root)
	w.keepStrays = true
	if offscreenMade != nil {
		offscreenMade(w)
	}
	return w
}

// NewOffscreenWith returns an offscreen window, as [NewOffscreen]
// does, set up by o as [App.NewWindow] sets up a window on a display:
// its Size, MinSize, Root, Title, Theme, AskToClose, ZoomKeys and Zoom.
// The rest of o needs a display. A zero Size is 800 by 600.
//
// It is how a test steps an application's own window: a click on the
// close button, as [driver.CloseAsked], sends AskToClose to the
// application as it does on a display.
func NewOffscreenWith(o WindowOptions) *Window {
	if o.Size == (geom.Size{}) {
		o.Size = geom.Sz(800, 600)
	}
	w := NewOffscreen(o.Size, o.Root)
	w.setUp(o)
	if o.MinSize != (geom.Size{}) {
		// The offscreen window keeps its least size, and says no error.
		_ = w.ui.SetMinSize(o.MinSize)
	}
	if o.Title != "" {
		w.Offscreen().SetTitle(o.Title)
	}
	return w
}

// setUp sets w up by the options a window on a display and an offscreen
// one share: its theme, title, AskToClose, zoom keys and zoom.
func (w *Window) setUp(o WindowOptions) {
	if o.Theme.Name != "" {
		anim.SetScale(float64(theme.Scale.In(o.Theme)))
		w.ui.theme = theme.NewLive(o.Theme)
	}
	w.title = o.Title
	w.askToClose = o.AskToClose
	w.ui.zoomKeys = o.ZoomKeys
	if o.Zoom > 0 {
		w.ui.SetZoom(o.Zoom)
	}
}

// offscreenMade is told of each offscreen window made, for the engine's
// own tests to check them all.
var offscreenMade func(*Window)

// Strays returns the calls about nodes out of the tree the window has
// kept since the last call, and forgets them. Only an offscreen window
// keeps them.
func (w *Window) Strays() []Stray {
	s := w.strays
	w.strays = nil
	return s
}

// Offscreen returns the driver window behind a window from
// [NewOffscreen], which holds the last frame's ops, and nil for a
// window on a display.
func (w *Window) Offscreen() *driver.OffscreenWindow {
	d, _ := w.dw.(*driver.OffscreenWindow)
	return d
}

// Frame advances the window by delta and draws once; see
// [Window.PaintWhenLooked] for a window that draws only when looked at.
//
// It is the body of the window's own loop, exposed so that a caller
// holding an offscreen window can step time. Commands queued through
// [Client] are applied first, the same way the loop applies them.
func (w *Window) Frame(delta time.Duration) {
	w.frameAt(w.clock.Add(delta), delta)
}

// frameAt is [Window.Frame] at the time now, delta after the frame before.
func (w *Window) frameAt(now time.Time, delta time.Duration) {
	defer w.drive()()
	// What comes before the frame lays out may go by where the last
	// frame drew things, as a timer asking where a node is: each kind of
	// work paints a frame owed as it is taken, before it runs.
	for drained := false; !drained; {
		select {
		case e := <-w.popupIn:
			w.ui.paintOwed()
			w.ui.popupEvent(e)
		case m := <-w.dragIn:
			w.ui.paintOwed()
			w.ui.dragMsg(m)
		case <-w.leave:
			w.ui.paintOwed()
			w.ui.startLeaving()
		case out := <-w.fade:
			w.ui.paintOwed()
			w.ui.setFade(out)
		default:
			drained = true
		}
	}
	w.applyPending()
	w.clock = now
	w.ui.stepped = true
	w.ui.frame(now, now, delta)
}

// PaintWhenLooked has a window from [NewOffscreen] paint a frame only
// when something looks at what it drew, for a test that steps many
// frames and checks few pixels. Each frame still steps time, settles
// the tree and lays it out. The frame last laid out paints, as it would
// have, before anything goes by where it drew things: input, which finds
// what it lands on; commands, events and timers a frame takes, which may
// ask where a node is; [Window.Access]; [Window.Pixels]; [Window.Paint];
// and reading the driver window's ops, damage, cursor, accessibility
// tree, pointer region, edge swipe bands, pixels, or its pretend frame's
// caption and maximize button. A frame also paints at once for a shot,
// and while what comes next goes by where it drew things: a node to
// bring into view, a popup or a native child open, the pointer held, a
// drag, a fling.
//
// Some things differ from a window that paints every frame:
//   - A node that changes its state as it paints changes it when the
//     frame paints, not when it lays out, which is a node's bug to fix.
//   - Test code that changes a node or the model a node paints from
//     between Frame and a look, not through a command, has that new state
//     paint on the layout of the frame before. Let a frame pass after
//     such a change before looking, or look before making it.
//   - Hover follows what moves under a still pointer at the next paint,
//     not frame by frame.
//   - The driver window's damage is what changed since the frame painted
//     before, which may be frames back.
//
// It must be called from the goroutine calling Frame.
func (w *Window) PaintWhenLooked(on bool) {
	o := w.Offscreen()
	if o == nil {
		return
	}
	defer w.drive()()
	w.ui.lazy = on
	if !on {
		w.ui.paintOwed()
		o.OnLook(nil)
		return
	}
	o.OnLook(w.paintOnLook)
}

// paintOnLook paints the frame owed before a read of what the offscreen window drew.
func (w *Window) paintOnLook() {
	defer w.drive()()
	w.ui.paintOwed()
}

// TimeByFrames has a window from [NewOffscreen] keep time by its frames
// alone, for a test that counts frames: everything the window times goes
// by the time [Window.Frame] has stepped, never by the wall clock, so a
// slow machine takes the same frames as a fast one. A window stepped by
// Frame runs its timers so already; this takes in the rest: the times
// on the events the window makes itself (focus, shown and hidden, a
// drag's steps, Alt tapped), and what nodes time with [UI.Clock], as an
// editor's pause that ends a step of undo, a table's pause in typing to
// find a row, how long a scrollbar lingers, a drag's speed for a fling,
// and a live graph catching up after the window slept.
//
// It must be called from the goroutine calling Frame.
func (w *Window) TimeByFrames(on bool) {
	if w.Offscreen() == nil {
		return
	}
	defer w.drive()()
	w.ui.byFrames = on
	if w.ui.now.IsZero() {
		// Before its first frame, the window's time is where its
		// frames will start from.
		w.ui.now = w.clock
	}
}

// Paint paints the frame last laid out, where a window that paints when
// looked at left it unpainted; see [Window.PaintWhenLooked]. It is for a
// test that reads what the window drew another way, as a chromeless
// window's title bar. It must be called from the goroutine calling Frame.
func (w *Window) Paint() {
	defer w.drive()()
	w.ui.paintOwed()
}

// Still reports whether nothing in the window moves: no animation runs and nothing waits to be applied. A redraw
// owed for a change already applied and timers set for later do not count. It must be called from the goroutine
// calling Frame.
func (w *Window) Still() bool {
	defer w.drive()()
	return !w.ui.animating && !w.queued() && len(w.popupIn) == 0 && len(w.dragIn) == 0 && len(w.leave) == 0 &&
		len(w.fade) == 0
}

// Input hands the window a platform event, as a driver would: a pointer
// move, a click, a key, the close button as [driver.CloseAsked]. It is
// for a window driven with [Window.Frame],
// such as one from [NewOffscreen], and must be called from the goroutine
// calling Frame. Positions are in window space, and the pointer is
// routed through where the last frame drew each node. The event reaches
// this window alone, as a platform's does: a click where a popup shows
// lands in the window under it. [Window.Inject] hands an event to an
// open popup where it lands, as a person's click does.
func (w *Window) Input(ev any) {
	defer w.drive()()
	w.platform(ev)
}

// platform handles one of the platform's events. A window out of sight
// draws no frames, and a frame is where the application's state reaches
// the views; input handled meanwhile, as a media key with a phone's
// screen off, would act on the state from before the window went out of
// sight. So such a window applies the queue first.
func (w *Window) platform(ev any) {
	// Input lands where the last frame drew things.
	w.ui.paintOwed()
	if _, asked := ev.(driver.CloseAsked); asked {
		// The close button, as the loop takes it.
		w.ui.AskToClose()
		return
	}
	if !w.draws() {
		w.applyPending()
	}
	w.ui.handlePlatform(w.ui.heldTime(ev))
}

// Err returns the error that ended the window. Read it once
// [Client.Intents] has closed.
func (w *Window) Err() error { return w.err }

// Placement is where the window is on the screen and whether it is maximized, for the application to save as it
// quits, and open the window there next time with [WindowOptions.Place]. The bounds are in screen coordinates,
// as [driver.Monitor.Bounds] are, and for a maximized or minimized window they are where it goes back to when
// restored. ok is false where the platform cannot say.
//
// Once the window has closed, it reports where the window was as it closed, so an application can save it as it
// quits; [WindowOptions.PlaceFile] does that for it. While the window is open, it waits on the main goroutine, which
// runs the platform's events, so call it from the function given to [Main] or a goroutine of the application's, not
// from the main goroutine itself.
func (w *Window) Placement() (p driver.Placement, ok bool) {
	if r, is := w.dw.(driver.PlacementReader); is {
		return r.Placement()
	}
	return p, false
}

// FromScreen turns p, in screen coordinates, the space
// [driver.Placement.Bounds] is in, into the window's own logical space,
// as the window stands now: its place, its monitor's scale and its zoom.
// It reports false where the window cannot say where it is.
func (w *Window) FromScreen(p geom.Point) (geom.Point, bool) {
	if sc, ok := w.dw.(driver.Screener); ok {
		return sc.FromScreen(p), true
	}
	return p, false
}

// RefreshRate is the rate of the monitor the window is currently on.
func (w *Window) RefreshRate() float64 { return w.dw.RefreshRate() }

// Close shuts the window down at once and drops any queued work. For an
// animated goodbye, unmount the root's views first and call Close once
// they have gone. It is safe from any goroutine. An offscreen window,
// which has no loop of its own, runs what [UI.BeforeClose] was given and
// closes its popups in Close, or at the end of the call driving it that is
// in flight, as [Window.Frame] or [Window.Input].
func (w *Window) Close() {
	w.closeOnce.Do(func() {
		close(w.done)
		if w.app == nil {
			// An offscreen window has no loop to close its popups.
			w.popupsOwed.Store(true)
			w.closeOwedPopups()
		}
	})
}

// drive locks framing for a call that drives an offscreen window, as [Window.Frame] and [Window.Input] are, and
// returns what ends the call: it unlocks, then closes the popups a Close left meanwhile.
// A call made inside another, as a view's Input inside a frame, only counts.
func (w *Window) drive() (end func()) {
	if w.driveDepth > 0 {
		w.driveDepth++
		return func() { w.driveDepth-- }
	}
	w.framing.Lock()
	w.driveDepth = 1
	return func() {
		w.driveDepth = 0
		w.framing.Unlock()
		w.closeOwedPopups()
	}
}

// closeOwedPopups runs the closers and closes the popups a Close left, unless a call that drives the window is in
// flight, which does both as it ends.
func (w *Window) closeOwedPopups() {
	for w.popupsOwed.Load() && w.framing.TryLock() {
		if w.popupsOwed.Swap(false) {
			w.ui.runClosers()
			w.ui.closeAllPopups()
		}
		w.framing.Unlock()
	}
}

// loop is the window's whole life.
//
// It draws when there is something to show and no frame in flight, and
// otherwise waits for whatever comes first: a command, input, the
// display taking the frame in flight, or the application making room
// for an intent. Every frame after the first waits for the one before
// it to reach the screen, so the window draws at most once per refresh
// whatever the application does with [Client].
func (w *Window) loop() {
	if w.app != nil {
		defer w.app.loops.Done()
	}
	// However the loop ends, done says so: as the user closed the window, or its driver window went. A held
	// clock's Advance waits on it.
	defer w.Close()
	defer close(w.out)
	defer w.ui.closeAllPopups()
	defer func() {
		if w.app != nil {
			w.app.windows.remove(w)
		}
	}()
	defer func() {
		if err := w.dw.Close(); err != nil {
			w.err = errors.Join(w.err, fmt.Errorf("gunim: close window: %w", err))
		}
		// Before the application hears the window has closed.
		w.keepPlace()
	}()
	// The compass stops before the window closes.
	defer w.ui.stopHeading()
	// The child windows of the system's in it close before it does.
	defer w.ui.closeNatives()
	defer w.ui.runClosers()

	for {
		w.followClock()
		if w.ui.onHeld {
			if w.heldFrame() {
				continue
			}
		} else if w.free() && w.wants() && w.draws() {
			w.draw()
			continue
		}
		if w.catchUp() {
			continue
		}
		if !w.wait() {
			return
		}
	}
}

// free reports whether the window may build a frame now. With a driver
// that is a [driver.Releaser], it builds the next frame while the one
// before waits for the screen: at most two frames handed and not yet
// shown, and the frame before the last one released, so the frame
// built records into buffers nothing reads. Otherwise it builds once
// the frame before is shown.
func (w *Window) free() bool {
	if _, ok := w.dw.(driver.Releaser); ok {
		return w.unshown < 2 && w.unreleased < 2
	}
	return w.unshown == 0
}

// released is the driver's channel of frames let go, or nil where it
// tells none apart from Presented.
func (w *Window) released() <-chan struct{} {
	if r, ok := w.dw.(driver.Releaser); ok {
		return r.Released()
	}
	return nil
}

// wants reports whether anything is waiting to be drawn: queued
// commands, running animations, or a node that asked for one more
// frame.
func (w *Window) wants() bool {
	return w.queued() || w.ui.needsFrame()
}

// draws reports whether the window draws its frames: while it is
// hidden, or covered with none of its popups in sight, it draws none, and
// its animations hold where they are, unless it is closing, as a window
// leaving animates out, or a shot waits on a frame.
func (w *Window) draws() bool {
	if w.ui.away && !w.ui.goingAway {
		return w.ui.shotsOwed.Load() > 0
	}
	return !w.hidden && (!w.covered || w.ui.popupInSight()) || w.ui.goingAway || w.ui.fadeMoving() || w.ui.shotsOwed.Load() > 0
}

// awayApply is how often a window out of sight applies the commands queued for it.
const awayApply = 250 * time.Millisecond

// catchUp applies the commands queued for a window that draws no frames, once each awayApply at most, and reports
// whether it did. Out of sight, the application still hears its own news: a job finishing in a window under others
// plays its sound, and the rings it sends round the window open their popup, which shows past the window's edges.
// The frame drawn once the window is back lays out what changed.
func (w *Window) catchUp() bool {
	if w.draws() || !w.queued() || time.Since(w.caughtUp) < awayApply {
		return false
	}
	w.caughtUp = time.Now()
	w.applyPending()
	return true
}

// queued reports whether commands are waiting to be applied.
func (w *Window) queued() bool {
	w.inMu.Lock()
	defer w.inMu.Unlock()
	return len(w.pending) > 0
}

// wait blocks until something happens, handles it, and reports whether
// the window is still open.
//
// Input is handled as it arrives, because a click and the release after
// it mean different things in different orders. Commands queue, because
// the last state for a topic is the only one worth drawing.
func (w *Window) wait() bool {
	// A send on a nil channel blocks forever, so the send case is live
	// only while intents are waiting for the application.
	var out chan<- Envelope
	var next Envelope
	if len(w.ui.pending) > 0 {
		out, next = w.out, w.ui.pending[0]
	}
	// A timer wakes the loop a refresh before the first refresh at or
	// after it is due, so the frame drawn then runs it. One timer
	// serves every wait: a terminal's output wakes the loop hundreds of
	// times a second, and a new timer each time is garbage each time.
	//
	// While a frame is on its way to the screen, the loop can draw no
	// other, and the frame shown wakes it. A timer due by then would
	// fire at every wait and spin the loop until the frame showed, so
	// it waits for the wait after.
	//
	// A window out of sight draws no frame, so a timer due then waits
	// for the window to come back, and the frame drawn then runs it.
	// Armed anyway, it would spin the loop for as long as the window
	// stayed hidden or covered. Commands queued meanwhile wake it when
	// catchUp is due to apply them.
	var alarm <-chan time.Time
	var at time.Time
	switch timer, ok := w.ui.nextTimer(); {
	case w.ui.onHeld:
		// Held, a timer runs in the frame Advance draws at its time. A
		// frame waiting on a popup's waits so long at most.
		if !w.popupWait.IsZero() {
			at = w.popupWait.Add(popupWaitMost)
		}
	case w.draws():
		if ok && w.free() {
			at = wakeFor(timer, w.shown, refreshInterval(w.dw.RefreshRate()))
		}
	case w.queued():
		at = w.caughtUp.Add(awayApply)
	}
	if !at.IsZero() {
		d := time.Until(at)
		if w.alarm == nil {
			w.alarm = time.NewTimer(d)
		} else {
			w.alarm.Reset(d)
		}
		defer w.alarm.Stop()
		alarm = w.alarm.C
	}
	select {
	case <-alarm:
		w.ui.due = true
	case <-w.done:
		return false
	case <-w.wake:
	case st := <-w.steps:
		w.heldAsked(st)
	case ev, ok := <-w.dw.Input():
		if !ok {
			return false
		}
		if _, asked := ev.(driver.CloseAsked); asked {
			return w.ui.closeAsked()
		}
		w.platform(ev)
		w.ui.focusNow()
	case e := <-w.popupIn:
		w.ui.popupEvent(e)
	case m := <-w.dragIn:
		w.ui.dragMsg(m)
	case req := <-w.shots:
		w.ui.shoot(req)
	case <-w.leave:
		w.ui.startLeaving()
	case out := <-w.fade:
		w.ui.setFade(out)
	case ev := <-w.injected:
		w.route(ev)
		w.ui.focusNow()
	case got := <-w.accesses:
		got <- w.ui.clientAccess()
	case <-w.released():
		w.unreleased = max(w.unreleased-1, 0)
	case f, ok := <-w.dw.Presented():
		if !ok {
			return false
		}
		w.unshown = max(w.unshown-1, 0)
		if w.released() == nil {
			w.unreleased = max(w.unreleased-1, 0)
		}
		w.shown = f.Shown
		w.learnLate(f)
		if w.ui.arriving && w.ui.arrivedAt.IsZero() && !w.ui.onHeld {
			// Held, the next frame takes its arrival from the held clock.
			w.ui.arrivedAt = w.shown
			if w.shown.IsZero() {
				w.ui.arrivedAt = time.Now()
			}
		}
		if took := time.Since(w.sent); took > slowFrame {
			pointerf("the last frame took %.0f ms to reach the screen", float64(took.Microseconds())/1000)
		}
		w.ui.makeSpare()
	case out <- next:
		w.ui.pending = w.ui.pending[1:]
	}
	return true
}

// draw builds one frame and hands it to the driver.
//
// The frame is stamped with the moment it is predicted to reach the
// screen, so animation lines up with what the viewer sees. Its delta is
// the time since the previous frame's stamp while something was moving
// through the gap. After the window has slept, everything was at rest,
// and whatever starts now is a frame old when it appears, so the delta
// is one refresh. Carrying the time slept would push a fresh hover most
// of the way through its animation before its first frame.
func (w *Window) draw() {
	t0 := time.Now()
	wanted, stirred := w.ui.needsFrame(), w.ui.stirred
	w.ui.stirredBy = false
	w.applyPending()
	applied := time.Since(t0)
	if !w.ui.stirredBy {
		// Only patches that changed nothing laid out came: the frame
		// carries what it would have without them.
		w.ui.stirred = stirred
		if !wanted && !w.ui.needsFrame() {
			// Nor did they start a spring: no frame at all. Looking
			// mustn't leave a wake asked for by a node now at rest.
			wake := w.ui.wakeIn
			if !w.ui.springsMoving() {
				w.ui.wakeIn = wake
				return
			}
		}
	}
	interval := refreshInterval(w.dw.RefreshRate())
	due := nextVsync(w.shown, interval, time.Now())
	if w.unshown > 0 {
		// Built while the frame before waits for the screen, it shows a
		// refresh after that one at the soonest.
		due = later(due, w.due.Add(interval))
	}
	delta := interval
	if w.ui.animating && due.After(w.due) && !w.resumed {
		delta = due.Sub(w.due)
	}
	w.resumed = false
	w.due = due
	w.dues = append(w.dues, due)
	start := time.Now()
	present := w.presentAt(due, start)
	w.ui.frame(due, present, delta)
	w.sent = time.Now()
	if took := w.sent.Sub(start); took > slowFrame {
		pointerf("a frame took %.0f ms to build", float64(took.Microseconds())/1000)
	}
	if uiFramesDebug {
		w.uiTimes.add(w, applied, w.sent.Sub(start), present.Sub(start))
	}
	w.unshown++
	w.unreleased++
}

// presentAt returns when a frame due at due is expected to light the
// screen, built from start: its due time plus how late the frames
// before lit it. One rule serves every platform; a driver that knows
// when frames lit the screen tells it through [driver.Frame.Lag], and
// one that does not estimates it. A frame lights the screen after it
// is built, whatever the frames before said.
func (w *Window) presentAt(due, start time.Time) time.Time {
	return later(due.Add(w.late), start)
}

// learnLate takes in how late after its due time the frame f reports
// lit the screen. The first report counts whole; each after it moves
// the average an eighth of the way, which follows a change in the
// system's pipeline within a few dozen frames and smooths a frame
// shown a refresh off.
func (w *Window) learnLate(f driver.Frame) {
	if len(w.dues) == 0 {
		return
	}
	due := w.dues[0]
	w.dues = w.dues[:copy(w.dues, w.dues[1:])]
	if f.Shown.IsZero() || due.IsZero() {
		return
	}
	e := min(max(f.Shown.Sub(due)+f.Lag, -maxLate), maxLate)
	if !w.lateKnown {
		w.late, w.lateKnown = e, true
		return
	}
	w.late += (e - w.late) / 8
}

// maxLate bounds how early or late a frame is taken to light the
// screen, so a driver's wild report is not believed.
const maxLate = 500 * time.Millisecond

// later returns the later of a and b.
func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// slowFrame is how long building a frame, or its trip to the screen, may take before GUNIM_DEBUG_POINTER says so:
// while it lasts, nothing moves, popups included, and hover seems not to follow the pointer.
const slowFrame = 250 * time.Millisecond

// refreshInterval turns a refresh rate into the time between frames,
// assuming 60 Hz when the driver reports no rate.
func refreshInterval(hz float64) time.Duration {
	if hz <= 0 {
		hz = 60
	}
	return time.Duration(float64(time.Second) / hz)
}

// nextVsync predicts when a frame drawn at now will reach the screen:
// the first refresh after now, in step with the last frame shown.
// Before the first frame is shown, it is one interval from now.
func nextVsync(shown time.Time, interval time.Duration, now time.Time) time.Time {
	if shown.IsZero() {
		return now.Add(interval)
	}
	n := int64(now.Sub(shown)/interval) + 1
	if n < 1 {
		n = 1
	}
	return shown.Add(time.Duration(n) * interval)
}

// wakeFor returns when to draw the frame that runs a timer due at: a refresh before the first refresh at or after
// at, in step with the last frame shown, so [nextVsync] stamps the frame at or after at.
func wakeFor(at, shown time.Time, interval time.Duration) time.Time {
	if shown.IsZero() {
		return at.Add(-interval)
	}
	n := max(int64((at.Sub(shown)+interval-1)/interval), 1)
	return shown.Add(time.Duration(n-1) * interval)
}

// queue adds cmd to the batch for the next frame.
//
// A [Publish] that a later one supersedes is replaced where it stands,
// so an application pushing state at 1 kHz costs one view update per
// frame rather than one per push. The work saved is the reconciliation
// behind that update, which is the expensive half.
//
// [Patch] stays out of it. Two patches on one topic may aim at
// different rows, so each carries something the other lacks.
func (w *Window) queueLocked(cmd Command) {
	w.stats.commands.Add(1)
	if key, ok := coalesceKey(cmd); ok {
		for i := w.barrier; i < len(w.pending); i++ {
			if k, ok := coalesceKey(w.pending[i]); ok && k == key {
				w.pending[i] = cmd
				w.stats.coalesced.Add(1)
				return
			}
		}
	} else {
		// Mount, Unmount and Focus change what exists, so state queued
		// ahead of one of them keeps its place and gets applied. A Patch
		// keeps its place too, as it may stand on the state before it:
		// publishes either side of one don't merge.
		w.barrier = len(w.pending) + 1
	}
	w.pending = append(w.pending, cmd)
}

// coalesceKey returns the topic a command replaces whole state on.
func coalesceKey(c Command) (string, bool) {
	switch c := c.(type) {
	case Publish:
		return c.Key, true
	case Update:
		return string(c.ID), true
	default:
		return "", false
	}
}

// applyPending takes the queue and runs it in order.
//
// The batch is lifted out under the lock and applied outside it, so an
// application goroutine can carry on queueing while the frame it
// triggered is still being built.
func (w *Window) applyPending() {
	w.inMu.Lock()
	batch := w.pending
	w.pending = nil
	w.barrier = 0
	w.inMu.Unlock()

	if len(batch) > 0 {
		w.ui.stirred = true
		// The commands may ask where the last frame drew things.
		w.ui.paintOwed()
	}
	for _, cmd := range batch {
		w.ui.apply(cmd)
	}
}

// Stats reports what a window has done since it opened.
//
// Read it to check that an application pushing hard is still costing
// one frame per refresh: Commands counts what arrived, Coalesced counts
// what was superseded before it ever reached a view.
type Stats struct {
	Frames    int64
	Commands  int64
	Coalesced int64
}

// Stats returns a snapshot. It is safe from any goroutine.
func (w *Window) Stats() Stats {
	return Stats{
		Frames:    w.stats.frames.Load(),
		Commands:  w.stats.commands.Load(),
		Coalesced: w.stats.coalesced.Load(),
	}
}

type windowStats struct {
	frames    atomic.Int64
	commands  atomic.Int64
	coalesced atomic.Int64
}

// UI is the engine's handle to one window's tree.
//
// It is valid on the UI goroutine, inside a view's update or patch
// function or inside a node's own methods, and only for the length of
// that call.
// Use it and let it go.
type UI struct {
	// injectHeld is the popup an injected press landed in, which takes
	// the pointer's injected events until the button comes up; see
	// Window.inject.
	injectHeld *surface
	// wakeIn is the soonest a resting [Waker] wants a frame again, as
	// this frame's nodes say, and wakeAt and wakeStop the timer that will
	// draw it.
	wakeIn   time.Duration
	wakeAt   time.Time
	wakeStop func()
	// shotsOwed counts the frames a shot waits for, from windows that have yet to draw them.
	shotsOwed atomic.Int32

	w     *Window
	root  *state
	index map[Node]*state
	ids   map[ID]*state
	// topics maps a key to the mounted views watching it. One Publish
	// then reaches the list, the sidebar count and the status line
	// together.
	topics map[string][]*state
	// kept holds what the nodes asked for with KeepDrawing drew last.
	kept map[Node]*Drawing
	// chrome is the title bar of a chromeless window, nil for a window
	// with the system's.
	chrome *titleBar
	// titleBar is the title bar the engine gave the window, or nil; see giveTitleBar.
	titleBar TitleBar
	// underBar says the application draws under the title bar; see [WindowOptions.UnderTitleBar].
	underBar bool
	// fixed says the user cannot size or maximize the window; see [WindowOptions.Fixed].
	fixed bool
	// minSize is the window's least size; see [UI.SetMinSize].
	minSize geom.Size
	// modals are the modals in the tree, in the order they came; see Modal.
	modals []modalHold
	// pinned says the window is kept above other windows; see SetPinned.
	pinned bool
	// keyboardCue says Tab has moved the focus since the last click, which shows the focus rings, and ringed are the
	// nodes told to show one. focusStep is the Step of the next FocusGained.
	keyboardCue bool
	ringed      []ringed
	focusStep   int
	// mods are the modifier keys held, as the last input said, for the intents sent.
	mods input.Mods
	// keyed says a key press is being delivered, so focus it moves is keyed, and userFocus that the user is moving
	// the focus, by a press or through assistive technology.
	keyed     bool
	userFocus bool
	// goingAway says the window is animating out, and leftAt is the frame
	// it began in.
	goingAway bool
	leftAt    time.Time
	// animated says the window arrives and leaves animated, unless
	// [WindowOptions.Instant] asks otherwise.
	animated bool
	// arriving says the window is coming in as it opens, and arrivedAt
	// is when it began: as its first frame reached the screen, or, for
	// a window stepped by [Window.Frame], its first frame.
	arriving  bool
	arrivedAt time.Time
	// fadeOut says the window is fading out, or out, as Client.Fade
	// asked, and faded how far it has gone, from 0 to 1, having gone
	// fadeFrom at the frame fadeAt. away says it has gone whole, and is
	// off the screen.
	fadeOut         bool
	faded, fadeFrom float32
	fadeAt          time.Time
	away            bool
	// spare holds popup windows hidden to open again, and spared counts
	// the ones made ahead of time.
	spare  []spareWindow
	spared int

	focus *state
	// altAlone says Alt is down with nothing pressed since, for [input.AltTapped].
	altAlone bool
	// keyboardAway says the window has given the keyboard to another; see KeyboardAway.
	keyboardAway bool
	hover        *state
	// fingerOn is the node a finger's move last reached with nothing holding it,
	// until it hears the finger has gone. See fingerGone.
	fingerOn *state
	// capture is the node that took the last press and keeps the
	// pointer until its release.
	capture *state
	// current is the node whose Handle, update or patch is running.
	current *state
	// touch is the finger pressing now, and fling the scroll coasting on
	// after one lifted; see touch.go.
	touch *touchPress
	fling *touchFling
	// pinch is two fingers pinching now; see pinch.go.
	pinch *pinchGesture
	// fingers is the node holding each finger down; see fingers.go.
	fingers map[int]*state
	// compassOn says the window has the device's compass running, for a
	// node that watches the heading, and headingNodes holds the nodes in
	// the tree that can watch it, so a frame looks for watchers only
	// while there are some; see heading.go.
	compassOn    bool
	headingNodes map[*state]struct{}
	// edgeSwipeNodes holds the nodes in the tree that can keep edge
	// swipes, so a frame looks for them only while there are some, and
	// edgeSwipes is the bands last handed the window's driver; see
	// edgeswipe.go.
	edgeSwipeNodes map[*state]struct{}
	edgeSwipes     []geom.Rect
	// nativeNodes holds the nodes in the tree a window of the system's covers, each with where it was last told it
	// stands; see native.go.
	nativeNodes map[*state]nativeNode
	// caretAt is the text caret last told to the driver, and boxAt the
	// bounds of the node it is in.
	caretAt, boxAt geom.Rect
	// textState is the focused node's text state last told to the
	// driver, nil for none, and textSent the seq it went with. textSeq
	// is the seq of the last [input.TextEdit] the focus took.
	textState         *input.TextState
	textSent, textSeq uint64
	// firstTyped is the [KeyFirst] that took the last press, when that
	// press typed, until the next press: the text it types goes to it
	// too, and to no one else.
	firstTyped *state

	now time.Time
	// present is when the current frame is expected to light the
	// screen; see [Frame.Present].
	present time.Time
	// theme is the window's live theme, stepped every frame.
	theme *theme.Live
	// seq counts frames; see [Frame].
	seq uint64
	// focusLaid waits for a layout to give a node the keyboard; see [UI.FocusFirstLaidOut].
	focusLaid *laidFocus
	// revealing are the nodes Reveal named, to bring into view once a
	// frame has laid them out and drawn them.
	revealing []*state
	painter   paint.Painter
	animating bool
	invalid   bool
	// unchanged says the patch handler running called Unchanged.
	unchanged bool
	// stirredBy says a command applied since the window last looked
	// needs a frame: any but a patch that changed nothing laid out.
	stirredBy bool
	// due runs a deadline's frame. runTimers invalidates ordinary timers;
	// only Waker deadlines leave unrelated Steady subtrees calm.
	due bool
	// stirred says input, a command or a change of focus came since the
	// last frame, and calm that none did before this one, nor did the
	// theme, a scroll or a drag move: only then are Steady nodes' subtrees
	// carried (steady.go). keptPainted counts the nodes painted for a
	// [Drawing].
	stirred     bool
	calm        bool
	keptPainted int
	// pending holds intents the application has yet to take. See
	// [UI.post] for why it grows instead of blocking or dropping.
	pending []Envelope
	// zoom is the factor the content is drawn larger by, zoomKeys says Ctrl with +, - and 0 or the wheel change it,
	// and zoomNotches holds the wheel's movement short of a step.
	zoom        float32
	zoomKeys    bool
	zoomNotches float32
	// popups are the open popups, each after the one it was opened
	// from.
	popups []*surface
	// timers are waiting to run, in no order.
	timers []*timer
	// closers run as the window closes; see [UI.BeforeClose].
	closers []*func(u *UI)
	// stepped says [Window.Frame] steps the window's time, as a test
	// does: its clock is the steps', and the wall clock's plays no part.
	stepped bool
	// byFrames says everything the window times goes by the time its
	// frames have stepped; see [Window.TimeByFrames].
	byFrames bool
	// onHeld says the window keeps a held clock's time ([HeldClock]), and
	// wasStepped and wasByFrames are how it kept time before.
	onHeld, wasStepped, wasByFrames bool
	// heldSkip says the frame being built under a held clock is laid out and left unpainted; see HoldOptions.PaintEvery.
	heldSkip bool
	// everHeld says the window has kept a held clock's time, so its drivers' windows are told when it no longer does.
	everHeld bool
	// lazy says the window paints a frame only when something looks at
	// it; see [Window.PaintWhenLooked]. owed says the last frame was laid
	// out and left unpainted, and owedFrame is what painting it takes.
	// painting is set while a frame paints.
	lazy, owed, painting bool
	owedFrame            framePaint
	// locals holds what Local stores.
	locals map[any]any
	// pointer is where the pointer was last in the window. drag is the
	// drag the pointer carries from here, and dragAt the node taking a
	// drag over this window.
	pointer geom.Point
	// pointerIn says the pointer rests over the window, at pointer.
	pointerIn bool
	// movedOn is the node the last move landed on, movedTo where in its
	// space, and movedMods the keys held; see hoverAgain.
	movedOn   *state
	movedTo   geom.Point
	movedMods input.Mods
	drag      *drag
	dragAt    *state
	// behind says the button went down on the window as it lay behind
	// another, which left it there, and is still down; dragged says a
	// drag started from that press. See [WindowOptions.DragFromBehind].
	behind, dragged bool
	// drops are the drags let go from here whose end is still to be
	// heard, by number, and dropSeq the last number given. dragOuts are
	// the drops handed to other programs, oldest first, whose ends the
	// platform says in that order.
	drops map[uint64]pendingDrop
	// opened holds the files handed over that no node has taken yet.
	opened   []input.FilesOpened
	dropSeq  uint64
	dragOuts []uint64
	// outDrags are the drags handed to other programs, by drop number, to carry on should the pointer come back.
	outDrags map[uint64]*drag
	// dragOver is set while a drag is over this window, at dragOverAt,
	// carrying dragOverData with dragOverMods held, from the window
	// dragOverFrom.
	dragOver     bool
	dragOverAt   geom.Point
	dragOverData any
	dragOverMods input.Mods
	dragOverFrom *Window
	// edgeLast is the scroll view a drag over this window was last over,
	// which scrolls on while the drag is held past its edge.
	edgeLast *state
	// dragAnswer is what the node under a drag answers as it takes
	// DragOver, and dragAnswered the answer last sent back.
	dragAnswer   any
	dragAnswered any
	// aids counts the IDs given to nodes for assistive technology, and
	// byAID finds a node by its ID, for each tree published, the
	// window's and each popup's, keyed by the tree's root, as of the
	// last time it was published.
	aids  uint64
	byAID map[*state]map[uint64]*state
	// focusMoved is set when focus moves, until the tree has told
	// assistive technology.
	focusMoved bool
	// cursors is the pointer's shape last set on each window.
	cursors map[driver.Window]input.Cursor
}

// Local returns the value this window keeps under key, making it with
// fresh the first time. It is for state that nodes of one kind share
// across a window, such as the elements that fly between screens
// looking for their counterparts. It belongs to the window's UI
// goroutine, as nodes do; call it from Layout, Paint or Transition.
func Local[T any](f Frame, key any, fresh func() T) T {
	u := f.u
	if u == nil {
		return fresh()
	}
	if u.locals == nil {
		u.locals = map[any]any{}
	}
	if v, ok := u.locals[key].(T); ok {
		return v
	}
	v := fresh()
	u.locals[key] = v
	return v
}

// timer is a function waiting for its time.
type timer struct {
	at time.Time
	fn func(u *UI)
	// redraw marks a timer from redrawAt.
	redraw bool
	// wake marks only a Waker deadline; ordinary After timers still invalidate.
	wake bool
	// held marks a timer set while the window kept a held clock's time, which Advance cuts a frame at.
	held bool
}

// redrawAt sets a timer that draws a frame at t, unless one due by then is already set.
func (u *UI) redrawAt(t time.Time) {
	for _, o := range u.timers {
		if o.redraw && !o.at.After(t) {
			return
		}
	}
	u.timers = append(u.timers, &timer{at: t, redraw: true, held: u.onHeld, fn: func(u *UI) { u.Invalidate() }})
}

// After runs fn on the UI goroutine at the first frame d or more after
// this one, and returns a function that cancels it. The window sleeps
// while it waits, so a tooltip's delay costs no frames.
func (u *UI) After(d time.Duration, fn func(u *UI)) (stop func()) {
	t := &timer{at: u.timerClock().Add(d), fn: fn, held: u.onHeld}
	u.timers = append(u.timers, t)
	return func() {
		u.timers = slices.DeleteFunc(u.timers, func(o *timer) bool { return o == t })
	}
}

// BeforeClose runs fn on the UI goroutine as the window closes, unless the function it returns is called first: for
// a node that must end what it started, as a gesture the host waits to hear end. On a nil UI it does nothing.
func (u *UI) BeforeClose(fn func(u *UI)) (stop func()) {
	if u == nil {
		return func() {}
	}
	c := &fn
	u.closers = append(u.closers, c)
	return func() {
		u.closers = slices.DeleteFunc(u.closers, func(o *func(u *UI)) bool { return o == c })
	}
}

// runClosers runs what BeforeClose was given, once.
func (u *UI) runClosers() {
	cs := u.closers
	u.closers = nil
	for _, c := range cs {
		(*c)(u)
	}
}

// wakeAfter has the window draw a frame d from now, for a [Waker]: one
// timer at a time, the earliest asked for.
func (u *UI) wakeAfter(d time.Duration) {
	at := u.timerClock().Add(d)
	if u.wakeStop != nil && !u.wakeAt.After(at) {
		return
	}
	if u.wakeStop != nil {
		u.wakeStop()
	}
	u.wakeAt = at
	// The timer's own frame is the one the node asked for, and the node
	// draws anew in it as it moves; the rest may stay carried.
	t := &timer{at: at, wake: true, held: u.onHeld, fn: func(u *UI) { u.wakeStop = nil }}
	u.timers = append(u.timers, t)
	u.wakeStop = func() { u.timers = slices.DeleteFunc(u.timers, func(o *timer) bool { return o == t }) }
}

// timerClock returns the later of the last frame's time and the wall
// clock's: the frame's time runs a refresh ahead while frames draw, and
// is long past once the window has slept. A window stepped by
// [Window.Frame] keeps the time its steps make alone, so a timer comes
// due after the steps it waits for, however long they take to draw.
func (u *UI) timerClock() time.Time {
	if u.stepped || u.byFrames {
		return u.now
	}
	if now := time.Now(); now.After(u.now) {
		return now
	}
	return u.now
}

// Clock returns the time now, for a node that counts the time between
// events, as an editor that joins quick typing into one step of undo:
// the wall clock's, or, in a window that keeps time by its frames alone
// ([Window.TimeByFrames]), the time its frames have stepped. A nil UI,
// as a frame made outside a window has, reads the wall clock.
func (u *UI) Clock() time.Time {
	if u != nil && u.byFrames {
		return u.now
	}
	return time.Now()
}

// runTimers runs the timers whose time has come.
func (u *UI) runTimers() {
	var due []*timer
	u.timers = slices.DeleteFunc(u.timers, func(t *timer) bool {
		if t.at.After(u.now) {
			return false
		}
		due = append(due, t)
		return true
	})
	if len(due) > 0 {
		// A timer may ask where the last frame drew things.
		u.paintOwed()
	}
	for _, t := range due {
		if !t.wake {
			u.invalid = true
		}
		t.fn(u)
	}
}

// nextTimer returns when the earliest timer is due, and false when
// there is none.
func (u *UI) nextTimer() (time.Time, bool) {
	var at time.Time
	for _, t := range u.timers {
		if at.IsZero() || t.at.Before(at) {
			at = t.at
		}
	}
	return at, !at.IsZero()
}

// Root returns the node at the top of the application's tree, which [Root] names.
func (u *UI) Root() Node { return u.appRoot().node }

// appRoot is the top of the application's tree: the window's root, or the node below the window frame.
func (u *UI) appRoot() *state {
	if s := u.ids[Root]; s != nil {
		return s
	}
	return u.root
}

// Now is the current frame's timestamp.
func (u *UI) Now() time.Time { return u.now }

// Present is when the current frame is expected to light the screen:
// [Frame.Present], for a node that reads it outside Layout and Paint,
// as in Step or WakeIn.
func (u *UI) Present() time.Time { return u.present }

// Theme returns the window's live theme, for reading tokens outside
// Layout and Paint, such as the motion a Handle animates with.
//
// Inside a node's Handle, or a view's update or patch function, it is
// the theme that node is drawn with, which a [ThemeScope] above it may
// set. A nil UI, as a setter is given before its widget is mounted, has none, and tokens read their defaults.
func (u *UI) Theme() *theme.Live {
	if u == nil {
		return nil
	}
	return u.themeOf(u.current)
}

// themeOf returns the theme s is drawn with. A popup's content is drawn
// with the theme of the node that opened it.
func (u *UI) themeOf(s *state) *theme.Live {
	for ; s != nil; s = s.up() {
		if sc, ok := s.node.(ThemeScope); ok {
			return sc.ThemeScope()
		}
	}
	return u.theme
}

// on runs fn with s as the node being served, so Theme finds its scope.
func (u *UI) on(s *state, fn func()) {
	u.stirred = true
	u.ask(s, fn)
}

// ask is on for fn that only asks s, as the pointer's shape: it leaves
// the frame calm, for Steady nodes to be carried while the pointer
// rests on them.
func (u *UI) ask(s *state, fn func()) {
	prev := u.current
	u.current = s
	defer func() { u.current = prev }()
	fn()
}

// UseTheme switches the window to th. Every themed value animates from
// where it is to th's, with th's [theme.Switch] motion.
//
// It sets how long every animation in the program takes from th's
// [theme.Scale].
func (u *UI) UseTheme(th theme.Theme) {
	anim.SetScale(float64(theme.Scale.In(th)))
	u.theme.Use(th)
	u.invalid = true
}

// Clipboard returns the text on the system clipboard, or "" when there
// is none or the display server cannot say.
func (u *UI) Clipboard() string {
	s, err := u.w.dw.Clipboard()
	if err != nil {
		return ""
	}
	return s
}

// ReadClipboard returns the text on the system clipboard, "" with no
// error when it holds none, and an error when it could not be read, so
// an application can tell an empty clipboard from one that failed.
func (u *UI) ReadClipboard() (string, error) { return u.w.dw.Clipboard() }

// SetClipboard puts s on the system clipboard. Where it cannot, as on
// Windows with text that holds a NUL, the application is sent a
// [ClipboardFailed], so a copy that did nothing can be said, wherever in
// the window it was made.
func (u *UI) SetClipboard(s string) {
	if err := u.w.dw.SetClipboard(s); err != nil {
		u.report(ClipboardFailed{Reason: err.Error()})
	}
}

// MarksClipboardSensitive reports whether [UI.SetClipboardSensitive] marks text sensitive on this platform, so the
// system keeps it out of its history: true on Windows. Where it is false, an app may warn the user, or clear the text
// sooner.
func (u *UI) MarksClipboardSensitive() bool {
	if u == nil || u.w == nil {
		return false
	}
	_, ok := u.w.dw.(driver.SensitiveClipboard)
	return ok
}

// SetClipboardSensitive puts s, such as a password, on the system clipboard marked as sensitive, and returns an error
// where it could not.
//
// On Windows the system then keeps s out of the clipboard history (Win+V) and the cloud clipboard, and asks
// clipboard monitors to skip it; where those marks cannot be set, nothing is left on the clipboard. Elsewhere it puts
// s there as [UI.SetClipboard] does, and a clipboard manager may keep it. Pair it with [UI.ClearClipboardIf] to take
// s off again after a while.
func (u *UI) SetClipboardSensitive(s string) error {
	if c, ok := u.w.dw.(driver.SensitiveClipboard); ok {
		return c.SetClipboardSensitive(s)
	}
	return u.w.dw.SetClipboard(s)
}

// ClearClipboardIf empties the system clipboard if it still holds the text s, and reports whether it did: for taking
// a password off once the user has had time to paste it, without clearing what they copied since. An error says the
// clipboard could not be read or emptied.
//
// On Windows it clears only text this program put there. For the text [UI.SetClipboardSensitive] put there last, it
// goes by ownership and reads nothing: the text is still ours while this program owns the clipboard and the
// sensitive mark is there, so a paste or a format another program adds leaves it ours, and the user's own later copy
// of the same text, made elsewhere, survives. Other text is read and compared, but only while this program owns the
// clipboard, so another program's text is never read and the call never waits on it. Nothing can reach the clipboard
// between the check and the emptying. Elsewhere the clipboard is read and then set to "", so text another program
// puts there in that moment can be lost.
func (u *UI) ClearClipboardIf(s string) (bool, error) {
	if c, ok := u.w.dw.(driver.SensitiveClipboard); ok {
		return c.ClearClipboardIf(s)
	}
	now, err := u.w.dw.Clipboard()
	if err != nil || now != s {
		return false, err
	}
	if err := u.w.dw.SetClipboard(""); err != nil {
		return false, err
	}
	return true, nil
}

// ClipboardFailed travels to the application when [UI.SetClipboard]
// could not put text on the clipboard, with the error in Reason.
type ClipboardFailed struct{ Reason string }

func init() { RegisterType[ClipboardFailed]("gunim.clipboard-failed") }

// ClipboardImage returns the picture on the clipboard as PNG, or nil when it holds none or the platform cannot read
// pictures from it.
func (u *UI) ClipboardImage() ([]byte, error) {
	c, ok := u.w.dw.(driver.ImageClipboard)
	if !ok {
		return nil, nil
	}
	return c.ClipboardImage()
}

// SetClipboardImage puts the picture png, a PNG file's bytes, on the system clipboard in place of what it held, as
// copying a picture in another program does. It returns [driver.ErrNoClipboardImage] where the platform cannot put
// a picture there. [Client.SetClipboardImage] does the same from the application's side.
func (u *UI) SetClipboardImage(png []byte) error { return setClipboardImage(u.w.dw, png) }

// SetTitle changes the window's title, where the platform can.
func (u *UI) SetTitle(title string) {
	if t, ok := u.w.dw.(driver.Titler); ok {
		t.SetTitle(title)
	}
	if u.titleBar != nil {
		u.titleBar.SetTitle(title)
		u.invalid = true
	}
}

// SetBorder sets the thin line round a chromeless window's edge, as [WindowOptions.Border] does, such as to follow the
// application's theme.
func (u *UI) SetBorder(b driver.Border) {
	if d, ok := u.w.dw.(driver.Borderer); ok {
		d.SetBorder(b)
	}
}

// SetFullScreen makes the window fill its monitor, or gives it back
// its frame, where the platform can.
func (u *UI) SetFullScreen(on bool) {
	if f, ok := u.w.dw.(driver.FullScreener); ok {
		f.SetFullScreen(on)
	}
}

// FullScreen reports whether the window fills its monitor.
func (u *UI) FullScreen() bool {
	f, ok := u.w.dw.(driver.FullScreener)
	return ok && f.FullScreen()
}

// RequestAttention asks for the user's attention without taking the
// keyboard, where the platform can: it flashes the window in the task
// bar, or bounces it in the dock.
func (u *UI) RequestAttention() {
	if a, ok := u.w.dw.(driver.Attender); ok {
		a.RequestAttention()
	}
}

// ToFront brings the window to the front with the keyboard, shown again
// first if it was minimized, where the platform can.
func (u *UI) ToFront() {
	if f, ok := u.w.dw.(driver.Fronter); ok {
		f.ToFront()
	}
}

// Blends reports whether a popup's window blends with what is behind
// it, so the desktop shows wherever the popup paints nothing. It is a
// guess, true, until the first popup's window has opened; one is made
// ahead of time as the window first shows.
func (u *UI) Blends() bool { return u.w.blends }

// Invalidate asks for one more frame, whatever the animation state. Use
// it when something changed that the engine can see no other way. On a nil UI, as a setter is given before its
// widget is mounted, it does nothing: the first frame draws the widget anyway.
func (u *UI) Invalidate() {
	if u != nil {
		u.invalid = true
	}
}

// Unchanged says, from a patch's handler (see [RegisterPatch]), that
// the patch changed nothing the window lays out or paints but springs,
// which step and draw what moves themselves: the window builds no frame
// for it. A value that is shown, set without a spring, needs the frame.
// Anywhere else it does nothing.
func (u *UI) Unchanged() {
	if u != nil {
		u.unchanged = true
	}
}

// Send reports an intent from n to the application.
//
// The intent travels as a value, so the widget stays ignorant of the
// application and the application stays off this goroutine. From is
// filled in with the ID of the nearest mounted view above n, which is
// how the application knows which of three open dialogs answered.
//
// Send returns at once. It reports whether n was in the tree: a node
// that has left, as one a timer or a late update still names, sends
// with no ID, and in an offscreen window counts as a [Stray]. A nil n
// sends from no node, and reports true.
func (u *UI) Send(n Node, v Intent) bool {
	_, ok := u.index[n]
	if !ok && n != nil {
		u.stray("Send", n)
	}
	u.post(Envelope{From: u.idOf(n), Intent: v, Mods: u.mods})
	return ok || n == nil
}

// report sends a gunim-generated intent.
func (u *UI) report(v Intent) { u.post(Envelope{Intent: v, Mods: u.mods}) }

// post queues an envelope for the application.
//
// The queue is unbounded because intents are generated at human speed,
// so it stays short in practice, and because the alternatives are both
// worse: blocking here would freeze every animation in the window, and
// dropping would lose the click that mattered.
func (u *UI) post(e Envelope) {
	u.pending = append(u.pending, e)
	u.flush()
}

// flush hands as much of the queue to the application as it will take
// right now.
func (u *UI) flush() {
	for len(u.pending) > 0 {
		select {
		case u.w.out <- u.pending[0]:
			u.pending = u.pending[1:]
		default:
			return
		}
	}
}

// idOf finds the ID of the nearest mounted view at or above n.
func (u *UI) idOf(n Node) ID {
	for s := u.index[n]; s != nil; s = s.up() {
		if s.id != "" {
			return s.id
		}
	}
	return ""
}

// Insert adds child to the end of parent's children and starts its
// entrance.
//
// Inserting a node that is already in the tree leaves it in place when
// parent already holds it, and moves it to the end of parent's children
// otherwise. Either way, a node that is leaving reverses: it goes back
// to [Entering] and keeps the animation it was part way through, and so
// does everything beneath it that was leaving with it. Because springs
// carry their velocity, a dialog dismissed and immediately reopened
// swings smoothly back. Getting that for free is the point of the whole
// design.
func (u *UI) Insert(parent, child Node) {
	u.InsertAt(parent, -1, child)
}

// InsertAt adds child at index i, or at the end when i is negative.
//
// A child already in the tree moves to index i under parent, where i
// counts parent's children with child taken out. With a negative i it
// keeps its place when parent already holds it. Inserting a node into
// its own subtree panics.
func (u *UI) InsertAt(parent Node, i int, child Node) {
	ps, ok := u.index[parent]
	if !ok {
		panic("gunim: Insert into a node that is not in the tree")
	}
	assertAddressable(child)
	if cs, ok := u.index[child]; ok {
		if cs.node != child {
			panic("gunim: Insert of a node embedded in another node in the tree")
		}
		wasLeaving := cs.leaving()
		if u.move(cs, ps, i) {
			u.invalid = true
		}
		if wasLeaving {
			reenter(cs)
			u.invalid = true
			u.rearrived(cs)
		}
		return
	}
	cs := &state{node: child, parent: ps, presence: Entering}
	ps.kids = insertKid(ps.kids, i, cs)
	u.index[child] = cs
	u.noteHeadingNode(cs)
	u.noteEdgeSwipeNode(cs)
	u.noteNativeNode(cs)
	u.invalid = true
	if m, ok := child.(Modal); ok && m.Modal() {
		u.Cue(CueOpen, nil)
	}

	// A composite arrives whole, so a view can return one node and get
	// the widget it describes.
	if c, ok := child.(Composite); ok {
		for _, k := range c.Children() {
			u.Insert(child, k)
		}
	}
	// A node embedded in child stands for child, so a method it promotes can name itself
	for _, e := range embedded(child) {
		if _, taken := u.index[e]; !taken {
			u.index[e] = cs
			cs.aliases = append(cs.aliases, e)
		}
	}
	u.arrived(cs)
}

// move puts s at index i under ps and reports whether it moved. A
// negative i leaves s where it is when ps already holds it.
func (u *UI) move(s, ps *state, i int) bool {
	if s.parent == ps && i < 0 {
		return false
	}
	if ps.within(s) {
		panic("gunim: Insert would put a node inside its own subtree")
	}
	s.parent.kids = slices.DeleteFunc(s.parent.kids, func(k *state) bool { return k == s })
	s.parent = ps
	ps.kids = insertKid(ps.kids, i, s)
	return true
}

func insertKid(kids []*state, i int, s *state) []*state {
	if i < 0 || i > len(kids) {
		return append(kids, s)
	}
	return slices.Insert(kids, i, s)
}

// reenter turns s and the part of its subtree that was leaving with it
// back to [Entering]. A descendant removed in its own right stays
// [Exiting].
func reenter(s *state) {
	s.presence = Entering
	for _, k := range s.kids {
		if k.presence != Exiting {
			reenter(k)
		}
	}
}

// Remove starts n's exit.
//
// n stays in the tree. It moves to [Exiting], keeps laying out and
// painting, and leaves once it and everything beneath it reports
// settled. A plain node goes at once; one with a [Transitioner] gets
// exactly as long as it asks for.
//
// Input skips a leaving node, so focus and hover inside n end here,
// with [input.FocusLost] and [input.PointerLeave] sent as usual.
//
// Remove reports whether n was in the tree. Removing a node that has
// already left, as a timer that closes a toast the user closed first
// does, is common and harmless, and does nothing.
func (u *UI) Remove(n Node) bool {
	s, ok := u.index[n]
	if !ok {
		return false
	}
	if s == u.root {
		return true
	}
	if m, ok := n.(Modal); ok && m.Modal() && s.presence != Exiting {
		u.Cue(CueClose, nil)
	}
	s.presence = Exiting
	u.invalid = true
	u.closePopupsOf(s)
	if u.focus != nil && u.focus.within(s) {
		u.Focus(nil)
	}
	if u.hover != nil && u.hover.within(s) {
		for h := u.hover; h != s.parent; h = h.parent {
			u.deliver(h, input.PointerLeave{Time: u.now})
		}
		u.hover = s.parent
		if s.parent != nil && s.parent.parent == nil {
			u.hover = nil // a root is never hovered
		}
	}
	if u.fingerOn != nil && u.fingerOn.within(s) {
		u.fingerGone(u.now)
	}
	if u.capture != nil && u.capture.within(s) {
		u.capture = nil
	}
	u.losePinch(s)
	u.loseFingers(s)
	u.left()
	return true
}

// Bounds returns the box n was last drawn in, in the window's logical
// pixels, and false when the last frame drew it nowhere, as for a node
// out of the tree or under a popup's window.
func (u *UI) Bounds(n Node) (geom.Rect, bool) {
	s, ok := u.index[n]
	if !ok || s.drawn == 0 || s.drawn != u.seq || u.surfaceOf(s) != nil {
		return geom.Rect{}, false
	}
	return u.rectIn(s, u.root), true
}

// Drawn reports whether n's parent painted it in the last frame, in the window or in a popup's. A node clipped away or
// covered by another reads true. It reads false before the window's first frame, during a frame until the node is
// painted, and on a nil UI.
func (u *UI) Drawn(n Node) bool {
	if u == nil {
		return false
	}
	s, ok := u.index[n]
	return ok && s.drawn != 0 && s.drawn == u.seq
}

// ScrollsAround reports whether a [WheelScroller] around n takes a wheel turned by delta, for a node that leaves the
// wheel to the view it scrolls in. It is false on a nil UI and for a node out of the tree.
func (u *UI) ScrollsAround(n Node, delta geom.Point) bool {
	if u == nil {
		return false
	}
	s, ok := u.index[n]
	if !ok {
		return false
	}
	for s = s.parent; s != nil; s = s.parent {
		if w, ok := s.node.(WheelScroller); ok && w.ScrollsAlong(delta) {
			return true
		}
	}
	return false
}

// Presence reports where n is in its lifecycle.
func (u *UI) Presence(n Node) Presence {
	if s, ok := u.index[n]; ok {
		return s.presence
	}
	return Exiting
}

// Focused returns the node with keyboard focus, or nil.
func (u *UI) Focused() Node {
	if u.focus == nil {
		return nil
	}
	return u.focus.node
}

// FocusTakesText reports whether the node with keyboard focus takes typed text, as a text field does.
func (u *UI) FocusTakesText() bool { return takingText(u.focus) }

// HasFocus reports whether the keyboard is on n or on a node inside it.
func (u *UI) HasFocus(n Node) bool {
	s, ok := u.index[n]
	return ok && u.focus != nil && u.focus.within(s)
}

// Focus moves keyboard focus to n, sending [input.FocusLost] and
// [input.FocusGained] to the nodes concerned. Pass nil to drop focus.
//
// A node that is leaving takes no input, so focusing one does nothing.
// Focus reports whether n took focus: false for a node leaving, or out
// of the tree, which leaves focus where it was, and in an offscreen
// window counts as a [Stray]. It is false too for a node a [Modal] keeps
// out, which takes the focus once the modal leaves, unless the user asked.
func (u *UI) Focus(n Node) bool {
	var next *state
	if n != nil {
		next = u.index[n]
		if next == nil {
			u.stray("Focus", n)
			return false
		}
		if next.leaving() {
			return false
		}
		// A modal keeps the keyboard. One in a scope takes it for what is
		// focused under it from outside the scope.
		if h, ok := u.holdFor(next); ok && !inside(next, h.s) {
			if !u.userFocus {
				u.focusAfter(h, next)
			}
			if h.scope == nil || u.focus != nil && inside(u.focus, h.scope) {
				return false
			}
			// Coming into the scope from outside it.
			next, n = h.s, h.s.node
		}
	}
	if next == u.focus {
		return true
	}
	// Focused from code, a modal already holding the keyboard, as a dialog that gave it to its first field, keeps it
	// where it is
	if m, ok := n.(Modal); ok && m.Modal() && !u.userFocus && u.focus != nil && u.focus.within(next) {
		return true
	}
	if u.focus != nil {
		u.deliver(u.focus, input.FocusLost{Time: u.now})
	}
	prev := u.focus
	u.focus = next
	u.focusMoved = true
	// Each group round the node remembers it as its stop for Tab
	grouped := false
	for a := next; a != nil; a = a.parent {
		if _, ok := a.node.(TabGroup); ok {
			a.tabStop = next
			grouped = grouped || a != next
		}
	}
	u.takeText(prev, next)
	if next != nil {
		u.deliver(next, input.FocusGained{Keyed: u.keyed, Step: u.focusStep, Grouped: grouped, Time: u.now})
		// The nodes round it that it has come into, innermost first.
		for a := next.parent; a != nil; a = a.parent {
			if prev != nil && prev.within(a) {
				break
			}
			u.deliver(a, input.FocusEntered{Time: u.now})
		}
	}
	u.ring()
	u.invalid = true
	return true
}

// ringed is a node told to show its focus ring, and how.
type ringed struct {
	s  *state
	ev input.FocusRing
}

// ring tells the nodes that show the focus, the focused node and the outermost tab group round it, to show it while
// the keyboard is in use, and the ones that no longer do to stop.
func (u *UI) ring() {
	var want []ringed
	if u.keyboardCue && u.focus != nil {
		var group *state
		for a := u.focus.parent; a != nil; a = a.parent {
			if _, ok := a.node.(TabGroup); ok {
				group = a
			}
		}
		want = append(want, ringed{u.focus, input.FocusRing{On: true, Grouped: group != nil}})
		if group != nil {
			want = append(want, ringed{group, input.FocusRing{On: true, Within: true}})
		}
	}
	for _, r := range u.ringed {
		if !slices.ContainsFunc(want, func(w ringed) bool { return w == r }) {
			u.deliver(r.s, input.FocusRing{Within: r.ev.Within, Time: u.now})
		}
	}
	for _, w := range want {
		if !slices.Contains(u.ringed, w) {
			ev := w.ev
			ev.Time = u.now
			u.deliver(w.s, ev)
		}
	}
	u.ringed = want
}

// ShowFocusRing shows the focus rings, as Tab does, until the next
// click: for a widget that moves the keyboard somewhere the user has to
// see, such as a dialog that opens on Cancel.
func (u *UI) ShowFocusRing() { u.showRings(true) }

// showRings says whether the focus rings show: on from Tab, and off from a click.
func (u *UI) showRings(keyboard bool) {
	if u.keyboardCue == keyboard {
		return
	}
	u.keyboardCue = keyboard
	u.ring()
	u.invalid = true
}

// takeText tells the driver whether the newly focused node takes typed
// text, so the input method composes into the window only then. A
// composition belongs to the node it was typed into, so focus moving
// between two nodes that take text turns text input off on the way,
// which ends it. The new node's text state reaches the driver before
// text input turns on, so the input method starts from it.
func (u *UI) takeText(prev, next *state) {
	// The driver forgets the caret and its box as text input turns off,
	// so the next node to take text tells them afresh, though they are
	// where they were.
	u.caretAt, u.boxAt = geom.Rect{}, geom.Rect{}
	ti, ok := u.w.dw.(driver.TextInputter)
	takes := takingText(next)
	if ok && takes && takingText(prev) {
		ti.SetTextInput(false)
	}
	u.syncText(next, true)
	if ok {
		ti.SetTextInput(takes)
	}
}

// syncText tells the driver the state of s's text when s is a
// [TextEditor] that takes text, and nil when s is something else. It
// tells only a change since the driver last heard, unless fresh says s
// has just taken the focus: a text node taking the focus always starts
// the input method over, though its text matches the last one's.
func (u *UI) syncText(s *state, fresh bool) {
	ts, ok := u.w.dw.(driver.TextStater)
	if !ok {
		return
	}
	var st *input.TextState
	if s != nil {
		if ed, ok := s.node.(TextEditor); ok && ed.TakesText() {
			v := ed.TextState()
			st = &v
		}
	}
	same := st == nil && u.textState == nil ||
		st != nil && u.textState != nil && *st == *u.textState && u.textSent == u.textSeq
	if same && (!fresh || st == nil) {
		return
	}
	u.textState, u.textSent = st, u.textSeq
	ts.SetTextState(st, u.textSeq)
}

// askKeyboard shows the on-screen keyboard, where the window has one,
// for a press let go on hit, the focused node or inside it, while that
// node takes text: a tap on a text field asks for the keyboard, though
// the field had the focus already. It asks as the press is let go, so a
// finger that starts a scroll on a field, whose press is let go away
// from it, leaves the keyboard as it was.
func (u *UI) askKeyboard(hit *state) {
	ks, ok := u.w.dw.(driver.KeyboardShower)
	if !ok || hit == nil || u.focus == nil || !hit.within(u.focus) || !takingText(u.focus) {
		return
	}
	ks.ShowKeyboard()
}

// takingText reports whether s is a node that takes typed text.
func takingText(s *state) bool {
	if s == nil {
		return false
	}
	t, ok := s.node.(TextTaker)
	return ok && t.TakesText()
}

// placeCaret tells the driver where the focused node's text caret is,
// in window space, when it has moved, and where the node itself is, for
// a driver that keeps the whole of it in view.
func (u *UI) placeCaret() {
	cp, ok := u.w.dw.(driver.CaretPlacer)
	if !ok || u.focus == nil {
		return
	}
	cr, ok := u.focus.node.(CaretReporter)
	if !ok || !cr.TakesText() {
		return
	}
	r := cr.TextCaret()
	f := u.focus
	at := geom.Rect{Min: f.screenAt(r.Min), Max: f.screenAt(r.Max)}.Normalized()
	if bp, ok := u.w.dw.(driver.TextBoxPlacer); ok {
		box := f.screenRect(geom.Rect{Max: f.size.Point()})
		if box != u.boxAt {
			u.boxAt = box
			bp.SetTextBox(box)
		}
	}
	if at != u.caretAt {
		u.caretAt = at
		cp.SetTextCaret(at)
	}
}

// springsMoving reports whether any spring moves, looking without moving
// time on, as a frame looks again at its end: a patch's handler may have
// started one.
func (u *UI) springsMoving() bool {
	if u.theme.Step(0) {
		return true
	}
	for _, r := range u.roots() {
		if u.step(r, 0) {
			return true
		}
	}
	return false
}

// needsFrame reports whether there is anything to draw.
func (u *UI) needsFrame() bool { return u.animating || u.invalid || u.due }

// frame runs one complete cycle: advance time, settle the tree, lay
// out, paint, present.
func (u *UI) frame(now, present time.Time, delta time.Duration) {
	u.now, u.present = now, present
	// Timers run first, so what they invalidate is drawn in this frame
	u.runTimers()
	u.calm = !u.invalid && !u.stirred && !u.dragOver
	u.stirred = false
	u.invalid = false
	u.due = false
	if u.shotsOwed.Load() > 0 {
		// A shot waits on the windows' next frames: keep drawing them.
		u.invalid = true
	}
	u.flush()
	// Files no node took yet: a view just mounted may take them.
	u.offerOpened()
	// Scroll what a held drag is near the edge of, while the nodes are
	// still where the last frame drew them, to find what the pointer is
	// over.
	scrolled := u.edgeScroll(delta)
	scrolled = u.stepFling(delta) || scrolled
	if !scrolled && u.dragOver {
		// Offer a resting drag again, as what lies under it may have changed
		u.dragHover(u.dragOverAt, u.dragOverData)
	}
	u.seq++
	f := Frame{Now: now, Present: present, Delta: delta, Scale: u.w.dw.Scale(), Theme: u.theme, seq: u.seq, u: u}
	if sa, ok := u.w.dw.(driver.SafeAreaer); ok {
		f.Safe = sa.SafeArea()
	}
	if kc, ok := u.w.dw.(driver.KeyboardCoverer); ok {
		f.Keyboard = kc.KeyboardCover()
	}

	// 1. Advance every animated value by the real elapsed time, the
	//    theme's included.
	animating := u.theme.Step(delta) || scrolled
	u.calm = u.calm && !animating
	for _, r := range u.roots() {
		if u.step(r, delta) {
			animating = true
		}
	}

	// 2. Let entering and exiting nodes run their transitions, then
	//    unlink the ones that have finished leaving.
	for _, r := range u.roots() {
		fr := f
		if r.opener != nil {
			fr.Theme = u.themeOf(r.opener)
		}
		if !u.settle(r, Present, fr) {
			animating = true
		}
		u.reap(r)
	}

	u.animating = animating

	// 3. Lay the tree out at the window's current size.
	size := u.w.dw.Size()
	f = scoped(f, u.root.node)
	u.root.laid = u.seq
	u.root.size = u.root.node.Layout(Tight(size), f, Children{ns: u.root.kids, f: f, s: u.root})
	// A node asked to take the keyboard once laid out takes it now, so this frame paints it with the keyboard.
	u.focusLaidOut()

	// 4. Record the frame and hand it to the driver, or, for a window
	//    that paints only when looked at, keep what painting it takes.
	fp, left := u.fading(now, f, size)
	if u.mustPaint() {
		u.paintFrame(fp)
	} else {
		u.owed, u.owedFrame = true, fp
		u.fadedAway()
		if u.heldSkip {
			u.layoutPopups(f)
		}
	}

	// 5. Layout and paint may have started animations: a caret aimed at
	//    a new place, a row sent to a new position. Look again, without
	//    moving time on, so the window keeps drawing until they finish.
	//    Going by step 1 alone, the window would sleep, and the motion
	//    would wait for the next input to get going.
	if !u.animating && u.theme.Step(0) {
		u.animating = true
	}
	for _, r := range u.roots() {
		if !u.animating && u.step(r, 0) {
			u.animating = true
		}
	}
	// A node at rest that wants a frame later has one then, and the
	// window sleeps till it.
	if !u.animating && u.wakeIn > 0 {
		u.wakeAfter(u.wakeIn)
	}
	u.wakeIn = 0

	// Counted last, so a reader that sees the count also sees the frame.
	u.w.stats.frames.Add(1)
	if left {
		u.w.Close()
	}
}

// framePaint is what painting a frame laid out takes: the frame, the
// window's size, and how far the window is from being all there.
type framePaint struct {
	f    Frame
	size geom.Size
	// gone is how far the window is from being all there, scale what it
	// shrinks to, and whole says it fades whole, inside its shape, which
	// has radius and edge.
	gone, scale, radius, edge float32
	whole                     bool
}

// fading moves the window's coming, going and fading on to the frame
// stamped now, and tells the driver how far it has faded. It reports
// what painting the frame takes, and whether the window has now left.
func (u *UI) fading(now time.Time, f Frame, size geom.Size) (fp framePaint, left bool) {
	// gone is how far the window is from being all there: coming in
	// as it opens, or going as it leaves.
	gone := float32(0)
	if u.arriving {
		// The window comes in once its first frame, drawn all gone,
		// is on the screen: the system may take a while to show it,
		// and a window shown late would be most of the way in.
		if u.arrivedAt.IsZero() && u.stepped {
			u.arrivedAt = now
		}
		k := float32(0)
		if !u.arrivedAt.IsZero() {
			k = min(float32(now.Sub(u.arrivedAt))/float32(ArriveTime), 1)
		}
		u.arriving = k < 1
		u.animating = true
		// Eased out: quick to show, settling into place.
		gone = (1 - k) * (1 - k)
	}
	if u.goingAway {
		k := u.leftBy(now)
		left = k >= 1
		u.animating = true
		// Eased in: slow to start, gone quickly.
		gone = max(gone, k*k)
	}
	// A window stepping aside fades where it stands, and shrinks no
	// more than leaving or arriving makes it: it comes back exactly
	// where it was, as what stood in for it meanwhile lands there.
	shrink := gone
	if k := u.fadeBy(now); k > 0 {
		gone = max(gone, k*k)
	}
	// The window's background shows where the tree paints nothing, such
	// as the gap beside a split's handle. Where the window can show what
	// is behind it, a fading window draws it itself, inside its shape,
	// so the window fades and shrinks whole: background, corners, border
	// and shadow. Elsewhere it fades into the dark.
	var radius, edge float32
	blends := false
	if o, ok := u.w.dw.(driver.Outliner); ok {
		radius, edge, blends = o.Outline()
	}
	whole := gone > 0 && blends
	if b, ok := u.w.dw.(driver.Backgrounder); ok {
		c := WindowBackground.Get(f.Theme)
		c.A = uint8(float32(c.A)*(1-gone) + 0.5)
		if whole {
			c.A = 0
		}
		b.SetBackground(c)
	}
	scale := 1 - leaveShrink*shrink
	if fd, ok := u.w.dw.(driver.Fader); ok {
		fd.SetFade(1-gone, scale)
	}
	return framePaint{f: f, size: size, gone: gone, scale: scale, radius: radius, edge: edge, whole: whole}, left
}

// mustPaint reports whether this frame paints. A window that paints when
// looked at paints only for a shot, and while what it does next goes by
// where it drew things: a node to bring into view, a popup or a native
// child to place, and the pointer held, carrying a drag, or flinging.
func (u *UI) mustPaint() bool {
	if u.heldSkip {
		return false
	}
	return !u.lazy || u.shotsOwed.Load() > 0 || len(u.revealing) > 0 || len(u.popups) > 0 || len(u.nativeNodes) > 0 ||
		u.capture != nil || u.drag != nil || u.dragOver || u.fling != nil
}

// paintFrame paints the frame laid out, as fp says, hands it to the
// driver, and does what goes by where it drew things: hover, the caret,
// popups, the accessibility tree.
func (u *UI) paintFrame(fp framePaint) {
	u.painting = true
	defer func() { u.painting = false }()
	// This frame is the newest painted: an older one owed is owed no more.
	u.owed, u.owedFrame = false, framePaint{}
	f, size := fp.f, fp.size
	u.painter.Reset()
	u.beginTitleBar()
	u.root.toWindow, u.root.drawn = paint.Identity, u.seq
	func() {
		if gone := fp.gone; gone > 0 {
			box := geom.Rect{Max: size.Point()}
			defer u.painter.Push(paint.Scale(fp.scale, box.Center()))()
			if fp.whole {
				shape := box.Inset(geom.Uniform(fp.edge))
				defer u.painter.Layer(paint.LayerOpts{Bounds: shape, Opacity: 1 - gone, Clip: true, Radius: max(0, fp.radius-fp.edge)})()
				u.painter.RRect(shape, 0, paint.Solid(WindowBackground.Get(f.Theme)))
			} else {
				defer u.painter.Layer(paint.LayerOpts{Bounds: box, Opacity: 1 - gone})()
			}
		}
		u.root.node.Paint(&u.painter, f, u.root.size, Children{ns: u.root.kids, f: f, s: u.root})
		u.painter.PaintFloats()
	}()
	u.stampClock(u.w.dw)
	if err := u.w.dw.Present(u.painter.Ops(), u.painter.DamageRects()); err != nil {
		u.w.err = fmt.Errorf("gunim: present frame: %w", err)
		u.w.Close()
	}
	u.fadedAway()
	u.placeNatives(f.Scale)
	u.sendTitleBar()
	u.placeCaret()
	u.syncText(u.focus, false)
	u.hoverAgain(f.Now)
	u.framePopups(f)
	u.revealDrawn()
	u.watchHeading()
	u.keepEdgeSwipes()
	u.publishAccess(u.w.dw, u.root, u.w.title)
	u.focusMoved = false
	for _, s := range u.popups {
		if s.dw != nil {
			u.publishAccess(s.dw, s.root, "")
		}
	}
}

// paintOwed paints the frame last laid out, where the window left it
// unpainted, before anything goes by where it drew things. It paints
// the frame as the frame itself would have: nothing has changed since.
func (u *UI) paintOwed() {
	if !u.owed || u.painting {
		return
	}
	if u.onHeld && u.w.app != nil {
		// Held, a frame owed paints only into buffers the driver is done with, else the next painted frame shows
		// it, and one painted is in flight as a frame drawn is.
		if !u.w.free() {
			return
		}
		// Asked for as a frame is built, the frame owed and the one built are two frames painted on one check
		// for room. The painter keeps two frames' buffers, so both have room only once the driver has let every
		// frame go; else the second would record into ops the driver still reads.
		if u.w.building && u.w.unreleased > 0 {
			return
		}
		u.paintFrame(u.owedFrame)
		u.w.unshown++
		u.w.unreleased++
		return
	}
	u.paintFrame(u.owedFrame)
}

// leaveShrink is how much of its size a window's content loses as it
// leaves.
const leaveShrink = 0.06

// startLeaving starts the window leaving: see [Client.Leave].
func (u *UI) startLeaving() {
	if u.goingAway {
		return
	}
	u.goingAway, u.invalid = true, true
	u.closeAllPopups()
}

// setFade starts the window fading out, or back in: see [Client.Fade].
func (u *UI) setFade(out bool) {
	if out == u.fadeOut {
		return
	}
	u.fadeOut, u.invalid = out, true
	u.fadeFrom, u.fadeAt = u.faded, time.Time{}
	if out {
		u.closeAllPopups()
		return
	}
	if u.away {
		// On the screen again, as it was last drawn: gone whole.
		u.away = false
		if c, ok := u.w.dw.(driver.Cloaker); ok {
			c.SetCloaked(false)
		}
	}
}

// fadeMoving reports whether the window is fading, out or in.
func (u *UI) fadeMoving() bool {
	return u.fadeOut && u.faded < 1 || !u.fadeOut && u.faded > 0
}

// fadeBy is how far the window has faded, from 0 to 1, at the frame
// stamped now, moving it on toward where Client.Fade asked.
func (u *UI) fadeBy(now time.Time) float32 {
	if !u.fadeMoving() {
		return u.faded
	}
	if u.fadeAt.IsZero() {
		u.fadeAt = now
	}
	step := float32(now.Sub(u.fadeAt)) / float32(LeaveTime)
	if u.fadeOut {
		u.faded = min(u.fadeFrom+step, 1)
	} else {
		u.faded = max(u.fadeFrom-step, 0)
	}
	u.animating = true
	return u.faded
}

// fadedAway takes a window that has faded out whole off the screen, once
// that last frame has gone to it.
func (u *UI) fadedAway() {
	if !u.fadeOut || u.faded < 1 || u.away {
		return
	}
	u.away = true
	if c, ok := u.w.dw.(driver.Cloaker); ok {
		c.SetCloaked(true)
	}
}

// leftBy is how far the window has gone in leaving, from 0 to 1, at the
// frame stamped now.
func (u *UI) leftBy(now time.Time) float32 {
	if u.leftAt.IsZero() {
		u.leftAt = now
	}
	return min(float32(now.Sub(u.leftAt))/float32(LeaveTime), 1)
}

// roots returns the top of the window's tree and of every popup's.
func (u *UI) roots() []*state {
	rs := make([]*state, 0, 1+len(u.popups))
	rs = append(rs, u.root)
	for _, p := range u.popups {
		rs = append(rs, p.root)
	}
	return rs
}

// step advances animated values depth-first and reports whether
// anything is still moving.
func (u *UI) step(s *state, dt time.Duration) bool {
	animating := false
	if a, ok := s.node.(Animator); ok && a.Step(dt) {
		animating = true
		s.moved = u.seq
	}
	if w, ok := s.node.(Waker); ok {
		d := w.WakeIn()
		if d > 0 {
			// Resting, it may still change as it is drawn, as a graph
			// sliding on: Steady nodes round it are drawn anew.
			s.moved = u.seq
		}
		if d > 0 && (u.wakeIn == 0 || d < u.wakeIn) {
			u.wakeIn = d
		}
	}
	for _, k := range s.kids {
		if u.step(k, dt) {
			animating = true
		}
	}
	return animating
}

// settle runs each node's transition and records whether it has
// finished. A node inherits Exiting from its ancestors: when a panel
// leaves, everything inside it is leaving too, and the panel waits for
// its children to agree they are done.
func (u *UI) settle(s *state, inherited Presence, f Frame) bool {
	f = scoped(f, s.node)
	p := s.presence
	if inherited == Exiting {
		p = Exiting
	}
	settled := true
	for _, k := range s.kids {
		if !u.settle(k, p, f) {
			settled = false
		}
	}
	if t, ok := s.node.(Transitioner); ok && p != Present {
		if !t.Transition(p, f) {
			settled = false
		}
	}
	if p == Entering && settled {
		s.presence = Present
	}
	s.settled = settled
	return settled
}

// reap unlinks exiting subtrees that have finished animating out.
func (u *UI) reap(s *state) {
	kept := s.kids[:0]
	for _, k := range s.kids {
		if k.presence == Exiting && k.settled {
			u.forget(k)
			continue
		}
		u.reap(k)
		kept = append(kept, k)
	}
	clear(s.kids[len(kept):])
	s.kids = kept
}

// forget drops a subtree from the index and gives up any focus or
// hover it held.
func (u *UI) forget(s *state) {
	u.closePopupsOf(s)
	if u.focus == s {
		u.focus = nil
	}
	if u.hover == s {
		u.hover = nil
	}
	if u.fingerOn == s {
		u.fingerOn = nil
	}
	if u.movedOn == s {
		u.movedOn = nil
	}
	if u.capture == s {
		u.capture = nil
	}
	u.losePinch(s)
	u.loseFingers(s)
	if s.id != "" && u.ids[s.id] == s {
		delete(u.ids, s.id)
	}
	u.unsubscribe(s)
	delete(u.headingNodes, s)
	delete(u.edgeSwipeNodes, s)
	u.forgetNativeNode(s)
	delete(u.index, s.node)
	for _, a := range s.aliases {
		if u.index[a] == s {
			delete(u.index, a)
		}
	}
	s.aliases = nil
	for _, k := range s.kids {
		u.forget(k)
	}
	s.kids = nil
	s.parent = nil
}

// assertAddressable requires nodes the tree can tell apart.
//
// The tree is keyed on the Node interface value, which is what lets
// UI.Remove take the node itself and spares the caller a handle to
// keep. That holds as long as distinct nodes are distinct values. Go
// promises that for pointers to types of non-zero size; two separate
// &Box{} values may legitimately share an address, at which point one
// silently becomes the other. That is a baffling bug to meet at frame
// 400, so it is caught here, once, at the point of the mistake.
func assertAddressable(n Node) {
	t := reflect.TypeOf(n)
	if t.Kind() == reflect.Pointer && t.Elem().Size() == 0 {
		panic("gunim: " + t.String() + " points at a zero-size type, so two of them can share an address and collide in the tree; give it at least one field")
	}
}
