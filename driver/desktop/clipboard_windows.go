//go:build windows

package desktop

import (
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/internal/glfw"
)

// SetClipboardSensitive implements [driver.SensitiveClipboard], on the main thread.
func (w *Window) SetClipboardSensitive(s string) error {
	return w.d.call(func() error { return glfw.SetClipboardStringSensitive(s) })
}

// ClearClipboardIf implements [driver.SensitiveClipboard], on the main thread.
func (w *Window) ClearClipboardIf(s string) (bool, error) {
	var cleared bool
	err := w.d.call(func() error {
		var err error
		cleared, err = glfw.ClearClipboardStringIf(s)
		return err
	})
	return cleared, err
}

var _ driver.SensitiveClipboard = (*Window)(nil)
