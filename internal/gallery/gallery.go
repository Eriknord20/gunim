// Package gallery is the gallery of gunim's widgets that example/widgets
// shows, and that tools/kmsbench measures on a Raspberry Pi's screen: a
// header row with a search field, and a scrolling list of cards, each a
// row with a label that wraps and a button. See example/widgets.
package gallery

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/markdown"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// The vocabulary the two halves share.
type (
	// Gallery is the state the gallery view renders.
	Gallery struct{ Items []string }
	// ThemeToggled travels when the user presses the theme button.
	ThemeToggled struct{}
	// Opened travels when the user opens an item.
	Opened struct{ Item string }
	// Confirm is the state of the confirm dialog.
	Confirm struct{ Title string }
	// Closed travels when the confirm dialog closes.
	Closed struct{}
	// Filtered travels as the user types in the search field.
	Filtered struct{ Text string }
	// DeleteAsked travels when the user presses Delete notes.
	DeleteAsked struct{}
	// Danger is the state of the danger dialog.
	Danger struct{ Title string }
	// ToastsAsked travels when the user asks for the toasts again.
	ToastsAsked struct{}

	// ShareAsked travels when the user presses Share.
	ShareAsked struct{}
	// BuzzAsked travels when the user presses Buzz.
	BuzzAsked struct{}
	// ShowToasts is the patch that shows a toast of each kind.
	ShowToasts struct{}
)

func init() {
	gunim.RegisterType[Gallery]("gallery")
	gunim.RegisterType[ThemeToggled]("gallery.theme")
	gunim.RegisterType[Opened]("gallery.open")
	gunim.RegisterType[Confirm]("gallery.confirm")
	gunim.RegisterType[Closed]("gallery.closed")
	gunim.RegisterType[Filtered]("gallery.filter")
	gunim.RegisterType[DeleteAsked]("gallery.delete")
	gunim.RegisterType[Danger]("gallery.danger")
	gunim.RegisterType[ToastsAsked]("gallery.toasts")
	gunim.RegisterType[ShareAsked]("gallery.share")
	gunim.RegisterType[BuzzAsked]("gallery.buzz")
	gunim.RegisterType[ShowToasts]("gallery.toasts.show")
}

// Register is the window half: the views and the themes.
func Register(w *gunim.Window) {
	w.RegisterTheme(widget.Dark())
	w.RegisterTheme(widget.Light())

	gunim.RegisterView(w, "gallery", buildGallery,
		func(g *gallery, s Gallery, u *gunim.UI) {
			// Cards are keyed by their text, so a filter that hides some
			// collapses them and one that brings them back grows them in.
			widget.Sync(g.list, u, s.Items,
				func(item string) widget.Key { return widget.Key(item) },
				newCard, nil)
		})
	gunim.RegisterPatch(w, "gallery", func(g *gallery, _ ShowToasts, u *gunim.UI) { g.showToasts(u) })
	gunim.RegisterView(w, "confirm",
		func(s Confirm) *widget.Dialog {
			d := widget.NewDialog(s.Title)
			d.OnAccept = widget.Sends(Closed{})
			d.OnDismiss = widget.Sends(Closed{})
			return d
		}, nil)
	gunim.RegisterView(w, "danger",
		func(s Danger) *widget.Dialog {
			d := widget.NewDialog(s.Title)
			d.Danger = true
			d.SetButtons("Delete", "Cancel")
			d.OnAccept = widget.Sends(Closed{})
			d.OnDismiss = widget.Sends(Closed{})
			return d
		}, nil)
}

// gallery is the gallery view: a padded page with toasts over its bottom right, and a handle on the list of cards
// so updates can sync it.
type gallery struct {
	page   *widget.Scroll
	body   *galleryPage
	list   *widget.List
	toasts *widget.Toasts
}

// Children implements [gunim.Composite].
func (g *gallery) Children() []gunim.Node { return []gunim.Node{g.page, g.toasts} }

// Layout implements [gunim.Node]: the page fills the window, and the toasts sit at its bottom right.
func (g *gallery) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	page, toasts := kids.At(0), kids.At(1)
	m := widget.Margin.Get(f.Theme)
	g.body.view = c.Max.H - m.Top - m.Bottom
	page.Layout(gunim.Tight(c.Max))
	page.Place(geom.Point{})
	ts := toasts.Layout(gunim.Loose(geom.Sz(c.Max.W-32, c.Max.H)))
	toasts.Place(geom.Pt(c.Max.W-ts.W-16, c.Max.H-ts.H-16))
	return c.Max
}

// Paint implements [gunim.Node].
func (g *gallery) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for k := range kids.All {
		k.Paint(p)
	}
}

// showToasts shows a toast of each kind.
func (g *gallery) showToasts(u *gunim.UI) {
	for _, t := range []widget.Toast{
		{Title: "Syncing", Body: "Your notes sync in the background.", Kind: widget.ToastInfo},
		{Title: "Saved", Body: "The notes are saved.", Kind: widget.ToastSuccess},
		{Title: "Disk almost full", Kind: widget.ToastWarning},
		{Title: "Could not reach the server", Kind: widget.ToastError, Action: "Retry", OnClick: widget.Sends(ToastsAsked{})},
	} {
		g.toasts.Show(t, u)
	}
}

// buildGallery lays the gallery out: a heading, a search field and a
// theme button over a scrolling list of cards.
func buildGallery(Gallery) *gallery {
	title := widget.NewLabel("Widgets")
	title.Size = widget.HeadingSize
	search := widget.NewTextField()
	search.Placeholder = "Filter"
	search.Icon, search.Clearable = icon.Search, true
	search.OnChange = func(s string, u *gunim.UI) gunim.Intent { return Filtered{Text: s} }
	toggle := widget.NewButton("Switch theme")
	toggle.Icon = icon.SunMoon
	toggle.OnClick = widget.Sends(ThemeToggled{})
	// The heading and its tools wrap onto a second line where the window
	// is too narrow for one, as on a phone.
	header := widget.NewWrap(title, widget.NewSized(search, 220, 0), widget.NewIconButton(icon.Filter, "Filter"),
		widget.NewIconButton(icon.Columns3, "Columns"), toggle)
	header.Cross = widget.CrossCenter

	// The callout wears a theme of its own that sets only its colours,
	// so a theme switch still moves its padding and corners with the
	// rest of the page.
	callout := widget.NewThemed(
		widget.NewCard(widget.NewLabel("This card wears a theme of its own. Switch the theme: its colours stay, its padding and corners move with the page.")),
		theme.Make("callout",
			theme.Set(widget.CardFill, color.NRGBA{R: 0x2f, G: 0x6f, B: 0xe0, A: 0xff}),
			theme.Set(widget.Ink, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff})))

	notes := widget.NewTextArea()
	notes.Rows = 3
	notes.Placeholder = "Notes: several lines, wrapped to the width"

	list := widget.NewList()
	body := &galleryPage{parts: []gunim.Node{header, icons(), moreIcons(), places(), choices(), callout, notes}, list: widget.NewScroll(list)}
	return &gallery{page: widget.NewScroll(widget.NewPad(body)), body: body, list: list, toasts: widget.NewToasts()}
}

// minList is the least height the list of cards keeps, in logical
// pixels: on a screen too short for it and the cards above, the page
// scrolls.
const minList = 320

// galleryPage stacks the gallery's parts, and under them the list,
// which takes the height the window leaves it, and at least minList.
// view is the height the page shows, which the gallery sets.
type galleryPage struct {
	parts []gunim.Node
	list  gunim.Node
	view  float32
}

// Children implements [gunim.Composite].
func (p *galleryPage) Children() []gunim.Node {
	return append(append([]gunim.Node{}, p.parts...), p.list)
}

// Layout implements [gunim.Node].
func (p *galleryPage) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	w, gap := c.Max.W, widget.Gap.Get(f.Theme)
	var y float32
	for i := range p.parts {
		k := kids.At(i)
		s := k.Layout(gunim.Constraints{Min: geom.Sz(w, 0), Max: geom.Sz(w, 1e5)})
		k.Place(geom.Pt(0, y))
		y += s.H + gap
	}
	h := max(p.view-y, minList)
	list := kids.At(len(p.parts))
	list.Layout(gunim.Tight(geom.Sz(w, h)))
	list.Place(geom.Pt(0, y))
	return geom.Sz(w, y+h)
}

// Paint implements [gunim.Node].
func (p *galleryPage) Paint(pt *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for k := range kids.All {
		k.Paint(pt)
	}
}

// moreIcons lays out a card of widgets with icons: tabs, a chip, a drop-down, rich text with icons inline, buttons
// for a danger dialog and the toasts, a toolbar, and a Markdown document.
func moreIcons() *widget.Card {
	tabs := widget.NewTabs([]string{"Files", "Search", "Settings"},
		widget.NewLabel("Tabs show an icon before each title."),
		widget.NewLabel("The chosen tab's icon is drawn in the ink."),
		widget.NewLabel("The rest are dim."))
	tabs.Icons = []*icon.Icon{icon.Folder, icon.Search, icon.Settings}
	chip := widget.NewChip("", "Pictures")
	chip.Icon = icon.Image
	view := widget.NewDropdown([]widget.MenuItem{{Label: "List", Icon: icon.List}, {Label: "Grid", Icon: icon.LayoutGrid}})
	del := widget.NewButton("Delete notes")
	del.Icon, del.Kind = icon.Trash2, widget.ButtonDanger
	del.OnClick = widget.Sends(DeleteAsked{})
	again := widget.NewButton("Toasts")
	again.Icon = icon.Bell
	again.OnClick = widget.Sends(ToastsAsked{})
	tools := widget.NewToolbar(widget.NewIconButton(icon.Reply, "Reply"), widget.NewIconButton(icon.Pencil, "Edit"),
		widget.NewIconButton(icon.Trash2, "Delete"))
	share := widget.NewButton("Share")
	share.Icon = icon.Share2
	share.OnClick = widget.Sends(ShareAsked{})
	buzz := widget.NewButton("Buzz")
	buzz.Icon = icon.Vibrate
	buzz.OnClick = widget.Sends(BuzzAsked{})
	controls := widget.NewWrap(chip, view, del, again, share, buzz, tools)
	controls.Cross = widget.CrossCenter
	rich := widget.NewRichText(
		widget.RichSpan{Text: "Saved "},
		widget.RichSpan{Icon: icon.CircleCheck, Ink: widget.ToastSuccessInk},
		widget.RichSpan{Text: " to "},
		widget.RichSpan{Icon: icon.FolderOpen, Text: "Documents", OnClick: widget.Sends(Opened{Item: "Documents"})},
		widget.RichSpan{Text: ". Icons sit in rich text as words do, and one in a link is part of the link."},
	)
	doc := markdown.New("Markdown shows as **text**: *emphasis*, `code`, [links](https://example.com) and lists.\n\n" +
		"- [x] select across paragraphs\n- [ ] copy with Ctrl+C\n\n> A quote, and a line of code:\n\n" +
		"```\ngo run ./example/widgets\n```")
	col := widget.Column(tabs, controls, rich, doc)
	col.Cross = widget.CrossStretch
	return widget.NewCard(col)
}

// places lays out a card with a navigation list: a row with a count, and one that opens a window of its own.
func places() *widget.Card {
	nav := widget.NewNavList(
		widget.NavItem{Label: "Overview", Icon: icon.Home},
		widget.NavItem{Label: "Storage", Icon: icon.HardDrive},
		widget.NavItem{Label: "Can't sync", Icon: icon.CircleAlert, Count: 4, CountLabel: "4 files", CountInk: widget.ToastWarningInk},
		widget.NavItem{Label: "Time travel", Icon: icon.History, Trail: icon.ExternalLink, Acts: true},
	)
	nav.Tooltip = "Pages"
	return widget.NewCard(widget.Row(widget.NewSized(nav, 220, 0)))
}

// bigIcon is the size of the large icons in the gallery.
var bigIcon = theme.Length("gallery.icon.big", 48)

// icons lays out a card of icons: small ones, large ones that draw themselves on, a spinner, and icons in a link and
// a button that draws the large ones on again.
func icons() *widget.Card {
	row := make([]gunim.Node, 0, 16)
	for _, ic := range []*icon.Icon{icon.Database, icon.HardDrive, icon.ShieldAlert, icon.Target, icon.ScrollText,
		icon.Copy, icon.RefreshCw, icon.ChevronRight, icon.X, icon.Check} {
		row = append(row, widget.NewIcon(ic, ic.Name))
	}
	spin := widget.NewIcon(icon.Loader2, "Loading")
	spin.Spin = true
	big := make([]*widget.Icon, 0, 3)
	for _, ic := range []*icon.Icon{icon.TriangleAlert, icon.FolderOpen, icon.CircleCheck} {
		i := widget.NewIcon(ic, ic.Name)
		i.Size = bigIcon
		i.DrawOn(1200 * time.Millisecond)
		big = append(big, i)
		row = append(row, i)
	}
	again := widget.NewIconButton(icon.RotateCcw, "Draw the icons on again")
	again.OnClick = func(*gunim.UI) gunim.Intent {
		for _, i := range big {
			i.DrawOn(1200 * time.Millisecond)
		}
		return nil
	}
	link := widget.NewLink("Open folder")
	link.Icon = icon.FolderOpen
	row = append(row, spin, again, link)
	r := widget.NewWrap(row...)
	r.Cross = widget.CrossCenter
	return widget.NewCard(r)
}

// newCard makes the card for one item: its text, wrapping, and a button.
func newCard(item string) *widget.Card {
	label := widget.NewLabel(item)
	open := widget.NewButton("Open")
	open.Icon = icon.ExternalLink
	open.OnClick = widget.Sends(Opened{Item: item})
	row := widget.Row(label, open).Grow(label, 1)
	row.Cross = widget.CrossCenter
	return widget.NewCard(row)
}

// Serve is the application half: it mounts the gallery and answers what the user does until ctx ends. Given a shot
// path, it writes the window there after a while and quits.
func Serve(ctx context.Context, c gunim.Client, shot string, after time.Duration) error {
	items := make([]string, 40)
	for i := range items {
		items[i] = fmt.Sprintf("Item %d. %s", i+1, blurb[i%len(blurb)])
	}
	if err := c.Mount(gunim.Root, "gallery", "gallery", Gallery{Items: items}); err != nil {
		return err
	}
	if err := c.Patch("gallery", ShowToasts{}); err != nil {
		return err
	}
	filter := func(text string) Gallery {
		var out []string
		for _, item := range items {
			if strings.Contains(strings.ToLower(item), strings.ToLower(text)) {
				out = append(out, item)
			}
		}
		return Gallery{Items: out}
	}
	light := false
	var shoot <-chan time.Time
	if shot != "" {
		shoot = time.After(after)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-shoot:
			if err := writeShot(ctx, c, shot); err != nil {
				return err
			}
			c.Close()
			return nil
		case ev, ok := <-c.Intents():
			if !ok {
				return c.Err()
			}
			switch v := ev.Intent.(type) {
			case ThemeToggled:
				light = !light
				_ = c.SetTheme(map[bool]string{false: "dark", true: "light"}[light])
			case Filtered:
				_ = c.Update("gallery", filter(v.Text))
			case Opened:
				_ = c.Mount(gunim.Root, "confirm", "confirm", Confirm{Title: "Open " + v.Item})
				_ = c.Focus("confirm")
			case DeleteAsked:
				_ = c.Mount(gunim.Root, "danger", "danger", Danger{Title: "Delete all notes?"})
				_ = c.Focus("danger")
			case ToastsAsked:
				_ = c.Patch("gallery", ShowToasts{})
			case ShareAsked:
				if err := shareNotes(c); err != nil {
					log.Printf("widgets: share: %v", err)
				}
			case BuzzAsked:
				// Two short buzzes, a tenth of a second apart.
				if err := c.Vibrate(80*time.Millisecond, 100*time.Millisecond, 80*time.Millisecond); err != nil {
					log.Printf("widgets: buzz: %v", err)
				}
			case gunim.CommandFailed:
				log.Printf("command %s failed: %s", v.Command, v.Reason)
			}
		}
	}
}

// shareNotes writes a small note to the temporary folder and shares it,
// with a line of text, through the system's share sheet.
func shareNotes(c gunim.Client) error {
	path := filepath.Join(os.TempDir(), "gunim notes.txt")
	if err := os.WriteFile(path, []byte("Notes from the gunim widgets gallery.\n"), 0o644); err != nil {
		return err
	}
	return c.Share(driver.Share{Text: "Notes from gunim", Subject: "gunim notes", Paths: []string{path}})
}

var blurb = []string{
	"A short one.",
	"This one runs long enough that it wraps onto a second line when the window is narrow, and reflows as the window grows.",
	"Scroll with the wheel: the list glides to where the wheel sends it.",
	"Nothing here but a button.",
}

// writeShot writes what the window shows to a PNG file.
func writeShot(ctx context.Context, c gunim.Client, path string) error {
	img, err := c.Shot(ctx)
	if err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	return errors.Join(png.Encode(f, img), f.Close())
}

// choices lays out a card with a radio group: one choice of three, each with a line that explains it.
func choices() *widget.Card {
	g := widget.NewRadioGroup(
		widget.RadioItem{Label: "Put them back", Detail: "Your PCs get the old versions. The newer ones stay in the history."},
		widget.RadioItem{Label: "Copy them to a folder", Detail: "Your libraries stay as they are."},
		widget.RadioItem{Label: "Do nothing yet"},
	)
	g.Tooltip = "Restore"
	return widget.NewCard(widget.Row(widget.NewSized(g, 360, 0)))
}
