package paint

// Blend is how an op's colour meets what is drawn beneath it.
//
// Shapes, masks, images, text and layers take a blend: [RRectOp],
// [MaskOp], [ImageOp], [TextOp] and [LayerOpts]. Cells and scenes have
// none, and draw as [BlendNormal] whatever blend is in force.
type Blend uint8

const (
	// BlendInherit is the zero Blend. An op that asks for it takes the
	// painter's blend in force as it is recorded: [BlendNormal] unless
	// [Painter.Blend] set another. A recorded op holds the blend it
	// took, never this one.
	BlendInherit Blend = iota
	// BlendNormal lays the op over what is beneath, hiding it as far as
	// the op is opaque. An op that asks for it draws so inside a
	// [BlendAdd] scope too.
	BlendNormal
	// BlendAdd adds the op's colour, times its alpha, to what is
	// beneath, and hides nothing, as light does: where two glows
	// overlap they brighten each other, up to white. It is for
	// projectiles, sparks, trails and halos. A colour added to black
	// shows as itself, and an alpha of zero adds nothing.
	//
	// Inside a layer drawn offscreen the light is kept apart from the
	// layer's coverage, and adds to whatever lies beneath the layer when
	// it composites, times the layer's opacity. So a glow inside a layer
	// draws as it would outside, and fades as the layer does. The same
	// holds where nothing opaque lies beneath, as in a transparent
	// window: the light adds to what shows behind it.
	BlendAdd
)

// Blend draws the ops recorded from now on that take the blend in force
// with b, until the returned function is called, which puts back the
// blend in force before:
//
//	defer p.Blend(paint.BlendAdd)()
//
// An op that asks for [BlendNormal] or [BlendAdd] in its own Blend
// draws so whatever the blend in force. Blend with [BlendInherit] keeps
// the blend in force, and its function still puts back the one before.
func (p *Painter) Blend(b Blend) func() {
	p.blends = append(p.blends, p.blend)
	p.blend = p.blendFor(b)
	if p.unblendFn == nil {
		p.unblendFn = p.unblend
	}
	return p.unblendFn
}

func (p *Painter) unblend() {
	n := len(p.blends) - 1
	p.blend = p.blends[n]
	p.blends = p.blends[:n]
}

// blendFor returns the blend an op that asks for b draws with: its own
// where it asks for one, else the one in force, which a zero Painter
// has not set yet and takes as BlendNormal.
func (p *Painter) blendFor(b Blend) Blend {
	if b != BlendInherit {
		return b
	}
	if p.blend == BlendInherit {
		return BlendNormal
	}
	return p.blend
}

// blendNow returns the blend in force, for an op that takes it.
func (p *Painter) blendNow() Blend { return p.blendFor(BlendInherit) }
