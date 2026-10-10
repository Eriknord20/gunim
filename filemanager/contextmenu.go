package filemanager

import (
	"slices"
	"strings"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/widget"
)

// Commands the context menus do in the window.
const (
	localCopyPath   = "local.copypath"
	localOpenWindow = "local.openwindow"
	localOpenPlace  = "local.openplace"
	localUnpin      = "local.unpin"
	localEditFav    = "local.editfav"
	localCollapse   = "local.collapse"
	localMoveUp     = "local.moveup"
	localMoveDown   = "local.movedown"
	localOpenWith   = "local.openwith"
)

// rowItems are the context menu of the items selected.
var rowItems = []menuItem{
	{"Open", "Enter", CmdOpen},
	{"View", "Space", CmdViewer},
	{"Open with system", "", CmdOpenSystem},
	{"Open with", "", localOpenWith},
	{"Open in new window", "", localOpenWindow},
	{"Show in system file manager", "", CmdReveal},
	{"-", "", ""},
	{"Cut", "Ctrl+X", CmdCut},
	{"Copy", "Ctrl+C", CmdCopy},
	{"Paste", "Ctrl+V", CmdPaste},
	{"Paste as zip…", "", CmdPasteZip},
	{"-", "", ""},
	{"Rename", "F2", CmdRename},
	{"Duplicate", "", CmdDuplicate},
	{"Create zip…", "", CmdZip},
	{"Extract…", "", CmdExtract},
	{"Move to trash", "Delete", CmdTrash},
	{"Delete permanently", "Shift+Delete", CmdDelete},
	{"-", "", ""},
	{"Pin to favourites", "Ctrl+D", CmdPin},
	{"Copy path", "Ctrl+Shift+C", localCopyPath},
	{"Properties", "Alt+Enter", CmdProperties},
}

// emptyItems are the context menu of the folder showing, for a press
// beside the items.
var emptyItems = []menuItem{
	{"New folder", "Ctrl+Shift+N", CmdNewFolder},
	{"Paste", "Ctrl+V", CmdPaste},
	{"Paste as zip…", "", CmdPasteZip},
	{"Refresh", "F5", CmdRefresh},
	{"-", "", ""},
	{"Sort by name", "", CmdSortName},
	{"Sort by size", "", CmdSortSize},
	{"Sort by date modified", "", CmdSortTime},
	{"Sort by type", "", CmdSortType},
	{"-", "", ""},
	{"Show hidden files", "Ctrl+H", CmdHidden},
	{"Preview pane", "", CmdPreview},
	{"-", "", ""},
	{"Open in new window", "Ctrl+N", CmdNewWindow},
	{"Copy path", "Ctrl+Shift+C", localCopyPath},
	{"Properties", "Alt+Enter", CmdProperties},
}

// placeItems are the context menu of a place in the sidebar, on any file
// system, and favItems of a favourite. Open on another file system than
// the window's asks for a Visit.
var (
	placeItems = []menuItem{
		{"Open", "", localOpenPlace},
		{"Open in new window", "", localOpenWindow},
	}
	favItems = []menuItem{
		{"Open", "", localOpenPlace},
		{"Open in new window", "", localOpenWindow},
		{"-", "", ""},
		{"Edit favourite…", "F2", localEditFav},
		{"Unpin", "", localUnpin},
	}
	// awayFavItems are the context menu of a favourite on another file
	// system than the window's, whose Open asks for a Visit.
	awayFavItems = []menuItem{
		{"Open", "", localOpenPlace},
		{"Open in new window", "", localOpenWindow},
		{"-", "", ""},
		{"Edit favourite…", "F2", localEditFav},
		{"Unpin", "", localUnpin},
	}
	// headItems are the context menu of a section's heading, whose first
	// item says Expand for a section closed.
	headItems = []menuItem{
		{"Collapse", "Enter", localCollapse},
		{"-", "", ""},
		{"Move up", "", localMoveUp},
		{"Move down", "", localMoveDown},
	}
)

// menuState is what a context menu offers, worked out as it opens.
type menuState struct {
	cmds []string
	// path is the item or place the menu is about, and fs the ID of
	// the file system of a place.
	path, fs string
	// away is set for a place on another file system than the window's.
	away bool
	// place is the place or favourite of the sidebar the menu is about.
	place Place
	// section is the ID of the section whose heading the menu is about.
	section string
	// paths are the items selected, for an action of the program's own.
	paths []string
}

// fill sets c's items from items, dimming those off says are off and
// ticking those on says are on, and returns the commands in order.
func fill(c *widget.ContextMenu, items []menuItem, off, on func(cmd string) bool) []string {
	lines, cmds := menuLines(items, off, on)
	c.SetItems(lines)
	return cmds
}

// menuLines returns the lines of a menu of items, dimming those off says
// are off and ticking those on says are on, and the commands in order.
func menuLines(items []menuItem, off, on func(cmd string) bool) (lines []widget.MenuItem, cmds []string) {
	line := false
	for _, it := range items {
		if it.label == "-" {
			line = true
			continue
		}
		lines = append(lines, widget.MenuItem{Label: it.label, Hint: it.hint, Disabled: off(it.cmd), Checked: on(it.cmd), Break: line})
		line = false
		cmds = append(cmds, it.cmd)
	}
	return lines, cmds
}

// paneHints takes away the hint of Copy path in a pane, whose
// Ctrl+Shift+C is the program's.
func (b *browser) paneHints(m *widget.ContextMenu, cmds []string) {
	items := m.Items()
	if i := slices.Index(cmds, localCopyPath); i >= 0 && b.shell.Pane && i < len(items) {
		items = slices.Clone(items)
		items[i].Hint = ""
		m.SetItems(items)
	}
}

// checked reports whether the menu bar ticks the item that sends cmd.
func (t *titleBar) checked(cmd string) bool {
	for m, cmds := range t.cmds {
		for i, c := range cmds {
			if c == cmd {
				return t.bar.Menus[m].Items[i].Checked
			}
		}
	}
	return false
}

// contextMenu makes the listing's context menu around the grid: for the
// items selected when pressed on one, and for the folder showing when
// pressed beside them.
func (pg *listingPage) contextMenu(g gunim.Node, rowAt func(geom.Point) int, selected func() [][2]int) *widget.ContextMenu {
	m := widget.NewContextMenu(g, nil)
	var st menuState
	m.Prepare = func(at geom.Point, u *gunim.UI) bool {
		b := pg.b
		// Another menu opened meanwhile: the programs asked for before
		// no longer fill one.
		b.dnd.withAsked = ""
		row := rowAt(at)
		clipEmpty := b.dnd.clip.Count == 0
		if row < 0 {
			b.listing.selectNone(u)
			st = menuState{path: b.listing.path}
			st.cmds = fill(m, emptyItems, func(cmd string) bool { return (cmd == CmdPaste || cmd == CmdPasteZip) && clipEmpty },
				b.title.checked)
			b.paneHints(m, st.cmds)
			return true
		}
		sel, dirs := pg.selectedRows(selected())
		one := len(sel) == 1
		st = menuState{}
		if one {
			st.path = b.shell.Paths.Join(b.listing.path, sel[0].Name)
		}
		items := rowItems
		if b.shell.Fetches {
			items = without(items, CmdReveal)
		}
		if acts := b.shell.Actions; len(acts) > 0 {
			// The program's own, after the ways to open.
			at := slices.IndexFunc(items, func(it menuItem) bool { return it.label == "-" })
			own := make([]menuItem, 0, len(acts)+1)
			for _, act := range acts {
				own = append(own, menuItem{act.Label, "", actionCmd + act.ID})
			}
			items = slices.Concat(items[:at], own, items[at:])
		}
		for _, r := range sel {
			st.paths = append(st.paths, b.shell.Paths.Join(b.listing.path, r.Name))
		}
		if !b.shell.OpenWith {
			items = without(items, localOpenWith)
		}
		if !one || sel[0].Dir || !isArchive(sel[0].Name) {
			// Only for an archive it can open.
			items = without(items, CmdExtract)
		}
		st.cmds = fill(m, items, func(cmd string) bool {
			switch cmd {
			case CmdOpenSystem:
				// Folders elsewhere do not open with this computer's
				// programs; their files are fetched to.
				return b.shell.Fetches && dirs > 0
			case CmdPaste, CmdPasteZip:
				return clipEmpty
			case CmdRename:
				return !one
			case CmdViewer:
				return !one || sel[0].Dir
			case localOpenWindow:
				return !one || !sel[0].Dir
			case localOpenWith:
				return !one || sel[0].Dir
			case CmdPin:
				return dirs == 0
			}
			if id, ok := strings.CutPrefix(cmd, actionCmd); ok {
				for _, act := range b.shell.Actions {
					if act.ID == id {
						return act.Files && dirs > 0 || act.One && !one
					}
				}
			}
			return false
		}, func(string) bool { return false })
		if i := slices.Index(st.cmds, CmdTrash); i >= 0 {
			items := m.Items()
			items[i].Label = trashLabel(b.shell.NoTrash)
			m.SetItems(items)
		}
		b.paneHints(m, st.cmds)
		b.dnd.askOpenWith(m, st, u)
		return true
	}
	m.OnPick = func(i int, u *gunim.UI) gunim.Intent {
		pg.b.dnd.menuPicked(m, st, i, u)
		return nil
	}
	m.OnPickSub = func(path []int, u *gunim.UI) gunim.Intent {
		pg.b.dnd.subPicked(m, st, path, u)
		return nil
	}
	return m
}

// items returns the node showing the items: the tiles or the grid.
func (pg *listingPage) items() gunim.Node {
	if pg.icons != nil && pg.icons.on {
		return pg.icons.grid
	}
	return pg.grid
}

// itemAt returns the item at p in the space of items, or -1.
func (pg *listingPage) itemAt(p geom.Point) int {
	if pg.icons != nil && pg.icons.on {
		return pg.icons.grid.TileAt(p)
	}
	return pg.grid.RowAt(p)
}

// itemRect returns where item i is in the space of items.
func (pg *listingPage) itemRect(i int) (geom.Rect, bool) {
	if pg.icons != nil && pg.icons.on {
		return pg.icons.grid.TileRect(i), i >= 0 && i < pg.icons.grid.Len()
	}
	return pg.grid.RowRect(i)
}

// selection returns the runs selected in the view showing: the tiles or the grid.
func (pg *listingPage) selection() [][2]int {
	if pg.icons != nil && pg.icons.on {
		sel, _ := pg.icons.grid.Selected()
		return sel
	}
	return pg.grid.SelectedRows()
}

// selectedRows returns the rows of the runs selected, and how many are folders.
func (pg *listingPage) selectedRows(runs [][2]int) (rows []Row, dirs int) {
	for _, r := range runs {
		for i := r[0]; i < r[1]; i++ {
			if row, ok := pg.view(i); ok {
				rows = append(rows, row)
				if row.Dir {
					dirs++
				}
			}
		}
	}
	return rows, dirs
}

// menuPicked does item i of a context menu m, which offered st.
func (v *dndView) menuPicked(m *widget.ContextMenu, st menuState, i int, u *gunim.UI) {
	if i < 0 || i >= len(st.cmds) {
		return
	}
	switch cmd := st.cmds[i]; cmd {
	case localCopyPath:
		v.copyPaths(u)
	case localOpenWindow:
		if st.away {
			u.Send(m, Visit{FS: st.fs, Path: st.path, NewWindow: true})
			return
		}
		u.Send(m, OpenWindow{Path: st.path})
	case localOpenPlace:
		if st.away {
			u.Send(m, Visit{FS: st.fs, Path: st.path})
			return
		}
		u.Send(m, Navigate{Path: st.path})
	case localUnpin:
		u.Send(m, Unpin{FS: st.fs, Path: st.path})
	case localEditFav:
		u.Send(m, EditFavourite{FS: st.fs, Path: st.path})
	case localCollapse:
		u.Send(m, v.b.side.toggle(st.section))
		u.Invalidate()
	case localMoveUp, localMoveDown:
		step := map[string]int{localMoveUp: -1, localMoveDown: 1}[cmd]
		if in := v.b.side.moved(st.section, step); in != nil {
			u.Send(m, in)
		}
	default:
		if id, ok := strings.CutPrefix(cmd, actionCmd); ok {
			u.Send(m, ItemActed{ID: id, Paths: st.paths})
			return
		}
		if id, ok := strings.CutPrefix(cmd, placeCmd); ok {
			u.Send(m, PlaceCommanded{Place: st.place, ID: id})
			return
		}
		u.Send(m, Command{Name: cmd})
	}
}

// copyPaths puts the paths of the items selected on the clipboard, a
// line each, or the folder's path when none is selected.
func (v *dndView) copyPaths(u *gunim.UI) {
	l := v.b.listing
	var paths []string
	if l.cur != nil {
		rows, _ := l.cur.selectedRows(l.cur.selection())
		for _, r := range rows {
			paths = append(paths, v.b.shell.Paths.Show(v.b.shell.Paths.Join(l.path, r.Name)))
		}
	}
	if len(paths) == 0 {
		paths = []string{v.b.shell.Paths.Show(l.path)}
	}
	u.SetClipboard(strings.Join(paths, "\n"))
}

// withCmd starts the command of a program in the Open with submenu,
// before its ID; with none, the item asks for the system's dialog.
const withCmd = "with:"

// openWithItems are the items of the Open with submenu: the programs,
// and the system's dialog to choose another. Until found, the programs
// are a dimmed line saying they are being looked for.
func openWithItems(apps []OpenWithApp, found bool) []menuItem {
	var items []menuItem
	if !found {
		items = append(items, menuItem{"Looking for apps…", "", ""})
	}
	for _, p := range apps {
		items = append(items, menuItem{p.Name, "", withCmd + p.ID})
	}
	if len(items) > 0 {
		items = append(items, menuItem{"-", "", ""})
	}
	return append(items, menuItem{"Choose another app…", "", withCmd})
}

// openWithSub returns the Open with submenu of the file at path, and its
// commands in order: the programs found for it, or a line saying they
// are being looked for.
func (v *dndView) openWithSub(path string) (sub *widget.Submenu, cmds []string) {
	found := path != "" && path == v.withPath
	var apps []OpenWithApp
	if found {
		apps = v.withApps
	}
	lines, cmds := menuLines(openWithItems(apps, found), func(cmd string) bool { return cmd == "" },
		func(string) bool { return false })
	return &widget.Submenu{Items: lines}, cmds
}

// askOpenWith gives the Open with item of menu m, which offered st, its
// submenu, and asks the program for the programs that open its file. The
// submenu fills in once they come.
func (v *dndView) askOpenWith(m *widget.ContextMenu, st menuState, u *gunim.UI) {
	i := slices.Index(st.cmds, localOpenWith)
	items := m.Items()
	if i < 0 || i >= len(items) {
		return
	}
	items = slices.Clone(items)
	items[i].Sub, _ = v.openWithSub(st.path)
	m.SetItems(items)
	if items[i].Disabled {
		return
	}
	v.withMenu, v.withAt, v.withAsked = m, i, st.path
	u.Send(m, OpenWithAsked{Path: st.path})
}

// openWithMenu fills the Open with submenu of the listing's menu with
// the programs the program sent for the file it was asked for, in place,
// while that menu is still open.
func (v *dndView) openWithMenu(o OpenWithMenu, u *gunim.UI) {
	if o.Path == "" || o.Path != v.withAsked {
		// Asked of another menu, which has opened since.
		return
	}
	v.withAsked = ""
	v.withPath, v.withApps = o.Path, o.Apps
	m := v.withMenu
	items := m.Items()
	if !m.Focusable() || v.withAt >= len(items) {
		return
	}
	items = slices.Clone(items)
	items[v.withAt].Sub, _ = v.openWithSub(o.Path)
	m.SetItems(items)
	u.Invalidate()
}

// subPicked does the item at path of a submenu of the context menu m,
// which offered st: a program of Open with, or its dialog.
func (v *dndView) subPicked(m *widget.ContextMenu, st menuState, path []int, u *gunim.UI) {
	if len(path) != 2 || path[0] < 0 || path[0] >= len(st.cmds) || st.cmds[path[0]] != localOpenWith {
		return
	}
	_, cmds := v.openWithSub(st.path)
	if path[1] < 0 || path[1] >= len(cmds) {
		return
	}
	if id, ok := strings.CutPrefix(cmds[path[1]], withCmd); ok {
		u.Send(m, OpenWith{Path: st.path, ID: id})
	}
}

// placeCmd starts the command of an item of the program's own in a
// place's menu, before the item's ID.
const placeCmd = "place:"

// actionCmd starts the command of an action of the program's own on the
// items selected.
const actionCmd = "action:"

// sideMenu is the sidebar's context menu for its places and favourites.
// Where the program adds items of its own, the menu asks it for them as
// the secondary button goes down, and opens once they come.
type sideMenu struct {
	b  *browser
	m  *widget.ContextMenu
	st menuState
	// seq counts the menus asked of the program. asked is the one waiting
	// for its items, at where it was pressed, about the row of key.
	seq   int
	asked bool
	at    geom.Point
	key   widget.Key
	// given are the program's items for the menu opening now.
	given []PlaceItem
	// giving is set while the menu opens with the items the program gave.
	giving bool
}

// newSideMenu wraps the sidebar in a context menu for its places,
// favourites and headings.
func newSideMenu(b *browser) *sideMenu {
	s := &sideMenu{b: b, m: widget.NewContextMenu(b.side, nil)}
	s.m.Prepare = s.prepare
	s.m.OnPick = func(i int, u *gunim.UI) gunim.Intent {
		b.dnd.menuPicked(s.m, s.st, i, u)
		return nil
	}
	b.side.menuAt = func(h *sectionHead, u *gunim.UI) {
		hr, ok := u.Bounds(h)
		mr, ok2 := u.Bounds(s.m)
		if ok && ok2 {
			s.m.Open(hr.Center().Sub(mr.Min), u)
		}
	}
	return s
}

// headAt returns the heading under at, in the menu's space.
func (s *sideMenu) headAt(at geom.Point, u *gunim.UI) *sectionHead {
	mr, ok := u.Bounds(s.m)
	if !ok {
		return nil
	}
	p := at.Add(mr.Min)
	for _, id := range s.b.side.order {
		if c := s.b.side.byID[id]; c != nil {
			if r, drawn := u.Bounds(c.head); drawn && r.Contains(p) {
				return c.head
			}
		}
	}
	return nil
}

// prepareHead fills the menu for the heading h: open or close its
// section, or move it up or down.
func (s *sideMenu) prepareHead(h *sectionHead) {
	id, order := h.sec.id, s.b.side.order
	s.st = menuState{section: id}
	s.st.cmds = fill(s.m, headItems, func(cmd string) bool {
		switch cmd {
		case localMoveUp:
			return slices.Index(order, id) <= 0
		case localMoveDown:
			return slices.Index(order, id) >= len(order)-1
		}
		return false
	}, func(string) bool { return false })
	if h.sec.closed() {
		items := s.m.Items()
		items[0].Label = "Expand"
		s.m.SetItems(items)
	}
}

// rowAt returns the place or favourite under at, in the menu's space, and
// whether it is a favourite.
func (s *sideMenu) rowAt(at geom.Point, u *gunim.UI) (*placeRow, bool) {
	mr, ok := u.Bounds(s.m)
	if !ok {
		return nil, false
	}
	p := at.Add(mr.Min)
	for _, l := range s.b.side.lists() {
		for _, k := range l.Keys() {
			n, ok := l.Row(k)
			pr, isPlace := n.(*placeRow)
			if !ok || !isPlace {
				continue
			}
			if r, ok := u.Bounds(n); ok && r.Contains(p) {
				return pr, l == s.b.side.favs
			}
		}
	}
	return nil, false
}

// prepare fills the menu for the place pressed at at. Where the program
// adds items, it asks for them first, and opens no menu until they come.
func (s *sideMenu) prepare(at geom.Point, u *gunim.UI) bool {
	if h := s.headAt(at, u); h != nil {
		s.prepareHead(h)
		return true
	}
	pr, fav := s.rowAt(at, u)
	if pr == nil {
		return false
	}
	if s.b.shell.PlaceMenu && !s.giving {
		s.seq++
		s.asked, s.at, s.key = true, at, pr.item.key
		u.Send(s.m, PlaceMenuAsked{Seq: s.seq, Place: pr.item.Place})
		return false
	}
	s.st = menuState{path: pr.item.Path, fs: pr.item.FS, away: pr.item.away, place: pr.item.Place}
	items := placeItems
	switch {
	case fav && pr.item.away:
		items = awayFavItems
	case fav:
		items = favItems
	}
	if s.giving && pr.item.key == s.key && len(s.given) > 0 {
		items = slices.Clone(items)
		items = append(items, menuItem{"-", "", ""})
		for _, it := range s.given {
			items = append(items, menuItem{it.Label, "", placeCmd + it.ID})
		}
	}
	s.st.cmds = fill(s.m, items, func(string) bool { return false }, func(string) bool { return false })
	return true
}

// give opens the menu the program has given its items for, unless
// another has been asked for since.
func (s *sideMenu) give(v PlaceMenuItems, u *gunim.UI) {
	if !s.asked || v.Seq != s.seq {
		return
	}
	s.asked = false
	s.given, s.giving = v.Items, true
	defer func() { s.given, s.giving = nil, false }()
	s.m.Open(s.at, u)
}

// keys takes the keys of drag and drop, the context menus and the
// windows: the Menu key and Shift+F10 open the listing's menu at the row
// the keyboard is on, Ctrl+N opens a window, Alt+Enter the properties,
// and Ctrl+Shift+C copies the paths.
func (v *dndView) keys(e input.KeyPress, u *gunim.UI) bool {
	ctrl, shift, alt := e.Mods.Has(input.ModControl), e.Mods.Has(input.ModShift), e.Mods.Has(input.ModAlt)
	switch {
	case ctrl && !shift && e.Key == input.KeyN:
		u.Send(v.b, Command{Name: CmdNewWindow})
		return true
	case alt && (e.Key == input.KeyEnter || e.Key == input.KeyKPEnter):
		u.Send(v.b, Command{Name: CmdProperties})
		return true
	case ctrl && shift && e.Key == input.KeyC:
		if v.b.shell.Pane {
			// The program's, as its Copy often is: Copy path is a
			// menu's.
			return false
		}
		v.copyPaths(u)
		return true
	case e.Key == input.KeyMenu, shift && e.Key == input.KeyF10:
	default:
		return false
	}
	l := v.b.listing
	if l.cur == nil {
		return false
	}
	at := geom.Pt(40, 40)
	if row := l.cur.grid.Selected(); row >= 0 {
		if r, ok := l.cur.grid.RowRect(row); ok {
			at = geom.Pt(r.Min.X+40, r.Max.Y)
		}
	}
	l.cur.menu.Open(at, u)
	return true
}
