//go:build linux

package desktop

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/godbus/dbus/v5"

	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/geom"
)

// On Linux, screen readers such as Orca talk to applications through
// AT-SPI, over a D-Bus bus of their own. The application answers their
// questions about its objects, and tells them when something changes.
//
// Here the application is one object, with a child for each window,
// and each window's nodes below it, from the tree the engine published
// last. The tree is swapped whole, so D-Bus's goroutines read it while
// the engine builds the next one. A request, such as pressing a button,
// goes to the window's input for the engine to carry out.

const (
	atspiPrefix   = "/org/a11y/atspi/accessible/"
	atspiRootPath = dbus.ObjectPath(atspiPrefix + "root")
	atspiNullPath = dbus.ObjectPath("/org/a11y/atspi/null")
	ifAccessible  = "org.a11y.atspi.Accessible"
	ifApplication = "org.a11y.atspi.Application"
	ifComponent   = "org.a11y.atspi.Component"
	ifAction      = "org.a11y.atspi.Action"
	ifValue       = "org.a11y.atspi.Value"
	ifText        = "org.a11y.atspi.Text"
	ifProperties  = "org.freedesktop.DBus.Properties"
	ifCache       = "org.a11y.atspi.Cache"
)

// ref is an AT-SPI object reference: a bus name and a path.
type ref struct {
	Name string
	Path dbus.ObjectPath
}

// atspi is the application's connection to the accessibility bus.
type atspi struct {
	conn    *dbus.Conn
	name    string
	desktop ref

	mu      sync.Mutex
	windows []*atspiWindow
	nextID  int
}

// atspiWindow is one window, as AT-SPI sees it.
type atspiWindow struct {
	a     *atspi
	id    int
	win   *Window
	popup bool
	tree  atomic.Pointer[access.Tree]
	// parents maps a node's ID to its parent's, for the tree in tree.
	parents atomic.Pointer[map[uint64]uint64]
}

// accessibilityOn reports whether the session has accessibility on: a
// screen reader running, or a tool that reads applications, such as
// Accerciser. GUNIM_ACCESS=1 turns it on regardless.
func accessibilityOn(session *dbus.Conn) bool {
	if os.Getenv("GUNIM_ACCESS") == "1" {
		return true
	}
	bus := session.Object("org.a11y.Bus", "/org/a11y/bus")
	for _, p := range []string{"org.a11y.Status.IsEnabled", "org.a11y.Status.ScreenReaderEnabled"} {
		v, err := bus.GetProperty(p)
		if b, ok := v.Value().(bool); err == nil && ok && b {
			return true
		}
	}
	return false
}

// watchAccessibility runs on until the session turns accessibility on,
// then calls on once and returns. It reports false at once when the
// session is out of reach.
func watchAccessibility(on func()) bool {
	session, err := dbus.ConnectSessionBus()
	if err != nil {
		return false
	}
	if accessibilityOn(session) {
		_ = session.Close()
		on()
		return true
	}
	changes := make(chan *dbus.Signal, 8)
	session.Signal(changes)
	if err = session.AddMatchSignal(
		dbus.WithMatchObjectPath("/org/a11y/bus"),
		dbus.WithMatchInterface("org.freedesktop.DBus.Properties"),
		dbus.WithMatchMember("PropertiesChanged"),
	); err != nil {
		_ = session.Close()
		return false
	}
	go func() {
		defer func() { _ = session.Close() }()
		for range changes {
			if accessibilityOn(session) {
				on()
				return
			}
		}
	}()
	return true
}

// startATSPI connects to the accessibility bus and registers the
// application. It returns nil when the bus is out of reach.
func startATSPI() *atspi {
	session, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil
	}
	defer func() { _ = session.Close() }()
	var addr string
	if err = session.Object("org.a11y.Bus", "/org/a11y/bus").Call("org.a11y.Bus.GetAddress", 0).Store(&addr); err != nil {
		return nil
	}
	a := &atspi{}
	conn, err := dbus.Connect(addr, dbus.WithHandler(a))
	if err != nil {
		return nil
	}
	a.conn = conn
	a.name = conn.Names()[0]
	var desktop ref
	err = conn.Object("org.a11y.atspi.Registry", atspiRootPath).
		Call("org.a11y.atspi.Socket.Embed", 0, ref{a.name, atspiRootPath}).Store(&desktop)
	if err != nil {
		_ = conn.Close()
		return nil
	}
	a.desktop = desktop
	return a
}

// add registers a window.
func (a *atspi) add(w *Window, popup bool) *atspiWindow {
	a.mu.Lock()
	a.nextID++
	aw := &atspiWindow{a: a, id: a.nextID, win: w, popup: popup}
	a.windows = append(a.windows, aw)
	index := len(a.windows) - 1
	a.mu.Unlock()
	a.emit(atspiRootPath, "Object.ChildrenChanged", "add", int32(index), dbus.MakeVariant(ref{a.name, aw.path(0)}))
	return aw
}

// remove unregisters a window.
func (a *atspi) remove(aw *atspiWindow) {
	a.mu.Lock()
	index := -1
	for i, o := range a.windows {
		if o == aw {
			index = i
		}
	}
	if index >= 0 {
		a.windows = append(a.windows[:index], a.windows[index+1:]...)
	}
	a.mu.Unlock()
	if index >= 0 {
		a.emit(atspiRootPath, "Object.ChildrenChanged", "remove", int32(index), dbus.MakeVariant(ref{a.name, aw.path(0)}))
	}
}

// path returns the path of node id in the window; 0 is the window.
func (aw *atspiWindow) path(id uint64) dbus.ObjectPath {
	if id == 0 {
		if t := aw.tree.Load(); t != nil {
			id = t.Root.ID
		}
	}
	return dbus.ObjectPath(atspiPrefix + "w" + strconv.Itoa(aw.id) + "_" + strconv.FormatUint(id, 10))
}

// publish takes a window's new tree, and tells screen readers what
// changed that they follow: focus, and a node's state, name or value.
func (a *atspi) publish(aw *atspiWindow, t *access.Tree) {
	parents := map[uint64]uint64{}
	var walk func(n *access.Node)
	walk = func(n *access.Node) {
		for _, k := range n.Children {
			parents[k.ID] = n.ID
			walk(k)
		}
	}
	walk(t.Root)
	old := aw.tree.Swap(t)
	aw.parents.Store(&parents)
	if old == nil {
		return
	}
	if t.Focus != nil && (old.Focus == nil || old.Focus.ID != t.Focus.ID) {
		if old.Focus != nil && t.Find(old.Focus.ID) != nil {
			a.emit(aw.path(old.Focus.ID), "Object.StateChanged", "focused", 0, dbus.MakeVariant(int32(0)))
		}
		a.emit(aw.path(t.Focus.ID), "Object.StateChanged", "focused", 1, dbus.MakeVariant(int32(0)))
	}
	var diff func(n *access.Node)
	diff = func(n *access.Node) {
		if o := old.Find(n.ID); o != nil {
			p := aw.path(n.ID)
			for _, s := range []struct {
				name  string
				state access.State
			}{{"checked", access.StateChecked}, {"selected", access.StateSelected}, {"expanded", access.StateExpanded}} {
				if was, is := o.State.Has(s.state), n.State.Has(s.state); was != is {
					a.emit(p, "Object.StateChanged", s.name, boolInt(is), dbus.MakeVariant(int32(0)))
				}
			}
			if o.Name != n.Name {
				a.emit(p, "Object.PropertyChange", "accessible-name", 0, dbus.MakeVariant(n.Name))
			}
			if o.Value != n.Value {
				a.emit(p, "Object.PropertyChange", "accessible-value", 0, dbus.MakeVariant(n.Value))
			}
		}
		for _, k := range n.Children {
			diff(k)
		}
	}
	diff(t.Root)
}

// focused tells screen readers the window took or lost the keyboard.
func (a *atspi) focused(aw *atspiWindow, in bool) {
	kind := "Deactivate"
	if in {
		kind = "Activate"
	}
	a.emit(aw.path(0), "Window."+kind, "", 0, dbus.MakeVariant(""))
	a.emit(aw.path(0), "Object.StateChanged", "active", boolInt(in), dbus.MakeVariant(int32(0)))
}

// emit sends an AT-SPI event from path.
func (a *atspi) emit(path dbus.ObjectPath, event, kind string, d1 int32, data dbus.Variant) {
	_ = a.conn.Emit(path, "org.a11y.atspi.Event."+event, kind, d1, int32(0), data, map[string]dbus.Variant{})
}

func boolInt(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

// object is what a path names: the application, or a node of a window.
type object struct {
	a    *atspi
	aw   *atspiWindow
	tree *access.Tree
	node *access.Node
}

// LookupObject implements [dbus.Handler].
func (a *atspi) LookupObject(path dbus.ObjectPath) (dbus.ServerObject, bool) {
	if path == atspiRootPath {
		return &object{a: a}, true
	}
	if path == "/org/a11y/atspi/cache" {
		return &object{a: a}, true
	}
	rest, ok := strings.CutPrefix(string(path), atspiPrefix+"w")
	if !ok {
		return nil, false
	}
	win, node, ok := strings.Cut(rest, "_")
	if !ok {
		return nil, false
	}
	wid, err1 := strconv.Atoi(win)
	nid, err2 := strconv.ParseUint(node, 10, 64)
	if err1 != nil || err2 != nil {
		return nil, false
	}
	a.mu.Lock()
	var aw *atspiWindow
	for _, o := range a.windows {
		if o.id == wid {
			aw = o
		}
	}
	a.mu.Unlock()
	if aw == nil {
		return nil, false
	}
	t := aw.tree.Load()
	n := t.Find(nid)
	if n == nil {
		return nil, false
	}
	return &object{a: a, aw: aw, tree: t, node: n}, true
}

// LookupInterface implements [dbus.ServerObject].
func (o *object) LookupInterface(name string) (dbus.Interface, bool) {
	var ms map[string]*method
	switch name {
	case ifProperties:
		ms = o.properties()
	case ifAccessible:
		ms = o.accessible()
	case ifApplication:
		if o.node == nil {
			ms = map[string]*method{
				"GetLocale": do("", o.locale, uint32(0)),
				// An empty address keeps screen readers on the shared bus.
				"GetApplicationBusAddress": do("", func([]any) any { return "" }),
			}
		}
	case ifCache:
		ms = map[string]*method{"GetItems": do([]cacheItem{}, func([]any) any { return []cacheItem{} })}
	case ifComponent:
		if o.node != nil {
			ms = o.component()
		}
	case ifAction:
		if o.node != nil && len(o.node.Actions) > 0 {
			ms = o.action()
		}
	case ifValue:
		if o.node != nil && o.node.Range != nil {
			ms = map[string]*method{}
		}
	case ifText:
		if o.node != nil && o.node.Role == access.RoleTextField {
			ms = o.text()
		}
	}
	if ms == nil {
		return nil, false
	}
	return methods(ms), true
}

// cacheItem is one entry of the Cache interface's GetItems, which this
// application leaves empty, so screen readers ask for each object.
type cacheItem struct {
	Self, App, Parent ref
	Index, Children   int32
	Interfaces        []string
	Name              string
	Role              uint32
	Description       string
	States            []uint32
}

type methods map[string]*method

// LookupMethod implements [dbus.Interface].
func (ms methods) LookupMethod(name string) (dbus.Method, bool) {
	m, ok := ms[name]
	return m, ok
}

// method is one D-Bus method: fn takes the call's arguments as they
// came, and in holds a zero value of each, for their types.
type method struct {
	fn  func(args []any) any
	in  []any
	out any
}

// do makes a method of fn, returning a value like out and taking
// arguments like in. A method with several return values has a
// multiple for out.
func do(out any, fn func(args []any) any, in ...any) *method {
	return &method{fn: fn, in: in, out: out}
}

// Call implements [dbus.Method].
func (m *method) Call(args ...any) ([]any, error) {
	if len(args) != len(m.in) {
		return nil, errors.New("atspi: wrong number of arguments")
	}
	r := m.fn(args)
	if multi, ok := r.(multiple); ok {
		return multi, nil
	}
	return []any{r}, nil
}

// NumArguments implements [dbus.Method].
func (m *method) NumArguments() int { return len(m.in) }

// NumReturns implements [dbus.Method].
func (m *method) NumReturns() int {
	if multi, ok := m.out.(multiple); ok {
		return len(multi)
	}
	return 1
}

// ArgumentValue implements [dbus.Method].
func (m *method) ArgumentValue(i int) any { return m.in[i] }

// ReturnValue implements [dbus.Method].
func (m *method) ReturnValue(i int) any {
	if multi, ok := m.out.(multiple); ok {
		return multi[i]
	}
	return m.out
}

// DecodeArguments implements [dbus.ArgumentDecoder]: the body, as it
// came.
func (m *method) DecodeArguments(_ *dbus.Conn, _ string, _ *dbus.Message, args []any) ([]any, error) {
	return args, nil
}

// multiple is a method's several return values.
type multiple []any

// Arguments are read through these, which give zero for one missing
// or of another type.
func argInt(args []any, i int) int32 {
	if i < len(args) {
		if v, ok := args[i].(int32); ok {
			return v
		}
	}
	return 0
}

func argUint(args []any, i int) uint32 {
	if i < len(args) {
		if v, ok := args[i].(uint32); ok {
			return v
		}
	}
	return 0
}

func argString(args []any, i int) string {
	if i < len(args) {
		if v, ok := args[i].(string); ok {
			return v
		}
	}
	return ""
}

// self is o's reference.
func (o *object) self() ref {
	if o.node == nil {
		return ref{o.a.name, atspiRootPath}
	}
	return ref{o.a.name, o.aw.path(o.node.ID)}
}

// parent is o's parent's reference.
func (o *object) parent() ref {
	switch o.node {
	case nil:
		return o.a.desktop
	case o.tree.Root:
		return ref{o.a.name, atspiRootPath}
	}
	if ps := o.aw.parents.Load(); ps != nil {
		if p, ok := (*ps)[o.node.ID]; ok {
			return ref{o.a.name, o.aw.path(p)}
		}
	}
	return ref{o.a.name, atspiRootPath}
}

// children are o's children's references.
func (o *object) children() []ref {
	out := []ref{}
	if o.node == nil {
		o.a.mu.Lock()
		defer o.a.mu.Unlock()
		for _, aw := range o.a.windows {
			if aw.tree.Load() != nil {
				out = append(out, ref{o.a.name, aw.path(0)})
			}
		}
		return out
	}
	for _, k := range o.node.Children {
		out = append(out, ref{o.a.name, o.aw.path(k.ID)})
	}
	return out
}

func (o *object) name() string {
	if o.node == nil {
		return filepath.Base(os.Args[0])
	}
	return o.node.Name
}

func (o *object) role() uint32 {
	if o.node == nil {
		return 75 // application
	}
	if o.node.Role == access.RoleWindow && o.aw.popup {
		return 69 // window
	}
	return atspiRole(o.node)
}

// atspiRole is a node's AT-SPI role.
func atspiRole(n *access.Node) uint32 {
	switch n.Role {
	case access.RoleWindow:
		return 23 // frame
	case access.RoleButton:
		return 43 // push button
	case access.RoleCheckbox:
		return 7
	case access.RoleSwitch:
		return 62 // toggle button
	case access.RoleSlider:
		return 51
	case access.RoleTextField:
		if n.State.Has(access.StateMultiline) {
			return 61 // text
		}
		return 79 // entry
	case access.RoleLabel:
		return 29
	case access.RoleHeading:
		return 83
	case access.RoleImage:
		return 27
	case access.RoleList:
		return 31
	case access.RoleListItem:
		return 32
	case access.RoleTabList:
		return 38
	case access.RoleTab:
		return 37
	case access.RoleMenu:
		return 33
	case access.RoleMenuItem:
		return 35
	case access.RoleComboBox:
		return 11
	case access.RoleDialog:
		return 16
	case access.RoleTooltip:
		return 64
	case access.RoleScrollArea:
		return 49
	case access.RoleLink:
		return 88
	case access.RoleTable:
		return 55
	case access.RoleRow:
		return 90 // table row
	case access.RoleCell:
		return 56 // table cell
	case access.RoleColumnHeader:
		return 57 // table column header
	case access.RoleMenuBar:
		return 34
	case access.RoleProgressBar:
		return 42
	case access.RoleScrollBar:
		return 48
	case access.RoleStatus:
		return 54 // status bar
	case access.RoleAlert:
		return 2
	case access.RoleRadioButton:
		return 44
	case access.RoleGroup:
	}
	return 39 // panel
}

// states are o's AT-SPI states, as two words of bits.
func (o *object) states() []uint32 {
	var bits uint64
	set := func(b uint) { bits |= 1 << b }
	set(8)  // enabled
	set(24) // sensitive
	set(30) // visible
	set(25) // showing
	if o.node != nil {
		n := o.node
		s := n.State
		if s.Has(access.StateDisabled) {
			bits &^= 1<<8 | 1<<24
		}
		if n.Focusable {
			set(11)
		}
		if n.Focused {
			set(12)
		}
		if n.Role == access.RoleWindow && o.aw.win.hasFocus() {
			set(1) // active
		}
		for _, m := range []struct {
			s   access.State
			bit uint
		}{
			{access.StateCheckable, 41}, {access.StateChecked, 4}, {access.StateSelected, 23},
			{access.StateExpandable, 9}, {access.StateExpanded, 10}, {access.StateEditable, 7},
			{access.StateReadOnly, 43}, {access.StateHasPopup, 42}, {access.StateModal, 16},
		} {
			if s.Has(m.s) {
				set(m.bit)
			}
		}
		switch n.Role {
		case access.RoleTab, access.RoleMenuItem, access.RoleListItem, access.RoleRow:
			set(22) // selectable
		case access.RoleTextField:
			if s.Has(access.StateMultiline) {
				set(17)
			} else {
				set(26)
			}
		case access.RoleSlider:
			set(14) // horizontal
		default:
		}
	}
	return []uint32{uint32(bits), uint32(bits >> 32)}
}

func (o *object) interfaces() []string {
	if o.node == nil {
		return []string{ifAccessible, ifApplication}
	}
	out := []string{ifAccessible, ifComponent}
	if len(o.node.Actions) > 0 {
		out = append(out, ifAction)
	}
	if o.node.Range != nil {
		out = append(out, ifValue)
	}
	if o.node.Role == access.RoleTextField {
		out = append(out, ifText)
	}
	return out
}

func (o *object) accessible() map[string]*method {
	return map[string]*method{
		"GetChildAtIndex": do(ref{}, func(args []any) any {
			kids := o.children()
			i := int(argInt(args, 0))
			if i < 0 || i >= len(kids) {
				return ref{"", atspiNullPath}
			}
			return kids[i]
		}, int32(0)),
		"GetChildren": do([]ref{}, func([]any) any { return o.children() }),
		"GetIndexInParent": do(int32(0), func([]any) any {
			p := o.parent()
			if p.Path == atspiRootPath && o.node == nil {
				return int32(0)
			}
			parent, ok := o.a.LookupObject(p.Path)
			if !ok {
				return int32(-1)
			}
			po, _ := parent.(*object)
			for i, k := range po.children() {
				if k.Path == o.self().Path {
					return int32(i)
				}
			}
			return int32(-1)
		}),
		"GetRelationSet": do([]struct {
			Type    uint32
			Targets []ref
		}{}, func([]any) any {
			return []struct {
				Type    uint32
				Targets []ref
			}{}
		}),
		"GetRole":              do(uint32(0), func([]any) any { return o.role() }),
		"GetRoleName":          do("", func([]any) any { return o.roleName() }),
		"GetLocalizedRoleName": do("", func([]any) any { return o.roleName() }),
		"GetState":             do([]uint32{}, func([]any) any { return o.states() }),
		"GetAttributes":        do(map[string]string{}, func([]any) any { return map[string]string{"toolkit": "gunim"} }),
		"GetApplication":       do(ref{}, func([]any) any { return ref{o.a.name, atspiRootPath} }),
		"GetInterfaces":        do([]string{}, func([]any) any { return o.interfaces() }),
	}
}

func (o *object) roleName() string {
	if o.node == nil {
		return "application"
	}
	return o.node.Role.String()
}

func (o *object) locale([]any) any { return "en" }

// screenBox returns o's bounds in screen coordinates, or in the
// window's when window is set.
func (o *object) screenBox(window bool) (x, y, w, h int32) {
	b := o.node.Bounds
	win := o.aw.win
	lo, hi := win.ToScreen(b.Min), win.ToScreen(b.Max)
	if window {
		origin := win.ToScreen(geom.Point{})
		lo, hi = lo.Sub(origin), hi.Sub(origin)
	}
	return int32(lo.X), int32(lo.Y), int32(hi.X - lo.X), int32(hi.Y - lo.Y)
}

func (o *object) component() map[string]*method {
	extents := func(coords uint32) struct{ X, Y, W, H int32 } {
		x, y, w, h := o.screenBox(coords != 0)
		return struct{ X, Y, W, H int32 }{x, y, w, h}
	}
	return map[string]*method{
		"GetExtents": do(struct{ X, Y, W, H int32 }{}, func(args []any) any { return extents(argUint(args, 0)) }, uint32(0)),
		"GetPosition": do(multiple{int32(0), int32(0)}, func(args []any) any {
			e := extents(argUint(args, 0))
			return multiple{e.X, e.Y}
		}, uint32(0)),
		"GetSize": do(multiple{int32(0), int32(0)}, func([]any) any {
			e := extents(0)
			return multiple{e.W, e.H}
		}),
		"Contains": do(false, func(args []any) any {
			e := extents(argUint(args, 2))
			x, y := argInt(args, 0), argInt(args, 1)
			return x >= e.X && y >= e.Y && x < e.X+e.W && y < e.Y+e.H
		}, int32(0), int32(0), uint32(0)),
		"GetAccessibleAtPoint": do(ref{}, func([]any) any { return ref{"", atspiNullPath} }, int32(0), int32(0), uint32(0)),
		"GetLayer":             do(uint32(0), func([]any) any { return uint32(3) }), // widget
		"GetMDIZOrder":         do(int16(0), func([]any) any { return int16(0) }),
		"GrabFocus": do(false, func([]any) any {
			o.request(access.Request{Focus: true})
			return true
		}),
		"GetAlpha": do(float64(0), func([]any) any { return float64(1) }),
	}
}

func (o *object) action() map[string]*method {
	actions := o.node.Actions
	name := func(args []any) any {
		if i := int(argInt(args, 0)); i >= 0 && i < len(actions) {
			return actions[i]
		}
		return ""
	}
	return map[string]*method{
		"GetName":          do("", name, int32(0)),
		"GetLocalizedName": do("", name, int32(0)),
		"GetDescription":   do("", name, int32(0)),
		"GetKeyBinding":    do("", func([]any) any { return "" }, int32(0)),
		"GetActions": do([]struct{ Name, Description, KeyBinding string }{}, func([]any) any {
			out := make([]struct{ Name, Description, KeyBinding string }, 0, len(actions))
			for _, a := range actions {
				out = append(out, struct{ Name, Description, KeyBinding string }{a, a, ""})
			}
			return out
		}),
		"DoAction": do(false, func(args []any) any {
			i := int(argInt(args, 0))
			if i < 0 || i >= len(actions) {
				return false
			}
			o.request(access.Request{Action: actions[i]})
			return true
		}, int32(0)),
	}
}

func (o *object) text() map[string]*method {
	runes := []rune(o.node.Value)
	return map[string]*method{
		"GetText": do("", func(args []any) any {
			from, to := int(argInt(args, 0)), int(argInt(args, 1))
			if to < 0 || to > len(runes) {
				to = len(runes)
			}
			from = max(0, min(from, to))
			return string(runes[from:to])
		}, int32(0), int32(0)),
		"GetCharacterAtOffset": do(int32(0), func(args []any) any {
			if i := int(argInt(args, 0)); i >= 0 && i < len(runes) {
				return runes[i]
			}
			return int32(0)
		}, int32(0)),
		"GetNSelections": do(int32(0), func([]any) any { return int32(0) }),
		"GetStringAtOffset": do(multiple{"", int32(0), int32(0)}, func([]any) any {
			return multiple{string(runes), int32(0), int32(len(runes))}
		}, int32(0), uint32(0)),
	}
}

// request hands a request for o's node to its window.
func (o *object) request(r access.Request) {
	r.ID = o.node.ID
	o.aw.win.in.Push(r)
}

// properties is org.freedesktop.DBus.Properties for o.
func (o *object) properties() map[string]*method {
	return map[string]*method{
		"Get": do(dbus.MakeVariant(""), func(args []any) any {
			v, ok := o.property(argString(args, 0), argString(args, 1))
			if !ok {
				return dbus.MakeVariant("")
			}
			return dbus.MakeVariant(v)
		}, "", ""),
		"GetAll": do(map[string]dbus.Variant{}, func(args []any) any {
			out := map[string]dbus.Variant{}
			iface := argString(args, 0)
			for _, p := range propertyNames[iface] {
				if v, ok := o.property(iface, p); ok {
					out[p] = dbus.MakeVariant(v)
				}
			}
			return out
		}, ""),
		"Set": do(multiple{}, func(args []any) any {
			if argString(args, 0) == ifValue && argString(args, 1) == "CurrentValue" && o.node != nil && len(args) > 2 {
				if v, ok := args[2].(dbus.Variant); ok {
					if f, ok := v.Value().(float64); ok {
						o.request(access.Request{Value: f, SetValue: true})
					}
				}
			}
			return multiple{}
		}, "", "", dbus.MakeVariant("")),
	}
}

var propertyNames = map[string][]string{
	ifAccessible:  {"Name", "Description", "Parent", "ChildCount", "Locale", "AccessibleId", "HelpText"},
	ifApplication: {"ToolkitName", "Version", "AtspiVersion", "Id"},
	ifAction:      {"NActions"},
	ifValue:       {"MinimumValue", "MaximumValue", "MinimumIncrement", "CurrentValue", "Text"},
	ifText:        {"CharacterCount", "CaretOffset"},
}

// property returns one of o's properties.
func (o *object) property(iface, name string) (any, bool) {
	switch iface + "." + name {
	case ifAccessible + ".Name":
		return o.name(), true
	case ifAccessible + ".Description":
		if o.node != nil {
			return o.node.Description, true
		}
		return "", true
	case ifAccessible + ".Parent":
		return o.parent(), true
	case ifAccessible + ".ChildCount":
		return int32(len(o.children())), true
	case ifAccessible + ".Locale":
		return "en", true
	case ifAccessible + ".AccessibleId":
		return string(o.self().Path), true
	case ifAccessible + ".HelpText":
		return "", true
	case ifApplication + ".ToolkitName":
		return "gunim", true
	case ifApplication + ".Version":
		return "0", true
	case ifApplication + ".AtspiVersion":
		return "2.1", true
	case ifApplication + ".Id":
		return int32(0), true
	}
	if o.node == nil {
		return nil, false
	}
	n := o.node
	switch iface + "." + name {
	case ifAction + ".NActions":
		return int32(len(n.Actions)), true
	case ifText + ".CharacterCount":
		return int32(len([]rune(n.Value))), true
	case ifText + ".CaretOffset":
		return int32(len([]rune(n.Value))), true
	case ifValue + ".Text":
		return n.Value, true
	}
	if r := n.Range; r != nil {
		switch name {
		case "MinimumValue":
			return r.Min, true
		case "MaximumValue":
			return r.Max, true
		case "MinimumIncrement":
			return r.Step, true
		case "CurrentValue":
			return r.Value, true
		}
	}
	return nil, false
}
