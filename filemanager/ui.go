package filemanager

import (
	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// RegisterViews registers the window half on w: the themes, the browser that holds
// every area, and the dialogs.
func RegisterViews(w *gunim.Window) {
	w.RegisterTheme(darkTheme())
	w.RegisterTheme(lightTheme())
	gunim.RegisterView(w, "browser", func(Shell) *browser { return newBrowser() }, (*browser).setShell)
	registerListing(w)
	registerSidebar(w)
	registerPathBar(w)
	registerPreview(w)
	registerOps(w)
	registerStatus(w)
	registerDialogs(w)
	registerIcons(w)
	registerPalette(w)
	registerSpeed(w)
	registerRowsLeft(w)
	registerDnd(w)
	registerProps(w)
}

// root is the window's root: the background, with the views stacked on
// it. It tells the application when the window has the keyboard back.
type root struct {
	widget.Surface
}

// Handle implements [gunim.Handler].
func (r *root) Handle(e input.Event, u *gunim.UI) bool {
	if _, ok := e.(input.WindowFocusGained); ok {
		u.Send(r, WindowFocused{})
	}
	return false
}

// browser is the whole window: the title bar, the path bar, the sidebar,
// the listing and the preview, the progress panel, the status bar, and
// the toasts over them.
type browser struct {
	shell Shell
	// icons are the icons Windows shows for items, by their keys, as the
	// app half sent them.
	icons   map[string]SystemIcon
	shown   bool
	title   *titleBar
	path    *pathBar
	banner  *bannerView
	side    *sidebar
	listing *listingArea
	preview *previewPane
	ops     *opsPanel
	status  *statusBar
	toasts  *widget.Toasts
	// layer holds the dialogs, over the browser only.
	layer   *dialogLayer
	palette *filesPalette
	split   *widget.Split
	main    *widget.Split
	// narrow says the browser is too narrow for the preview, which
	// folds away meanwhile, as in a pane among others.
	narrow bool
	page   *widget.Flex
	dnd    *dndView
}

func newBrowser() *browser {
	b := &browser{toasts: widget.NewToasts(), icons: map[string]SystemIcon{}, layer: &dialogLayer{}}
	b.title = newTitleBar(b)
	b.path = newPathBar(b)
	b.banner = newBannerView(b.focusListing)
	b.side = newSidebar()
	b.listing = newListingArea(b)
	b.preview = newPreviewPane(func(key string) *paint.Image { return b.icons[key].Large })
	b.ops = newOpsPanel()
	b.status = newStatusBar()
	b.palette = newFilesPalette(b)
	b.dnd = newDndView(b)
	b.main = widget.NewSplit(b.dnd.listing, b.preview)
	b.main.Glide = Page
	b.main.SetShare(0.72, nil)
	b.split = widget.NewSplit(b.dnd.side, b.main)
	b.split.Fixed = true
	b.split.SetShare(sidebarWidth, nil)
	b.split.OnCommit = func(w float32, u *gunim.UI) gunim.Intent { return SidebarMoved{Width: w} }
	b.page = widget.Column(b.title.head, b.dnd.crumbs, b.banner.fold, b.split, b.ops.fold, b.status).Grow(b.split, 1)
	b.page.Cross = widget.CrossStretch
	b.page.Gap = noGap
	return b
}

// previewRoom is how wide the browser must be to show the preview.
const previewRoom = 860

// previewShare is the listing's share of the room beside the preview:
// all of it while the preview is hidden, or the browser too narrow.
func (b *browser) previewShare() float32 {
	if !b.shell.ShowPreview || b.narrow {
		return 1
	}
	return 0.72
}

// sidebarWidth is the sidebar's width until the user moves it.
const sidebarWidth = 220

// noGap is a gap of nothing, for rows and columns packed tight.
var noGap = theme.Length("files.nogap", 0)

// setShell takes the state of the window as a whole.
func (b *browser) setShell(s Shell, u *gunim.UI) {
	was := b.shell
	b.shell = s
	if s.Light != was.Light && !s.Pane {
		th := darkTheme()
		if s.Light {
			th = lightTheme()
		}
		u.UseTheme(th)
	}
	if s.Sidebar > 0 && !b.shown {
		b.split.SetShare(s.Sidebar, nil)
	}
	if b.shown {
		b.main.SetShare(b.previewShare(), u)
	} else {
		b.main.SetShare(b.previewShare(), nil)
	}
	b.title.setShell(s, u)
	b.path.setShell(s, u)
	b.side.fs, b.side.ps = s.FS, s.Paths
	b.dnd.u = u
	b.shown = true
}

// Children implements [gunim.Composite].
func (b *browser) Children() []gunim.Node { return []gunim.Node{b.page, b.toasts, b.layer} }

// Slot implements [gunim.Slotted]: the dialogs mounted under the browser
// go over it.
func (b *browser) Slot() gunim.Node { return b.layer }

// ModalScope implements [gunim.ModalScope]: a dialog holds the keyboard
// in the browser only, so in a pane the rest of the window works on.
func (b *browser) ModalScope() {}

// Layout implements [gunim.Node]: the page fills the window, and the
// toasts sit at the bottom right, over the progress panel and the status
// bar. The dialogs go over them all.
func (b *browser) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	b.path.room = c.Max.W
	if narrow := c.Max.W < previewRoom; narrow != b.narrow {
		b.narrow = narrow
		// Laid out, with no UI to glide with: it moves at once.
		b.main.SetShare(b.previewShare(), nil)
	}
	page, toasts, layer := kids.At(0), kids.At(1), kids.At(2)
	page.Layout(gunim.Tight(c.Max))
	page.Place(geom.Point{})
	ts := toasts.Layout(gunim.Loose(geom.Sz(c.Max.W-32, c.Max.H)))
	toasts.Place(geom.Pt(c.Max.W-ts.W-16, c.Max.H-ts.H-40-b.ops.fold.height))
	layer.Layout(gunim.Tight(c.Max))
	layer.Place(geom.Point{})
	return c.Max
}

// dialogLayer holds the dialogs, each over the whole of the browser. It
// takes no room while it holds none, so the pointer goes by it.
type dialogLayer struct{ _ byte }

// Layout implements [gunim.Node].
func (*dialogLayer) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	if kids.Len() == 0 {
		return geom.Size{}
	}
	for k := range kids.All {
		k.Layout(gunim.Tight(c.Max))
		k.Place(geom.Point{})
	}
	return c.Max
}

// Paint implements [gunim.Node].
func (*dialogLayer) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for k := range kids.All {
		k.Paint(p)
	}
}

// Paint implements [gunim.Node].
func (b *browser) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for k := range kids.All {
		k.Paint(p)
	}
}

// Handle implements [gunim.Handler]: going back and forward, and the keys that work anywhere in the
// window.
func (b *browser) Handle(e input.Event, u *gunim.UI) bool {
	if b.shell.Pane {
		// The places are looked for again as the keyboard comes back to
		// the window, as to a window of its own.
		if _, ok := e.(input.WindowFocusGained); ok {
			u.Send(b, WindowFocused{})
			return false
		}
	}
	// The mouse's side buttons, and a keyboard's Browser Back and Forward keys
	if h, ok := e.(input.HistoryStep); ok {
		cmd := CmdBack
		if h.Forward {
			cmd = CmdForward
		}
		u.Send(b, Command{Name: cmd})
		return true
	}
	k, ok := e.(input.KeyPress)
	if !ok {
		return false
	}
	if b.dnd.keys(k, u) {
		return true
	}
	ctrl, shift, alt := k.Mods.Has(input.ModControl), k.Mods.Has(input.ModShift), k.Mods.Has(input.ModAlt)
	// plain is Ctrl alone, so Ctrl+Shift+Z, say, leaves undo alone.
	plain := ctrl && !shift && !alt
	var cmd string
	switch {
	case ctrl && (alt || shift && k.Key != input.KeyN):
		// Left to the program round the browser, such as a pane's host,
		// whose own keys these often are.
		return false
	case plain && k.Key == input.KeyL:
		b.path.edit(u)
		return true
	case plain && k.Key == input.KeyF:
		u.Focus(b.path.filter)
		return true
	case ctrl && k.Key == input.KeyP:
		b.palette.open("", u)
		return true
	case ctrl && shift && k.Key == input.KeyN:
		cmd = CmdNewFolder
	case plain && k.Key == input.KeyC:
		cmd = CmdCopy
	case plain && k.Key == input.KeyX:
		cmd = CmdCut
	case plain && k.Key == input.KeyV:
		cmd = CmdPaste
	case plain && k.Key == input.KeyZ:
		cmd = CmdUndo
	case plain && k.Key == input.KeyH:
		cmd = CmdHidden
	case plain && k.Key == input.KeyD:
		cmd = CmdPin
	case plain && k.Key == input.KeyR:
		// As a browser reloads, beside F5.
		cmd = CmdRefresh
	case plain && k.Key == input.KeyW:
		cmd = CmdCloseApp
	case plain && k.Key == input.Key1:
		cmd = CmdViewDetails
	case plain && k.Key == input.Key2:
		cmd = CmdViewIcons
	case ctrl:
		return false
	case alt && k.Key == input.KeyLeft, !alt && k.Key == input.KeyBackspace:
		cmd = CmdBack
	case alt && k.Key == input.KeyRight:
		cmd = CmdForward
	case alt && k.Key == input.KeyUp:
		cmd = CmdUp
	case alt && k.Key == input.KeyHome:
		cmd = CmdHome
	case k.Key == input.KeyF5:
		cmd = CmdRefresh
	case k.Key == input.KeySpace && !shift:
		cmd = CmdViewer
	case k.Key == input.KeyF2:
		if in := b.side.editFocused(u); in != nil {
			u.Send(b, in)
			return true
		}
		cmd = CmdRename
	case shift && k.Key == input.KeyDelete:
		cmd = CmdDelete
	case k.Key == input.KeyDelete:
		cmd = CmdTrash
	default:
		return false
	}
	u.Send(b, Command{Name: cmd})
	return true
}

// focusListing gives the keyboard to the grid showing.
func (b *browser) focusListing(u *gunim.UI) {
	if b.listing.cur != nil {
		u.Focus(b.listing.cur.focusNode())
	}
}
