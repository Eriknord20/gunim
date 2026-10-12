package filemanager

import (
	"strings"
	"time"
)

// scriptPause is how long each step of a script waits for the one
// before it to show.
const scriptPause = 700 * time.Millisecond

// runScript runs the next step of the script from -do, and the rest after
// it, each after a pause. A step is a command's name, "into:name" to go
// into a folder, "select:name" to select an item, "filter:text" to filter,
// "palette:text" to open the palette with text typed, "theme:light" or
// "theme:dark" to switch the theme, "big-copy" or
// "big-copy:MB a second" to copy a 2 GB file slowly,
// "edit-favourite:name" to open the Edit favourite dialog of the
// favourite of that name, or "wait" to do nothing.
func (a *app) runScript() {
	time.AfterFunc(scriptPause, func() {
		a.post(func() {
			if len(a.script) == 0 {
				return
			}
			step := a.script[0]
			a.script = a.script[1:]
			a.scriptStep(step)
			a.runScript()
		})
	})
}

func (a *app) scriptStep(step string) {
	verb, arg, _ := strings.Cut(step, ":")
	switch verb {
	case "into":
		a.navigate(a.ps.Join(a.nav.path, arg), 1, true)
	case "select":
		clear(a.nav.sel)
		a.nav.sel[arg] = true
		a.nav.cursor = arg
		a.showSelection()
		a.publishStatus()
		a.showPreview()
	case "filter":
		a.handle(a.handlers, FilterChanged{Text: arg})
	case "theme":
		a.handle(a.handlers, Command{Name: "theme." + arg})
	case "big-copy":
		a.bigCopy(arg)
	case "palette":
		a.patch(OpenPalette{Query: arg})
	case "edit-favourite":
		for _, f := range a.favs {
			if a.favName(f.FS, f.Path) == arg {
				a.editFavourite(f.FS, f.Path)
				return
			}
		}
	case "wait":
	default:
		if a.scriptDnd(verb, arg) {
			return
		}
		a.handle(a.handlers, Command{Name: step})
	}
}
