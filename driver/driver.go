// Package driver is the seam between gunim and the operating system.
//
// Everything above this line is portable Go. Everything below it knows
// about X11, Win32 and Cocoa, and gunim takes that layer from
// Ebitengine.
//
// Ebitengine 2.10 ships a complete reimplementation of GLFW in pure Go:
// X11 with GLX and EGL on Linux and the BSDs, Win32 with WGL on
// Windows, Cocoa with NSGL on macOS, all of it reached through purego.
// Its window constructor is the real GLFW one,
//
//	CreateWindow(width, height int, title string, monitor *Monitor, share *Window)
//
// so several windows, a chosen monitor and shared GL objects are all
// already possible down there. Ebitengine confines itself to a single
// window one layer up, in internal/ui, and that is the layer gunim
// replaces.
//
// So a gunim driver is a fork of ebiten/internal/glfw with its
// identifiers exported, plus the GL calls to replay a [paint] op list.
// That turns the hardest and least interesting part of the project —
// three platforms' worth of window and context creation, in pure Go —
// from a year of work into a merge.
//
// On Linux that port speaks X11, so a Wayland desktop runs gunim
// through XWayland. For [KindPopup] windows that is the lucky outcome:
// X11 lets a client place a window at an absolute screen position,
// while Wayland keeps a popup anchored to its parent through xdg_popup.
// The API here stays expressible on both by making popups
// anchor-relative.
//
// # Pacing
//
// A window is paced by its own buffer swaps. Pure-Go GL has no vsync
// event to wait on; what it has is SwapBuffers with a swap interval of
// one, which blocks until the display takes the frame. So each window
// gets a render goroutine of its own, locked to an OS thread with
// runtime.LockOSThread because a GL context belongs to one thread. That
// goroutine replays a frame's ops, swaps, and reports on
// [Window.Presented] once the swap returns.
//
// The engine keeps one frame in flight. It draws when it has something
// to show and nothing in flight, then waits for the report before it
// draws again. An idle window has nothing in flight, so the first frame
// after a keystroke is drawn at once. Two
// windows on two monitors swap on separate threads, so each one keeps
// its own monitor's rate.
package driver

import (
	"context"
	"image"
	"time"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
)

// A Driver owns the connection to the display server.
type Driver interface {
	// Run pumps platform events on the calling goroutine, which must be
	// the main goroutine: every one of the three platforms requires it.
	// Run calls ready once the pump is live and returns when ctx is
	// cancelled or the last window closes. Both of those are a normal
	// end and return nil; an error means the pump itself failed.
	Run(ctx context.Context, ready func()) error

	// NewWindow opens a window. It is safe to call from any goroutine;
	// the driver marshals the request onto the main goroutine.
	//
	// Every window a driver opens shares one group of GL objects, so a
	// glyph atlas or a shader can serve them all.
	NewWindow(o Options) (Window, error)

	// Monitors lists the attached displays.
	Monitors() []Monitor
}

// A StayOpener is a [Driver] that can keep running after its last
// window closes, until StayOpen(false) with no window open, or its
// context ends.
type StayOpener interface {
	StayOpen(on bool)
}

// A Stacker is a [Driver] that knows how the system stacks windows on
// the screen, so that where two of the application's windows overlap a
// drag goes to the one in front.
type Stacker interface {
	// Depths returns, for each of ws, how deep it lies in the stack:
	// 0 for the frontmost window on the screen, larger further back.
	// Only the order of the numbers means anything. A window the
	// driver cannot place, such as a hidden one or one it did not
	// open, gets -1. It is safe to call from any goroutine.
	Depths(ws []Window) []int
}

// A Coverer is a [Driver] that knows other programs' windows too, so a
// drag over one that lies in front of the application's windows leaves
// the application.
type Coverer interface {
	// Covered reports whether, at the screen point p, a window of
	// another program lies in front of every one of ws, and ok whether
	// the system could say. The application's other windows, such as a
	// drag's picture, do not count. It is safe to call from any
	// goroutine.
	Covered(ws []Window, p geom.Point) (covered, ok bool)
}

// A Pointer is a [Driver] that says where the pointer is on the screen,
// for a window to open where the user is looking.
type Pointer interface {
	// PointerOnScreen returns where the pointer is, in the screen
	// coordinates [Monitor] gives bounds in, and false where the system
	// cannot say. It is safe to call from any goroutine.
	PointerOnScreen() (geom.Point, bool)
}

// Options describes a window to open.
type Options struct {
	Title   string
	Size    geom.Size
	Monitor *Monitor
	Kind    Kind
	// Parent, for a popup or utility window, is the window it belongs
	// to. Anchor is a rectangle in Parent's logical space, which keeps
	// the request expressible on Wayland as well as on X11.
	//
	// A popup attaches to Anchor: it opens just below it, starting at
	// its left edge. Where the screen runs out below and there is more
	// room above, it opens above. It slides sideways to stay on the
	// screen. A utility window puts its top-left corner at Anchor.Min.
	Parent Window
	Anchor geom.Rect
	// Passthrough lets the pointer through the window to whatever is
	// under it, as for the picture carried under the pointer in a drag.
	Passthrough bool
	// Over puts a popup's top-left corner at Anchor.Min exactly, for a
	// popup laid over its parent, such as a glow reaching past the
	// parent's edges. It stays there even where the screen runs out.
	Over bool
	// Above puts a popup above Anchor, or below it where the room above runs out and there is more below, as for a
	// list of suggestions at the bottom of a window.
	Above bool
	// Owned keeps a popup just above Parent, under any window in front of Parent, in place of above every window, as
	// for a glow round Parent. Only Windows does so; elsewhere a popup stays as it is.
	Owned bool
	// Icons are the window's icon at several sizes, for the title bar
	// and the taskbar to pick from. None leaves the system's own.
	Icons []image.Image
	// Hidden opens the window hidden, for a popup's window made ahead
	// of time, which a [Recycler] shows once there is a popup for it.
	Hidden bool
	// Chromeless takes the system's title bar and frame away from an
	// ordinary window, for the application to draw its own: see
	// [Framer].
	Chromeless bool
	// Border is the line round a chromeless window's edge; see [Border].
	Border Border
	// DragFromBehind, for a [KindNormal] window, leaves the window where
	// it is when the left button is pressed on its content while another
	// window is active, so a drag can start from it as it lies behind,
	// as from Explorer's windows. The press arrives as a
	// [github.com/marrasen/gunim/input.PointerDown] with Behind set, and
	// the engine brings the window to the front as the button comes up,
	// unless a drag started. Only Windows does so; elsewhere the system
	// raises the window on a press as it always does.
	DragFromBehind bool
	// Text says how the window draws text. Its zero value follows the
	// system, and a window with a Parent takes the parent's.
	Text text.Rendering
	// Place, for a [KindNormal] window, opens it at a placement saved
	// from [PlacementReader], in place of Size and Monitor. It is first
	// made safe with [FitPlacement], so a window saved on a monitor
	// since unplugged opens on the primary one. A maximized placement
	// opens at its bounds, then maximizes, so restoring it later gives
	// those bounds back.
	Place *Placement
	// Fixed keeps the window at its size against the user: no edge
	// sizes it, and the system neither maximizes nor snaps it. The
	// application's own sizing, such as for a saved placement or a
	// change of monitor scale, goes on as ever. A maximized Place opens
	// at its bounds instead.
	Fixed bool
	// NoColorManagement draws the window's sRGB colours to the display
	// as they are, unconverted, whatever the display's profile; see
	// [ColorInfo]. A window with a Parent takes the parent's.
	NoColorManagement bool
}

// Kind is what sort of window to open. Each one maps onto a real window
// type in the display server, which is what lets an overlay leave its
// parent's bounds.
type Kind uint8

const (
	// KindNormal is an ordinary top-level window.
	KindNormal Kind = iota
	// KindUtility is a tool window: a palette or an inspector. It floats
	// above its parent and keeps off the taskbar.
	KindUtility
	// KindPopup is a bare, transient overlay: a menu, a tooltip, a combo
	// box list. It extends past its parent's bounds, which is the one
	// thing Ebitengine has to give up by owning a single window. It
	// never takes the keyboard, so the window that opened it keeps
	// focus. Its surface is transparent where the display server can
	// blend windows, so a popup can have round corners and a shadow.
	KindPopup
)

// A Window is one on-screen surface with its own GL context.
type Window interface {
	// Presented reports each frame handed to Present once it has
	// reached the screen: exactly one Frame per Present, in order.
	//
	// The engine waits for it before drawing the next frame, so the
	// rate the display takes frames at is the rate the window draws
	// at, whatever the application is doing. The engine keeps at most
	// one frame in flight, so a channel with room for one always has
	// room when the render thread reports.
	Presented() <-chan Frame

	// Input carries raw platform input for this window.
	Input() <-chan any

	// Present hands a frame to the render thread and returns at once;
	// the display takes it later. damage is the region that changed,
	// which a driver may use to present just that part of the surface.
	//
	// ops belong to the driver until the matching Frame arrives on
	// Presented; the engine records the next frame into the same
	// buffers only after that.
	Present(ops []paint.Op, damage geom.Rect) error

	// Size is the current size in logical pixels, and Scale is device
	// pixels per logical pixel on the monitor the window is on.
	Size() geom.Size
	Scale() float32

	// RefreshRate is the current monitor's rate in Hz. It follows the
	// window as it is dragged to another display.
	RefreshRate() float64

	// Clipboard returns the text on the system clipboard, and
	// SetClipboard replaces it. Both may wait on the display server.
	Clipboard() (string, error)
	SetClipboard(s string) error

	Close() error
}

// A Zoomer is a window that draws its content larger or smaller than the monitor's scale asks for.
type Zoomer interface {
	// SetZoom multiplies the window's scale by z, 1 for none, keeping the window's size on screen.
	SetZoom(z float32)
}

// Frame reports one presented frame.
type Frame struct {
	// Shown is when the frame reached the screen: when the swap
	// returned, or the display's own timestamp for it where the
	// platform has one. The engine predicts the next frame's
	// presentation time from it.
	Shown time.Time
}

// Monitor is an attached display.
type Monitor struct {
	Name string
	// Bounds is the whole display in screen coordinates: device pixels
	// on Windows and X11, and points on macOS.
	Bounds geom.Rect
	// WorkArea is the part of Bounds windows may use, without the task
	// bar, the dock or the menu bar, in screen coordinates. It is empty
	// where the platform cannot say.
	WorkArea geom.Rect
	// RefreshRate is in Hz.
	RefreshRate float64
	// Scale is device pixels per logical pixel.
	Scale float32
	// CoordsPerLogical is screen coordinates per logical pixel: Scale on
	// Windows and X11, where screen coordinates are device pixels, and 1
	// on macOS, where they are points already. Zero is taken as 1.
	CoordsPerLogical float32
	Primary          bool
}
