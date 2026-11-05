//go:build !js

package glfw

import (
	"errors"
	"fmt"
)

// gunim change: a window whose context is made, used and deleted on the
// thread that draws with it, rather than made and touched on the main
// thread as CreateWindow does. A GL driver can wait in a call on the
// main thread for a lock a thread drawing in the same share group holds;
// the main thread is the one every window waits on, so the program
// freezes. With these, the main thread makes only the window, and the
// thread that draws does all the rest.

// errNoDeferredContext says a context can't be made later here.
var errNoDeferredContext = errors.New("glfw: a context made later is for WGL on Windows alone")

// deferredContext is what a context made later is to be.
type deferredContext struct {
	ctx ctxconfig
	fb  fbconfig
}

// CreateWindowDeferringContext is CreateWindow for a window whose
// context, as the hints ask and sharing with share, is made later by
// CreateDeferredContext. It runs on the main thread; the window is made
// with no context, as for NoAPI.
func CreateWindowDeferringContext(width, height int, title string, share *Window) (*Window, error) {
	if !_glfw.initialized {
		return nil, NotInitialized
	}
	want := _glfw.hints.context
	want.share = share
	if want.client == NoAPI {
		return nil, fmt.Errorf("glfw: a deferred context needs an API: %w", InvalidValue)
	}
	if err := checkValidContextConfig(&want); err != nil {
		return nil, err
	}
	fb := _glfw.hints.framebuffer
	_glfw.hints.context.client = NoAPI
	w, err := CreateWindow(width, height, title, nil, nil)
	_glfw.hints.context.client = want.client
	if err != nil {
		return nil, err
	}
	w.deferred = &deferredContext{ctx: want, fb: fb}
	return w, nil
}

// CreateDeferredContext makes the context of a window made by
// CreateWindowDeferringContext, on the calling thread, which should be
// the one that draws with it. The context is not current afterwards.
func (w *Window) CreateDeferredContext() error {
	if w.deferred == nil {
		return errors.New("glfw: the window was not made to defer its context")
	}
	if w.context.client != NoAPI {
		return errors.New("glfw: the window's context is made already")
	}
	return w.platformCreateDeferredContext(&w.deferred.ctx, &w.deferred.fb)
}

// DeleteContext deletes the window's context on the calling thread,
// which should be the one it was current on, and leaves the window with
// none: destroying the window later deletes nothing. A context deleted
// from another thread while current here would be in use, and the
// driver may refuse it, or leak it.
func (w *Window) DeleteContext() error {
	if w.context.client == NoAPI {
		return nil
	}
	if w == currentContext() {
		if err := (*Window)(nil).MakeContextCurrent(); err != nil {
			return err
		}
	}
	var err error
	if w.context.destroy != nil {
		err = w.context.destroy(w)
		w.context.destroy = nil
	}
	w.context.client = NoAPI
	return err
}
