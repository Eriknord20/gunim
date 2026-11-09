//go:build !js

package desktop

import (
	"os"
	"path/filepath"

	"github.com/marrasen/gunim/driver"
)

// UserFolder implements [driver.FolderFinder]: the folder the system
// names for f, where it does, or else the one in the user's home of
// its usual name, where it is there.
func (d *Driver) UserFolder(f driver.UserFolder) string {
	if f != driver.FolderMusic {
		return ""
	}
	if dir := systemFolder(f); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(home, "Music")
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		return dir
	}
	return ""
}
