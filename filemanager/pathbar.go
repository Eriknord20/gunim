package filemanager

import (
	"image/color"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// pathBarHeight is the path bar's height.
const pathBarHeight = 44

func registerPathBar(w *gunim.Window) {
	gunim.RegisterPatch(w, "browser", func(b *browser, s Banner, u *gunim.UI) { b.banner.set(s, u) })
}

// pathBar is the row under the title bar: back, forward and up, the
// folders of the path, and the filter.
type pathBar struct {
	b             *browser
	back, fwd, up *widget.IconButton
	addr          *widget.AddressBar
	filter        *filterField
	row           *widget.Flex
	path          string
	// room is the browser's width, as it was laid out last.
	room float32
}

func newPathBar(b *browser) *pathBar {
	p := &pathBar{b: b}
	p.back = newNavButton(icon.ArrowLeft, "Back (Alt+Left)", CmdBack)
	p.fwd = newNavButton(icon.ArrowRight, "Forward (Alt+Right)", CmdForward)
	p.up = newNavButton(icon.ArrowUp, "Up (Alt+Up)", CmdUp)
	p.addr = widget.NewAddressBar()
	// A path entered in the field was typed; one of the places was not.
	p.addr.OnGo = func(path string, u *gunim.UI) gunim.Intent { return Navigate{Path: path, Typed: p.addr.Editing()} }
	// The machine the path is on goes to its home there.
	p.addr.OnLead = widget.Sends(Command{Name: CmdHome})
	p.addr.OnDone = func(u *gunim.UI) gunim.Intent {
		b.focusListing(u)
		return nil
	}
	p.filter = &filterField{TextField: widget.NewTextField(), bar: p}
	p.filter.Placeholder = "Filter this folder"
	p.filter.Icon, p.filter.Clearable = icon.Search, true
	p.filter.OnChange = func(s string, u *gunim.UI) gunim.Intent { return FilterChanged{Text: s} }
	nav := func(b *widget.IconButton) gunim.Node { return widget.NewSized(b, navSize, navSize) }
	// The menus sit behind a button left of Back, drawn as the path bar's
	// buttons are, on nothing of their own.
	menus := &menuSlot{child: widget.NewThemed(widget.NewSized(b.title.bar, navSize+8, navSize), menuButtonTheme()), b: b}
	p.row = widget.Row(menus, nav(p.back), nav(p.fwd), nav(p.up), p.addr, &filterBox{child: p.filter, bar: p}).Grow(p.addr, 1)
	p.row.Cross = widget.CrossCenter
	p.row.Gap = smallGap
	return p
}

// smallGap is the gap between the path bar's parts.
var smallGap = theme.Length("files.gap.small", 4)

// menuSlot holds the menus' button, and nothing where the program shows
// the menus in its own.
type menuSlot struct {
	child gunim.Node
	b     *browser
}

// Children implements [gunim.Composite].
func (m *menuSlot) Children() []gunim.Node { return []gunim.Node{m.child} }

// Layout implements [gunim.Node].
func (m *menuSlot) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	if m.b.shell.HostMenus {
		kids.At(0).Layout(gunim.Tight(geom.Size{}))
		return geom.Size{}
	}
	size := kids.At(0).Layout(c)
	kids.At(0).Place(geom.Point{})
	return size
}

// Paint implements [gunim.Node].
func (m *menuSlot) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	if !m.b.shell.HostMenus {
		kids.At(0).Paint(p)
	}
}

// filterBox gives the filter a share of the path bar: 220 wide, and
// narrower in a narrow browser, as a pane among others can be, so the
// path keeps room. Where too little is left, it gives the path all of
// it, and the filter hides.
type filterBox struct {
	child  gunim.Node
	bar    *pathBar
	hidden bool
}

// Children implements [gunim.Composite].
func (f *filterBox) Children() []gunim.Node { return []gunim.Node{f.child} }

// Layout implements [gunim.Node].
func (f *filterBox) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	room := f.bar.room
	w := min(max(room*0.25, 120), 220)
	if f.hidden = room > 0 && room < 560; f.hidden {
		w = 0
	}
	cs := c
	cs.Min.W, cs.Max.W = w, w
	size := kids.At(0).Layout(cs)
	kids.At(0).Place(geom.Point{})
	size.W = w
	return c.Constrain(size)
}

// Paint implements [gunim.Node].
func (f *filterBox) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	if !f.hidden {
		kids.At(0).Paint(p)
	}
}

// navSize is the size of the back, forward and up buttons.
const navSize = 32

// menuButtonTheme draws the menus' button with no fill of its own, as
// high as the path bar's buttons, so the menus open below them.
func menuButtonTheme() theme.Theme {
	return theme.Make("files.menubutton", theme.Set(widget.MenubarFill, color.NRGBA{}), theme.Set(widget.MenubarHeight, navSize))
}

// newNavButton returns a button that sends cmd, which leaves the keyboard with the listing when clicked.
func newNavButton(ic *icon.Icon, tooltip, cmd string) *widget.IconButton {
	b := widget.NewIconButton(ic, tooltip)
	b.KeepFocus, b.Disabled = true, true
	b.OnClick = widget.Sends(Command{Name: cmd})
	return b
}

// setListing shows the folder of l.
func (p *pathBar) setListing(l Listing, u *gunim.UI) {
	p.back.Disabled, p.fwd.Disabled, p.up.Disabled = !l.CanBack, !l.CanForward, !l.CanUp
	u.Invalidate()
	if l.Path != p.path {
		p.path = l.Path
		cs := make([]widget.Crumb, len(l.Crumbs))
		for i, c := range l.Crumbs {
			cs[i] = widget.Crumb{Name: c.Name, Path: c.Path}
		}
		p.addr.SetPath(p.b.shell.Paths.Show(l.Path), cs, u)
		p.filter.SetText(l.Filter, u)
	}
}

// setShell names the file system the path is on before its folders, as
// the title does.
func (p *pathBar) setShell(s Shell, u *gunim.UI) {
	lead := widget.AddressLead{Name: s.Where}
	if s.Where != "" {
		lead.Icon, lead.Tooltip = icon.Server, "Home on "+s.Where
		if s.FS == "" {
			lead.Icon = icon.Monitor
		}
	}
	p.addr.SetLead(lead, u)
}

// edit turns the folders into a field holding the path, all selected.
func (p *pathBar) edit(u *gunim.UI) { p.addr.Edit(u) }

// Children implements [gunim.Composite].
func (p *pathBar) Children() []gunim.Node { return []gunim.Node{p.row} }

// Layout implements [gunim.Node].
func (p *pathBar) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	kid := kids.At(0)
	kid.Layout(gunim.Tight(geom.Sz(c.Max.W-16, pathBarHeight-10)))
	kid.Place(geom.Pt(8, 5))
	return geom.Sz(c.Max.W, pathBarHeight)
}

// Paint implements [gunim.Node].
func (p *pathBar) Paint(pt *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	pt.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(widget.Background.Get(f.Theme)))
	pt.RRect(geom.Rc(0, box.H-1, box.W, 1), 0, paint.Solid(widget.SplitLine.Get(f.Theme)))
	kids.At(0).Paint(pt)
}

// filterField is the filter: Escape empties it, and Down goes to the
// listing.
type filterField struct {
	*widget.TextField
	bar *pathBar
}

// Handle implements [gunim.Handler].
func (f *filterField) Handle(e input.Event, u *gunim.UI) bool {
	if k, ok := e.(input.KeyPress); ok {
		switch k.Key {
		case input.KeyEscape:
			if f.Text() != "" {
				f.SetText("", u)
				u.Send(f, FilterChanged{})
			}
			f.bar.b.focusListing(u)
			return true
		case input.KeyDown, input.KeyEnter, input.KeyKPEnter:
			f.bar.b.focusListing(u)
			return true
		default:
		}
	}
	return f.TextField.Handle(e, u)
}

// bannerView is the line under the path bar that says what went wrong.
// As it shuts, the keyboard on its Dismiss link goes to away.
type bannerView struct {
	fold    *fold
	label   *widget.Label
	dismiss *widget.Link
	away    func(u *gunim.UI)
	seq     int
}

func newBannerView(away func(u *gunim.UI)) *bannerView {
	b := &bannerView{label: widget.NewLabel(""), dismiss: widget.NewLink("Dismiss"), away: away}
	b.label.Color = ErrorInk
	row := widget.Row(b.label, b.dismiss).Grow(b.label, 1)
	row.Cross = widget.CrossCenter
	b.fold = newFold(&bannerBox{child: row})
	b.dismiss.OnClick = func(u *gunim.UI) gunim.Intent {
		b.shut(u)
		return BannerDismissed{Seq: b.seq}
	}
	return b
}

func (b *bannerView) set(s Banner, u *gunim.UI) {
	if s.Seq < b.seq {
		return
	}
	b.seq = s.Seq
	if s.Text == "" {
		b.shut(u)
		return
	}
	b.label.Text = s.Text
	b.fold.set(true, u)
}

// shut folds the banner away, and sends the keyboard away from it.
func (b *bannerView) shut(u *gunim.UI) {
	b.fold.set(false, u)
	if u.HasFocus(b.dismiss) {
		b.away(u)
	}
}

// bannerBox pads the banner and paints its tinted background.
type bannerBox struct {
	child gunim.Node
}

// Children implements [gunim.Composite].
func (b *bannerBox) Children() []gunim.Node { return []gunim.Node{b.child} }

// Layout implements [gunim.Node].
func (b *bannerBox) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	kid := kids.At(0)
	s := kid.Layout(gunim.Constraints{Min: geom.Sz(c.Max.W-24, 0), Max: geom.Sz(c.Max.W-24, 0)})
	kid.Place(geom.Pt(12, 8))
	return geom.Sz(c.Max.W, s.H+16)
}

// Paint implements [gunim.Node].
func (b *bannerBox) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(ErrorFill.Get(f.Theme)))
	kids.At(0).Paint(p)
}
