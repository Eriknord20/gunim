//go:build !windows && !js

package glfw

// platformCreateDeferredContext is for Windows alone, where a DXGI
// window's context is made on its render thread.
func (w *Window) platformCreateDeferredContext(*ctxconfig, *fbconfig) error {
	return errNoDeferredContext
}
