package filemanager

import (
	"slices"
	"strings"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// menuItem is one item of a menu: its label, its keys, and the command
// it sends. A label of "-" puts a line in the menu.
type menuItem struct {
	label, hint, cmd string
}

// Commands the window does itself. A program runs them with [Run].
const (
	// CmdSelectAll selects every item, and CmdSelectNone none.
	CmdSelectAll = localSelectAll

	localEditPath   = "local.editpath"
	localFilter     = "local.filter"
	localSelectAll  = "local.selectall"
	localSelectNone = "local.selectnone"
)

// menus are the file manager's menus.
var menus = []struct {
	title string
	items []menuItem
}{
	{"File", []menuItem{
		{"New window", "Ctrl+N", CmdNewWindow},
		{"New folder", "Ctrl+Shift+N", CmdNewFolder},
		{"Open", "Enter", CmdOpen},
		{"-", "", ""},
		{"Pin to sidebar", "Ctrl+D", CmdPin},
		{"Show in system file manager", "", CmdReveal},
		{"-", "", ""},
		{"Upload edited copies: ask", "", CmdUploadAsk},
		{"Upload edited copies: always", "", CmdUploadAlways},
		{"Upload edited copies: never", "", CmdUploadNever},
		{"-", "", ""},
		{"Close", "Ctrl+W", CmdCloseApp},
	}},
	{"Edit", []menuItem{
		{"Undo", "Ctrl+Z", CmdUndo},
		{"-", "", ""},
		{"Cut", "Ctrl+X", CmdCut},
		{"Copy", "Ctrl+C", CmdCopy},
		{"Paste", "Ctrl+V", CmdPaste},
		{"Paste as zip…", "", CmdPasteZip},
		{"-", "", ""},
		{"Rename", "F2", CmdRename},
		{"Create zip…", "", CmdZip},
		{"Extract…", "", CmdExtract},
		{"Move to trash", "Delete", CmdTrash},
		{"Delete permanently", "Shift+Delete", CmdDelete},
		{"-", "", ""},
		{"Select all", "Ctrl+A", localSelectAll},
		{"Select none", "Escape", localSelectNone},
	}},
	{"View", []menuItem{
		{"Show hidden files", "Ctrl+H", CmdHidden},
		{"Preview pane", "", CmdPreview},
		{"Windows icons", "", CmdSystemIcons},
		{"-", "", ""},
		{"Details", "Ctrl+1", CmdViewDetails},
		{"Icons", "Ctrl+2", CmdViewIcons},
		{"View", "Space", CmdViewer},
		{"-", "", ""},
		{"Sort by name", "", CmdSortName},
		{"Sort by size", "", CmdSortSize},
		{"Sort by date modified", "", CmdSortTime},
		{"Sort by type", "", CmdSortType},
		{"-", "", ""},
		{"Dark theme", "", CmdThemeDark},
		{"Light theme", "", CmdThemeLight},
		{"Refresh", "F5", CmdRefresh},
	}},
	{"Go", []menuItem{
		{"Back", "Alt+Left", CmdBack},
		{"Forward", "Alt+Right", CmdForward},
		{"Up", "Alt+Up", CmdUp},
		{"Home", "Alt+Home", CmdHome},
		{"-", "", ""},
		{"Edit path", "Ctrl+L", localEditPath},
		{"Filter", "Ctrl+F", localFilter},
	}},
}

// titleBar is the file manager's menus, behind a button left of Back,
// and the window's own title bar: the title, and the window's buttons.
// A pane has no title bar, and nor has a window with the system's frame.
type titleBar struct {
	b *browser
	// bar is the menus, a compact menubar the path bar shows.
	bar  *menuButton
	head *titleHead
	// cmds holds each menu's commands, in the order the bar has them.
	cmds [][]string
	// folder is the name of the folder showing, once one is, and native
	// the title the window was given last.
	folder, native string
	// fetches says the menus are those of a file system whose files are
	// fetched to open, and pane that they are a pane's.
	fetches, pane bool
	// hosted are the commands the program's menus offer, which these
	// leave out.
	hosted []string
}

func newTitleBar(b *browser) *titleBar {
	t := &titleBar{b: b}
	t.bar = &menuButton{Menubar: widget.NewMenubar(t.build(false, false, nil)...), b: b}
	t.bar.Compact = true
	t.bar.OnPick = func(m, i int, u *gunim.UI) gunim.Intent {
		t.pick(m, i, u)
		return nil
	}
	t.head = &titleHead{name: &titleText{Menubar: widget.NewMenubar()}, controls: widget.NewWindowControls()}
	t.head.name.Title = "Files"
	return t
}

// menuButton is the menus, behind one button. In a pane, F10 and Alt
// open them only while the keyboard is in the pane.
type menuButton struct {
	*widget.Menubar
	b *browser
}

// CatchKey implements [gunim.KeyCatcher].
func (m *menuButton) CatchKey(e input.Event, u *gunim.UI) bool {
	if m.b.shell.HostMenus {
		return false
	}
	switch e.(type) {
	case input.KeyRelease, input.WindowFocusLost:
		// Wherever the keyboard is, so Alt held as it left the pane does
		// not keep the access keys underlined.
	default:
		if m.b.shell.Pane && !u.HasFocus(m.b) {
			return false
		}
	}
	return m.Menubar.CatchKey(e, u)
}

// titleText is the window's title, drawn by a menubar with no menus,
// which moves the window as a title bar does. It leaves the keys to the
// menus.
type titleText struct{ *widget.Menubar }

// CatchKey implements [gunim.KeyCatcher].
func (*titleText) CatchKey(input.Event, *gunim.UI) bool { return false }

// titleHead is the window's own title bar, where the window has none of
// the system's: its title, and its buttons.
type titleHead struct {
	name     *titleText
	controls *widget.WindowControls
	// hidden says there is no title bar to show: in a pane, or a window
	// with the system's frame.
	hidden bool
}

// Children implements [gunim.Composite].
func (h *titleHead) Children() []gunim.Node { return []gunim.Node{h.name, h.controls} }

// Layout implements [gunim.Node].
func (h *titleHead) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	name, controls := kids.At(0), kids.At(1)
	if h.hidden || !f.Chromeless() {
		name.Layout(gunim.Tight(geom.Size{}))
		controls.Layout(gunim.Tight(geom.Size{}))
		return geom.Sz(c.Max.W, 0)
	}
	hgt := max(widget.MenubarHeight.Get(f.Theme), 34)
	cs := controls.Layout(gunim.Constraints{Max: geom.Sz(c.Max.W, hgt)})
	controls.Place(geom.Pt(c.Max.W-cs.W, 0))
	name.Layout(gunim.Tight(geom.Sz(max(0, c.Max.W-cs.W), hgt)))
	name.Place(geom.Point{})
	return geom.Sz(c.Max.W, hgt)
}

// Paint implements [gunim.Node].
func (h *titleHead) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	if box.H <= 0 {
		return
	}
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(widget.MenubarFill.Get(f.Theme)))
	for k := range kids.All {
		k.Paint(p)
	}
}

// build makes the bar's menus, and sets the commands of their items. On
// a file system whose files are fetched to open, nothing shows in the
// system's file manager, so the menus leave that out. The items keep
// their ticks.
func (t *titleBar) build(fetches, pane bool, hosted []string) []widget.BarMenu {
	var was map[string]bool
	if t.bar != nil {
		was = map[string]bool{}
		for m, cmds := range t.cmds {
			for i, c := range cmds {
				was[c] = t.bar.Menus[m].Items[i].Checked
			}
		}
	}
	bars := make([]widget.BarMenu, 0, len(menus))
	t.cmds = t.cmds[:0]
	for _, m := range menus {
		items := m.items
		if !iconsHere {
			items = without(items, CmdSystemIcons)
		}
		if pane {
			// The program the pane is in picks the theme.
			items = without(without(items, CmdThemeDark), CmdThemeLight)
		}
		for _, c := range hosted {
			// The program's own menus offer it.
			items = without(items, c)
		}
		if fetches {
			items = without(items, CmdReveal)
		} else {
			// Only files fetched to open are uploaded.
			for _, c := range []string{CmdUploadAsk, CmdUploadAlways, CmdUploadNever} {
				items = without(items, c)
			}
		}
		bm := widget.BarMenu{Title: m.title}
		var cmds []string
		line := false
		for _, it := range items {
			if it.label == "-" {
				line = true
				continue
			}
			bm.Items = append(bm.Items, widget.MenuItem{Label: it.label, Hint: it.hint, Checked: was[it.cmd], Break: line})
			line = false
			cmds = append(cmds, it.cmd)
		}
		bars = append(bars, bm)
		t.cmds = append(t.cmds, cmds)
	}
	t.fetches, t.pane, t.hosted = fetches, pane, slices.Clone(hosted)
	return bars
}

// without returns items without the one that sends cmd, and without a
// line that would then start or end the menu, or follow another.
func without(items []menuItem, cmd string) []menuItem {
	var out []menuItem
	for _, it := range items {
		switch {
		case it.cmd == cmd && it.label != "-":
		case it.label == "-" && (len(out) == 0 || out[len(out)-1].label == "-"):
		default:
			out = append(out, it)
		}
	}
	if n := len(out); n > 0 && out[n-1].label == "-" {
		out = out[:n-1]
	}
	return out
}

// pick runs the command of item i of menu m.
func (t *titleBar) pick(m, i int, u *gunim.UI) { t.run(t.cmds[m][i], u) }

// run runs cmd as a pick of it in the menus does.
func (t *titleBar) run(cmd string, u *gunim.UI) {
	switch cmd {
	case localEditPath:
		t.b.path.edit(u)
	case localFilter:
		u.Focus(t.b.path.filter)
	case localSelectAll:
		t.b.listing.selectAll(u)
	case localSelectNone:
		t.b.listing.selectNone(u)
	default:
		u.Send(t.bar.Menubar, Command{Name: cmd})
	}
}

// check ticks the item that sends cmd, or not.
func (t *titleBar) check(cmd string, on bool) {
	for m, cmds := range t.cmds {
		for i, c := range cmds {
			if c == cmd {
				t.bar.Menus[m].Items[i].Checked = on
			}
		}
	}
}

func (t *titleBar) setShell(s Shell, u *gunim.UI) {
	if s.Fetches != t.fetches || s.Pane != t.pane || !slices.Equal(s.Hosted, t.hosted) {
		t.bar.Menus = t.build(s.Fetches, s.Pane, s.Hosted)
	}
	t.head.hidden = s.Pane
	for m, cmds := range t.cmds {
		for i, c := range cmds {
			if c == CmdTrash {
				t.bar.Menus[m].Items[i].Label = trashLabel(s.NoTrash)
			}
		}
	}
	t.retitle(u)
	t.check(CmdHidden, s.ShowHidden)
	t.check(CmdPreview, s.ShowPreview)
	t.check(CmdSystemIcons, s.SystemIcons)
	t.check(CmdThemeDark, !s.Light)
	t.check(CmdThemeLight, s.Light)
	up := s.UploadEdited
	t.check(CmdUploadAsk, up != uploadAlways && up != uploadNever)
	t.check(CmdUploadAlways, up == uploadAlways)
	t.check(CmdUploadNever, up == uploadNever)
}

// setListing names the folder in the title, and ticks the sort.
func (t *titleBar) setListing(l Listing, u *gunim.UI) {
	t.folder = l.Title
	t.retitle(u)
	for by, cmd := range []string{CmdSortName, CmdSortSize, CmdSortTime, CmdSortType} {
		t.check(cmd, SortBy(by) == l.Sort)
	}
}

// retitle names the file system, the folder and the program in the
// title, those it knows: the one drawn, and the window's own, which the
// system shows as it switches between windows. A pane leaves the
// window's title to the program. u may be nil, as in a test with no
// window.
func (t *titleBar) retitle(u *gunim.UI) {
	s := t.b.shell
	var parts []string
	for _, p := range []string{s.Where, t.folder, s.appName()} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	t.head.name.Title = strings.Join(parts, " — ")
	if u != nil && !s.Pane && t.head.name.Title != t.native {
		t.native = t.head.name.Title
		u.SetTitle(t.native)
	}
}

// appName is what the title calls the program.
func (s Shell) appName() string {
	if s.Name == "" {
		return "Files"
	}
	return s.Name
}

// trashLabel names the command that trashes: on a file system without a
// trash it deletes for good, after asking.
func trashLabel(noTrash bool) string {
	if noTrash {
		return "Delete…"
	}
	return "Move to trash"
}
