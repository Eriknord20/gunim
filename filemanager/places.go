package filemanager

import (
	"errors"
	"fmt"
	"os"
	"runtime"
)

// space is how much room a volume has, in bytes.
type space struct {
	free, total uint64
}

// userPlace is one of the user's own folders, found by the system: its
// kind, such as documents, and where it is.
type userPlace struct {
	kind, path string
}

// LocalPlaces returns the places of the computer's own file system: the
// user's home, the volumes with their space, and the trash, where the
// window can open the system's. A volume whose space cannot be read
// carries the error. The user's own folders, such as Documents, are not
// places but favourites, which the user can unpin: see
// DefaultFavourites. It is what a window on the computer's own file
// system offers when its options give no places, and a start for a
// caller that adds its own.
func LocalPlaces() ([]Place, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("finding your home folder: %w", err)
	}
	out := []Place{{Name: "Home", Path: home, Kind: "home"}}
	vols, err := volumes()
	if err != nil {
		return nil, err
	}
	for _, v := range vols {
		s, err := volumeSpace(v.Path)
		if err != nil {
			// The row names the volume, so the cause alone is said.
			v.Err = rootCause(err).Error()
		} else {
			v.Free, v.Total = s.free, s.total
		}
		out = append(out, v)
	}
	if _, err := systemTrashCan(); err == nil {
		name := "Trash"
		if runtime.GOOS == "windows" {
			name = "Recycle Bin"
		}
		out = append(out, Place{Name: name, Path: TrashPath, Kind: "trash"})
	}
	return out, nil
}

// rootCause returns the innermost error err wraps.
func rootCause(err error) error {
	for {
		inner := errors.Unwrap(err)
		if inner == nil {
			return err
		}
		err = inner
	}
}
