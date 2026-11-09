//go:build linux && !android && (amd64 || arm64)

package alsa

import (
	"testing"
	"unsafe"
)

func TestTheKernelsStructuresAreTheirSize(t *testing.T) {
	if n := unsafe.Sizeof(hwParams{}); n != 608 {
		t.Errorf("hwParams is %d bytes", n)
	}
	if n := unsafe.Sizeof(swParams{}); n != 136 {
		t.Errorf("swParams is %d bytes", n)
	}
	if n := unsafe.Sizeof(xferi{}); n != 24 {
		t.Errorf("xferi is %d bytes", n)
	}
	if n := unsafe.Offsetof(hwParams{}.intervals); n != 260 {
		t.Errorf("hwParams's intervals are at %d", n)
	}
	if n := unsafe.Offsetof(hwParams{}.fifoSize); n != 536 {
		t.Errorf("hwParams's fifo size is at %d", n)
	}
}

func TestParseList(t *testing.T) {
	cards := ` 0 [PCH            ]: HDA-Intel - HDA Intel PCH
                      HDA Intel PCH at 0x604b118000 irq 181
 1 [M4             ]: USB-Audio - M4
                      MOTU M4 at usb-0000:00:14.0-6, high speed
`
	pcm := `00-00: ALC3271 Analog : ALC3271 Analog : playback 1 : capture 1
00-03: HDMI 0 : HDMI 0 : playback 1
01-00: USB Audio : USB Audio : playback 1 : capture 1
`
	got := parseList(cards, pcm)
	want := []PCMInfo{
		{0, 0, "HDA Intel PCH", "ALC3271 Analog", true, true},
		{0, 3, "HDA Intel PCH", "HDMI 0", true, false},
		{1, 0, "M4", "USB Audio", true, true},
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseStream(t *testing.T) {
	p, c := parseStream(`MOTU M4 at usb-0000:00:14.0-6, high speed : USB Audio

Playback:
  Status: Stop
  Interface 1
    Altset 1
    Channels: 4
    Rates: 44100, 48000

Capture:
  Status: Stop
  Interface 2
    Altset 1
    Channels: 6
`)
	if p != 4 || c != 6 {
		t.Errorf("got %d, %d", p, c)
	}
	if p, c := parseStream(""); p != 0 || c != 0 {
		t.Errorf("of nothing got %d, %d", p, c)
	}
}
