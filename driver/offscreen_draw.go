//go:build (linux && !android) || windows

package driver

import (
	"fmt"
	"image"
	"runtime"
	"sync"

	"github.com/marrasen/gunim/driver/internal/render"
	"github.com/marrasen/gunim/internal/gl"
	"github.com/marrasen/gunim/internal/glfw"
	"github.com/marrasen/gunim/paint"
)

// hidden is the hidden window offscreen windows draw their pixels in: made on first use, on a thread of its own that
// owns its GL context, and kept for the rest of the run.
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
	got      chan<- drawResult
}

type drawResult struct {
	img *image.RGBA
	err error
}

func init() { drawOps = drawHidden }

// drawHidden draws ops in the hidden window, making it first.
func drawHidden(ops []paint.Op, fbW, fbH int, scale float32) (*image.RGBA, error) {
	hidden.once.Do(func() {
		ready := make(chan error)
		hidden.jobs = make(chan drawJob)
		go serveHidden(ready)
		hidden.err = <-ready
	})
	if hidden.err != nil {
		return nil, hidden.err
	}
	got := make(chan drawResult, 1)
	hidden.jobs <- drawJob{ops: ops, fbW: fbW, fbH: fbH, scale: scale, got: got}
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
		j.got <- drawResult{img: img, err: err}
	}
}

// openHidden opens a hidden window with the context a window on a display gets, OpenGL 3.2 core or OpenGL ES 3.0
// where that is all the GL library offers, makes it current, and returns it.
func openHidden() (gl.Context, bool, error) {
	if err := glfw.Init(); err != nil {
		return nil, false, fmt.Errorf("driver: offscreen pixels: %w", err)
	}
	probe, err := gl.NewDefaultContext()
	if err != nil {
		return nil, false, fmt.Errorf("driver: offscreen pixels: %w", err)
	}
	es := probe.IsES()
	hints := [][2]int{{int(glfw.ContextVersionMajor), 3}, {int(glfw.Visible), glfw.False}}
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
	win, err := glfw.CreateWindow(16, 16, "gunim offscreen pixels", nil, nil)
	if err != nil {
		return nil, false, fmt.Errorf("driver: offscreen pixels: opening a hidden window: %w", err)
	}
	if err := win.MakeContextCurrent(); err != nil {
		return nil, false, fmt.Errorf("driver: offscreen pixels: %w", err)
	}
	ctx, err := gl.NewDefaultContext()
	if err != nil {
		return nil, false, fmt.Errorf("driver: offscreen pixels: %w", err)
	}
	if err := ctx.LoadFunctions(); err != nil {
		return nil, false, fmt.Errorf("driver: offscreen pixels: %w", err)
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
	return img, nil
}
