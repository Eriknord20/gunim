package desktop

import (
	"fmt"
	"image/color"
	"os"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/internal/glfw"
)

// drawnShadowMode is GUNIM_DRAWN_SHADOW: "0" leaves a chromeless window's shadow to the system, and "measure" draws
// green bands instead of the drawn shadow, for measuring how closely it follows.
var drawnShadowMode = os.Getenv("GUNIM_DRAWN_SHADOW")

// startShadow gives a chromeless window a drawn shadow where it presents through DXGI, unless GUNIM_DRAWN_SHADOW=0.
// It runs on the main thread.
func startShadow(w *Window) {
	// Only DXGI's frames carry the alpha the cut corners need
	if drawnShadowMode == "0" || !w.d.dxgi {
		return
	}
	if err := w.gw.SetDrawnShadow(drawnShadowMode == "measure", float64(w.cornerLogical())); err != nil {
		w.debugf("shadow left to the system: %v", err)
		return
	}
	w.mu.Lock()
	w.shadow = true
	w.mu.Unlock()
	// The window leaves its edge clear for the border as wide as it is drawn, and draws again as that changes
	w.gw.SetBorderWidthCallback(func(_ *glfw.Window, px float32) {
		w.mu.Lock()
		w.edge = px
		w.mu.Unlock()
		w.in.Push(driver.Redraw{})
	})
}

// applyBorder gives a chromeless window the border the application asked for, drawn with its shadow or by the
// system. It runs on the main thread.
func applyBorder(w *Window) error {
	if !w.Chromeless() {
		return nil
	}
	w.mu.Lock()
	b := w.border
	w.mu.Unlock()
	inactive := b.Inactive
	if inactive == (color.NRGBA{}) {
		inactive = b.Color
	}
	transition := b.Transition
	switch {
	case transition == 0:
		transition = driver.BorderTransition
	case transition < 0:
		transition = 0
	}
	if err := w.gw.SetBorder(b.Color, inactive, b.Width, b.None, transition); err != nil {
		w.debugf("border: %v", err)
		return fmt.Errorf("desktop: set the window's border: %w", err)
	}
	return nil
}

// edgeShown tells the drawn border the edge a presented frame left for it, which it covers until the window has drawn
// a narrower one, so a shrinking border leaves no gap. It runs on the render thread.
func edgeShown(w *Window, px float32) {
	w.d.post(func() {
		if w.closed {
			return
		}
		if err := w.gw.SetBorderShown(px); err != nil {
			w.fail(fmt.Errorf("desktop: draw the window's border: %w", err))
		}
	})
}

// fadeShadow shows the drawn shadow at opacity o, or hides it at 0, round the window drawn scale times its size. It
// runs on the main thread.
func fadeShadow(w *Window, o, scale float32) {
	if scale <= 0 {
		scale = 1
	}
	if err := w.gw.SetShadowFade(o, scale); err != nil {
		w.debugf("shadow: %v", err)
	}
}
