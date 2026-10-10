// Package callbackshape holds a test that every callback of widget, calendar and audioui has the one shape. It
// lives apart from package widget so that its test binary alone loads calendar and audioui, and their theme tokens.
package callbackshape_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/calendar"
	"github.com/marrasen/gunim/widget"
)

// structs are the exported struct types of widget, calendar and audioui. TestEveryStructIsListed keeps the list
// whole, from the packages' source.
var structs = []any{
	(*widget.AddressBar)(nil),
	(*widget.AddressLead)(nil),
	(*widget.AddressPlace)(nil),
	(*widget.BarMenu)(nil),
	(*widget.Button)(nil),
	(*widget.Card)(nil),
	(*widget.Cell)(nil),
	(*widget.CellGrid)(nil),
	(*widget.Checkbox)(nil),
	(*widget.Chip)(nil),
	(*widget.Control)(nil),
	(*widget.Clicker)(nil),
	(*widget.CodeEditor)(nil),
	(*widget.CodeMark)(nil),
	(*widget.ColorButton)(nil),
	(*widget.ColorPicker)(nil),
	(*widget.Completion)(nil),
	(*widget.ContextMenu)(nil),
	(*widget.Crumb)(nil),
	(*widget.Cursor)(nil),
	(*widget.CurveGuide)(nil),
	(*widget.DataGrid)(nil),
	(*widget.Dialog)(nil),
	(*widget.DragGhost)(nil),
	(*widget.Draggable)(nil),
	(*widget.Drawer)(nil),
	(*widget.DropHint)(nil),
	(*widget.DropSpot)(nil),
	(*widget.DropTarget)(nil),
	(*widget.DropZone)(nil),
	(*widget.Dropdown)(nil),
	(*widget.Echo)(nil),
	(*widget.EmojiPicker)(nil),
	(*widget.Flex)(nil),
	(*widget.Fold)(nil),
	(*widget.Form)(nil),
	(*widget.GridColumn)(nil),
	(*widget.GridRow)(nil),
	(*widget.GridSpan)(nil),
	(*widget.Group)(nil),
	(*widget.Hero)(nil),
	(*widget.Histogram)(nil),
	(*widget.Icon)(nil),
	(*widget.IconButton)(nil),
	(*widget.Image)(nil),
	(*widget.Label)(nil),
	(*widget.Link)(nil),
	(*widget.List)(nil),
	(*widget.LiveGraph)(nil),
	(*widget.Menu)(nil),
	(*widget.MenuButton)(nil),
	(*widget.MenuItem)(nil),
	(*widget.Menubar)(nil),
	(*widget.NumberField)(nil),
	(*widget.Overview)(nil),
	(*widget.OverviewBand)(nil),
	(*widget.OverviewPart)(nil),
	(*widget.Pad)(nil),
	(*widget.Palette)(nil),
	(*widget.PaletteItem)(nil),
	(*widget.PartTip)(nil),
	(*widget.ProgressBar)(nil),
	(*widget.RichSpan)(nil),
	(*widget.RichText)(nil),
	(*widget.Scroll)(nil),
	(*widget.Segmented)(nil),
	(*widget.Sized)(nil),
	(*widget.Slider)(nil),
	(*widget.SliderRow)(nil),
	(*widget.Spacer)(nil),
	(*widget.Split)(nil),
	(*widget.Submenu)(nil),
	(*widget.Surface)(nil),
	(*widget.Switch)(nil),
	(*widget.Table)(nil),
	(*widget.TableColumn)(nil),
	(*widget.TableRow)(nil),
	(*widget.Tabs)(nil),
	(*widget.TextArea)(nil),
	(*widget.TextField)(nil),
	(*widget.Themed)(nil),
	(*widget.TileGrid)(nil),
	(*widget.TitleBar)(nil),
	(*widget.Toast)(nil),
	(*widget.ToastButton)(nil),
	(*widget.Toasts)(nil),
	(*widget.ToneCurve)(nil),
	(*widget.Toolbar)(nil),
	(*widget.Tooltip)(nil),
	(*widget.Tree)(nil),
	(*widget.TreeItem)(nil),
	(*widget.TypeAhead)(nil),
	(*widget.VirtualList)(nil),
	(*widget.WindowControls)(nil),
	(*widget.WindowTitle)(nil),
	(*widget.Wrap)(nil),
	(*calendar.DateField)(nil),
	(*calendar.Days)(nil),
	(*calendar.Event)(nil),
	(*calendar.MiniMonth)(nil),
	(*calendar.Month)(nil),
	(*calendar.TimeField)(nil),
	(*audioui.CurveView)(nil),
	(*audioui.Curves)(nil),
	(*audioui.Fader)(nil),
	(*audioui.Fling)(nil),
	(*audioui.Gram)(nil),
	(*audioui.GramScan)(nil),
	(*audioui.GramTiles)(nil),
	(*audioui.Levels)(nil),
	(*audioui.Loudness)(nil),
	(*audioui.Samples)(nil),
	(*audioui.Scope)(nil),
	(*audioui.Spectrogram)(nil),
	(*audioui.Spectrometer)(nil),
	(*audioui.Spectrum)(nil),
	(*audioui.Wave)(nil),
	(*audioui.WaveLevel)(nil),
	(*audioui.WaveScan)(nil),
	(*audioui.WaveView)(nil),
}

// dirs are the packages' directories, from this one, by their names.
var dirs = map[string]string{"widget": "..", "calendar": "../../calendar", "audioui": "../../audioui"}

func TestEveryStructIsListed(t *testing.T) {
	listed := map[string]bool{}
	for _, s := range structs {
		listed[reflect.TypeOf(s).Elem().String()] = true
	}
	for pkg, dir := range dirs {
		for _, name := range exportedStructs(t, dir) {
			if !listed[pkg+"."+name] {
				t.Errorf("%s.%s is not in the list of structs the callbacks are checked on", pkg, name)
			}
		}
	}
}

// exportedStructs returns the names of the exported struct types declared in the Go files of dir, tests left out.
func exportedStructs(t *testing.T, dir string) []string {
	t.Helper()
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	fs := token.NewFileSet()
	for _, fi := range files {
		if fi.IsDir() || !strings.HasSuffix(fi.Name(), ".go") || strings.HasSuffix(fi.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fs, filepath.Join(dir, fi.Name()), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			g, ok := d.(*ast.GenDecl)
			if !ok || g.Tok != token.TYPE {
				continue
			}
			for _, s := range g.Specs {
				ts, ok := s.(*ast.TypeSpec)
				if !ok {
					continue
				}
				if _, isStruct := ts.Type.(*ast.StructType); isStruct && ts.Name.IsExported() && ts.TypeParams == nil {
					out = append(out, ts.Name.Name)
				}
			}
		}
	}
	return out
}

// TestEveryCallbackHasOneShape checks every exported field named On or On followed by a capital, promoted ones too:
// it is a func whose last parameter is the UI and whose only result is an intent.
func TestEveryCallbackHasOneShape(t *testing.T) {
	ui, intent := reflect.TypeFor[*gunim.UI](), reflect.TypeFor[gunim.Intent]()
	for _, s := range structs {
		ty := reflect.TypeOf(s).Elem()
		for _, f := range reflect.VisibleFields(ty) {
			if !f.IsExported() || !isOn(f.Name) {
				continue
			}
			ft := f.Type
			if ft.Kind() != reflect.Func || ft.NumIn() == 0 || ft.In(ft.NumIn()-1) != ui || ft.NumOut() != 1 ||
				ft.Out(0) != intent || ft.IsVariadic() {
				t.Errorf("%s.%s is %s, want func(…, *gunim.UI) gunim.Intent", ty, f.Name, ft)
			}
		}
	}
}

// isOn reports whether name is On, or On and then a word.
func isOn(name string) bool {
	rest, ok := strings.CutPrefix(name, "On")
	r, _ := utf8.DecodeRuneInString(rest)
	return ok && (rest == "" || unicode.IsUpper(r))
}
