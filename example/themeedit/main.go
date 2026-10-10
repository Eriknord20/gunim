// Command themeedit shows the theme editor in a window of its own. Its
// first tab picks out the cursor, which glides or jumps, a few motions,
// the accent colour and the gap between things; All values lists every
// token. The preview beside the controls wears every edit at once, and
// plays a motion as it changes.
//
// With -file, the editor starts from the values in that file, saves
// each edit to it, and Import and Export read and write it.
//
//	CGO_ENABLED=0 go run ./example/themeedit
//	CGO_ENABLED=0 go run ./example/themeedit -file mytheme.json
//
// With -shot, it writes the window to a PNG file after -after and
// quits. The other flags set the editor up for the picture first:
//
//	CGO_ENABLED=0 go run ./example/themeedit -shot all.png -tab all -filter changed -changes
//
// With -drag, the picture catches a number field mid-drag: the pointer
// presses at a point and is held some pixels up from it.
//
//	CGO_ENABLED=0 go run ./example/themeedit -shot drag.png -tab all -search padding -drag 700,200,24
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"image/color"
	"image/png"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/themeedit"
	"github.com/marrasen/gunim/widget"
)

// The vocabulary the two halves share.
type (
	// Page is the state the page renders: the values saved, as JSON,
	// and whether the edits lie over the light theme.
	Page struct {
		Saved []byte
		Light bool
	}
	// Saved travels as an edit ends, with the values changed as JSON.
	Saved struct{ Data []byte }
	// ImportAsked travels when the user picks Import.
	ImportAsked struct{}
	// Imported is the patch that hands the editor the file's values.
	Imported struct{ Data []byte }
	// Setup is the patch that sets the editor up for a picture: the tab,
	// the search, the filter, a picker to open and a motion to play.
	Setup struct {
		Tab, Search, Filter, Picker, Play string
	}
)

func init() {
	gunim.RegisterType[Page]("themeedit.page")
	gunim.RegisterType[Saved]("themeedit.saved")
	gunim.RegisterType[ImportAsked]("themeedit.import")
	gunim.RegisterType[Imported]("themeedit.imported")
	gunim.RegisterType[Setup]("themeedit.setup")
}

// options are the command's flags.
type options struct {
	runFor, after time.Duration
	file, shot    string
	size          geom.Size
	light         bool
	changes       bool
	setup         Setup
	// drag, when set, is where a press goes down for the shot, and
	// dragUp how far up from there the pointer is then held.
	drag   *geom.Point
	dragUp float32
}

func main() {
	var o options
	var size string
	flag.DurationVar(&o.runFor, "for", 0, "quit after this long; zero runs until the window closes")
	flag.StringVar(&o.file, "file", "", "a file to read the theme's values from and save them to")
	flag.StringVar(&o.shot, "shot", "", "write the window to this PNG file after -after, and quit")
	flag.DurationVar(&o.after, "after", 2*time.Second, "how long -shot waits")
	flag.StringVar(&size, "size", "1100x700", "the window's size, as WxH")
	flag.BoolVar(&o.light, "light", false, "lay the edits over the light theme")
	flag.BoolVar(&o.changes, "changes", false, "start with a few values changed: the accent, the gap, settling, a custom bounce")
	flag.StringVar(&o.setup.Tab, "tab", "", "the tab to show: basics or all")
	flag.StringVar(&o.setup.Search, "search", "", "search All values for this")
	flag.StringVar(&o.setup.Filter, "filter", "", "filter All values: all, changed, colours, sizes, motion or other")
	flag.StringVar(&o.setup.Picker, "picker", "", "open the colour picker of this key")
	flag.StringVar(&o.setup.Play, "play", "", "play the motion of this key in the preview")
	var drag string
	flag.StringVar(&drag, "drag", "", "press at X,Y and hold the pointer DY pixels up from there, as X,Y,DY, for a shot of a number field mid-drag")
	flag.Parse()
	if _, err := fmt.Sscanf(size, "%fx%f", &o.size.W, &o.size.H); err != nil {
		log.Fatalf("-size %q: want WxH, such as 1100x700", size)
	}
	if drag != "" {
		var at geom.Point
		if _, err := fmt.Sscanf(drag, "%f,%f,%f", &at.X, &at.Y, &o.dragUp); err != nil {
			log.Fatalf("-drag %q: want X,Y,DY, such as 700,200,24", drag)
		}
		o.drag = &at
	}
	if err := run(o); err != nil {
		log.Fatal(err)
	}
}

func run(o options) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if o.runFor > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.runFor)
		defer cancel()
	}
	return gunim.Main(ctx, func(a *gunim.App) error {
		w, err := a.NewWindow(gunim.WindowOptions{
			Title: "gunim theme editor",
			Size:  o.size,
			Root:  widget.NewSurface(),
		})
		if err != nil {
			return err
		}
		w.RegisterTheme(widget.Dark())
		w.RegisterTheme(widget.Light())
		gunim.RegisterView(w, "page", buildPage, nil)
		gunim.RegisterPatch(w, "page", func(p *page, im Imported, u *gunim.UI) {
			if err := p.editor.Import(im.Data, u); err != nil {
				log.Print(err)
			}
		})
		gunim.RegisterPatch(w, "page", func(p *page, s Setup, u *gunim.UI) { p.setup(s, u) })
		return serve(ctx, w.Client(), o)
	})
}

// page is the editor, filling the window.
type page struct {
	*widget.Pad
	editor *themeedit.Editor
}

// sections are what the first tab picks out.
var sections = []themeedit.Section{
	{Title: "Terminal", Fields: []themeedit.Field{{
		Key:    widget.Caret.Key(),
		Label:  "Cursor",
		Detail: "How the cursor moves to the next place on its line.",
		Presets: []themeedit.Preset{
			{Label: "Glides", Value: widget.Caret.Default()},
			{Label: "Jumps", Value: themeedit.Instant},
		},
	}}},
	{Title: "Motion", Fields: []themeedit.Field{
		{Key: widget.Quick.Key(), Label: "Quick moves", Detail: "Buttons, switches and highlights as they answer the pointer."},
		{Key: widget.Settle.Key(), Label: "Settling", Detail: "Panels, tabs and panes as they open, close and move."},
		{Key: widget.Bounce.Key(), Label: "Bounces", Detail: "What springs into place, such as a toast."},
		{Key: theme.Switch.Key(), Label: "Theme change", Detail: "Every colour as the window takes another theme."},
	}},
	{Title: "Look", Fields: []themeedit.Field{
		{Key: widget.Accent.Key(), Label: "Accent", Detail: "The colour of focus, selection and the chosen tab."},
		{Key: widget.Background.Key(), Label: "Ground", Detail: "Behind the window's own parts."},
		{Key: widget.Gap.Key(), Label: "Gap", Detail: "The room between things side by side.", Min: 0, Max: 32},
		{Key: widget.TextSize.Key(), Label: "Text size", Detail: "The window's own words.", Min: 10, Max: 22},
	}},
}

func buildPage(s Page) *page {
	base := widget.Dark()
	if s.Light {
		base = widget.Light()
	}
	// The editor starts from the base theme, with what was saved over it.
	over, err := theme.UnmarshalValues(theme.Make("mine"), s.Saved)
	if err != nil && len(s.Saved) > 0 {
		log.Print(err)
	}
	ed := themeedit.New(themeedit.Options{
		Base:      base,
		Overrides: over,
		Sections:  sections,
		OnChange: func(th theme.Theme, u *gunim.UI) gunim.Intent {
			u.UseTheme(th)
			return nil
		},
		OnCommit: func(over theme.Theme, u *gunim.UI) gunim.Intent {
			data, err := theme.MarshalValues(over)
			if err != nil {
				return nil
			}
			return Saved{Data: data}
		},
		OnExport: func(data []byte, u *gunim.UI) gunim.Intent { return Saved{Data: data} },
		OnImport: widget.Sends(ImportAsked{}),
	})
	pad := widget.NewPad(ed)
	pad.Padding = noPadding
	return &page{Pad: pad, editor: ed}
}

// noPadding leaves the editor the whole window.
var noPadding = theme.Insets("example.themeedit.padding", geom.Insets{})

// setup sets the editor up as s says.
func (p *page) setup(s Setup, u *gunim.UI) {
	ed := p.editor
	if s.Tab == "all" || s.Search != "" || s.Filter != "" {
		ed.ShowAllValues(u)
	}
	if s.Search != "" {
		ed.Search(s.Search, u)
	}
	for i := themeedit.FilterAll; i <= themeedit.FilterOther; i++ {
		if strings.EqualFold(i.String(), s.Filter) {
			ed.SetFilter(i, u)
		}
	}
	if s.Picker != "" {
		key := s.Picker
		u.After(300*time.Millisecond, func(u *gunim.UI) {
			if !ed.OpenPicker(key, u) {
				log.Printf("no colour picker for %q in the tab showing", key)
			}
		})
	}
	if s.Play != "" {
		key := s.Play
		u.After(500*time.Millisecond, func(u *gunim.UI) { ed.Play(key, u) })
	}
}

// changes are the values -changes starts with.
func changes() []byte {
	over := theme.Make("mine",
		theme.Set(widget.Accent, color.NRGBA{R: 0xe8, G: 0x8a, B: 0x3c, A: 0xff}),
		theme.Set(widget.Gap, 12),
		theme.Set(widget.Settle, anim.Snappy),
		theme.Set(widget.Bounce, anim.Spring{Response: 0.5, Damping: 0.6}),
		theme.Set(widget.ButtonRadius, 14),
	)
	data, _ := theme.MarshalValues(over)
	return data
}

func serve(ctx context.Context, c gunim.Client, o options) error {
	var saved []byte
	if o.file != "" {
		data, err := os.ReadFile(o.file)
		switch {
		case err == nil:
			saved = data
		case !errors.Is(err, fs.ErrNotExist):
			return err
		}
	}
	if o.changes {
		saved = changes()
	}
	if o.light {
		_ = c.SetTheme("light")
	}
	if err := c.Mount(gunim.Root, "page", "page", Page{Saved: saved, Light: o.light}); err != nil {
		return err
	}
	if o.setup != (Setup{}) {
		if err := c.Patch("page", o.setup); err != nil {
			return err
		}
	}
	var shoot, drag <-chan time.Time
	if o.shot != "" {
		shoot = time.After(o.after)
	}
	if o.drag != nil {
		// Half way to the shot, so the field is laid out and has done
		// moving by the time it is taken.
		drag = time.After(o.after / 2)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-drag:
			if err := hold(ctx, c, *o.drag, o.dragUp); err != nil {
				return err
			}
		case <-shoot:
			if err := writeShot(ctx, c, o.shot); err != nil {
				return err
			}
			c.Close()
			return nil
		case ev, ok := <-c.Intents():
			if !ok {
				return c.Err()
			}
			switch v := ev.Intent.(type) {
			case Saved:
				if o.file == "" {
					continue
				}
				if err := os.WriteFile(o.file, v.Data, 0o600); err != nil {
					log.Print(err)
				}
			case ImportAsked:
				if o.file == "" {
					log.Print("Import reads the file -file names; none was given")
					continue
				}
				data, err := os.ReadFile(o.file)
				if err != nil {
					log.Print(err)
					continue
				}
				if err := c.Patch("page", Imported{Data: data}); err != nil {
					return fmt.Errorf("importing: %w", err)
				}
			}
		}
	}
}

// hold presses the primary button at p and moves the pointer up from
// there in a few steps, leaving it held.
func hold(ctx context.Context, c gunim.Client, p geom.Point, up float32) error {
	evs := []input.Event{
		input.PointerMove{Pos: p, Time: time.Now()},
		input.PointerDown{Pos: p, Button: input.ButtonPrimary, Clicks: 1, Time: time.Now()},
	}
	for i := float32(1); i <= 4; i++ {
		evs = append(evs, input.PointerMove{Pos: p.Add(geom.Pt(0, -up*i/4)), Time: time.Now()})
	}
	for _, ev := range evs {
		if err := c.Input(ctx, ev); err != nil {
			return err
		}
	}
	return nil
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
