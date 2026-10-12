// Package themeedit is an editor for a gunim theme that any program can
// show: in a dialog, on a tab, or anywhere else a node goes.
//
// The editor edits overrides: values laid over a base theme, such as the
// dark or light theme the user started from. Every edit is live: the
// editor hands the program the new theme on each change, and the program
// switches the window to it. Or, with [Options.OnSave], the edits are a
// draft that only the Preview wears, until the user presses Save.
//
//	ed := themeedit.New(themeedit.Options{
//		Base:      widget.Dark(),
//		Overrides: saved,
//		Sections: []themeedit.Section{{
//			Title: "Terminal",
//			Fields: []themeedit.Field{{
//				Key:    widget.Caret.Key(),
//				Label:  "Cursor",
//				Detail: "How the cursor moves along a line.",
//				Presets: []themeedit.Preset{
//					{Label: "Glides", Value: widget.Caret.Default()},
//					{Label: "Jumps", Value: themeedit.Instant},
//				},
//			}},
//		}},
//		OnChange: func(th theme.Theme, u *gunim.UI) gunim.Intent {
//			u.UseTheme(th)
//			return nil
//		},
//		OnCommit: func(over theme.Theme, u *gunim.UI) gunim.Intent {
//			return SaveTheme{Over: over}
//		},
//	})
//
// The editor keeps its inputs and what they change apart. Its controls
// stay in the base theme, so they hold still while the user edits; a
// Preview beside them, or above them in a narrow editor, wears the
// edits. A motion plays there as it changes, and again at its row's
// Play button.
//
// It has two tabs. The first, Basics, shows the values the program
// picked out, in sections, each with a label and a sentence saying what
// it does. All values lists every token declared, grouped by the first
// part of its key, with a field to search them and filters for those
// changed and for each kind.
package themeedit

import (
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// Options sets up an editor.
type Options struct {
	// Base is the theme the edits are laid over. A token the overrides
	// leave out has Base's value, or its default where Base leaves it
	// out too.
	Base theme.Theme
	// Name is the base theme's name as the user knows it, as in
	// "Editing Dark" and "Reset to Dark". Empty takes Base's name, in
	// words.
	Name string
	// Overrides are the values the user has changed, as the program
	// saved them: [theme.UnmarshalValues] reads them from a file.
	Overrides theme.Theme
	// Sections are the values the program picks out for the first tab.
	// With none, the editor shows All values alone.
	Sections []Section
	// ChosenTitle is the first tab's title. Empty is "Basics".
	ChosenTitle string
	// Choices offers values to pick from for tokens the editor has no
	// control of its own for, such as a font, by key. A token without
	// choices shows its value as text.
	Choices map[string][]Preset
	// Preview, when set, is what the Preview shows in the theme as
	// edited, in place of the editor's own few widgets and terminal. A
	// motion plays over it; the cursor's and the quick motion's own
	// demonstrations need the editor's widgets, and play as any other
	// motion's do over it.
	Preview gunim.Node
	// PreviewTitle is the name in the title bar of the window the
	// Preview draws, such as the program's own name. Empty is "Notes"
	// over the editor's own widgets, and no name over a Preview of the
	// program's own.
	PreviewTitle string
	// OnChange runs on the UI goroutine with the theme as edited, Base
	// with the overrides over it, on every change: every step of a drag
	// too. It usually switches the window to it with [gunim.UI.UseTheme].
	// A non-nil result is sent to the application as the editor's
	// intent.
	OnChange func(th theme.Theme, u *gunim.UI) gunim.Intent
	// OnCommit runs, in the same way, with the overrides alone, as each
	// edit ends: a drag let go, a value typed, a preset picked, a value
	// reset. It is the time to save them.
	OnCommit func(over theme.Theme, u *gunim.UI) gunim.Intent
	// OnSave, when set, makes the edits a draft: the program leaves its
	// windows as they are, and the Preview alone wears the edits, until
	// the user presses Save in the header. That runs OnSave with the
	// overrides, for the program to keep and use them. Discard beside
	// it takes the draft back to the overrides as last saved: those the
	// editor was made with, or given by [Editor.SetOverrides], or saved
	// since. OnChange and OnCommit run as ever, on the draft.
	OnSave func(over theme.Theme, u *gunim.UI) gunim.Intent
	// OnExport, when set, adds Export… to the header's menu, which runs
	// it with the overrides written as JSON, for the program to save
	// where the user says.
	OnExport func(data []byte, u *gunim.UI) gunim.Intent
	// OnImport, when set, adds Import… to the header's menu, which runs
	// it. The program reads a file the user picks and hands it to
	// [Editor.Import].
	OnImport func(u *gunim.UI) gunim.Intent
}

// A Section is a group of fields on the first tab, under a title.
type Section struct {
	Title  string
	Fields []Field
}

// A Field is one value on the first tab.
type Field struct {
	// Key is the token's key, such as widget.Caret.Key().
	Key string
	// Label names the value, such as "Cursor", and Detail says in one
	// plain sentence what it changes.
	Label  string
	Detail string
	// Presets, when set, are the values to pick from, shown as a
	// segmented control in place of the token's own control. A value
	// none of them has shows as Custom; a spring then shows its Speed
	// and Bounce.
	Presets []Preset
	// Min and Max, when Max is above Min, bound a length or a number,
	// which then shows as a slider over that range with a field for
	// the value beside it.
	Min, Max float32
}

// A Preset is a value with a name, such as "Jumps" for an instant
// spring.
type Preset struct {
	Label string
	Value any
}

// Editor edits a theme as overrides on a base theme. Make one with
// [New] and mount it as any node; it fills the room it is given, or
// [Width] by [Height] where it is given none.
//
// Its header names the theme edited and counts the changes, with Reset
// all beside them, which takes every value back and can be undone until
// the next edit. Every row has one control for its value: a swatch that
// opens a colour picker, a field for a number, a slider and a field for
// a number with a range, four fields for insets, or a spring's presets
// in words. Every field for a number can be dragged up and down to
// change it.
// A changed row has a dot before its name and a button that resets it.
//
// With [Options.OnSave], the header also has Discard and Save, and says
// when there are changes not saved.
//
// Everything is reached by Tab, and every control is named for a screen
// reader.
type Editor struct {
	// OnChange, OnCommit, OnSave, OnExport and OnImport are as in
	// [Options].
	OnChange func(th theme.Theme, u *gunim.UI) gunim.Intent
	OnCommit func(over theme.Theme, u *gunim.UI) gunim.Intent
	OnSave   func(over theme.Theme, u *gunim.UI) gunim.Intent
	OnExport func(data []byte, u *gunim.UI) gunim.Intent
	OnImport func(u *gunim.UI) gunim.Intent

	base, over theme.Theme
	// saved is the overrides as last saved, which Discard goes back to,
	// with OnSave set.
	saved    theme.Theme
	name     string
	sections []Section
	choices  map[string][]Preset
	// tokens are every token declared, by key, labels the labels the
	// sections give some of them, and defaults a theme of every token
	// at its default, which pins a theme's every value.
	tokens   map[string]theme.Info
	labels   map[string]string
	defaults theme.Theme

	// themed dresses everything but the preview's window in the base
	// theme, and frame lays it out.
	themed   *widget.Themed
	frame    *frame
	title    *widget.Label
	count    *widget.Label
	resetAll *widget.Button
	// discard and save are the header's Discard and Save, with OnSave
	// set.
	discard, save *widget.Button
	more          *widget.MenuButton
	tabs          *widget.Tabs
	preview       *previewPanel
	spec          *specimen
	all           *allValues
	// chosen are the rows of the first tab, by key.
	chosen map[string][]*row
	// undo is the overrides Reset all took back, while it can be undone.
	undo *theme.Theme
}

// New returns an editor set up by o.
func New(o Options) *Editor {
	e := &Editor{
		OnChange: o.OnChange, OnCommit: o.OnCommit, OnSave: o.OnSave, OnExport: o.OnExport, OnImport: o.OnImport,
		base: o.Base, over: o.Overrides, saved: o.Overrides, name: o.Name, sections: o.Sections, choices: o.Choices,
		tokens: map[string]theme.Info{}, labels: map[string]string{},
		chosen: map[string][]*row{},
	}
	e.defaults = theme.Make("")
	for _, info := range theme.Tokens() {
		e.tokens[info.Key] = info
		if over, err := e.defaults.WithValue(info.Key, info.Default); err == nil {
			e.defaults = over
		}
	}
	for _, s := range o.Sections {
		for _, f := range s.Fields {
			if _, ok := e.labels[f.Key]; !ok && f.Label != "" {
				e.labels[f.Key] = f.Label
			}
		}
	}
	e.build(o)
	return e
}

// pinned returns th with every token it leaves out at its default, so a
// part of the window that wears it takes nothing from the theme around
// it.
func (e *Editor) pinned(th theme.Theme) theme.Theme { return th.Over(e.defaults) }

// baseName returns the base theme's name as the user knows it.
func (e *Editor) baseName() string {
	if e.name != "" {
		return e.name
	}
	if e.base.Name == "" {
		return "the base theme"
	}
	return Words(e.base.Name)
}

// build makes the editor's nodes.
func (e *Editor) build(o Options) {
	e.title = widget.NewLabel("")
	e.title.Face, e.title.Size, e.title.NoWrap, e.title.MaxLines = widget.BoldFont, TitleSize, true, 1
	e.count = widget.NewLabel("")
	e.count.Color, e.count.NoWrap = widget.Placeholder, true
	e.resetAll = widget.NewButton("Reset all")
	e.resetAll.OnClick = func(u *gunim.UI) gunim.Intent {
		if e.undo != nil {
			e.Undo(u)
		} else {
			e.ResetAll(u)
		}
		return nil
	}
	var menu []widget.MenuItem
	var acts []func(u *gunim.UI) gunim.Intent
	if e.OnImport != nil {
		menu = append(menu, widget.MenuItem{Label: "Import…", Icon: icon.FileUp})
		acts = append(acts, func(u *gunim.UI) gunim.Intent { return e.OnImport(u) })
	}
	if e.OnExport != nil {
		menu = append(menu, widget.MenuItem{Label: "Export…", Icon: icon.FileDown})
		acts = append(acts, func(u *gunim.UI) gunim.Intent {
			data, err := theme.MarshalValues(e.over)
			if err != nil {
				return nil
			}
			return e.OnExport(data, u)
		})
	}
	head := []gunim.Node{}
	names := widget.Column(e.title, e.count)
	names.Gap = NamesGap
	head = append(head, names, widget.NewSpacer(), e.resetAll)
	if e.OnSave != nil {
		e.discard = widget.NewButton("Discard")
		e.discard.OnClick = func(u *gunim.UI) gunim.Intent { e.Discard(u); return nil }
		e.save = widget.NewButton("Save")
		e.save.Kind = widget.ButtonPrimary
		e.save.OnClick = func(u *gunim.UI) gunim.Intent { e.Save(u); return nil }
		head = append(head, e.discard, e.save)
	}
	if len(menu) > 0 {
		e.more = widget.NewMenuButton("", menu)
		e.more.Icon, e.more.Tooltip = icon.Ellipsis, "Import and export"
		e.more.OnPick = func(i int, u *gunim.UI) gunim.Intent { return acts[i](u) }
		head = append(head, e.more)
	}
	bar := widget.Row(head...).Grow(head[1], 1)
	bar.Cross = widget.CrossCenter
	header := widget.NewPad(bar)
	header.Padding = HeaderPadding

	content, title := o.Preview, o.PreviewTitle
	if content == nil {
		e.spec = newSpecimen()
		content = e.spec
		if title == "" {
			title = "Notes"
		}
	}
	e.preview = newPreviewPanel(content, e.spec, title, e.pinned(e.Theme()))
	e.preview.use(e.pinned(e.Theme()))

	e.all = newAllValues(e)
	if len(e.sections) == 0 {
		e.tabs = widget.NewTabs([]string{"All values"}, e.all)
	} else {
		title := o.ChosenTitle
		if title == "" {
			title = "Basics"
		}
		e.tabs = widget.NewTabs([]string{title, "All values"}, widget.NewScroll(&page{child: e.buildChosen()}), e.all)
	}
	e.tabs.Inset = TabInset
	e.frame = &frame{header: header, tabs: e.tabs, preview: e.preview}
	e.themed = widget.NewThemed(e.frame, e.pinned(e.base))
	e.showCount(nil)
}

// buildChosen makes the first tab's sections: each a heading over a
// card of rows.
func (e *Editor) buildChosen() gunim.Node {
	blocks := make([]gunim.Node, 0, len(e.sections))
	for _, s := range e.sections {
		rows := make([]gunim.Node, 0, len(s.Fields))
		for _, f := range s.Fields {
			info, ok := e.tokens[f.Key]
			if !ok {
				continue
			}
			r := e.newRow(info, f, false)
			e.chosen[f.Key] = append(e.chosen[f.Key], r)
			rows = append(rows, r.line)
		}
		if len(rows) == 0 {
			continue
		}
		blocks = append(blocks, section(s.Title, RowGap, rows...))
	}
	col := widget.Column(blocks...)
	col.Cross, col.Gap = widget.CrossStretch, SectionGap
	return col
}

// section returns a heading over a card of rows, gap apart.
func section(title string, gap theme.Token[float32], rows ...gunim.Node) gunim.Node {
	col := widget.Column(rows...)
	col.Cross, col.Gap = widget.CrossStretch, gap
	card := widget.NewCard(col)
	if title == "" {
		return card
	}
	h := widget.NewLabel(title)
	h.Face = widget.BoldFont
	all := widget.Column(h, card)
	all.Cross, all.Gap = widget.CrossStretch, HeadingGap
	return all
}

// label returns the name a token shows under: the one a section gives
// it, or its key in words.
func (e *Editor) label(key string) string {
	if l, ok := e.labels[key]; ok {
		return l
	}
	return Words(key)
}

// Words writes a key as words, such as "Button primary hover" for
// "button.primary.hover".
func Words(key string) string {
	s := strings.NewReplacer(".", " ", "-", " ", "_", " ").Replace(key)
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// Chosen returns the keys of the first tab's fields, in order.
func (e *Editor) Chosen() []string {
	var out []string
	for _, s := range e.sections {
		for _, f := range s.Fields {
			if _, ok := e.tokens[f.Key]; ok {
				out = append(out, f.Key)
			}
		}
	}
	return out
}

// Listed returns the keys All values lists now, under the search and
// the filter, in order.
func (e *Editor) Listed() []string { return e.all.listedKeys() }

// Search sets the search of All values, as if typed, and sends no
// intent.
func (e *Editor) Search(q string, u *gunim.UI) {
	e.all.search.SetText(q, u)
	e.all.apply(q, e.all.filter, u)
}

// SetFilter narrows All values to f, as its filter does, and sends no
// intent.
func (e *Editor) SetFilter(f Filter, u *gunim.UI) {
	e.all.apply(e.all.query, f, u)
}

// ShowAllValues brings All values to the front.
func (e *Editor) ShowAllValues(u *gunim.UI) { e.tabs.SetSelected(len(e.tabs.Titles)-1, u) }

// OpenPicker opens the colour picker of the token named key, in the tab
// in front, as a click on its swatch does, and reports whether it
// could: false for a key with no colour, or none in that tab.
func (e *Editor) OpenPicker(key string, u *gunim.UI) bool {
	var rows []*row
	if e.tabs.Selected() == len(e.tabs.Titles)-1 {
		if r := e.all.row(key); r != nil {
			rows = append(rows, r)
		}
	} else {
		rows = e.chosen[key]
	}
	for _, r := range rows {
		if c, ok := r.ctl.(*colorControl); ok {
			// The row comes into view first, and the picker opens below
			// it once it is there.
			u.Reveal(c.button)
			u.After(revealTime, c.button.Open)
			return true
		}
	}
	return false
}

// revealTime is how long a row takes to scroll into view, for the
// picker to open under it once it is there.
const revealTime = 400 * time.Millisecond

// Theme returns the theme as edited: the base with the overrides over
// it.
func (e *Editor) Theme() theme.Theme { return e.over.Over(e.base) }

// Base returns the theme the edits are laid over.
func (e *Editor) Base() theme.Theme { return e.base }

// Overrides returns the values the user has changed.
func (e *Editor) Overrides() theme.Theme { return e.over }

// SetBase lays the edits over th from now on, as when the user switches
// from the dark theme to the light, and sends no intent. Rows the
// overrides leave out show th's values, and the controls take th.
func (e *Editor) SetBase(th theme.Theme, u *gunim.UI) {
	e.base = th
	e.themed.Use(e.pinned(th))
	e.undo = nil
	e.refreshAll(u)
}

// SetOverrides makes over the values the user has changed, as saved,
// and sends no intent.
func (e *Editor) SetOverrides(over theme.Theme, u *gunim.UI) {
	e.over, e.saved = over, over
	e.undo = nil
	e.refreshAll(u)
}

// Unsaved reports whether the draft has changes not saved, with
// [Options.OnSave] set.
func (e *Editor) Unsaved() bool { return e.OnSave != nil && !sameValues(e.over, e.saved) }

// Save keeps the draft, as the Save button does, running OnSave with
// the overrides. It does nothing with no changes unsaved.
func (e *Editor) Save(u *gunim.UI) {
	if !e.Unsaved() {
		return
	}
	e.saved = e.over
	e.showCount(u)
	send(u, e, e.OnSave(e.over, u))
}

// Discard takes the draft back to the overrides as last saved, as the
// Discard button does, running OnChange and OnCommit.
func (e *Editor) Discard(u *gunim.UI) {
	if !e.Unsaved() {
		return
	}
	e.over = e.saved
	e.undo = nil
	e.refreshAll(u)
	e.changed(true, u)
}

// sameValues reports whether a and b give the same tokens the same
// values.
func sameValues(a, b theme.Theme) bool {
	if a.Len() != b.Len() {
		return false
	}
	for _, k := range a.Keys() {
		av, _ := a.Value(k)
		bv, ok := b.Value(k)
		if !ok || !same(av, bv) {
			return false
		}
	}
	return true
}

// Export returns the overrides written as JSON, as [theme.MarshalValues]
// writes them.
func (e *Editor) Export() ([]byte, error) { return theme.MarshalValues(e.over) }

// Import makes the values in data, written as [Editor.Export] writes
// them, the overrides, as the user asked by Import. It runs OnChange and
// OnCommit, as an edit does. Values it cannot read are left out, and the
// error lists them; the rest are used.
func (e *Editor) Import(data []byte, u *gunim.UI) error {
	over, err := theme.UnmarshalValues(theme.Make(e.over.Name), data)
	if over.Len() == 0 && err != nil {
		return err
	}
	e.over = over
	e.undo = nil
	e.refreshAll(u)
	e.changed(true, u)
	return err
}

// ResetAll takes every value back to the base theme, as the Reset all
// button does, running OnChange and OnCommit. Until the next edit,
// [Editor.Undo] brings the values back, and the button says Undo.
func (e *Editor) ResetAll(u *gunim.UI) {
	if e.over.Len() == 0 {
		return
	}
	was := e.over
	e.over = e.over.Without(e.over.Keys()...)
	e.refreshAll(u)
	e.undo = &was
	e.showCount(u)
	e.changed(true, u)
}

// Undo brings back the values Reset all took, as the button does while
// it says Undo, running OnChange and OnCommit. It does nothing once
// another edit is made.
func (e *Editor) Undo(u *gunim.UI) {
	if e.undo == nil {
		return
	}
	e.over = *e.undo
	e.undo = nil
	e.refreshAll(u)
	e.changed(true, u)
}

// Reset takes the token named key back to the base theme, as its row's
// reset button does, running OnChange and OnCommit.
func (e *Editor) Reset(key string, u *gunim.UI) {
	if !e.over.Has(key) {
		return
	}
	e.over = e.over.Without(key)
	e.undo = nil
	e.refresh(key, nil, u)
	e.changed(true, u)
	e.playIfMotion(key, u)
}

// Set gives the token named key the value v, as an edit in its row
// does, running OnChange, and OnCommit when commit is set. A value that
// does not suit the token gives an error and changes nothing.
func (e *Editor) Set(key string, v any, commit bool, u *gunim.UI) error {
	return e.edit(key, v, commit, nil, u)
}

// Play plays the motion of the token named key in the preview, as its
// row's Play button does: the cursor's jumps along the terminal's line,
// the quick motion flips a switch, settling slides a panel in, a bounce
// pops a toast, and a theme switch fades the preview to the base theme
// and back. Any other motion slides a chip along a track. A key that is
// no motion plays nothing.
func (e *Editor) Play(key string, u *gunim.UI) {
	s, ok := e.value(key).(anim.Spring)
	if !ok {
		return
	}
	d := demos[key]
	if e.spec == nil && (d == demoCaret || d == demoQuick) {
		d = demoSlide
	}
	e.preview.play(d, s, e.label(key), e.pinned(e.base), e.pinned(e.Theme()), u)
}

// playIfMotion plays the token named key, where it is a motion.
func (e *Editor) playIfMotion(key string, u *gunim.UI) {
	if e.tokens[key].Kind == theme.KindSpring {
		e.Play(key, u)
	}
}

// edit sets key to v from the control src, which already shows it.
func (e *Editor) edit(key string, v any, commit bool, src control, u *gunim.UI) error {
	over, err := e.over.WithValue(key, v)
	if err != nil {
		return err
	}
	e.over = over
	e.undo = nil
	e.refresh(key, src, u)
	e.changed(commit, u)
	if src == nil && commit {
		e.playIfMotion(key, u)
	}
	return nil
}

// changed dresses the preview in the edits and tells the program.
func (e *Editor) changed(commit bool, u *gunim.UI) {
	e.preview.use(e.pinned(e.Theme()))
	e.showCount(u)
	if e.OnChange != nil {
		send(u, e, e.OnChange(e.Theme(), u))
	}
	if commit && e.OnCommit != nil {
		send(u, e, e.OnCommit(e.over, u))
	}
}

// showCount says how many values are changed, and sets Reset all to
// suit.
func (e *Editor) showCount(u *gunim.UI) {
	e.title.Text = "Editing " + e.baseName()
	switch n := e.over.Len(); n {
	case 0:
		e.count.Text = "No changes"
	case 1:
		e.count.Text = "1 change"
	default:
		e.count.Text = strconv.Itoa(n) + " changes"
	}
	if e.save != nil {
		unsaved := e.Unsaved()
		if unsaved {
			e.count.Text += ", not saved"
		}
		e.save.Disabled, e.discard.Disabled = !unsaved, !unsaved
		e.save.Tooltip = "Uses these values everywhere, and keeps them"
		e.discard.Tooltip = "Takes back the changes made since the last save"
	}
	e.resetAll.Label, e.resetAll.Tooltip = "Reset all", "Takes every value back to "+e.baseName()
	e.resetAll.Disabled = e.over.Len() == 0
	if e.undo != nil {
		e.resetAll.Label, e.resetAll.Tooltip = "Undo reset", "Brings back the values Reset all took"
		e.resetAll.Disabled = false
	}
	e.all.showCounts(u)
	u.Invalidate()
}

// send sends in from n, when there is one.
func send(u *gunim.UI, n gunim.Node, in gunim.Intent) {
	if in != nil && u != nil {
		u.Send(n, in)
	}
}

// value returns the value the token named key has as edited.
func (e *Editor) value(key string) any {
	if v, ok := e.over.Value(key); ok {
		return v
	}
	if v, ok := e.base.Value(key); ok {
		return v
	}
	return e.tokens[key].Default
}

// rowsOf returns every row of key built now.
func (e *Editor) rowsOf(key string) []*row {
	rows := e.chosen[key]
	return append(rows[:len(rows):len(rows)], e.all.rowsOf(key)...)
}

// refresh shows key's value in every row of it but the one of src.
func (e *Editor) refresh(key string, src control, u *gunim.UI) {
	v, on := e.value(key), e.over.Has(key)
	for _, r := range e.rowsOf(key) {
		if r.ctl != src {
			r.ctl.show(v, u)
		}
		r.mark(on, u)
	}
	u.Invalidate()
}

// refreshAll shows every row's value, and dresses the preview in the
// edits.
func (e *Editor) refreshAll(u *gunim.UI) {
	for k := range e.chosen {
		e.refresh(k, nil, u)
	}
	for _, rows := range e.all.built {
		for _, r := range rows {
			e.refresh(r.key, nil, u)
		}
	}
	e.preview.use(e.pinned(e.Theme()))
	e.showCount(u)
}

// Children implements [gunim.Composite].
func (e *Editor) Children() []gunim.Node { return []gunim.Node{e.themed} }

// Layout implements [gunim.Node]. The editor fills the room it is given,
// or [Width] by [Height] where that is unbounded.
func (e *Editor) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	w, h := c.Max.W, c.Max.H
	if w <= 0 {
		w = Width.Get(f.Theme)
	}
	if h <= 0 {
		h = Height.Get(f.Theme)
	}
	size := c.Constrain(geom.Sz(w, h))
	kids.At(0).Layout(gunim.Tight(size))
	kids.At(0).Place(geom.Point{})
	return size
}

// Paint implements [gunim.Node].
func (e *Editor) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}

// frame lays the editor out: the header over the tabs, with the preview
// beside them, or, narrower than [Narrow], between the header and the
// tabs. It paints the window's ground under them, in the base theme.
type frame struct {
	header, tabs gunim.Node
	preview      *previewPanel
}

// Children implements [gunim.Composite].
func (fr *frame) Children() []gunim.Node { return []gunim.Node{fr.header, fr.tabs, fr.preview} }

// Layout implements [gunim.Node].
func (fr *frame) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	th := f.Theme
	w, h := c.Max.W, c.Max.H
	pad := PagePadding.Get(th)
	header, tabs, preview := kids.At(0), kids.At(1), kids.At(2)
	narrow := w < Narrow.Get(th)
	fr.preview.setNarrow(narrow, narrow && h < Short.Get(th), f.UI())
	if narrow {
		// The header, the preview and the tabs keep to the page's width,
		// in the middle.
		pw := min(w-pad.Left-pad.Right, PageWidth.Get(th))
		hw := pw + pad.Left + pad.Right
		hs := header.Layout(gunim.Constraints{Min: geom.Sz(hw, 0), Max: geom.Sz(hw, 0)})
		header.Place(geom.Pt((w-hw)/2, 0))
		ps := preview.Layout(gunim.Constraints{Min: geom.Sz(pw, 0), Max: geom.Sz(pw, max(1, h*0.5))})
		preview.Place(geom.Pt((w-pw)/2, hs.H))
		top := hs.H + ps.H + HeadingGap.Get(th)
		tabs.Layout(gunim.Tight(geom.Sz(hw, max(0, h-top))))
		tabs.Place(geom.Pt((w-hw)/2, top))
		return c.Constrain(geom.Sz(w, h))
	}
	// The controls take the left, the preview the right, a gutter
	// between them, and the two keep together in the middle of a wide
	// window.
	pw := PreviewWidth.Get(th)
	gutter := widget.Gap.Get(th) * 2
	cw := min(w-pw-pad.Right-gutter, PageWidth.Get(th)+pad.Left+pad.Right)
	x := max(0, (w-cw-gutter-pw-pad.Right)/2)
	hs := header.Layout(gunim.Constraints{Min: geom.Sz(cw, 0), Max: geom.Sz(cw, 0)})
	header.Place(geom.Pt(x, 0))
	tabs.Layout(gunim.Tight(geom.Sz(cw, max(0, h-hs.H))))
	tabs.Place(geom.Pt(x, hs.H))
	top := HeaderPadding.Get(th).Top
	preview.Layout(gunim.Constraints{Min: geom.Sz(pw, 0), Max: geom.Sz(pw, max(1, h-top-pad.Bottom))})
	preview.Place(geom.Pt(x+cw+gutter, top))
	return c.Constrain(geom.Sz(w, h))
}

// Paint implements [gunim.Node].
func (fr *frame) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(widget.Background.Get(f.Theme)))
	for i := range kids.Len() {
		kids.At(i).Paint(p)
	}
}

// page is a tab's content: inset by [PagePadding], no wider than
// [PageWidth], in the middle of the room it is given. With sides, it is
// inset at the sides alone, as a group of All values is.
type page struct {
	child gunim.Node
	sides bool
}

// Children implements [gunim.Composite].
func (pg *page) Children() []gunim.Node { return []gunim.Node{pg.child} }

// Layout implements [gunim.Node].
func (pg *page) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	th := f.Theme
	pad := PagePadding.Get(th)
	if pg.sides {
		pad.Top, pad.Bottom = 0, 0
	}
	w := c.Max.W
	inner := min(max(0, w-pad.Left-pad.Right), PageWidth.Get(th))
	maxH := float32(0)
	if c.Max.H > 0 {
		maxH = max(0, c.Max.H-pad.Top-pad.Bottom)
	}
	cs := gunim.Constraints{Min: geom.Sz(inner, 0), Max: geom.Sz(inner, maxH)}
	if c.Min.H > 0 {
		cs.Min.H = max(0, c.Min.H-pad.Top-pad.Bottom)
	}
	s := kids.At(0).Layout(cs)
	kids.At(0).Place(geom.Pt((w-inner)/2, pad.Top))
	return c.Constrain(geom.Sz(w, s.H+pad.Top+pad.Bottom))
}

// Paint implements [gunim.Node].
func (pg *page) Paint(p *paint.Painter, _ gunim.Frame, _ geom.Size, kids gunim.Children) {
	kids.At(0).Paint(p)
}

// same reports whether two values are equal.
func same(a, b any) bool { return reflect.DeepEqual(a, b) }
