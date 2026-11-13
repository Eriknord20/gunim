//go:build linux && (arm64 || amd64)

package kms

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/marrasen/gunim/driver"
)

// The bench's touch script, played through the driver, leaves no finger
// down, so the screen can blank after it.
func TestAScriptPlayedLeavesNoFingerDown(t *testing.T) {
	d := testDriver(90, 1.75)
	w, _ := d.NewWindow(driver.Options{})
	_ = w.(*Window).TakeFingers(true)
	p, err := readRecording(bytes.NewReader(benchScript(15 * time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	m := touchMap{x: p.x, y: p.y}
	dec := newMTDecoder(p.slots)
	now := time.Now()
	for _, e := range p.events {
		if cs := dec.feed(e); cs != nil {
			d.touchReport(m, cs, now)
			now = now.Add(time.Millisecond)
		}
	}
	if d.blank.fingers || d.input.contacts.Count() != 0 || len(d.input.swallow) != 0 {
		t.Fatalf("after the script: a finger down %v, %d down, %d swallowed", d.blank.fingers, d.input.contacts.Count(), len(d.input.swallow))
	}
}

// benchScript is internal/kmsdemo's Script, which this package cannot
// import: one finger drawing circles, taps, and two fingers dragging.
func benchScript(d time.Duration) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "gunim-evdev 1 0 719 0 1279 5\n")
	var ev [24]byte
	at := time.Duration(0)
	put := func(typ, code uint16, v int32) {
		binary.NativeEndian.PutUint64(ev[:], uint64(at/time.Second))
		binary.NativeEndian.PutUint64(ev[8:], uint64(at%time.Second/time.Microsecond))
		binary.NativeEndian.PutUint16(ev[16:], typ)
		binary.NativeEndian.PutUint16(ev[18:], code)
		binary.NativeEndian.PutUint32(ev[20:], uint32(v))
		b.Write(ev[:])
	}
	const (
		evSyn, evAbs                    = 0, 3
		slot, posX, posY, track         = 0x2f, 0x35, 0x36, 0x39
		step                            = 17 * time.Millisecond
		circle                          = 2 * time.Second
		tapHold, tapGap                 = 80 * time.Millisecond, 400 * time.Millisecond
		cx, cy, cr              float64 = 360, 640, 220
	)
	id := int32(1)
	report := func() { put(evSyn, 0, 0); at += step }
	finger := func(s, x, y int32, down bool) {
		put(evAbs, slot, s)
		if down {
			put(evAbs, track, id)
			id++
		}
		put(evAbs, posX, x)
		put(evAbs, posY, y)
	}
	lift := func(s int32) {
		put(evAbs, slot, s)
		put(evAbs, track, -1)
	}
	for at < d {
		// A circle with one finger.
		start := at
		for first := true; at-start < circle && at < d; first = false {
			a := 2 * math.Pi * float64(at-start) / float64(circle)
			finger(0, int32(cx+cr*math.Cos(a)), int32(cy+cr*math.Sin(a)), first)
			report()
		}
		lift(0)
		report()
		// Three taps.
		for range 3 {
			at += tapGap
			finger(0, 200, 300, true)
			report()
			at += tapHold
			lift(0)
			report()
		}
		// Two fingers dragging down together.
		at += tapGap
		for k := int32(0); k < 60 && at < d; k++ {
			finger(0, 250, 300+10*k, k == 0)
			finger(1, 470, 300+10*k, k == 0)
			report()
		}
		lift(0)
		lift(1)
		report()
		at += tapGap
	}
	return b.Bytes()
}
