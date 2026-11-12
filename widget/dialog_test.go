package widget

import (
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/gunim/input"
)

func TestDialogAnimatesInAndOut(t *testing.T) {
	w := gunimtest.New(t, geom.Sz(800, 600), nil)
	w.PaintWhenLooked(false)
	var d *Dialog
	gunim.RegisterView(w, "confirm", func(title string) *Dialog {
		d = NewDialog(title)
		return d
	}, nil)
	c := w.Client()
	if err := c.Mount(gunim.Root, "confirm", "confirm", "Delete?"); err != nil {
		t.Fatal(err)
	}
	step := func(n int) {
		for range n {
			w.Frame(time.Second / 60)
		}
	}

	step(3)
	if v := d.in.Value(); v <= 0 {
		t.Fatalf("three frames after mounting, in = %v; the dialog is still invisible", v)
	}
	step(120)
	if v := d.in.Value(); v != 1 {
		t.Fatalf("once settled, in = %v, want 1", v)
	}

	if err := c.Unmount("confirm"); err != nil {
		t.Fatal(err)
	}
	step(3)
	if v := d.in.Value(); v <= 0 || v >= 1 {
		t.Fatalf("three frames into the exit, in = %v, want part way out", v)
	}
	step(120)
	if v := d.in.Value(); v != 0 {
		t.Fatalf("once gone, in = %v, want 0", v)
	}
}

func TestADangerButtonFadesInFromThePlainColours(t *testing.T) {
	w := gunimtest.New(t, geom.Sz(800, 600), nil)
	w.PaintWhenLooked(false)
	var d *Dialog
	gunim.RegisterView(w, "confirm", func(title string) *Dialog {
		d = NewDialog(title)
		d.Danger = true
		return d
	}, nil)
	if err := w.Client().Mount(gunim.Root, "confirm", "confirm", "Delete?"); err != nil {
		t.Fatal(err)
	}
	w.Frame(time.Second / 60)
	b := d.ok
	if b.was != ButtonPlain || b.is != ButtonDanger {
		t.Fatalf("the button fades from kind %d to %d, want plain to danger", b.was, b.is)
	}
	last := b.tone.Value()
	if last > 0.2 {
		t.Fatalf("one frame in, the button is %v of the way to red", last)
	}
	for i := range 40 {
		w.Frame(time.Second / 60)
		v := b.tone.Value()
		if v < last || v > 1 {
			t.Fatalf("frame %d: the tone went from %v to %v", i, last, v)
		}
		last = v
	}
	if last != 1 {
		t.Fatalf("settled, the button is %v of the way to red", last)
	}
}

func TestADangerDialogOpensOnCancelSoEnterCancels(t *testing.T) {
	w := gunimtest.New(t, geom.Sz(800, 600), nil)
	w.PaintWhenLooked(false)
	var d *Dialog
	gunim.RegisterView(w, "confirm", func(title string) *Dialog {
		d = NewDialog(title)
		d.Danger = true
		d.Body = NewForm().Add("Name", NewTextField())
		d.OnAccept = Sends("delete")
		d.OnDismiss = Sends("keep")
		return d
	}, nil)
	c := w.Client()
	if err := c.Mount(gunim.Root, "confirm", "confirm", "Delete?"); err != nil {
		t.Fatal(err)
	}
	if err := c.Focus("confirm"); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		w.Frame(time.Second / 60)
	}
	w.Input(input.KeyPress{Key: input.KeyEnter})
	w.Frame(time.Second / 60)
	select {
	case env := <-c.Intents():
		if env.Intent != "keep" {
			t.Fatalf("Enter on a danger dialog sent %v, want keep", env.Intent)
		}
	case <-time.After(time.Second):
		t.Fatal("Enter on a danger dialog sent nothing")
	}
}

func TestADialogWithNoCancelHasOKAlone(t *testing.T) {
	w := gunimtest.New(t, geom.Sz(800, 600), nil)
	w.PaintWhenLooked(false)
	var d *Dialog
	gunim.RegisterView(w, "note", func(title string) *Dialog {
		d = NewDialog(title)
		d.Body = NewLabel("Written down.")
		d.SetButtons("Done", "")
		d.OnAccept = Sends("done")
		return d
	}, nil)
	c := w.Client()
	if err := c.Mount(gunim.Root, "note", "note", "A note"); err != nil {
		t.Fatal(err)
	}
	if err := c.Focus("note"); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		w.Frame(time.Second / 60)
	}
	for _, n := range d.Children() {
		if n == d.cancel {
			t.Fatal("with no cancel label, the dialog still has Cancel")
		}
	}
	w.Input(input.KeyPress{Key: input.KeyEnter})
	w.Frame(time.Second / 60)
	select {
	case env := <-c.Intents():
		if env.Intent != "done" {
			t.Fatalf("Enter sent %v, want done", env.Intent)
		}
	case <-time.After(time.Second):
		t.Fatal("Enter sent nothing")
	}
}

func TestAnActionLeavesTheDialogOpen(t *testing.T) {
	w := gunimtest.New(t, geom.Sz(800, 600), nil)
	w.PaintWhenLooked(false)
	var d *Dialog
	var box *Checkbox
	field := NewTextField()
	gunim.RegisterView(w, "form", func(title string) *Dialog {
		d = NewDialog(title)
		box = NewCheckbox("Show")
		box.OnChange = func(on bool, _ *gunim.UI) gunim.Intent {
			field.Secret = !on
			return nil
		}
		field.Secret = true
		d.Body = NewForm().Add("Password", field).Add("", box)
		d.AddAction("Generate", func(*gunim.UI) gunim.Intent { field.SetText("made", nil); return nil })
		return d
	}, nil)
	c := w.Client()
	if err := c.Mount(gunim.Root, "form", "form", "Add"); err != nil {
		t.Fatal(err)
	}
	if err := c.Focus("form"); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		w.Frame(time.Second / 60)
	}
	// Tab from the field to the box, and Space ticks it.
	w.Input(input.KeyPress{Key: input.KeyTab})
	w.Input(input.KeyPress{Key: input.KeySpace})
	// Tab on to the action, and Enter presses it.
	w.Input(input.KeyPress{Key: input.KeyTab})
	w.Input(input.KeyPress{Key: input.KeyEnter})
	for range 3 {
		w.Frame(time.Second / 60)
	}
	if field.Secret {
		t.Fatal("ticked, the box left the field hidden")
	}
	if field.Text() != "made" {
		t.Fatalf("after the action, the field holds %q", field.Text())
	}
	select {
	case env := <-c.Intents():
		t.Fatalf("the action closed the dialog, sending %v", env.Intent)
	default:
	}
}

func TestADialogWidensForItsButtons(t *testing.T) {
	w := gunimtest.New(t, geom.Sz(1000, 600), nil)
	w.PaintWhenLooked(false)
	var d *Dialog
	var u *gunim.UI
	gunim.RegisterView(w, "many", func(title string) *Dialog {
		d = NewDialog(title)
		d.Body = NewLabel("Several things can be done here.")
		d.AddAction("Copy Prompt", func(*gunim.UI) gunim.Intent { return nil })
		d.AddAction("Copy Setup", func(*gunim.UI) gunim.Intent { return nil })
		d.AddButton("Stop Sharing", func(u *gunim.UI) gunim.Intent { return nil })
		d.SetButtons("Done", "Cancel")
		return d
	}, func(_ *Dialog, _ string, got *gunim.UI) { u = got })
	c := w.Client()
	if err := c.Mount(gunim.Root, "many", "many", "Share", "many"); err != nil {
		t.Fatal(err)
	}
	for range 60 {
		w.Frame(time.Second / 60)
	}
	if err := c.Publish("many", "Share"); err != nil {
		t.Fatal(err)
	}
	w.Frame(time.Second / 60)
	panel, ok := u.Bounds(d)
	if !ok {
		t.Fatal("the dialog has no bounds")
	}
	inner := d.panel(panel.Size(), gunim.Frame{Theme: u.Theme()}).Add(panel.Min)
	for _, b := range append(d.extra, d.cancel, d.ok) {
		r, ok := u.Bounds(b)
		if !ok {
			t.Fatalf("%s has no bounds", b.Label)
		}
		if r.Min.X < inner.Min.X || r.Max.X > inner.Max.X {
			t.Fatalf("%s spans %v, outside the panel %v", b.Label, r, inner)
		}
	}
}

// A careful dialog opens on Cancel too, and keeps OK its usual colour.
func TestACarefulDialogOpensOnCancelInItsUsualColours(t *testing.T) {
	w := gunimtest.New(t, geom.Sz(800, 600), nil)
	w.PaintWhenLooked(false)
	var d *Dialog
	gunim.RegisterView(w, "trust", func(title string) *Dialog {
		d = NewDialog(title)
		d.Careful = true
		d.OnAccept = Sends("trust")
		d.OnDismiss = Sends("leave")
		return d
	}, nil)
	c := w.Client()
	if err := c.Mount(gunim.Root, "trust", "trust", "Trust this key?"); err != nil {
		t.Fatal(err)
	}
	if err := c.Focus("trust"); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		w.Frame(time.Second / 60)
	}
	if d.ok.Kind == ButtonDanger {
		t.Fatal("a careful dialog's OK is red")
	}
	w.Input(input.KeyPress{Key: input.KeyEnter})
	w.Frame(time.Second / 60)
	select {
	case env := <-c.Intents():
		if env.Intent != "leave" {
			t.Fatalf("Enter on a careful dialog sent %v, want leave", env.Intent)
		}
	case <-time.After(time.Second):
		t.Fatal("Enter sent nothing")
	}
}

// A disabled control takes no click and no focus.
func TestADisabledControlTakesNoClick(t *testing.T) {
	box := NewCheckbox("Forward agent")
	box.Disabled = true
	w, run := stage(t, &frame{child: box, size: geom.Sz(200, 30)})
	w.Input(input.PointerDown{Pos: geom.Pt(8, 15), Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: geom.Pt(8, 15), Button: input.ButtonPrimary})
	run(1)
	if box.Checked() || box.Focusable() {
		t.Fatalf("disabled, the box is on %v, focusable %v", box.Checked(), box.Focusable())
	}
	pick := NewDropdown(Labels("None", "desk"))
	pick.Disabled = true
	if pick.Focusable() {
		t.Fatal("a disabled drop-down takes focus")
	}
}

// ownDialog is a view that embeds a dialog, as an application's own dialog does.
type ownDialog struct {
	*Dialog
	note string
}

func TestADialogEmbeddedInAViewClosesItself(t *testing.T) {
	w := gunimtest.New(t, geom.Sz(800, 600), nil)
	w.PaintWhenLooked(false)
	w.Offscreen().ListenForAccess()
	var v *ownDialog
	gunim.RegisterView(w, "own", func(title string) *ownDialog {
		v = &ownDialog{Dialog: NewDialog(title), note: "mine"}
		v.OnAccept, v.OnDismiss = Sends("ok"), Sends("cancel")
		return v
	}, nil)
	c := w.Client()
	if err := c.Mount(gunim.Root, "own", "own", "Mine"); err != nil {
		t.Fatal(err)
	}
	if err := c.Focus("own"); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		w.Frame(time.Second / 60)
	}
	if !hasRole(w.Offscreen().AccessTree().Root, access.RoleDialog) {
		t.Fatal("the dialog does not show")
	}
	w.Input(input.KeyPress{Key: input.KeyEscape})
	w.Frame(time.Second / 60)
	select {
	case env := <-c.Intents():
		if env.Intent != "cancel" || env.From != "own" {
			t.Fatalf("Escape sent %v from %q, want cancel from own", env.Intent, env.From)
		}
	case <-time.After(time.Second):
		t.Fatal("Escape sent nothing")
	}
	for range 120 {
		w.Frame(time.Second / 60)
	}
	if hasRole(w.Offscreen().AccessTree().Root, access.RoleDialog) {
		t.Fatal("the dialog stayed after Escape")
	}
}

// hasRole reports whether n or a node under it has role r.
func hasRole(n *access.Node, r access.Role) bool {
	if n == nil {
		return false
	}
	if n.Role == r {
		return true
	}
	for _, k := range n.Children {
		if hasRole(k, r) {
			return true
		}
	}
	return false
}

// A dialog takes the keyboard as it opens, keeps it from what lies behind, and gives it back as it closes.
func TestADialogHoldsTheKeyboardWhileItIsOpen(t *testing.T) {
	behind := NewTextField()
	w, run := stage(t, &frame{child: behind, size: geom.Sz(300, 36)})
	w.Input(input.PointerDown{Pos: geom.Pt(20, 18), Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: geom.Pt(20, 18), Button: input.ButtonPrimary})
	run(1)
	field := NewTextField()
	gunim.RegisterView(w, "ask", func(string) *Dialog {
		d := NewDialog("Name")
		d.Body = NewForm().Add("Name", field)
		d.OnAccept = Sends("ok")
		d.OnDismiss = Sends("cancel")
		return d
	}, nil)
	c := w.Client()
	if err := c.Mount(gunim.Root, "ask", "ask", ""); err != nil {
		t.Fatal(err)
	}
	run(3)
	w.Input(input.TextInput{Text: "Ada"})
	run(1)
	if field.Text() != "Ada" || behind.Text() != "" {
		t.Fatalf("typing went to %q in the dialog and %q behind it, want the dialog's field", field.Text(), behind.Text())
	}
	// Tab goes round the dialog, and never behind it.
	for range 7 {
		w.Input(input.KeyPress{Key: input.KeyTab})
		run(1)
		w.Input(input.TextInput{Text: "!"})
		run(1)
		if behind.Text() != "" {
			t.Fatal("Tab went behind the dialog")
		}
	}
	if err := c.Unmount("ask"); err != nil {
		t.Fatal(err)
	}
	run(40)
	w.Input(input.TextInput{Text: "x"})
	run(1)
	if behind.Text() != "x" {
		t.Fatalf("after the dialog closed, typing did not go back to the field behind it: %q", behind.Text())
	}
}

// shortcuts is a window's own keys, round what it shows: it counts the
// key presses, text and releases that reach it.
type shortcuts struct {
	*Flex
	presses, texts, releases int
}

func (s *shortcuts) Handle(e input.Event, _ *gunim.UI) bool {
	switch e.(type) {
	case input.KeyPress:
		s.presses++
		return true
	case input.TextInput:
		s.texts++
		return true
	case input.KeyRelease:
		s.releases++
		return true
	}
	return false
}

// A key a dialog and its fields leave goes no further: the window it is
// open in does not see it as a shortcut. A key coming up still reaches
// the window, which may have seen it go down before the dialog came.
func TestADialogKeepsItsKeysFromTheWindowBehind(t *testing.T) {
	keys := &shortcuts{Flex: Column()}
	w := gunimtest.New(t, geom.Sz(800, 600), nil)
	w.PaintWhenLooked(false)
	d := NewDialog("Edit")
	d.Body = NewForm().Add("Name", NewTextField())
	d.OnAccept = Sends("ok")
	d.OnDismiss = Sends("cancel")
	gunim.RegisterView(w, "window", func(struct{}) gunim.Node { return keys },
		func(n gunim.Node, _ struct{}, u *gunim.UI) { u.Insert(n, d) })
	if err := w.Client().Mount(gunim.Root, "window", "window", nil); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		w.Frame(time.Second / 60)
	}
	w.Input(input.KeyPress{Key: input.KeyK, Mods: input.ModControl | input.ModShift})
	w.Input(input.TextInput{Text: "a"})
	w.Input(input.KeyRelease{Key: input.KeyLeftControl})
	w.Frame(time.Second / 60)
	if keys.presses != 0 || keys.texts != 0 || keys.releases != 1 {
		t.Fatalf("the window behind saw %d presses and %d texts, and %d releases, want only the release", keys.presses, keys.texts, keys.releases)
	}
}

// A danger dialog shows the ring on Cancel as it opens, however it was
// opened, and the arrows go along its buttons: Right to the action,
// whose Enter then does it, and Left back to Cancel.
func TestADangerDialogRingsCancelAndTheArrowsMoveAlong(t *testing.T) {
	w := gunimtest.New(t, geom.Sz(800, 600), nil)
	w.PaintWhenLooked(false)
	var d *Dialog
	gunim.RegisterView(w, "confirm", func(title string) *Dialog {
		d = NewDialog(title)
		d.Danger = true
		d.Body = NewLabel("It goes for good.")
		d.OnAccept = Sends("delete")
		d.OnDismiss = Sends("keep")
		return d
	}, nil)
	c := w.Client()
	// A click first, as the button that opened it took one: the rings
	// are off.
	w.Input(input.PointerDown{Pos: geom.Pt(5, 5), Button: input.ButtonPrimary, Clicks: 1})
	w.Input(input.PointerUp{Pos: geom.Pt(5, 5), Button: input.ButtonPrimary})
	if err := c.Mount(gunim.Root, "confirm", "confirm", "Delete?"); err != nil {
		t.Fatal(err)
	}
	if err := c.Focus("confirm"); err != nil {
		t.Fatal(err)
	}
	for range 30 {
		w.Frame(time.Second / 60)
	}
	if d.cancel.ring.Value() <= 0 {
		t.Fatal("opened on Cancel, it shows no ring")
	}
	w.Input(input.KeyPress{Key: input.KeyRight})
	for range 30 {
		w.Frame(time.Second / 60)
	}
	if d.ok.ring.Value() < 0.9 || d.cancel.ring.Value() > 0.1 {
		t.Fatalf("after Right, the rings are %v on OK and %v on Cancel", d.ok.ring.Value(), d.cancel.ring.Value())
	}
	w.Input(input.KeyPress{Key: input.KeyLeft})
	w.Input(input.KeyPress{Key: input.KeyRight})
	w.Input(input.KeyPress{Key: input.KeyEnter})
	w.Frame(time.Second / 60)
	select {
	case env := <-c.Intents():
		if env.Intent != "delete" {
			t.Fatalf("Enter on the action sent %v, want delete", env.Intent)
		}
	case <-time.After(time.Second):
		t.Fatal("Enter on the action sent nothing")
	}
}

// Keys hears what the dialog's own nodes leave, before the dialog does,
// and Buttons are its buttons as they stand.
func TestADialogsKeysComeFirst(t *testing.T) {
	w := gunimtest.New(t, geom.Sz(800, 600), nil)
	w.PaintWhenLooked(false)
	var d *Dialog
	var heard []input.Key
	gunim.RegisterView(w, "confirm", func(title string) *Dialog {
		d = NewDialog(title)
		d.Body = NewLabel("Pick one.")
		d.SetButtons("Type", "")
		d.AddButton("Cancel", func(u *gunim.UI) gunim.Intent { return "cancel" })
		d.AddButton("Copy", func(u *gunim.UI) gunim.Intent { return "copy" })
		d.Keys = func(e input.Event, u *gunim.UI) bool {
			if k, ok := e.(input.KeyPress); ok && k.Key == input.KeyDown {
				heard = append(heard, k.Key)
				return true
			}
			return false
		}
		return d
	}, nil)
	c := w.Client()
	if err := c.Mount(gunim.Root, "confirm", "confirm", "Use"); err != nil {
		t.Fatal(err)
	}
	if err := c.Focus("confirm"); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		w.Frame(time.Second / 60)
	}
	w.Input(input.KeyPress{Key: input.KeyDown})
	w.Frame(time.Second / 60)
	if len(heard) != 1 {
		t.Fatalf("Keys heard %v", heard)
	}
	row := d.Buttons()
	if len(row) != 3 || row[0].(*Button).Label != "Cancel" || row[1].(*Button).Label != "Copy" || row[2] != gunim.Node(d.ok) {
		t.Fatalf("the buttons are %v", row)
	}
}
