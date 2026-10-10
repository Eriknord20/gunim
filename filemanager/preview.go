package filemanager

import (
	"cmp"
	"path/filepath"
	"strings"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/paint"
	fileview "github.com/marrasen/gunim/viewer"
	"github.com/marrasen/gunim/widget"
)

func registerPreview(w *gunim.Window) {
	gunim.RegisterPatch(w, "browser", func(b *browser, p Preview, u *gunim.UI) { b.preview.show(p, u) })
	gunim.RegisterPatch(w, "browser", func(b *browser, c Counted, u *gunim.UI) { b.preview.counted(c, u) })
}

// previewPane shows the item selected: a picture's thumbnail or the start
// of a text file, and what there is to know about it. A new item
// crossfades in over the one before.
type previewPane struct {
	deck *deck
	cur  *previewPage
	seq  int
	// systemIcon returns the system icon of a key, nil for none yet.
	systemIcon func(key string) *paint.Image
}

func newPreviewPane(systemIcon func(key string) *paint.Image) *previewPane {
	return &previewPane{deck: &deck{}, systemIcon: systemIcon}
}

func (p *previewPane) show(s Preview, u *gunim.UI) {
	if s.Seq < p.seq {
		return
	}
	p.seq = s.Seq
	var system *paint.Image
	if s.IconKey != "" && p.systemIcon != nil {
		system = p.systemIcon(s.IconKey)
	}
	p.cur = newPreviewPage(s, system)
	p.deck.show(p.cur, 0, u)
}

func (p *previewPane) counted(c Counted, u *gunim.UI) {
	if p.cur == nil || c.Seq != p.seq {
		return
	}
	p.cur.counted(c)
	u.Invalidate()
}

// Children implements [gunim.Composite].
func (p *previewPane) Children() []gunim.Node { return []gunim.Node{p.deck} }

// Layout implements [gunim.Node].
func (p *previewPane) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	kid := kids.At(0)
	kid.Layout(gunim.Tight(c.Max))
	kid.Place(geom.Point{})
	return c.Max
}

// Paint implements [gunim.Node].
func (p *previewPane) Paint(pt *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	pt.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(PaneFill.Get(f.Theme)))
	kids.At(0).Paint(pt)
}

// previewPage is the preview of one subject.
type previewPage struct {
	scroll *widget.Scroll
	holds  *widget.Label
	size   *widget.Label
	// path shows the item's path, and links are the links under it.
	path  *widget.Label
	links []*widget.Link
}

func newPreviewPage(s Preview, system *paint.Image) *previewPage {
	pg := &previewPage{}
	var kids []gunim.Node
	if s.Title == "" && s.Seq > 0 && len(s.Facts) == 0 {
		empty := widget.NewLabel("Nothing to preview.")
		empty.Color = Faint
		pg.scroll = widget.NewScroll(&previewPad{child: empty})
		return pg
	}
	if s.Image != nil {
		img := widget.NewImage(s.Image)
		img.Fit, img.Radius, img.Alt = widget.FitContain, 8, s.Title
		kids = append(kids, &thumbBox{img: img})
	} else if s.Title != "" {
		kids = append(kids, newPreviewIcon(s, system))
	}
	title := widget.NewLabel(s.Title)
	title.Size, title.Face, title.MaxLines, title.Selectable = TitleText, widget.BoldFont, 3, true
	kind := widget.NewLabel(s.Type)
	kind.Color, kind.Size = Faint, SmallText
	kids = append(kids, title, kind)
	for _, f := range s.Facts {
		kids = append(kids, factRow(f.Label, f.Value))
	}
	if s.Counting {
		pg.holds, pg.size = widget.NewLabel("Counting…"), widget.NewLabel("…")
		kids = append(kids, factRowOf("Holds", pg.holds), factRowOf("Total size", pg.size))
	}
	if s.Path != "" {
		shown := cmp.Or(s.Shown, s.Path)
		pg.path = widget.NewLabel(shown)
		pg.path.Size, pg.path.Color, pg.path.Selectable = SmallText, Caption, true
		kids = append(kids, pg.path)
		if !s.NoReveal {
			reveal := widget.NewLink("Show in system file manager")
			reveal.Size = SmallText
			reveal.OnClick = widget.Sends(RevealPath{Path: s.Path})
			pg.links = append(pg.links, reveal)
		}
		copyPath := widget.NewLink("Copy path")
		copyPath.Size = SmallText
		copyPath.OnClick = func(u *gunim.UI) gunim.Intent {
			u.SetClipboard(shown)
			return nil
		}
		pg.links = append(pg.links, copyPath)
		nodes := make([]gunim.Node, len(pg.links))
		for i, l := range pg.links {
			nodes[i] = l
		}
		links := &linkWrap{Wrap: widget.NewWrap(), kids: nodes}
		links.Gap = sideGap
		kids = append(kids, links)
	}
	if s.Online {
		note := widget.NewLabel("This file is kept online only. Download it to preview it.")
		note.Color, note.Size = Faint, SmallText
		fetch := widget.NewButton("Download")
		fetch.Icon = icon.CloudDownload
		fetch.OnClick = widget.Sends(FetchPreview{Path: s.Path})
		row := widget.Row(fetch, widget.NewSpacer())
		kids = append(kids, note, row)
	}
	if s.Err != "" {
		e := widget.NewLabel(s.Err)
		e.Color, e.Size, e.Selectable = ErrorInk, SmallText, true
		kids = append(kids, e)
	}
	if s.Text != "" {
		// In its language's colours, or rendered for Markdown, whose links
		// lead nowhere from here.
		v := fileview.New(s.Title, []byte(s.Text), fileview.Options{Compact: true, Cut: s.Cut})
		if v.Kind() == fileview.Markdown {
			kids = append(kids, &textBox{child: v})
		} else {
			kids = append(kids, v)
		}
	}
	col := widget.Column(kids...)
	col.Cross = widget.CrossStretch
	pg.scroll = widget.NewScroll(&previewPad{child: col})
	return pg
}

// sideGap is the room between links in a row.
var sideGap = widget.Gap

// linkWrap sets links in a row, and moves a link that would pass the
// pane's edge to a line of its own.
type linkWrap struct {
	*widget.Wrap
	kids []gunim.Node
}

// Children implements [gunim.Composite].
func (l *linkWrap) Children() []gunim.Node { return l.kids }

// factRow is a label and its value, side by side.
func factRow(label, value string) gunim.Node {
	v := widget.NewLabel(value)
	return factRowOf(label, v)
}

func factRowOf(label string, v *widget.Label) gunim.Node {
	l := widget.NewLabel(label)
	l.Color, l.Size = Faint, SmallText
	v.Size, v.Selectable = SmallText, true
	row := widget.Row(widget.NewSized(l, 84, 0), v).Grow(v, 1)
	return row
}

func (pg *previewPage) counted(c Counted) {
	if pg.holds == nil {
		return
	}
	suffix := ""
	if c.Counting {
		suffix = " so far"
	}
	pg.holds.Text = c.Items + suffix
	pg.size.Text = c.Size + suffix
	if c.Err != "" {
		pg.holds.Color = ErrorInk
		pg.holds.Text = c.Items + " counted, then: " + c.Err
	}
}

// Children implements [gunim.Composite].
func (pg *previewPage) Children() []gunim.Node { return []gunim.Node{pg.scroll} }

// Layout implements [gunim.Node].
func (pg *previewPage) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	kid := kids.At(0)
	kid.Layout(gunim.Tight(c.Max))
	kid.Place(geom.Point{})
	return c.Max
}

// Paint implements [gunim.Node].
func (pg *previewPage) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}

// previewPad leaves room around the preview.
type previewPad struct {
	child gunim.Node
}

// Children implements [gunim.Composite].
func (p *previewPad) Children() []gunim.Node { return []gunim.Node{p.child} }

// Layout implements [gunim.Node].
func (p *previewPad) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	kid := kids.At(0)
	w := max(0, c.Max.W-32)
	s := kid.Layout(gunim.Constraints{Min: geom.Sz(w, 0), Max: geom.Sz(w, 0)})
	kid.Place(geom.Pt(16, 16))
	return geom.Sz(c.Max.W, s.H+32)
}

// Paint implements [gunim.Node].
func (p *previewPad) Paint(pt *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(pt)
}

// thumbBox shows a thumbnail as wide as the pane, and no taller than
// thumbHeight.
type thumbBox struct {
	img *widget.Image
}

const thumbHeight = 260

// Children implements [gunim.Composite].
func (t *thumbBox) Children() []gunim.Node { return []gunim.Node{t.img} }

// Layout implements [gunim.Node].
func (t *thumbBox) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	w, h := t.img.Source().Size()
	width := c.Max.W
	height := min(thumbHeight, width*float32(h)/float32(max(w, 1)))
	kid := kids.At(0)
	kid.Layout(gunim.Tight(geom.Sz(width, height)))
	kid.Place(geom.Point{})
	return geom.Sz(width, height+8)
}

// Paint implements [gunim.Node].
func (t *thumbBox) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}

// previewIcon is an item's picture when it has no thumbnail: drawn as
// the icon view draws it, its system icon, the folder, or a page in its
// colour with its extension on it.
type previewIcon struct{ pic *tilePic }

// newPreviewIcon is the picture of the item s previews, with its system
// icon, nil for none.
func newPreviewIcon(s Preview, system *paint.Image) *previewIcon {
	pic := newTilePic()
	pic.loaded, pic.dir, pic.tint, pic.system = true, s.Dir, s.Tint, system
	if ext := strings.TrimPrefix(filepath.Ext(s.Title), "."); !s.Dir && s.Path != "" && ext != "" && len(ext) <= 5 {
		pic.ext = strings.ToUpper(ext)
	}
	return &previewIcon{pic: pic}
}

// previewIconSize is how large the picture is.
const previewIconSize = 88

// Children implements [gunim.Composite].
func (i *previewIcon) Children() []gunim.Node { return []gunim.Node{i.pic} }

// Layout implements [gunim.Node]: the picture at the left of a row.
func (i *previewIcon) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	k := kids.At(0)
	k.Layout(gunim.Tight(geom.Sz(previewIconSize, previewIconSize)))
	k.Place(geom.Pt(0, 4))
	return geom.Sz(c.Max.W, previewIconSize+8)
}

// Paint implements [gunim.Node].
func (i *previewIcon) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}

// textBox frames the start of a text file.
type textBox struct {
	child gunim.Node
}

// Children implements [gunim.Composite].
func (t *textBox) Children() []gunim.Node { return []gunim.Node{t.child} }

// Layout implements [gunim.Node].
func (t *textBox) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	kid := kids.At(0)
	w := max(0, c.Max.W-20)
	s := kid.Layout(gunim.Constraints{Min: geom.Sz(w, 0), Max: geom.Sz(w, 0)})
	kid.Place(geom.Pt(10, 18))
	return geom.Sz(c.Max.W, s.H+28)
}

// Paint implements [gunim.Node].
func (t *textBox) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	r := geom.Rc(0, 8, box.W, box.H-8)
	p.RRect(r, 8, paint.Solid(widget.FieldFill.Get(f.Theme)))
	defer p.Layer(paint.LayerOpts{Bounds: r, Opacity: 1, Clip: true, Radius: 8})()
	kids.At(0).Paint(p)
}
