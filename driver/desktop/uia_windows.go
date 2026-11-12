//go:build windows && (amd64 || arm64)

package desktop

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
)

// On Windows, screen readers such as Narrator talk to applications
// through UI Automation. A window answers WM_GETOBJECT with a provider,
// a COM object standing for the window, and UI Automation asks it, and
// the providers it hands out, for the rest: each node's kind, name,
// state and place, and what it can do.
//
// A provider here stands for one node of a window, by its ID, and reads
// the tree the window published last on each call, so a provider for a
// node the window has since dropped answers that the element is gone.
// UI Automation calls providers on threads of its own. As on Linux, the
// tree is swapped whole, and a request, such as pressing a button, goes
// to the window's input for the engine to carry out.
//
// The window's own provider, the root, leaves its name and kind to the
// provider the system keeps for the window, which knows the title bar.

var (
	uiaCore                                    = windows.NewLazySystemDLL("uiautomationcore.dll")
	oleaut32                                   = windows.NewLazySystemDLL("oleaut32.dll")
	procUiaReturnRawElementProvider            = uiaCore.NewProc("UiaReturnRawElementProvider")
	procUiaHostProviderFromHwnd                = uiaCore.NewProc("UiaHostProviderFromHwnd")
	procUiaRaiseAutomationEvent                = uiaCore.NewProc("UiaRaiseAutomationEvent")
	procUiaRaiseAutomationPropertyChangedEvent = uiaCore.NewProc("UiaRaiseAutomationPropertyChangedEvent")
	procUiaRaiseStructureChangedEvent          = uiaCore.NewProc("UiaRaiseStructureChangedEvent")
	procUiaClientsAreListening                 = uiaCore.NewProc("UiaClientsAreListening")
	procUiaRaiseNotificationEvent              = uiaCore.NewProc("UiaRaiseNotificationEvent")
	procSafeArrayCreateVector                  = oleaut32.NewProc("SafeArrayCreateVector")
	procSafeArrayPutElement                    = oleaut32.NewProc("SafeArrayPutElement")
	procSysAllocString                         = oleaut32.NewProc("SysAllocString")
	procSysFreeString                          = oleaut32.NewProc("SysFreeString")
	procCoInitializeEx                         = windows.NewLazySystemDLL("ole32.dll").NewProc("CoInitializeEx")
	procCoUninitialize                         = windows.NewLazySystemDLL("ole32.dll").NewProc("CoUninitialize")
)

// uiaDebug is set by GUNIM_DEBUG_UIA=1, which logs each event raised,
// and what UI Automation answered, to standard error.
var uiaDebug = os.Getenv("GUNIM_DEBUG_UIA") == "1"

func uiaLog(format string, args ...any) {
	if uiaDebug {
		fmt.Fprintf(os.Stderr, "gunim uia: "+format+"\n", args...)
	}
}

// The interfaces an element answers to, in the order of its method
// table pointers.
const (
	ifSimple = iota
	ifFragment
	ifRoot
	ifInvoke
	ifToggle
	ifValue
	ifRange
	ifSelectionItem
	ifExpandCollapse
	ifaceCount
)

var iids = [ifaceCount]windows.GUID{
	ifSimple:         {Data1: 0xd6dd68d1, Data2: 0x86fd, Data3: 0x4332, Data4: [8]byte{0x86, 0x66, 0x9a, 0xbe, 0xde, 0xa2, 0xd2, 0x4c}},
	ifFragment:       {Data1: 0xf7063da8, Data2: 0x8359, Data3: 0x439c, Data4: [8]byte{0x92, 0x97, 0xbb, 0xc5, 0x29, 0x9a, 0x7d, 0x87}},
	ifRoot:           {Data1: 0x620ce2a5, Data2: 0xab8f, Data3: 0x40a9, Data4: [8]byte{0x86, 0xcb, 0xde, 0x3c, 0x75, 0x59, 0x9b, 0x58}},
	ifInvoke:         {Data1: 0x54fcb24b, Data2: 0xe18e, Data3: 0x47a2, Data4: [8]byte{0xb4, 0xd3, 0xec, 0xcb, 0xe7, 0x75, 0x99, 0xa2}},
	ifToggle:         {Data1: 0x56d00bd0, Data2: 0xc4f4, Data3: 0x433c, Data4: [8]byte{0xa8, 0x36, 0x1a, 0x52, 0xa5, 0x7e, 0x08, 0x92}},
	ifValue:          {Data1: 0xc7935180, Data2: 0x6fb3, Data3: 0x4201, Data4: [8]byte{0xb1, 0x74, 0x7d, 0xf7, 0x3a, 0xdb, 0xf6, 0x4a}},
	ifRange:          {Data1: 0x36dc7aef, Data2: 0x33e6, Data3: 0x4691, Data4: [8]byte{0xaf, 0xe1, 0x2b, 0xe7, 0x27, 0x4b, 0x3d, 0x33}},
	ifSelectionItem:  {Data1: 0x2acad808, Data2: 0xb2d4, Data3: 0x452d, Data4: [8]byte{0xa4, 0x07, 0x91, 0xff, 0x1a, 0xd1, 0x67, 0xb2}},
	ifExpandCollapse: {Data1: 0xd847d3a5, Data2: 0xcab0, Data3: 0x4a98, Data4: [8]byte{0x8c, 0x32, 0xec, 0xb4, 0x5c, 0x59, 0xad, 0x24}},
}

var iidIUnknown = windows.GUID{Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}

// Pattern IDs, for GetPatternProvider.
var patterns = map[int32]int{
	10000: ifInvoke,
	10002: ifValue,
	10003: ifRange,
	10005: ifExpandCollapse,
	10010: ifSelectionItem,
	10015: ifToggle,
}

const (
	hrOK               = 0
	hrNoInterface      = 0x80004002
	hrNotAvailable     = 0x80040201 // UIA_E_ELEMENTNOTAVAILABLE
	hrInvalidOperation = 0x80131509 // UIA_E_INVALIDOPERATION

	uiaRootObjectID        = -25
	uiaAppendRuntimeID     = 3
	providerServerSide     = 1
	vtEmpty                = 0
	vtI4                   = 3
	vtR8                   = 5
	vtBSTR                 = 8
	vtBool                 = 11
	variantTrue            = 0xffff
	childrenInvalidated    = 2
	eventFocusChanged      = 20005
	eventElementSelected   = 20012
	eventLiveRegionChanged = 20024
	propLiveSetting        = 30135
	propPositionInSet      = 30152
	propSizeOfSet          = 30153
	livePolite             = 1
	liveAssertive          = 2
	notificationOther      = 4
	notificationMostRecent = 3
	notificationAnnounceID = "gunim.announce"

	propControlType         = 30003
	propName                = 30005
	propHasKeyboardFocus    = 30008
	propIsKeyboardFocusable = 30009
	propIsEnabled           = 30010
	propHelpText            = 30013
	propIsControlElement    = 30016
	propIsContentElement    = 30017
	propFrameworkID         = 30024
	propValue               = 30045
	propRangeValue          = 30047
	propExpandCollapseState = 30070
	propIsSelected          = 30079
	propToggleState         = 30086
	propHeadingLevel        = 30173
	propIsDialog            = 30174
	headingLevel1           = 80051
)

// The control types, UI Automation's roles.
const (
	ctButton      = 50000
	ctCheckBox    = 50002
	ctComboBox    = 50003
	ctEdit        = 50004
	ctHyperlink   = 50005
	ctImage       = 50006
	ctListItem    = 50007
	ctList        = 50008
	ctMenu        = 50009
	ctMenuBar     = 50010
	ctMenuItem    = 50011
	ctProgressBar = 50012
	ctRadioButton = 50013
	ctScrollBar   = 50014
	ctSlider      = 50015
	ctTab         = 50018
	ctTabItem     = 50019
	ctText        = 50020
	ctToolTip     = 50022
	ctGroup       = 50026
	ctDataItem    = 50029
	ctWindow      = 50032
	ctPane        = 50033
	ctHeaderItem  = 50035
	ctTable       = 50036
	navParent     = 0
	navNext       = 1
	navPrevious   = 2
	navFirst      = 3
	navLast       = 4
)

// uiaWindow is one window, as UI Automation sees it.
type uiaWindow struct {
	win  *Window
	hwnd uintptr
	// asked is set once UI Automation has asked for the window's
	// provider; from then on the window publishes its tree.
	asked   atomic.Bool
	tree    atomic.Pointer[access.Tree]
	parents atomic.Pointer[map[uint64]uint64]
	// events carries events to raise, in order, to a goroutine of their
	// own: raising one can call back into the window. stop ends it.
	events chan func()
	stop   chan struct{}
	closed atomic.Bool
	// spoken is the announcement still to make, which speak wakes the goroutine for. A slot of its own keeps it
	// from being dropped with a full queue, and only the latest counts.
	spoken atomic.Pointer[string]
	speak  chan struct{}
	// liveAt is when each live node was last read out, and livePending the nodes waiting their turn; see liveChanged.
	liveMu      sync.Mutex
	liveAt      map[uint64]time.Time
	livePending map[uint64]bool
}

// liveEvery is how often a live node is read out at most, however often its words change.
var liveEvery = time.Second

func newUIAWindow(w *Window, hwnd uintptr) *uiaWindow {
	uw := &uiaWindow{win: w, hwnd: hwnd, events: make(chan func(), 64), stop: make(chan struct{}),
		speak: make(chan struct{}, 1), liveAt: map[uint64]time.Time{}, livePending: map[uint64]bool{}}
	go func() {
		// UI Automation's functions, raising events among them, need COM
		// on the thread that calls them.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		const coinitMultithreaded = 0
		hr, _, _ := procCoInitializeEx.Call(0, coinitMultithreaded)
		uiaLog("CoInitializeEx: HRESULT %#x", uint32(hr))
		if int32(hr) >= 0 {
			defer func() { _, _, _ = procCoUninitialize.Call() }()
		}
		for {
			select {
			case ev := <-uw.events:
				ev()
			case <-uw.speak:
				if s := uw.spoken.Swap(nil); s != nil {
					uw.notify(*s)
				}
			case <-uw.stop:
				return
			}
		}
	}()
	return uw
}

// getObject answers WM_GETOBJECT, on the main thread.
func (uw *uiaWindow) getObject(wParam, lParam uintptr) (uintptr, bool) {
	if int32(lParam) != uiaRootObjectID || uw.closed.Load() {
		return 0, false
	}
	if !uw.asked.Swap(true) {
		// Something now listens, so the window draws once more to hand
		// over its tree.
		uw.win.in.Push(driver.Redraw{})
	}
	e := uw.element(0)
	r, _, _ := procUiaReturnRawElementProvider.Call(uw.hwnd, wParam, lParam, e.iface(ifSimple))
	e.release()
	return r, true
}

// close lets UI Automation go of the window's providers.
func (uw *uiaWindow) close() {
	uw.closed.Store(true)
	uw.tree.Store(nil)
	_, _, _ = procUiaReturnRawElementProvider.Call(uw.hwnd, 0, 0, 0)
	close(uw.stop)
}

// keyboard reports whether the window has the keyboard, or, for a
// popup, the window it belongs to.
func (uw *uiaWindow) keyboard() bool {
	w := uw.win
	return w.hasFocus() || (w.parent != nil && w.parent.hasFocus())
}

// raise queues ev, dropping it when the queue is full.
func (uw *uiaWindow) raise(ev func()) {
	select {
	case <-uw.stop:
	case uw.events <- ev:
	default:
		uiaLog("dropped an event: the queue is full")
	}
}

// liveChanged has a screen reader read live node id again: at once where it was last read a second or more ago, and
// else once that second is up, with its words as they are then.
func (uw *uiaWindow) liveChanged(id uint64) {
	uw.liveMu.Lock()
	defer uw.liveMu.Unlock()
	if uw.livePending[id] {
		return
	}
	wait := liveEvery - time.Since(uw.liveAt[id])
	if wait <= 0 {
		uw.liveAt[id] = time.Now()
		uw.raise(func() { uw.event(id, eventLiveRegionChanged) })
		return
	}
	uw.livePending[id] = true
	time.AfterFunc(wait, func() {
		uw.liveMu.Lock()
		delete(uw.livePending, id)
		uw.liveAt[id] = time.Now()
		uw.liveMu.Unlock()
		uw.raise(func() { uw.event(id, eventLiveRegionChanged) })
	})
}

// focusChanged tells UI Automation where the window's focus is, when
// the window takes the keyboard.
func (uw *uiaWindow) focusChanged() {
	if t := uw.tree.Load(); t != nil && t.Focus != nil && t.Focus != t.Root {
		id := t.Focus.ID
		uiaLog("focus moved to node %d, %q; the window has the keyboard: %v", id, t.Focus.Name, uw.keyboard())
		uw.raise(func() { uw.event(id, eventFocusChanged) })
	}
}

// publish takes the window's new tree, and tells UI Automation what
// changed that screen readers follow: focus, children, and a node's
// state, name or value.
func (uw *uiaWindow) publish(t *access.Tree) {
	if uw.closed.Load() {
		return
	}
	parents := map[uint64]uint64{}
	var walk func(n *access.Node)
	walk = func(n *access.Node) {
		for _, k := range n.Children {
			parents[k.ID] = n.ID
			walk(k)
		}
	}
	walk(t.Root)
	uw.parents.Store(&parents)
	old := uw.tree.Swap(t)
	if r, _, _ := procUiaClientsAreListening.Call(); r == 0 {
		if old == nil {
			uiaLog("no clients listen for events")
		}
		return
	}
	if old == nil {
		uw.first(t)
		if t.Focus != nil && uw.keyboard() {
			uw.focusChanged()
		}
		return
	}
	var diff func(n *access.Node)
	diff = func(n *access.Node) {
		if o := old.Find(n.ID); o != nil {
			uw.diff(o, n, n == t.Root)
		} else if n.Role == access.RoleAlert {
			// An alert is read as it appears.
			uw.liveChanged(n.ID)
		}
		for _, k := range n.Children {
			diff(k)
		}
	}
	diff(t.Root)
	if t.Focus != nil && t.Focus != t.Root && (old.Focus == nil || old.Focus.ID != t.Focus.ID) {
		if uw.keyboard() {
			uw.focusChanged()
		} else {
			uiaLog("focus moved to node %d, but the window lacks the keyboard", t.Focus.ID)
		}
	}
}

// diff raises the events for what changed from o to n, one node in two
// trees.
func (uw *uiaWindow) diff(o, n *access.Node, root bool) {
	id := n.ID
	if root {
		id = 0
	}
	if !slices.EqualFunc(o.Children, n.Children, func(a, b *access.Node) bool { return a.ID == b.ID }) {
		uw.raise(func() { uw.structure(id) })
	}
	switch {
	case n.Role == access.RoleRadioButton && isSelected(o) != isSelected(n):
		was, is := isSelected(o), isSelected(n)
		uw.raise(func() { uw.changed(id, propIsSelected, boolVariant(was), boolVariant(is)) })
		if is {
			uw.raise(func() { uw.event(id, eventElementSelected) })
		}
	case o.State.Has(access.StateChecked) != n.State.Has(access.StateChecked):
		was, is := toggleState(o), toggleState(n)
		uw.raise(func() { uw.changed(id, propToggleState, intVariant(was), intVariant(is)) })
	}
	if live(n) && (o.Name != n.Name || o.Value != n.Value || !live(o)) {
		uw.liveChanged(id)
	}
	if o.State.Has(access.StateExpanded) != n.State.Has(access.StateExpanded) {
		was, is := expandState(o), expandState(n)
		uw.raise(func() { uw.changed(id, propExpandCollapseState, intVariant(was), intVariant(is)) })
	}
	if !o.State.Has(access.StateSelected) && n.State.Has(access.StateSelected) &&
		(n.Role == access.RoleTab || n.Role == access.RoleRow) {
		uw.raise(func() { uw.event(id, eventElementSelected) })
	}
	if o.Name != n.Name && !root {
		was, is := o.Name, n.Name
		uw.raise(func() { uw.changed(id, propName, stringVariant(was), stringVariant(is)) })
	}
	switch {
	case n.Range != nil && (o.Range == nil || o.Range.Value != n.Range.Value):
		was, is := n.Range.Value, n.Range.Value
		if o.Range != nil {
			was = o.Range.Value
		}
		uw.raise(func() { uw.changed(id, propRangeValue, floatVariant(was), floatVariant(is)) })
	case n.Range == nil && o.Value != n.Value && hasValue(n):
		was, is := o.Value, n.Value
		uw.raise(func() { uw.changed(id, propValue, stringVariant(was), stringVariant(is)) })
	}
}

// event raises an automation event from node id.
func (uw *uiaWindow) event(id uint64, event int32) {
	e := uw.element(id)
	defer e.release()
	hr, _, _ := procUiaRaiseAutomationEvent.Call(e.iface(ifSimple), uintptr(event))
	uiaLog("event %d from node %d: HRESULT %#x", event, id, uint32(hr))
}

// first tells UI Automation of the window's first tree: its children, and any alert in it, which is read too.
func (uw *uiaWindow) first(t *access.Tree) {
	uw.raise(func() { uw.structure(0) })
	for _, id := range alerts(t.Root) {
		uw.liveChanged(id)
	}
}

// alerts returns the IDs of the alerts under n.
func alerts(n *access.Node) []uint64 {
	var out []uint64
	if n.Role == access.RoleAlert {
		out = append(out, n.ID)
	}
	for _, k := range n.Children {
		out = append(out, alerts(k)...)
	}
	return out
}

// announce has a screen reader say text, from the window, through UiaRaiseNotificationEvent, and reports whether it
// passed the words on: false where nothing listens, or on Windows before 10 version 1709, which has no such call.
func (uw *uiaWindow) announce(text string) bool {
	if uw.closed.Load() || procUiaRaiseNotificationEvent.Find() != nil {
		return false
	}
	if r, _, _ := procUiaClientsAreListening.Call(); r == 0 {
		return false
	}
	uw.say(text)
	return true
}

// say puts text in the announcement's slot, in place of one not made yet, and wakes the COM goroutine for it.
func (uw *uiaWindow) say(text string) {
	uw.spoken.Store(&text)
	select {
	case uw.speak <- struct{}{}:
	default:
	}
}

// notify raises the notification event that says text. It runs on the window's COM goroutine.
func (uw *uiaWindow) notify(text string) {
	e := uw.element(0)
	defer e.release()
	words, activity := bstr(text), bstr(notificationAnnounceID)
	defer func() {
		_, _, _ = procSysFreeString.Call(words)
		_, _, _ = procSysFreeString.Call(activity)
	}()
	hr, _, _ := procUiaRaiseNotificationEvent.Call(e.iface(ifSimple), notificationOther, notificationMostRecent,
		words, activity)
	uiaLog("announced %q: HRESULT %#x", text, uint32(hr))
}

// changed raises a property's change on node id, and frees the two
// values.
func (uw *uiaWindow) changed(id uint64, prop int32, was, is variant) {
	defer was.clear()
	defer is.clear()
	e := uw.element(id)
	defer e.release()
	// A VARIANT, larger than a register, goes by the address of a copy.
	hr, _, _ := procUiaRaiseAutomationPropertyChangedEvent.Call(e.iface(ifSimple), uintptr(prop),
		uintptr(unsafe.Pointer(&was)), uintptr(unsafe.Pointer(&is)))
	uiaLog("property %d of node %d changed: HRESULT %#x", prop, id, uint32(hr))
}

// structure tells UI Automation node id's children changed.
func (uw *uiaWindow) structure(id uint64) {
	e := uw.element(id)
	defer e.release()
	rid := runtimeID(id)
	hr, _, _ := procUiaRaiseStructureChangedEvent.Call(e.iface(ifSimple), childrenInvalidated,
		uintptr(unsafe.Pointer(&rid[0])), uintptr(len(rid)))
	uiaLog("children of node %d changed: HRESULT %#x", id, uint32(hr))
}

// runtimeID is node id's runtime ID, which UI Automation adds to the
// window's own.
func runtimeID(id uint64) [3]int32 {
	return [3]int32{uiaAppendRuntimeID, int32(uint32(id)), int32(uint32(id >> 32))}
}

// element is a provider for one node of a window, or, with id 0, for
// the window itself. COM reaches it through the addresses of its method
// table pointers, one for each interface, which come first.
type element struct {
	ifaces [ifaceCount]*uintptr
	refs   atomic.Int32
	w      *uiaWindow
	id     uint64
}

// element returns a provider for node id, holding one reference. It
// stays alive, however COM holds it, until its references are gone.
func (uw *uiaWindow) element(id uint64) *element {
	tables()
	e := &element{w: uw, id: id}
	for k := range e.ifaces {
		e.ifaces[k] = &uiaTables[k][0]
	}
	e.refs.Store(1)
	uiaLive.Store(e, struct{}{})
	return e
}

// elementFor returns a provider for node n of tree t.
func (uw *uiaWindow) elementFor(t *access.Tree, n *access.Node) *element {
	if n == t.Root {
		return uw.element(0)
	}
	return uw.element(n.ID)
}

// iface is the address COM knows e by as interface k.
func (e *element) iface(k int) uintptr { return uintptr(unsafe.Pointer(&e.ifaces[k])) }

func (e *element) addRef() uintptr { return uintptr(e.refs.Add(1)) }

func (e *element) release() uintptr {
	n := e.refs.Add(-1)
	if n == 0 {
		uiaLive.Delete(e)
	}
	return uintptr(max(n, 0))
}

// hand gives e to COM through out, as interface k.
func (e *element) hand(out uintptr, k int) {
	*(*uintptr)(unsafe.Pointer(out)) = e.iface(k) //nolint:govet // COM's out parameter
}

// node returns the tree e reads and its node, or a nil node when the
// window has dropped it.
func (e *element) node() (*access.Tree, *access.Node) {
	t := e.w.tree.Load()
	if t == nil {
		if e.id == 0 && !e.w.closed.Load() {
			return nil, &access.Node{Info: access.Info{Role: access.RoleWindow}}
		}
		return nil, nil
	}
	if e.id == 0 {
		return t, t.Root
	}
	n := t.Find(e.id)
	return t, n
}

// request hands a request for e's node to its window.
func (e *element) request(r access.Request) uintptr {
	t, n := e.node()
	if n == nil || t == nil {
		return hrNotAvailable
	}
	r.ID = n.ID
	e.w.win.in.Push(r)
	return hrOK
}

// supports reports whether e answers to interface k.
func (e *element) supports(k int) bool {
	_, n := e.node()
	if n == nil {
		return false
	}
	switch k {
	case ifSimple, ifFragment:
		return true
	case ifRoot:
		return e.id == 0
	case ifInvoke:
		switch n.Role {
		case access.RoleCheckbox, access.RoleSwitch, access.RoleTab, access.RoleComboBox, access.RoleRadioButton:
			return false
		default:
			return slices.Contains(n.Actions, access.ActionPress)
		}
	case ifToggle:
		return n.State.Has(access.StateCheckable) && n.Role != access.RoleRadioButton
	case ifValue:
		return hasValue(n)
	case ifRange:
		return n.Range != nil
	case ifSelectionItem:
		return n.Role == access.RoleTab || n.Role == access.RoleRow || n.Role == access.RoleRadioButton
	case ifExpandCollapse:
		return n.State.Has(access.StateExpandable)
	}
	return false
}

func hasValue(n *access.Node) bool {
	return n.Role == access.RoleTextField || n.Role == access.RoleComboBox
}

func toggleState(n *access.Node) int32 {
	if n.State.Has(access.StateChecked) {
		return 1
	}
	return 0
}

func expandState(n *access.Node) int32 {
	if n.State.Has(access.StateExpanded) {
		return 1
	}
	return 0
}

// controlType is n's kind, as UI Automation names it.
func controlType(n *access.Node) int32 {
	switch n.Role {
	case access.RoleWindow, access.RoleDialog:
		return ctWindow
	case access.RoleButton, access.RoleSwitch:
		// A switch is a button that toggles, as Windows' own are.
		return ctButton
	case access.RoleCheckbox:
		return ctCheckBox
	case access.RoleSlider:
		return ctSlider
	case access.RoleTextField:
		return ctEdit
	case access.RoleLabel, access.RoleHeading:
		return ctText
	case access.RoleImage:
		return ctImage
	case access.RoleList:
		return ctList
	case access.RoleListItem:
		return ctListItem
	case access.RoleTabList:
		return ctTab
	case access.RoleTab:
		return ctTabItem
	case access.RoleMenu:
		return ctMenu
	case access.RoleMenuItem:
		return ctMenuItem
	case access.RoleComboBox:
		return ctComboBox
	case access.RoleTooltip:
		return ctToolTip
	case access.RoleScrollArea:
		return ctPane
	case access.RoleLink:
		return ctHyperlink
	case access.RoleTable:
		return ctTable
	case access.RoleRow:
		return ctDataItem
	case access.RoleCell:
		return ctText
	case access.RoleColumnHeader:
		return ctHeaderItem
	case access.RoleMenuBar:
		return ctMenuBar
	case access.RoleProgressBar:
		return ctProgressBar
	case access.RoleScrollBar:
		return ctScrollBar
	case access.RoleStatus, access.RoleAlert:
		// Text whose live setting says how soon a screen reader reads its changes.
		return ctText
	case access.RoleRadioButton:
		return ctRadioButton
	case access.RoleGroup:
		return ctGroup
	}
	return ctGroup
}

// variant is a VARIANT, as far as gunim fills one in: its type, and a
// value of up to 8 bytes.
type variant struct {
	vt  uint16
	_   [3]uint16
	val uint64
	_   uint64
}

func intVariant(i int32) variant     { return variant{vt: vtI4, val: uint64(uint32(i))} }
func floatVariant(f float64) variant { return variant{vt: vtR8, val: math.Float64bits(f)} }

func boolVariant(b bool) variant {
	if b {
		return variant{vt: vtBool, val: variantTrue}
	}
	return variant{vt: vtBool}
}

// stringVariant holds a copy of s, which clear frees.
func stringVariant(s string) variant { return variant{vt: vtBSTR, val: uint64(bstr(s))} }

func (v *variant) clear() {
	if v.vt == vtBSTR {
		_, _, _ = procSysFreeString.Call(uintptr(v.val))
	}
	*v = variant{}
}

// bstr returns a BSTR holding s, for its receiver to free.
func bstr(s string) uintptr {
	p, err := windows.UTF16PtrFromString(s)
	if err != nil {
		p, _ = windows.UTF16PtrFromString("")
	}
	r, _, _ := procSysAllocString.Call(uintptr(unsafe.Pointer(p)))
	return r
}

// property answers GetPropertyValue. Properties it leaves empty take
// UI Automation's defaults, or, for the window, the system's.
func (e *element) property(id int32) variant {
	_, n := e.node()
	if n == nil {
		return variant{}
	}
	root := e.id == 0
	switch id {
	case propControlType:
		if !root {
			return intVariant(controlType(n))
		}
	case propName:
		if !root && n.Name != "" {
			return stringVariant(n.Name)
		}
	case propHelpText:
		if n.Description != "" {
			return stringVariant(n.Description)
		}
	case propHasKeyboardFocus:
		if !root {
			return boolVariant(n.Focused && e.w.keyboard())
		}
	case propIsKeyboardFocusable:
		if !root {
			return boolVariant(n.Focusable)
		}
	case propIsEnabled:
		return boolVariant(!n.State.Has(access.StateDisabled))
	case propIsControlElement, propIsContentElement:
		if !root {
			return boolVariant(true)
		}
	case propFrameworkID:
		return stringVariant("gunim")
	case propHeadingLevel:
		if n.Role == access.RoleHeading {
			return intVariant(headingLevel1)
		}
	case propIsDialog:
		if n.Role == access.RoleDialog {
			return boolVariant(true)
		}
	case propLiveSetting:
		if n.Role == access.RoleStatus {
			return intVariant(livePolite)
		}
		if n.Role == access.RoleAlert {
			return intVariant(liveAssertive)
		}
	case propIsSelected:
		if n.Role == access.RoleRadioButton {
			return boolVariant(isSelected(n))
		}
	case propPositionInSet, propSizeOfSet:
		if n.Role != access.RoleRadioButton {
			break
		}
		t, _ := e.node()
		parents := e.w.parents.Load()
		if t == nil || parents == nil {
			break
		}
		pos, size := radioPlace(t.Find((*parents)[n.ID]), n.ID)
		if pos == 0 {
			break
		}
		if id == propPositionInSet {
			return intVariant(int32(pos))
		}
		return intVariant(int32(size))
	}
	return variant{}
}

// radioPlace returns where radio button id is among the radio buttons under parent, from 1, and how many there are,
// as "2 of 3": 0, 0 where parent holds no such button.
func radioPlace(parent *access.Node, id uint64) (pos, size int) {
	if parent == nil {
		return 0, 0
	}
	for _, k := range parent.Children {
		if k.Role != access.RoleRadioButton {
			continue
		}
		size++
		if k.ID == id {
			pos = size
		}
	}
	if pos == 0 {
		return 0, 0
	}
	return pos, size
}

// isSelected reports whether n is selected, as a tab or a row is, or, for a radio button, chosen.
func isSelected(n *access.Node) bool {
	return n.State.Has(access.StateSelected) || n.Role == access.RoleRadioButton && n.State.Has(access.StateChecked)
}

// live reports whether a screen reader follows n's changes as they come.
func live(n *access.Node) bool { return n.Role == access.RoleStatus || n.Role == access.RoleAlert }

// screenRect returns r, in the window's space, in screen pixels.
func (e *element) screenRect(r geom.Rect) [4]float64 {
	lo, hi := e.w.win.ToScreen(r.Min), e.w.win.ToScreen(r.Max)
	return [4]float64{float64(lo.X), float64(lo.Y), float64(hi.X - lo.X), float64(hi.Y - lo.Y)}
}

// navigate answers Navigate: node e's parent, sibling or child.
func (e *element) navigate(dir int32) *element {
	t, n := e.node()
	if t == nil || n == nil {
		return nil
	}
	var parent *access.Node
	if e.id != 0 {
		if ps := e.w.parents.Load(); ps != nil {
			if p, ok := (*ps)[n.ID]; ok {
				parent = t.Find(p)
			}
		}
	}
	pick := func(list []*access.Node, i int) *element {
		if i < 0 || i >= len(list) {
			return nil
		}
		return e.w.elementFor(t, list[i])
	}
	switch dir {
	case navParent:
		if parent != nil {
			return e.w.elementFor(t, parent)
		}
	case navNext, navPrevious:
		if parent == nil {
			return nil
		}
		i := slices.Index(parent.Children, n)
		if i < 0 {
			return nil
		}
		if dir == navNext {
			return pick(parent.Children, i+1)
		}
		return pick(parent.Children, i-1)
	case navFirst:
		return pick(n.Children, 0)
	case navLast:
		return pick(n.Children, len(n.Children)-1)
	}
	return nil
}

// at answers ElementProviderFromPoint: the deepest node at p, a screen
// point, or nil for the window itself.
func (e *element) at(p geom.Point) *element {
	t := e.w.tree.Load()
	if t == nil {
		return nil
	}
	q := e.w.win.FromScreen(p)
	n := t.Root
	for {
		var in *access.Node
		for _, k := range slices.Backward(n.Children) {
			if k.Bounds.Contains(q) {
				in = k
				break
			}
		}
		if in == nil {
			break
		}
		n = in
	}
	if n == t.Root {
		return nil
	}
	return e.w.element(n.ID)
}

// out writes v through COM's out parameter p.
func out[T any](p uintptr, v T) {
	*(*T)(unsafe.Pointer(p)) = v //nolint:govet // COM's out parameter
}

// handOrNil gives e to COM through p as interface k, or nil.
func handOrNil(p uintptr, e *element, k int) {
	if e == nil {
		out(p, uintptr(0))
		return
	}
	e.hand(p, k)
}

func boolOut(p uintptr, b bool) {
	if b {
		out(p, int32(1))
		return
	}
	out(p, int32(0))
}

// withNode runs fn with e's node, or answers that it is gone.
func withNode(this uintptr, k int, fn func(e *element, n *access.Node) uintptr) uintptr {
	e := elementAt(this, k)
	_, n := e.node()
	if n == nil {
		return hrNotAvailable
	}
	return fn(e, n)
}

// elementAt is the element COM knows as this, interface k.
func elementAt(this uintptr, k int) *element {
	return (*element)(unsafe.Pointer(this - uintptr(k)*unsafe.Sizeof(uintptr(0)))) //nolint:govet // an element COM holds
}

var (
	uiaTables     [ifaceCount][]uintptr
	uiaTablesOnce sync.Once
	// uiaLive holds the elements COM holds, which Go would otherwise
	// collect.
	uiaLive sync.Map
	// uiaFromPointGo and uiaSetRangeGo are the callbacks the assembly
	// shims in uia_windows_*.s go on to.
	uiaFromPointGo, uiaSetRangeGo uintptr //nolint:unused // read by the shims
)

// The assembly shims, which UI Automation calls at the addresses
// uiaThunks returns: see uia_windows_amd64.s.
func uiaFromPoint() //nolint:unused // called by UI Automation
func uiaSetRange()  //nolint:unused // called by UI Automation
func uiaThunks() (fromPoint, setRange uintptr)

// tables makes the method tables, once.
func tables() {
	uiaTablesOnce.Do(func() {
		cb := windows.NewCallback
		for k := range ifaceCount {
			uiaTables[k] = []uintptr{
				cb(func(this, riid, ppv uintptr) uintptr {
					e := elementAt(this, k)
					id := *(*windows.GUID)(unsafe.Pointer(riid)) //nolint:govet // COM's IID
					for j, iid := range iids {
						if iid == id && e.supports(j) {
							e.addRef()
							e.hand(ppv, j)
							return hrOK
						}
					}
					if id == iidIUnknown {
						e.addRef()
						e.hand(ppv, ifSimple)
						return hrOK
					}
					out(ppv, uintptr(0))
					return hrNoInterface
				}),
				cb(func(this uintptr) uintptr { return elementAt(this, k).addRef() }),
				cb(func(this uintptr) uintptr { return elementAt(this, k).release() }),
			}
		}
		add := func(k int, fns ...any) {
			for _, fn := range fns {
				uiaTables[k] = append(uiaTables[k], cb(fn))
			}
		}

		// IRawElementProviderSimple
		add(ifSimple,
			func(this, p uintptr) uintptr { out(p, int32(providerServerSide)); return hrOK },
			func(this, pattern, p uintptr) uintptr {
				e := elementAt(this, ifSimple)
				if k, ok := patterns[int32(pattern)]; ok && e.supports(k) {
					e.addRef()
					e.hand(p, k)
					return hrOK
				}
				out(p, uintptr(0))
				return hrOK
			},
			func(this, prop, p uintptr) uintptr {
				out(p, elementAt(this, ifSimple).property(int32(prop)))
				return hrOK
			},
			func(this, p uintptr) uintptr {
				e := elementAt(this, ifSimple)
				if e.id != 0 {
					out(p, uintptr(0))
					return hrOK
				}
				r, _, _ := procUiaHostProviderFromHwnd.Call(e.w.hwnd, p)
				return r
			},
		)

		// IRawElementProviderFragment
		add(ifFragment,
			func(this, dir, p uintptr) uintptr {
				handOrNil(p, elementAt(this, ifFragment).navigate(int32(dir)), ifFragment)
				return hrOK
			},
			func(this, p uintptr) uintptr {
				e := elementAt(this, ifFragment)
				if e.id == 0 {
					// The window's runtime ID is the system's.
					out(p, uintptr(0))
					return hrOK
				}
				rid := runtimeID(e.id)
				sa, _, _ := procSafeArrayCreateVector.Call(vtI4, 0, uintptr(len(rid)))
				for i := range rid {
					index := int32(i)
					_, _, _ = procSafeArrayPutElement.Call(sa, uintptr(unsafe.Pointer(&index)), uintptr(unsafe.Pointer(&rid[i])))
				}
				out(p, sa)
				return hrOK
			},
			func(this, p uintptr) uintptr {
				return withNode(this, ifFragment, func(e *element, n *access.Node) uintptr {
					if e.id == 0 {
						// The system knows the window's bounds.
						out(p, [4]float64{})
						return hrOK
					}
					out(p, e.screenRect(n.Bounds))
					return hrOK
				})
			},
			func(this, p uintptr) uintptr { out(p, uintptr(0)); return hrOK },
			func(this uintptr) uintptr {
				return elementAt(this, ifFragment).request(access.Request{Focus: true})
			},
			func(this, p uintptr) uintptr {
				e := elementAt(this, ifFragment)
				e.w.element(0).hand(p, ifRoot)
				return hrOK
			},
		)

		// IRawElementProviderFragmentRoot. ElementProviderFromPoint takes
		// its point in floating-point registers, and goes through a shim.
		uiaFromPointGo = cb(func(this, x, y, p uintptr) uintptr {
			e := elementAt(this, ifRoot)
			pt := geom.Pt(float32(math.Float64frombits(uint64(x))), float32(math.Float64frombits(uint64(y))))
			handOrNil(p, e.at(pt), ifFragment)
			return hrOK
		})
		fromPoint, setRange := uiaThunks()
		uiaTables[ifRoot] = append(uiaTables[ifRoot], fromPoint)
		add(ifRoot, func(this, p uintptr) uintptr {
			e := elementAt(this, ifRoot)
			t := e.w.tree.Load()
			if t == nil || t.Focus == nil || t.Focus == t.Root {
				out(p, uintptr(0))
				return hrOK
			}
			e.w.element(t.Focus.ID).hand(p, ifFragment)
			return hrOK
		})

		// IInvokeProvider
		add(ifInvoke, func(this uintptr) uintptr {
			return elementAt(this, ifInvoke).request(access.Request{Action: access.ActionPress})
		})

		// IToggleProvider
		add(ifToggle,
			func(this uintptr) uintptr {
				return elementAt(this, ifToggle).request(access.Request{Action: access.ActionPress})
			},
			func(this, p uintptr) uintptr {
				return withNode(this, ifToggle, func(_ *element, n *access.Node) uintptr {
					out(p, toggleState(n))
					return hrOK
				})
			},
		)

		// IValueProvider. Text is typed at a field, so setting it is
		// refused.
		add(ifValue,
			func(this, s uintptr) uintptr { return hrInvalidOperation },
			func(this, p uintptr) uintptr {
				return withNode(this, ifValue, func(_ *element, n *access.Node) uintptr {
					out(p, bstr(n.Value))
					return hrOK
				})
			},
			func(this, p uintptr) uintptr { boolOut(p, true); return hrOK },
		)

		// IRangeValueProvider. SetValue takes its value in a
		// floating-point register, and goes through a shim.
		uiaSetRangeGo = cb(func(this, v uintptr) uintptr {
			return elementAt(this, ifRange).request(access.Request{Value: math.Float64frombits(uint64(v)), SetValue: true})
		})
		uiaTables[ifRange] = append(uiaTables[ifRange], setRange)
		rangeGet := func(get func(r *access.Range) float64) func(this, p uintptr) uintptr {
			return func(this, p uintptr) uintptr {
				return withNode(this, ifRange, func(_ *element, n *access.Node) uintptr {
					if n.Range == nil {
						return hrNotAvailable
					}
					out(p, get(n.Range))
					return hrOK
				})
			}
		}
		add(ifRange,
			rangeGet(func(r *access.Range) float64 { return r.Value }),
			func(this, p uintptr) uintptr { boolOut(p, false); return hrOK },
			rangeGet(func(r *access.Range) float64 { return r.Max }),
			rangeGet(func(r *access.Range) float64 { return r.Min }),
			rangeGet(func(r *access.Range) float64 { return (r.Max - r.Min) / 10 }),
			rangeGet(func(r *access.Range) float64 {
				if r.Step > 0 {
					return r.Step
				}
				return (r.Max - r.Min) / 100
			}),
		)

		// ISelectionItemProvider: choosing a tab.
		selectTab := func(this uintptr) uintptr {
			return elementAt(this, ifSelectionItem).request(access.Request{Action: access.ActionPress})
		}
		add(ifSelectionItem,
			selectTab,
			selectTab,
			func(this uintptr) uintptr { return hrInvalidOperation },
			func(this, p uintptr) uintptr {
				return withNode(this, ifSelectionItem, func(_ *element, n *access.Node) uintptr {
					boolOut(p, isSelected(n))
					return hrOK
				})
			},
			func(this, p uintptr) uintptr {
				handOrNil(p, elementAt(this, ifSelectionItem).navigate(navParent), ifSimple)
				return hrOK
			},
		)

		// IExpandCollapseProvider
		add(ifExpandCollapse,
			func(this uintptr) uintptr {
				return elementAt(this, ifExpandCollapse).request(access.Request{Action: access.ActionOpen})
			},
			func(this uintptr) uintptr {
				return elementAt(this, ifExpandCollapse).request(access.Request{Action: access.ActionClose})
			},
			func(this, p uintptr) uintptr {
				return withNode(this, ifExpandCollapse, func(_ *element, n *access.Node) uintptr {
					out(p, expandState(n))
					return hrOK
				})
			},
		)
	})
}
