package desktop

import (
	"fmt"
	"strings"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/internal/glfw"
)

// ChooseFiles implements [driver.FileChooser] with the system's open
// panel, shown as a sheet on the window.
func (w *Window) ChooseFiles(o driver.ChooseOptions) ([]string, error) {
	return w.showPanel(glfw.FileDialog{Message: o.Title, Folder: o.Folder, Folders: o.Folders,
		Multiple: o.Multiple, Extensions: extensions(o.Filters)})
}

// SaveFile implements [driver.FileSaver] with the system's save panel,
// shown as a sheet on the window. The panel asks before it lets an
// existing file be chosen.
func (w *Window) SaveFile(o driver.SaveOptions) (string, error) {
	paths, err := w.showPanel(glfw.FileDialog{Save: true, Message: o.Title, Name: o.Name, Folder: o.Folder,
		Extensions: extensions(o.Filters)})
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}

// showPanel shows d on the main thread and waits for it to close. The
// main thread goes on drawing and hearing input while it shows.
func (w *Window) showPanel(d glfw.FileDialog) ([]string, error) {
	chosen := make(chan []string, 1)
	if err := w.d.call(func() error {
		return w.gw.ShowFileDialog(d, func(paths []string) { chosen <- paths })
	}); err != nil {
		return nil, fmt.Errorf("desktop: showing a file dialog: %w", err)
	}
	return <-chosen, nil
}

// extensions returns the extensions the filters' patterns name, as "hej"
// for "*.hej" and "gz" for "*.tar.gz". The panel offers no choice of
// filter, so it shows the files of every one. A pattern other than
// "*.ext", such as "*", shows every file.
func extensions(filters []driver.FileFilter) []string {
	var out []string
	for _, f := range filters {
		for _, p := range f.Patterns {
			e, ok := strings.CutPrefix(p, "*.")
			if !ok || e == "" || strings.ContainsAny(e, "*?[") {
				return nil
			}
			if i := strings.LastIndexByte(e, '.'); i >= 0 {
				e = e[i+1:]
			}
			if e == "" {
				return nil
			}
			out = append(out, e)
		}
	}
	return out
}
