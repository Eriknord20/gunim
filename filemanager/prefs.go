package filemanager

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// prefs are the settings the app keeps between runs.
type prefs struct {
	Favourites []string
	ShowHidden bool
	// DeepFilter says the filter looks in the folders under the one
	// showing too.
	DeepFilter  bool `json:",omitempty"`
	HidePreview bool
	// SystemIcons says whether items show the icons Windows does, there:
	// unset, they do.
	SystemIcons *bool `json:",omitempty"`
	Light       bool
	Zoom        float32
	Sidebar     float32
	Sort        SortBy
	Desc        bool
	// Views says which folders show as icons, Icons how the rest do, and Tile how wide an icon's tile is.
	Views map[string]bool
	Icons bool
	Tile  float32
	// FavNames holds the names given to favourites, by path, and
	// FavColors and FavIcons their colours and icons.
	FavNames  map[string]string
	FavColors map[string]string `json:",omitempty"`
	FavIcons  map[string]string `json:",omitempty"`
	// FavSeeded says the default favourites were added, once, so those
	// the user unpins stay unpinned.
	FavSeeded bool `json:",omitempty"`
	// SidebarOrder is the order the user put the sidebar's sections in,
	// by their IDs, and SidebarCollapsed the IDs of those closed. See
	// Places.
	SidebarOrder     []string `json:",omitempty"`
	SidebarCollapsed []string `json:",omitempty"`
	// UploadEdited says what to do with a file fetched to open that
	// changes on this computer: ask, the default when empty, upload it
	// as always, or never.
	UploadEdited string `json:",omitempty"`
}

// defaultPrefsPath is where the settings live: gunim-files/prefs.json in
// the user's configuration folder.
func defaultPrefsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("finding where to keep settings: %w", err)
	}
	return filepath.Join(dir, "gunim-files", "prefs.json"), nil
}

// loadPrefs reads the settings at path. A missing file gives the
// defaults.
func loadPrefs(path string) (prefs, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return prefs{}, nil
	}
	if err != nil {
		return prefs{}, fmt.Errorf("reading the settings in %s: %w", path, err)
	}
	var p prefs
	if err := json.Unmarshal(b, &p); err != nil {
		return prefs{}, fmt.Errorf("reading the settings in %s: %w", path, err)
	}
	return p, nil
}

// prefsLocks holds a lock for each settings file, by path, so windows
// that change one at once take turns.
var prefsLocks sync.Map

// updatePrefs makes change to the settings in the file at path: it reads
// them, changes them, and writes them back, while no other window of the
// process does.
func updatePrefs(path string, change func(p *prefs)) error {
	l, _ := prefsLocks.LoadOrStore(path, &sync.Mutex{})
	mu, _ := l.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	p, err := loadPrefs(path)
	if err != nil {
		return err
	}
	change(&p)
	return savePrefs(path, p)
}

// savePrefs writes the settings to path through a file beside it, so a
// failed write leaves the old settings whole.
func savePrefs(path string, p prefs) error {
	b, jerr := json.MarshalIndent(p, "", "  ")
	if jerr != nil {
		return fmt.Errorf("saving the settings: %w", jerr)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("saving the settings: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "prefs-*.json")
	if err != nil {
		return fmt.Errorf("saving the settings: %w", err)
	}
	_, werr := tmp.Write(b)
	if err := errors.Join(werr, tmp.Close()); err != nil {
		return errors.Join(fmt.Errorf("saving the settings in %s: %w", path, err), os.Remove(tmp.Name()))
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return errors.Join(fmt.Errorf("saving the settings in %s: %w", path, err), os.Remove(tmp.Name()))
	}
	return nil
}
