//go:build android

package android

/*
#cgo LDFLAGS: -landroid -lEGL -llog
#include <stdlib.h>
#include "glue.h"
*/
import "C"

import (
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/driver/internal/compass"
	"github.com/marrasen/gunim/input"
)

// The functions Java calls, through glue.c, on its UI thread. Each
// hands its news to the driver and returns quickly, as the UI thread
// has a frame of its own to keep.

//export goStart
func goStart(zone *C.uint16_t, n C.int) { theDriver.start(goString(zone, n)) }

//export goSurfaceChanged
func goSurfaceChanged(w *C.ANativeWindow, width, height C.int) {
	theDriver.surfaceChanged(w, int(width), int(height))
}

//export goSurfaceDestroyed
func goSurfaceDestroyed(w *C.ANativeWindow) { theDriver.surfaceDestroyed(w) }

//export goMetrics
func goMetrics(density, rate C.float) { theDriver.metrics(float32(density), float64(rate)) }

//export goTouch
func goTouch(action C.int, x, y C.float, ms C.longlong) {
	theDriver.touch(int(action), float32(x), float32(y), time.Now())
}

//export goPinch
func goPinch(action C.int, x0, y0, x1, y1 C.float) {
	theDriver.pinch(int(action), float32(x0), float32(y0), float32(x1), float32(y1), time.Now())
}

//export goKey
func goKey(down C.uchar, code, meta, ch, repeat C.int) {
	theDriver.key(down != 0, int(code), int(meta), rune(ch), repeat > 0)
}

//export goKeyboard
func goKeyboard(px C.int) { theDriver.keyboardCovers(int(px)) }

//export goInsets
func goInsets(top, right, bottom, left C.int) {
	theDriver.insets(int(top), int(right), int(bottom), int(left))
}

//export goShown
func goShown(shown C.uchar) { theDriver.shown(shown != 0) }

//export goFocus
func goFocus(focused C.uchar) { theDriver.windowFocus(focused != 0) }

//export goEdit
func goEdit(with *C.uint16_t, n, r0, r1, s0, s1, c0, c1 C.int, seq C.longlong) {
	theDriver.edit(input.TextEdit{
		Replace:   [2]int{int(r0), int(r1)},
		With:      goString(with, n),
		Selection: [2]int{int(s0), int(s1)},
		Composing: [2]int{int(c0), int(c1)},
		Seq:       uint64(seq),
		Time:      time.Now(),
	})
}

//export goText
func goText(s *C.uint16_t, n C.int) {
	theDriver.typed(input.TextInput{Text: goString(s, n), Time: time.Now()})
}

//export goComposing
func goComposing(s *C.uint16_t, n, selA, selB C.int) {
	theDriver.typed(input.Composing{Text: goString(s, n), Selected: [2]int{int(selA), int(selB)}})
}

// goString turns n UTF-16 units from Java into a Go string.
func goString(s *C.uint16_t, n C.int) string {
	if n <= 0 {
		return ""
	}
	return string(utf16.Decode(unsafe.Slice((*uint16)(unsafe.Pointer(s)), int(n))))
}

// The calls into Java, from any thread.

func showKeyboard(show bool) {
	v := 0
	if show {
		v = 1
	}
	C.gunim_show_keyboard(C.int(v))
}

// sendCaret tells Java where the text caret is on the surface, in
// device pixels.
func sendCaret(x0, y0, x1, y1 int) {
	C.gunim_caret(C.int(x0), C.int(y0), C.int(x1), C.int(y1))
}

// sendTextState hands Java the focused text: the text in UTF-16, and
// where the selection and the composition fall in it, in UTF-16 units.
func sendTextState(s *input.TextState, seq uint64) {
	if s == nil {
		C.gunim_clear_text_state()
		return
	}
	u := utf16.Encode([]rune(s.Text))
	at := func(b int) C.int { return C.int(utf16Len(s.Text[:max(0, min(b-s.Start, len(s.Text)))])) }
	var p *C.uint16_t
	if len(u) > 0 {
		p = (*C.uint16_t)(unsafe.Pointer(&u[0]))
	}
	C.gunim_text_state(p, C.int(len(u)), C.int(s.Start),
		at(s.Selection[0]), at(s.Selection[1]), at(s.Composing[0]), at(s.Composing[1]),
		cBool(s.Multiline), cBool(s.Secret), C.longlong(seq))
}

func cBool(b bool) C.int {
	if b {
		return 1
	}
	return 0
}

// utf16Len returns how many UTF-16 units s takes.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

func clipboard() string {
	var n C.int
	p := C.gunim_get_clipboard(&n)
	if p == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(p))
	return goString(p, n)
}

func setClipboard(s string) {
	u := utf16.Encode([]rune(s))
	var p *C.uint16_t
	if len(u) > 0 {
		p = (*C.uint16_t)(unsafe.Pointer(&u[0]))
	}
	C.gunim_set_clipboard(p, C.int(len(u)))
}

func finish() { C.gunim_finish() }

// utf16Of returns s as UTF-16, and a pointer to it for C, nil when empty.
func utf16Of(s string) ([]uint16, *C.uint16_t) {
	u := utf16.Encode([]rune(s))
	if len(u) == 0 {
		return u, nil
	}
	return u, (*C.uint16_t)(unsafe.Pointer(&u[0]))
}

// nowPlaying hands what plays to the media session, or takes it away
// for nil.
func nowPlaying(np *driver.NowPlaying) {
	if np == nil {
		C.gunim_now_playing(0, 0, nil, 0, nil, 0, nil, 0, 0, 0, nil, 0)
		return
	}
	t, tp := utf16Of(np.Title)
	r, rp := utf16Of(np.Artist)
	l, lp := utf16Of(np.Album)
	var art unsafe.Pointer
	if len(np.Cover) > 0 {
		art = unsafe.Pointer(&np.Cover[0])
	}
	playing := 0
	if np.Playing {
		playing = 1
	}
	C.gunim_now_playing(1, C.int(playing), tp, C.int(len(t)), rp, C.int(len(r)), lp, C.int(len(l)),
		C.longlong(np.Length.Milliseconds()), C.longlong(np.Position.Milliseconds()), art, C.int(len(np.Cover)))
	runtime.KeepAlive(t)
	runtime.KeepAlive(r)
	runtime.KeepAlive(l)
	runtime.KeepAlive(np.Cover)
}

//export goMedia
func goMedia(action C.int, ms C.longlong) {
	theDriver.media(int(action), time.Duration(ms)*time.Millisecond)
}

// buzz gives the short buzz a long press gives.
func buzz() { C.gunim_buzz() }

// share hands s to the share sheet: the paths go to Java as one
// string, each ended by a NUL, which no path holds.
func share(s driver.Share) error {
	var paths strings.Builder
	for _, p := range s.Paths {
		paths.WriteString(p)
		paths.WriteByte(0)
	}
	t, tp := utf16Of(s.Text)
	j, jp := utf16Of(s.Subject)
	f, fp := utf16Of(paths.String())
	ok := C.gunim_share(tp, C.int(len(t)), jp, C.int(len(j)), fp, C.int(len(f)))
	runtime.KeepAlive(t)
	runtime.KeepAlive(j)
	runtime.KeepAlive(f)
	if ok == 0 {
		return driver.ErrNoSharer
	}
	return nil
}

// openLink opens the web page at url in the phone's browser.
func openLink(url string) error {
	u, up := utf16Of(url)
	ok := C.gunim_open_link(up, C.int(len(u)))
	runtime.KeepAlive(u)
	if ok == 0 {
		return driver.ErrNoLinkOpener
	}
	return nil
}

// vibrate runs the motor in pattern, on and off in turn, from on.
func vibrate(pattern []time.Duration) error {
	ms := make([]C.longlong, len(pattern))
	for i, d := range pattern {
		ms[i] = C.longlong(max(0, d.Milliseconds()))
	}
	var p *C.longlong
	if len(ms) > 0 {
		p = &ms[0]
	}
	ok := C.gunim_vibrate(p, C.int(len(ms)))
	runtime.KeepAlive(ms)
	if ok == 0 {
		return driver.ErrNoVibrator
	}
	return nil
}

// hasCompass reports whether the phone has the sensors that tell which
// way it faces.
func hasCompass() bool { return C.gunim_has_compass() != 0 }

// watchHeading starts or stops those sensors.
func watchHeading(on bool) { C.gunim_watch_heading(cBool(on)) }

//export goHeading
func goHeading(r *C.float, screen, accuracy C.int, err C.float) {
	var m [9]float32
	copy(m[:], unsafe.Slice((*float32)(unsafe.Pointer(r)), 9))
	theDriver.heading(m, compass.Rotation(screen), int(accuracy), float32(err))
}

//export goAnswered
func goAnswered(code, granted C.int) { answered(int(code), granted != 0) }

// The prompts for permissions waiting on the user's answer, by their
// request code.
var (
	asking  sync.Mutex
	askCode int
	answers = map[int]chan bool{}
)

// permitted reports whether permission p is granted.
func permitted(p driver.Permission) bool { return C.gunim_permitted(C.int(p)) != 0 }

// ask asks the user for permission p, and waits for their answer.
func ask(p driver.Permission) bool {
	asking.Lock()
	askCode++
	code := askCode
	ch := make(chan bool, 1)
	answers[code] = ch
	asking.Unlock()
	C.gunim_ask(C.int(p), C.int(code))
	return <-ch
}

// answered hands the user's answer to the prompt of code to its ask.
func answered(code int, granted bool) {
	asking.Lock()
	ch := answers[code]
	delete(answers, code)
	asking.Unlock()
	if ch != nil {
		ch <- granted
	}
}

// userFolder returns the shared folder of kind f, or "".
func userFolder(f driver.UserFolder) string {
	var n C.int
	p := C.gunim_user_folder(C.int(f), &n)
	if p == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(p))
	return goString(p, n)
}

//export goChosen
func goChosen(code C.int, path *C.uint16_t, n C.int) {
	var p string
	if path != nil {
		p = goString(path, n)
	}
	chose(int(code), p)
}

// The choosers waiting on the user, by their request code, which
// shares its count with the prompts' and keeps clear of them.
var choosing = map[int]chan string{}

// chooseFolder shows the system's chooser of folders, and waits for
// the folder chosen, "" for none.
func chooseFolder() string {
	asking.Lock()
	askCode++
	code := askCode
	ch := make(chan string, 1)
	choosing[code] = ch
	asking.Unlock()
	C.gunim_choose_folder(C.int(code))
	return <-ch
}

// chose hands the folder chosen to the chooser of code.
func chose(code int, path string) {
	asking.Lock()
	ch := choosing[code]
	delete(choosing, code)
	asking.Unlock()
	if ch != nil {
		ch <- path
	}
}
