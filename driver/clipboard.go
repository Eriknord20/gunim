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

// SensitiveClipboard is a window that marks text on the clipboard as sensitive, such as a password, so the system
// keeps it out of the clipboard's history and the cloud clipboard. Only Windows does so far.
type SensitiveClipboard interface {
	// SetClipboardSensitive replaces what the clipboard holds with s, marked sensitive; an empty s empties the
	// clipboard. It returns an error, and leaves nothing on the clipboard, where the marks cannot be set.
	SetClipboardSensitive(s string) error
	// ClearClipboardIf empties the clipboard if it still holds the text s that this window's program put there, and
	// reports whether it did, with nothing put there between the check and the emptying. For the text
	// SetClipboardSensitive put there last it goes by ownership, and reads nothing.
	ClearClipboardIf(s string) (bool, error)
}
