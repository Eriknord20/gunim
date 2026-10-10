// Package input holds the events a window delivers to its nodes, and
// the keys, buttons and modifiers they carry.
//
// It sits below both gunim and the driver, so a driver can report input
// in the same types a node receives.
package input

import (
	"time"

	"github.com/marrasen/gunim/geom"
)

// An Event is something that happened to a node: pointer, keyboard or
// focus. Nodes receive events by implementing gunim.Handler.
type Event interface{ isEvent() }

// Pointer events carry Pos in the receiving node's own coordinate
// space, with the origin at its top-left corner, so a node can work out
// where it was clicked from Pos alone.

// PointerEnter arrives when the pointer moves onto a node.
//
// The engine tracks the node under the pointer between frames and sends
// PointerEnter and [PointerLeave] itself, because hover is the single
// most common thing to animate, and one implementation beats every
// widget writing its own.
type PointerEnter struct {
	Pos  geom.Point
	Time time.Time
}

// PointerLeave arrives when the pointer moves off a node.
type PointerLeave struct {
	Time time.Time
}

// PointerMove arrives as the pointer travels across a node.
type PointerMove struct {
	Pos  geom.Point
	Mods Mods
	// Touch says a finger moved; see [PointerDown].
	Touch bool
	Time  time.Time
}

// PointerDown arrives when a button goes down over a node.
type PointerDown struct {
	Pos    geom.Point
	Button Button
	Mods   Mods
	// Clicks counts this press within a rapid sequence: 1 for a single
	// click, 2 for the second of a double click.
	Clicks int
	// Focusing says the press moved the keyboard focus, as it arrives:
	// what had it before was elsewhere. A node that takes a first click
	// as only choosing it, as a terminal does, passes such a press by.
	Focusing bool
	// Behind says the press came while another window was active, and
	// left this one where it was: a window opened with
	// gunim.WindowOptions.DragFromBehind, on Windows. A drag started
	// from the press runs with the window left behind, and otherwise the
	// window comes to the front as the button comes up.
	Behind bool
	// Touch says a finger pressed. A finger that moves after pressing
	// scrolls what it came down on, unless what it pressed drags by
	// touch, which the engine works out: the node pressed hears its
	// press let go far away as the scroll starts. A finger held still is
	// a press of the secondary button, as a right click is, which opens
	// a context menu, and a text field selects the word under it.
	Touch bool
	Time  time.Time
}

// PointerUp arrives when a button comes back up.
type PointerUp struct {
	Pos    geom.Point
	Button Button
	Mods   Mods
	// Touch says a finger lifted; see [PointerDown].
	Touch bool
	Time  time.Time
}

// Away is a point outside any window. A press let go at Away lets go of
// nothing: it was called off, as when a finger that pressed a button
// goes on to scroll, or a second finger comes down to pinch.
var Away = geom.Pt(-1e6, -1e6)

// Scroll carries wheel or touchpad movement. Delta is in logical
// pixels, so momentum scrolling keeps the fractional precision the
// touchpad reported.
type Scroll struct {
	Pos   geom.Point
	Delta geom.Point
	// Notches is the movement in the wheel's notches, as the system
	// counts them, for a node that scrolls by a count of lines a notch
	// rather than by distance. A touchpad gives fractions. Zero when the
	// source does not say.
	Notches geom.Point
	Mods    Mods
	// Touch says the scroll is a finger dragged or flung on a touch
	// screen, rather than a wheel or a touchpad: its Delta is then the
	// finger's own move, so a node that pans with it moves by Delta.
	Touch bool
	Time  time.Time
}

// Pinch carries two fingers on a touch screen spreading apart or
// closing together, to a node that zooms with them: see
// gunim.PinchZoomer. Pos is the point midway between the fingers, and
// Delta how far it moved since the last Pinch, so fingers that move
// together as they pinch pan what they zoom. Scale is how much further
// apart the fingers are than at the last Pinch: 2 for twice as far, 0.5
// for half, 1 for no change. A node that zooms by Scale about the point
// Pos less Delta, and then moves by Delta, keeps what was under the
// fingers under them.
type Pinch struct {
	Pos   geom.Point
	Delta geom.Point
	Scale float32
	Phase PinchPhase
	Time  time.Time
}

// PinchPhase says where a [Pinch] is in its gesture.
type PinchPhase uint8

// The phases of a pinch. PinchStart arrives as the second finger comes
// down, and PinchEnd as one of the two lifts or the touch is called
// off; both have a Scale of 1 and no Delta. PinchMove arrives each time
// the fingers move between.
const (
	PinchStart PinchPhase = iota
	PinchMove
	PinchEnd
)

// Heading says which way the device faces, to a node that watches it: see gunim.HeadingWatcher. It goes to every
// node shown that watches, and does not bubble.
//
// Degrees is clockwise from magnetic north, from 0 up to but not 360: 90 is east, 180 south. It is the way the top
// edge of the screen points with the device lying flat, screen up, and the way its back looks with it held upright
// before the user, and the same way at every tilt between. A screen turned with the device keeps its own top edge.
//
// It is the sensor's reading as it comes, a dozen or so times a second, and wavers by a degree or two; an
// application that wants a steady needle smooths it, minding the step from 359 to 0. A phone's compass is a
// magnetometer: indoors, near steel, a magnet or a speaker, the reading can be off by tens of degrees, and
// Accuracy falls while the sensor knows it is unsure. Magnetic north lies a few degrees from true north in most
// places.
type Heading struct {
	Degrees float32
	// Accuracy is how far the sensor trusts the reading. At [HeadingUnreliable] or [HeadingLow] an application
	// can ask the user to move the device in a figure eight, which calibrates it.
	Accuracy HeadingAccuracy
	// Error is how far off the sensor reckons Degrees may be, in degrees either way, or 0 where it does not say.
	Error float32
	Time  time.Time
}

// HeadingAccuracy is how far a compass trusts its reading, as Android's SENSOR_STATUS values say.
type HeadingAccuracy uint8

// The accuracies of a [Heading], worst first.
const (
	// HeadingUnreliable says the reading cannot be trusted: the compass needs calibrating, or something magnetic
	// is near.
	HeadingUnreliable HeadingAccuracy = iota
	HeadingLow
	HeadingMedium
	HeadingHigh
)

// Keyboard events split press from release, and deliver the text a
// keystroke produced as its own [TextInput]. Polled key state collapses
// the two, so it reads Ctrl+C and the letter c the same way, which is
// the exact reason gridterm carries a fork of Ebitengine. The same
// shape of bug would rule gunim out for anything that cares about
// modifiers, held keys or key repeat, so the split is designed in from
// the start.

// KeyPress arrives when a key goes down, and again for each key repeat.
type KeyPress struct {
	Key  Key
	Mods Mods
	// Repeat is true when the system's key repeat produced the press,
	// and false for a fresh one.
	Repeat bool
	// Typed is true when the press also typed text, which arrives next
	// as a [TextInput], and for a dead key, which types with the key
	// after it. Modifiers alone leave this unclear: AltGr+Q types @ on
	// a German keyboard with Control and Alt held. A node that takes
	// text leaves a typing press to the text. It is reported on Windows
	// and Linux.
	Typed bool
	// Char is the character the key types on the keyboard layout in
	// use, without Shift, for a key that types one: '+' for the key
	// marked + on a Swedish keyboard, which sits where a US one has -.
	// A shortcut on + or [ reads it, so it follows what is printed on
	// the key rather than where the key sits. Zero when the system does
	// not say.
	Char rune
	Time time.Time
}

// KeyRelease arrives when a key comes back up.
type KeyRelease struct {
	Key  Key
	Mods Mods
	Time time.Time
}

// AltTapped arrives when Alt is pressed and let go with no other key, click or scroll between, as moves the keyboard
// to a window's menu bar on Windows. It goes to the focused node and bubbles, as a key does.
type AltTapped struct{ Time time.Time }

// TextInput carries text the keystroke produced, after the input method
// has had its say. A node that wants characters reads this one; a node
// that wants shortcuts reads [KeyPress].
type TextInput struct {
	Text string
	Time time.Time
}

// Composing carries in-progress input method text: show it, and wait
// for [TextInput] to commit it.
type Composing struct {
	Text string
	// Selected is the range the input method has highlighted, as byte
	// offsets into Text.
	Selected [2]int
}

// FocusGained arrives when a node takes keyboard focus. Keyed says a key press moved the focus here, such as an arrow
// key or a shortcut, for a node that is one of several the keys walk to show where they are. Step is 1 when a group's
// arrow keys walked the focus here from the node before it, -1 from the node after it, and 0 otherwise. Grouped says
// the node is in a tab group, as the item the group has selected, which shows as selected however the focus came.
type FocusGained struct {
	Keyed   bool
	Step    int
	Grouped bool
	Time    time.Time
}

// FocusLost arrives when a node gives keyboard focus up.
type FocusLost struct{ Time time.Time }

// FocusEntered arrives at each node round the focused one that the
// focus has just come into from outside it, after the node itself hears
// [FocusGained]. A pane of several controls hears it once, as the
// keyboard comes to any of them, and not as it moves among them.
type FocusEntered struct{ Time time.Time }

// FocusRing says whether a node shows that it has the keyboard. The focused node hears it, and so does the outermost
// tab group around it, with Within set. Rings show once Tab has moved the focus, until a click. Grouped says a
// group around the focused node draws the ring round the whole.
type FocusRing struct {
	On      bool
	Within  bool
	Grouped bool
	Time    time.Time
}

// WindowFocusLost arrives when the window gives the keyboard to another
// program. It goes to the focused node and bubbles, as a key does. A
// key held as the keyboard went sends no release here, so a node
// waiting on one, as for Ctrl let go, ends the wait on it.
type WindowFocusLost struct{ Time time.Time }

// WindowFocusGained arrives when the window has the keyboard back. It
// goes to the focused node and bubbles, as a key does.
type WindowFocusGained struct{ Time time.Time }

// WindowHidden arrives when the window can no longer be seen: it was
// minimized, or, on a phone, its application went to the background.
// The window draws no frames meanwhile, so its animations hold where
// they are; an application stops what else only matters while it is
// seen, as a game's music. It goes to the focused node and bubbles, as
// a key does, and then to each view mounted at the root that it did not
// pass, so a view under a dialog that holds the keyboard hears it too.
type WindowHidden struct{ Time time.Time }

// WindowShown arrives when a hidden window can be seen again. It goes
// where [WindowHidden] goes.
type WindowShown struct{ Time time.Time }

// MediaSeek arrives when the user moves what plays to At, in the media
// controls a phone shows for an application playing, as on its lock
// screen. It goes to the focused node and bubbles, as a key does.
type MediaSeek struct {
	At   time.Duration
	Time time.Time
}

// DragOver arrives while something is dragged over a node: another
// node's Data, dragged inside the application, from this window or
// another, or [Files] from another program. It bubbles like a pointer
// event. A node that returns true
// for it will take the drop, and hears DragLeave if the drag moves on.
// Mods are the modifier keys held, such as Ctrl to copy.
type DragOver struct {
	Pos  geom.Point
	Data any
	Mods Mods
	Time time.Time
}

// Files is what a drag of files from another program, such as a file
// manager, carries: the Data of [DragOver] and [Drop]. Paths holds the
// files once the system has told them, which may be a moment after the
// drag arrives over the window.
type Files struct{ Paths []string }

// DragLeave arrives when a drag a node took DragOver for moves on, or
// ends somewhere else.
type DragLeave struct{ Time time.Time }

// Drop arrives when something is let go over a node: another node's
// Data, dragged inside the application, or files from another program,
// such as a file manager, as Paths, with [Files] as the Data. It bubbles like a pointer event,
// and the node that returns true has taken it. Mods are the modifier
// keys held as it was let go, where the system says.
type Drop struct {
	Pos   geom.Point
	Data  any
	Paths []string
	Mods  Mods
	Time  time.Time
}

// DragEnd arrives at the node a drag started from once it ends, and at
// the picture the drag carries. Taken says whether a node took the drop.
//
// Out says the drag was let go over no window of the application, as
// on the desktop, and At is where, in the space of the window the drag
// started in: a node can open a window of its own there for what it
// carried. A drag given up with Escape is not Out.
type DragEnd struct {
	Taken bool
	Out   bool
	At    geom.Point
	Time  time.Time
}

// DragMove arrives at the picture a drag carries each time the pointer
// moves it. At is where the pointer is, in the space of the window the
// drag started in.
type DragMove struct {
	At   geom.Point
	Time time.Time
}

// DragAnswer arrives at the picture a drag carries when what a drop
// would do changes: the answer the node under the pointer gave, or nil
// over nothing that answers.
type DragAnswer struct {
	Answer any
	Time   time.Time
}

func (PointerEnter) isEvent() {}
func (PointerLeave) isEvent() {}
func (PointerMove) isEvent()  {}
func (PointerDown) isEvent()  {}
func (PointerUp) isEvent()    {}
func (Scroll) isEvent()       {}
func (Pinch) isEvent()        {}
func (Heading) isEvent()      {}
func (KeyPress) isEvent()     {}
func (KeyRelease) isEvent()   {}
func (AltTapped) isEvent()    {}
func (TextInput) isEvent()    {}
func (Composing) isEvent()    {}
func (FocusGained) isEvent()  {}
func (FocusLost) isEvent()    {}
func (FocusEntered) isEvent() {}
func (FocusRing) isEvent()    {}
func (HistoryStep) isEvent()  {}
func (DragOver) isEvent()     {}
func (DragLeave) isEvent()    {}
func (Drop) isEvent()         {}
func (DragEnd) isEvent()      {}
func (DragMove) isEvent()     {}
func (DragAnswer) isEvent()   {}

func (WindowFocusLost) isEvent()   {}
func (WindowFocusGained) isEvent() {}
func (WindowHidden) isEvent()      {}
func (WindowShown) isEvent()       {}
func (MediaSeek) isEvent()         {}

// Button identifies a pointer button.
type Button uint8

// The pointer buttons gunim reports.
const (
	ButtonPrimary Button = iota
	ButtonSecondary
	ButtonMiddle
	// ButtonBack and ButtonForward are a mouse's side buttons, which go
	// back and forward through where one has been, as in a browser. Nodes
	// hear them as [HistoryStep], never as a press.
	ButtonBack
	ButtonForward
)

// HistoryStep asks to go back through where the user has been, or forward with Forward set: from a mouse's side
// buttons as they are let go, or on Windows from a keyboard's Browser Back and Forward keys. It bubbles as a key does,
// from the node under the pointer for a mouse button and from the focused node for a key, so the view that keeps the
// history takes it wherever it is.
type HistoryStep struct {
	Forward bool
	Time    time.Time
}

// Mods is the set of modifier keys held when an event happened.
type Mods uint8

// The modifier keys gunim reports.
const (
	ModShift Mods = 1 << iota
	ModControl
	ModAlt
	// ModSuper is Command on macOS and the Windows key elsewhere.
	ModSuper
)

// Has reports whether every modifier in m is held.
func (m Mods) Has(want Mods) bool { return m&want == want }

// Key is a physical key, whatever layout is in force. Matching on the
// physical key keeps a shortcut on the same spot on the keyboard for
// QWERTY and Dvorak alike. Each key is named after what it shows on a
// US keyboard.
type Key uint16

// The keys gunim reports.
const (
	KeyUnknown Key = iota

	KeyA
	KeyB
	KeyC
	KeyD
	KeyE
	KeyF
	KeyG
	KeyH
	KeyI
	KeyJ
	KeyK
	KeyL
	KeyM
	KeyN
	KeyO
	KeyP
	KeyQ
	KeyR
	KeyS
	KeyT
	KeyU
	KeyV
	KeyW
	KeyX
	KeyY
	KeyZ

	Key0
	Key1
	Key2
	Key3
	Key4
	Key5
	Key6
	Key7
	Key8
	Key9

	KeyF1
	KeyF2
	KeyF3
	KeyF4
	KeyF5
	KeyF6
	KeyF7
	KeyF8
	KeyF9
	KeyF10
	KeyF11
	KeyF12

	KeyEscape
	KeyEnter
	KeyTab
	KeySpace
	KeyBackspace
	KeyDelete
	KeyInsert
	KeyLeft
	KeyRight
	KeyUp
	KeyDown
	KeyHome
	KeyEnd
	KeyPageUp
	KeyPageDown

	KeyMinus
	KeyEqual
	KeyLeftBracket
	KeyRightBracket
	KeyBackslash
	KeySemicolon
	KeyApostrophe
	KeyGraveAccent
	KeyComma
	KeyPeriod
	KeySlash

	KeyLeftShift
	KeyRightShift
	KeyLeftControl
	KeyRightControl
	KeyLeftAlt
	KeyRightAlt
	KeyLeftSuper
	KeyRightSuper
	KeyCapsLock
	KeyMenu

	// The keypad's keys, which type digits and signs with Num Lock on.
	KeyKP0
	KeyKP1
	KeyKP2
	KeyKP3
	KeyKP4
	KeyKP5
	KeyKP6
	KeyKP7
	KeyKP8
	KeyKP9
	KeyKPDecimal
	KeyKPDivide
	KeyKPMultiply
	KeyKPSubtract
	KeyKPAdd
	KeyKPEnter
	KeyKPEqual

	KeyF13
	KeyF14
	KeyF15
	KeyF16
	KeyF17
	KeyF18
	KeyF19
	KeyF20
	KeyF21
	KeyF22
	KeyF23
	KeyF24

	KeyPause
	KeyPrintScreen
	KeyScrollLock
	KeyNumLock

	// The media keys: a keyboard's, a headset's or a phone's, and the
	// buttons of the media controls a phone shows for an application
	// playing, on its lock screen.
	KeyMediaPlayPause
	KeyMediaPlay
	KeyMediaPause
	KeyMediaStop
	KeyMediaNext
	KeyMediaPrevious
)
