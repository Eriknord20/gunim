package widget

import (
	"fmt"
	"image/color"
	"os"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

// Menubar tokens.
var (
	MenubarFill   = theme.Color("menubar.fill", color.NRGBA{R: 0x17, G: 0x1a, B: 0x22, A: 0xff})
	MenubarHeight = theme.Length("menubar.height", 30)
	// MenubarHot lights the title under the pointer, and the title of
	// the menu that is open.
	MenubarHot = theme.Color("menubar.hot", color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x16})
)

// BarMenu is one menu of a [Menubar]: its title, and its items. The
// title and the items' labels may mark their access keys with a & before
// the letter, as "&File"; see [Menu.AccessKeys]. The bar makes the menu
// anew each time it opens, from the items as they are then.
type BarMenu struct {
	Title string
	Items []MenuItem
}

// Menubar is a row of menu titles along the top of a window, each
// opening its menu below it.
//
// A compact menubar keeps its menus behind one button, drawn as three
// lines. A click opens a list of the menus' titles, and each opens its
// menu beside the list as the pointer or the arrow keys come to it.
// Right goes into that menu and Left back out to the list. The rest of
// a chromeless window's title bar is left free to move the window by.
//
// A click on a title opens its menu; with one open, the pointer moving
// onto another title opens that one instead, and a light glides along
// the bar to it. While a menu is open the menubar has the keyboard:
// Left and Right move between menus, Up, Down and Enter work the menu,
// and Escape closes it, giving the keyboard back where it was. A click
// on the bar leaves the keyboard where it is, so the bar's commands
// act on what has it.
//
// Wherever the keyboard is, Alt and a title's access key open its
// menu, and F10 or Alt pressed alone puts the keyboard on the bar with
// the first title lit, for Left, Right and Down to work. While Alt is
// held, or the keyboard opened the bar, the access keys are underlined.
type Menubar struct {
	anim.Group
	Menus []BarMenu
	// Title is the window's title, drawn faint in the middle of the room
	// the menus leave, when the bar is a chromeless window's title bar.
	// A compact bar draws it after its button, and Subtitle after it,
	// fainter, such as what the window shows now.
	Title    string
	Subtitle string
	// Compact keeps the menus behind one button.
	Compact bool
	// OnPick runs on the UI goroutine with the menu and the item picked,
	// once the menu has closed. It may act in the window through u; a
	// non-nil result is sent to the application as the bar's intent.
	OnPick func(menu, item int, u *gunim.UI) gunim.Intent
	// OnHighlight runs on the UI goroutine when the highlight moves,
	// with the open menu and the item highlighted on it, -1 for none.
	// Closing the menus runs it with -1 for both. A non-nil result is sent
	// as the bar's intent.
	OnHighlight func(menu, item int, u *gunim.UI) gunim.Intent

	open  int
	popup *gunim.Popup
	menu  *Menu
	back  gunim.Node
	// over is the title under the pointer, or -1.
	over int

	// list is a compact bar's list of the menus, on panel in listPopup,
	// and inMenu says the keys work the menu open beside it rather than
	// the list.
	list      *Menu
	panel     *barPanel
	listPopup *gunim.Popup
	inMenu    bool
	// keyed says the list's highlight is moving by a key, not the
	// pointer, and wait cancels a menu waiting to open beside the list
	// while the pointer heads for the one open.
	keyed bool
	wait  func()
	// armed says the keyboard is on the bar with no menu open and title lit lit. byKeys says the keyboard opened
	// the bar, and altDown that Alt is held; either underlines the access keys.
	armed, byKeys, altDown bool
	lit                    int

	titles   []shapedText
	titleRun shapedText
	subRun   shapedText
	subEll   shapedText
	// spans holds each title's left and right edges, and height the
	// bar's height, from the last layout.
	spans    [][2]float32
	height   float32
	lightX   *anim.Float
	lightW   *anim.Float
	lightOn  *anim.Float
	lightSet bool
}

// NewMenubar returns a menubar of menus.
func NewMenubar(menus ...BarMenu) *Menubar {
	b := &Menubar{Menus: menus, open: -1, over: -1, lightX: anim.NewFloat(0), lightW: anim.NewFloat(0), lightOn: anim.NewFloat(0)}
	b.Add(b.lightX, b.lightW, b.lightOn)
	return b
}

// KeepsFocus implements [gunim.FocusKeeper].
func (b *Menubar) KeepsFocus() {}

// IsOpen reports whether a menu is open, or a compact bar's list.
func (b *Menubar) IsOpen() bool { return b.open >= 0 || b.listPopup != nil }

// Open opens menu i, taking the keyboard, as F10 does in most
// programs. A compact bar opens its list, with menu i's line lit and
// its menu beside it, and the keys on the list: Down and Up move along
// it, and Right or Enter go into the menu.
func (b *Menubar) Open(i int, u *gunim.UI) {
	if i < 0 || i >= len(b.Menus) || i == b.open {
		return
	}
	menuf("open %d, was %d, over %d, armed %v", i, b.open, b.over, b.armed)
	if !b.IsOpen() {
		u.Cue(gunim.CueOpen, b)
		if !b.armed {
			b.back = u.Focused()
		}
	}
	b.armed = false
	if b.Compact {
		b.showList(u)
		b.list.Highlight(i)
		b.openBeside(i, u)
		return
	}
	b.shut(u)
	b.open = i
	menu := b.newMenu(i)
	menu.OnHighlight = func(item int, u *gunim.UI) gunim.Intent {
		b.highlight(i, item, u)
		return nil
	}
	menu.OnPick = func(item int, u *gunim.UI) gunim.Intent {
		b.pick(i, item, u)
		return nil
	}
	b.menu = menu
	span := b.span(i)
	b.popup = u.OpenPopup(b, menu, gunim.PopupOptions{
		Anchor:  geom.Rect{Min: geom.Pt(span[0], 0), Max: geom.Pt(span[1], MenubarHeight.Get(u.Theme()))},
		Max:     geom.Sz(600, 800),
		Dismiss: dismissed(b, b.Close),
	})
	b.aim(i, u)
	u.Focus(b)
	b.highlight(i, -1, u)
	u.Invalidate()
}

// newMenu makes menu i's [Menu].
func (b *Menubar) newMenu(i int) *Menu {
	m := NewMenu(nil)
	b.fill(m, i)
	m.MinWidth = 180
	return m
}

// fill gives m menu i's items and what the bar says about them.
func (b *Menubar) fill(m *Menu, i int) {
	m.SetItems(b.Menus[i].Items)
	m.AccessKeys, m.cues = true, b.byKeys
}

// showList opens a compact bar's list of its menus, with none open
// beside it, and takes the keyboard.
func (b *Menubar) showList(u *gunim.UI) {
	if b.listPopup != nil {
		return
	}
	if b.open < 0 && b.back == nil {
		b.back = u.Focused()
	}
	titles := make([]MenuItem, len(b.Menus))
	for i, m := range b.Menus {
		titles[i] = MenuItem{Label: m.Title, Hint: "›"}
	}
	list := NewMenu(titles)
	list.AccessKeys, list.cues = true, b.byKeys
	list.MinWidth = 160
	list.OnHighlight = func(i int, u *gunim.UI) gunim.Intent {
		b.stopWaiting()
		if i < 0 || i == b.open {
			return nil
		}
		if !b.keyed && b.aiming() {
			// Crossing lines on the way to the menu open: it stays, unless the pointer rests
			b.wait = u.After(menuAimWait, func(u *gunim.UI) {
				b.wait = nil
				if b.list != nil && b.list.Highlighted() == i && i != b.open {
					b.inMenu = false
					b.openBeside(i, u)
				}
			})
			return nil
		}
		b.inMenu = false
		b.openBeside(i, u)
		return nil
	}
	list.OnPick = func(i int, u *gunim.UI) gunim.Intent {
		if i != b.open {
			b.openBeside(i, u)
		}
		b.enterMenu(u)
		return nil
	}
	b.list = list
	b.panel = newBarPanel(b, list)
	span := b.span(0)
	// No cap on its size: the panel keeps itself to the room the screen leaves
	b.listPopup = u.OpenPopup(b, b.panel, gunim.PopupOptions{
		Anchor:  geom.Rect{Min: geom.Pt(span[0], 0), Max: geom.Pt(span[1], MenubarHeight.Get(u.Theme()))},
		Dismiss: b.Close,
	})
	b.aim(0, u)
	u.Focus(b)
	u.Invalidate()
}

// menuAimWait is how long the pointer rests on a line of a compact bar's list, while it heads for the menu open
// beside the list, before that line's menu opens.
const menuAimWait = 120 * time.Millisecond

// aiming reports whether the pointer's last move on a compact bar's list headed for the menu open beside it, on the
// list's right or its left: into the wedge from where it was to the near edge of the menu's card.
func (b *Menubar) aiming() bool {
	if b.menu == nil || b.panel == nil {
		return false
	}
	return headsFor(b.list.wasPointer, b.list.pointer, b.panel.cardInList(), b.panel.left)
}

// stopWaiting cancels a menu waiting to open beside a compact bar's list.
func (b *Menubar) stopWaiting() {
	if b.wait != nil {
		b.wait()
		b.wait = nil
	}
}

// openBeside opens menu i beside its line on a compact bar's list: on the card beside the list, where the menu open
// before fades out as it fades in.
func (b *Menubar) openBeside(i int, u *gunim.UI) {
	b.shut(u)
	b.open, b.inMenu = i, false
	menu := b.newMenu(i)
	menu.bare = true
	menu.OnHighlight = func(item int, u *gunim.UI) gunim.Intent {
		if item >= 0 && b.list != nil {
			// The pointer on the menu: the keys follow it there, and the list keeps its line lit.
			b.inMenu = true
			b.list.Highlight(i)
		}
		b.highlight(i, item, u)
		return nil
	}
	menu.OnPick = func(item int, u *gunim.UI) gunim.Intent {
		b.pick(i, item, u)
		return nil
	}
	b.menu = menu
	u.Insert(b.panel, menu)
	b.highlight(i, -1, u)
	u.Invalidate()
}

// enterMenu has the keys work the menu open beside a compact bar's
// list, from its first item.
func (b *Menubar) enterMenu(u *gunim.UI) {
	if b.menu == nil {
		return
	}
	b.inMenu = true
	if b.menu.Highlighted() < 0 {
		b.menu.Key(input.KeyPress{Key: input.KeyHome}, u)
	}
	u.Invalidate()
}

// highlight runs OnHighlight.
func (b *Menubar) highlight(menu, item int, u *gunim.UI) {
	if b.OnHighlight != nil {
		send(u, b, b.OnHighlight(menu, item, u))
	}
}

// pick closes the menus and runs OnPick.
func (b *Menubar) pick(menu, item int, u *gunim.UI) {
	b.Close(u)
	if b.OnPick != nil {
		send(u, b, b.OnPick(menu, item, u))
	}
}

// Close closes the open menu, and a compact bar's list, and gives the
// keyboard back.
func (b *Menubar) Close(u *gunim.UI) {
	if !b.IsOpen() && !b.armed {
		return
	}
	menuf("close %d, over %d, armed %v", b.open, b.over, b.armed)
	b.shut(u)
	b.stopWaiting()
	if b.listPopup != nil {
		b.listPopup.Close()
		b.listPopup, b.list, b.panel = nil, nil, nil
	}
	b.open, b.inMenu, b.armed, b.byKeys = -1, false, false, false
	b.highlight(-1, -1, u)
	if b.back != nil {
		u.Focus(b.back)
		b.back = nil
	} else if u.Focused() == b {
		u.Focus(nil)
	}
	b.aim(b.over, u)
	u.Invalidate()
}

// shut closes the open menu: its popup, or on a compact bar, the menu beside the list, which fades from the card.
func (b *Menubar) shut(u *gunim.UI) {
	if b.popup != nil {
		b.popup.Close()
		b.popup = nil
	}
	if b.menu != nil && b.Compact {
		u.Remove(b.menu)
	}
	b.menu = nil
}

func (b *Menubar) span(i int) [2]float32 {
	if i >= 0 && i < len(b.spans) {
		return b.spans[i]
	}
	return [2]float32{}
}

// CaptionRects implements [gunim.Caption]: in a chromeless window, the
// bar where no title is moves the window, as a title bar does.
func (b *Menubar) CaptionRects(size geom.Size) []geom.Rect {
	end := float32(0)
	if n := len(b.spans); n > 0 {
		end = b.spans[n-1][1]
	}
	if end >= size.W {
		return nil
	}
	return []geom.Rect{geom.Rc(end, 0, size.W-end, size.H)}
}

// aim glides the light to title i, or fades it for -1.
func (b *Menubar) aim(i int, u *gunim.UI) {
	motion := Quick.Get(u.Theme())
	if i < 0 {
		b.lightOn.Animate(0, motion)
		return
	}
	s := b.span(i)
	if !b.lightSet || b.lightOn.Value() < 0.05 {
		b.lightX.Jump(s[0])
		b.lightW.Jump(s[1] - s[0])
		b.lightSet = true
	} else {
		b.lightX.Animate(s[0], motion)
		b.lightW.Animate(s[1]-s[0], motion)
	}
	b.lightOn.Animate(1, motion)
}

// titleAt returns the title at p, in the bar's space, or -1.
func (b *Menubar) titleAt(p geom.Point) int {
	for i, s := range b.spans {
		if p.X >= s[0] && p.X < s[1] {
			return i
		}
	}
	return -1
}

// Handle implements [gunim.Handler].
func (b *Menubar) Handle(e input.Event, u *gunim.UI) bool {
	if menuDebug {
		menuf("%T %+v, open %d, over %d", e, e, b.open, b.over)
	}
	switch e := e.(type) {
	case input.PointerEnter:
		b.hover(b.titleAt(e.Pos), u)
		return true
	case input.PointerMove:
		b.hover(b.titleAt(e.Pos), u)
		return true
	case input.PointerLeave:
		b.over = -1
		switch {
		case b.armed:
			b.aim(b.lit, u)
		case b.open < 0:
			b.aim(-1, u)
		}
		return true
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		i := b.titleAt(e.Pos)
		b.byKeys = false
		switch {
		case i < 0:
		case b.Compact && b.IsOpen():
			b.Close(u)
		case b.Compact:
			b.showList(u)
		case i == b.open:
			b.Close(u)
		default:
			b.Open(i, u)
		}
		return true
	case input.KeyPress:
		return b.key(e, u)
	case input.KeyRelease:
		b.altUp(e, u)
	case input.AltTapped:
		if !b.IsOpen() && !b.armed {
			return false
		}
		b.Close(u)
		return true
	case input.FocusLost:
		if b.armed && !b.IsOpen() {
			// Focus went elsewhere, as by a click: it stays there.
			b.back = nil
			b.Close(u)
		}
	}
	return false
}

// CatchKey implements [gunim.KeyCatcher]: F10 or Alt alone puts the keyboard on the bar, Alt and a title's access
// key opens its menu, and Alt held underlines the access keys.
func (b *Menubar) CatchKey(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.AltTapped:
		b.altDown = false
		b.arm(u)
		return true
	case input.KeyRelease:
		b.altUp(e, u)
	case input.WindowFocusLost:
		b.altDown = false
	case input.KeyPress:
		switch {
		case e.Key == input.KeyF10 && e.Mods == 0:
			b.arm(u)
			return true
		case altKey(e.Key):
			b.altHeld(e, u)
		case altOnly(e.Mods):
			return b.openByKey(keyRune(e), u)
		}
	}
	return false
}

// altHeld underlines the access keys while Alt is held alone.
func (b *Menubar) altHeld(e input.KeyPress, u *gunim.UI) {
	if e.Mods&^input.ModAlt == 0 && !b.altDown {
		b.altDown = true
		u.Invalidate()
	}
}

// altUp takes the underlines away as Alt is let go.
func (b *Menubar) altUp(e input.KeyRelease, u *gunim.UI) {
	if altKey(e.Key) && b.altDown {
		b.altDown = false
		u.Invalidate()
	}
}

// arm puts the keyboard on the bar with its first title lit and no menu open, as F10 and Alt alone do; a compact
// bar opens its list. With the keyboard on the bar already, it gives it back.
func (b *Menubar) arm(u *gunim.UI) {
	if b.IsOpen() || b.armed {
		b.Close(u)
		return
	}
	if len(b.Menus) == 0 {
		return
	}
	b.byKeys = true
	if b.Compact {
		b.Open(0, u)
		return
	}
	b.back = u.Focused()
	b.armed, b.lit = true, 0
	u.Focus(b)
	b.aim(0, u)
	u.Invalidate()
}

// openByKey opens the menu whose title's access key is r, from the keyboard, and reports whether there is one.
func (b *Menubar) openByKey(r rune, u *gunim.UI) bool {
	if r == 0 {
		return false
	}
	for i, m := range b.Menus {
		if _, key, _ := accessKey(m.Title); key == r {
			b.openKeyed(i, u)
			return true
		}
	}
	return false
}

// openKeyed opens menu i from the keyboard, with the keys in the menu from its first item.
func (b *Menubar) openKeyed(i int, u *gunim.UI) {
	b.byKeys = true
	b.Open(i, u)
	b.enterMenu(u)
}

// backToBar closes the open menu and leaves its title lit, with the keyboard on the bar, as Escape does in a menu
// opened from the keyboard.
func (b *Menubar) backToBar(u *gunim.UI) {
	i := b.open
	b.shut(u)
	b.stopWaiting()
	b.open, b.inMenu = -1, false
	b.highlight(-1, -1, u)
	if u.Focused() != b {
		b.back = u.Focused()
		u.Focus(b)
	}
	b.armed, b.lit = true, i
	b.aim(i, u)
	u.Invalidate()
}

// armedKey works the bar while it has the keyboard with no menu open: Left and Right move along the titles; Down,
// Up, Enter and Space open the one lit; a title's access key opens its menu.
func (b *Menubar) armedKey(k input.KeyPress, u *gunim.UI) bool {
	n := len(b.Menus)
	switch k.Key {
	case input.KeyLeft:
		b.lit = (b.lit + n - 1) % n
		b.aim(b.lit, u)
	case input.KeyRight:
		b.lit = (b.lit + 1) % n
		b.aim(b.lit, u)
	case input.KeyDown, input.KeyUp, input.KeyEnter, input.KeyKPEnter, input.KeySpace:
		b.openKeyed(b.lit, u)
	case input.KeyEscape, input.KeyF10:
		b.Close(u)
	case input.KeyTab:
		b.Close(u)
		return false
	default:
		if k.Mods.Has(input.ModControl) {
			return false
		}
		b.openByKey(keyRune(k), u)
	}
	u.Invalidate()
	return true
}

// menuDebug is set by GUNIM_DEBUG_MENU=1, which logs to standard error
// what a menu bar hears and the menus it opens and closes.
var menuDebug = os.Getenv("GUNIM_DEBUG_MENU") == "1"

// menuf logs a menu bar's doing, under GUNIM_DEBUG_MENU=1.
func menuf(format string, args ...any) {
	if menuDebug {
		fmt.Fprintf(os.Stderr, "gunim menu %s: %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
	}
}

// hover follows the pointer onto title i, or off the titles for -1:
// with a menu open, onto that title's menu.
func (b *Menubar) hover(i int, u *gunim.UI) {
	if i == b.over {
		return
	}
	b.over = i
	switch {
	case b.Compact:
		if !b.IsOpen() {
			b.aim(i, u)
		}
	case b.open >= 0 && i >= 0:
		b.Open(i, u)
	case b.armed && i >= 0:
		b.lit = i
		b.aim(i, u)
	case b.open < 0 && !b.armed:
		b.aim(i, u)
	}
}

func (b *Menubar) key(k input.KeyPress, u *gunim.UI) bool {
	if altKey(k.Key) {
		b.altHeld(k, u)
		return b.IsOpen() || b.armed
	}
	if b.Compact {
		return b.compactKey(k, u)
	}
	if b.armed && b.open < 0 {
		return b.armedKey(k, u)
	}
	if b.open < 0 {
		return false
	}
	n := len(b.Menus)
	switch k.Key {
	case input.KeyLeft:
		b.Open((b.open+n-1)%n, u)
	case input.KeyRight:
		b.Open((b.open+1)%n, u)
	case input.KeyEscape:
		u.Cue(gunim.CueClose, b)
		if b.byKeys {
			b.backToBar(u)
		} else {
			b.Close(u)
		}
	case input.KeyF10:
		b.Close(u)
	case input.KeyTab:
		b.Close(u)
		return false
	default:
		if altOnly(k.Mods) && b.openByKey(keyRune(k), u) {
			return true
		}
		if b.menu != nil && b.menu.Key(k, u) {
			u.Invalidate()
			return true
		}
		// While a menu is open, other keys stop here, so nothing behind
		// the menu acts on them.
		return !k.Mods.Has(input.ModControl)
	}
	return true
}

// compactKey works a compact bar's list, and the menu beside it.
func (b *Menubar) compactKey(k input.KeyPress, u *gunim.UI) bool {
	if !b.IsOpen() {
		return false
	}
	if k.Key == input.KeyEscape || k.Key == input.KeyF10 {
		u.Cue(gunim.CueClose, b)
		b.Close(u)
		return true
	}
	if k.Key == input.KeyTab {
		b.Close(u)
		return false
	}
	var used bool
	switch {
	case altOnly(k.Mods) && b.openByKey(keyRune(k), u):
		used = true
	case b.inMenu && k.Key == input.KeyLeft:
		// Back out to the list, the menu left open beside it.
		b.inMenu = false
		b.menu.Highlight(-1)
		used = true
	case b.inMenu && k.Key == input.KeyRight:
		used = true
	case b.inMenu:
		used = b.menu != nil && b.menu.Key(k, u)
	case k.Key == input.KeyRight:
		b.enterMenu(u)
		used = true
	case k.Key == input.KeyLeft:
		used = true
	default:
		b.keyed = true
		used = b.list != nil && b.list.Key(k, u)
		b.keyed = false
	}
	if used {
		u.Invalidate()
		return true
	}
	// While the list is open, other keys stop here, so nothing behind
	// it acts on them.
	return !k.Mods.Has(input.ModControl)
}

// Layout implements [gunim.Node]. The bar is as wide as it is given.
func (b *Menubar) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	th := f.Theme
	if len(b.titles) != len(b.Menus) {
		b.titles = make([]shapedText, len(b.Menus))
	}
	b.spans = b.spans[:0]
	pad := MenuRowPadding.Get(th)
	x := float32(4)
	if b.Compact {
		// One square button, the whole bar high.
		h := MenubarHeight.Get(th)
		b.spans = append(b.spans, [2]float32{x, x + h + 4})
		size := c.Constrain(geom.Sz(c.Max.W, h))
		b.height = size.H
		return size
	}
	for i, m := range b.Menus {
		shown, _, _ := accessKey(m.Title)
		w := b.titles[i].shape(faceIn(Font, th), shown, TextSize.Get(th)).Advance + 2*pad
		b.spans = append(b.spans, [2]float32{x, x + w})
		x += w
	}
	size := c.Constrain(geom.Sz(max(c.Max.W, x), MenubarHeight.Get(th)))
	b.height = size.H
	return size
}

// Paint implements [gunim.Node].
func (b *Menubar) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(MenubarFill.Get(th)))
	if t := min(max(b.lightOn.Value(), 0), 1); t > 0.01 {
		c := MenubarHot.Get(th)
		c.A = uint8(float32(c.A) * t)
		p.RRect(geom.Rc(b.lightX.Value(), 3, b.lightW.Value(), box.H-6), 5, paint.Solid(c))
	}
	pad := MenuRowPadding.Get(th)
	ink := Ink.Get(th)
	if b.Compact {
		b.paintCompact(p, f, box)
		return
	}
	cues := b.byKeys || b.altDown
	for i, s := range b.spans {
		run := b.titles[i].run
		at := geom.Pt(s[0]+pad, (box.H-run.Height())/2)
		run.Paint(p, at, ink)
		if cues {
			_, _, k := accessKey(b.Menus[i].Title)
			underline(p, run, k, at, ink)
		}
	}
	if b.Title != "" && f.Chromeless() {
		// In the middle of the bar, or of the room the menus leave when
		// that would run into them, and left out when there is none.
		end := float32(0)
		if n := len(b.spans); n > 0 {
			end = b.spans[n-1][1] + 2*pad
		}
		run := b.titleRun.shape(faceIn(Font, th), b.Title, TextSize.Get(th))
		x := (box.W - run.Advance) / 2
		if x < end {
			x = end + (box.W-end-run.Advance)/2
		}
		if x >= end {
			faintInk := ink
			faintInk.A = uint8(float32(faintInk.A) * 0.6)
			run.Paint(p, geom.Pt(x, (box.H-run.Height())/2), faintInk)
		}
	}
}

// paintCompact draws a compact bar: its button, then the title and the
// subtitle, fainter, where there is room.
func (b *Menubar) paintCompact(p *paint.Painter, f gunim.Frame, box geom.Size) {
	th := f.Theme
	ink := Ink.Get(th)
	s := b.span(0)
	const side = 16
	c := geom.Pt((s[0]+s[1])/2, box.H/2)
	paintIcon(p, th, icon.Menu, geom.Rc(c.X-side/2, c.Y-side/2, side, side), ink, 1)
	if !f.Chromeless() {
		return
	}
	pad := MenuRowPadding.Get(th)
	x := s[1] + pad
	room := box.W - x - pad
	size := TextSize.Get(th)
	if b.Title != "" && room > 0 {
		run := b.titleRun.shape(faceIn(Font, th), b.Title, size)
		if run.Advance <= room {
			run.Paint(p, geom.Pt(x, (box.H-run.Height())/2), ink)
			x += run.Advance + pad
			room -= run.Advance + pad
		} else {
			room = 0
		}
	}
	if b.Subtitle != "" && room > 0 {
		faint := ink
		faint.A = uint8(float32(faint.A) * 0.6)
		run := b.subRun.shape(faceIn(Font, th), b.Subtitle, size)
		if run.Advance > room {
			run = cutRunWith(run, b.subEll.shape(faceIn(Font, th), "…", size), room)
		}
		run.Paint(p, geom.Pt(x, (box.H-run.Height())/2), faint)
	}
}
