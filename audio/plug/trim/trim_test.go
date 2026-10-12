package trim

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/plug"
)

// host is a host for tests, which notes the gestures it is sent.
type host struct {
	values map[uint32]float64
	log    []string
	// out is the sound the views hear come out, or nil.
	out plug.Sound
}

func (h *host) Param(id uint32) float64 { return h.values[id] }
func (h *host) Begin(uint32, *gunim.UI) { h.log = append(h.log, "begin") }
func (h *host) Set(id uint32, v float64, _ *gunim.UI) {
	h.values[id] = v
	h.log = append(h.log, "set")
}
func (h *host) End(uint32, *gunim.UI)                { h.log = append(h.log, "end") }
func (h *host) BeginEdit(name string, _ *gunim.UI)   { h.log = append(h.log, "edit "+name) }
func (h *host) Changed(*gunim.UI)                    { h.log = append(h.log, "changed") }
func (h *host) EndEdit(*gunim.UI)                    { h.log = append(h.log, "end edit") }
func (h *host) Sound() (in, out plug.Sound)          { return nil, h.out }
func (h *host) Traced(uint32, int64) (float32, bool) { return 0, false }

func newPlugin(t *testing.T) (plug.Plugin, *host) {
	t.Helper()
	d, ok := plug.Lookup("marrasen.trim")
	if !ok {
		t.Fatal("the trim did not register itself")
	}
	h := &host{values: map[uint32]float64{GainID: 0}}
	return d.New(h), h
}

// ones returns n frames of a channel at full scale.
func ones(n int) []float32 {
	s := make([]float32, n)
	for i := range s {
		s[i] = 1
	}
	return s
}

func TestTheFirstGainJumpsAndLaterOnesGlide(t *testing.T) {
	p, _ := newPlugin(t)
	pr := p.Processor()
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 512, Channels: 2})
	ch := [][]float32{ones(512), ones(512)}
	pr.Process(&plug.Block{In: ch, Out: ch, Frames: 512, Changes: []plug.Change{{ID: GainID, Value: -6}}})
	want := float32(math.Pow(10, -6.0/20))
	if math.Abs(float64(ch[0][0]-want)) > 1e-6 || math.Abs(float64(ch[1][511]-want)) > 1e-6 {
		t.Fatalf("at -6 dB from the start the block runs %v to %v, want %v throughout", ch[0][0], ch[1][511], want)
	}

	// A move up at frame 100 glides: no step at once, there by the end.
	ch = [][]float32{ones(4800), ones(4800)}
	pr.Process(&plug.Block{In: ch, Out: ch, Frames: 4800, Changes: []plug.Change{{Frame: 100, ID: GainID, Value: 0}}})
	if ch[0][99] != want {
		t.Errorf("before the change the gain is %v, want %v", ch[0][99], want)
	}
	if ch[0][100] >= 0.6 {
		t.Errorf("the gain stepped to %v at the change", ch[0][100])
	}
	if math.Abs(float64(ch[0][4799]-1)) > 1e-4 {
		t.Errorf("100 ms later the gain is %v, want 1", ch[0][4799])
	}
}

func TestAGainOutOfRangeIsClamped(t *testing.T) {
	p, _ := newPlugin(t)
	pr := p.Processor()
	pr.Prepare(plug.Setup{Rate: 44100, MaxBlock: 64, Channels: 1})
	in, out := [][]float32{ones(64)}, [][]float32{make([]float32, 64)}
	pr.Process(&plug.Block{In: in, Out: out, Frames: 64, Changes: []plug.Change{{ID: GainID, Value: 100}}})
	if want := float32(math.Pow(10, 24.0/20)); math.Abs(float64(out[0][63]-want)) > 1e-4 {
		t.Errorf("at +100 dB the gain is %v, want +24 dB's %v", out[0][63], want)
	}
	if in[0][0] != 1 {
		t.Error("processing apart from In wrote to In")
	}
}

func TestTheTrimKeepsNoState(t *testing.T) {
	p, _ := newPlugin(t)
	if p.State() != nil || p.SetState(nil) != nil {
		t.Error("a trim has state")
	}
	if p.SetState([]byte("x")) == nil {
		t.Error("a trim took state it cannot have")
	}
}

func TestTheViewsWorkThroughTheHost(t *testing.T) {
	p, h := newPlugin(t)
	h.values[GainID] = -3
	v, ok := p.(plug.Viewer)
	if !ok {
		t.Fatal("the trim has no views")
	}
	if v.View(plug.Full, h) != nil {
		t.Error("the trim has a full view of its own")
	}
	c, ok := v.View(plug.Chip, h).(*chip)
	if !ok || c.label.Text != "-3.0 dB" {
		t.Fatalf("the chip shows %q, want -3.0 dB", c.label.Text)
	}
	c.Follow(GainID, 1.5, nil)
	if c.label.Text != "+1.5 dB" {
		t.Errorf("the chip follows to %q, want +1.5 dB", c.label.Text)
	}

	pn, ok := v.View(plug.Panel, h).(*panel)
	if !ok || pn.row.Slider.Value() != -3 {
		t.Fatal("the panel's slider does not start at the host's gain")
	}
	// A drag of two steps and a let-go is one gesture.
	var u *gunim.UI
	pn.row.Slider.OnChange(-2, u)
	pn.row.Slider.OnChange(-1, u)
	pn.row.Slider.OnCommit(-1, u)
	// A key's step commits alone, as a gesture of its own.
	pn.row.Slider.OnCommit(-0.9, u)
	want := []string{"begin", "set", "set", "set", "end", "begin", "set", "end"}
	if !slices.Equal(h.log, want) {
		t.Errorf("the host heard %v, want %v", h.log, want)
	}
	if math.Abs(h.values[GainID]+0.9) > 1e-6 {
		t.Errorf("the host's gain is %v, want -0.9", h.values[GainID])
	}
	pn.Follow(GainID, 6, nil)
	if pn.row.Slider.Value() != 6 {
		t.Errorf("the slider follows to %v, want 6", pn.row.Slider.Value())
	}
}

// steady is a sound at one level, heard to frame at.
type steady struct {
	level float32
	at    int64
}

func (s *steady) Heard(dst []float32) (frames int, at int64, rate int) {
	for i := range dst {
		dst[i] = s.level
	}
	return len(dst) / 2, s.at, 48000
}

func TestTheChipMetersTheSoundAndRestsWhenItStops(t *testing.T) {
	p, h := newPlugin(t)
	s := &steady{level: 0.5}
	h.out = s
	v, ok := p.(plug.Viewer)
	if !ok {
		t.Fatal("the trim has no views")
	}
	c, ok := v.View(plug.Chip, h).(*chip)
	if !ok {
		t.Fatal("the chip is of another type")
	}
	frame := time.Second / 60
	for range 60 {
		s.at += 800
		if !c.Step(frame) {
			t.Fatal("the chip rests while the sound plays")
		}
	}
	if rms := c.out.levels.RMS[0]; math.Abs(float64(rms)+6.02) > 0.5 {
		t.Fatalf("a steady half of full scale meters %v dB, want -6", rms)
	}
	// The sound stops: the levels fall, and the chip comes to rest.
	rested := false
	for range 60 * 20 {
		if !c.Step(frame) {
			rested = true
			break
		}
	}
	if !rested {
		t.Fatal("the chip never rests once the sound stops")
	}
}

func TestTheChipsBarsHoldBetweenTheChunksTheSoundComesIn(t *testing.T) {
	p, h := newPlugin(t)
	s := &steady{level: 0.5}
	h.out = s
	v, ok := p.(plug.Viewer)
	if !ok {
		t.Fatal("the trim has no views")
	}
	c, ok := v.View(plug.Chip, h).(*chip)
	if !ok {
		t.Fatal("the chip is of another type")
	}
	// At 144 frames a second, a chunk of 1024 frames every third: the
	// frames between hear nothing new.
	frame := time.Second / 144
	low, high := float32(0), float32(-200)
	for i := range 144 * 2 {
		if i%3 == 0 {
			s.at += 1024
		}
		c.Step(frame)
		if i >= 144 {
			low, high = min(low, c.out.levels.RMS[0]), max(high, c.out.levels.RMS[0])
		}
	}
	// A steady half of full scale: -6 dB, held.
	if high-low > 0.2 || math.Abs(float64(low+high)/2+6.02) > 0.3 {
		t.Errorf("between chunks the level swings %.1f dB, from %.1f to %.1f; want -6 held", high-low, low, high)
	}
}
