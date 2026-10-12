package widget

import (
	"fmt"
	"image/color"
	"math"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
)

// Tree tokens.
var (
	TreeRowHeight = theme.Length("tree.row", 26)
	// TreeIndent is how far each level sits in from the one above it.
	TreeIndent = theme.Length("tree.indent", 16)
	// TreeHover lights the row under the pointer, and TreeSelected the cursor's row.
	TreeHover    = theme.Color("tree.hover", color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x0e})
	TreeSelected = theme.Color("tree.selected", color.NRGBA{R: 0x5e, G: 0x9c, B: 0xff, A: 0x30})
	// TreeGuide draws the lines that run down beside each open branch's rows.
	TreeGuide = theme.Color("tree.guide", color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x12})
	// TreeBranch colours a branch's icon, as a folder's.
	TreeBranch = theme.Color("tree.branch", color.NRGBA{R: 0xe8, G: 0xb3, B: 0x4a, A: 0xff})
	treeNoGap  = theme.Length("tree.gap", 0)
)

// TreeItem is what a [Tree] shows for one row.
type TreeItem struct {
	Text string
	// Depth is how many branches the row sits under.
	Depth int
	// Branch says the row can open, as a folder can, and Open that it is.
	Branch, Open bool
	// Icon is drawn before the text, in IconInk or else the text's ink. A branch without one shows a folder.
	Icon    *icon.Icon
	IconInk *theme.Token[color.NRGBA]
	// Detail is quiet text at the row's right end, such as a file's size.
	Detail string
	// Faint dims the row, as for a note or a file that cannot be shown.
	Faint bool
	// Face is the text's face, and the theme's [Font] when unset.
	Face theme.Token[*text.Face]
}

// Tree shows a hierarchy as rows, a branch's rows under it and set in, and scrolls through them, building only the
// rows in view.
//
// The application keeps the hierarchy and gives the tree the rows of the open branches, in order, with SetKeys. Rows
// that arrive grow into place and rows that go collapse, so a branch opens and shuts with motion, and its chevron
// turns as it does. A selection pill slides to the cursor's row. The arrow keys move the cursor, Right and Left open
// and shut branches, and Enter, Space or a click activates the row. A row cut short shows its whole text in a tooltip.
type Tree struct {
	anim.Group
	Item func(Key) TreeItem
	// OnActivate runs on the UI goroutine with the row a click, Enter or Space activates, such as to open a branch
	// or show a file. A non-nil result is sent to the application as the tree's intent.
	OnActivate func(key Key, u *gunim.UI) gunim.Intent

	list   *VirtualList
	keys   []Key
	index  map[Key]int
	cursor Key
	// at is the cursor's place, kept when its row goes so the keys carry on from there.
	at      int
	focused bool
	cue     ringCue
	// lag is how far the pill trails the cursor's row, springing to nothing, and shown how present it is.
	lag, shown *anim.Float
	tip        PartTip
	rowH       float32
}

// NewTree returns an empty tree.
func NewTree() *Tree {
	t := &Tree{index: map[Key]int{}, at: -1, lag: anim.NewFloat(0), shown: anim.NewFloat(0)}
	t.list = NewVirtualList(func(k Key) gunim.Node { return newTreeRow(t, k) })
	t.list.Spacing = treeNoGap
	t.Add(t.lag, t.shown)
	return t
}

// SetKeys makes keys the tree's rows, in order: the rows of the open branches, each branch's rows after it.
func (t *Tree) SetKeys(keys []Key, u *gunim.UI) {
	t.keys = keys
	clear(t.index)
	for i, k := range keys {
		t.index[k] = i
	}
	if i, ok := t.index[t.cursor]; ok {
		t.at = i
	}
	t.list.SetKeys(keys, u)
}

// Cursor returns the cursor's row, and false when it is on none.
func (t *Tree) Cursor() (Key, bool) {
	_, ok := t.index[t.cursor]
	return t.cursor, ok
}

// SetCursor puts the cursor on key's row and scrolls it into view, as for the file a tree's application shows, and
// sends no intent. The pill slides over from where it was; with a nil u it jumps, and the view stays.
func (t *Tree) SetCursor(key Key, u *gunim.UI) {
	if key == t.cursor {
		return
	}
	i, ok := t.index[key]
	if u == nil {
		t.cursor = key
		if ok {
			t.at = i
		}
		t.lag.Jump(0)
		return
	}
	if ok {
		t.move(i, u)
		return
	}
	t.cursor = key
	u.Invalidate()
}

// move puts the cursor on row i, the pill sliding over from where it was.
func (t *Tree) move(i int, u *gunim.UI) {
	if len(t.keys) == 0 {
		return
	}
	i = min(max(i, 0), len(t.keys)-1)
	from, had := t.pillY()
	t.cursor, t.at = t.keys[i], i
	if to, ok := t.rowY(t.cursor); ok && had {
		t.lag.Jump(from - to)
		t.lag.Animate(0, Quick.Get(u.Theme()))
	} else {
		t.lag.Jump(0)
	}
	h := TreeRowHeight.Get(u.Theme())
	t.list.revealContent(geom.Rc(0, float32(i)*h, 1, h), u)
	u.Invalidate()
}

// rowY is where key's row is in the list's content, while the row is built.
func (t *Tree) rowY(key Key) (float32, bool) {
	r, ok := t.list.live[key]
	if !ok || t.list.gone[key] {
		return 0, false
	}
	return r.y.Value(), true
}

// pillY is where the pill is in the list's content, while it shows.
func (t *Tree) pillY() (float32, bool) {
	y, ok := t.rowY(t.cursor)
	if !ok || t.shown.Value() < 0.05 {
		return 0, false
	}
	return y + t.lag.Value(), true
}

func (t *Tree) activate(key Key, u *gunim.UI) {
	if t.OnActivate != nil {
		act(u, t, gunim.CuePress, t.OnActivate, key)
	}
}

func (t *Tree) item(key Key) TreeItem {
	if t.Item == nil {
		return TreeItem{}
	}
	return t.Item(key)
}

// Children implements [gunim.Composite].
func (t *Tree) Children() []gunim.Node { return []gunim.Node{t.list} }

// Focusable implements [gunim.Focusable].
func (t *Tree) Focusable() bool { return true }

// Handle implements [gunim.Handler]: the keys that move the cursor, open and shut branches and activate rows.
func (t *Tree) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.FocusGained:
		t.focused = true
		if _, ok := t.index[t.cursor]; !ok && e.Keyed && len(t.keys) > 0 {
			t.move(max(t.at, 0), u)
		}
		u.Invalidate()
		return true
	case input.FocusLost:
		t.focused = false
		u.Invalidate()
		return true
	case input.FocusRing:
		t.cue.follow(e)
		u.Invalidate()
		return true
	case input.KeyPress:
		if e.Mods.Has(input.ModControl) || e.Mods.Has(input.ModAlt) || e.Mods.Has(input.ModSuper) {
			return false
		}
		return t.key(e.Key, u)
	}
	return false
}

func (t *Tree) key(k input.Key, u *gunim.UI) bool {
	i, on := t.index[t.cursor]
	if !on {
		i = min(t.at, len(t.keys)-1)
	}
	page := max(1, int(t.pageRows(u)))
	switch k {
	case input.KeyUp:
		if !on && i >= 0 {
			t.move(i, u)
		} else {
			t.move(i-1, u)
		}
	case input.KeyDown:
		t.move(i+1, u)
	case input.KeyPageUp:
		t.move(i-page, u)
	case input.KeyPageDown:
		t.move(i+page, u)
	case input.KeyHome:
		t.move(0, u)
	case input.KeyEnd:
		t.move(len(t.keys)-1, u)
	case input.KeyRight:
		if !on {
			return false
		}
		switch it := t.item(t.cursor); {
		case it.Branch && !it.Open:
			t.activate(t.cursor, u)
		case it.Branch:
			t.move(i+1, u)
		}
	case input.KeyLeft:
		if !on {
			return false
		}
		it := t.item(t.cursor)
		if it.Branch && it.Open {
			t.activate(t.cursor, u)
			break
		}
		for j := i - 1; j >= 0; j-- {
			if t.item(t.keys[j]).Depth < it.Depth {
				t.move(j, u)
				break
			}
		}
	case input.KeyEnter, input.KeyKPEnter, input.KeySpace:
		if !on {
			return false
		}
		t.activate(t.cursor, u)
	default:
		return false
	}
	return true
}

// pageRows is how many rows the tree shows at once.
func (t *Tree) pageRows(u *gunim.UI) float32 {
	h := TreeRowHeight.Get(u.Theme())
	if b, ok := u.Bounds(t.list); ok && h > 0 {
		return float32(math.Floor(float64(b.Size().H / h)))
	}
	return 10
}

// Layout implements [gunim.Node]. The tree fills the space it is given.
func (t *Tree) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	own := c.Max
	t.rowH = TreeRowHeight.Get(f.Theme)
	t.list.Estimate = t.rowH
	kids.At(0).Layout(gunim.Tight(own))
	kids.At(0).Place(geom.Point{})
	_, built := t.rowY(t.cursor)
	to := float32(0)
	if built {
		to = 1
	}
	if t.shown.Target() != to {
		if to == 1 && t.shown.Value() < 0.05 {
			t.lag.Jump(0)
		}
		t.shown.Animate(to, Quick.Get(f.Theme))
	}
	return own
}

// Paint implements [gunim.Node]: the pill, then the rows over it.
func (t *Tree) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	th := f.Theme
	if y, ok := t.rowY(t.cursor); ok {
		if v := min(max(t.shown.Value(), 0), 1); v > 0.01 {
			func() {
				defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: 1, Clip: true})()
				top := y + t.lag.Value() - t.list.Offset()
				pill := geom.Rc(4, top+1, box.W-8, t.rowH-2)
				fill := TreeSelected.Get(th)
				if !t.focused {
					fill.A = uint8(float32(fill.A) * 0.7)
				}
				p.RRect(pill, 6, paint.Solid(scaleAlpha(fill, v)))
				// An accent edge that grows from the middle of the pill's left side
				edge := (t.rowH - 10) * v
				p.RRect(geom.Rc(4, top+t.rowH/2-edge/2, 3, edge), 1.5, paint.Solid(scaleAlpha(Accent.Get(th), v)))
			}()
		}
	}
	kids.At(0).Paint(p)
	if t.cue.whole {
		GroupRing(p, geom.Rect{Max: box.Point()}, 0, 1, th)
	}
}

// treeRow is one row of a tree, drawn from the tree's Item.
type treeRow struct {
	anim.Group
	t   *Tree
	key Key
	// hot follows the pointer over the row, and turn the chevron, 1 open.
	hot, turn *anim.Float
	laid      bool
	click     Clicker
	box       geom.Size
	name      laidText
	detail    shapedText
	// cut says the text was cut short at the last paint.
	cut bool
}

func newTreeRow(t *Tree, k Key) *treeRow {
	r := &treeRow{t: t, key: k, hot: anim.NewFloat(0), turn: anim.NewFloat(0)}
	r.Add(r.hot, r.turn)
	return r
}

// Layout implements [gunim.Node]. The chevron turns toward the branch being open.
func (r *treeRow) Layout(c gunim.Constraints, f gunim.Frame, _ gunim.Children) geom.Size {
	open := float32(0)
	if r.t.item(r.key).Open {
		open = 1
	}
	if !r.laid {
		r.turn.Jump(open)
		r.laid = true
	}
	r.turn.Animate(open, Settle.Get(f.Theme))
	r.box = c.Constrain(geom.Sz(c.Max.W, TreeRowHeight.Get(f.Theme)))
	return r.box
}

// Paint implements [gunim.Node].
func (r *treeRow) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	it := r.t.item(r.key)
	if v := min(max(r.hot.Value(), 0), 1); v > 0.01 {
		p.RRect(geom.Rc(4, 1, box.W-8, box.H-2), 6, paint.Solid(scaleAlpha(TreeHover.Get(th), v)))
	}
	indent := TreeIndent.Get(th)
	const pad, chev, side = 10, 14, 16
	mid := box.H / 2
	guide := TreeGuide.Get(th)
	for d := range it.Depth {
		x := pad + float32(d)*indent + chev/2
		p.RRect(geom.Rc(x, 0, 1, box.H), 0, paint.Solid(guide))
	}
	x := pad + float32(it.Depth)*indent
	ink := Ink.Get(th)
	if it.Faint {
		ink = Placeholder.Get(th)
	}
	if it.Branch {
		c := Placeholder.Get(th)
		centre := geom.Pt(x+chev/2, mid)
		func() {
			turn := 1 - min(max(r.turn.Value(), 0), 1)
			defer p.Push(paint.Rotate(-math.Pi/2*turn, centre))()
			PaintIcon(p, th, icon.ChevronDown, geom.Rc(centre.X-chev/2, mid-chev/2, chev, chev), c)
		}()
	}
	x += chev + 4
	ic, icInk := it.Icon, ink
	if it.IconInk != nil {
		icInk = it.IconInk.Get(th)
	}
	if ic == nil && it.Branch {
		ic, icInk = icon.Folder, TreeBranch.Get(th)
		if r.turn.Value() > 0.5 {
			ic = icon.FolderOpen
		}
	}
	if ic != nil {
		PaintIcon(p, th, ic, geom.Rc(x, mid-side/2, side, side), icInk)
		x += side + 6
	}
	size := TextSize.Get(th) * 0.93
	right := box.W - 12
	if it.Detail != "" {
		// The detail takes at most half the room the text has, cut short past that.
		face := faceIn(Font, th)
		run := r.detail.shape(face, it.Detail, size*0.88)
		if room := (right - x) / 2; run.Advance > room {
			run = cutRunWith(run, face.Shape("…", run.Size), room)
		}
		right -= run.Advance
		quiet := Placeholder.Get(th)
		run.Paint(p, geom.Pt(right, (box.H-run.Height())/2), quiet)
		right -= 10
	}
	para, fits := cellText(&r.name, faceIn(it.Face, th), it.Text, size, right-x)
	r.cut = para.Truncated || !fits
	if fits {
		para.Paint(p, geom.Pt(x, (box.H-para.Size.H)/2), ink)
	}
}

// Handle implements [gunim.Handler]: the pointer lights the row, and a click puts the cursor on it and activates it.
func (r *treeRow) Handle(e input.Event, u *gunim.UI) bool {
	t := r.t
	switch e := e.(type) {
	case input.PointerEnter:
		r.hot.Animate(1, Quick.Get(u.Theme()))
		t.tip.Handle(e, u, r, r.tipText())
		return false
	case input.PointerLeave:
		r.hot.Animate(0, Settle.Get(u.Theme()))
		t.tip.Handle(e, u, r, "")
		return false
	case input.PointerMove:
		t.tip.Handle(e, u, r, r.tipText())
		return false
	case input.Scroll:
		t.tip.Handle(e, u, r, "")
		return false
	case input.PointerDown:
		t.tip.Handle(e, u, r, "")
		if e.Button != input.ButtonPrimary {
			return false
		}
		r.click.Press(e, 0)
		return true
	case input.PointerUp:
		// A click lands on the row it lets go on, while the tree still holds it, so a finger that lands on a row to
		// scroll moves nothing.
		i, held := t.index[r.key]
		if r.click.Release(e, over(e.Pos, r.box)) && held {
			t.move(i, u)
			t.activate(r.key, u)
		}
		return true
	}
	return false
}

// tipText is the row's whole text when it was cut short.
func (r *treeRow) tipText() string {
	if !r.cut {
		return ""
	}
	return r.t.item(r.key).Text
}

// Access implements [gunim.Accessible]: the row's text, its level, and whether it opens, is open and is the cursor's.
func (r *treeRow) Access() access.Info {
	it := r.t.item(r.key)
	info := access.Info{Role: access.RoleLabel, Name: it.Text, Description: fmt.Sprintf("level %d", it.Depth+1),
		Actions: []string{access.ActionPress}}
	if it.Branch {
		info.State |= access.StateExpandable
		if it.Open {
			info.State |= access.StateExpanded
		}
	}
	if r.key == r.t.cursor {
		info.State |= access.StateSelected
	}
	return info
}

// AccessAct implements [gunim.AccessActor]: a press activates the row.
func (r *treeRow) AccessAct(req access.Request, u *gunim.UI) bool {
	if req.Action != access.ActionPress {
		return false
	}
	if i, ok := r.t.index[r.key]; ok {
		r.t.move(i, u)
	}
	r.t.activate(r.key, u)
	return true
}

// Access implements [gunim.Accessible].
func (t *Tree) Access() access.Info { return access.Info{Role: access.RoleList} }
