// Command openfiles shows the files it is given: those another application opens with it, and those chosen with
// its button. It says what each holds, so a run on a phone shows the whole way a file takes to a program.
//
//	go run ./tools/gunimapk -run -name "Open files" -opens .hej ./example/openfiles
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// The intents the view raises.
type (
	// chooseIntent is the button clicked.
	chooseIntent struct{}
	// openedIntent is files another application opened with this one.
	openedIntent struct{ paths []string }
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err := gunim.Main(ctx, func(a *gunim.App) error {
		w, werr := a.NewWindow(gunim.WindowOptions{Title: "gunim open files", Size: geom.Sz(420, 640), Root: widget.NewSurface()})
		if werr != nil {
			return werr
		}
		gunim.RegisterView(w, "opener", func(string) *opener { return newOpener() }, func(v *opener, s string, _ *gunim.UI) {
			v.list.Text = s
		})
		c := w.Client()
		lines := "No files yet."
		if merr := c.Mount(gunim.Root, "opener", "opener", lines); merr != nil {
			return merr
		}
		show := func(from string, paths []string, err error) {
			text := describe(from, paths, err)
			log.Print(text)
			if lines == "No files yet." {
				lines = ""
			}
			lines = text + "\n" + lines
			_ = c.Update("opener", lines)
		}
		chosen := make(chan func(), 1)
		for {
			select {
			case e, ok := <-c.Intents():
				if !ok {
					return c.Err()
				}
				switch in := e.Intent.(type) {
				case openedIntent:
					show("opened", in.paths, nil)
				case chooseIntent:
					// The dialog waits on the user, and files opened meanwhile do not.
					go func() {
						paths, err := c.ChooseFiles(ctx, driver.ChooseOptions{Title: "Choose files", Multiple: true})
						chosen <- func() { show("chosen", paths, err) }
					}()
				}
			case f := <-chosen:
				f()
			}
		}
	})
	if errors.Is(err, driver.ErrNoDriver) {
		log.Print("gunim has no driver for this operating system yet")
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// describe says what came, by which way, and what each file holds.
func describe(from string, paths []string, err error) string {
	var b strings.Builder
	if err != nil {
		fmt.Fprintf(&b, "%s: %v\n", from, err)
	}
	if len(paths) == 0 && err == nil {
		fmt.Fprintf(&b, "%s: nothing\n", from)
	}
	for _, p := range paths {
		fmt.Fprintf(&b, "%s: %s\n", from, describeFile(p))
	}
	return strings.TrimRight(b.String(), "\n")
}

// describeFile gives a file's name and size and the start of what it holds.
func describeFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return err.Error()
	}
	head := data[:min(len(data), 40)]
	if line, _, ok := bytes.Cut(head, []byte("\n")); ok {
		head = line
	}
	if !utf8.Valid(head) {
		head = []byte("(binary)")
	}
	return fmt.Sprintf("%s, %d bytes: %s", filepath.Base(path), len(data), head)
}

// opener is a button over a list of the files given so far.
type opener struct {
	button *widget.Button
	list   *widget.Label
}

func newOpener() *opener {
	b := widget.NewButton("Choose files")
	b.OnClick = widget.Sends(chooseIntent{})
	return &opener{button: b, list: widget.NewLabel("")}
}

// Children implements [gunim.Composite].
func (v *opener) Children() []gunim.Node { return []gunim.Node{v.button, v.list} }

// Handle implements [gunim.Handler]: files another application opened go to the application.
func (v *opener) Handle(e input.Event, u *gunim.UI) bool {
	if o, ok := e.(input.FilesOpened); ok {
		u.Send(v, openedIntent{paths: o.Paths})
		return true
	}
	return false
}

// Layout implements [gunim.Node]: the button on top and the list filling the rest. The surface under the view keeps it
// clear of the system's bars.
func (v *opener) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	box := c.Max
	const margin, buttonH = 16, 48
	w := box.W - 2*margin
	kids.At(0).Layout(gunim.Tight(geom.Sz(w, buttonH)))
	kids.At(0).Place(geom.Pt(margin, margin))
	y := float32(margin + buttonH + margin)
	kids.At(1).Layout(gunim.Tight(geom.Sz(w, max(box.H-y-margin, 0))))
	kids.At(1).Place(geom.Pt(margin, y))
	return box
}

// Paint implements [gunim.Node].
func (v *opener) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	for kid := range kids.All {
		kid.Paint(p)
	}
}
