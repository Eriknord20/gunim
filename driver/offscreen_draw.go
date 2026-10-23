//go:build (linux && !android) || windows

package driver

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"runtime"
	"sync"

	"github.com/marrasen/gunim/driver/internal/render"
	"github.com/marrasen/gunim/internal/gl"
	"github.com/marrasen/gunim/internal/glfw"
	"github.com/marrasen/gunim/paint"
)

// hidden is the hidden window offscreen windows draw their pixels in: made on first use, on a thread of its own that
// owns GLFW and the window's GL context, and kept for the rest of the run.
var hidden struct {
	once sync.Once
	err  error
	jobs chan drawJob
}

// drawJob is a frame for the hidden window to draw, answered on got.
type drawJob struct {
	ops      []paint.Op
	fbW, fbH int
	scale    float32
	under    color.NRGBA
	got      chan<- drawResult
}

type drawResult struct {
	img *image.RGBA
	err error
}

func init() { drawOps = drawHidden }

// drawHidden draws ops in the hidden window, making it first.
func drawHidden(ops []paint.Op, fbW, fbH int, scale float32, under color.NRGBA) (*image.RGBA, error) {
	hidden.once.Do(func() {
		if err := claimGLFWForOffscreen(); err != nil {
			hidden.err = err
			return
		}
		ready := make(chan error)
		hidden.jobs = make(chan drawJob)
		go serveHidden(ready)
		if hidden.err = <-ready; hidden.err != nil {
			// GLFW is taken down again, for a window on a display to start.
			glfwOwner.Store(glfwFree)
		}
	})
	if hidden.err != nil {
		return nil, hidden.err
	}
	got := make(chan drawResult, 1)
	hidden.jobs <- drawJob{ops: ops, fbW: fbW, fbH: fbH, scale: scale, under: under, got: got}
	r := <-got
	return r.img, r.err
}

// serveHidden makes the hidden window and its context, says on ready whether it could, and draws each job sent.
func serveHidden(ready chan<- error) {
	runtime.LockOSThread()
	ctx, es, err := openHidden()
	ready <- err
	if err != nil {
		return
	}
	var sh render.Shared
	for j := range hidden.jobs {
		img, err := drawJobIn(ctx, es, &sh, j)
		if err == nil {
			// The window's messages are answered between frames.
			err = glfw.PollEvents()
		}
		j.got <- drawResult{img: img, err: err}
	}
}

// openHidden starts GLFW and opens a hidden window with the context a window on a display gets, OpenGL 3.2 core or
// OpenGL ES 3.0 where that is all the GL library offers, and makes it current. What it fails to make it takes down
// again, and a failure for want of a display or OpenGL wraps ErrCannotDraw.
func openHidden() (ctx gl.Context, es bool, err error) {
	if err := glfw.Init(); err != nil {
		return nil, false, fmt.Errorf("%w: starting GLFW: %w", ErrCannotDraw, err)
	}
	var win *glfw.Window
	defer func() {
		if err == nil {
			return
		}
		if win != nil {
			err = errors.Join(err, win.Destroy())
		}
		err = errors.Join(err, glfw.Terminate())
	}()
	probe, err := gl.NewDefaultContext()
	if err != nil {
		return nil, false, fmt.Errorf("%w: loading OpenGL: %w", ErrCannotDraw, err)
	}
	es = probe.IsES()
	if err := glfw.DefaultWindowHints(); err != nil {
		return nil, false, fmt.Errorf("driver: offscreen pixels: %w", err)
	}
	hints := [][2]int{{int(glfw.ContextVersionMajor), 3}, {int(glfw.Visible), glfw.False}, {int(glfw.ContextOnly), glfw.True}}
	if es {
		hints = append(hints,
			[2]int{int(glfw.ClientAPI), glfw.OpenGLESAPI},
			[2]int{int(glfw.ContextVersionMinor), 0},
			[2]int{int(glfw.ContextCreationAPI), glfw.EGLContextAPI})
	} else {
		hints = append(hints,
			[2]int{int(glfw.ClientAPI), glfw.OpenGLAPI},
			[2]int{int(glfw.ContextVersionMinor), 2},
			[2]int{int(glfw.OpenGLProfile), glfw.OpenGLCoreProfile},
			[2]int{int(glfw.OpenGLForwardCompat), glfw.True})
	}
	for _, h := range hints {
		if err := glfw.WindowHint(glfw.Hint(h[0]), h[1]); err != nil {
			return nil, false, fmt.Errorf("driver: offscreen pixels: %w", err)
		}
	}
	w, err := glfw.CreateWindow(16, 16, "gunim offscreen pixels", nil, nil)
	if err != nil {
		return nil, false, fmt.Errorf("%w: opening a hidden window: %w", ErrCannotDraw, err)
	}
	win = w
	if err := win.MakeContextCurrent(); err != nil {
		return nil, false, fmt.Errorf("%w: %w", ErrCannotDraw, err)
	}
	if ctx, err = gl.NewDefaultContext(); err != nil {
		return nil, false, fmt.Errorf("%w: %w", ErrCannotDraw, err)
	}
	if err := ctx.LoadFunctions(); err != nil {
		return nil, false, fmt.Errorf("%w: loading OpenGL: %w", ErrCannotDraw, err)
	}
	return ctx, es, nil
}

// drawJobIn draws j with a renderer of its own, so each picture starts from nothing, and reads its canvas back.
func drawJobIn(ctx gl.Context, es bool, sh *render.Shared, j drawJob) (*image.RGBA, error) {
	r, err := render.New(ctx, es, sh)
	if err != nil {
		return nil, fmt.Errorf("driver: offscreen pixels: %w", err)
	}
	defer r.Release()
	r.Under = j.under
	r.Draw(j.ops, paint.Everything, j.fbW, j.fbH, j.scale)
	pix := make([]byte, j.fbW*j.fbH*4)
	ctx.BindFramebuffer(gl.FRAMEBUFFER, r.Canvas())
	ctx.ReadPixels(pix, 0, 0, int32(j.fbW), int32(j.fbH), gl.RGBA, gl.UNSIGNED_BYTE)
	if e := ctx.GetError(); e != 0 {
		return nil, fmt.Errorf("driver: offscreen pixels: reading the frame back: GL error %#x", e)
	}
	// GL reads from the bottom row up.
	img := image.NewRGBA(image.Rect(0, 0, j.fbW, j.fbH))
	row := j.fbW * 4
	for y := range j.fbH {
		copy(img.Pix[y*img.Stride:y*img.Stride+row], pix[(j.fbH-1-y)*row:(j.fbH-y)*row])
	}
	over(img, j.under)
	return img, nil
}

// over lays img, premultiplied as the canvas holds it, over the background c, which the renderer clears the window to
// and not the canvas.
func over(img *image.RGBA, c color.NRGBA) {
	if c.A == 0 {
		return
	}
	a := uint32(c.A)
	bg := [4]uint32{uint32(c.R) * a / 0xff, uint32(c.G) * a / 0xff, uint32(c.B) * a / 0xff, a}
	for i := 0; i < len(img.Pix); i += 4 {
		left := 0xff - uint32(img.Pix[i+3])
		for k := range 4 {
			img.Pix[i+k] += uint8(bg[k] * left / 0xff)
		}
	}
}
