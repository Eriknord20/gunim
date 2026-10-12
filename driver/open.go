package driver

import (
	"errors"
	"image"
	"image/color"
	"math"
	"time"

	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// A CursorSetter is a [Window] that can change the pointer's shape
// while the pointer is over it.
type CursorSetter interface {
	SetCursor(c input.Cursor)
}

// ErrNoDriver is returned when no platform driver is built in for the
// operating system gunim is running on.
var ErrNoDriver = errors.New("driver: no platform driver for this operating system")

// A TextInputter is a [Window] that can be told when the application is
// taking text, so the platform's input method composes into it: it
// holds the key presses it needs while the user composes, and reports
// the composition as [github.com/marrasen/gunim/input.Composing] and the
// result as [github.com/marrasen/gunim/input.TextInput]. Setting it false ends any composition.
type TextInputter interface {
	SetTextInput(active bool)
}

// A TextStater is a [Window] that keeps a copy of the focused text
// node's state, so it can answer an input method that asks about the
// text, as a phone's keyboard does, at once. The engine calls
// SetTextState when the state changes, and with nil when the focus
// moves to a node that keeps no text.
//
// The driver sends the input method's changes as
// [github.com/marrasen/gunim/input.TextEdit], numbered from 1. seq is
// the number of the last edit the state includes. A state with seq
// behind the driver's latest edit is out of date: the edits after it
// are still on their way to the node. A state with seq level with it
// that differs from the copy is a change the node made, such as text
// set by the program, and the input method starts over from it.
type TextStater interface {
	SetTextState(s *input.TextState, seq uint64)
}

// A TextBoxPlacer is a [CaretPlacer] that also hears where the node the
// caret is in lies, in logical pixels of window space, as a text field's
// bounds, so it can keep the whole field in view above a keyboard on the
// screen. The engine tells it after a frame where the bounds moved.
type TextBoxPlacer interface {
	SetTextBox(r geom.Rect)
}

// NowPlaying is what a media application plays, for the system's media
// controls.
type NowPlaying struct {
	Title, Artist, Album string
	// Cover is the artwork, as PNG or JPEG, or nil.
	Cover []byte
	// Length is how long it lasts, zero where that is unknown, and
	// Position how far it has played, as of now.
	Length, Position time.Duration
	Playing          bool
}

// A NowPlayer is a [Driver] that shows what an application plays in the
// system's media controls, as a phone's lock screen, its quick settings
// and its notifications do, and a desktop's media panel, and on a phone
// keeps the application running while it plays unseen. The controls' buttons arrive at the application's main
// window as the media keys of package input, and a move along the
// controls' bar as [input.MediaSeek]. SetNowPlaying with nil takes the
// controls away.
type NowPlayer interface {
	SetNowPlaying(np *NowPlaying) error
}

// Permission is something the user grants an application, where the
// system has the user grant it, as a phone's does.
type Permission int

// The permissions.
const (
	// PermissionMusic reads the user's music and other sound files.
	PermissionMusic Permission = iota + 1
)

// A Permitter is a [Driver] whose system has the user grant
// permissions. Permitted reports whether p is granted. Ask asks the
// user for p, where it is not granted and the user has not refused it
// for good, and blocks until they answer; it returns whether p is
// granted. Ask is called from a goroutine of the application's, never
// the main one.
type Permitter interface {
	Permitted(p Permission) bool
	Ask(p Permission) bool
}

// UserFolder is a kind of folder the user keeps their things in.
type UserFolder int

// The user's folders.
const (
	// FolderMusic is where the user keeps their music.
	FolderMusic UserFolder = iota + 1
)

// A FolderFinder is a [Driver] that says where the user keeps things.
// UserFolder returns the folder of kind f, or "" where there is none.
type FolderFinder interface {
	UserFolder(f UserFolder) string
}

// A SafeAreaer is a [Window] the system draws over in part, as a
// phone's status bar, navigation bar and camera cutout lie over an
// application drawn to the screen's edges. SafeArea returns how far in
// from each of the window's edges, in logical pixels, its content should
// keep clear; its background still runs to the edges. A driver sends
// [Redraw] as the area changes.
type SafeAreaer interface {
	Window
	SafeArea() geom.Insets
}

// A KeyboardCoverer is a [Window] a keyboard on the screen can cover,
// as a phone's soft keyboard covers the bottom of an application.
// KeyboardCover returns how far up from the window's bottom edge the
// keyboard reaches, in logical pixels, and 0 while it is down. It
// follows the keyboard as it slides, and a driver sends [Redraw] as it
// changes.
type KeyboardCoverer interface {
	Window
	KeyboardCover() float32
}

// A Buzzer is a [Window] on a device that can buzz, as a phone does. The
// engine calls Buzz as a finger held still becomes a long press.
type Buzzer interface {
	Buzz()
}

// A KeyboardShower is a [Window] with a keyboard on the screen, as a
// phone has. The engine calls ShowKeyboard when a press is let go on the
// focused node while it takes text, as a tap on a text field asks for
// the keyboard. Text input turning on leaves the keyboard down, so a
// field focused as a window opens waits for its tap, and turning off
// puts it away.
type KeyboardShower interface {
	ShowKeyboard()
}

// A CaretPlacer is a [Window] that can tell the platform's input method
// where the text caret is, in logical pixels of window space, so its
// candidate window opens beside the text being composed.
type CaretPlacer interface {
	SetTextCaret(r geom.Rect)
}

// Redraw is sent on [Window.Input] when the window needs drawing again
// with no input behind it: after a resize, a move to another monitor,
// or the display server asking for the contents back.
type Redraw struct{}

// A Placer is a popup [Window] that can move and resize: to fit new
// content, or to follow its anchor. anchor is in the parent's logical
// space, as in [Options], and size is in logical pixels.
type Placer interface {
	Place(anchor geom.Rect, size geom.Size) error
}

// A PointerRegioner is a popup [Window] that can take the pointer on part of itself only, so the pointer goes to
// the window under the rest: a menu whose window reaches past its card, for the shadow, over the menu bar that opened
// it, leaves the bar its hover and clicks. rects are in the window's logical pixels; nil gives the whole window the
// pointer again, and an empty, non-nil rects none of it.
//
// On X11 it is the window's input shape. On Windows the window tells the system the point is not its own, which
// passes the pointer to a window of the same program under it, but not to another program's. macOS has no such
// region, and the whole window goes on taking the pointer there.
type PointerRegioner interface {
	SetPointerRegion(rects []geom.Rect)
}

// A Recycler is a popup [Window] that can be hidden and shown again,
// so a popup that opens often, such as a menu or a palette, need not
// make a window and its surface each time: on Windows that is the
// larger part of opening one. Hide takes the window off the screen,
// keeping it; Show puts it back where Place last put it.
type Recycler interface {
	Hide() error
	Show() error
}

// WindowFocus is sent on [Window.Input] when the window gains or loses
// the keyboard.
type WindowFocus struct{ Focused bool }

// WindowShown is sent on [Window.Input] when the window can no longer
// be seen, or can be again: minimized and restored on a desktop, or its
// application sent to the background and brought back on a phone.
type WindowShown struct{ Shown bool }

// WindowCovered is sent on [Window.Input] as the window goes out of
// sight while it stays open, or comes back: covered whole by other
// windows, on another virtual desktop or workspace, or on a screen that
// is off, asleep or locked. The window draws nothing while covered, and
// the application goes on as before, so music plays on under other
// windows; a window put away, minimized, is [WindowShown]'s.
type WindowCovered struct{ Covered bool }

// FilesOver is sent on [Window.Input] each time files from another
// program, such as a file manager, move while dragged over the window:
// at Pos, in logical pixels, with their Paths once the system has told
// them, which may be a moment after the drag arrives. Mods are the
// modifier keys held. FilesLeft is sent as the drag leaves with no
// drop; a drop arrives as an [input.Drop] with its Paths.
type FilesOver struct {
	Pos   geom.Point
	Paths []string
	Mods  input.Mods
}

// FilesLeft is sent on [Window.Input] as files dragged over the window
// leave it with no drop.
type FilesLeft struct{}

// A Positioner is a [Window] that says where its content sits on the
// screen: the top-left corner of what it draws, in device pixels, so a
// screenshot can put a popup where it shows over its parent.
type Positioner interface {
	ContentOrigin() (image.Point, error)
}

// Shooter is a [Window] that can hand over what it draws: fn receives
// the next frame's pixels, the right way up, on a goroutine of the
// driver's. A driver that draws nothing, such as the offscreen one,
// does not implement it.
type Shooter interface {
	Shoot(fn func(*image.RGBA))
}

// CloseAsked is sent on [Window.Input] when the user asks to close the
// window: its close button, or the system's keys for closing one. The
// window stays open, for the engine to close or keep.
type CloseAsked struct{}

// A Backgrounder is a [Window] the engine tells the colour under its
// frame: what shows where the frame paints nothing and its first op
// leaves the window uncovered. Its alpha is how opaque that is, as for
// a window fading in, and zero leaves the window clear there.
type Backgrounder interface {
	SetBackground(c color.NRGBA)
}

// A Fader is a [Window] the engine tells how opaque it is drawing the
// window, below 1 as the window fades in as it opens or out as it
// leaves, and how large, about its middle, as it grows and shrinks
// meanwhile. What the driver draws round the window, such as a shadow,
// fades and shrinks with it; what the system draws, which cannot, the
// driver can hide.
type Fader interface {
	SetFade(opacity, scale float32)
}

// An Outliner is a [Window] that says what shape it shows: the radius its
// corners are cut to and the edge it leaves for a border, both in logical
// pixels and 0 for none, and whether it can show what is behind it. The
// engine draws a fading window's background inside that shape, so the
// window fades and shrinks whole.
type Outliner interface {
	Outline() (radius, edge float32, blends bool)
}

// A Transparent is a [Window] that can say whether it shows what is
// behind it wherever nothing is painted. A popup is transparent where
// the display server can blend windows.
type Transparent interface {
	Transparent() bool
}

// A Screener is a [Window] that knows where it is on the screen. It
// turns a point in its own logical space into screen coordinates, and
// back, so a drag can go from one window to another.
type Screener interface {
	ToScreen(p geom.Point) geom.Point
	FromScreen(p geom.Point) geom.Point
}

// A DragOuter is a [Window] that can hand a drag the pointer is
// carrying, with a button down in it, to other programs, as files.
// DragOut returns at once, and the window reports the end on
// [Window.Input] as [DragOutEnded].
type DragOuter interface {
	DragOut(paths []string) error
}

// DragOutEnded is sent on [Window.Input] when a drag handed to other
// programs ends. Taken says whether a program took the drop.
type DragOutEnded struct {
	Taken bool
	// Back says the pointer came back over a window of the application with the button still down, so the window
	// that started the drag carries it on, and At is where the pointer is, in the window's logical space.
	Back bool
	At   geom.Point
}

// An AccessPublisher is a [Window] that tells assistive technology,
// such as a screen reader, what the window holds. AccessWanted reports
// whether anything is listening, so the engine gathers the tree only
// then. PublishAccess takes the tree as the last frame drew it; it
// belongs to the publisher from then on. A screen reader's requests
// come back on [Window.Input] as [access.Request].
type AccessPublisher interface {
	AccessWanted() bool
	PublishAccess(t *access.Tree)
}

// A Titler is a [Window] whose title the application can change, as
// the platform shows it on the title bar and in the task switcher.
type Titler interface {
	SetTitle(title string)
}

// A FullScreener is a [Window] that can fill its monitor, without the
// platform's frame, and go back to the size and place it had.
type FullScreener interface {
	SetFullScreen(on bool)
	FullScreen() bool
}

// An Attender is a [Window] that can ask for the user's attention
// without taking the keyboard, as a terminal does for a bell in a
// window the user is not looking at: the platform flashes it in the
// task bar, or bounces it in the dock.
type Attender interface {
	RequestAttention()
}

// A Cloaker is a [Window] that can be taken off the screen while it
// stays open, out of the pointer's way, and put back as it was: on
// Windows cloaked, as the system does a window on another desktop,
// keeping its button on the task bar; elsewhere hidden.
type Cloaker interface {
	SetCloaked(on bool)
}

// A Fronter is a [Window] that can come to the front with the keyboard,
// shown again first if it was minimized: one of an application's
// windows the user asked for from another. Where the platform keeps a
// program in the background from taking the keyboard, it asks for the
// user's attention instead.
type Fronter interface {
	ToFront()
}

// A Framer is a [Window] opened with [Options.Chromeless]: its
// application draws the title bar, and the system goes on moving,
// sizing and maximizing it as it would by its own.
type Framer interface {
	// Chromeless reports whether the system's title bar is gone. It is
	// false where a system keeps its own, as macOS does for now, and
	// the application then draws none.
	Chromeless() bool
	// SetTitleBar says which parts of the window are title bar with
	// nothing on them, where a press moves the window and a double
	// click maximizes it, and where the maximize button is, in logical
	// pixels in the window's space. The system asks about a point at
	// once, on its own thread, and is answered from these.
	SetTitleBar(caption []geom.Rect, maximize geom.Rect)
	// NativeFrame reports whether the system moves and sizes the window
	// from the title bar and the edges itself, as Windows does. Where it
	// does not, the engine starts moves and resizes with StartMove and
	// StartResize, and maximizes on a double click.
	NativeFrame() bool
	StartMove() error
	StartResize(e Edge) error
	Minimize() error
	SetMaximized(on bool) error
	Maximized() bool
}

// A Pinner is a [Window] that can be kept above other windows.
type Pinner interface {
	SetPinned(on bool) error
}

// MoveStarted is sent on [Window.Input] when the user starts moving or sizing the window by its frame or its title
// bar, where the system takes the press that starts it for itself. It closes what a press outside would.
type MoveStarted struct{}

// A PopupRoomer is a [Window] that says how much room the screen leaves round a rectangle in its own logical space,
// for a popup attached there: to the edges of the work area of the monitor under it.
type PopupRoomer interface {
	PopupRoom(anchor geom.Rect) Room
}

// Room is the room, in logical pixels, from a popup's anchor to the edges of the screen's work area: below its bottom
// and above its top, and right and left of its left edge, where a popup starts.
type Room struct {
	Below, Above, Left, Right float32
}

// NoRoomLimit is the room where nothing says where the screen ends.
var NoRoomLimit = Room{
	Below: float32(math.Inf(1)), Above: float32(math.Inf(1)), Left: float32(math.Inf(1)), Right: float32(math.Inf(1)),
}

// Border is the thin line round the edge of a window opened with [Options.Chromeless], where the platform draws one,
// as Windows 11 does. Its zero value is the system's own.
type Border struct {
	// Color is the line's colour while the window is active. Its zero value is the system's.
	Color color.NRGBA
	// Inactive is its colour while another window is active. Its zero value is Color.
	Inactive color.NRGBA
	// Width is the line's width in logical pixels, taken from the window's edge. Its zero value is one. Where the
	// system draws the line, it is always one pixel wide.
	Width float32
	// None draws no line, and the window's content reaches its edge.
	None bool
	// Transition is how long a change of colour or width takes, eased, whether the application changes the border or
	// the window is activated. Its zero value is [BorderTransition], and a negative one changes at once.
	Transition time.Duration
}

// BorderTransition is how long a [Border] takes to change when its Transition is zero.
const BorderTransition = 150 * time.Millisecond

// A Borderer is a [Window] whose [Border] the application can set.
type Borderer interface {
	SetBorder(b Border)
}

// Edge is an edge or corner of a window, to size it by.
type Edge uint8

// The edges and corners, clockwise from the top left.
const (
	EdgeTopLeft Edge = iota
	EdgeTop
	EdgeTopRight
	EdgeRight
	EdgeBottomRight
	EdgeBottom
	EdgeBottomLeft
	EdgeLeft
)

// WindowMaximized is sent on [Window.Input] when the window is
// maximized or restored.
type WindowMaximized struct{ Maximized bool }
