package filemanager

import (
	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/widget"
)

func registerDialogs(w *gunim.Window) {
	gunim.RegisterView(w, "clash", newClashDialog, nil)
	gunim.RegisterView(w, "confirm", newConfirmDialog, nil)
	gunim.RegisterView(w, "prompt", newPromptDialog, nil)
	gunim.RegisterView(w, "error", newErrorDialog, nil)
	gunim.RegisterView(w, "favourite", newFavouriteDialog, nil)
	gunim.RegisterView(w, "password", newPasswordDialog, nil)
}

// clashBody says what the two items are, and offers to answer the same
// for the rest.
type clashBody struct {
	*widget.Flex
	all *widget.Checkbox
}

// Focusables is what Tab moves through in the dialog's body.
func (b *clashBody) Focusables() []gunim.Node { return []gunim.Node{b.all} }

// newClashDialog asks what to do about an item going where one of its
// name is: replace it, keep both, skip it, or stop.
func newClashDialog(s ClashAsk) *widget.Dialog {
	d := widget.NewDialog("“" + s.Name + "” is already in " + s.Where)
	arriving := widget.NewLabel("Arriving: " + s.New)
	there := widget.NewLabel("Already there: " + s.Old)
	for _, l := range []*widget.Label{arriving, there} {
		l.Size, l.Color = SmallText, Faint
	}
	all := widget.NewCheckbox("Do the same for every clash in this operation")
	col := widget.Column(arriving, there, all)
	col.Cross = widget.CrossStretch
	body := &clashBody{Flex: col, all: all}
	if !s.CanForAll {
		all.Disabled = true
	}
	d.Body = body
	answer := func(c Choice) func(*gunim.UI) gunim.Intent {
		return func(*gunim.UI) gunim.Intent { return ClashAnswered{Op: s.Op, Choice: c, All: all.Checked()} }
	}
	d.OnDismiss = widget.Sends(ClashAnswered{Op: s.Op, Stop: true})
	d.AddButton("Skip", answer(ChoiceSkip))
	if s.SameKind {
		d.AddButton("Keep both", answer(ChoiceKeepBoth))
		d.SetButtons("Replace", "Stop")
		d.OnAccept = answer(ChoiceReplace)
	} else {
		d.SetButtons("Keep both", "Stop")
		d.OnAccept = answer(ChoiceKeepBoth)
	}
	d.Careful = true
	return d
}

// newConfirmDialog asks before something that cannot be taken back.
func newConfirmDialog(s Confirm) *widget.Dialog {
	d := widget.NewDialog(s.Title)
	body := widget.NewLabel(s.Body)
	body.Color = Faint
	d.Body = body
	d.Danger = true
	if s.Alt != "" {
		d.AddButton(s.Alt, func(u *gunim.UI) gunim.Intent { return Confirmed{Token: s.Token, Alt: true} })
	}
	d.SetButtons(s.OK, "Cancel")
	d.OnAccept = widget.Sends(Confirmed{Token: s.Token, OK: true})
	d.OnDismiss = widget.Sends(Confirmed{Token: s.Token})
	return d
}

// fieldsBody is a dialog's body of fields, and the tick boxes under
// them, which Tab moves through in turn.
type fieldsBody struct {
	*widget.Flex
	parts []gunim.Node
}

// Focusables is what Tab moves through in the dialog's body.
func (b *fieldsBody) Focusables() []gunim.Node { return b.parts }

// newFieldsBody lays parts out in a column, with notes, which take no
// keyboard, among them.
func newFieldsBody(parts ...gunim.Node) *fieldsBody {
	col := widget.Column(parts...)
	col.Cross = widget.CrossStretch
	b := &fieldsBody{Flex: col}
	for _, p := range parts {
		if _, note := p.(*widget.Label); !note {
			b.parts = append(b.parts, p)
		}
	}
	return b
}

// newPromptDialog asks for a name, with the name without its extension
// selected, and will not take one an item cannot have.
func newPromptDialog(s Prompt) *widget.Dialog {
	d := widget.NewDialog(s.Title)
	field := widget.NewTextField()
	field.SetText(s.Text, nil)
	field.Select(0, s.Stem)
	parts := []gunim.Node{field}
	var box *widget.Checkbox
	if s.Check != "" {
		box = widget.NewCheckbox(s.Check)
		parts = append(parts, box)
	}
	d.Body = newFieldsBody(parts...)
	d.SetButtons(s.OK, "Cancel")
	d.Check = func() string {
		if err := checkName(s.Paths, field.Text()); err != nil {
			return err.Error()
		}
		return ""
	}
	d.OnAccept = func(u *gunim.UI) gunim.Intent {
		return Prompted{Token: s.Token, Text: field.Text(), OK: true, Checked: box != nil && box.Checked()}
	}
	d.OnDismiss = widget.Sends(Prompted{Token: s.Token})
	return d
}

// newPasswordDialog asks for the password of a zip: once to open one,
// and twice, the same both times, to protect one being made.
func newPasswordDialog(s PasswordPrompt) *widget.Dialog {
	d := widget.NewDialog(s.Title)
	text := widget.NewLabel(s.Text)
	text.Color = Faint
	field := widget.NewTextField()
	field.Secret, field.Placeholder = true, "Password"
	parts := []gunim.Node{text}
	if s.Problem != "" {
		problem := widget.NewLabel(s.Problem)
		problem.Color = ErrorInk
		parts = append(parts, problem)
	}
	parts = append(parts, field)
	var again *widget.TextField
	if s.Make {
		again = widget.NewTextField()
		again.Secret, again.Placeholder = true, "The same again"
		parts = append(parts, again)
	}
	d.Body = newFieldsBody(parts...)
	d.SetButtons(s.OK, "Cancel")
	d.Check = func() string {
		switch {
		case field.Text() == "":
			return "Type the password."
		case again != nil && again.Text() != field.Text():
			return "The two passwords are not the same."
		}
		return ""
	}
	d.OnAccept = func(u *gunim.UI) gunim.Intent { return PasswordGiven{Token: s.Token, Text: field.Text(), OK: true} }
	d.OnDismiss = widget.Sends(PasswordGiven{Token: s.Token})
	return d
}

// newErrorDialog says an operation failed, and why.
func newErrorDialog(s ErrorBox) *widget.Dialog {
	d := widget.NewDialog(s.Title)
	body := widget.NewLabel(s.Body)
	body.Selectable = true
	d.Body = body
	d.SetButtons("OK", "")
	d.OnAccept = widget.Sends(DialogClosed{})
	d.OnDismiss = widget.Sends(DialogClosed{})
	return d
}
