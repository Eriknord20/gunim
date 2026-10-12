package main

import (
	"strconv"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/markdown"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// The page's vocabulary: what its code editor and its buttons tell the
// application, and what the application tells the page back.
type (
	// Edited travels as the lesson's code is edited.
	Edited struct {
		Lesson int
		Source string
	}
	// RunAsked travels when Run is pressed, StopAsked when Stop is,
	// FormatAsked when Format is, and ResetAsked when Reset is.
	RunAsked    struct{ Lesson int }
	StopAsked   struct{}
	FormatAsked struct{ Lesson int }
	ResetAsked  struct{ Lesson int }

	// Code is a patch that puts new code in the editor, as one step of
	// undo: formatted, put back as it was, or as edited before.
	Code struct{ Source string }
	// RunState is a patch that shows how the lesson's last run went:
	// its status, what it wrote, and the marks on the code.
	RunState struct {
		Status  string
		Running bool
		Failed  bool
		Output  string
		Marks   []widget.CodeMark
	}
)

func init() {
	gunim.RegisterType[Edited]("tutorial.edited")
	gunim.RegisterType[RunAsked]("tutorial.run")
	gunim.RegisterType[StopAsked]("tutorial.stop")
	gunim.RegisterType[FormatAsked]("tutorial.format")
	gunim.RegisterType[ResetAsked]("tutorial.reset-code")
	gunim.RegisterType[Code]("tutorial.code")
	gunim.RegisterType[RunState]("tutorial.run-state")
}

// paged is a lesson's view root: a page, or a node built on one.
type paged interface {
	gunim.Node
	thePage() *page
}

// registerPagePatches wires the page's patches to a lesson's view.
// Every lesson's root is built on a page, so one function serves them
// all.
func registerPagePatches(w *gunim.Window, view string) {
	gunim.RegisterPatch(w, view, func(n paged, c Code, u *gunim.UI) { n.thePage().code.Replace(c.Source, u) })
	gunim.RegisterPatch(w, view, func(n paged, r RunState, u *gunim.UI) { n.thePage().showRun(r, u) })
}

// page is what every lesson sits on: a title, a few paragraphs, the
// lesson's demo in a card, and the lesson's source in an editor, with
// buttons to run it and a panel for what it writes. It fades and
// slides in as it enters, and out as it leaves, which is the whole of
// what a Transitioner does.
type page struct {
	anim.Group
	in   *anim.Float
	body gunim.Node

	code   *widget.CodeEditor
	output *widget.CodeEditor
	panel  *reveal
	status *widget.Label
	run    *widget.Button
	stop   *widget.Button
}

func (p *page) thePage() *page { return p }

// lessonFile names lesson i's file. It is apart from the lessons so a
// page can name its file while the lessons are being built.
func lessonFile(i int) string { return "lesson" + strconv.Itoa(i+1) + ".go" }

// newPage lays lesson i out. source is the lesson's file, in an editor
// the reader can change and run.
func newPage(i int, intro string, demo gunim.Node, source string) *page {
	heading := widget.NewLabel(lessonTitles[i])
	heading.Size, heading.Color = widget.HeadingSize, hue(i)

	p := &page{in: anim.NewFloat(0)}
	p.Add(p.in)

	// The editor holds the lesson's own file. Each edit goes to the
	// application, which keeps it, so it survives a trip to another
	// lesson and back.
	p.code = widget.NewCodeEditor()
	p.code.Label = lessonFile(i)
	p.code.SetText(source, nil)
	p.code.OnChange = func(s string, u *gunim.UI) gunim.Intent { return Edited{Lesson: i, Source: s} }

	title := widget.NewLabel(lessonFile(i))
	title.Size = widget.HeadingSize
	p.status = widget.NewLabel("")
	p.status.Color = widget.Placeholder
	p.run = widget.NewButton("Run")
	p.run.Icon, p.run.Kind = icon.Play, widget.ButtonPrimary
	p.run.OnClick = widget.Sends(RunAsked{Lesson: i})
	p.run.Tooltip = "Build the tutorial with this file as it is now, and open it on this lesson"
	p.stop = widget.NewButton("Stop")
	p.stop.Icon, p.stop.Disabled = icon.Square, true
	p.stop.OnClick = widget.Sends(StopAsked{})
	format := widget.NewButton("Format")
	format.Icon, format.Tooltip = icon.WandSparkles, "Format the code as gofmt does"
	format.OnClick = widget.Sends(FormatAsked{Lesson: i})
	reset := widget.NewButton("Revert")
	reset.Icon, reset.Tooltip = icon.RotateCcw, "Put the file back as it was; Ctrl+Z brings your edits back"
	reset.OnClick = widget.Sends(ResetAsked{Lesson: i})
	spacer := widget.NewSpacer()
	bar := widget.Row(title, p.status, spacer, format, reset, p.stop, p.run).Grow(spacer, 1)
	bar.Cross = widget.CrossCenter

	// The output is a read only editor with no line numbers. It slides
	// open the first time there is something in it.
	p.output = widget.NewCodeEditor()
	p.output.Highlight, p.output.Numbers, p.output.Label = nil, false, "Output"
	p.output.SetReadOnly(true)
	p.panel = newReveal(p.output, 130)

	col := widget.Column(heading, markdown.New(intro), widget.NewCard(demo), bar, p.code, p.panel).Grow(p.code, 1)
	col.Cross = widget.CrossStretch
	p.body = widget.NewPad(col)
	return p
}

// showRun shows how the lesson's last run went.
func (p *page) showRun(r RunState, u *gunim.UI) {
	p.status.Text = r.Status
	switch {
	case r.Failed:
		p.status.Color = widget.CodeProblem
	case r.Running:
		p.status.Color = widget.Accent
	default:
		p.status.Color = widget.Placeholder
	}
	p.stop.Disabled = !r.Running
	p.code.SetMarks(r.Marks, u)
	if r.Failed && len(r.Marks) > 0 {
		// Take the reader to the first error.
		p.code.GoTo(r.Marks[0].Line, r.Marks[0].Col, u)
	}
	p.output.Replace(r.Output, u)
	// Keep the end of the output in view as it grows.
	p.output.GoTo(1<<30, 1, u)
	p.panel.Open(r.Output != "", u)
	u.Invalidate()
}

// Children implements [gunim.Composite].
func (p *page) Children() []gunim.Node { return []gunim.Node{p.body} }

// Transition implements [gunim.Transitioner]. The engine calls it every
// frame while the page is entering or leaving. Animate ignores a
// target it is already heading for, so calling it again is free, and
// the page reports settled once the value comes to rest.
func (p *page) Transition(pr gunim.Presence, f gunim.Frame) bool {
	switch pr {
	case gunim.Entering:
		p.in.Animate(1, widget.Settle.Get(f.Theme))
	case gunim.Exiting:
		p.in.Animate(0, widget.Quick.Get(f.Theme))
	default:
		// A page that is simply there, or simply gone, animates nothing.
	}
	return !p.in.Active()
}

// Layout implements [gunim.Node].
func (p *page) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	kids.At(0).Layout(gunim.Tight(c.Max))
	kids.At(0).Place(geom.Point{})
	return c.Max
}

// Paint implements [gunim.Node]: the page draws in a layer whose
// opacity is how far in it is, shifted down and shrunk a little by the
// rest of the way, so it settles into place.
func (p *page) Paint(pt *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	in := min(max(p.in.Value(), 0), 1)
	defer pt.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: in})()
	defer pt.Push(paint.Translate(geom.Pt(0, 32*(1-in))))()
	defer pt.Push(paint.Scale(0.96+0.04*in, geom.Pt(box.W/2, box.H/2)))()
	kids.At(0).Paint(pt)
}

// reveal shows its child at a fixed height, opening and closing on a
// spring. Closed, it takes no room, and the node above it grows into
// the space.
type reveal struct {
	anim.Group
	open   *anim.Float
	child  gunim.Node
	height float32
}

func newReveal(child gunim.Node, height float32) *reveal {
	r := &reveal{open: anim.NewFloat(0), child: child, height: height}
	r.Add(r.open)
	return r
}

// Open opens or closes the reveal.
func (r *reveal) Open(on bool, u *gunim.UI) {
	to := float32(0)
	if on {
		to = 1
	}
	r.open.Animate(to, widget.Settle.Get(u.Theme()))
}

// Children implements [gunim.Composite].
func (r *reveal) Children() []gunim.Node { return []gunim.Node{r.child} }

// Layout implements [gunim.Node]: the child at its full height, and
// the reveal as tall as it is open.
func (r *reveal) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	kids.At(0).Layout(gunim.Tight(geom.Sz(c.Max.W, r.height)))
	kids.At(0).Place(geom.Point{})
	return geom.Sz(c.Max.W, r.height*max(r.open.Value(), 0))
}

// Paint implements [gunim.Node]: the child, clipped to the part open,
// fading as it closes.
func (r *reveal) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	open := min(max(r.open.Value(), 0), 1)
	if open <= 0 {
		return
	}
	defer p.Layer(paint.LayerOpts{Bounds: geom.Rect{Max: box.Point()}, Opacity: open, Clip: true})()
	kids.At(0).Paint(p)
}
