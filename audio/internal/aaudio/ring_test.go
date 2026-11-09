package aaudio

import (
	"testing"
	"unsafe"
)

// TestTheLayoutsMatchC checks the structs C reads as aaudio_android.h
// lays them out: every 64-bit field first, then the pointers.
func TestTheLayoutsMatchC(t *testing.T) {
	ptr := unsafe.Sizeof(unsafe.Pointer(nil))
	if got := unsafe.Sizeof(ringHeader{}); got != 4*8+8 {
		t.Errorf("a ring's header is %d bytes, gunim_ring %d", got, 4*8+8)
	}
	if got := unsafe.Offsetof(ringHeader{}.buf); got != 4*8 {
		t.Errorf("a ring's buffer is at %d, in gunim_ring at %d", got, 4*8)
	}
	var sh Shared
	if got := unsafe.Sizeof(sh); got != 6*8+2*ptr {
		t.Errorf("Shared is %d bytes, gunim_shared %d", got, 6*8+2*ptr)
	}
	if got := unsafe.Offsetof(sh.data); got != 6*8 {
		t.Errorf("Shared's rings are at %d, gunim_shared's at %d", got, 6*8)
	}
	if got := unsafe.Offsetof(sh.Err); got != 5*8 {
		t.Errorf("Shared's Err is at %d, gunim_shared's err at %d", got, 5*8)
	}
}

// TestARingKeepsBlocksAcrossItsEnd writes blocks of frames of two
// values across the ring's end, and reads each back whole, in order,
// as the input's blocks ring holds each block's first frame and length.
func TestARingKeepsBlocksAcrossItsEnd(t *testing.T) {
	q := NewRing[int64](5, 2)
	defer q.Free()
	for i := range int64(40) {
		if n := q.Push([]int64{i, 10 * i, i + 1, 10 * (i + 1)}); n != 2 {
			t.Fatalf("pushed %d frames, want 2", n)
		}
		var b [2]int64
		for want := i; want <= i+1; want++ {
			if q.Pop(b[:]) != 1 || b != [2]int64{want, 10 * want} {
				t.Fatalf("read %v, want %v", b, [2]int64{want, 10 * want})
			}
		}
	}
	if q.Push(make([]int64, 2*7)) != 5 || q.Room() != 0 {
		t.Error("a ring of 5 took more than 5 frames")
	}
	q.Drop()
	if q.Len() != 0 || q.Room() != 5 {
		t.Errorf("after Drop %d frames wait", q.Len())
	}
}
