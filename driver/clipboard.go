package driver

import "errors"

// ImageClipboard is a window that reads pictures from the system clipboard and puts them there, as well as text.
type ImageClipboard interface {
	// ClipboardImage returns the picture on the clipboard as PNG, or nil when it holds none. It may wait on the
	// display server.
	ClipboardImage() ([]byte, error)
	// SetClipboardImage replaces what the clipboard holds with the picture png, a PNG file's bytes; nil empties
	// the clipboard. It returns an error for bytes that are not a PNG, and [ErrNoClipboardImage] where the
	// platform cannot put a picture there. It may wait on the display server, and is safe from any goroutine.
	SetClipboardImage(png []byte) error
}

// ErrNoClipboardImage is returned where a window cannot put a picture on the system clipboard.
var ErrNoClipboardImage = errors.New("driver: no way to put a picture on the clipboard on this platform")
