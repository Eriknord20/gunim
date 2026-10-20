package main

import (
	"image/color"
	"strings"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/chat"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// registerViews is the window half: the chat view, its patches and the themes.
func registerViews(w *gunim.Window) {
	w.RegisterTheme(darkTheme())
	w.RegisterTheme(lightTheme())
	gunim.RegisterView(w, "chat", buildChat, (*chatView).set)
	gunim.RegisterPatch(w, "chat", func(v *chatView, t Typing, u *gunim.UI) { v.chat.SetTyping(t.Who, u) })
}

// Widths of the rail and the sidebar.
const (
	railW    = 68
	sidebarW = 248
)

// chatView is the whole window: the project rail, the sidebar of conversations, and the open conversation.
type chatView struct {
	root gunim.Node
	// screens lays out the rail, the sidebar and the open conversation,
	// side by side or, on a phone, one screen at a time; sidebar is the
	// sidebar's panel. picks is the count of picks from the list seen.
	screens *screens
	sidebar *panel
	picks   int

	rail        *rail
	projectName *widget.Label
	convs       *widget.List

	// link is the header's button that drops the connection and brings it back, and linkBar the bar over the
	// pane while it is down.
	link    *widget.Button
	linkBar *chat.LinkBar
	// chat is the open conversation.
	chat *chat.View
	// solo says the window shows its conversation alone, without the projects and the conversations.
	solo bool
	// areas holds the conversation and the project's files, and shows one of them; files is the second.
	areas *areas
	files *filesPane
}

// buildChat builds the chat's window: the projects, the conversations and the one open, or for a window popped out
// of the main one, its conversation alone.
func buildChat(s Chat) *chatView {
	v := &chatView{solo: s.Solo}
	hints := []string{"/help to show commands"}
	if s.Solo {
		// A window of its own names the conversation in its title bar.
		v.chat = chat.NewView(chat.Options{LinkBar: true, Hints: hints})
		v.root = v.chat
		return v
	}

	v.rail = &rail{hover: -1}
	v.projectName = widget.NewLabel("")
	v.projectName.Face, v.projectName.Size = widget.BoldFont, sidebarTitle
	v.convs = widget.NewList()
	v.convs.OnActivate = func(k widget.Key, u *gunim.UI) gunim.Intent {
		if area, ok := strings.CutPrefix(string(k), "area:"); ok {
			return AreaChosen{Area: area}
		}
		return ConversationChosen{ID: string(k)}
	}
	side := widget.Column(v.projectName, v.convs)
	side.Cross = widget.CrossStretch
	sideTheme := theme.Make("chat.side", theme.Set(widget.ListSpacing, 2))
	sidebar := &panel{child: widget.NewThemed(widget.NewPad(side), sideTheme), fill: chat.SidebarFill, width: sidebarW}
	v.sidebar = sidebar

	v.link = widget.NewButton("Online")
	v.link.Icon, v.link.Ghost = icon.Wifi, true
	v.link.OnClick = widget.Sends(LinkToggled{})
	themeButton := widget.NewIconButton(icon.SunMoon, "Switch theme")
	themeButton.OnClick = widget.Sends(ThemeToggled{})
	popOut := widget.NewIconButton(icon.SquareArrowOutUpRight, "Open in a window of its own")
	popOut.OnClick = widget.Sends(chat.PopOut{})
	back := widget.NewIconButton(icon.ArrowLeft, "Conversations")
	back.OnClick = func(u *gunim.UI) gunim.Intent {
		v.screens.show(false, u)
		return nil
	}
	v.screens = newScreens()
	v.chat = chat.NewView(chat.Options{
		Header:      true,
		Members:     true,
		HeaderLead:  []gunim.Node{&narrowOnly{s: v.screens, on: true, child: back}},
		HeaderTools: []gunim.Node{v.link},
		// The pop-out button is for the desktop, so it is the example's, which hides it on a phone.
		HeaderEnd: []gunim.Node{&narrowOnly{s: v.screens, child: popOut}, themeButton},
		Hints:     hints,
	})

	v.linkBar = chat.NewLinkBar()
	v.files = newFilesPane()
	v.areas = newAreas(v.chat, v.files.root)
	// The bar saying the connection is down spans every area
	right := widget.Column(v.linkBar, v.areas).Grow(v.areas, 1)
	right.Cross, right.Gap = widget.CrossStretch, zeroGap
	rightPane := &panel{child: right, fill: chat.PaneFill}
	v.screens.rail, v.screens.sidebar, v.screens.right = v.rail, sidebar, rightPane
	v.root = v.screens
	return v
}

var (
	sidebarTitle = theme.Length("chat.title.size", 16)
	zeroGap      = theme.Length("chat.gap.none", 0)
)

// set shows the state s.
func (v *chatView) set(s Chat, u *gunim.UI) {
	if !v.solo {
		v.rail.set(s.Projects, s.Project, u)
		if s.Project < len(s.Projects) {
			v.projectName.Text = s.Projects[s.Project].Name
		}
		widget.Sync(v.convs, u, s.Conversations,
			func(c Conversation) widget.Key { return widget.Key(c.ID) },
			func(c Conversation) *convRow { return newConvRow(c, chosen(s, c)) },
			func(r *convRow, c Conversation, u *gunim.UI) { r.set(c, chosen(s, c), u) })
		v.setArea(s, u)
		v.setLink(s.Link, u)
		if s.Picks != v.picks {
			// On a phone, a conversation or an area picked from the list
			// slides in over it.
			v.picks = s.Picks
			v.screens.show(true, u)
		}
	}
	v.chat.Set(s.Conversation, u)
}

// chosen reports whether c is the row the pane shows: the area open, or the conversation when none is.
func chosen(s Chat, c Conversation) bool {
	if c.Area != "" || s.Area != "" {
		return c.Area == s.Area
	}
	return c.ID == s.ID
}

// setArea shows the area of the project s says, and hands it the keyboard as it arrives.
func (v *chatView) setArea(s Chat, u *gunim.UI) {
	was := v.areas.shown
	switch s.Area {
	case "files":
		name := ""
		if s.Project < len(s.Projects) {
			name = s.Projects[s.Project].Name
		}
		v.files.set(name, s.Files, s.Conversations, u)
		v.areas.show(1, u)
		if was != 1 {
			u.Focus(v.files.grid)
		}
	default:
		v.areas.show(0, u)
		if was != 0 {
			v.chat.Focus(u)
		}
	}
}

// setLink shows the state of the connection, in the header's button and the bar under it.
func (v *chatView) setLink(l chat.Link, u *gunim.UI) {
	switch l {
	case chat.Online:
		v.link.Label = "Online"
		v.link.Icon = icon.Wifi
	case chat.Offline:
		v.link.Label = "Offline"
		v.link.Icon = icon.WifiOff
	case chat.Reconnecting:
		v.link.Label = "Connecting"
		v.link.Icon = icon.Wifi
	}
	v.linkBar.Set(l, u)
}

// Children implements [gunim.Composite].
func (v *chatView) Children() []gunim.Node { return []gunim.Node{v.root} }

// Layout implements [gunim.Node].
func (v *chatView) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	kid := kids.At(0)
	kid.Layout(gunim.Tight(c.Max))
	kid.Place(geom.Point{})
	return c.Max
}

// Paint implements [gunim.Node].
func (v *chatView) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}

// Handle implements [gunim.Handler]: typing that nothing else takes goes to the message box, and on a phone Escape
// goes back to the list.
func (v *chatView) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.TextInput, input.Composing:
		return v.chat.Handle(e, u)
	case input.KeyPress:
		// On a phone, Escape, as the system's back gesture sends it, goes
		// back from the conversation to the list, where nothing else took it.
		if e.Key == input.KeyEscape && v.screens != nil && v.screens.narrow && v.screens.showing() {
			v.screens.show(false, u)
			return true
		}
	}
	return false
}

// panel fills its box with a colour behind its child, and is width wide when width is set.
type panel struct {
	child gunim.Node
	fill  theme.Token[color.NRGBA]
	width float32
}

// Children implements [gunim.Composite].
func (p *panel) Children() []gunim.Node { return []gunim.Node{p.child} }

// Layout implements [gunim.Node].
func (p *panel) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	size := c.Max
	if p.width > 0 {
		size.W = p.width
	}
	kid := kids.At(0)
	kid.Layout(gunim.Tight(size))
	kid.Place(geom.Point{})
	return size
}

// Paint implements [gunim.Node].
func (p *panel) Paint(pt *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	pt.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(p.fill.Get(f.Theme)))
	kids.At(0).Paint(pt)
}

// fade returns c with its alpha scaled by t.
func fade(c color.NRGBA, t float32) color.NRGBA {
	c.A = uint8(float32(c.A) * max(0, min(t, 1)))
	return c
}
