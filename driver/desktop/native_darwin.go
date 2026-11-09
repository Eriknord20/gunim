package desktop

// NativeHandle implements [driver.NativeWindow]: the window's content
// view, an NSView, which a plugin's editor is a subview of.
func (w *Window) NativeHandle() (uintptr, error) {
	var h uintptr
	err := w.d.call(func() (err error) {
		h, err = w.gw.GetCocoaView()
		return err
	})
	return h, err
}
