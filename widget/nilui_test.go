package widget

import (
	"image/color"
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
)

// Every setter that takes the UI also takes nil, as a view does when it builds a widget before mounting it: the
// state is set at once, and the first layout shows it.
func TestEverySetterTakesANilUI(t *testing.T) {
	var u *gunim.UI
	sets := map[string]func(){
		"TextArea.SetText":     func() { NewTextArea().SetText("a\nb", u) },
		"AddressBar.SetPath":   func() { NewAddressBar().SetPath("/a/b", []Crumb{{Name: "a"}}, u) },
		"CodeEditor.SetText":   func() { NewCodeEditor().SetText("x := 1", u) },
		"CodeEditor.SetMarks":  func() { NewCodeEditor().SetMarks(nil, u) },
		"Checkbox.SetChecked":  func() { NewCheckbox("c").SetChecked(true, u) },
		"Slider.SetValue":      func() { NewSlider(0, 10).SetValue(5, u) },
		"Tabs.SetSelected":     func() { NewTabs([]string{"a", "b"}, newSpot(1, 1), newSpot(1, 1)).SetSelected(1, u) },
		"DataGrid.SetRows":     func() { NewDataGrid(GridColumn{Title: "a"}).SetRows(10, u) },
		"DataGrid.SetSelected": func() { g := NewDataGrid(GridColumn{Title: "a"}); g.SetRows(10, u); g.SetSelected(3, u) },
		"DataGrid.SetSelectedRows": func() {
			g := NewDataGrid(GridColumn{Title: "a"})
			g.SetRows(10, u)
			g.SetSelectedRows([][2]int{{1, 3}}, 1, u)
		},
		"Drawer.SetOpen":          func() { NewDrawer(newSpot(1, 1), newSpot(1, 1)).SetOpen(true, u) },
		"TextField.SetText":       func() { NewTextField().SetText("t", u) },
		"Histogram.SetCounts":     func() { NewHistogram().SetCounts([3][256]uint32{}, u) },
		"Fold.SetOpen":            func() { NewFold(newSpot(1, 1), false).SetOpen(true, u) },
		"Image.SetSource":         func() { NewImage(nil).SetSource(nil, u) },
		"NumberField.SetValue":    func() { NewNumberField(0, 10).SetValue(5, u) },
		"ProgressBar.SetValue":    func() { NewProgressBar().SetValue(0.5, u) },
		"Palette.SetItems":        func() { NewPalette().SetItems([]PaletteItem{{Title: "a"}}, u) },
		"Palette.SetStatus":       func() { NewPalette().SetStatus("s", u) },
		"Palette.SetQuery":        func() { NewPalette().SetQuery("q", u) },
		"SliderRow.SetActive":     func() { NewSliderRow("s", NewSlider(0, 1)).SetActive(true, u) },
		"Table.SetKeys":           func() { NewTable(TableColumn{Title: "a"}).SetKeys([]Key{"a"}, u) },
		"Table.SetCursor":         func() { tb := NewTable(TableColumn{Title: "a"}); tb.SetKeys([]Key{"a"}, u); tb.SetCursor("a", u) },
		"Segmented.SetSelected":   func() { NewSegmented("a", "b").SetSelected(1, u) },
		"Tree.SetKeys":            func() { NewTree().SetKeys([]Key{"a"}, u) },
		"Tree.SetCursor":          func() { tr := NewTree(); tr.SetKeys([]Key{"a"}, u); tr.SetCursor("a", u) },
		"Split.SetPane":           func() { NewSplit(newSpot(1, 1), newSpot(1, 1)).SetPane(0, newSpot(2, 2), u) },
		"Split.SetShare":          func() { NewSplit(newSpot(1, 1), newSpot(1, 1)).SetShare(0.3, u) },
		"VirtualList.SetKeys":     func() { NewVirtualList(func(Key) gunim.Node { return newSpot(1, 1) }).SetKeys([]Key{"a"}, u) },
		"TileGrid.SetLen":         func() { NewTileGrid(geom.Sz(10, 10)).SetLen(5, u) },
		"TileGrid.SetSelected":    func() { g := NewTileGrid(geom.Sz(10, 10)); g.SetLen(5, u); g.SetSelected([][2]int{{1, 2}}, 1, u) },
		"TileGrid.ShowTile":       func() { g := NewTileGrid(geom.Sz(10, 10)); g.SetLen(5, u); g.ShowTile(3, u) },
		"TileGrid.Reshape":        func() { NewTileGrid(geom.Sz(10, 10)).Reshape(u) },
		"ToneCurve.SetPoints":     func() { NewToneCurve().SetPoints([]geom.Point{{X: 0, Y: 0}, {X: 1, Y: 1}}, u) },
		"Dropdown.SetSelected":    func() { NewDropdown(Labels("a", "b")).SetSelected(1, u) },
		"ColorPicker.SetValue":    func() { NewColorPicker(color.NRGBA{}).SetValue(color.NRGBA{R: 1}, u) },
		"ColorPicker.SetOriginal": func() { NewColorPicker(color.NRGBA{}).SetOriginal(color.NRGBA{R: 1}, u) },
		"ColorButton.SetValue":    func() { NewColorButton(color.NRGBA{}).SetValue(color.NRGBA{R: 1}, u) },
	}
	for name, set := range sets {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panicked with a nil UI: %v", r)
				}
			}()
			set()
		})
	}
}

func TestAnAddressBarShowsThePlacesSetBeforeItWasMounted(t *testing.T) {
	a := NewAddressBar()
	a.SetPath("/home/ada", []Crumb{{Name: "home", Path: "/home"}, {Name: "ada", Path: "/home/ada"}}, nil)
	w, run := stage(t, &frame{child: a, size: geom.Sz(400, 40)})
	u := stageUI(t, w, run)
	run(2)
	places := a.Places(u)
	if len(places) != 2 {
		t.Fatalf("the bar holds %d places, want the 2 set before it was mounted", len(places))
	}
	for _, pl := range places {
		if pl.Rect.Empty() {
			t.Fatalf("the place %q set before the bar was mounted is not laid out", pl.Name)
		}
	}
}
