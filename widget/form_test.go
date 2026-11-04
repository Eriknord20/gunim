package widget

import (
	"slices"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/input"
)

type signIn struct {
	Host, Password string
	Remember       bool
}

// newSignInStage mounts a dialog with a form of a host, a password and
// a checkbox, and gives it the keyboard.
func newSignInStage(t *testing.T) (*gunim.Window, *Form, [3]gunim.Node, func(int)) {
	t.Helper()
	host, pass, remember := NewTextField(), NewTextField(), NewCheckbox("Remember the password")
	pass.Secret = true
	form := NewForm().Add("Host", host).Add("Password", pass).Add("", remember)
	d := NewDialog("Connect to a server")
	d.Body = form
	d.SetButtons("Connect", "Cancel")
	d.OnAccept = func(u *gunim.UI) gunim.Intent {
		return signIn{Host: host.Text(), Password: pass.Text(), Remember: remember.Checked()}
	}
	w := gunimtest.New(t, geom.Sz(800, 600), nil)
	gunim.RegisterView(w, "d", func(struct{}) gunim.Node { return d }, nil)
	if err := w.Client().Mount(gunim.Root, "d", "d", nil); err != nil {
		t.Fatal(err)
	}
	if err := w.Client().Focus("d"); err != nil {
		t.Fatal(err)
	}
	run := func(n int) {
		for range n {
			w.Frame(time.Second / 60)
		}
	}
	run(30)
	return w, form, [3]gunim.Node{host, pass, remember}, run
}

func TestAFormDialogTakesTypingAndConfirmsWithEnter(t *testing.T) {
	w, _, fields, run := newSignInStage(t)
	w.Input(input.TextInput{Text: "example.com"})
	w.Input(input.KeyPress{Key: input.KeyTab})
	w.Input(input.TextInput{Text: "hunter2"})
	w.Input(input.KeyPress{Key: input.KeyEnter})
	run(30)
	got := sent(w)
	want := signIn{Host: "example.com", Password: "hunter2"}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("intents %v, want %v", got, want)
	}
	if host, ok := fields[0].(*TextField); !ok || host.Text() != "example.com" {
		t.Fatal("the host field lost its text")
	}
}

func TestASecretFieldShowsDotsAndKeepsOffTheClipboard(t *testing.T) {
	w, _, fields, run := newSignInStage(t)
	pass, ok := fields[1].(*TextField)
	if !ok {
		t.Fatal("the second field is no text field")
	}
	w.Input(input.KeyPress{Key: input.KeyTab})
	w.Input(input.TextInput{Text: "hunter2"})
	run(1)
	dot := pass.line.face.Shape("•", pass.line.size).Glyphs[0].ID
	var glyphs []uint32
	for _, pc := range pass.line.pieces {
		for _, g := range pc.run.Glyphs {
			glyphs = append(glyphs, g.ID)
		}
	}
	if len(glyphs) != 7 || slices.ContainsFunc(glyphs, func(id uint32) bool { return id != dot }) {
		t.Fatalf("the field shows glyphs %v, want seven dots", glyphs)
	}
	_ = w.Offscreen().SetClipboard("before")
	w.Input(input.KeyPress{Key: input.KeyA, Mods: input.ModControl})
	w.Input(input.KeyPress{Key: input.KeyC, Mods: input.ModControl})
	run(1)
	if c, _ := w.Offscreen().Clipboard(); c != "before" {
		t.Fatalf("copying a secret field put %q on the clipboard", c)
	}
}

func TestTabGoesRoundTheFieldsAndButtons(t *testing.T) {
	w, _, fields, run := newSignInStage(t)
	// Host, password, checkbox, Cancel, Connect, and round to the host.
	for range 5 {
		w.Input(input.KeyPress{Key: input.KeyTab})
	}
	w.Input(input.TextInput{Text: "x"})
	run(1)
	if host, ok := fields[0].(*TextField); !ok || host.Text() != "x" {
		t.Fatal("after Tab went round, typing missed the host field")
	}
}

func TestAFormLinesItsLabelsUpAgainstItsFields(t *testing.T) {
	_, form, _, _ := newSignInStage(t)
	// Every label ends at one line, a gap short of where every field
	// starts.
	first := form.rows[0]
	for i, r := range form.rows {
		if r.labelRight != first.labelRight || r.fieldLeft != first.fieldLeft {
			t.Fatalf("row %d: label ends at %v and field starts at %v, want %v and %v",
				i, r.labelRight, r.fieldLeft, first.labelRight, first.fieldLeft)
		}
	}
	if gap := first.fieldLeft - first.labelRight; gap != FormGap.Default() {
		t.Fatalf("the gap between labels and fields is %v, want %v", gap, FormGap.Default())
	}
}

func TestADialogWithAProblemStaysOpenAndShakes(t *testing.T) {
	host := NewTextField()
	d := NewDialog("Connect to a server")
	d.Body = NewForm().Add("Host", host)
	d.OnAccept = func(u *gunim.UI) gunim.Intent { return signIn{Host: host.Text()} }
	d.Check = func() string {
		if host.Text() == "" {
			return "Say which server."
		}
		return ""
	}
	w := gunimtest.New(t, geom.Sz(800, 600), nil)
	gunim.RegisterView(w, "d", func(struct{}) gunim.Node { return d }, nil)
	if err := w.Client().Mount(gunim.Root, "d", "d", nil); err != nil {
		t.Fatal(err)
	}
	if err := w.Client().Focus("d"); err != nil {
		t.Fatal(err)
	}
	run := func(n int) {
		for range n {
			w.Frame(time.Second / 60)
		}
	}
	run(30)
	w.Input(input.KeyPress{Key: input.KeyEnter})
	run(3)
	if d.problem.Text != "Say which server." {
		t.Fatalf("the dialog says %q", d.problem.Text)
	}
	if d.shake.Value() == 0 {
		t.Fatal("the dialog kept still")
	}
	run(90)
	if got := sent(w); len(got) != 0 {
		t.Fatalf("an empty form sent %v", got)
	}
	if v := d.shake.Value(); v != 0 {
		t.Fatalf("the shake settled at %v, want 0", v)
	}
	w.Input(input.TextInput{Text: "example.com"})
	w.Input(input.KeyPress{Key: input.KeyEnter})
	run(30)
	if got := sent(w); len(got) != 1 || got[0] != (signIn{Host: "example.com"}) {
		t.Fatalf("intents %v, want the host", got)
	}
}

type answered struct{ What string }

func TestADialogsExtraButtonSendsItsAnswer(t *testing.T) {
	d := NewDialog("Replace notes.txt?")
	d.SetButtons("Replace", "Stop")
	d.OnAccept = Sends(answered{"replace"})
	d.AddButton("Leave It", func(u *gunim.UI) gunim.Intent { return answered{"leave"} })
	w := gunimtest.New(t, geom.Sz(800, 600), nil)
	gunim.RegisterView(w, "d", func(struct{}) gunim.Node { return d }, nil)
	if err := w.Client().Mount(gunim.Root, "d", "d", nil); err != nil {
		t.Fatal(err)
	}
	if err := w.Client().Focus("d"); err != nil {
		t.Fatal(err)
	}
	run := func(n int) {
		for range n {
			w.Frame(time.Second / 60)
		}
	}
	run(30)
	// Tab goes to the first button, Leave It, then Enter presses it.
	w.Input(input.KeyPress{Key: input.KeyTab})
	w.Input(input.KeyPress{Key: input.KeyEnter})
	run(30)
	if got := sent(w); len(got) != 1 || got[0] != (answered{"leave"}) {
		t.Fatalf("intents %v, want leave", got)
	}
}

// With DefaultFirst, Tab from the fields reaches OK before an extra
// button, so Tab then Enter does what Enter alone does.
func TestADefaultFirstDialogTabsToOKFirst(t *testing.T) {
	d := NewDialog("Replace notes.txt?")
	d.SetButtons("Leave It", "Stop")
	d.OnAccept = Sends(answered{"leave"})
	d.AddButton("Replace", func(u *gunim.UI) gunim.Intent { return answered{"replace"} })
	d.DefaultFirst = true
	w := gunimtest.New(t, geom.Sz(800, 600), nil)
	gunim.RegisterView(w, "d", func(struct{}) gunim.Node { return d }, nil)
	if err := w.Client().Mount(gunim.Root, "d", "d", nil); err != nil {
		t.Fatal(err)
	}
	if err := w.Client().Focus("d"); err != nil {
		t.Fatal(err)
	}
	run := func(n int) {
		for range n {
			w.Frame(time.Second / 60)
		}
	}
	run(30)
	w.Input(input.KeyPress{Key: input.KeyTab})
	w.Input(input.KeyPress{Key: input.KeyEnter})
	run(30)
	if got := sent(w); len(got) != 1 || got[0] != (answered{"leave"}) {
		t.Fatalf("intents %v, want leave", got)
	}
}

// A label stands level with its field's first line: beside a field as tall as a control, at its middle; beside a tall
// field, as a column of controls, at its top, not halfway down.
func TestAFormLabelStandsLevelWithItsFieldsFirstLine(t *testing.T) {
	short := NewTextField()
	tall := Column(NewCheckbox("One"), NewCheckbox("Two"), NewCheckbox("Three"), NewCheckbox("Four"))
	form := NewForm().Add("Name", short).Add("Options", tall)
	w, run := stage(t, &frame{child: form, size: geom.Sz(500, 300)})
	run(10)
	u := stageUI(t, w, run)
	mid := func(n gunim.Node) float32 {
		r, ok := u.Bounds(n)
		if !ok {
			t.Fatal("a part of the form does not show")
		}
		return r.Center().Y
	}
	if d := mid(form.rows[0].label) - mid(short); d < -1 || d > 1 {
		t.Errorf("the label stands %v off its field's middle", d)
	}
	first, ok := u.Bounds(tall.Children()[0])
	if !ok {
		t.Fatal("the column does not show")
	}
	if d := mid(form.rows[1].label) - first.Center().Y; d < -2 || d > 2 {
		t.Errorf("the label beside a tall field stands %v off its first line", d)
	}
}

// Check's words fade in as the panel grows to hold them, frame by frame.
// Other words fade the last ones out first, and the panel holds its height
// as they change.
func TestADialogsProblemGlidesIn(t *testing.T) {
	host := NewTextField()
	d := NewDialog("Connect to a server")
	d.Body = NewForm().Add("Host", host)
	say := "Say which server."
	d.Check = func() string { return say }
	w := gunimtest.New(t, geom.Sz(800, 600), nil)
	gunim.RegisterView(w, "d", func(struct{}) gunim.Node { return d }, nil)
	if err := w.Client().Mount(gunim.Root, "d", "d", nil); err != nil {
		t.Fatal(err)
	}
	if err := w.Client().Focus("d"); err != nil {
		t.Fatal(err)
	}
	run := func(n int) {
		for range n {
			w.Frame(time.Second / 60)
		}
	}
	run(120)
	before := d.height

	w.Input(input.KeyPress{Key: input.KeyEnter})
	heights := make([]float32, 0, 90)
	sawFade := false
	for i := range 90 {
		w.Frame(time.Second / 60)
		heights = append(heights, d.height)
		if i == 0 && d.problemClear != 0 {
			t.Fatalf("the words show over the buttons, cleared %v, before the panel has grown", d.problemClear)
		}
		if v := d.problemIn.Value(); v > 0.1 && v < 0.9 {
			sawFade = true
		}
	}
	after := d.height
	if after <= before {
		t.Fatalf("the panel stayed %v tall with words to hold, was %v", after, before)
	}
	if d.problemClear != 1 {
		t.Fatalf("the words stopped short of clearing the buttons: %v", d.problemClear)
	}
	if !sawFade {
		t.Fatal("the words never showed part way faded in")
	}
	between, last := false, before
	for _, h := range heights {
		if h > before+1 && h < after-1 {
			between = true
		}
		if h-last > (after-before)*0.6 {
			t.Fatalf("the panel jumped from %v to %v on the way to %v", last, h, after)
		}
		last = h
	}
	if !between {
		t.Fatalf("the panel never stood between %v and %v: %v", before, after, heights)
	}

	// Other words, of the same height: the last fade out, then the new in.
	say = "That server is not known."
	w.Input(input.KeyPress{Key: input.KeyEnter})
	run(1)
	if d.problem.Text != "Say which server." {
		t.Fatalf("the words changed at once, to %q", d.problem.Text)
	}
	for range 90 {
		w.Frame(time.Second / 60)
		if d.height < after-1 || d.height > after+1 {
			t.Fatalf("the panel moved to %v, from %v, as the words changed", d.height, after)
		}
	}
	if d.problem.Text != say || d.problemIn.Value() != 1 {
		t.Fatalf("the dialog says %q, faded in to %v", d.problem.Text, d.problemIn.Value())
	}
}
