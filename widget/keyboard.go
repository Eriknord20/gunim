package widget

import (
	"image/color"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-text/typesetting/segmenter"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
)

// Keyboard tokens.
var (
	// KeyboardKey is the height of a [Keyboard]'s key, KeyboardGap the room between keys, and KeyboardText the size
	// of a key's letter.
	KeyboardKey    = theme.TextRow("keyboard.key", 44)
	KeyboardGap    = theme.Length("keyboard.gap", 6)
	KeyboardText   = theme.FontSize("keyboard.text", 20)
	KeyboardRadius = theme.Length("keyboard.radius", 8)
	// KeyboardFill is the fill behind the keys, and KeyboardKeyFill the fill of a key that types no letter, such as
	// Shift and Enter.
	KeyboardFill    = theme.Color("keyboard.fill", color.NRGBA{R: 0x17, G: 0x1a, B: 0x21, A: 0xff})
	KeyboardKeyFill = theme.Color("keyboard.key.fill", color.NRGBA{R: 0x1d, G: 0x20, B: 0x28, A: 0xff})
	// KeyboardKeyEdge edges every key, so a key stands clear of the panel at 3:1 or more.
	KeyboardKeyEdge = theme.Color("keyboard.key.edge", color.NRGBA{R: 0x7a, G: 0x82, B: 0x92, A: 0xff})
	// The names a screen reader gives the keys that type no letter, and the keyboard's own.
	KeyboardName      = theme.Choice("keyboard.name", "Keyboard")
	KeyboardShiftName = theme.Choice("keyboard.shift", "Shift")
	KeyboardDelete    = theme.Choice("keyboard.delete", "Delete")
	KeyboardEnter     = theme.Choice("keyboard.enter", "Enter")
	KeyboardSpace     = theme.Choice("keyboard.space", "Space")
	KeyboardHide      = theme.Choice("keyboard.hide", "Hide keyboard")
	KeyboardSigns     = theme.Choice("keyboard.signs", "Digits and signs")
	KeyboardLetters   = theme.Choice("keyboard.letters", "Letters")
	// KeyboardSignsLabel and KeyboardLettersLabel are what the keys that switch between the two pages show.
	KeyboardSignsLabel   = theme.Choice("keyboard.signs.label", "123")
	KeyboardLettersLabel = theme.Choice("keyboard.letters.label", "ABC")
)

// keyKind is what a key of a [Keyboard] does.
type keyKind uint8

const (
	keyChar keyKind = iota
	keyShift
	keyDelete
	keyPage
	keySpace
	keyEnter
	keyHide
)

// kbKey is one key of a [Keyboard]: what it does, what it types, and its width in keys.
type kbKey struct {
	kind keyKind
	text string
	wide float32
}

// keyRows returns the rows of keys of a page: letters, or digits and signs.
func keyRows(signs bool) [][]kbKey {
	chars := func(s string) []kbKey {
		out := make([]kbKey, 0, len(s))
		for _, r := range s {
			out = append(out, kbKey{kind: keyChar, text: string(r), wide: 1})
		}
		return out
	}
	join := func(parts ...[]kbKey) []kbKey {
		var out []kbKey
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	page, del := kbKey{kind: keyPage, wide: 1.5}, kbKey{kind: keyDelete, wide: 1.5}
	hide, space, enter := kbKey{kind: keyHide, wide: 1}, kbKey{kind: keySpace, text: " ", wide: 4}, kbKey{kind: keyEnter, wide: 1.5}
	comma, stop := kbKey{kind: keyChar, text: ",", wide: 1}, kbKey{kind: keyChar, text: ".", wide: 1}
	bottom := []kbKey{page, hide, comma, space, stop, enter}
	if signs {
		return [][]kbKey{
			chars("1234567890"),
			chars(`-/:;()&@"`),
			join(chars("+=_?!'#%"), []kbKey{del}),
			bottom,
		}
	}
	return [][]kbKey{
		chars("qwertyuiop"),
		chars("asdfghjkl"),
		join([]kbKey{{kind: keyShift, wide: 1.5}}, chars("zxcvbnm"), []kbKey{del}),
		bottom,
	}
}

// keyboardKeys is the most keys a page has.
const keyboardKeys = 40

// Shift's states: off, on for the next letter, and locked on.
const (
	shiftOff = iota
	shiftOnce
	shiftLocked
)

// Keyboard holds content and a keyboard drawn on the screen, for a screen with no keyboard of its own. The keyboard
// slides up from the bottom while a node that takes text has the focus, such as a [TextField] a finger tapped, and
// down when the focus leaves it. The content takes the room above the keyboard, and the focused field is scrolled
// into view as it opens.
//
// It has two pages: the letters, and the digits with a few signs. A key lights and glows under the finger. Shift
// makes the next letter a capital; pressed twice in a row it stays on until pressed again. Delete held down deletes
// on. The key with a chevron hides the keyboard, and the keyboard focus with it.
//
// A key types into the focused node through the same edit a phone's keyboard makes, [input.TextEdit], so a text
// field takes it as it takes a phone's typing. Delete takes off one character as it is seen, an accented letter or
// an emoji whole, and a finger that slides off it stops it. Enter in a field of one line is the Enter key, which
// commits.
//
// The keyboard has no way to know of a keyboard plugged in. An app that sees one, as by the keys it hears, sets
// Disabled, and the keyboard on the screen stays down.
type Keyboard struct {
	Control
	child gunim.Node

	open  *anim.Float
	signs bool
	shift int
	// shiftAt is when Shift was last pressed, for two presses that lock it.
	shiftAt time.Time
	keys    []kbKey
	rects   []geom.Rect
	lit     [keyboardKeys]*anim.Float
	texts   [keyboardKeys]shapedText
	click   Click[int]
	pressed int
	// repeat stops a held Delete deleting on.
	repeat func()
	// names are the names of the keys that type no letter, and the keyboard's own, in the theme of the last layout.
	names map[string]string
	// top is where the keys start, height the panel's height when open, and size the whole box, at the last layout.
	top, height float32
	size        geom.Size
	reveal      bool
}

// NewKeyboard returns content with a keyboard under it, hidden until a node that takes text has the focus.
func NewKeyboard(content gunim.Node) *Keyboard {
	k := &Keyboard{Control: newControl(), child: content, open: anim.NewFloat(0), pressed: -1}
	k.KeepFocus = true
	k.Add(k.open)
	for i := range k.lit {
		k.lit[i] = anim.NewFloat(0)
		k.Add(k.lit[i])
	}
	k.keys = flatKeys(keyRows(false))
	return k
}

// flatKeys returns the keys of rows in order.
func flatKeys(rows [][]kbKey) []kbKey {
	var out []kbKey
	for _, r := range rows {
		out = append(out, r...)
	}
	return out
}

// Shown reports whether the keyboard is up, or on its way up.
func (k *Keyboard) Shown() bool { return k.open.Target() > 0.5 }

// Focusable implements [gunim.Focusable]: the keyboard never takes the focus, which stays in the text it types into.
func (k *Keyboard) Focusable() bool { return false }

// KeepsFocus implements [gunim.FocusKeeper]: a press on a key leaves the focus in the text.
func (k *Keyboard) KeepsFocus() {}

// DragsTouch implements [gunim.TouchDragger]: a finger on a key keeps it, and never turns into a long press or a
// scroll, so Delete held down deletes on.
func (k *Keyboard) DragsTouch() bool { return k.held }

// Children implements [gunim.Composite].
func (k *Keyboard) Children() []gunim.Node { return []gunim.Node{k.child} }

// Handle implements [gunim.Handler]: the presses on the keys.
func (k *Keyboard) Handle(e input.Event, u *gunim.UI) bool {
	k.showTip(e, u, k)
	th := u.Theme()
	switch e := e.(type) {
	case input.PointerDown:
		i := k.keyAt(e.Pos)
		if e.Button != input.ButtonPrimary || i < 0 {
			return e.Pos.Y >= k.top && k.Shown()
		}
		k.held, k.pressed = true, i
		k.click.Press(e, i)
		k.lit[i].Animate(1, Quick.Get(th))
		if k.keys[i].kind == keyDelete {
			k.press(i, u)
			k.repeatFrom(i, u, 400*time.Millisecond)
		}
	case input.PointerUp:
		if !k.held {
			return false
		}
		k.held = false
		k.stopRepeat()
		i := k.pressed
		k.pressed = -1
		if i >= 0 && i < len(k.lit) {
			k.lit[i].Animate(0, Settle.Get(th))
		}
		if at := k.keyAt(e.Pos); k.click.Release(e, at) && at >= 0 && k.keys[at].kind != keyDelete {
			k.press(at, u)
		}
	case input.PointerMove:
		if !k.held {
			return false
		}
		// A finger that slides off the key it pressed lets it go: the key goes dark, and Delete stops.
		if i := k.pressed; i >= 0 && k.keyAt(e.Pos) != i {
			k.stopRepeat()
			k.lit[i].Animate(0, Settle.Get(th))
			k.pressed = -1
		}
	default:
		return false
	}
	return true
}

// repeatFrom deletes again after wait while Delete, key i, is held, and on, quicker.
func (k *Keyboard) repeatFrom(i int, u *gunim.UI, wait time.Duration) {
	k.repeat = u.After(wait, func(u *gunim.UI) {
		if !k.held || k.pressed != i {
			return
		}
		k.press(i, u)
		k.repeatFrom(i, u, 70*time.Millisecond)
	})
}

// stopRepeat stops a held Delete deleting on.
func (k *Keyboard) stopRepeat() {
	if k.repeat != nil {
		k.repeat()
		k.repeat = nil
	}
}

// keyAt returns the key under pos, or -1.
func (k *Keyboard) keyAt(pos geom.Point) int {
	if !k.Shown() {
		return -1
	}
	for i, r := range k.rects {
		if r.Contains(pos) {
			return i
		}
	}
	return -1
}

// press does what key i does.
func (k *Keyboard) press(i int, u *gunim.UI) {
	if i < 0 || i >= len(k.keys) {
		return
	}
	key := k.keys[i]
	switch key.kind {
	case keyChar, keySpace:
		k.typeText(k.typed(key), u)
		if k.shift == shiftOnce && key.kind == keyChar {
			k.shift = shiftOff
		}
	case keyShift:
		now := u.Now()
		switch {
		case k.shift == shiftOnce && now.Sub(k.shiftAt) < 400*time.Millisecond:
			k.shift = shiftLocked
		case k.shift == shiftOff:
			k.shift = shiftOnce
		default:
			k.shift = shiftOff
		}
		k.shiftAt = now
		u.Cue(gunim.CueTick, k)
	case keyDelete:
		k.deleteBack(u)
	case keyPage:
		k.signs = !k.signs
		k.keys = flatKeys(keyRows(k.signs))
		u.Cue(gunim.CueSelect, k)
	case keyEnter:
		k.enter(u)
	case keyHide:
		u.Cue(gunim.CueClose, k)
		u.Focus(nil)
	}
	u.Invalidate()
}

// typed is what key types: its text, a capital while Shift is on.
func (k *Keyboard) typed(key kbKey) string {
	if k.shift != shiftOff && !k.signs {
		r, _ := utf8.DecodeRuneInString(key.text)
		return string(unicode.ToUpper(r))
	}
	return key.text
}

// editor returns the focused node where it edits text of its own, as a text field does.
func focusedEditor(u *gunim.UI) (gunim.TextEditor, bool) {
	ed, ok := u.Focused().(gunim.TextEditor)
	return ed, ok && ed.TakesText()
}

// typeText types s into the focused node: as an edit of its text, where it has one, and as typed text otherwise.
func (k *Keyboard) typeText(s string, u *gunim.UI) {
	u.Cue(gunim.CueTick, k)
	if ed, ok := focusedEditor(u); ok {
		u.Type(ed.TextState().Commit(s))
		return
	}
	u.Type(input.TextInput{Text: s, Time: u.Now()})
}

// deleteBack deletes what is selected in the focused node, or the character before its caret.
func (k *Keyboard) deleteBack(u *gunim.UI) {
	u.Cue(gunim.CueTick, k)
	ed, ok := focusedEditor(u)
	if !ok {
		k.tap(input.KeyBackspace, u)
		return
	}
	st := ed.TextState()
	a, b := min(st.Selection[0], st.Selection[1]), max(st.Selection[0], st.Selection[1])
	if a == b {
		i := a - st.Start
		if i <= 0 || i > len(st.Text) {
			// The character is outside the stretch of text the node gave: the key deletes it.
			k.tap(input.KeyBackspace, u)
			return
		}
		a -= lastCharacter(st.Text[:i])
	}
	u.Type(input.TextEdit{Replace: [2]int{a, b}, Selection: [2]int{a, a}, Composing: [2]int{a, a}, Time: u.Now()})
}

// lastCharacter returns how many bytes the last character of s takes, as a reader sees a character: a letter with
// its accents, or an emoji with its joiners and skin tone, whole.
func lastCharacter(s string) int {
	// A character is a few runes at most; the last few dozen hold it.
	start := len(s)
	for n := 0; n < 32 && start > 0; n++ {
		_, size := utf8.DecodeLastRuneInString(s[:start])
		start -= size
	}
	runes := []rune(s[start:])
	var seg segmenter.Segmenter
	seg.Init(runes)
	last := 0
	for it := seg.GraphemeIterator(); it.Next(); {
		last = it.Grapheme().Offset
	}
	return len(string(runes[last:]))
}

// enter starts a new line in text of many lines, and is the Enter key in text of one.
func (k *Keyboard) enter(u *gunim.UI) {
	if ed, ok := focusedEditor(u); ok && ed.TextState().Multiline {
		k.typeText("\n", u)
		return
	}
	u.Cue(gunim.CuePress, k)
	k.tap(input.KeyEnter, u)
}

// tap presses key and lets it go, as from a keyboard.
func (k *Keyboard) tap(key input.Key, u *gunim.UI) {
	u.Type(input.KeyPress{Key: key, Time: u.Now()})
	u.Type(input.KeyRelease{Key: key, Time: u.Now()})
}

// Layout implements [gunim.Node]: the content in the room the keyboard leaves, and the keys under it as far as the
// keyboard is up.
func (k *Keyboard) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	th := f.Theme
	k.follow(th)
	if k.names == nil {
		k.names = map[string]string{}
	}
	for _, t := range []theme.Token[string]{KeyboardName, KeyboardShiftName, KeyboardDelete, KeyboardEnter, KeyboardSpace,
		KeyboardHide, KeyboardSigns, KeyboardLetters} {
		k.names[t.Key()] = t.Get(th)
	}
	want := false
	if u := f.UI(); u != nil {
		want = u.FocusTakesText() && !k.Disabled
	}
	if to := anim.OnOff(want); k.open.Target() != to {
		if !k.laid || f.UI() == nil {
			k.open.Jump(to)
		} else {
			k.open.Animate(to, Settle.Get(th))
		}
		k.reveal = want
		if !want {
			k.shift = shiftOff
			k.stopRepeat()
		}
	}
	size := fillOr(c, geom.Sz(480, 320))
	k.size = size
	gap, h := KeyboardGap.Get(th), KeyboardKey.Get(th)
	rows := keyRows(k.signs)
	k.height = float32(len(rows))*h + float32(len(rows)+1)*gap
	shown := k.height * min(max(k.open.Value(), 0), 1)
	k.top = size.H - shown
	child := kids.At(0)
	child.Layout(gunim.Tight(geom.Sz(size.W, max(size.H-shown, 0))))
	child.Place(geom.Point{})

	// The keys: ten to a row, each row centred. A key as wide as several takes the gaps between them too.
	unit := (size.W - 11*gap) / 10
	wide := func(key kbKey) float32 { return key.wide*unit + (key.wide-1)*gap }
	k.rects = k.rects[:0]
	y := k.top + gap
	for _, row := range rows {
		w := -gap
		for _, key := range row {
			w += wide(key) + gap
		}
		x := (size.W - w) / 2
		for _, key := range row {
			k.rects = append(k.rects, geom.Rc(x, y, wide(key), h))
			x += wide(key) + gap
		}
		y += h + gap
	}
	// The field is brought into view once the keyboard has landed: on the way up, the room above it still shrinks.
	if u := f.UI(); u != nil && k.reveal && !k.open.Active() && k.open.Value() > 0.99 {
		k.reveal = false
		u.After(0, k.revealFocus)
	}
	return size
}

// revealFocus scrolls the focused node into the room above the keyboard, now it has landed, whole and with room
// round it where there is more to scroll.
func (k *Keyboard) revealFocus(u *gunim.UI) {
	if n := u.Focused(); n != nil && k.Shown() {
		u.Reveal(n)
	}
}

// Paint implements [gunim.Node].
func (k *Keyboard) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	if kids.Len() > 0 {
		kids.At(0).Paint(p)
	}
	if k.top >= box.H-0.5 {
		return
	}
	th := f.Theme
	defer k.faint(p, box)()
	panel := geom.Rc(0, k.top, box.W, box.H-k.top)
	// Part way up, the rows still below the edge are cut off there; a clip to a rectangle costs no layer.
	defer p.Layer(paint.LayerOpts{Bounds: panel, Clip: true, Opacity: 1})()
	p.RRect(panel, 0, paint.Solid(KeyboardFill.Get(th)))
	radius := KeyboardRadius.Get(th)
	for i, r := range k.rects {
		if i >= len(k.keys) || r.Min.Y >= box.H {
			break
		}
		k.paintKey(p, th, i, r, radius)
	}
}

// paintKey draws key i in r.
func (k *Keyboard) paintKey(p *paint.Painter, th *theme.Live, i int, r geom.Rect, radius float32) {
	key := k.keys[i]
	lit := min(max(k.lit[i].Value(), 0), 1)
	rest := ButtonFill.Get(th)
	if key.kind != keyChar && key.kind != keySpace {
		rest = KeyboardKeyFill.Get(th)
	}
	if key.kind == keyEnter {
		rest = ButtonPrimaryFill.Get(th)
	}
	p.RRectStroke(r, radius, paint.Solid(anim.Mix(anim.ColorCodec, rest, ButtonHover.Get(th), lit)),
		paint.Stroke{Width: 1, Color: KeyboardKeyEdge.Get(th)})
	if i == k.pressed {
		k.paintGlow(p, r, radius, th)
	}
	ink := Ink.Get(th)
	if key.kind == keyEnter {
		ink = ButtonPrimaryInk.Get(th)
	}
	var ic *icon.Icon
	label := ""
	switch key.kind {
	case keyChar:
		label = k.typed(key)
	case keyShift:
		ic = icon.ArrowBigUp
		if k.shift != shiftOff {
			ink = Accent.Get(th)
		}
	case keyDelete:
		ic = icon.Delete
	case keyEnter:
		ic = icon.CornerDownLeft
	case keyHide:
		ic = icon.ChevronDown
	case keySpace:
	case keyPage:
		label = KeyboardSignsLabel.Get(th)
		if k.signs {
			label = KeyboardLettersLabel.Get(th)
		}
	}
	c := r.Center()
	if ic != nil {
		s := IconSize.Get(th)
		drawIcon(p, th, ic, geom.Rc(c.X-s/2, c.Y-s/2, s, s), ink, 1)
		if key.kind == keyShift && k.shift == shiftLocked {
			p.RRect(geom.Rc(c.X-s/2, c.Y+s/2+2, s, 2), 1, paint.Solid(ink))
		}
		return
	}
	if label == "" {
		return
	}
	size := KeyboardText.Get(th)
	if key.kind != keyChar {
		size = TextSize.Get(th)
	}
	run := k.texts[i].shape(faceIn(Font, th), label, size)
	run.Paint(p, geom.Pt(c.X-run.Advance/2, c.Y-run.Height()/2), ink)
}

// name is t in the theme of the last layout, or its default before one.
func (k *Keyboard) name(t theme.Token[string]) string {
	if v, ok := k.names[t.Key()]; ok {
		return v
	}
	return t.Default()
}

// keyName is key i's name for a screen reader.
func (k *Keyboard) keyName(i int) string {
	key := k.keys[i]
	switch key.kind {
	case keyShift:
		return k.name(KeyboardShiftName)
	case keyDelete:
		return k.name(KeyboardDelete)
	case keyEnter:
		return k.name(KeyboardEnter)
	case keySpace:
		return k.name(KeyboardSpace)
	case keyHide:
		return k.name(KeyboardHide)
	case keyPage:
		if k.signs {
			return k.name(KeyboardLetters)
		}
		return k.name(KeyboardSigns)
	case keyChar:
	}
	return k.typed(key)
}

// Access implements [gunim.Accessible]: the keyboard while it is up, with a part for each key.
func (k *Keyboard) Access() access.Info {
	info := access.Info{Role: access.RoleGroup, Name: k.name(KeyboardName), State: k.accessState()}
	if !k.Shown() {
		// Down, it has no keys to press.
		return info
	}
	for i, r := range k.rects {
		if i >= len(k.keys) {
			break
		}
		part := access.Info{Role: access.RoleButton, Name: k.keyName(i), Bounds: r, Actions: []string{access.ActionPress}}
		if k.keys[i].kind == keyShift && k.shift != shiftOff {
			part.State = access.StateCheckable | access.StateChecked
		}
		info.Parts = append(info.Parts, part)
	}
	return info
}

// AccessAct implements [gunim.AccessActor]: a press on a key does what the key does.
func (k *Keyboard) AccessAct(r access.Request, u *gunim.UI) bool {
	if r.Action != access.ActionPress || !k.Shown() || r.Part < 0 || r.Part >= len(k.keys) {
		return false
	}
	k.press(r.Part, u)
	return true
}
