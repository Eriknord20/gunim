package desktop

import (
	"errors"
	"fmt"
)

// NativeHandle implements [driver.NativeWindow]: macOS hands out no
// window yet.
func (w *Window) NativeHandle() (uintptr, error) {
	return 0, fmt.Errorf("desktop: native window: %w", errors.ErrUnsupported)
}
