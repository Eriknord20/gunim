//go:build windows

package desktop

import (
	"errors"
	"fmt"
	"math"
	"os"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/internal/gl"
	"github.com/marrasen/gunim/internal/glfw"
)

// On Windows a window shows its frames through DXGI, the way Direct3D
// programs do, where the machine allows.
//
// NVIDIA's OpenGL driver hands its frames to the compositor by a path
// that fails on a display Windows puts in a monitor's place, such as
// when the monitor is switched off, and so over remote desktop tools:
// the window stays white (issue #9). DXGI's path works everywhere.
//
// So each window's OpenGL context lives on a hidden window of its own,
// and the renderer draws, as it does everywhere, into a Direct3D 11
// texture it shares with OpenGL through WGL_NV_DX_interop2. The texture
// is copied into a flip-model swap chain, which DirectComposition shows
// in the window. The swap chain is resized with the window, so a frame
// always shows at the size it was drawn, and it carries alpha, so a
// popup's round corners and shadow blend with what is behind it.
//
// Where Direct3D 11, DirectComposition or the interop extension is
// missing, as under some virtual machines, the window presents through
// OpenGL as on the other platforms. GUNIM_PRESENT=gl chooses that too.

var (
	d3d11dll                     = windows.NewLazySystemDLL("d3d11.dll")
	dcompdll                     = windows.NewLazySystemDLL("dcomp.dll")
	opengl32dll                  = windows.NewLazySystemDLL("opengl32.dll")
	procD3D11CreateDevice        = d3d11dll.NewProc("D3D11CreateDevice")
	procDCompositionCreateDevice = dcompdll.NewProc("DCompositionCreateDevice")
	procWglGetProcAddress        = opengl32dll.NewProc("wglGetProcAddress")
	procWglGetCurrentDC          = opengl32dll.NewProc("wglGetCurrentDC")
	iidIDXGIDevice               = windows.GUID{Data1: 0x54ec77fa, Data2: 0x1377, Data3: 0x44e6, Data4: [8]byte{0x8c, 0x32, 0x88, 0xfd, 0x5f, 0x44, 0xc8, 0x4c}}
	iidIDXGIFactory2             = windows.GUID{Data1: 0x50c83a1c, Data2: 0xe072, Data3: 0x4c48, Data4: [8]byte{0x87, 0xb0, 0x36, 0x30, 0xfa, 0x36, 0xa6, 0xd0}}
	iidIDCompositionDevice       = windows.GUID{Data1: 0xc37ea93a, Data2: 0xe7aa, Data3: 0x450d, Data4: [8]byte{0xb1, 0x6f, 0x97, 0x46, 0xcb, 0x04, 0x07, 0xf3}}
	iidID3D11Texture2D           = windows.GUID{Data1: 0x6f15aaf2, Data2: 0xd208, Data3: 0x4e89, Data4: [8]byte{0x9a, 0xb4, 0x48, 0x95, 0x35, 0xd3, 0x4f, 0x9c}}
	errNoDXGI                    = errors.New("desktop: presenting through DXGI is unavailable")
)

const (
	d3dDriverTypeHardware        = 1
	d3d11CreateDeviceBGRA        = 0x20
	d3d11SDKVersion              = 7
	dxgiFormatR8G8B8A8           = 28
	dxgiUsageRenderTarget        = 0x20
	dxgiScalingStretch           = 0
	dxgiSwapEffectFlipSequential = 3
	dxgiAlphaPremultiplied       = 1
	d3d11BindRenderTarget        = 0x20
	d3d11BindShaderResource      = 0x8
	glTexture2D                  = 0x0DE1
	wglAccessWriteDiscardNV      = 0x0002
)

// Method numbers in the COM interfaces used here.
const (
	comRelease                   = 2
	comQueryInterface            = 0
	dxgiObjectGetParent          = 6
	dxgiDeviceGetAdapter         = 7
	dxgiFactory2SwapChainForComp = 24
	dxgiSwapChainPresent         = 8
	dxgiSwapChainGetBuffer       = 9
	dxgiSwapChainResizeBuffers   = 13
	dxgiSwapChain1Present1       = 22
	d3d11DeviceCreateTexture2D   = 5
	d3d11ContextCopyResource     = 47
	d3d11ContextFlush            = 111
	dcompDeviceCommit            = 3
	dcompDeviceCreateTargetHwnd  = 6
	dcompDeviceCreateVisual      = 7
	dcompTargetSetRoot           = 3
	dcompVisualSetContent        = 15
)

// call calls method m of COM object obj.
//
// Arguments are often the address of a Go variable, turned into a
// uintptr for the call. Go may move a goroutine's stack at any moment
// and has no way to know a uintptr points into it, so COM would write
// to where the variable used to be. uintptrescapes moves any variable
// whose address a caller passes here off the stack, where it stays put
// for the length of the call, as it does for the syscall package's own
// calls.
//
//go:uintptrescapes
func call(obj uintptr, m int, args ...uintptr) uintptr {
	// COM hands its objects over as addresses of memory Go never
	// allocated or moves.
	vtbl := *(*uintptr)(unsafe.Pointer(obj))                                       //nolint:govet // a COM object's address
	fn := *(*uintptr)(unsafe.Pointer(vtbl + uintptr(m)*unsafe.Sizeof(uintptr(0)))) //nolint:govet // its method table's entry
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{obj}, args...)...)
	return r
}

func release(obj uintptr) {
	if obj != 0 {
		call(obj, comRelease)
	}
}

func failed(hr uintptr) bool { return int32(hr) < 0 }

// presentsThroughDXGI reports whether windows present through DXGI on
// this machine. It runs on the main thread, once, at Open, with a
// throwaway context to ask the driver for the interop extension.
func (d *Driver) presentsThroughDXGI() bool {
	if os.Getenv("GUNIM_PRESENT") == "gl" {
		return false
	}
	if procD3D11CreateDevice.Find() != nil || procDCompositionCreateDevice.Find() != nil {
		return false
	}
	var dev, ctx uintptr
	var level uint32
	if hr, _, _ := procD3D11CreateDevice.Call(0, d3dDriverTypeHardware, 0, d3d11CreateDeviceBGRA, 0, 0,
		d3d11SDKVersion, uintptr(unsafe.Pointer(&dev)), uintptr(unsafe.Pointer(&level)),
		uintptr(unsafe.Pointer(&ctx))); failed(hr) {
		return false
	}
	release(ctx)
	release(dev)

	if err := glfw.DefaultWindowHints(); err != nil {
		return false
	}
	if err := d.setContextHints(); err != nil {
		return false
	}
	if err := glfw.WindowHint(glfw.Visible, glfw.False); err != nil {
		return false
	}
	probe, err := glfw.CreateWindow(1, 1, "", nil, nil)
	if err != nil {
		return false
	}
	defer func() { _ = probe.Destroy() }()
	if err := probe.MakeContextCurrent(); err != nil {
		return false
	}
	defer func() { _ = (*glfw.Window)(nil).MakeContextCurrent() }()
	ext := wglProc("wglGetExtensionsStringARB")
	if ext == 0 {
		return false
	}
	dc, _, _ := procWglGetCurrentDC.Call()
	s, _, _ := syscall.SyscallN(ext, dc)
	if s == 0 {
		return false
	}
	names := windows.BytePtrToString((*byte)(unsafe.Pointer(s))) //nolint:govet // a string the driver owns
	return strings.Contains(names, "WGL_NV_DX_interop2")
}

func wglProc(name string) uintptr {
	b, err := windows.BytePtrFromString(name)
	if err != nil {
		return 0
	}
	r, _, _ := procWglGetProcAddress.Call(uintptr(unsafe.Pointer(b)))
	return r
}

// presenter shows one window's frames through DXGI. It belongs to the
// window's render thread, with the window's context current.
type presenter struct {
	g gl.Context

	device, context uintptr
	swap            uintptr
	dcomp           uintptr
	target, visual  uintptr

	// texture is the Direct3D texture the renderer draws into, tex and
	// fbo its OpenGL side, and share and object the interop's handles
	// for the device and the texture.
	texture  uintptr
	tex, fbo uint32
	share    uintptr
	object   uintptr
	w, h     int
	// whole is set until a frame of the swap chain's size has been shown, which must cover all of it.
	whole bool

	wglDXOpenDevice, wglDXCloseDevice       uintptr
	wglDXRegisterObject, wglDXUnregisterObj uintptr
	wglDXLockObjects, wglDXUnlockObjects    uintptr
}

// newPresenter sets up DXGI presentation for the window hwnd.
func newPresenter(g gl.Context, hwnd windows.HWND) (*presenter, error) {
	p := &presenter{
		g:                   g,
		wglDXOpenDevice:     wglProc("wglDXOpenDeviceNV"),
		wglDXCloseDevice:    wglProc("wglDXCloseDeviceNV"),
		wglDXRegisterObject: wglProc("wglDXRegisterObjectNV"),
		wglDXUnregisterObj:  wglProc("wglDXUnregisterObjectNV"),
		wglDXLockObjects:    wglProc("wglDXLockObjectsNV"),
		wglDXUnlockObjects:  wglProc("wglDXUnlockObjectsNV"),
	}
	if p.wglDXOpenDevice == 0 || p.wglDXRegisterObject == 0 || p.wglDXLockObjects == 0 {
		return nil, errNoDXGI
	}
	ok := false
	defer func() {
		if !ok {
			p.close()
		}
	}()

	var level uint32
	if hr, _, _ := procD3D11CreateDevice.Call(0, d3dDriverTypeHardware, 0, d3d11CreateDeviceBGRA, 0, 0,
		d3d11SDKVersion, uintptr(unsafe.Pointer(&p.device)), uintptr(unsafe.Pointer(&level)),
		uintptr(unsafe.Pointer(&p.context))); failed(hr) {
		return nil, fmt.Errorf("desktop: D3D11CreateDevice: HRESULT %#x", uint32(hr))
	}

	// The swap chain comes from the device's own DXGI factory.
	var dxgiDevice, adapter, factory uintptr
	if hr := call(p.device, comQueryInterface, uintptr(unsafe.Pointer(&iidIDXGIDevice)), uintptr(unsafe.Pointer(&dxgiDevice))); failed(hr) {
		return nil, fmt.Errorf("desktop: IDXGIDevice: HRESULT %#x", uint32(hr))
	}
	defer release(dxgiDevice)
	if hr := call(dxgiDevice, dxgiDeviceGetAdapter, uintptr(unsafe.Pointer(&adapter))); failed(hr) {
		return nil, fmt.Errorf("desktop: GetAdapter: HRESULT %#x", uint32(hr))
	}
	defer release(adapter)
	if hr := call(adapter, dxgiObjectGetParent, uintptr(unsafe.Pointer(&iidIDXGIFactory2)), uintptr(unsafe.Pointer(&factory))); failed(hr) {
		return nil, fmt.Errorf("desktop: IDXGIFactory2: HRESULT %#x", uint32(hr))
	}
	defer release(factory)

	desc := swapChainDesc{
		Width: 1, Height: 1, Format: dxgiFormatR8G8B8A8,
		SampleCount: 1, BufferUsage: dxgiUsageRenderTarget, BufferCount: 2,
		Scaling: dxgiScalingStretch, SwapEffect: dxgiSwapEffectFlipSequential, AlphaMode: dxgiAlphaPremultiplied,
	}
	if hr := call(factory, dxgiFactory2SwapChainForComp, p.device, uintptr(unsafe.Pointer(&desc)), 0,
		uintptr(unsafe.Pointer(&p.swap))); failed(hr) {
		return nil, fmt.Errorf("desktop: CreateSwapChainForComposition: HRESULT %#x", uint32(hr))
	}
	p.w, p.h = 1, 1

	// DirectComposition shows the swap chain in the window.
	if hr, _, _ := procDCompositionCreateDevice.Call(dxgiDevice, uintptr(unsafe.Pointer(&iidIDCompositionDevice)),
		uintptr(unsafe.Pointer(&p.dcomp))); failed(hr) {
		return nil, fmt.Errorf("desktop: DCompositionCreateDevice: HRESULT %#x", uint32(hr))
	}
	// The frames show behind the window's children, so a child window
	// of the system's put in it, as a plugin's editor, shows over them.
	if hr := call(p.dcomp, dcompDeviceCreateTargetHwnd, uintptr(hwnd), 0, uintptr(unsafe.Pointer(&p.target))); failed(hr) {
		return nil, fmt.Errorf("desktop: CreateTargetForHwnd: HRESULT %#x", uint32(hr))
	}
	if hr := call(p.dcomp, dcompDeviceCreateVisual, uintptr(unsafe.Pointer(&p.visual))); failed(hr) {
		return nil, fmt.Errorf("desktop: CreateVisual: HRESULT %#x", uint32(hr))
	}
	if hr := call(p.visual, dcompVisualSetContent, p.swap); failed(hr) {
		return nil, fmt.Errorf("desktop: SetContent: HRESULT %#x", uint32(hr))
	}
	if hr := call(p.target, dcompTargetSetRoot, p.visual); failed(hr) {
		return nil, fmt.Errorf("desktop: SetRoot: HRESULT %#x", uint32(hr))
	}
	if hr := call(p.dcomp, dcompDeviceCommit); failed(hr) {
		return nil, fmt.Errorf("desktop: Commit: HRESULT %#x", uint32(hr))
	}

	// OpenGL's side of the device.
	share, _, _ := syscall.SyscallN(p.wglDXOpenDevice, p.device)
	if share == 0 {
		return nil, errors.New("desktop: wglDXOpenDeviceNV failed")
	}
	p.share = share
	p.fbo = g.CreateFramebuffer()
	ok = true
	return p, nil
}

// swapChainDesc is DXGI_SWAP_CHAIN_DESC1.
type swapChainDesc struct {
	Width, Height uint32
	Format        uint32
	Stereo        int32
	SampleCount   uint32
	SampleQuality uint32
	BufferUsage   uint32
	BufferCount   uint32
	Scaling       uint32
	SwapEffect    uint32
	AlphaMode     uint32
	Flags         uint32
}

// textureDesc is D3D11_TEXTURE2D_DESC.
type textureDesc struct {
	Width, Height  uint32
	MipLevels      uint32
	ArraySize      uint32
	Format         uint32
	SampleCount    uint32
	SampleQuality  uint32
	Usage          uint32
	BindFlags      uint32
	CPUAccessFlags uint32
	MiscFlags      uint32
}

// begin readies a frame of w by h pixels and returns the framebuffer to
// draw it into. The texture behind it belongs to OpenGL until present.
func (p *presenter) begin(w, h int) (uint32, error) {
	if w != p.w || h != p.h || p.texture == 0 {
		if err := p.resize(w, h); err != nil {
			return 0, err
		}
	}
	obj := p.object
	if r, _, _ := syscall.SyscallN(p.wglDXLockObjects, p.share, 1, uintptr(unsafe.Pointer(&obj))); r == 0 {
		return 0, errors.New("desktop: wglDXLockObjectsNV failed")
	}
	// OpenGL may touch the texture only while it holds the lock.
	p.g.BindFramebuffer(gl.FRAMEBUFFER, p.fbo)
	p.g.FramebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, p.tex, 0)
	return p.fbo, nil
}

// present hands the frame drawn since begin to the window, where it differs from the last one within dirty, in
// device pixels from the top left.
func (p *presenter) present(dirty geom.Rect) error {
	obj := p.object
	if r, _, _ := syscall.SyscallN(p.wglDXUnlockObjects, p.share, 1, uintptr(unsafe.Pointer(&obj))); r == 0 {
		return errors.New("desktop: wglDXUnlockObjectsNV failed")
	}
	var back uintptr
	if hr := call(p.swap, dxgiSwapChainGetBuffer, 0, uintptr(unsafe.Pointer(&iidID3D11Texture2D)), uintptr(unsafe.Pointer(&back))); failed(hr) {
		return fmt.Errorf("desktop: GetBuffer: HRESULT %#x", uint32(hr))
	}
	call(p.context, d3d11ContextCopyResource, back, p.texture)
	release(back)
	// The pacing is the vertical blank wait's, so present at once.
	rect := &[4]int32{
		max(int32(dirty.Min.X), 0), max(int32(dirty.Min.Y), 0),
		min(int32(math.Ceil(float64(dirty.Max.X))), int32(p.w)), min(int32(math.Ceil(float64(dirty.Max.Y))), int32(p.h)),
	}
	if p.whole || rect[2] <= rect[0] || rect[3] <= rect[1] {
		if hr := call(p.swap, dxgiSwapChainPresent, 0, 0); failed(hr) {
			return fmt.Errorf("desktop: Present: HRESULT %#x", uint32(hr))
		}
		p.whole = false
		return nil
	}
	// Tells the compositor which part of the window changed.
	// The rectangle is on the heap, which Go does not move, and kept alive past the call.
	params := presentParameters{DirtyRectsCount: 1, DirtyRects: uintptr(unsafe.Pointer(rect))}
	hr := call(p.swap, dxgiSwapChain1Present1, 0, 0, uintptr(unsafe.Pointer(&params)))
	runtime.KeepAlive(rect)
	if failed(hr) {
		return fmt.Errorf("desktop: Present1: HRESULT %#x", uint32(hr))
	}
	return nil
}

// presentParameters is DXGI_PRESENT_PARAMETERS.
type presentParameters struct {
	DirtyRectsCount uint32
	DirtyRects      uintptr
	ScrollRect      uintptr
	ScrollOffset    uintptr
}

// resize sizes the swap chain and the shared texture to w by h.
func (p *presenter) resize(w, h int) error {
	p.dropTexture()
	if hr := call(p.swap, dxgiSwapChainResizeBuffers, 0, uintptr(w), uintptr(h), 0, 0); failed(hr) {
		return fmt.Errorf("desktop: ResizeBuffers: HRESULT %#x", uint32(hr))
	}
	desc := textureDesc{
		Width: uint32(w), Height: uint32(h), MipLevels: 1, ArraySize: 1, Format: dxgiFormatR8G8B8A8,
		SampleCount: 1, BindFlags: d3d11BindRenderTarget | d3d11BindShaderResource,
	}
	if hr := call(p.device, d3d11DeviceCreateTexture2D, uintptr(unsafe.Pointer(&desc)), 0, uintptr(unsafe.Pointer(&p.texture))); failed(hr) {
		p.texture = 0
		return fmt.Errorf("desktop: CreateTexture2D: HRESULT %#x", uint32(hr))
	}
	p.tex = p.g.CreateTexture()
	obj, _, _ := syscall.SyscallN(p.wglDXRegisterObject, p.share, p.texture, uintptr(p.tex), glTexture2D, wglAccessWriteDiscardNV)
	if obj == 0 {
		return errors.New("desktop: wglDXRegisterObjectNV failed")
	}
	p.object = obj
	p.w, p.h, p.whole = w, h, true
	return nil
}

// dropTexture lets go of the shared texture.
func (p *presenter) dropTexture() {
	if p.object != 0 {
		_, _, _ = syscall.SyscallN(p.wglDXUnregisterObj, p.share, p.object)
		p.object = 0
	}
	if p.tex != 0 {
		p.g.DeleteTexture(p.tex)
		p.tex = 0
	}
	release(p.texture)
	p.texture = 0
}

// close lets go of everything.
func (p *presenter) close() {
	p.dropTexture()
	if p.fbo != 0 {
		p.g.DeleteFramebuffer(p.fbo)
		p.fbo = 0
	}
	if p.share != 0 {
		_, _, _ = syscall.SyscallN(p.wglDXCloseDevice, p.share)
		p.share = 0
	}
	for _, o := range []*uintptr{&p.visual, &p.target, &p.dcomp, &p.swap, &p.context, &p.device} {
		release(*o)
		*o = 0
	}
}

// startPresenter sets up DXGI presentation for the window, on its
// render thread with its context current.
func (w *Window) startPresenter(g gl.Context) (*presenter, error) {
	hwnd, err := w.gw.GetWin32Window()
	if err != nil {
		return nil, err
	}
	return newPresenter(g, hwnd)
}
