//go:build js && wasm

package filemanager

import "errors"

// A browser cannot enumerate the host's volumes or personal folders.
func userFolders(string) ([]userPlace, error) { return nil, errors.ErrUnsupported }
func volumes() ([]Place, error)               { return nil, errors.ErrUnsupported }
func volumeSpace(string) (space, error)       { return space{}, errors.ErrUnsupported }
