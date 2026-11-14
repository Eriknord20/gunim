package dynamics

import (
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// wiredHost is a host that knows the wires into the plugin, so the bolt is right while the plugin sleeps.
type wiredHost interface {
	// ControlInWired reports whether a wire that carries reaches the plugin's control input of port key.
	ControlInWired(key string) bool
}

// The bolt's look: its box, how far its ends reach from its middle, how wide its line is, how bright it stands
// between hits, and how much a hit grows it.
const (
	keyBoltBox  = 20
	keyBoltR    = 5.5
	keyBoltLine = 2.2
	keyBoltRest = 0.55
	keyBoltGrow = 0.25
)

// The bolt's springs: a hit lights it fast, and it falls back slower.
var (
	keyBoltUp   = anim.Spring{Response: 0.06, Damping: 1}
	keyBoltDown = anim.Spring{Response: 0.3, Damping: 1}
)

// keyBoltTip is the bolt's tooltip.
const keyBoltTip = "Hits drive the gate and the compressor: Key is wired, so they follow the hits, not the sound"

// keyBolt is the small bolt the panel shows by the sidechain's name while Key is wired. It fades in and out on a
// spring, and each hit lights it up for a moment.
type keyBolt struct {
	h    plug.Host
	on   bool
	hits float64
	// in is how far the bolt has faded in, and flash how lit a hit has it; rising says flash is on its way up.
	in, flash *anim.Float
	rising    bool
	group     anim.Group
	hint      widget.PartTip
}

// newKeyBolt returns the bolt for h's plugin. It starts where Key stands.
func newKeyBolt(h plug.Host) *keyBolt {
	b := &keyBolt{h: h, hits: h.Param(KeyHitsID), in: anim.NewFloat(0), flash: anim.NewFloat(0)}
	if b.on = b.wired(); b.on {
		b.in.Jump(1)
	}
	b.group.Add(b.in, b.flash)
	return b
}

// wired asks the host whether Key is wired, or the plugin playing where the host cannot tell.
func (b *keyBolt) wired() bool {
	if wh, ok := b.h.(wiredHost); ok {
		return wh.ControlInWired(Descriptor.ControlIn[KeyIn].PortKey())
	}
	return b.h.Param(KeyWiredID) > 0.5
}

// follow starts the bolt toward where Key stands and lights it for a new hit. It reports whether anything changed.
func (b *keyBolt) follow() bool {
	changed := false
	if on := b.wired(); on != b.on {
		b.on, changed = on, true
		b.in.Animate(anim.OnOff(on), anim.Snappy)
	}
	if hits := b.h.Param(KeyHitsID); hits != b.hits {
		b.hits = hits
		if b.on {
			b.rising, changed = true, true
			b.flash.Retarget(1, keyBoltUp)
		}
	}
	return changed
}

// Step implements [gunim.Animator]: true only while the bolt fades or a hit's light moves.
func (b *keyBolt) Step(dt time.Duration) bool {
	b.follow()
	if b.rising && b.flash.Value() >= 0.9 {
		b.rising = false
		b.flash.Retarget(0, keyBoltDown)
	}
	return b.group.Step(dt)
}

// Layout implements [gunim.Node].
func (b *keyBolt) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	return c.Constrain(geom.Sz(keyBoltBox, keyBoltBox))
}

// Paint implements [gunim.Node]: the bolt as one ribbon.
func (b *keyBolt) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, _ gunim.Children) {
	in := min(max(b.in.Value(), 0), 1)
	if in <= 0.01 {
		return
	}
	lit := min(max(b.flash.Value(), 0), 1)
	c := paint.Faded(audioui.Sound.Get(f.Theme), in*(keyBoltRest+(1-keyBoltRest)*lit))
	k := 1 + keyBoltGrow*lit
	mid, r, w := geom.Pt(box.W/2, box.H/2), float32(keyBoltR)*k, float32(keyBoltLine)*k
	p.Ribbon([]paint.RibbonPoint{
		{At: mid.Add(geom.Pt(0.27*r, -r)), Width: w, Color: c},
		{At: mid.Add(geom.Pt(-0.45*r, 0.09*r)), Width: w, Color: c},
		{At: mid.Add(geom.Pt(0.18*r, 0.09*r)), Width: w, Color: c},
		{At: mid.Add(geom.Pt(-0.27*r, r)), Width: w, Color: c},
	})
}

// Handle implements [gunim.Handler]: the bolt's tooltip, while it shows.
func (b *keyBolt) Handle(e input.Event, u *gunim.UI) bool {
	words := ""
	if b.on {
		words = keyBoltTip
	}
	b.hint.Handle(e, u, b, words)
	return false
}

// Access implements [gunim.Accessible].
func (b *keyBolt) Access() access.Info {
	info := access.Info{Role: access.RoleImage, Name: "Key", Value: "Not wired"}
	if b.on {
		info.Value, info.Description = "Wired", keyBoltTip
	}
	return info
}
