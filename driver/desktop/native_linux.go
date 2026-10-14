package desktop

// NativeHandle implements [driver.NativeWindow]: the window's X11 ID.
func (w *Window) NativeHandle() (uintptr, error) {
	var h uintptr
	err := w.d.call(func() (err error) {
		h, err = w.gw.GetX11Window()
		return err
	})
	return h, err
}
