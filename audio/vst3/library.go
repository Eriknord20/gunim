package vst3

import (
	"slices"
	"strings"
	"sync"

	"github.com/marrasen/gunim/audio/plug"
)

// A Library is the VST3 plugins a host has on disk, which package plug
// finds by their IDs, as [PlugID] gives them, once the host hands
// [Library.Find] to [plug.AddFinder]: a song naming a VST3 plugin loads
// it as the song's plugins are looked up.
type Library struct {
	scanned chan struct{}
	// found and bundles are what the scan found, set before scanned
	// closes.
	found   []Found
	bundles []Bundle

	mu     sync.Mutex
	failed map[string]error
}

// Found is an effect or an instrument a Library found.
type Found struct {
	// ID is what package plug knows it by.
	ID string
	Class
	// Path is the bundle or file that makes it.
	Path string
}

// NewLibrary returns the library of the plugins in the system's folders,
// as [Dirs] gives them, and in dirs, scanned on a goroutine of its own:
// from what each bundle lists, or, for one that lists nothing, loading
// its module.
func NewLibrary(dirs ...string) *Library {
	l := &Library{scanned: make(chan struct{}), failed: map[string]error{}}
	go l.scan(append(Dirs(), dirs...))
	return l
}

func (l *Library) scan(dirs []string) {
	defer close(l.scanned)
	l.bundles = Scan(dirs...)
	for _, b := range l.bundles {
		classes := append(slices.Clone(b.Effects), b.Instruments...)
		if !b.Listed {
			m, err := Load(b.Path)
			if err != nil {
				continue
			}
			classes = append(m.Effects(), m.Instruments()...)
		}
		for _, c := range classes {
			l.found = append(l.found, Found{ID: PlugID(c), Class: c, Path: b.Path})
		}
	}
	slices.SortFunc(l.found, func(a, b Found) int {
		if c := strings.Compare(strings.ToLower(a.Vendor), strings.ToLower(b.Vendor)); c != 0 {
			return c
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
}

// Scanned reports whether the scan is done, without waiting for it.
func (l *Library) Scanned() bool {
	select {
	case <-l.scanned:
		return true
	default:
		return false
	}
}

// Plugins returns the effects and instruments found, by vendor, then
// name, once the scan is done.
func (l *Library) Plugins() []Found {
	<-l.scanned
	return l.found
}

// Find is a [plug.Finder]: it registers the plugin of id, a VST3
// plugin's ID found, and reports whether it did, once the scan is done.
// An ID it found nothing of, or whose plugin would not load, it
// remembers, with [Library.Err] saying why.
func (l *Library) Find(id string) bool {
	if _, ok := ClassID(id); !ok {
		return false
	}
	<-l.scanned
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.failed[id]; ok {
		return false
	}
	if _, err := RegisterID(id, l.bundles); err != nil {
		l.failed[id] = err
		return false
	}
	return true
}

// Err returns why the plugin of ID id would not load, or nil.
func (l *Library) Err(id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.failed[id]
}

var _ plug.Finder = (*Library)(nil).Find
