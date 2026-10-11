//go:build windows

package desktop

import (
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/marrasen/gunim/internal/gl"
)

// HDR, a spike: with GUNIM_HDR=1 a window's swap chain holds 16-bit
// floats in scRGB, linear light where 1 is 80 nits, so it can show
// colours brighter than the white of everything else on the screen. The
// renderer draws the window as ever, in 8-bit sRGB, and a last pass
// turns that into linear light at the display's SDR white level, the
// brightness Windows gives ordinary windows. GUNIM_HDR_TEST=1 has that
// pass draw a test pattern over the window too: patches at one to eight
// times SDR white, and a ramp from black to eight times; see hdrShader.

const (
	dxgiFormatR16G16B16A16Float = 10
	// dxgiColorSpaceScRGB is DXGI_COLOR_SPACE_RGB_FULL_G10_NONE_P709.
	dxgiColorSpaceScRGB         = 1
	dxgiSwapChain3SetColorSpace = 38
	dxgiSwapChain3CheckColor    = 37
	dxgiAdapterEnumOutputs      = 7
	dxgiOutput6GetDesc1         = 27
	dcGetSDRWhiteLevel          = 11
	glStaticDraw                = 0x88E4
	glUnsignedShort             = 0x1403
)

var (
	iidIDXGISwapChain3 = windows.GUID{Data1: 0x94d99bdb, Data2: 0xf1f8, Data3: 0x4ab0, Data4: [8]byte{0xb2, 0x36, 0x7d, 0xa0, 0x17, 0x0e, 0xda, 0xb1}}
	iidIDXGIOutput6    = windows.GUID{Data1: 0x068346e8, Data2: 0xaaec, Data3: 0x4b84, Data4: [8]byte{0xad, 0xd7, 0x13, 0x7f, 0x51, 0x3f, 0x77, 0xa1}}
)

// hdrWanted says the spike is on, and hdrTestWanted its test pattern.
func hdrWanted() bool     { return os.Getenv("GUNIM_HDR") == "1" }
func hdrTestWanted() bool { return os.Getenv("GUNIM_HDR_TEST") == "1" }

// hdrState is what the spike found, for HDRStatus.
var hdrState struct {
	sync.Mutex
	line string
}

func setHDRStatus(format string, args ...any) {
	s := fmt.Sprintf(format, args...)
	hdrState.Lock()
	changed := hdrState.line != s
	hdrState.line = s
	hdrState.Unlock()
	if changed {
		log.Printf("gunim hdr: %s", s)
	}
}

// HDRStatus says what the HDR spike found: whether the window shows in
// scRGB, the display's SDR white level, and its peak brightness, or why
// not. It is empty where the spike is off.
func HDRStatus() string {
	hdrState.Lock()
	defer hdrState.Unlock()
	return hdrState.line
}

// hdrPass is the last pass of a frame in HDR: it reads the frame as the
// renderer left it, in 8-bit sRGB, and writes it in scRGB.
type hdrPass struct {
	prog, vao, ebo uint32
	uSDR, uSize    int32
	uTest, uUI     int32
	// sdr is the SDR white level in scRGB, nits over 80, and peak the
	// display's peak brightness in nits, or 0 where unknown.
	sdr, peak float32
	device    string
	// at is when the display was last read: again on another display,
	// or every two seconds, as the user may move Windows' SDR content
	// brightness slider.
	at time.Time
}

const hdrVertex = `#version 150
void main() {
	// One triangle over the whole target.
	vec2 p = vec2((gl_VertexID << 1) & 2, gl_VertexID & 2);
	gl_Position = vec4(p * 2.0 - 1.0, 0.0, 1.0);
}
`

// hdrShader turns the frame's sRGB into linear light at SDR white, and
// draws the test pattern: in the band from 8% to 92% across and 40% to
// 70% down, patches at 1, 1.5, 2, 3, 4, 6 and 8 times SDR white over a
// ramp from 0 to 8 times, with marks at 1, 2, 4 and 8 times.
const hdrShader = `#version 150
uniform sampler2D ui;
uniform float sdr;
uniform vec2 size;
uniform int test;
out vec4 color;

vec3 linear(vec3 c) {
	return mix(c / 12.92, pow((c + 0.055) / 1.055, vec3(2.4)), step(0.04045, c));
}

void main() {
	vec4 s = texelFetch(ui, ivec2(gl_FragCoord.xy), 0);
	vec3 c = s.a > 0.0 ? linear(s.rgb / s.a) * s.a : vec3(0.0);
	color = vec4(c * sdr, s.a);
	if (test != 1) {
		return;
	}
	// The window's texture is upside down for Direct3D: its first row,
	// GL's y = 0, is the top of the window.
	vec2 f = gl_FragCoord.xy / size;
	if (f.x < 0.08 || f.x > 0.92 || f.y < 0.40 || f.y > 0.70) {
		return;
	}
	float u = (f.x - 0.08) / 0.84;
	float m;
	if (f.y < 0.55) {
		const float k[7] = float[7](1.0, 1.5, 2.0, 3.0, 4.0, 6.0, 8.0);
		float cell = u * 7.0;
		int i = min(int(cell), 6);
		m = fract(cell) < 0.06 || f.y > 0.54 ? 0.0 : k[i];
	} else {
		m = u * 8.0;
		float px = 8.0 / (0.84 * size.x);
		for (int j = 0; j < 4; j++) {
			float at = float(1 << j);
			if (abs(m - at) < 1.5 * px && f.y > 0.66) {
				m = 0.0;
			}
		}
	}
	color = vec4(vec3(m * sdr), 1.0);
}
`

// newHDRPass builds the pass's program.
func newHDRPass(g gl.Context) (*hdrPass, error) {
	compile := func(kind uint32, src string) (uint32, error) {
		s := g.CreateShader(kind)
		g.ShaderSource(s, src)
		g.CompileShader(s)
		if g.GetShaderi(s, gl.COMPILE_STATUS) == 0 {
			return 0, fmt.Errorf("desktop: hdr shader: %s", g.GetShaderInfoLog(s))
		}
		return s, nil
	}
	vs, err := compile(gl.VERTEX_SHADER, hdrVertex)
	if err != nil {
		return nil, err
	}
	fs, err := compile(gl.FRAGMENT_SHADER, hdrShader)
	if err != nil {
		return nil, err
	}
	h := &hdrPass{prog: g.CreateProgram(), sdr: 1}
	g.AttachShader(h.prog, vs)
	g.AttachShader(h.prog, fs)
	g.LinkProgram(h.prog)
	if g.GetProgrami(h.prog, gl.LINK_STATUS) == 0 {
		return nil, fmt.Errorf("desktop: hdr program: %s", g.GetProgramInfoLog(h.prog))
	}
	h.uSDR = g.GetUniformLocation(h.prog, "sdr")
	h.uSize = g.GetUniformLocation(h.prog, "size")
	h.uTest = g.GetUniformLocation(h.prog, "test")
	h.uUI = g.GetUniformLocation(h.prog, "ui")
	// A core profile draws with a vertex array bound, and DrawElements
	// wants its indices in a buffer of it.
	h.vao = g.CreateVertexArray()
	g.BindVertexArray(h.vao)
	h.ebo = g.CreateBuffer()
	g.BindBuffer(gl.ELEMENT_ARRAY_BUFFER, h.ebo)
	idx := make([]byte, 6)
	for i := range 3 {
		binary.LittleEndian.PutUint16(idx[2*i:], uint16(i))
	}
	g.BufferInit(gl.ELEMENT_ARRAY_BUFFER, len(idx), glStaticDraw)
	g.BufferSubData(gl.ELEMENT_ARRAY_BUFFER, 0, idx)
	g.BindVertexArray(0)
	return h, nil
}

// draw writes the frame in tex, w by h, into the framebuffer fbo in
// scRGB. The renderer sets what it needs again at the start of each
// frame, but for blending, which it leaves on.
func (h *hdrPass) draw(g gl.Context, fbo, tex uint32, w, hgt int) {
	g.BindFramebuffer(gl.FRAMEBUFFER, fbo)
	g.Viewport(0, 0, int32(w), int32(hgt))
	g.Disable(gl.SCISSOR_TEST)
	g.Disable(gl.BLEND)
	g.UseProgram(h.prog)
	g.ActiveTexture(gl.TEXTURE0)
	g.BindTexture(gl.TEXTURE_2D, tex)
	g.Uniform1i(h.uUI, 0)
	g.Uniform1fv(h.uSDR, []float32{h.sdr})
	g.Uniform2fv(h.uSize, []float32{float32(w), float32(hgt)})
	test := int32(0)
	if hdrTestWanted() {
		test = 1
	}
	g.Uniform1i(h.uTest, test)
	g.BindVertexArray(h.vao)
	g.DrawElements(gl.TRIANGLES, 3, glUnsignedShort, 0)
	g.BindVertexArray(0)
	g.Enable(gl.BLEND)
}

// useScRGB has the swap chain show its buffers as scRGB.
func useScRGB(swap uintptr) error {
	var sc3 uintptr
	if hr := call(swap, comQueryInterface, uintptr(unsafe.Pointer(&iidIDXGISwapChain3)), uintptr(unsafe.Pointer(&sc3))); failed(hr) {
		return fmt.Errorf("IDXGISwapChain3: HRESULT %#x", uint32(hr))
	}
	defer release(sc3)
	var support uint32
	if hr := call(sc3, dxgiSwapChain3CheckColor, dxgiColorSpaceScRGB, uintptr(unsafe.Pointer(&support))); failed(hr) || support&1 == 0 {
		return fmt.Errorf("the swap chain cannot present scRGB (support %#x, HRESULT %#x)", support, uint32(hr))
	}
	if hr := call(sc3, dxgiSwapChain3SetColorSpace, dxgiColorSpaceScRGB); failed(hr) {
		return fmt.Errorf("SetColorSpace1: HRESULT %#x", uint32(hr))
	}
	return nil
}

// outputDesc1 is DXGI_OUTPUT_DESC1.
type outputDesc1 struct {
	DeviceName            [32]uint16
	Desktop               [4]int32
	Attached              int32
	Rotation              uint32
	Monitor               uintptr
	BitsPerColor          uint32
	ColorSpace            uint32
	Primaries             [8]float32
	MinLuminance          float32
	MaxLuminance          float32
	MaxFullFrameLuminance float32
}

// peakOf is the peak brightness in nits of the display named device, as
// the adapter's outputs report it, and its colour space and bits.
func peakOf(adapter uintptr, device string) (peak, full float32, space, bits uint32, err error) {
	for i := uintptr(0); ; i++ {
		var out uintptr
		if hr := call(adapter, dxgiAdapterEnumOutputs, i, uintptr(unsafe.Pointer(&out))); failed(hr) {
			return 0, 0, 0, 0, fmt.Errorf("no output of the adapter is %s", device)
		}
		var o6 uintptr
		hr := call(out, comQueryInterface, uintptr(unsafe.Pointer(&iidIDXGIOutput6)), uintptr(unsafe.Pointer(&o6)))
		release(out)
		if failed(hr) {
			continue
		}
		var d outputDesc1
		hr = call(o6, dxgiOutput6GetDesc1, uintptr(unsafe.Pointer(&d)))
		release(o6)
		if failed(hr) || windows.UTF16ToString(d.DeviceName[:]) != device {
			continue
		}
		return d.MaxLuminance, d.MaxFullFrameLuminance, d.ColorSpace, d.BitsPerColor, nil
	}
}

// sdrWhiteOf is the SDR white level in nits Windows gives ordinary
// windows on the display named device.
func sdrWhiteOf(device string) (float32, error) {
	var np, nm uint32
	if r, _, _ := procGetDisplayConfigBufferSizes.Call(qdcOnlyActivePaths, uintptr(unsafe.Pointer(&np)), uintptr(unsafe.Pointer(&nm))); r != 0 || np == 0 {
		return 0, errors.New("no display paths")
	}
	paths := make([]dcPath, np)
	modes := make([]dcMode, max(nm, 1))
	if r, _, _ := procQueryDisplayConfig.Call(qdcOnlyActivePaths, uintptr(unsafe.Pointer(&np)), uintptr(unsafe.Pointer(&paths[0])),
		uintptr(unsafe.Pointer(&nm)), uintptr(unsafe.Pointer(&modes[0])), 0); r != 0 {
		return 0, errors.New("QueryDisplayConfig failed")
	}
	for _, p := range paths[:np] {
		name := dcSourceName{header: dcHeader{kind: dcGetSourceName, size: uint32(unsafe.Sizeof(dcSourceName{})), adapter: p.sourceAdapter, id: p.sourceID}}
		if r, _, _ := procDisplayConfigGetDeviceInfo.Call(uintptr(unsafe.Pointer(&name))); r != 0 || windows.UTF16ToString(name.gdiName[:]) != device {
			continue
		}
		white := struct {
			header dcHeader
			level  uint32
		}{header: dcHeader{kind: dcGetSDRWhiteLevel, size: 24, adapter: p.targetAdapter, id: p.targetID}}
		white.header.size = uint32(unsafe.Sizeof(white))
		if r, _, _ := procDisplayConfigGetDeviceInfo.Call(uintptr(unsafe.Pointer(&white))); r != 0 {
			return 0, fmt.Errorf("DisplayConfigGetDeviceInfo: %d", r)
		}
		// A thousand is 80 nits.
		return float32(white.level) / 1000 * 80, nil
	}
	return 0, fmt.Errorf("no display path is %s", device)
}

// monitorDevice is the GDI name of the monitor hwnd is on, such as
// \\.\DISPLAY1.
func monitorDevice(hwnd windows.HWND) string {
	const monitorDefaultToNearest = 2
	m, _, _ := procMonitorFromWindow.Call(uintptr(hwnd), monitorDefaultToNearest)
	if m == 0 {
		return ""
	}
	mi := monitorInfoEx{cbSize: uint32(unsafe.Sizeof(monitorInfoEx{}))}
	if ok, _, _ := procGetMonitorInfoW.Call(m, uintptr(unsafe.Pointer(&mi))); ok == 0 {
		return ""
	}
	return windows.UTF16ToString(mi.device[:])
}

// measure reads the display's SDR white and peak for the window, and
// says what it found.
func (h *hdrPass) measure(device string, adapter uintptr) {
	if device == "" || device == h.device && time.Since(h.at) < 2*time.Second {
		return
	}
	h.device, h.at = device, time.Now()
	white, werr := sdrWhiteOf(device)
	if werr == nil && white > 0 {
		h.sdr = white / 80
	}
	var peakNote string
	if adapter != 0 {
		peak, full, space, bits, err := peakOf(adapter, device)
		if err == nil {
			h.peak = peak
			// DXGI_COLOR_SPACE_RGB_FULL_G2084_NONE_P2020 is HDR10.
			hdr := map[bool]string{false: "the display is not in HDR", true: "the display is in HDR"}[space == 12]
			peakNote = fmt.Sprintf("peak %.0f nits, %.0f over the whole screen, %d bits, %s", peak, full, bits, hdr)
		} else {
			peakNote = "peak unknown: " + err.Error()
		}
	}
	whiteNote := fmt.Sprintf("SDR white %.0f nits", h.sdr*80)
	if werr != nil {
		whiteNote = "SDR white unknown (" + werr.Error() + "), taken as 80 nits"
	}
	headroom := ""
	if h.peak > 0 {
		headroom = fmt.Sprintf("; headroom %.1fx SDR white (%.1f stops)", h.peak/(h.sdr*80), math.Log2(float64(h.peak/(h.sdr*80))))
	}
	setHDRStatus("scRGB on %s: %s, %s%s", device, whiteNote, peakNote, headroom)
}

// adapterOf is the DXGI adapter of the Direct3D device, for the caller
// to release.
func adapterOf(device uintptr) uintptr {
	var dxgiDevice, adapter uintptr
	if hr := call(device, comQueryInterface, uintptr(unsafe.Pointer(&iidIDXGIDevice)), uintptr(unsafe.Pointer(&dxgiDevice))); failed(hr) {
		return 0
	}
	defer release(dxgiDevice)
	if hr := call(dxgiDevice, dxgiDeviceGetAdapter, uintptr(unsafe.Pointer(&adapter))); failed(hr) {
		return 0
	}
	return adapter
}
