// Package access describes an interface to assistive technology, such
// as screen readers: what each part of a window is, what it is called,
// what state it is in, and what can be done with it.
//
// A node that means something to a person using a screen reader
// implements [github.com/marrasen/gunim.Accessible] and returns an
// [Info]. The engine gathers them after a frame into a [Tree], with each
// node's place on screen and which one has focus, and hands the tree to
// the platform, which answers the screen reader's questions from it. A
// screen reader's requests, such as pressing a button or moving focus,
// come back to the window as a [Request].
package access

import "github.com/marrasen/gunim/geom"

// Role is what kind of thing a node is, as a screen reader names it.
type Role uint8

// The roles gunim reports.
const (
	// RoleGroup holds other nodes and says nothing of its own.
	RoleGroup Role = iota
	RoleWindow
	RoleButton
	RoleCheckbox
	// RoleSwitch is a control that is on or off, such as a toggle.
	RoleSwitch
	RoleSlider
	// RoleTextField takes text; with StateMultiline, several lines.
	RoleTextField
	// RoleLabel is text to read.
	RoleLabel
	RoleHeading
	RoleImage
	RoleList
	RoleListItem
	RoleTabList
	RoleTab
	RoleMenu
	RoleMenuItem
	// RoleComboBox shows one choice and opens a list of the others.
	RoleComboBox
	RoleDialog
	RoleTooltip
	RoleScrollArea
	// RoleLink is text that does something when clicked.
	RoleLink
	// RoleTable holds rows of cells, each row a RoleRow of RoleCell or,
	// for the titles, RoleColumnHeader.
	RoleTable
	RoleRow
	RoleCell
	RoleColumnHeader
	// RoleMenuBar holds the titles of a window's menus.
	RoleMenuBar
	// RoleProgressBar shows how far work has got, in its Range.
	RoleProgressBar
	// RoleScrollBar scrolls something else, its place in its Range.
	RoleScrollBar
	// RoleStatus says how something stands, such as "Syncing 3 files". A screen reader reads its new Name or Value
	// when the user is free to hear it, at most once a second. Change its words at the steps that matter, and keep a
	// number that changes fast, such as a speed, in a node that is not live.
	RoleStatus
	// RoleAlert says something the user must hear at once, such as an error. A screen reader reads it as it
	// appears, and its new Name or Value as it changes.
	RoleAlert
	// RoleRadioButton is one choice of several in a RoleGroup, with StateChecked on the chosen one.
	RoleRadioButton
)

var roleNames = [...]string{
	"group", "window", "button", "checkbox", "switch", "slider", "text field",
	"label", "heading", "image", "list", "list item", "tab list", "tab",
	"menu", "menu item", "combo box", "dialog", "tooltip", "scroll area", "link",
	"table", "row", "cell", "column header", "menu bar", "progress bar", "scroll bar",
	"status", "alert", "radio button",
}

func (r Role) String() string {
	if int(r) < len(roleNames) {
		return roleNames[r]
	}
	return "unknown"
}

// State is a set of facts about a node.
type State uint32

// The states a node can be in.
const (
	// StateCheckable marks a node that can be checked, and StateChecked
	// one that is.
	StateCheckable State = 1 << iota
	StateChecked
	StateSelected
	// StateExpandable marks a node that opens more, such as a drop-down,
	// and StateExpanded one that is open.
	StateExpandable
	StateExpanded
	StateDisabled
	StateEditable
	StateMultiline
	StateReadOnly
	// StateHasPopup marks a node that opens a popup.
	StateHasPopup
	// StateModal marks a dialog that holds the window until it closes.
	StateModal
)

// Has reports whether every state in want is set.
func (s State) Has(want State) bool { return s&want == want }

// Info is what a node says about itself.
type Info struct {
	Role Role
	// Name is what a screen reader reads for the node: a button's
	// label, a field's placeholder or the label beside it.
	Name        string
	Description string
	// Value is the node's text, for a text field, or a slider's value
	// as words.
	Value string
	// Range is a slider's range and value; nil for other nodes.
	Range *Range
	State State
	// Actions are the things the node can be asked to do, such as
	// [ActionPress]. The node carries them out as a
	// [github.com/marrasen/gunim.AccessActor].
	Actions []string
	// Parts are parts of the node it draws itself, such as a menu's
	// items or a tab list's tabs, each with Bounds in the node's own
	// space. A part may hold parts, as a table's rows hold cells. A
	// request for a part names it by its index in Part, counting parts
	// and the parts inside them in order.
	Parts []Info
	// Bounds is where a part lies, in its node's space. The engine
	// finds a node's own bounds.
	Bounds geom.Rect
	// Key, set on a part, names what the part shows, such as a row's
	// number, so a screen reader holding the part keeps it as the
	// node's parts move: a request for it reaches the part with that Key
	// wherever it is now, and none once it has gone, as a row scrolled
	// away. A node's keyed parts have Keys apart. Zero names a part by
	// its place among the node's parts.
	Key uint64
	// Active is the index of the part the node's focus is on, such as
	// the highlighted item of an open menu, or -1 for none. The zero
	// Info has none: it counts from 1, so set it to the index plus one.
	Active int
}

// Range is the range and value of a node that holds a number.
type Range struct {
	Min, Max, Value float64
	// Step is the smallest change; zero means any.
	Step float64
}

// The actions nodes commonly take.
const (
	// ActionPress presses a button, flips a checkbox or a switch,
	// chooses a tab or a menu item.
	ActionPress = "press"
	// ActionOpen opens a drop-down's list, and ActionClose closes it.
	ActionOpen  = "open"
	ActionClose = "close"
)

// Node is one node of a [Tree]: what it said about itself, and where
// the engine found it.
type Node struct {
	Info
	// ID stays the same for a node from frame to frame.
	ID uint64
	// Bounds is where the node was drawn, in the window's logical
	// pixels.
	Bounds geom.Rect
	// Focusable and Focused come from the engine's focus.
	Focusable bool
	Focused   bool
	Children  []*Node
}

// Tree is a window's nodes, as the last frame drew them.
type Tree struct {
	// Root is the window itself, with the window's title as its name.
	Root *Node
	// Focus is the node with keyboard focus, or nil.
	Focus *Node
	// byID finds a node by its ID.
	byID map[uint64]*Node
}

// NewTree returns a tree with root, indexing its nodes.
func NewTree(root *Node) *Tree {
	t := &Tree{Root: root, byID: map[uint64]*Node{}}
	var walk func(n *Node)
	walk = func(n *Node) {
		t.byID[n.ID] = n
		if n.Focused {
			t.Focus = n
		}
		for _, k := range n.Children {
			walk(k)
		}
	}
	walk(root)
	return t
}

// Find returns the node with id, or nil.
func (t *Tree) Find(id uint64) *Node {
	if t == nil {
		return nil
	}
	return t.byID[id]
}

// Request is something a screen reader asks of a node, sent on the
// window's input: an action, a new value, or focus.
type Request struct {
	ID uint64
	// Part is the index of the part the request is for, or -1 for the
	// node itself.
	Part int
	// Action is one of the node's Actions, or empty.
	Action string
	// Value, with SetValue, is a new value for a node with a Range.
	Value    float64
	SetValue bool
	// Focus asks for keyboard focus.
	Focus bool
}
