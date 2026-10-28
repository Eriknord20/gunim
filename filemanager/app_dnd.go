package filemanager

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/input"
)

// dndState is what the app knows for drops: the volume of each folder a
// window can drop on, and why a volume could not be read.
type dndState struct {
	vols    map[string]string
	volErrs map[string]string
	// finding is set while volumes are being read, and again when more
	// were asked for meanwhile.
	finding, again bool
	// fetches stop the fetches of the drags going on, by the drags' IDs.
	fetches map[int]context.CancelFunc
}

// handleDnd takes the intents of drag and drop, the context menus and
// the windows.
func (a *app) handleDnd(in gunim.Intent) bool {
	switch v := in.(type) {
	case DropFiles:
		a.dropFiles(v)
	case DragFetch:
		a.fetchForDrag(v)
	case DragFetchEnd:
		a.dragFetchEnded(v.ID)
	case PinFolders:
		a.pinFolders(v.Paths)
	case OpenWindow:
		a.openWindow(v.Path)
	case EditFavourite:
		a.editFavourite(v.FS, v.Path)
	case RenameFavourite:
		a.editFavourite(v.FS, v.Path)
	case PropsApplied:
		a.answered(v)
	case OpenWithAsked:
		a.askedOpenWith(v.Path)
	case OpenWith:
		a.openWith(v)
	case Command:
		switch v.Name {
		case CmdOpenSystem:
			a.openSystem()
		case CmdDuplicate:
			if a.readOnlyHere(v.Name) {
				return true
			}
			if paths := a.selectedPaths(); len(paths) > 0 {
				a.startOp(job{kind: OpCopy, srcs: paths, dest: a.nav.path}, "Duplicating "+a.what(paths))
			}
		case CmdProperties:
			a.properties()
		case CmdNewWindow:
			a.openWindow(a.nav.path)
		default:
			return false
		}
	default:
		return false
	}
	return true
}

// dropFiles moves or copies what was dropped into the folder it was
// dropped on, as an operation with progress and undo. Items from another
// file system go to the program to carry across.
func (a *app) dropFiles(v DropFiles) {
	if len(v.Paths) == 0 || v.Into == "" {
		return
	}
	if !v.Away && v.To != a.fs.ID() {
		a.fail("The window went to another file system before the drop, so nothing was dropped.")
		return
	}
	if v.To == a.fs.ID() {
		if at := a.archiveOf(v.Into, true); at != "" {
			a.refuseArchive(at)
			return
		}
	}
	if !v.Copy && v.FS == a.fs.ID() && a.anyInArchive(v.Paths) {
		// What an archive holds stays in it: a move out of it copies.
		v.Copy = true
	}
	if v.FS != a.fs.ID() || v.To != a.fs.ID() {
		if a.opts.Transfer == nil {
			a.fail("Items cannot go between file systems yet.")
			return
		}
		// Files from another program are the computer's own, written as
		// it writes paths, which Style is by default.
		a.transfer(v.FS, v.Style, v.Paths, v.To, v.Into, !v.Copy)
		return
	}
	for _, p := range v.Paths {
		if a.ps.Same(a.ps.Dir(p), v.Into) {
			a.fail(a.ps.Base(p) + " is already in " + a.ps.placeName(v.Into) + ".")
			return
		}
		if err := into(a.fs, p, v.Into); err != nil {
			a.fail(err.Error() + ".")
			return
		}
	}
	srcs := slices.Clone(v.Paths)
	if v.Copy {
		a.startOp(job{kind: OpCopy, srcs: srcs, dest: v.Into}, "Copying "+a.what(srcs)+" to "+a.ps.placeName(v.Into))
		return
	}
	a.startOp(job{kind: OpMove, srcs: srcs, dest: v.Into}, "Moving "+a.what(srcs)+" to "+a.ps.placeName(v.Into))
}

// pinFolders adds the folders among paths to the favourites, once each
// is read, and says which items were left out as not folders.
func (a *app) pinFolders(paths []string) {
	paths = slices.Clone(paths)
	fsys := a.fs
	go func() {
		var dirs, files []string
		for _, p := range paths {
			info, err := fsys.Stat(p)
			if err != nil {
				a.post(func() { a.fail("Pinning to favourites: " + err.Error()) })
				return
			}
			if info.IsDir() {
				dirs = append(dirs, p)
			} else {
				files = append(files, fsys.Paths().Base(p))
			}
		}
		a.post(func() {
			added := a.addFavourites(dirs)
			n := Notice{Title: "Pinned " + plural(added, "folder"), Kind: "success"}
			if len(files) > 0 {
				n.Body = strings.Join(files, ", ") + " left out: only folders can be pinned."
			}
			a.patch(n)
		})
	}()
}

// editFavourite asks for a name, a colour and an icon for the favourite
// at path on the file system of ID fs, in the Edit favourite dialog.
func (a *app) editFavourite(fs, path string) {
	i := a.favourite(fs, path)
	if i < 0 {
		return
	}
	f := a.favs[i]
	a.ops.tokens++
	e := FavouriteEdit{Token: a.ops.tokens, Name: a.favName(fs, path), Folder: a.folderName(fs, path), Color: f.Color,
		Icon: f.Icon}
	a.showDialog(&dialog{view: "favourite", state: e, answer: func(in gunim.Intent) {
		v, ok := in.(FavouriteEdited)
		i := a.favourite(fs, path)
		if !ok || !v.OK || i < 0 {
			return
		}
		n := strings.TrimSpace(v.Name)
		if n == a.folderName(fs, path) {
			n = ""
		}
		favs := slices.Clone(a.favs)
		favs[i].Name, favs[i].Color, favs[i].Icon = n, known(FavouriteColors, v.Color), known(FavouriteIcons, v.Icon)
		a.setFavourites(favs)
	}})
}

// known returns name if it is one of names, and else empty.
func known(names []string, name string) string {
	if slices.Contains(names, name) {
		return name
	}
	return ""
}

// openSystem opens the items selected with the system's programs, or
// the folder showing when none is selected.
func (a *app) openSystem() {
	paths := a.selectedPaths()
	if len(paths) == 0 {
		paths = []string{a.nav.path}
	}
	a.openFiles(paths)
}

// publishVolumes finds the volume of each folder in s the window can
// drop on, in the background, and sends them: the folder showing and
// the folders along its path, the links to folders in it, the places
// and the favourites.
func (a *app) publishVolumes(s Places) {
	d := &a.dnd
	if d.vols == nil {
		d.vols, d.volErrs = map[string]string{}, map[string]string{}
	}
	var missing []string
	add := func(p string) {
		if _, ok := d.vols[p]; !ok && p != "" && !slices.Contains(missing, p) {
			missing = append(missing, p)
		}
	}
	add(s.Current)
	if s.Current != "" {
		for _, c := range crumbs(a.ps, s.Current) {
			add(c.Path)
		}
	}
	if a.ps.Same(s.Current, a.nav.path) {
		for _, e := range a.nav.all {
			if e.Kind == KindLink && e.Dir {
				add(a.ps.Join(s.Current, e.Name))
			}
		}
	}
	for _, p := range slices.Concat(s.Places, s.Favourites) {
		if p.FS == a.fs.ID() && p.Path != TrashPath {
			add(p.Path)
		}
	}
	if len(missing) > 0 && d.finding {
		d.again = true
	}
	if len(missing) == 0 || d.finding {
		a.patch(Volumes{Of: cloneNames(d.vols), Errs: cloneNames(d.volErrs)})
		return
	}
	d.finding = true
	fsys := a.fs
	go func() {
		vols, errs := map[string]string{}, map[string]string{}
		for _, p := range missing {
			v, err := volumeName(fsys, p)
			if err != nil {
				errs[p] = err.Error()
				continue
			}
			vols[p] = v
		}
		a.post(func() {
			d.finding = false
			if fsys.ID() != a.fs.ID() {
				// The window turned to another file system meanwhile.
				a.publishPlaces()
				return
			}
			for p, v := range vols {
				d.vols[p] = v
				delete(d.volErrs, p)
			}
			for p, e := range errs {
				d.volErrs[p] = e
			}
			a.patch(Volumes{Of: cloneNames(d.vols), Errs: cloneNames(d.volErrs)})
			if d.again {
				d.again = false
				a.publishPlaces()
			}
		})
	}()
}

// scriptDnd runs a script's step of drag and drop, and reports whether
// it knew the step: drag-start, drag-over and menu name an item or
// place, with +ctrl or +shift after the name to hold the key, drop lets
// go, and add adds an item to the selection.
func (a *app) scriptDnd(verb, arg string) bool {
	name, key, _ := strings.Cut(arg, "+")
	mods := map[string]input.Mods{"ctrl": input.ModControl, "shift": input.ModShift}[key]
	step := map[string]string{"drag-start": "start", "drag-over": "over", "drop": "drop", "menu": "menu"}[verb]
	switch {
	case step != "":
		a.patch(ScriptDrag{Step: step, Name: name, Mods: int(mods)})
	case verb == "add":
		a.nav.sel[arg] = true
		a.publishSelection()
		a.publishStatus()
		a.showPreview()
	default:
		return false
	}
	return true
}

// volumeName names the volume the folder at path on fsys is on: the file
// system's own name for it, or for a file system of one volume its ID.
func volumeName(fsys FS, path string) (string, error) {
	if v, ok := optional[VolumeNamer](fsys); ok {
		name, err := v.Volume(path)
		if !errors.Is(err, errors.ErrUnsupported) {
			return name, err
		}
	}
	return fsys.ID(), nil
}

// systemOpen returns what opens a file or a folder with the system's
// programs: the window, for the computer's own files, or the file system,
// for one that can open its files there. Where neither can, it says so,
// and ok is false.
func (a *app) systemOpen() (open func(path string) error, ok bool) {
	if a.fs.ID() == "" {
		return a.c.Open, true
	}
	if so, ok := optional[SystemOpener](a.fs); ok {
		return so.OpenInSystem, true
	}
	a.fail("Files here cannot open with this computer's programs.")
	return nil, false
}

// systemReveal returns what shows an item in the system's file manager,
// as systemOpen does for opening it.
func (a *app) systemReveal() (reveal func(path string) error, ok bool) {
	if a.fs.ID() == "" {
		if at := a.nav.archive; at != "" {
			// What an archive holds shows as the archive.
			return func(string) error { return a.c.Reveal(at) }, true
		}
		return a.c.Reveal, true
	}
	if so, ok := optional[SystemOpener](a.fs); ok {
		return so.RevealInSystem, true
	}
	a.fail("Items here cannot show in this computer's file manager.")
	return nil, false
}
