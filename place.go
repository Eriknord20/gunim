package gunim

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/marrasen/gunim/driver"
)

// readPlace is the placement kept in file, or nil where there is none
// to read.
func readPlace(file string) *driver.Placement {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	var p driver.Placement
	if json.Unmarshal(b, &p) != nil || p.Bounds.Empty() {
		return nil
	}
	return &p
}

// keepPlace writes where the window was as it closed to its
// [WindowOptions.PlaceFile]. A place it cannot write is let go: the
// window opens at its size next time, which is no reason to report an
// error as it closes.
func (w *Window) keepPlace() {
	if w.placeFile == "" {
		return
	}
	p, ok := w.Placement()
	if !ok || p.Bounds.Empty() {
		return
	}
	b, err := json.Marshal(p)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(w.placeFile), 0o755); err != nil {
		return
	}
	// Written whole beside it first, so a crash halfway leaves the
	// place before rather than half a file.
	tmp := w.placeFile + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return
	}
	if err := os.Rename(tmp, w.placeFile); err != nil {
		_ = os.Remove(tmp)
	}
}
