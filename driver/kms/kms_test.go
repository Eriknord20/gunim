//go:build linux && (arm64 || amd64)

package kms

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
	"unsafe"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

// The structures go to the kernel as they are, so their sizes are the
// kernel's on 64-bit Linux.
func TestStructuresHaveTheKernelsSizes(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("the sizes checked are 64-bit Linux's")
	}
	for _, c := range []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"drm_version", unsafe.Sizeof(drmVersion{}), 64},
		{"drm_mode_card_res", unsafe.Sizeof(modeCardRes{}), 64},
		{"drm_mode_modeinfo", unsafe.Sizeof(modeInfo{}), 68},
		{"drm_mode_get_connector", unsafe.Sizeof(modeGetConnector{}), 80},
		{"drm_mode_get_encoder", unsafe.Sizeof(modeGetEncoder{}), 20},
		{"drm_mode_get_plane_res", unsafe.Sizeof(modeGetPlaneRes{}), 16},
		{"drm_mode_get_plane", unsafe.Sizeof(modeGetPlane{}), 32},
		{"drm_mode_obj_get_properties", unsafe.Sizeof(modeObjGetProperties{}), 32},
		{"drm_mode_get_property", unsafe.Sizeof(modeGetProperty{}), 64},
		{"drm_mode_fb_cmd2", unsafe.Sizeof(modeFBCmd2{}), 104},
		{"drm_mode_atomic", unsafe.Sizeof(modeAtomic{}), 56},
		{"drm_mode_create_blob", unsafe.Sizeof(modeCreateBlob{}), 16},
		{"input_absinfo", unsafe.Sizeof(absInfo{}), 24},
		{"vt_mode", unsafe.Sizeof(vtMode{}), 8},
	} {
		if c.got != c.want {
			t.Errorf("%s is %d bytes, want %d", c.name, c.got, c.want)
		}
	}
	if got := unsafe.Offsetof(modeFBCmd2{}.modifiers); got != 72 {
		t.Errorf("drm_mode_fb_cmd2's modifiers at %d, want 72", got)
	}
	// DRM_IOCTL_MODE_ATOMIC and DRM_IOCTL_MODE_ADDFB2, as libdrm has them.
	if ioctlAtomic != 0xc03864bc || ioctlAddFB2 != 0xc06864b8 {
		t.Errorf("atomic ioctl %#x, addfb2 %#x", ioctlAtomic, ioctlAddFB2)
	}
}

// The trial Pi's Touch Display 2: 83 333 kHz over 937 by 1 482.
func TestRateComesFromTheTimings(t *testing.T) {
	m := modeInfo{Clock: 83333, HDisplay: 720, HTotal: 937, VDisplay: 1280, VTotal: 1482, VRefresh: 60}
	if got := m.rate(); math.Abs(got-60.011) > 0.001 {
		t.Fatalf("rate %.4f, want 60.011", got)
	}
	b := m.bytes()
	if len(b) != 68 || binary.NativeEndian.Uint32(b) != 83333 || binary.NativeEndian.Uint16(b[4:]) != 720 ||
		binary.NativeEndian.Uint16(b[14:]) != 1280 || binary.NativeEndian.Uint32(b[24:]) != 60 {
		t.Fatalf("mode blob % x", b)
	}
}

func TestDensityIsTheNearestQuarter(t *testing.T) {
	for _, c := range []struct {
		w    int
		mm   uint32
		want float32
	}{
		{720, 87, 1.25},  // the 7" Touch Display 2
		{720, 62, 1.75},  // the 5"
		{1200, 135, 1.5}, // the 10"
		{1920, 0, 1},     // a screen that doesn't say
		{1920, 527, 1},   // a 24" monitor, never below 1
	} {
		if got := density(c.w, c.mm); got != c.want {
			t.Errorf("%d px over %d mm: %v, want %v", c.w, c.mm, got, c.want)
		}
	}
}

// fakeDev keeps the atomic commit it was handed.
type fakeDev struct{ req modeAtomic }

func (f *fakeDev) atomic(r *modeAtomic) error {
	f.req = *r
	return nil
}

func addr[T any](s []T) uint64 { return uint64(uintptr(unsafe.Pointer(&s[0]))) }

func TestAtomicRequestGroupsPropertiesByObject(t *testing.T) {
	var a atomicReq
	a.add(10, 1, 100)
	a.add(20, 2, 200)
	a.add(20, 3, 300)
	a.add(30, 4, 400)
	var f fakeDev
	if err := a.commit(&f, atomicNonblock|atomicPageFlipEvent, 7); err != nil {
		t.Fatal(err)
	}
	r := f.req
	if r.flags != atomicNonblock|atomicPageFlipEvent || r.userData != 7 || r.countObjs != 3 {
		t.Fatalf("flags %#x, user data %d, %d objects", r.flags, r.userData, r.countObjs)
	}
	if r.objs != addr(a.objs) || r.countProps != addr(a.counts) || r.props != addr(a.props) || r.values != addr(a.values) {
		t.Fatal("the request points elsewhere than its arrays")
	}
	if !slices.Equal(a.objs, []uint32{10, 20, 30}) || !slices.Equal(a.counts, []uint32{1, 2, 1}) ||
		!slices.Equal(a.props, []uint32{1, 2, 3, 4}) || !slices.Equal(a.values, []uint64{100, 200, 300, 400}) {
		t.Fatalf("built %v %v %v %v", a.objs, a.counts, a.props, a.values)
	}
	a.reset()
	f.req = modeAtomic{}
	if err := a.commit(&f, 0, 0); err != nil || f.req.countObjs != 0 || f.req.flags != 0 {
		t.Fatalf("an empty request went to the kernel")
	}
}

func TestAtomicRequestAllocatesNothingOnceWarm(t *testing.T) {
	var a atomicReq
	var f countDev
	build := func() {
		a.reset()
		a.add(5, 1, 1)
		a.add(5, 2, 2)
		_ = a.commit(&f, atomicNonblock, 0)
	}
	build()
	if n := testing.AllocsPerRun(100, build); n != 0 {
		t.Fatalf("%v allocations a commit", n)
	}
}

type countDev struct{ n int }

func (c *countDev) atomic(*modeAtomic) error { c.n++; return nil }

func TestFlipEventsAreRead(t *testing.T) {
	var b []byte
	put := func(typ uint32, seq uint32) {
		ev := make([]byte, 32)
		binary.NativeEndian.PutUint32(ev, typ)
		binary.NativeEndian.PutUint32(ev[4:], 32)
		binary.NativeEndian.PutUint64(ev[8:], 5)
		binary.NativeEndian.PutUint32(ev[16:], 12)
		binary.NativeEndian.PutUint32(ev[20:], 500)
		binary.NativeEndian.PutUint32(ev[24:], seq)
		binary.NativeEndian.PutUint32(ev[28:], 36)
		b = append(b, ev...)
	}
	put(eventFlipComplete, 41)
	put(0x01, 42) // a vblank event, not asked for
	put(eventFlipComplete, 43)
	var got []flipEvent
	parseEvents(b, func(e flipEvent) { got = append(got, e) })
	want := []flipEvent{{userData: 5, sec: 12, usec: 500, sequence: 41, crtc: 36}, {userData: 5, sec: 12, usec: 500, sequence: 43, crtc: 36}}
	if !slices.Equal(got, want) {
		t.Fatalf("read %+v, want %+v", got, want)
	}
	parseEvents(b[:20], func(flipEvent) { t.Fatal("read a cut event") })
}

// stream builds evdev events for a decoder, reports 17 ms apart.
type stream struct {
	at  time.Duration
	evs []event
}

func (s *stream) abs(code uint16, v int32) {
	s.evs = append(s.evs, event{sec: int64(s.at / time.Second), usec: int64(s.at % time.Second / time.Microsecond), typ: evAbs, code: code, value: v})
}

func (s *stream) syn(code uint16) {
	s.evs = append(s.evs, event{typ: evSyn, code: code})
	s.at += 17 * time.Millisecond
}

func (s *stream) down(slot, id, x, y int32) {
	s.abs(absMTSlot, slot)
	s.abs(absMTTrackingID, id)
	s.abs(absMTPositionX, x)
	s.abs(absMTPositionY, y)
}

func (s *stream) up(slot int32) {
	s.abs(absMTSlot, slot)
	s.abs(absMTTrackingID, -1)
}

func decode(m *mtDecoder, evs []event) [][]contact {
	var out [][]contact
	for _, e := range evs {
		if cs := m.feed(e); cs != nil {
			out = append(out, slices.Clone(cs))
		}
	}
	return out
}

func TestATapIsADownAndAnUp(t *testing.T) {
	var s stream
	s.down(0, 7, 100, 200)
	s.syn(synReport)
	s.up(0)
	s.syn(synReport)
	got := decode(newMTDecoder(5), s.evs)
	want := [][]contact{
		{{slot: 0, id: 7, x: 100, y: 200, phase: phaseDown}},
		{{slot: 0, id: 7, x: 100, y: 200, phase: phaseUp}},
	}
	if !slices.EqualFunc(got, want, slices.Equal) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// A tap so quick its press and lift come in one report still presses.
func TestATapInOneReportPressesAndLifts(t *testing.T) {
	var s stream
	s.down(0, 7, 100, 200)
	s.abs(absMTTrackingID, -1)
	s.syn(synReport)
	got := decode(newMTDecoder(5), s.evs)
	if len(got) != 1 || len(got[0]) != 2 || got[0][0].phase != phaseDown || got[0][1].phase != phaseUp {
		t.Fatalf("got %v", got)
	}
}

func TestFiveFingersMoveTogether(t *testing.T) {
	var s stream
	for i := range int32(5) {
		s.down(i, 10+i, 100*i, 100)
	}
	s.syn(synReport)
	for i := range int32(5) {
		s.abs(absMTSlot, i)
		s.abs(absMTPositionY, 150)
	}
	s.syn(synReport)
	// A report with nothing new reports nothing.
	s.syn(synReport)
	got := decode(newMTDecoder(5), s.evs)
	if len(got) != 2 || len(got[0]) != 5 || len(got[1]) != 5 {
		t.Fatalf("got %v", got)
	}
	for i, c := range got[1] {
		if c.phase != phaseMove || c.slot != i || c.y != 150 || c.x != int32(100*i) {
			t.Fatalf("finger %d: %+v", i, c)
		}
	}
}

// A slot given a new finger while its last lift was lost lifts the old one
// first, so no node waits on it.
func TestANewFingerInABusySlotLiftsTheOld(t *testing.T) {
	var s stream
	s.down(0, 1, 10, 10)
	s.syn(synReport)
	s.down(0, 2, 50, 50)
	s.syn(synReport)
	got := decode(newMTDecoder(5), s.evs)
	if len(got) != 2 || len(got[1]) != 2 || got[1][0] != (contact{slot: 0, id: 1, x: 50, y: 50, phase: phaseUp}) ||
		got[1][1] != (contact{slot: 0, id: 2, x: 50, y: 50, phase: phaseDown}) {
		t.Fatalf("got %v", got)
	}
}

// After SYN_DROPPED the events up to the next report are lost, and the
// slots are read again: a finger gone lifts, one new presses, one still
// down moves.
func TestDroppedEventsAreReadAgain(t *testing.T) {
	var s stream
	s.down(0, 1, 10, 10)
	s.down(1, 2, 20, 20)
	s.syn(synReport)
	s.syn(synDropped)
	s.abs(absMTPositionX, 999) // lost
	s.syn(synReport)
	m := newMTDecoder(3)
	m.resync = func(code uint16) []int32 {
		switch code {
		case absMTTrackingID:
			return []int32{1, -1, 3}
		case absMTPositionX:
			return []int32{15, 0, 30}
		default:
			return []int32{16, 0, 31}
		}
	}
	got := decode(m, s.evs)
	want := []contact{
		{slot: 0, id: 1, x: 15, y: 16, phase: phaseMove},
		{slot: 1, id: 2, x: 20, y: 20, phase: phaseUp},
		{slot: 2, id: 3, x: 30, y: 31, phase: phaseDown},
	}
	if len(got) != 2 || !slices.Equal(got[1], want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// A recording, which cannot be read again after SYN_DROPPED, lifts every
// finger.
func TestDroppedEventsInARecordingLiftEveryFinger(t *testing.T) {
	var s stream
	s.down(0, 1, 10, 10)
	s.syn(synReport)
	s.syn(synDropped)
	s.syn(synReport)
	got := decode(newMTDecoder(2), s.evs)
	if len(got) != 2 || len(got[1]) != 1 || got[1][0].phase != phaseUp {
		t.Fatalf("got %v", got)
	}
}

func TestEventsDecodeAtBothSizes(t *testing.T) {
	e := event{sec: 12, usec: 345, typ: evAbs, code: absMTPositionX, value: -3}
	var b24 [24]byte
	encodeEvent(b24[:], e)
	if got := decodeEvent(b24[:], 24); got != e {
		t.Fatalf("24 bytes: %+v", got)
	}
	var b16 [16]byte
	binary.NativeEndian.PutUint32(b16[:], 12)
	binary.NativeEndian.PutUint32(b16[4:], 345)
	binary.NativeEndian.PutUint16(b16[8:], evAbs)
	binary.NativeEndian.PutUint16(b16[10:], absMTPositionX)
	binary.NativeEndian.PutUint32(b16[12:], uint32(0xfffffffd))
	if got := decodeEvent(b16[:], 16); got != e {
		t.Fatalf("16 bytes: %+v", got)
	}
}

func TestRecordingsReadBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "touch.evdev")
	r, err := newRecorder(path, &inputDev{x: absInfo{maximum: 719}, y: absInfo{maximum: 1279}, slots: 5})
	if err != nil {
		t.Fatal(err)
	}
	var s stream
	s.down(0, 1, 100, 200)
	s.syn(synReport)
	for _, e := range s.evs {
		r.write(e)
	}
	if err := r.close(); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	p, err := readRecording(f)
	if err != nil {
		t.Fatal(err)
	}
	if p.x.maximum != 719 || p.y.maximum != 1279 || p.slots != 5 || !slices.Equal(p.events, s.evs) {
		t.Fatalf("read %+v", p)
	}
	if _, err := readRecording(bytes.NewReader([]byte("not one\n"))); err == nil {
		t.Fatal("read a file that is no recording")
	}
}

// A corner of the panel lands on the right corner of the picture in each
// turn, and turning back finds the point again.
func TestTurnsMapThePanelOntoThePicture(t *testing.T) {
	const w, h = 720, 1280
	panelTopLeft := geom.Pt(0, 0)
	for _, c := range []struct {
		t    turn
		want geom.Point
	}{
		{0, geom.Pt(0, 0)},
		// Turned 90° clockwise the picture's top runs down the panel's
		// right edge, so the panel's top left is the picture's bottom left.
		{90, geom.Pt(0, w)},
		{180, geom.Pt(w, h)},
		{270, geom.Pt(h, 0)},
	} {
		if got := c.t.toPicture(panelTopLeft, w, h); got != c.want {
			t.Errorf("turned %d: panel's top left at %v, want %v", c.t, got, c.want)
		}
		p := geom.Pt(123, 456)
		if back := c.t.toPanel(c.t.toPicture(p, w, h), w, h); back != p {
			t.Errorf("turned %d: %v came back as %v", c.t, p, back)
		}
		pw, ph := c.t.size(w, h)
		if c.t.sideways() != (pw == h && ph == w) {
			t.Errorf("turned %d: picture %dx%d", c.t, pw, ph)
		}
	}
}

// The clip-space matrix turns the picture's top to where toPanel puts it.
func TestTurnMatricesAgreeWithTheTouchMapping(t *testing.T) {
	for _, tr := range []turn{0, 90, 180, 270} {
		m := tr.matrix()
		// The picture's top middle, in clip space (y up), and turned.
		x, y := float32(0), float32(1)
		cx, cy := m[0]*x+m[2]*y, m[1]*x+m[3]*y
		// The same point through toPanel on a 2×2 panel, as clip space.
		pw, _ := tr.size(2, 2)
		p := tr.toPanel(geom.Pt(float32(pw)/2, 0), 2, 2)
		if px, py := p.X-1, 1-p.Y; px != cx || py != cy {
			t.Errorf("turned %d: matrix puts the top at (%v, %v), the touch map at (%v, %v)", tr, cx, cy, px, py)
		}
	}
}

func TestBacklightTakesTheStepAtOrAbove(t *testing.T) {
	dir := t.TempDir()
	was := sysBacklight
	sysBacklight = dir
	t.Cleanup(func() { sysBacklight = was })
	bl := filepath.Join(dir, "panel_backlight@1")
	if err := os.Mkdir(bl, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]string{"max_brightness": "31\n", "brightness": "15\n"} {
		if err := os.WriteFile(filepath.Join(bl, name), []byte(v), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b, err := openBacklight("")
	if err != nil || b == nil {
		t.Fatalf("backlight %v, %v", b, err)
	}
	for _, c := range []struct {
		v    float32
		want int
	}{{0, 0}, {0.01, 1}, {0.5, 16}, {15.0 / 31, 15}, {1, 31}, {2, 31}} {
		if got := b.level(c.v); got != c.want {
			t.Errorf("level(%v) = %d, want %d", c.v, got, c.want)
		}
	}
	if err := b.set(1); err != nil {
		t.Fatal(err)
	}
	read := func() string { v, _ := os.ReadFile(filepath.Join(bl, "brightness")); return string(v) }
	if got := read(); got != "31" {
		t.Fatalf("wrote %q", got)
	}
	if err := b.restore(); err != nil || read() != "15" {
		t.Fatalf("put back %q, %v", read(), err)
	}
	if b, err := openBacklight("none"); b != nil || err == nil {
		t.Fatal("found a backlight that isn't there")
	}
}

func TestPercentiles(t *testing.T) {
	var d []time.Duration
	for i := 1; i <= 100; i++ {
		d = append(d, time.Duration(i)*time.Millisecond)
	}
	p := percentiles(d)
	if p.N != 100 || p.P50 != 51*time.Millisecond || p.P99 != 100*time.Millisecond || p.Max != 100*time.Millisecond {
		t.Fatalf("%+v", p)
	}
	r := newRing(4)
	for i := range 6 {
		r.add(time.Duration(i))
	}
	if got := r.times(); !slices.Equal(got, []time.Duration{4, 5, 2, 3}) {
		t.Fatalf("ring keeps %v", got)
	}
}

// The timeline counts a missed vblank only for a frame handed over
// straight after the flip before, as while something moves.
func TestMissedFlipsCountOnlyWhileMoving(t *testing.T) {
	tl := newTimeline(nil)
	t0 := time.Now()
	ms := func(n float64) time.Time { return t0.Add(time.Duration(n * float64(time.Millisecond))) }
	tl.shown(2, ms(0), ms(1), ms(3), ms(33.3), time.Time{}, true, 1)
	tl.shown(5, ms(33.3), ms(500), ms(502), ms(516), ms(490), false, 0)
	s := tl.stats()
	if s.Frames != 2 || s.Missed != 1 || s.Frame.N != 1 || s.Frame.P50 != 3*time.Millisecond || s.Touch.P50 != 26*time.Millisecond {
		t.Fatalf("%+v", s)
	}
	tl.reset()
	if s := tl.stats(); s.Frames != 0 || s.Frame.N != 0 {
		t.Fatalf("after reset %+v", s)
	}
}

// testDriver is a driver with a screen of the trial Pi's size and no
// screen behind it, for the input path.
func testDriver(tr turn, scale float32) *Driver {
	d := &Driver{turn: tr, panelW: 720, panelH: 1280, density: scale, wake: -1, stop: -1, quit: make(chan struct{})}
	d.picW, d.picH = tr.size(720, 1280)
	return d
}

func nextInput(t *testing.T, w *Window) any {
	t.Helper()
	select {
	case ev := <-w.Input():
		return ev
	case <-time.After(time.Second):
		t.Fatal("no input came")
		return nil
	}
}

// A touch at the panel's top right is the picture's top left when the
// picture is turned 90°, in logical pixels.
func TestATouchReachesTheWindowTurned(t *testing.T) {
	d := testDriver(90, 1.25)
	w, err := d.NewWindow(driver.Options{})
	if err != nil {
		t.Fatal(err)
	}
	win := w.(*Window)
	defer func() { _ = win.Close() }()
	if s := win.Size(); s != geom.Sz(1024, 576) {
		t.Fatalf("window %v, want 1024x576", s)
	}
	m := touchMap{x: absInfo{maximum: 719}, y: absInfo{maximum: 1279}}
	now := time.Now()
	d.touchReport(m, []contact{{slot: 0, id: 1, x: 719, y: 0, phase: phaseDown}}, now)
	mv, ok := nextInput(t, win).(input.PointerMove)
	if !ok || !mv.Touch || mv.Pos.X > 1 || mv.Pos.Y > 1 {
		t.Fatalf("got %+v, want a touch at the top left", mv)
	}
	if dn, ok := nextInput(t, win).(input.PointerDown); !ok || dn.Clicks != 1 || !dn.Touch {
		t.Fatalf("got %+v, want a press", dn)
	}
	// The panel's bottom left is the picture's bottom right.
	d.touchReport(m, []contact{{slot: 0, id: 1, x: 0, y: 1279, phase: phaseMove}}, now)
	mv = nextInput(t, win).(input.PointerMove)
	if math.Abs(float64(mv.Pos.X-1024)) > 1 || math.Abs(float64(mv.Pos.Y-576)) > 1 {
		t.Fatalf("moved to %v, want the bottom right, 1024x576", mv.Pos)
	}
	d.touchReport(m, []contact{{slot: 0, id: 1, x: 0, y: 1279, phase: phaseUp}}, now)
	if _, ok := nextInput(t, win).(input.PointerUp); !ok {
		t.Fatal("no lift")
	}
	if _, ok := nextInput(t, win).(input.PointerLeave); !ok {
		t.Fatal("the finger did not leave")
	}
	if d.touchAt.IsZero() {
		t.Fatal("the touch's time was not kept for the figures")
	}
}

// In a window that takes fingers each finger is a Finger, the first ahead
// of its press, and a second finger moves only as a Finger.
func TestFingersReachAWindowThatTakesThem(t *testing.T) {
	d := testDriver(0, 1)
	w, _ := d.NewWindow(driver.Options{})
	win := w.(*Window)
	defer func() { _ = win.Close() }()
	if err := win.TakeFingers(true); err != nil {
		t.Fatal(err)
	}
	m := touchMap{x: absInfo{maximum: 719}, y: absInfo{maximum: 1279}}
	now := time.Now()
	d.touchReport(m, []contact{{slot: 0, id: 1, x: 10, y: 10, phase: phaseDown}, {slot: 1, id: 2, x: 500, y: 500, phase: phaseDown}}, now)
	want := []string{"finger 0 down", "move", "down", "finger 1 down"}
	var got []string
	for range want {
		switch ev := nextInput(t, win).(type) {
		case input.Finger:
			got = append(got, map[input.FingerPhase]string{input.FingerDown: "finger " + string(rune('0'+ev.ID)) + " down"}[ev.Phase])
		case input.PointerMove:
			got = append(got, "move")
		case input.PointerDown:
			got = append(got, "down")
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	d.touchReport(m, []contact{{slot: 1, id: 2, x: 600, y: 600, phase: phaseMove}}, now)
	if f, ok := nextInput(t, win).(input.Finger); !ok || f.ID != 1 || f.Phase != input.FingerMove {
		t.Fatalf("got %+v, want finger 1 moving alone", f)
	}
}
