package filemanager

import (
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
)

// Favourite is a folder the user pinned to the sidebar.
type Favourite struct {
	Path string
	// Name is the name the user gave it, and empty for the folder's own.
	Name string
	// FS is the ID of the file system the folder is on. A store that is
	// not an AnyFSFavourites leaves it empty, and its favourites are on
	// the window's file system.
	FS string
	// Color is the favourite's colour, by name: one of FavouriteColors;
	// empty draws the folder colour.
	Color string
	// Icon is the favourite's icon, by name: one of FavouriteIcons;
	// empty draws a folder.
	Icon string
}

// FavouriteColors are the colours a favourite can take, by name, in the
// order the Edit favourite dialog offers them.
var FavouriteColors = []string{"red", "orange", "yellow", "green", "teal", "blue", "indigo", "purple", "pink", "gray"}

// FavouriteIcons are the icons a favourite can take, by name, in the
// order the Edit favourite dialog offers them. The names are Lucide's,
// as the icon package has them.
var FavouriteIcons = []string{
	"folder", "house", "monitor", "file-text", "download", "image", "music", "video",
	"code", "briefcase", "book", "star", "heart", "cloud", "server", "database",
	"archive", "camera", "gamepad", "globe", "graduation-cap", "wrench", "flask-conical", "terminal",
}

// defaultLooks are the icon and the colour of each of the user's own
// folders, by its kind, as DefaultFavourites gives them.
var defaultLooks = map[string][2]string{
	"desktop":   {"monitor", "teal"},
	"documents": {"file-text", "blue"},
	"downloads": {"download", "green"},
	"pictures":  {"image", "purple"},
	"music":     {"music", "pink"},
	"videos":    {"video", "orange"},
}

// DefaultFavourites are the favourites a new user starts with, on the
// computer's own file system: Desktop, Documents, Downloads, Pictures,
// Music and Videos, those that exist, each with an icon and a colour.
func DefaultFavourites() []Favourite {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	folders, err := userFolders(home)
	if err != nil {
		return nil
	}
	var out []Favourite
	for _, f := range folders {
		if info, err := os.Stat(f.path); err != nil || !info.IsDir() {
			continue
		}
		look := defaultLooks[f.kind]
		out = append(out, Favourite{Path: f.path, Icon: look[0], Color: look[1]})
	}
	return out
}

// defaultFavourites are the favourites the settings file starts with:
// DefaultFavourites, unless the options say otherwise, as for tests.
func (a *app) defaultFavourites() []Favourite {
	if a.opts.defaults != nil {
		return a.opts.defaults()
	}
	return DefaultFavourites()
}

// nextColor is the colour for a favourite added after favs: the one used
// least, and of those the first after the colour of the last favourite,
// so favourites added one after another take colours of their own.
func nextColor(favs []Favourite) string {
	uses := map[string]int{}
	for _, f := range favs {
		uses[f.Color]++
	}
	start := 0
	if len(favs) > 0 {
		start = slices.Index(FavouriteColors, favs[len(favs)-1].Color) + 1
	}
	best := ""
	for i := range FavouriteColors {
		c := FavouriteColors[(start+i)%len(FavouriteColors)]
		if best == "" || uses[c] < uses[best] {
			best = c
		}
	}
	return best
}

// FavouriteStore keeps a window's favourites between runs. A window
// loads them as it opens and when its hub is told to refresh, and saves
// them whole each time the user pins, unpins, edits or reorders one.
// Its methods are called on the window's own goroutine, so they should
// be quick. Windows of a hub given the same store share its favourites
// as they change; a store that cannot be compared with ==, as a pointer
// can, is not shared, and a window's save may then undo another's. A
// store keeps the favourites of the window's file system, unless it is
// an [AnyFSFavourites]. It keeps each favourite's Name, Color and Icon as
// it is given them.
type FavouriteStore interface {
	Load() ([]Favourite, error)
	Save(favs []Favourite) error
}

// AnyFSFavourites is a FavouriteStore whose favourites may be on any file
// system, each naming its own in FS, empty for the computer's own. A
// window lists them all, each with the name of its file system under
// its own; one on another file system than the window's is shown as
// elsewhere, and a click on it asks for a Visit.
type AnyFSFavourites interface {
	FavouriteStore
	// Where names the file system of ID fs, as each favourite says
	// where it is, the window's own too.
	Where(fs string) string
}

// prefsFavourites keeps the favourites of a window on the computer's own
// file system in its settings file, as the store it has when its options
// give none.
type prefsFavourites struct{ a *app }

// Load implements [FavouriteStore]. The first time, it adds the
// default favourites, once, so those the user unpins stay unpinned.
func (s prefsFavourites) Load() ([]Favourite, error) {
	s.seed()
	return favouritesIn(&s.a.prefs), nil
}

// favouritesIn returns the favourites the settings p keep.
func favouritesIn(p *prefs) []Favourite {
	out := make([]Favourite, len(p.Favourites))
	for i, path := range p.Favourites {
		out[i] = Favourite{Path: path, Name: p.FavNames[path], Color: p.FavColors[path], Icon: p.FavIcons[path]}
	}
	return out
}

// seed adds the default favourites to the settings before the user's
// own, unless it was done before, and gives the user's own a colour
// each. Settings that could not be read are left as they are.
func (s prefsFavourites) seed() {
	if s.a.prefs.FavSeeded || s.a.prefsErr != nil {
		return
	}
	defaults := s.a.defaultFavourites()
	s.a.savePrefs(func(p *prefs) {
		if p.FavSeeded {
			return
		}
		p.FavSeeded = true
		favs := slices.Clone(defaults)
		for _, f := range favouritesIn(p) {
			// A default the user pinned already keeps the name they gave it.
			if i := slices.IndexFunc(favs, func(d Favourite) bool { return SystemPaths.Same(d.Path, f.Path) }); i >= 0 {
				if f.Name != "" {
					favs[i].Name = f.Name
				}
				continue
			}
			if f.Color == "" {
				f.Color = nextColor(favs)
			}
			favs = append(favs, f)
		}
		mergeFavourites(p, favs)
	})
}

// Save implements [FavouriteStore]. The name, colour and icon given to
// a favourite stay in the settings when it is unpinned, so it comes back
// with them. The settings say why they could not be saved themselves.
func (s prefsFavourites) Save(favs []Favourite) error {
	s.a.savePrefs(func(p *prefs) { mergeFavourites(p, favs) })
	return nil
}

// kept returns the favourite at path as it was before it was unpinned:
// its name, colour and icon, those it had.
func (s prefsFavourites) kept(path string) Favourite {
	p := &s.a.prefs
	return Favourite{Path: path, Name: p.FavNames[path], Color: p.FavColors[path], Icon: p.FavIcons[path]}
}

// mergeFavourites puts favs in the settings p: their paths, and the
// names, colours and icons given to them by path, keeping those of the
// favourites unpinned.
func mergeFavourites(p *prefs, favs []Favourite) {
	p.Favourites = make([]string, len(favs))
	for i, f := range favs {
		p.Favourites[i] = f.Path
	}
	p.FavNames = mergeByPath(p.FavNames, favs, func(f Favourite) string { return f.Name })
	p.FavColors = mergeByPath(p.FavColors, favs, func(f Favourite) string { return f.Color })
	p.FavIcons = mergeByPath(p.FavIcons, favs, func(f Favourite) string { return f.Icon })
}

// mergeByPath returns was with what of favs sets for each favourite's
// path, and without the paths of those for which of is empty.
func mergeByPath(was map[string]string, favs []Favourite, of func(f Favourite) string) map[string]string {
	out := cloneNames(was)
	for _, f := range favs {
		switch v := of(f); {
		case v != "":
			if out == nil {
				out = map[string]string{}
			}
			out[f.Path] = v
		case out != nil:
			delete(out, f.Path)
		}
	}
	return out
}

// memFavourites keeps the favourites of the windows of a hub on a file
// system other than the computer's own, for as long as the hub runs, as
// the store they have when their options give none: the settings file
// is the computer's own.
type memFavourites struct {
	mu   sync.Mutex
	favs []Favourite
}

// Load implements [FavouriteStore].
func (m *memFavourites) Load() ([]Favourite, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.favs), nil
}

// Save implements [FavouriteStore].
func (m *memFavourites) Save(favs []Favourite) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.favs = slices.Clone(favs)
	return nil
}

// favStore is the store of a's favourites.
func (a *app) favStore() FavouriteStore {
	switch {
	case a.opts.Favourites != nil:
		return a.opts.Favourites
	case a.fs.ID() == "":
		return prefsFavourites{a}
	}
	return a.hub.memFavourites(a.fs.ID())
}

// sharesFavourites reports whether a and o keep their favourites in the
// same store: the one their options give, the settings file, or the
// hub's store of their file system. A store that cannot be compared is
// shared with no other window.
func (a *app) sharesFavourites(o *app) bool {
	x, y := a.opts.Favourites, o.opts.Favourites
	switch {
	case x == nil && y == nil:
		return a.fs.ID() == o.fs.ID()
	case x == nil || y == nil:
		return false
	case reflect.TypeOf(x) != reflect.TypeOf(y) || !reflect.TypeOf(x).Comparable():
		return false
	}
	return x == y
}

// loadFavourites reads the favourites from their store.
func (a *app) loadFavourites() {
	favs, err := a.favStore().Load()
	if err != nil {
		a.fail("Reading the favourites: " + err.Error())
		return
	}
	a.favs = a.fromStore(favs)
}

// fromStore returns favs as loaded from a's store, each with its file
// system: those of a store that is not an AnyFSFavourites are on the
// window's.
func (a *app) fromStore(favs []Favourite) []Favourite {
	favs = slices.Clone(favs)
	if _, anyFS := a.favStore().(AnyFSFavourites); !anyFS {
		for i := range favs {
			favs[i].FS = a.fs.ID()
		}
	}
	return favs
}

// toStore returns favs as a's store keeps them: all of them for an
// AnyFSFavourites, and otherwise those on the window's file system, with
// FS empty, as such a store keeps them.
func (a *app) toStore(favs []Favourite) []Favourite {
	if _, anyFS := a.favStore().(AnyFSFavourites); anyFS {
		return slices.Clone(favs)
	}
	out := make([]Favourite, 0, len(favs))
	for _, f := range favs {
		if f.FS == a.fs.ID() {
			f.FS = ""
			out = append(out, f)
		}
	}
	return out
}

// setFavourites makes favs the favourites: it saves them, shows them,
// and has the windows that keep theirs in the same store show them too,
// each as it would load them.
func (a *app) setFavourites(favs []Favourite) {
	a.favs = slices.Clone(favs)
	saved := a.toStore(favs)
	if err := a.favStore().Save(slices.Clone(saved)); err != nil {
		a.fail("Saving the favourites: " + err.Error())
	}
	a.publishPlaces()
	a.hub.others(a, func(o *app) {
		if !a.sharesFavourites(o) {
			return
		}
		o.favs = o.fromStore(saved)
		if o.opts.Favourites == nil && o.fs.ID() == "" {
			mergeFavourites(&o.prefs, saved)
		}
		o.publishPlaces()
	})
}

// isFav reports whether f is the favourite at path on the file system
// of ID fs: paths on the window's file system compare as it writes them,
// and others as they are.
func (a *app) isFav(f Favourite, fs, path string) bool {
	switch {
	case f.FS != fs:
		return false
	case fs == a.fs.ID():
		return a.ps.Same(f.Path, path)
	}
	return f.Path == path
}

// favourite returns the index of the favourite at path on the file
// system of ID fs, or -1.
func (a *app) favourite(fs, path string) int {
	return slices.IndexFunc(a.favs, func(f Favourite) bool { return a.isFav(f, fs, path) })
}

// addFavourites adds the folders at paths on the window's file system
// that are not favourites yet, and returns how many it added. Each takes
// the name, colour and icon it had before it was unpinned, if the store
// kept them, or else a colour of its own, and the icon of the user's
// folder it is, if it is one.
func (a *app) addFavourites(paths []string) int {
	favs := slices.Clone(a.favs)
	added := 0
	var defaults []Favourite
	if a.fs.ID() == "" {
		defaults = a.defaultFavourites()
	}
	for _, p := range paths {
		if slices.ContainsFunc(favs, func(f Favourite) bool { return a.isFav(f, a.fs.ID(), p) }) {
			continue
		}
		f := Favourite{Path: p}
		if s, ok := a.favStore().(prefsFavourites); ok {
			f = s.kept(p)
		}
		if i := slices.IndexFunc(defaults, func(d Favourite) bool { return a.ps.Same(d.Path, p) }); i >= 0 && f.Icon == "" {
			f.Icon = defaults[i].Icon
		}
		if f.Color == "" {
			f.Color = nextColor(favs)
		}
		f.FS = a.fs.ID()
		favs = append(favs, f)
		added++
	}
	if added > 0 {
		a.setFavourites(favs)
	}
	return added
}

// favName is what the sidebar calls the favourite at path on the file
// system of ID fs: the name the user gave it, or the folder's.
func (a *app) favName(fs, path string) string {
	if i := a.favourite(fs, path); i >= 0 {
		if n := strings.TrimSpace(a.favs[i].Name); n != "" {
			return n
		}
	}
	return a.folderName(fs, path)
}

// folderName is the name of the folder at path on the file system of ID
// fs. How another file system writes paths is not known, so its paths
// are taken to end in a name after the last slash or backslash.
func (a *app) folderName(fs, path string) string {
	if fs == a.fs.ID() {
		return a.ps.placeName(path)
	}
	if name := path[strings.LastIndexAny(path, `/\`)+1:]; name != "" {
		return name
	}
	return path
}

// favPlaces returns the favourites as places, as the sidebar and the
// palette list them: where the store keeps favourites of any file
// system, each says which it is on, as the store names it.
func (a *app) favPlaces() []Place {
	where, _ := a.favStore().(AnyFSFavourites)
	out := make([]Place, len(a.favs))
	for i, f := range a.favs {
		out[i] = Place{Name: a.favName(f.FS, f.Path), Path: f.Path, Kind: "favourite", FS: f.FS, Color: f.Color, Icon: f.Icon}
		if where != nil {
			out[i].Note = where.Where(f.FS)
		}
	}
	return out
}
