// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: 2026 The gunim Authors

// gunim change: this file is gunim's. It puts a picture on the CLIPBOARD
// selection, as image/png, where GLFW puts only text.

//go:build freebsd || linux || netbsd

package glfw

import (
	"fmt"
	"slices"
	"unsafe"

	"github.com/ebitengine/purego"
)

// x11Clip is the picture the helper window offers as the CLIPBOARD selection, while it owns it, and the transfers
// of it under way.
var x11Clip struct {
	// png is the picture, nil while the clipboard holds text or nothing.
	png []byte
	// imagePNG is the image/png atom, once interned.
	imagePNG _Atom
	// incr are the pictures going out a piece at a time.
	incr []*incrTransfer
}

// An incrTransfer is a picture too large for one property, going to a requestor a piece at a time: the INCR
// protocol of ICCCM section 2.7.2. Each time the requestor deletes the property, it gets the next piece; a piece of
// no bytes ends it.
type incrTransfer struct {
	requestor _XID
	property  _Atom
	data      []byte
	sent      int
}

// maxIncrTransfers bounds the transfers kept: a requestor that goes away halfway leaves its transfer behind until
// the clipboard changes, or until enough newer ones push it out.
const maxIncrTransfers = 8

// imagePNGAtom returns the image/png atom.
func imagePNGAtom() _Atom {
	if x11Clip.imagePNG == _None {
		x11Clip.imagePNG = xInternAtom(_glfw.platformWindow.display, "image/png", false)
	}
	return x11Clip.imagePNG
}

// platformSetClipboardImage makes the helper window own CLIPBOARD, offering img as image/png, or gives the
// selection up when img holds no picture.
func platformSetClipboardImage(img *ClipboardImage) error {
	dropClipboardImage()
	_glfw.platformWindow.clipboardString = ""
	display := _glfw.platformWindow.display
	helper := _glfw.platformWindow.helperWindowHandle
	if img.png == nil {
		if xGetSelectionOwner(display, _glfw.platformWindow.CLIPBOARD) == helper {
			xSetSelectionOwner(display, _glfw.platformWindow.CLIPBOARD, _None, _CurrentTime)
		}
		return nil
	}
	imagePNGAtom()
	x11Clip.png = img.png
	xSetSelectionOwner(display, _glfw.platformWindow.CLIPBOARD, helper, _CurrentTime)
	if xGetSelectionOwner(display, _glfw.platformWindow.CLIPBOARD) != helper {
		x11Clip.png = nil
		return fmt.Errorf("glfw: x11: failed to become owner of clipboard selection: %w", PlatformError)
	}
	return nil
}

// dropClipboardImage forgets the picture and the transfers of it. It makes no X call, so terminating may call it.
func dropClipboardImage() {
	x11Clip.png = nil
	x11Clip.incr = nil
}

// maxPropertyBytes is the most bytes of a picture one property change carries: the server's largest request
// without the BIG-REQUESTS extension, less room for the request's own fields. A larger picture goes by INCR.
func maxPropertyBytes() int {
	n := int(xMaxRequestSize(_glfw.platformWindow.display))*4 - 1024
	if n < 4096 {
		n = 4096
	}
	return n
}

// writeImageToProperty answers a request to convert CLIPBOARD while it holds a picture, as writeTargetToProperty
// does for text: TARGETS, MULTIPLE and SAVE_TARGETS, and image/png. It returns the property to reply with, or
// None when the request cannot be met.
func writeImageToProperty(request *_XSelectionRequestEvent) _Atom {
	display := _glfw.platformWindow.display
	imagePNG := imagePNGAtom()
	if request.Property == _None {
		// A legacy client (ICCCM section 2.2), which GLFW does not serve either.
		return _None
	}
	switch request.Target {
	case _glfw.platformWindow.TARGETS:
		xChangePropertyGeneric(display, request.Requestor, request.Property, _XA_ATOM, _PropModeReplace,
			[]_Atom{_glfw.platformWindow.TARGETS, _glfw.platformWindow.MULTIPLE, imagePNG})
		return request.Property

	case _glfw.platformWindow.MULTIPLE:
		var targetsPtr uintptr
		count := getWindowPropertyX11(request.Requestor, request.Property, _glfw.platformWindow.ATOM_PAIR,
			&targetsPtr)
		if targetsPtr != 0 {
			defer xFree(targetsPtr)
		}
		var targets []_Atom
		if targetsPtr != 0 && count > 0 {
			targets = unsafe.Slice((*_Atom)(unsafe.Pointer(targetsPtr)), count)
		}
		for i := 0; i+1 < len(targets); i += 2 {
			// A picture within one property goes; a larger one does not, as INCR inside MULTIPLE is not served.
			if targets[i] == imagePNG && len(x11Clip.png) <= maxPropertyBytes() {
				xChangePropertyGeneric(display, request.Requestor, targets[i+1], imagePNG, _PropModeReplace,
					x11Clip.png)
			} else {
				targets[i+1] = _None
			}
		}
		xChangePropertyGeneric(display, request.Requestor, request.Property, _glfw.platformWindow.ATOM_PAIR,
			_PropModeReplace, targets)
		return request.Property

	case _glfw.platformWindow.SAVE_TARGETS:
		xChangePropertyGeneric(display, request.Requestor, request.Property, _glfw.platformWindow.NULL_,
			_PropModeReplace, []_Atom(nil))
		return request.Property

	case imagePNG:
		if len(x11Clip.png) <= maxPropertyBytes() {
			xChangePropertyGeneric(display, request.Requestor, request.Property, imagePNG, _PropModeReplace,
				x11Clip.png)
			return request.Property
		}
		return startIncr(request)
	}
	return _None
}

// startIncr begins sending the picture to request's requestor a piece at a time. It says how large the picture
// is, as the INCR property, and watches the requestor's window for the property's deletion, which asks for the
// next piece.
func startIncr(request *_XSelectionRequestEvent) _Atom {
	display := _glfw.platformWindow.display
	grabErrorHandlerX11()
	xSelectInput(display, request.Requestor, _PropertyChangeMask)
	xChangePropertyGeneric(display, request.Requestor, request.Property, _glfw.platformWindow.INCR,
		_PropModeReplace, []_Clong{_Clong(len(x11Clip.png))})
	releaseErrorHandlerX11()
	if _glfw.platformWindow.errorCode != _Success {
		// The requestor's window has gone.
		return _None
	}
	x11Clip.incr = slices.DeleteFunc(x11Clip.incr, func(t *incrTransfer) bool {
		return t.requestor == request.Requestor && t.property == request.Property
	})
	if len(x11Clip.incr) >= maxIncrTransfers {
		x11Clip.incr = x11Clip.incr[1:]
	}
	x11Clip.incr = append(x11Clip.incr, &incrTransfer{
		requestor: request.Requestor,
		property:  request.Property,
		data:      x11Clip.png,
	})
	return request.Property
}

// continueIncr sends the next piece of a picture going by INCR, when the event says its requestor deleted the
// property the last piece was in. It reports whether the event was one of those.
func continueIncr(ev *_XPropertyEvent) bool {
	i := incrFor(ev)
	if i < 0 {
		return false
	}
	t := x11Clip.incr[i]
	display := _glfw.platformWindow.display
	n := min(len(t.data)-t.sent, maxPropertyBytes())
	grabErrorHandlerX11()
	// The last piece is one of no bytes, which tells the requestor the picture is whole.
	xChangePropertyGeneric(display, t.requestor, t.property, imagePNGAtom(), _PropModeReplace,
		t.data[t.sent:t.sent+n])
	t.sent += n
	done := n == 0
	if done {
		x11Clip.incr = slices.Delete(x11Clip.incr, i, i+1)
		if !slices.ContainsFunc(x11Clip.incr, func(o *incrTransfer) bool { return o.requestor == t.requestor }) {
			xSelectInput(display, t.requestor, _NoEventMask)
		}
	}
	releaseErrorHandlerX11()
	if _glfw.platformWindow.errorCode != _Success && !done {
		// The requestor's window has gone, and the rest of the picture with it.
		x11Clip.incr = slices.DeleteFunc(x11Clip.incr, func(o *incrTransfer) bool { return o == t })
	}
	return true
}

// incrFor returns the index of the transfer whose property ev says was deleted, or -1.
func incrFor(ev *_XPropertyEvent) int {
	if ev.State != _PropertyDelete {
		return -1
	}
	return slices.IndexFunc(x11Clip.incr, func(t *incrTransfer) bool {
		return t.requestor == ev.Window && t.property == ev.Atom
	})
}

// isIncrEvent reports whether the event is a deleted property that asks for the next piece of a picture going by
// INCR.
func isIncrEvent(display uintptr, eventPtr uintptr, pointer uintptr) uintptr {
	event := (*_XEvent)(unsafe.Pointer(eventPtr))
	if event.EventType() == _PropertyNotify && incrFor(event.xproperty()) >= 0 {
		return 1
	}
	return 0
}

var isIncrEventPredicate uintptr

// isIncrEventCallback returns isIncrEvent as a C function pointer, for XCheckIfEvent.
func isIncrEventCallback() uintptr {
	if isIncrEventPredicate == 0 {
		isIncrEventPredicate = purego.NewCallback(isIncrEvent)
	}
	return isIncrEventPredicate
}
