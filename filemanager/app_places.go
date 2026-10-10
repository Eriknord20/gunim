package filemanager

import (
	"errors"
	"slices"

	"github.com/marrasen/gunim"
)

// handlePlaces takes the intents of the sidebar.
func (a *app) handlePlaces(in gunim.Intent) bool {
	switch v := in.(type) {
	case FavouritesReordered:
		favs := make([]Favourite, 0, len(v.Favourites))
		for _, at := range v.Favourites {
			f := Favourite{Path: at.Path, FS: at.FS}
			if i := a.favourite(at.FS, at.Path); i >= 0 {
				f = a.favs[i]
			}
			favs = append(favs, f)
		}
		a.setFavourites(favs)
	case SectionsArranged:
		a.arrangeSidebar(func(p *prefs) { p.SidebarOrder = keepAbsent(p.SidebarOrder, v.Order) })
	case SectionCollapsed:
		a.arrangeSidebar(func(p *prefs) {
			p.SidebarCollapsed = slices.DeleteFunc(slices.Clone(p.SidebarCollapsed), func(id string) bool { return id == v.ID })
			if v.Collapsed {
				p.SidebarCollapsed = append(p.SidebarCollapsed, v.ID)
			}
		})
	case Unpin:
		a.setFavourites(slices.DeleteFunc(slices.Clone(a.favs), func(f Favourite) bool { return a.isFav(f, v.FS, v.Path) }))
	case Visit:
		a.visit(v)
	case PlaceMenuAsked:
		var items []PlaceItem
		if a.opts.PlaceMenu != nil {
			items = a.opts.PlaceMenu(v.Place)
		}
		a.patch(PlaceMenuItems{Seq: v.Seq, Items: items})
	case ItemActed:
		if a.opts.ItemAction != nil && len(v.Paths) > 0 {
			go a.opts.ItemAction(a.win, a.fs.ID(), v.Paths, v.ID)
		}
	case PlaceCommanded:
		if a.opts.PlaceCommand != nil {
			go a.opts.PlaceCommand(a.win, v.Place, v.ID)
		}
	case Command:
		if v.Name != CmdPin {
			return false
		}
		a.pin()
	default:
		return false
	}
	return true
}

// pin adds the folders selected, or the folder showing, to the
// favourites.
func (a *app) pin() {
	var paths []string
	for _, e := range a.selectedEntries() {
		if e.Dir {
			paths = append(paths, a.ps.Join(a.nav.path, e.Name))
		}
	}
	if len(paths) == 0 {
		paths = []string{a.nav.path}
	}
	a.addFavourites(paths)
}

// visit goes to a place on another file system, as the options say, on a
// goroutine of its own, as it may open a window. With Ctrl held, as for a
// folder of the window's own, it asks for a new window.
func (a *app) visit(v Visit) {
	if a.opts.Visit == nil {
		a.fail(v.Path + " is on another file system, which this window cannot show.")
		return
	}
	newWindow := v.NewWindow || a.newWindowAsked()
	if !newWindow {
		// The user went elsewhere: a step through the history still on
		// its way is not taken, and the Show asked for now is.
		a.nav.hop, a.nav.dropped = nil, nil
	}
	go a.opts.Visit(a.win, v.FS, v.Path, newWindow)
}

// loadPlaces finds the places in the background, as a drive can be slow
// to answer.
func (a *app) loadPlaces() {
	find := a.opts.Places
	if find == nil {
		find = a.defaultPlaces
	}
	a.placesGen++
	gen := a.placesGen
	go func() {
		ps, err := find()
		a.post(func() {
			if gen != a.placesGen {
				// A later lookup has begun, and has the last word.
				return
			}
			if err != nil {
				a.fail("Finding the places for the sidebar: " + err.Error())
			}
			a.logPlaceErrs(ps)
			a.places = ps
			a.publishPlaces()
		})
	}()
}

// defaultPlaces are the places of a window whose options give none: the
// home folder and the drives of the computer, for its own file
// system, and the home folder and the top for another.
func (a *app) defaultPlaces() ([]Place, error) {
	if a.fs.ID() == "" {
		return LocalPlaces()
	}
	home, err := a.fs.Home()
	if err != nil {
		return nil, err
	}
	top := a.ps.VolumeName(home) + a.ps.Sep()
	out := []Place{{Name: "Home", Path: home, Kind: "home", FS: a.fs.ID()},
		{Name: a.ps.placeName(top), Path: top, Kind: "drive", FS: a.fs.ID()}}
	if sr, ok := a.fs.(SpaceReporter); ok {
		if free, total, err := sr.Space(top); errors.Is(err, errors.ErrUnsupported) {
			// The file system cannot say after all.
		} else if err != nil {
			out[1].Err = rootCause(err).Error()
		} else {
			out[1].Free, out[1].Total = free, total
		}
	}
	return out, nil
}

// arrangeSidebar makes change to the order of the sidebar's sections or
// to those closed, saves it, and shows it in this window and in every
// window that keeps its settings where this one does.
func (a *app) arrangeSidebar(change func(p *prefs)) {
	a.savePrefs(change)
	a.publishPlaces()
	order, closed, path := slices.Clone(a.prefs.SidebarOrder), slices.Clone(a.prefs.SidebarCollapsed), a.prefsPath
	a.hub.others(a, func(o *app) {
		if o.prefsPath == path {
			o.prefs.SidebarOrder, o.prefs.SidebarCollapsed = slices.Clone(order), slices.Clone(closed)
			o.publishPlaces()
		}
	})
}

// keepAbsent returns order, the sections shown in their new order, with
// the sections of was not shown now put back after the section they
// came after, so a group that comes back, such as a server's, comes
// back where it was.
func keepAbsent(was, order []string) []string {
	out := slices.Clone(order)
	for i, id := range was {
		if slices.Contains(out, id) {
			continue
		}
		at := 0
		for j := i - 1; j >= 0; j-- {
			if k := slices.Index(out, was[j]); k >= 0 {
				at = k + 1
				break
			}
		}
		out = slices.Insert(out, at, id)
	}
	return out
}

func (a *app) publishPlaces() {
	s := Places{Places: a.places, Favourites: a.favPlaces(), Current: a.nav.path,
		Order: slices.Clone(a.prefs.SidebarOrder), Collapsed: slices.Clone(a.prefs.SidebarCollapsed)}
	a.patch(s)
	a.publishVolumes(s)
}

// logPlaceErrs tells Options.Log of each place in ps the sidebar shows
// a failure in, which it did not show the last time: the places are
// found again each time the window gets the keyboard back.
func (a *app) logPlaceErrs(ps []Place) {
	was := map[string]string{}
	for _, p := range a.places {
		was[p.FS+"\x00"+p.Path] = p.Err
	}
	for _, p := range ps {
		if p.Err != "" && was[p.FS+"\x00"+p.Path] != p.Err {
			a.logLine("Reading "+p.Name, p.Err)
		}
	}
}
