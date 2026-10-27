package gunim

import (
	"log"
	"os"

	"github.com/marrasen/gunim/paint"
)

// steadyDebug is set by GUNIM_DEBUG_STEADY=1, which paints every Steady
// node's subtree anew, laid out as carried, and logs each whose key
// said it would draw the same, and did not.
var steadyDebug = os.Getenv("GUNIM_DEBUG_STEADY") == "1"

// carries reports whether the frame carries Steady node s's subtree as
// it was laid out, k its node: within cs, with the key k gives in f. It
// marks the subtree laid out where it does.
func (u *UI) carries(s *state, k Steady, cs Constraints, f Frame) bool {
	kp := s.keep
	if kp == nil {
		kp = &keeping{}
		s.keep = kp
	}
	key, ok := k.SteadyKey(f)
	if !ok {
		key = nil
	}
	carry := ok && u.calm && kp.ok && kp.laid+1 == u.seq && kp.drawn+1 == u.seq && kp.cs == cs &&
		sameKey(kp.key, key) && still(s, u.seq) && !themeMoving(s, u.seq)
	kp.key, kp.cs, kp.laid, kp.carry = key, cs, u.seq, carry
	if carry {
		stampLaid(s, u.seq)
	}
	return carry
}

// sameKey reports whether keys a and b are equal: never where either is
// of a type that cannot be compared, as a slice.
func sameKey(a, b any) (same bool) {
	defer func() {
		if recover() != nil {
			same = false
		}
	}()
	return a == b
}

// moving reports whether s moved, by its Step or its WakeIn, in frame
// seq or at the end of the one before.
func (s *state) moving(seq uint64) bool { return s.moved+1 >= seq }

// still reports whether no node in s's subtree is moving, and all are
// present.
func still(s *state, seq uint64) bool {
	if s.presence != Present || s.moving(seq) {
		return false
	}
	for _, k := range s.kids {
		if !still(k, seq) {
			return false
		}
	}
	return true
}

// themeMoving reports whether the theme s paints in is moving: a
// [ThemeScope] above it, or above the node that opened its popup,
// moving to another theme.
func themeMoving(s *state, seq uint64) bool {
	for a := s.up(); a != nil; a = a.up() {
		if _, ok := a.node.(ThemeScope); ok && a.moving(seq) {
			return true
		}
	}
	return false
}

// stampLaid marks laid out, in frame seq, each node below s that the
// frame before laid out, as Layout would have; below a carried node,
// what each was laid out as stands.
func stampLaid(s *state, seq uint64) {
	for _, k := range s.kids {
		if k.laid+1 == seq {
			k.laid = seq
			stampLaid(k, seq)
		}
	}
}

// stampDrawn marks drawn, in frame seq, each node below s that the frame
// before drew, as Paint would have. A node laid out but not drawn stays
// undrawn, for input and the rest to pass by.
func stampDrawn(s *state, seq uint64) {
	for _, k := range s.kids {
		if k.drawn+1 == seq {
			k.drawn = seq
			stampDrawn(k, seq)
		}
	}
}

// paintKept paints c, a Steady node, kp what is kept of it: the run it
// painted the frame before, where the frame carries it and it paints in
// the same place, inside the same clips, under the same blend; else as
// usual, keeping the run for the next frame.
func (u *UI) paintKept(c Child, kp *keeping, p *paint.Painter, f Frame) {
	s := c.n
	same := kp.carry && kp.laid == u.seq && s.toWindow == kp.toWindow && s.proj == nil && s.clip.Same(kp.clip) &&
		p.BlendInForce() == kp.blend
	if same && !steadyDebug {
		mark := p.Mark()
		if p.Again(kp.run) {
			kp.run, kp.drawn = p.RunFrom(mark), u.seq
			stampDrawn(s, u.seq)
			if u.chrome != nil {
				noteTitleBars(u, s, u.seq)
			}
			return
		}
	}
	old := kp.run
	mark, floated, kept := p.Mark(), p.Floated(), u.keptPainted
	c.paintNode(p, f)
	kp.run, kp.drawn, kp.toWindow, kp.clip, kp.blend = p.RunFrom(mark), u.seq, s.toWindow, s.clip, p.BlendInForce()
	kp.ok = s.proj == nil && p.Floated() == floated && u.keptPainted == kept
	if steadyDebug && same && !p.SameAs(old, mark) {
		log.Printf("gunim steady: a %T's key held, but its subtree drew otherwise", s.node)
	}
}

// noteTitleBars notes the title bar's parts in s's subtree, carried
// unpainted, where they were painted: those drawn in frame seq.
func noteTitleBars(u *UI, s *state, seq uint64) {
	for _, k := range s.kids {
		if k.drawn == seq {
			u.noteTitleBar(k.node, k.toWindow, k.size)
			noteTitleBars(u, k, seq)
		}
	}
}
