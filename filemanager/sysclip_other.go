//go:build !(windows && (amd64 || arm64))

package filemanager

import "errors"

// osClip is the system's clipboard, where the window reads no files from
// it: a copy in another program does not paste here.
type osClip struct{}

func (osClip) seq() uint32                            { return 0 }
func (osClip) hasFiles() bool                         { return false }
func (osClip) files() (systemFiles, error)            { return systemFiles{}, nil }
func (osClip) outside(func(outsideFiles) error) error { return errors.ErrUnsupported }
func (osClip) empty(uint32)                           {}
