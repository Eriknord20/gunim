package plugtest_test

import (
	"math"
	"math/rand/v2"
	"testing"

	"github.com/marrasen/gunim/audio/plug"
	_ "github.com/marrasen/gunim/audio/plug/braid"
	_ "github.com/marrasen/gunim/audio/plug/dynamics"
	_ "github.com/marrasen/gunim/audio/plug/enhancer"
	_ "github.com/marrasen/gunim/audio/plug/eq"
	_ "github.com/marrasen/gunim/audio/plug/imager"
	_ "github.com/marrasen/gunim/audio/plug/impact"
	_ "github.com/marrasen/gunim/audio/plug/maximizer"
	"github.com/marrasen/gunim/audio/plug/plugtest"
	_ "github.com/marrasen/gunim/audio/plug/stabilizer"
	_ "github.com/marrasen/gunim/audio/plug/vocal"
)

// monoHost is a host that answers each parameter with its default.
// A plugin made for a test calls nothing else.
type monoHost struct {
	plug.Host
	d *plug.Descriptor
}

func (h monoHost) Param(id uint32) float64 {
	p, _ := h.d.Param(id)
	return p.Default
}

// TestAPluginThatKeepsMonoGivesTheSameOnBothSides runs every gunim
// plugin that says it keeps mono on the same sound in both channels,
// at its defaults and at settings picked at random, and checks the two
// channels come out the same, within -120 dBFS, below a 24-bit
// sample's last bit: a float's rounding may differ. It checks the plugins that say they don't
// are the ones that spread a sound: Braid pans, the imager stereoizes
// and the vocal plugin widens.
func TestAPluginThatKeepsMonoGivesTheSameOnBothSides(t *testing.T) {
	stereo := map[string]bool{"marrasen.braid": true, "marrasen.imager": true, "marrasen.vocal": true}
	const frames = 48000 / 2
	in := make([]float32, frames)
	r := rand.New(rand.NewPCG(1, 2))
	for i := range in {
		in[i] = float32(0.5*math.Sin(2*math.Pi*110*float64(i)/48000) + 0.3*(r.Float64()-0.5))
		if i/4800%2 == 1 {
			in[i] *= 0.1
		}
	}
	seen := 0
	for _, d := range plug.All() {
		if d.Instrument() || d.Generator() {
			continue
		}
		if want := !stereo[d.ID]; d.KeepsMono != want {
			t.Errorf("%s says KeepsMono %v, want %v", d.ID, d.KeepsMono, want)
			continue
		}
		if !d.KeepsMono {
			continue
		}
		seen++
		for run := range 4 {
			var changes []plug.Change
			if run > 0 {
				pr := rand.New(rand.NewPCG(uint64(run), 7))
				for _, p := range d.Params {
					if p.Flags&(plug.ReadOnly|plug.Bypass) != 0 {
						continue
					}
					v := p.Min + pr.Float64()*(p.Max-p.Min)
					if p.Steps > 0 {
						v = p.Min + math.Round((v-p.Min)/(p.Max-p.Min)*float64(p.Steps))*(p.Max-p.Min)/float64(p.Steps)
					}
					changes = append(changes, plug.Change{ID: p.ID, Value: v})
				}
			}
			pl := d.New(monoHost{d: &d})
			res := plugtest.Drive(t, &d, pl.Processor(), plug.Setup{Rate: 48000, MaxBlock: 512, Channels: 2, Offline: true},
				frames, plugtest.Ports{In: [][]float32{in, in}, Changes: changes})
			pl.Close()
			l, rr := res.Out[0], res.Out[1]
			for i := range l {
				if math.Abs(float64(l[i]-rr[i])) > 1e-6 {
					t.Fatalf("%s, run %d: frame %d is %v on the left and %v on the right", d.ID, run, i, l[i], rr[i])
				}
			}
		}
	}
	if seen < 6 {
		t.Fatalf("checked %d plugins that keep mono, want at least 6", seen)
	}
}
