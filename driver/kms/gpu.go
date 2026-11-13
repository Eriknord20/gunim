//go:build linux && (arm64 || amd64)

package kms

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/ebitengine/purego"
)

// GBM and EGL are C libraries with no system-call interface. purego loads
// them at run time, so the build needs no C compiler and no cgo, as gunim
// already reaches libGL and libEGL on X11.

// gbm is libgbm's functions.
var gbm struct {
	createDevice            func(fd int32) uintptr
	deviceDestroy           func(dev uintptr)
	surfaceCreate           func(dev uintptr, w, h, format, flags uint32) uintptr
	surfaceDestroy          func(s uintptr)
	surfaceLockFront        func(s uintptr) uintptr
	surfaceRelease          func(s, bo uintptr)
	boGetHandle             func(bo uintptr) uint64
	boGetStride             func(bo uintptr) uint32
	boGetWidth, boGetHeight func(bo uintptr) uint32
	boGetFormat             func(bo uintptr) uint32
}

// egl is libEGL's functions.
var egl struct {
	getPlatformDisplay          func(platform uint32, native uintptr, attribs *uintptr) uintptr
	initialize                  func(dpy uintptr, major, minor *int32) uint32
	terminate                   func(dpy uintptr) uint32
	bindAPI                     func(api uint32) uint32
	chooseConfig                func(dpy uintptr, attribs *int32, configs *uintptr, size int32, n *int32) uint32
	getConfigAttrib             func(dpy, config uintptr, attrib int32, v *int32) uint32
	createContext               func(dpy, config, share uintptr, attribs *int32) uintptr
	destroyContext              func(dpy, ctx uintptr) uint32
	createPlatformWindowSurface func(dpy, config, native uintptr, attribs *uintptr) uintptr
	destroySurface              func(dpy, surface uintptr) uint32
	makeCurrent                 func(dpy, draw, read, ctx uintptr) uint32
	swapBuffers                 func(dpy, surface uintptr) uint32
	getError                    func() int32
	queryString                 func(dpy uintptr, name int32) string
}

// glGetString is OpenGL ES's, for the renderer's name.
var glGetString func(name uint32) string

var (
	loadOnce sync.Once
	errLoad  error
)

// loadLibs loads libgbm and libEGL, once.
func loadLibs() error {
	loadOnce.Do(func() { errLoad = loadLibsOnce() })
	return errLoad
}

func openLib(names ...string) (uintptr, error) {
	var errs []error
	for _, n := range names {
		h, err := purego.Dlopen(n, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err == nil {
			return h, nil
		}
		errs = append(errs, err)
	}
	return 0, errors.Join(errs...)
}

func loadLibsOnce() (err error) {
	// purego panics on a symbol it cannot find; a library too old for one
	// is an error here instead.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	g, err := openLib("libgbm.so.1", "libgbm.so")
	if err != nil {
		return fmt.Errorf("load libgbm (apt install libgbm1): %w", err)
	}
	e, err := openLib("libEGL.so.1", "libEGL.so")
	if err != nil {
		return fmt.Errorf("load libEGL (apt install libegl1 libegl-mesa0): %w", err)
	}
	purego.RegisterLibFunc(&gbm.createDevice, g, "gbm_create_device")
	purego.RegisterLibFunc(&gbm.deviceDestroy, g, "gbm_device_destroy")
	purego.RegisterLibFunc(&gbm.surfaceCreate, g, "gbm_surface_create")
	purego.RegisterLibFunc(&gbm.surfaceDestroy, g, "gbm_surface_destroy")
	purego.RegisterLibFunc(&gbm.surfaceLockFront, g, "gbm_surface_lock_front_buffer")
	purego.RegisterLibFunc(&gbm.surfaceRelease, g, "gbm_surface_release_buffer")
	purego.RegisterLibFunc(&gbm.boGetHandle, g, "gbm_bo_get_handle")
	purego.RegisterLibFunc(&gbm.boGetStride, g, "gbm_bo_get_stride")
	purego.RegisterLibFunc(&gbm.boGetWidth, g, "gbm_bo_get_width")
	purego.RegisterLibFunc(&gbm.boGetHeight, g, "gbm_bo_get_height")
	purego.RegisterLibFunc(&gbm.boGetFormat, g, "gbm_bo_get_format")

	if gles, err := openLib("libGLESv2.so.2", "libGLESv2.so"); err == nil {
		purego.RegisterLibFunc(&glGetString, gles, "glGetString")
	}
	purego.RegisterLibFunc(&egl.getPlatformDisplay, e, "eglGetPlatformDisplay")
	purego.RegisterLibFunc(&egl.initialize, e, "eglInitialize")
	purego.RegisterLibFunc(&egl.terminate, e, "eglTerminate")
	purego.RegisterLibFunc(&egl.bindAPI, e, "eglBindAPI")
	purego.RegisterLibFunc(&egl.chooseConfig, e, "eglChooseConfig")
	purego.RegisterLibFunc(&egl.getConfigAttrib, e, "eglGetConfigAttrib")
	purego.RegisterLibFunc(&egl.createContext, e, "eglCreateContext")
	purego.RegisterLibFunc(&egl.destroyContext, e, "eglDestroyContext")
	purego.RegisterLibFunc(&egl.createPlatformWindowSurface, e, "eglCreatePlatformWindowSurface")
	purego.RegisterLibFunc(&egl.destroySurface, e, "eglDestroySurface")
	purego.RegisterLibFunc(&egl.makeCurrent, e, "eglMakeCurrent")
	purego.RegisterLibFunc(&egl.swapBuffers, e, "eglSwapBuffers")
	purego.RegisterLibFunc(&egl.getError, e, "eglGetError")
	purego.RegisterLibFunc(&egl.queryString, e, "eglQueryString")
	return nil
}

// EGL's and GBM's constants.
const (
	eglPlatformGBM    = 0x31D7
	eglOpenGLESAPI    = 0x30A0
	eglNone           = 0x3038
	eglSurfaceType    = 0x3033
	eglWindowBit      = 0x0004
	eglRenderableType = 0x3040
	eglOpenGLES3Bit   = 0x0040
	eglRedSize        = 0x3024
	eglGreenSize      = 0x3023
	eglBlueSize       = 0x3022
	eglAlphaSize      = 0x3021
	eglNativeVisualID = 0x302E
	eglContextMajor   = 0x3098
	eglContextMinor   = 0x30FB
	eglVendor         = 0x3053
	eglVersion        = 0x3054
	eglTrue           = 1
	glRenderer        = 0x1F01
	eglSuccess        = 0x3000
	eglContextLost    = 0x300E

	gbmBoUseScanout   = 1 << 0
	gbmBoUseRendering = 1 << 2
)

// softwareEnv asks Mesa's GBM to draw on the CPU: kms_swrast with
// llvmpipe, into dumb buffers the screen can show. GBM reads
// GBM_ALWAYS_SOFTWARE as gbm_create_device loads its driver; purego's
// runtime hands os.Setenv to the C library's setenv. LIBGL_ALWAYS_SOFTWARE
// is not used: Mesa 26.2's swap on GBM jumps to a null function with it.
// The variable stays set for the process and its children.
func softwareEnv() error {
	if err := os.Setenv("GBM_ALWAYS_SOFTWARE", "1"); err != nil {
		return fmt.Errorf("ask Mesa for software drawing: %w", err)
	}
	return nil
}

// gpu is the GL context on the screen: a GBM device on the card, a GBM
// surface of the mode's size, and an EGL context drawing into it.
type gpu struct {
	dev, surface uintptr
	dpy, ctx     uintptr
	eglSurface   uintptr
	// version is the OpenGL ES version the context has, as "3.1", and
	// renderer the GL's name for what draws, as "V3D 7.1.10.2".
	version  string
	vendor   string
	renderer string
	// software says Mesa draws on the CPU.
	software bool
}

// eglErr is an EGL call that failed, with EGL's error.
type eglErr struct {
	call string
	code int32
}

func (e eglErr) Error() string { return fmt.Sprintf("%s failed: EGL error %#x", e.call, e.code) }

func lastEGL(call string) error { return eglErr{call: call, code: egl.getError()} }

// startGPU makes a GL context on the card's screen, of w×h pixels, current
// on the calling thread. With software it asks Mesa to draw on the CPU.
func startGPU(fd, w, h int, software bool) (*gpu, error) {
	if err := loadLibs(); err != nil {
		return nil, err
	}
	if software {
		if err := softwareEnv(); err != nil {
			return nil, err
		}
	}
	g := &gpu{software: software}
	ok := false
	defer func() {
		if !ok {
			g.stop()
		}
	}()
	g.dev = gbm.createDevice(int32(fd))
	if g.dev == 0 {
		return nil, errors.New("gbm_create_device failed")
	}
	g.surface = gbm.surfaceCreate(g.dev, uint32(w), uint32(h), formatXRGB8888, gbmBoUseScanout|gbmBoUseRendering)
	if g.surface == 0 {
		return nil, errors.New("gbm_surface_create failed")
	}
	g.dpy = egl.getPlatformDisplay(eglPlatformGBM, g.dev, nil)
	if g.dpy == 0 {
		return nil, lastEGL("eglGetPlatformDisplay")
	}
	var major, minor int32
	if egl.initialize(g.dpy, &major, &minor) != eglTrue {
		g.dpy = 0
		return nil, lastEGL("eglInitialize")
	}
	if egl.bindAPI(eglOpenGLESAPI) != eglTrue {
		return nil, lastEGL("eglBindAPI")
	}
	cfg, err := g.config()
	if err != nil {
		return nil, err
	}
	for _, v := range [][2]int32{{3, 1}, {3, 0}} {
		attribs := []int32{eglContextMajor, v[0], eglContextMinor, v[1], eglNone}
		g.ctx = egl.createContext(g.dpy, cfg, 0, &attribs[0])
		if g.ctx != 0 {
			g.version = fmt.Sprintf("%d.%d", v[0], v[1])
			break
		}
	}
	if g.ctx == 0 {
		return nil, lastEGL("eglCreateContext for OpenGL ES 3")
	}
	g.eglSurface = egl.createPlatformWindowSurface(g.dpy, cfg, g.surface, nil)
	if g.eglSurface == 0 {
		return nil, lastEGL("eglCreatePlatformWindowSurface")
	}
	if egl.makeCurrent(g.dpy, g.eglSurface, g.eglSurface, g.ctx) != eglTrue {
		return nil, lastEGL("eglMakeCurrent")
	}
	g.vendor = egl.queryString(g.dpy, eglVendor)
	if glGetString != nil {
		g.renderer = glGetString(glRenderer)
	}
	// Mesa falls back to its software renderer by itself where the GPU's
	// driver fails to load, so its name tells.
	r := strings.ToLower(g.renderer)
	g.software = g.software || strings.Contains(r, "llvmpipe") || strings.Contains(r, "softpipe") || strings.Contains(r, "swrast")
	ok = true
	return g, nil
}

// config picks an EGL config that draws OpenGL ES 3 into a window, whose
// native format is XRGB8888, as the GBM surface's.
func (g *gpu) config() (uintptr, error) {
	attribs := []int32{
		eglSurfaceType, eglWindowBit,
		eglRenderableType, eglOpenGLES3Bit,
		eglRedSize, 8, eglGreenSize, 8, eglBlueSize, 8, eglAlphaSize, 0,
		eglNone,
	}
	var n int32
	if egl.chooseConfig(g.dpy, &attribs[0], nil, 0, &n) != eglTrue || n == 0 {
		return 0, lastEGL("eglChooseConfig")
	}
	configs := make([]uintptr, n)
	if egl.chooseConfig(g.dpy, &attribs[0], &configs[0], n, &n) != eglTrue {
		return 0, lastEGL("eglChooseConfig")
	}
	for _, c := range configs[:n] {
		var id int32
		if egl.getConfigAttrib(g.dpy, c, eglNativeVisualID, &id) == eglTrue && uint32(id) == formatXRGB8888 {
			return c, nil
		}
	}
	return 0, errors.New("no EGL config draws XRGB8888")
}

// swap ends the frame drawn into the surface and locks the buffer it went
// into, for the screen to show.
func (g *gpu) swap() (bo uintptr, err error) {
	if egl.swapBuffers(g.dpy, g.eglSurface) != eglTrue {
		return 0, lastEGL("eglSwapBuffers")
	}
	bo = gbm.surfaceLockFront(g.surface)
	if bo == 0 {
		return 0, errors.New("gbm_surface_lock_front_buffer failed")
	}
	return bo, nil
}

// release hands a buffer the screen no longer shows back to the surface.
func (g *gpu) release(bo uintptr) {
	if bo != 0 {
		gbm.surfaceRelease(g.surface, bo)
	}
}

// stop lets go of the context, the surface and the device.
func (g *gpu) stop() {
	if g.dpy != 0 {
		egl.makeCurrent(g.dpy, 0, 0, 0)
		if g.eglSurface != 0 {
			egl.destroySurface(g.dpy, g.eglSurface)
		}
		if g.ctx != 0 {
			egl.destroyContext(g.dpy, g.ctx)
		}
		egl.terminate(g.dpy)
	}
	if g.surface != 0 {
		gbm.surfaceDestroy(g.surface)
	}
	if g.dev != 0 {
		gbm.deviceDestroy(g.dev)
	}
	*g = gpu{}
}

// boInfo returns a buffer's handle, stride and size, for a framebuffer.
func boInfo(bo uintptr) (handle, stride, w, h, format uint32) {
	return uint32(gbm.boGetHandle(bo)), gbm.boGetStride(bo), gbm.boGetWidth(bo), gbm.boGetHeight(bo), gbm.boGetFormat(bo)
}
