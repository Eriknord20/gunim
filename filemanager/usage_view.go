package filemanager

import (
	"fmt"
	"image/color"
	"strings"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/text"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// Disk usage: a page over the browser that shows what takes the room in
// a folder, as boxes sized by it, as a tree of folders, and by type. It
// tells the files a cloud provider keeps online apart from those on this
// device. Escape closes it.

// The page's colours.
var (
	// UsageOnline colours what a cloud provider keeps online only,
	// UsageLocal what it has on this device and may free again, and
	// UsagePlain what is on this device in no cloud. What it always
	// keeps here is CloudPinnedInk.
	UsageOnline = theme.Color("files.usage.online", color.NRGBA{R: 0x6c, G: 0xb6, B: 0xff, A: 0xff})
	UsageLocal  = theme.Color("files.usage.local", color.NRGBA{R: 0xff, G: 0xb4, B: 0x54, A: 0xff})
	UsagePlain  = theme.Color("files.usage.plain", color.NRGBA{R: 0x7d, G: 0x86, B: 0x99, A: 0xff})
	// UsageTrack is the track of a bar, and UsageBox the fill of a
	// folder's box, under its items.
	UsageTrack = theme.Color("files.usage.track", color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x12})
	UsageBox   = theme.Color("files.usage.box", color.NRGBA{R: 0x26, G: 0x2b, B: 0x36, A: 0xff})
	// UsageEdge is the line round a box, between it and the next.
	UsageEdge = theme.Color("files.usage.edge", color.NRGBA{R: 0x12, G: 0x14, B: 0x19, A: 0xff})
	// UsageHot lights the box under the pointer.
	UsageHot = theme.Color("files.usage.hot", color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x40})
	// UsageInk is the text on a dark box, in either theme.
	UsageInk = theme.Foreground("files.usage.ink", color.NRGBA{R: 0xf4, G: 0xf6, B: 0xfa, A: 0xff})
)

// usageLight are the page's colours in the light theme.
var usageLight = []theme.Entry{
	theme.Set(UsageOnline, color.NRGBA{R: 0x3d, G: 0x8f, B: 0xe0, A: 0xff}),
	theme.Set(UsageLocal, color.NRGBA{R: 0xe8, G: 0x90, B: 0x1c, A: 0xff}),
	theme.Set(UsagePlain, color.NRGBA{R: 0x8a, G: 0x93, B: 0xa6, A: 0xff}),
	theme.Set(UsageTrack, color.NRGBA{A: 0x10}),
	theme.Set(UsageBox, color.NRGBA{R: 0xe4, G: 0xe8, B: 0xef, A: 0xff}),
	theme.Set(UsageEdge, color.NRGBA{R: 0xfa, G: 0xfb, B: 0xfd, A: 0xff}),
	theme.Set(UsageHot, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0x55}),
}

func registerUsage(w *gunim.Window) {
	gunim.RegisterView(w, "usage", func(Usage) *usagePage { return newUsagePage() }, (*usagePage).set)
}

// The page's room: its margin, the height of its head, its summary and
// its foot, and the gap between its parts.
const (
	usageMargin = 16
	usageHeadH  = 52
	usageSumH   = 100
	usageFootH  = 30
	usageGap    = 10
)

// usagePage is the Disk usage page.
type usagePage struct {
	anim.Group
	state   Usage
	in      *anim.Float
	closing bool

	back    *widget.IconButton
	title   *widget.Label
	crumbs  *usageCrumbs
	measure *widget.Segmented
	colour  *widget.Segmented
	again   *widget.Button
	sum     *usageSummary
	list    *usageList
	types   *usageTypes
	tmap    *usageMap
	foot    *usageFoot
	top     *widget.Split
	body    *widget.Split

	// index finds an item by its key, and kids the items of each folder.
	index map[string]int
	kids  [][]int
	// sel is the key of the item selected, hot the one under the pointer,
	// and ext the type picked in the list of types, with picked set.
	sel, hot string
	ext      string
	picked   bool
	// open are the folders open in the tree, by key.
	open map[string]bool
	// cloud says the ways to look at what is in the cloud show.
	cloud bool
	// texts are the words drawn, shaped.
	texts texts
}

func newUsagePage() *usagePage {
	pg := &usagePage{in: anim.NewFloat(0), index: map[string]int{}, open: map[string]bool{}}
	pg.Add(pg.in)
	pg.back = widget.NewIconButton(icon.ArrowLeft, "Back to the files (Escape)")
	pg.back.OnClick = func(u *gunim.UI) gunim.Intent { pg.close(u); return nil }
	pg.title = widget.NewLabel("Disk usage")
	pg.title.Face, pg.title.Size, pg.title.MaxLines = widget.BoldFont, TitleText, 1
	pg.crumbs = &usageCrumbs{pg: pg}
	pg.measure = widget.NewSegmented("All files", "On this device", "Online only")
	pg.measure.Fit = true
	pg.measure.Tooltip = "What the boxes and the tree are sized by"
	pg.measure.OnChange = func(i int, _ *gunim.UI) gunim.Intent {
		return UsageSet{Seq: pg.state.Seq, Measure: Measure(i), Colour: pg.state.Colour}
	}
	pg.colour = widget.NewSegmented("By type", "By cloud")
	pg.colour.Fit = true
	pg.colour.Tooltip = "What colours the boxes"
	pg.colour.OnChange = func(i int, _ *gunim.UI) gunim.Intent {
		return UsageSet{Seq: pg.state.Seq, Measure: pg.state.Measure, Colour: ColourBy(i)}
	}
	pg.again = widget.NewButton("Read again")
	pg.again.OnClick = func(*gunim.UI) gunim.Intent {
		act := UsageRead
		if pg.state.Reading {
			act = UsageStop
		}
		return UsageAct{Seq: pg.state.Seq, Act: act}
	}
	pg.sum = &usageSummary{pg: pg}
	pg.list = newUsageList(pg)
	pg.types = newUsageTypes(pg)
	pg.tmap = newUsageMap(pg)
	pg.foot = &usageFoot{pg: pg}
	pg.top = widget.NewSplit(pg.list, pg.types)
	pg.top.SetShare(0.66, nil)
	pg.body = widget.NewSplit(pg.top, pg.tmap.menu)
	pg.body.Axis = widget.Vertical
	pg.body.SetShare(0.42, nil)
	return pg
}

// set takes the page's state.
func (pg *usagePage) set(s Usage, u *gunim.UI) {
	was := pg.state
	pg.state = s
	first := was.Seq == 0
	if s.Seq != was.Seq {
		// Another folder, or the same read again: what was picked goes.
		pg.sel, pg.hot, pg.ext, pg.picked = "", "", "", false
		clear(pg.open)
	}
	clear(pg.index)
	pg.kids = pg.kids[:0]
	for i, it := range s.Items {
		pg.index[it.Key] = i
		pg.kids = append(pg.kids, nil)
		if it.Parent >= 0 {
			pg.kids[it.Parent] = append(pg.kids[it.Parent], i)
		}
	}
	pg.measure.SetSelected(int(s.Measure), u)
	pg.colour.SetSelected(int(s.Colour), u)
	pg.again.Label = "Read again"
	pg.again.Icon = icon.RefreshCw
	if s.Reading {
		pg.again.Label, pg.again.Icon = "Stop", icon.Square
	}
	if first {
		for _, n := range []gunim.Node{pg.back, pg.title, pg.crumbs, pg.again, pg.sum, pg.body, pg.foot} {
			u.Insert(pg, n)
		}
	}
	// The ways to look at what is in the cloud, while some is.
	if s.Cloud != pg.cloud {
		pg.cloud = s.Cloud
		for _, n := range []gunim.Node{pg.measure, pg.colour} {
			if s.Cloud {
				u.Insert(pg, n)
			} else {
				u.Remove(n)
			}
		}
	}
	pg.list.update(was, u)
	pg.types.update(u)
	pg.tmap.update(was, u)
	if first {
		u.Focus(pg.list)
	}
	u.Invalidate()
}

// item is the item of key, and false where none was sent.
func (pg *usagePage) item(key string) (UsageItem, bool) {
	i, ok := pg.index[key]
	if !ok {
		return UsageItem{}, false
	}
	return pg.state.Items[i], true
}

// sizeOf is what the page sizes it by.
func (pg *usagePage) sizeOf(it UsageItem) int64 { return measureOf(it, pg.state.Measure) }

// pick selects the item of key, in the tree and in the boxes, opening
// the folders on its way in the tree.
func (pg *usagePage) pick(key string, u *gunim.UI) {
	pg.sel = key
	pg.list.reveal(key, u)
	u.Invalidate()
}

// setHot lights the item of key, under the pointer in the tree or the
// boxes, or with "" none.
func (pg *usagePage) setHot(key string, u *gunim.UI) {
	if key != pg.hot {
		pg.hot = key
		u.Invalidate()
	}
}

// markInk is the colour of the mark of it, in the tree and the boxes: by
// its type or by its cloud, as the page colours them.
func (pg *usagePage) markInk(th *theme.Live, it UsageItem) color.NRGBA {
	if pg.state.Colour == ColourCloud {
		return cloudInk(it).Get(th)
	}
	if it.Dir && it.Rest == 0 {
		return widget.TreeBranch.Get(th)
	}
	return TintToken(it.Tint).Get(th)
}

// cloudInk is the colour of how a cloud provider keeps most of it.
func cloudInk(it UsageItem) theme.Token[color.NRGBA] {
	plain := it.Size - it.Online - it.Local - it.Pinned
	best, ink := plain, UsagePlain
	for _, c := range []struct {
		n   int64
		ink theme.Token[color.NRGBA]
	}{{it.Local, UsageLocal}, {it.Pinned, CloudPinnedInk}, {it.Online, UsageOnline}} {
		if c.n > best {
			best, ink = c.n, c.ink
		}
	}
	return ink
}

// focusOn shows the folder of key in the boxes.
func (pg *usagePage) focusOn(key string, u *gunim.UI) {
	if it, ok := pg.item(key); ok && (!it.Dir || it.Rest > 0) {
		return
	}
	if key == pg.state.Focus {
		return
	}
	u.Send(pg, UsageFocus{Seq: pg.state.Seq, Key: key})
}

// up shows the folder above the one in focus.
func (pg *usagePage) up(u *gunim.UI) {
	f := pg.state.Focus
	if f == "" {
		return
	}
	parent := ""
	if i := strings.LastIndexByte(f, '/'); i >= 0 {
		parent = f[:i]
	}
	pg.sel = f
	u.Send(pg, UsageFocus{Seq: pg.state.Seq, Key: parent})
}

// act asks the application to do act to the item of key.
func (pg *usagePage) act(key, act string, u *gunim.UI) {
	u.Send(pg, UsageAct{Seq: pg.state.Seq, Key: key, Act: act})
}

// pathOf is the whole path of the item of key, as the file system shows
// it.
func (pg *usagePage) pathOf(key string) string {
	key, _, _ = strings.Cut(key, "\x00")
	ps := pg.state.Paths
	p := pg.state.Root
	if key != "" {
		p = ps.Join(append([]string{p}, strings.Split(key, "/")...)...)
	}
	return ps.Show(p)
}

// menuItems are the items of the context menu of the item of key, and the
// acts they do.
func (pg *usagePage) menuItems(key string) (items []widget.MenuItem, acts []string) {
	it, ok := pg.item(key)
	if !ok {
		return nil, nil
	}
	add := func(label, act string) {
		items = append(items, widget.MenuItem{Label: label})
		acts = append(acts, act)
	}
	if it.Dir && it.Rest == 0 && key != pg.state.Focus {
		add("Go into", "into")
	}
	if pg.state.Focus != "" {
		add("Go up", "up")
	}
	if it.Rest == 0 {
		add("Show in Files", UsageShow)
		if !it.Dir {
			add("Open", UsageOpen)
		}
		add("Copy path", "copy")
	} else {
		add("Show the folder in Files", UsageShow)
	}
	return items, acts
}

// menuPicked does what item i of the context menu of key asks.
func (pg *usagePage) menuPicked(key string, acts []string, i int, u *gunim.UI) {
	if i < 0 || i >= len(acts) {
		return
	}
	switch acts[i] {
	case "into":
		pg.focusOn(key, u)
	case "up":
		pg.up(u)
	case "copy":
		u.SetClipboard(pg.pathOf(key))
	default:
		pg.act(key, acts[i], u)
	}
}

// close takes the page away, and tells the application.
func (pg *usagePage) close(u *gunim.UI) {
	if pg.closing {
		return
	}
	pg.closing = true
	u.Remove(pg)
	u.Send(pg, UsageClosed{Seq: pg.state.Seq})
}

// Focusable implements [gunim.Focusable].
func (pg *usagePage) Focusable() bool { return !pg.closing }

// Transition implements [gunim.Transitioner]: the page rises into the
// browser and sinks away.
func (pg *usagePage) Transition(p gunim.Presence, f gunim.Frame) bool {
	switch p {
	case gunim.Entering:
		pg.in.Animate(1, Page.Get(f.Theme))
	case gunim.Exiting:
		pg.in.Animate(0, widget.Settle.Get(f.Theme))
	case gunim.Present:
	}
	return !pg.in.Active()
}

// Handle implements [gunim.Handler]: Escape closes the page, and
// Backspace or Alt+Up goes up. It keeps every other key from the browser
// behind it, whose keys act on the files.
func (pg *usagePage) Handle(e input.Event, u *gunim.UI) bool {
	if pg.closing {
		return false
	}
	switch e := e.(type) {
	case input.KeyPress:
		ctrl, alt, shift := e.Mods.Has(input.ModControl), e.Mods.Has(input.ModAlt), e.Mods.Has(input.ModShift)
		switch {
		case e.Key == input.KeyEscape && e.Mods == 0:
			pg.close(u)
		case e.Key == input.KeyBackspace && e.Mods == 0, alt && e.Key == input.KeyUp:
			pg.up(u)
		case e.Key == input.KeyF5 && e.Mods == 0:
			u.Send(pg, UsageAct{Seq: pg.state.Seq, Act: UsageRead})
		case ctrl && (alt || shift), ctrl && e.Key == input.KeyW, e.Key == input.KeyF10:
			// The program's round the page, and closing the window.
			return false
		}
		return true
	case input.PointerDown, input.PointerUp, input.PointerMove, input.Scroll:
		// Nothing under the page hears the pointer.
		return true
	}
	return false
}

// Layout implements [gunim.Node]: the head along the top, the summary
// under it, then the tree and the types beside each other over the boxes,
// and the foot along the bottom.
func (pg *usagePage) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	box := c.Max
	w := max(0, box.W-2*usageMargin)
	right := box.W - usageMargin
	for k := range kids.All {
		switch k.Node() {
		case gunim.Node(pg.back):
			s := k.Layout(gunim.Constraints{Max: geom.Sz(40, 40)})
			k.Place(geom.Pt(usageMargin-6, (usageHeadH-s.H)/2))
		case gunim.Node(pg.again):
			s := k.Layout(gunim.Constraints{Max: geom.Sz(160, 40)})
			right -= s.W
			k.Place(geom.Pt(right, (usageHeadH-s.H)/2))
		}
	}
	for k := range kids.All {
		switch k.Node() {
		case gunim.Node(pg.colour), gunim.Node(pg.measure):
			s := k.Layout(gunim.Constraints{Max: geom.Sz(max(0, w/3), 36)})
			right -= s.W + usageGap
			k.Place(geom.Pt(right, (usageHeadH-s.H)/2))
		}
	}
	left := float32(usageMargin + 38)
	for k := range kids.All {
		if k.Node() == gunim.Node(pg.title) {
			s := k.Layout(gunim.Constraints{Max: geom.Sz(200, 0)})
			k.Place(geom.Pt(left, (usageHeadH-s.H)/2))
			left += s.W + 14
		}
	}
	bodyTop := float32(usageHeadH + usageSumH + usageGap)
	bodyH := max(0, box.H-bodyTop-usageFootH)
	for k := range kids.All {
		switch k.Node() {
		case gunim.Node(pg.crumbs):
			k.Layout(gunim.Tight(geom.Sz(max(0, right-usageGap-left), usageHeadH)))
			k.Place(geom.Pt(left, 0))
		case gunim.Node(pg.sum):
			k.Layout(gunim.Tight(geom.Sz(w, usageSumH)))
			k.Place(geom.Pt(usageMargin, usageHeadH))
		case gunim.Node(pg.body):
			k.Layout(gunim.Tight(geom.Sz(w, bodyH)))
			k.Place(geom.Pt(usageMargin, bodyTop))
		case gunim.Node(pg.foot):
			k.Layout(gunim.Tight(geom.Sz(w, usageFootH)))
			k.Place(geom.Pt(usageMargin, box.H-usageFootH))
		}
	}
	return box
}

// Paint implements [gunim.Node]: the page over the browser, rising into
// it as it opens.
func (pg *usagePage) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	in := min(max(pg.in.Value(), 0), 1)
	r := geom.Rect{Max: box.Point()}
	defer p.Layer(paint.LayerOpts{Bounds: r, Opacity: in})()
	defer p.Push(paint.Translate(geom.Pt(0, 14*(1-in))))()
	p.RRect(r, 0, paint.Solid(PaneFill.Get(f.Theme)))
	for k := range kids.All {
		k.Paint(p)
	}
}

// texts shapes the words the page draws, once for each face, size and
// string, and forgets them when they grow many.
type texts struct {
	m map[textKey]text.Run
}

type textKey struct {
	face *text.Face
	s    string
	size float32
}

// run is s shaped in face at size.
func (t *texts) run(face *text.Face, s string, size float32) text.Run {
	k := textKey{face, s, size}
	if r, ok := t.m[k]; ok {
		return r
	}
	if t.m == nil || len(t.m) > 6000 {
		t.m = map[textKey]text.Run{}
	}
	r := face.Shape(s, size)
	t.m[k] = r
	return r
}

// draw paints s in face at size with its top left at at, fading out past
// room; it returns how wide it is drawn.
func (t *texts) draw(p *paint.Painter, th *theme.Live, face *text.Face, s string, size float32, at geom.Point,
	ink color.NRGBA, room float32) float32 {
	if room <= 0 || s == "" {
		return 0
	}
	r := t.run(face, s, size)
	if r.Advance <= room {
		r.Paint(p, at, ink)
		return r.Advance
	}
	r.PaintFaded(p, at, ink, room, widget.TextFade.Get(th))
	return room
}

// usageCrumbs are the folders from the one read down to the one in focus,
// each of which a click shows in the boxes.
type usageCrumbs struct {
	pg *usagePage
	// spots are where each crumb was drawn, and hot the one under the
	// pointer, or -1.
	spots []geom.Rect
	hot   int
	box   geom.Size
}

// Layout implements [gunim.Node].
func (c *usageCrumbs) Layout(k gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	c.box = k.Max
	return k.Max
}

// Paint implements [gunim.Node]: the crumbs from the right, those that
// do not fit cut from the left.
func (c *usageCrumbs) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	cs := c.pg.state.Crumbs
	c.spots = c.spots[:0]
	if len(cs) == 0 {
		return
	}
	face := widget.Font.Get(th)
	bold := widget.BoldFont.Get(th)
	size := widget.TextSize.Get(th)
	sep := c.pg.texts.run(face, "›", size)
	widths := make([]float32, len(cs))
	total := float32(0)
	first := 0
	for i := len(cs) - 1; i >= 0; i-- {
		fc := face
		if i == len(cs)-1 {
			fc = bold
		}
		widths[i] = c.pg.texts.run(fc, cs[i].Name, size).Advance
		need := widths[i]
		if i < len(cs)-1 {
			need += sep.Advance + 12
		}
		if total+need > box.W && i < len(cs)-1 {
			first = i + 1
			break
		}
		total += need
	}
	x := float32(0)
	if where := c.pg.state.Where; where != "" && first == 0 {
		x += c.pg.texts.draw(p, th, face, where+":", size, geom.Pt(x, (box.H-sep.Height())/2), Faint.Get(th), box.W) + 6
	}
	c.spots = make([]geom.Rect, len(cs))
	for i := first; i < len(cs); i++ {
		fc, ink := face, Faint.Get(th)
		if i == len(cs)-1 {
			fc, ink = bold, widget.Ink.Get(th)
		}
		run := c.pg.texts.run(fc, cs[i].Name, size)
		y := (box.H - run.Height()) / 2
		w := c.pg.texts.draw(p, th, fc, cs[i].Name, size, geom.Pt(x, y), ink, box.W-x)
		c.spots[i] = geom.Rc(x-4, y-4, w+8, run.Height()+8)
		if i == c.hot && i < len(cs)-1 {
			p.RRect(geom.Rc(x, y+run.Ascent+2, w, 1), 0, paint.Solid(ink))
		}
		x += w
		if i < len(cs)-1 {
			sep.Paint(p, geom.Pt(x+6, (box.H-sep.Height())/2), Caption.Get(th))
			x += sep.Advance + 12
		}
	}
}

// at is the crumb at pt, or -1.
func (c *usageCrumbs) at(pt geom.Point) int {
	for i, r := range c.spots {
		if r.Contains(pt) {
			return i
		}
	}
	return -1
}

// Handle implements [gunim.Handler]: a click on a crumb shows its folder.
func (c *usageCrumbs) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerMove:
		if h := c.at(e.Pos); h != c.hot {
			c.hot = h
			u.Invalidate()
		}
	case input.PointerLeave:
		c.hot = -1
		u.Invalidate()
	case input.PointerDown:
		if e.Button != input.ButtonPrimary {
			return false
		}
		if i := c.at(e.Pos); i >= 0 && i < len(c.pg.state.Crumbs) {
			c.pg.focusOn(c.pg.state.Crumbs[i].Path, u)
			return true
		}
	}
	return false
}

// usageSummary sums the folder up: its size, what of it is on this device
// and what online, and the room on its drive.
type usageSummary struct {
	pg *usagePage
}

// Layout implements [gunim.Node].
func (s *usageSummary) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	return c.Max
}

// usagePart is one part of a bar: its share and colour, and its words.
type usagePart struct {
	bytes int64
	ink   theme.Token[color.NRGBA]
	label string
}

// Paint implements [gunim.Node]: the total on the left, then a bar of
// what is on this device and what online, and one of the drive.
func (s *usageSummary) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	st := s.pg.state
	t := &s.pg.texts
	tot := st.Total
	face, bold := widget.Font.Get(th), widget.BoldFont.Get(th)
	small := SmallText.Get(th)
	card := geom.Rect{Max: box.Point()}
	p.RRect(card, widget.CardRadius.Get(th), paint.Solid(widget.CardFill.Get(th)))
	const pad = 16
	// The total, how many files, and how the reading went.
	big := t.run(bold, humanBytes(tot.Size), 26)
	y := float32(12)
	big.Paint(p, geom.Pt(pad, y), widget.Ink.Get(th))
	y += big.Height() + 2
	counts := plural(tot.Files, "file") + " in " + plural(tot.Folders, "folder")
	t.draw(p, th, face, counts, small, geom.Pt(pad, y), Faint.Get(th), 230)
	y += t.run(face, counts, small).Height() + 3
	var how string
	switch {
	case st.Reading:
		how = "Reading… " + st.Took
	case st.Stopped != "":
		how = st.Stopped + ", after " + st.Took
	default:
		how = "Read in " + st.Took
	}
	if st.Errs > 0 {
		how += " · " + plural(st.Errs, "folder") + " could not be read"
	}
	ink := Caption.Get(th)
	if st.Errs > 0 || st.Stopped != "" {
		ink = ErrorInk.Get(th)
	}
	t.draw(p, th, face, how, small, geom.Pt(pad, y), ink, 230)
	left := float32(pad + 250)
	room := box.W - left - pad
	if room < 120 {
		return
	}
	// The drive's bar beside the other, where there is room for both.
	drive := st.Space > 0 && room >= 560
	barW := room
	if drive {
		barW = room*0.58 - pad
	}
	// What is on this device, and what online.
	parts := []usagePart{
		{tot.Size - tot.Online - tot.Local - tot.Pinned, UsagePlain, "On this device"},
	}
	var title string
	if st.Cloud {
		parts[0].label = "No cloud"
		parts = append(parts,
			usagePart{tot.Local, UsageLocal, "Can be freed"},
			usagePart{tot.Pinned, CloudPinnedInk, "Always kept"},
			usagePart{tot.Online, UsageOnline, "Online only"})
		title = humanBytes(tot.Size-tot.Online) + " on this device, " + humanBytes(tot.Online) + " online only"
	} else {
		// No cloud: what types of file take the room.
		title = "By type"
		parts = parts[:0]
		rest := tot.Size
		for _, ty := range st.Types {
			if len(parts) == 6 || ty.Others {
				break
			}
			label := ty.Label
			if ty.Ext != "" {
				label = "." + ty.Ext
			}
			parts = append(parts, usagePart{ty.Size, TintToken(ty.Tint), label})
			rest -= ty.Size
		}
		parts = append(parts, usagePart{rest, UsagePlain, "Other"})
	}
	s.bar(p, th, geom.Rc(left, 14, barW, box.H-28), title, parts, tot.Size)
	if !drive {
		return
	}
	// The drive: this folder, the rest used, and what is free.
	used := int64(st.Space - st.Free)
	here := tot.Size - tot.Online
	dparts := []usagePart{
		{min(here, used), UsageLocal, "This folder"},
		{max(0, used-here), UsagePlain, "The rest"},
		{int64(st.Free), UsageTrack, "Free"},
	}
	dtitle := fmt.Sprintf("Drive: %s free of %s", humanBytes(int64(st.Free)), humanBytes(int64(st.Space)))
	s.bar(p, th, geom.Rc(left+barW+2*pad, 14, room-barW-2*pad, box.H-28), dtitle, dparts, int64(st.Space))
}

// bar draws a bar of parts in r, with title over it and the parts' words
// under it.
func (s *usageSummary) bar(p *paint.Painter, th *theme.Live, r geom.Rect, title string, parts []usagePart, whole int64) {
	t := &s.pg.texts
	face, bold := widget.Font.Get(th), widget.BoldFont.Get(th)
	small := SmallText.Get(th)
	head := t.run(bold, title, small)
	t.draw(p, th, bold, title, small, r.Min, widget.Ink.Get(th), r.Size().W)
	const barH = 12
	bar := geom.Rc(r.Min.X, r.Min.Y+head.Height()+8, r.Size().W, barH)
	p.RRect(bar, barH/2, paint.Solid(UsageTrack.Get(th)))
	if whole > 0 {
		func() {
			defer p.Layer(paint.LayerOpts{Bounds: bar, Opacity: 1, Clip: true})()
			x := bar.Min.X
			for _, pt := range parts {
				if pt.bytes <= 0 {
					continue
				}
				w := bar.Size().W * float32(float64(pt.bytes)/float64(whole))
				p.RRect(geom.Rc(x, bar.Min.Y, w+barH/2, barH), barH/2, paint.Solid(pt.ink.Get(th)))
				x += w
			}
		}()
		// The track's round ends over the parts.
		p.RRectStroke(bar, barH/2, paint.Fill{}, paint.Stroke{Width: 1, Color: UsageEdge.Get(th)})
	}
	// The words, a dot of each colour before them.
	x := r.Min.X
	y := bar.Max.Y + 8
	for _, pt := range parts {
		if pt.bytes <= 0 {
			continue
		}
		words := pt.label + " " + humanBytes(pt.bytes)
		run := t.run(face, words, small)
		if x+run.Advance+16 > r.Max.X && x > r.Min.X {
			// On a line of its own, where there is one.
			x, y = r.Min.X, y+run.Height()+4
			if y+run.Height() > r.Max.Y+10 {
				break
			}
		}
		dot := run.Height() * 0.6
		p.RRect(geom.Rc(x, y+(run.Height()-dot)/2, dot, dot), dot/2, paint.Solid(pt.ink.Get(th)))
		run.Paint(p, geom.Pt(x+dot+6, y), Faint.Get(th))
		x += dot + 6 + run.Advance + 16
	}
}

// usageFoot says what is under the pointer, or what can be done.
type usageFoot struct {
	pg *usagePage
}

// Layout implements [gunim.Node].
func (ft *usageFoot) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	return c.Max
}

// Paint implements [gunim.Node].
func (ft *usageFoot) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	pg := ft.pg
	face := widget.Font.Get(th)
	small := SmallText.Get(th)
	key := pg.hot
	if key == "" {
		key = pg.sel
	}
	it, ok := pg.item(key)
	if !ok {
		words := "Click a box to find it in the tree. Double-click a folder to go into it, and Backspace to come back out."
		run := pg.texts.run(face, words, small)
		pg.texts.draw(p, th, face, words, small, geom.Pt(0, (box.H-run.Height())/2), Caption.Get(th), box.W)
		return
	}
	name := pg.pathOf(key)
	if it.Rest > 0 {
		name = it.Name + " in " + pg.pathOf(key)
	}
	facts := describeUsage(it, pg.state.Cloud)
	run := pg.texts.run(face, facts, small)
	right := pg.texts.draw(p, th, face, facts, small, geom.Pt(max(0, box.W-run.Advance), (box.H-run.Height())/2),
		Faint.Get(th), box.W)
	pg.texts.draw(p, th, widget.BoldFont.Get(th), name, small, geom.Pt(0, (box.H-run.Height())/2),
		widget.Ink.Get(th), box.W-right-24)
}

// describeUsage says the sizes of it, and with cloud where its files are.
func describeUsage(it UsageItem, cloud bool) string {
	parts := []string{humanBytes(it.Size)}
	if cloud && it.Online > 0 {
		parts = append(parts, humanBytes(it.Size-it.Online)+" on this device", humanBytes(it.Online)+" online only")
	}
	if cloud && it.Local > 0 && it.Dir {
		parts = append(parts, humanBytes(it.Local)+" could be freed")
	}
	if !it.Dir && it.Rest == 0 {
		switch it.Cloud {
		case CloudOnline:
			parts = append(parts, "Online only")
		case CloudLocal:
			parts = append(parts, "In the cloud, on this device")
		case CloudPinned:
			parts = append(parts, "Always kept on this device")
		case CloudNone, CloudFolder:
		}
	} else {
		parts = append(parts, plural(it.Files, "file"))
	}
	if it.Reading {
		parts = append(parts, "still reading")
	}
	if it.Err != "" {
		parts = append(parts, "could not be read: "+it.Err)
	}
	if it.Skipped != "" {
		parts = append(parts, it.Skipped)
	}
	return strings.Join(parts, " · ")
}
