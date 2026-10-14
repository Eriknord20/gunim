package desktop

// NativeHandle implements [driver.NativeWindow]: the window's HWND.
func (w *Window) NativeHandle() (uintptr, error) {
	var h uintptr
	err := w.d.call(func() error {
		hwnd, err := w.gw.GetWin32Window()
		h = uintptr(hwnd)
		return err
	})
	return h, err
}
