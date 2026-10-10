package filemanager

import (
	"image/color"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"sync"

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

// labelRoom is the height under a tile's picture for its name, two lines of it.
const labelRoom = 38

// registerIcons wires the icon view's patches and the viewer.
func registerIcons(w *gunim.Window) {
	gunim.RegisterPatch(w, "browser", func(b *browser, v ViewMode, u *gunim.UI) {
		b.status.views.set(v, u)
		b.title.check(CmdViewDetails, !v.Icons)
		b.title.check(CmdViewIcons, v.Icons)
		b.listing.setView(v, u)
	})
	gunim.RegisterPatch(w, "browser", func(b *browser, t Thumb, u *gunim.UI) { b.listing.thumb(t, u) })
	gunim.RegisterPatch(w, "browser", func(b *browser, ic SystemIcon, u *gunim.UI) {
		b.icons[ic.Key] = ic
		u.Invalidate()
	})
	registerViewer(w)
	registerReading(w)
}

// setView shows the folder at v.Path as icons or as details, sliding from one to the other on the page showing.
func (a *listingArea) setView(v ViewMode, u *gunim.UI) {
	a.view = v
	if a.cur != nil && a.b.shell.Paths.Same(a.path, v.Path) {
		a.cur.icons.show(v.Icons, true, u)
	}
}

// thumb hands a thumbnail to the page of its folder.
func (a *listingArea) thumb(t Thumb, u *gunim.UI) {
	if a.cur != nil && a.b.shell.Paths.Same(a.path, t.Dir) {
		a.cur.icons.thumbs[t.Name] = tileThumb{img: t.Image, size: t.Size, err: t.Err}
		a.cur.icons.trimThumbs()
		u.Invalidate()
	}
}

// trimThumbs lets the thumbnails far from the tiles in view go, once there are many: the tiles ask for them again
// as they come back, and the app has them at hand.
func (iv *iconView) trimThumbs() {
	if len(iv.thumbs) <= max(512, 8*iv.count) {
		return
	}
	keep := make(map[string]tileThumb, 4*iv.count)
	for i := max(0, iv.first-iv.count); i < iv.first+2*iv.count; i++ {
		if r, ok := iv.pg.view(i); ok {
			if t, ok := iv.thumbs[r.Name]; ok {
				keep[r.Name] = t
			}
		}
	}
	iv.thumbs = keep
}

// focusNode returns the node the keyboard works the listing through: the tiles or the grid.
func (pg *listingPage) focusNode() gunim.Node {
	if pg.icons.on {
		return pg.icons.grid
	}
	return pg.grid
}

// tileThumb is a tile's thumbnail, or why it has none.
type tileThumb struct {
	img  *paint.Image
	size int
	err  string
}

// iconView is a folder's listing as tiles: a picture's thumbnail, an icon for anything else, and the name under
// it. It slides in over the details, each tile flying from its row, and back.
type iconView struct {
	anim.Group
	pg     *listingPage
	grid   *widget.TileGrid
	menu   *widget.ContextMenu
	thumbs map[string]tileThumb
	on     bool
	// live is set while the tiles show or fly away.
	live bool
	// in runs from 0, showing the details, to 1, showing the tiles.
	in *anim.Float
	// first and count are the tiles built, and asked the thumbnails last asked for.
	first, count int
	asked        NeedThumbs
	th           *theme.Live
	box          geom.Size
	// dir is the folder the tiles show.
	dir string
}

func newIconView(pg *listingPage) *iconView {
	iv := &iconView{pg: pg, thumbs: map[string]tileThumb{}, in: anim.NewFloat(0), dir: pg.b.listing.path}
	iv.Add(iv.in)
	g := widget.NewTileGrid(tileSize(pg.b.status.views.tile))
	g.Tile = func(i int) gunim.Node { return newIconTile(iv, i) }
	g.OnView = func(first, count int, u *gunim.UI) gunim.Intent {
		iv.first, iv.count = first, count
		return pg.need(first, count)
	}
	g.OnSelect = func(sel [][2]int, cursor int, u *gunim.UI) gunim.Intent {
		return Selected{Gen: pg.gen, Runs: sel, Cursor: cursor}
	}
	g.OnType = func(text string, u *gunim.UI) gunim.Intent { return Typed{Gen: pg.gen, Text: text} }
	g.OnActivate = func(i int, u *gunim.UI) gunim.Intent {
		if r, ok := pg.view(i); ok && !r.Dir && viewable(r.Name) {
			return OpenViewer{Gen: pg.gen, Row: i}
		}
		return Activated{Gen: pg.gen, Row: i}
	}
	g.OnZoom = func(notches float32, u *gunim.UI) gunim.Intent {
		pg.b.status.views.zoomBy(notches, u)
		return nil
	}
	iv.grid = g
	g.DragTiles = pg.dragRows
	g.OnDragEnd = func(e input.DragEnd, u *gunim.UI) gunim.Intent { return pg.b.dnd.dragEnded(e) }
	iv.menu = pg.contextMenu(g, g.TileAt, func() [][2]int { sel, _ := g.Selected(); return sel })
	return iv
}

// tileSize is the size of a tile w wide: a square for the picture, and room for the name under it.
func tileSize(w float32) geom.Size { return geom.Sz(w, w+labelRoom) }

// show turns the tiles on or off: with animate, each flies between its row and its place.
func (iv *iconView) show(on, animate bool, u *gunim.UI) {
	if on == iv.on {
		return
	}
	iv.on = on
	iv.live = iv.live || on || animate
	g, tiles := iv.pg.grid, iv.grid
	hadFocus := u.Focused() == gunim.Node(g) || u.Focused() == gunim.Node(tiles)
	cursor := g.Selected()
	if on {
		tiles.SetSelected(g.SelectedRows(), cursor, u)
		at := int(g.Top())
		if cursor >= 0 && (float64(cursor) < g.Top() || float64(cursor) >= g.Top()+g.Visible()-1) {
			at = cursor
		}
		tiles.JumpToTile(at, u)
		if animate {
			tiles.Arrive(iv.rowRect, u)
			iv.in.Animate(1, Page.Get(u.Theme()))
		} else {
			iv.in.Jump(1)
		}
		if hadFocus {
			u.Focus(tiles)
		}
		u.Invalidate()
		return
	}
	runs, at := tiles.Selected()
	g.SetSelectedRows(runs, at, u)
	if at >= 0 {
		g.JumpTo(float64(at), true, u)
	}
	if animate {
		g.Arrive(u)
		tiles.Depart(iv.rowRect, u)
		iv.in.Animate(0, widget.Settle.Get(u.Theme()))
	} else {
		iv.in.Jump(0)
	}
	if hadFocus {
		u.Focus(g)
	}
	u.Invalidate()
}

// rowRect is where row i of the details shows the item's name, in the page's space.
func (iv *iconView) rowRect(i int) (geom.Rect, bool) {
	if iv.th == nil {
		return geom.Rect{}, false
	}
	g := iv.pg.grid
	head, rowH := widget.GridHeaderHeight.Get(iv.th), widget.GridRowHeight.Get(iv.th)
	y := head + float32(float64(i)-g.Top())*rowH
	if y < head-rowH || y > iv.box.H {
		return geom.Rect{}, false
	}
	return geom.Rc(4, y, min(iv.box.W-8, 280), rowH), true
}

// arrive has the tiles grow in one after another, as a folder's rows arrive.
func (iv *iconView) arrive(u *gunim.UI) {
	if iv.on {
		iv.grid.Arrive(func(int) (geom.Rect, bool) { return geom.Rect{}, false }, u)
	}
}

// selection takes the selection the application sends.
func (iv *iconView) selection(s Selection, u *gunim.UI) {
	iv.grid.SetSelected(s.Runs, s.Cursor, u)
	if s.Cursor >= 0 {
		iv.grid.ShowTile(s.Cursor, u)
	}
}

// selectAll and selectNone work the tiles as Ctrl+A and Escape do.
func (iv *iconView) selectAll(u *gunim.UI) {
	if n := iv.grid.Len(); n > 0 {
		iv.grid.SetSelected([][2]int{{0, n}}, 0, u)
		u.Send(iv.grid, Selected{Gen: iv.pg.gen, Runs: [][2]int{{0, n}}, Cursor: 0})
	}
}

func (iv *iconView) selectNone(u *gunim.UI) {
	iv.grid.SetSelected(nil, -1, u)
	u.Send(iv.grid, Selected{Gen: iv.pg.gen, Cursor: -1})
}

// Children implements [gunim.Composite].
func (iv *iconView) Children() []gunim.Node { return []gunim.Node{iv.menu} }

// Layout implements [gunim.Node].
func (iv *iconView) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	iv.th, iv.box = f.Theme, c.Max
	iv.grid.Size = tileSize(iv.pg.b.status.views.tile)
	if !iv.on && iv.grid.Departed() {
		iv.live = false
	}
	if !iv.live {
		return c.Max
	}
	kid := kids.At(0)
	kid.Layout(gunim.Tight(c.Max))
	kid.Place(geom.Point{})
	if iv.on {
		iv.askThumbs(f)
	}
	return c.Max
}

// askThumbs asks for the thumbnails the tiles built lack at the size they show at.
func (iv *iconView) askThumbs(f gunim.Frame) {
	size := thumbBucket(int(iv.grid.Size.W * max(f.Scale, 1)))
	var rows []int
	for i := iv.first; i < iv.first+iv.count; i++ {
		r, ok := iv.pg.view(i)
		if !ok || r.Dir || !viewable(r.Name) {
			continue
		}
		if t, ok := iv.thumbs[r.Name]; ok && (t.err != "" || t.size >= size) {
			continue
		}
		rows = append(rows, i)
	}
	ask := NeedThumbs{Gen: iv.pg.gen, Size: size, Rows: rows}
	if len(rows) == 0 || ask.Gen == iv.asked.Gen && ask.Size == iv.asked.Size && slices.Equal(rows, iv.asked.Rows) {
		return
	}
	iv.asked = ask
	f.Send(iv.grid, ask)
}

// Covers implements [gunim.Shaped]: the details under the view take the pointer while the tiles are away.
func (iv *iconView) Covers(geom.Point) bool { return iv.live }

// Paint implements [gunim.Node].
func (iv *iconView) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	if iv.live {
		kids.At(0).Paint(p)
	}
}

// paintDetails paints the details under the tiles, fading them as the tiles come in.
func (iv *iconView) paintDetails(p *paint.Painter, box geom.Size, details gunim.Child) {
	t := 1 - min(max(iv.in.Value(), 0), 1)
	if t <= 0.001 {
		return
	}
	if t < 0.999 {
		defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: t})()
	}
	details.Paint(p)
}

// iconTile is one tile: the picture or icon, which flies to the viewer and back, the name under it, and a mark
// when the picture cannot be read.
type iconTile struct {
	iv    *iconView
	i     int
	name  string
	hero  *widget.Hero
	pic   *tilePic
	label *widget.Label
	mark  *errorMark
	tip   *widget.Tooltip
	// aspect springs to the shape of the picture as its thumbnail arrives.
	aspect *anim.Float
	// picAt is where the picture was laid out, shown how much it is a
	// tile rather than a row, from 0 to 1, and cloud how a cloud
	// provider keeps the item, for its mark.
	picAt geom.Rect
	shown float32
	cloud CloudState
	anim.Group
}

func newIconTile(iv *iconView, i int) *iconTile {
	t := &iconTile{iv: iv, i: i, pic: newTilePic(), label: widget.NewLabel(""), mark: &errorMark{},
		aspect: anim.NewFloat(1)}
	t.Add(t.aspect)
	t.hero = widget.NewHero("", t.pic)
	t.hero.Anchor = true
	t.label.Size, t.label.MaxLines, t.label.Align = SmallText, 2, text.AlignCenter
	t.tip = widget.NewTooltip(t.mark, "")
	return t
}

// Children implements [gunim.Composite].
func (t *iconTile) Children() []gunim.Node { return []gunim.Node{t.hero, t.label, t.tip} }

// Layout implements [gunim.Node].
func (t *iconTile) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	box := c.Max
	r, ok := t.iv.pg.view(t.i)
	if ok && r.Name != t.name {
		t.name = r.Name
		t.label.Text = r.Name
		t.hero.Tag = t.iv.pg.b.shell.Paths.Join(t.iv.dir, r.Name)
		t.aspect.Jump(1)
		t.pic.reset()
	}
	th := t.iv.thumbs[t.name]
	if !ok {
		th = tileThumb{}
	}
	t.pic.set(r, ok, th.img, f)
	t.pic.system = nil
	if ic, has := t.iv.pg.b.icons[r.IconKey]; has && ok && r.IconKey != "" {
		t.pic.system = ic.Large
	}
	t.label.Color = widget.Ink
	if r.Hidden || r.Broken {
		t.label.Color = Faint
	}
	want := float32(1)
	if th.img != nil {
		w, h := th.img.Size()
		want = float32(w) / float32(max(h, 1))
	}
	t.aspect.Animate(want, widget.Quick.Get(f.Theme))
	// A tile squeezed toward a row of the details, as it flies from one, lays out as the row does.
	rowH := widget.GridRowHeight.Get(f.Theme)
	k := min(max((box.H-rowH)/max(t.iv.grid.Size.H-rowH, 1), 0), 1)
	side := max(box.W-12, 1)
	a := max(t.aspect.Value(), 0.05)
	pw, ph := side, side/a
	if a < 1 {
		pw, ph = side*a, side
	}
	rowPic := geom.Rc(2, 2, max(box.H-4, 1), max(box.H-4, 1))
	pr := lerpRect(rowPic, geom.Rc(6+(side-pw)/2, 6+(side-ph)/2, pw, ph), k)
	hero, label, tip := kids.At(0), kids.At(1), kids.At(2)
	hero.Layout(gunim.Tight(pr.Size()))
	hero.Place(pr.Min)
	t.picAt, t.shown, t.cloud = pr, k, CloudNone
	if ok {
		t.cloud = r.Cloud
	}
	t.label.MaxLines, t.label.Align = 2, text.AlignCenter
	if k < 0.5 {
		t.label.MaxLines, t.label.Align = 1, text.AlignStart
	}
	lw := lerp(max(box.W-box.H-10, 1), box.W-8, k)
	ls := label.Layout(gunim.Constraints{Min: geom.Sz(lw, 0), Max: geom.Sz(lw, labelRoom)})
	label.Place(geom.Pt(lerp(box.H+6, 4, k), lerp((box.H-ls.H)/2, box.W+1, k)))
	t.tip.Text = th.err
	t.mark.on = th.err != ""
	tip.Layout(gunim.Tight(geom.Sz(18, 18)))
	tip.Place(geom.Pt(min(pr.Max.X-12, box.W-20), max(pr.Min.Y-6, 2)))
	return box
}

// Paint implements [gunim.Node].
func (t *iconTile) Paint(p *paint.Painter, f gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
	t.paintCloud(p, f.Theme)
	kids.At(1).Paint(p)
	if t.mark.on {
		kids.At(2).Paint(p)
	}
}

// The cloud mark on a tile: the same size at every zoom, on a disc of the
// pane's colour, so it reads over a thumbnail as over an icon.
const (
	cloudDisc = 22
	cloudIcon = 14
)

// paintCloud marks the picture's bottom right corner with how a cloud
// provider keeps the item, as the details mark it before the name. It
// fades as the tile squeezes into a row, where the row's own mark shows.
func (t *iconTile) paintCloud(p *paint.Painter, th *theme.Live) {
	mark, ok := cloudMark(t.cloud)
	if !ok || t.cloud == CloudFolder || t.shown < 0.5 {
		return
	}
	art := t.pic.art(t.picAt.Size()).Add(t.picAt.Min)
	// Over the corner, a little inside it, and never past the tile.
	c := geom.Pt(min(art.Max.X-cloudDisc*0.3, t.picAt.Max.X-cloudDisc/2), min(art.Max.Y-cloudDisc*0.3, t.picAt.Max.Y-cloudDisc/2))
	opacity := (t.shown - 0.5) * 2
	disc := PaneFill.Get(th)
	disc.A = uint8(float32(disc.A) * opacity)
	p.ShadowRRect(geom.Rc(c.X-cloudDisc/2, c.Y-cloudDisc/2, cloudDisc, cloudDisc), cloudDisc/2, paint.Solid(disc),
		paint.Shadow{Offset: geom.Pt(0, 1), Blur: 3, Color: color.NRGBA{A: uint8(0x70 * opacity)}})
	ink := mark.Ink.Get(th)
	if t.cloud == CloudOnline {
		// The details' caption ink is too dim on the disc.
		ink = Faint.Get(th)
	}
	ink.A = uint8(float32(ink.A) * opacity)
	widget.PaintIcon(p, th, mark.Icon, geom.Rc(c.X-cloudIcon/2, c.Y-cloudIcon/2, cloudIcon, cloudIcon), ink)
}

// tilePic is what a tile shows of its item: the thumbnail of a picture, crossfading in over its icon, or the icon of
// anything else.
type tilePic struct {
	anim.Group
	// system is the icon Windows shows for the item, drawn in place of
	// the window's own.
	system  *paint.Image
	mix     *anim.Float
	thumb   *paint.Image
	loaded  bool
	dir     bool
	tint    Tint
	ext     string
	run     text.Run
	shaped  [2]float32
	shapeOf string
}

func newTilePic() *tilePic {
	p := &tilePic{mix: anim.NewFloat(0)}
	p.Add(p.mix)
	return p
}

// reset shows the icon at once, for a tile that now holds another item.
func (p *tilePic) reset() {
	p.thumb = nil
	p.mix.Jump(0)
}

// set takes the item and its thumbnail, if it has one yet.
func (p *tilePic) set(r Row, loaded bool, thumb *paint.Image, f gunim.Frame) {
	p.loaded, p.dir, p.tint = loaded, r.Dir, r.Tint
	p.ext = ""
	if ext := strings.TrimPrefix(filepath.Ext(r.Name), "."); !r.Dir && ext != "" && len(ext) <= 5 {
		p.ext = strings.ToUpper(ext)
	}
	if thumb != nil && thumb != p.thumb {
		p.thumb = thumb
		p.mix.Animate(1, widget.Crossfade.Get(f.Theme))
	}
}

// Layout implements [gunim.Node].
func (p *tilePic) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	return c.Max
}

// folderPic is the folder icon at its largest, for tiles to draw smaller.
var folderPic = sync.OnceValue(func() *paint.Image { return paint.NewImage(drawIcon(256)) })

// Paint implements [gunim.Node].
func (p *tilePic) Paint(pt *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	th := f.Theme
	r := geom.Rect{Max: box.Point()}
	if !p.loaded {
		pt.RRect(r.Inset(geom.Uniform(box.W*0.12)), box.W*0.1, paint.Solid(widget.GridPending.Get(th)))
		return
	}
	mix := min(max(p.mix.Value(), 0), 1)
	if mix < 0.999 {
		p.paintIcon(pt, th, box, 1-mix*mix)
	}
	if p.thumb != nil && mix > 0.001 {
		pt.ShadowRRect(r, 6, paint.Solid(color.NRGBA{A: uint8(0x30 * mix)}),
			paint.Shadow{Offset: geom.Pt(0, 2), Blur: 8, Color: color.NRGBA{A: uint8(0x60 * mix)}})
		pt.Image(p.thumb, r, paint.ImageOpts{Radius: 6, Opacity: mix})
	}
}

// art is where the picture shows within a box of its size: the thumbnail
// fills it, and the icon's page or folder sits inside it.
func (p *tilePic) art(box geom.Size) geom.Rect {
	if p.thumb != nil && p.mix.Value() > 0.5 {
		return geom.Rect{Max: box.Point()}
	}
	s := min(box.W, box.H)
	if p.system != nil {
		r := systemIconRect(p.system, box)
		return r
	}
	if p.dir {
		s *= 0.92
		return geom.Rc((box.W-s)/2, (box.H-s)/2, s, s)
	}
	return geom.Rc((box.W-s*0.72)/2, (box.H-s*0.86)/2, s*0.72, s*0.86)
}

// paintIcon draws the folder, or a page in the item's colour with its extension on it.
func (p *tilePic) paintIcon(pt *paint.Painter, th *theme.Live, box geom.Size, opacity float32) {
	if p.system != nil {
		pt.Image(p.system, systemIconRect(p.system, box), paint.ImageOpts{Opacity: opacity})
		return
	}
	if p.dir {
		s := min(box.W, box.H) * 0.92
		pt.Image(folderPic(), geom.Rc((box.W-s)/2, (box.H-s)/2, s, s), paint.ImageOpts{Opacity: opacity})
		return
	}
	s := min(box.W, box.H)
	page := geom.Rc((box.W-s*0.72)/2, (box.H-s*0.86)/2, s*0.72, s*0.86)
	c := TintToken(p.tint).Get(th)
	c.A = uint8(float32(c.A) * opacity)
	pt.ShadowRRect(page, s*0.08, paint.Solid(c), paint.Shadow{Offset: geom.Pt(0, s*0.03), Blur: s * 0.08,
		Color: color.NRGBA{A: uint8(0x50 * opacity)}})
	// The folded corner.
	fold := s * 0.18
	pt.RRect(geom.Rc(page.Max.X-fold, page.Min.Y, fold, fold), s*0.04,
		paint.Solid(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: uint8(0x55 * opacity)}))
	if p.ext == "" {
		return
	}
	size := max(8, s*0.15)
	if p.shapeOf != p.ext || p.shaped[0] != size {
		p.run = widget.BoldFont.Default().Shape(p.ext, size)
		p.shapeOf, p.shaped[0] = p.ext, size
	}
	ink := widget.ButtonStrongInk.Get(th)
	ink.A = uint8(float32(ink.A) * opacity)
	p.run.Paint(pt, geom.Pt(page.Min.X+(page.Size().W-p.run.Advance)/2, page.Max.Y-page.Size().H*0.3-p.run.Height()/2), ink)
}

// systemIconRect is where Windows' icon img shows in a box: as large as
// the box's shorter side, but never larger than the icon's own pixels,
// as a small icon blown up blurs.
func systemIconRect(img *paint.Image, box geom.Size) geom.Rect {
	w, _ := img.Size()
	s := min(min(box.W, box.H), float32(w))
	return geom.Rc((box.W-s)/2, (box.H-s)/2, s, s)
}

// errorMark is the small red mark on a tile whose picture cannot be read; its tooltip says why.
type errorMark struct {
	on bool
}

// Layout implements [gunim.Node].
func (m *errorMark) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	return c.Max
}

// Paint implements [gunim.Node].
func (m *errorMark) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	r := geom.Rect{Max: box.Point()}
	p.ShadowRRect(r, box.W/2, paint.Solid(ErrorInk.Get(f.Theme)), paint.Shadow{Offset: geom.Pt(0, 1), Blur: 3,
		Color: color.NRGBA{A: 0x70}})
	w := box.W * 0.14
	white := paint.Solid(color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff})
	p.RRect(geom.Rc((box.W-w)/2, box.H*0.2, w, box.H*0.4), w/2, white)
	p.RRect(geom.Rc((box.W-w)/2, box.H*0.68, w, w), w/2, white)
}

// viewBar is the switch in the status bar between details and icons, and the slider that sizes the icons, which
// slides out while they show.
type viewBar struct {
	anim.Group
	tile   float32
	open   *anim.Float
	modes  *widget.Segmented
	scope  *widget.Themed
	slider *widget.Slider
	// shown is whether a view mode has arrived, before which nothing slides.
	shown bool
}

// viewTheme sizes the switch to fit the status bar.
var viewTheme = theme.Make("files.views",
	theme.Set(widget.SegmentedHeight, 20),
	theme.Set(widget.SegmentedPadding, 6),
)

// viewIcon is the size of the switch's icons.
var viewIcon = theme.Length("files.views.icon", 14)

func newViewBar() *viewBar {
	v := &viewBar{tile: defaultTile, open: anim.NewFloat(0), modes: widget.NewSegmented(),
		slider: widget.NewSlider(minTile, maxTile)}
	v.Add(v.open)
	v.scope = widget.NewThemed(v.modes, viewTheme)
	v.modes.Icons = []*icon.Icon{icon.List, icon.LayoutGrid}
	v.modes.IconSize = viewIcon
	v.slider.Label = "Tile size"
	v.modes.OnChange = func(i int, u *gunim.UI) gunim.Intent {
		if i == 1 {
			return Command{Name: CmdViewIcons}
		}
		return Command{Name: CmdViewDetails}
	}
	v.slider.Snap = tileStep
	v.slider.OnChange = func(s float32, u *gunim.UI) gunim.Intent {
		v.tile = s
		return TileSized{Size: s}
	}
	return v
}

// set takes the view mode of the folder showing.
func (v *viewBar) set(m ViewMode, u *gunim.UI) {
	to, mode := float32(0), 0
	if m.Icons {
		to, mode = 1, 1
	}
	v.modes.SetSelected(mode, u)
	if v.shown {
		v.open.Animate(to, Page.Get(u.Theme()))
	} else {
		v.open.Jump(to)
		v.tile = m.Tile
		v.slider.SetValue(m.Tile, u)
	}
	v.shown = true
	u.Invalidate()
}

// zoomBy makes the icons larger by notches of the wheel, or smaller.
func (v *viewBar) zoomBy(notches float32, u *gunim.UI) {
	to := v.tile * float32(math.Pow(1.12, float64(notches)))
	// Whole steps of the slider, and at least one for each notch.
	to = float32(math.Round(float64(to)/tileStep)) * tileStep
	if to == v.tile {
		to += tileStep * float32(math.Copysign(1, float64(notches)))
	}
	to = min(max(to, minTile), maxTile)
	if to == v.tile {
		return
	}
	v.tile = to
	v.slider.SetValue(to, u)
	u.Send(v, TileSized{Size: to})
	u.Invalidate()
}

const (
	// tileStep is the step the tile size moves in.
	tileStep = 4
	sliderW  = 120
)

// Children implements [gunim.Composite].
func (v *viewBar) Children() []gunim.Node { return []gunim.Node{v.scope, v.slider} }

// Layout implements [gunim.Node].
func (v *viewBar) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	open := min(max(v.open.Value(), 0), 1)
	modes, slider := kids.At(0), kids.At(1)
	ms := modes.Layout(gunim.Loose(c.Max))
	h := max(c.Max.H, ms.H)
	modes.Place(geom.Pt(0, (h-ms.H)/2))
	slider.Layout(gunim.Tight(geom.Sz(sliderW, h)))
	slider.Place(geom.Pt(ms.W+10, 0))
	return c.Constrain(geom.Sz(ms.W+(10+sliderW)*open, h))
}

// Paint implements [gunim.Node].
func (v *viewBar) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
	if open := v.open.Value(); open > 0.01 {
		defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: min(open, 1), Clip: true})()
		kids.At(1).Paint(p)
	}
}

func lerp(a, b, t float32) float32 { return a + (b-a)*t }

func lerpRect(a, b geom.Rect, t float32) geom.Rect {
	return geom.Rect{Min: geom.Pt(lerp(a.Min.X, b.Min.X, t), lerp(a.Min.Y, b.Min.Y, t)),
		Max: geom.Pt(lerp(a.Max.X, b.Max.X, t), lerp(a.Max.Y, b.Max.Y, t))}
}
