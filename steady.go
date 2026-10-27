package gunim

import (
	"log"
	"os"

	"github.com/marrasen/gunim/paint"
)

// steadyDebug is set by GUNIM_DEBUG_STEADY=1, which draws every Steady node's
// subtree anew and logs each whose key said it would draw the same, and
// did not.
var steadyDebug = os.Getenv("GUNIM_DEBUG_STEADY") == "1"

// carries reports whether the frame carries Steady node s's subtree as it was
// laid out, k its node: within cs, with the key k gives in f. It marks
// the subtree laid where it does.
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
	carry := ok && u.calm && kp.ok && kp.laid+1 == u.seq && kp.drawn+1 == u.seq && kp.cs == cs && kp.key == key &&
		still(s, u.seq)
	kp.key, kp.cs, kp.laid, kp.carry = key, cs, u.seq, carry
	if carry {
		stamp(s, u.seq, false)
	}
	return carry
}

// still reports whether no node in s's subtree moved, by its Step, in
// this frame or at the end of the one before, and all are present.
func still(s *state, seq uint64) bool {
	if s.presence != Present || s.moved+1 >= seq {
		return false
	}
	for _, k := range s.kids {
		if !still(k, seq) {
			return false
		}
	}
	return true
}

// stamp marks s's subtree laid out, or with drawn painted, in frame seq,
// as Layout and Paint would have. Below a carried node, what each node
// was laid out and painted as stands.
func stamp(s *state, seq uint64, drawn bool) {
	for _, k := range s.kids {
		if drawn {
			k.drawn = seq
		} else {
			k.laid = seq
		}
		stamp(k, seq, drawn)
	}
}

// paintKept paints c, a Steady node, kp what is kept of it: the run
// it painted the frame before, where the frame carries it and it paints
// in the same place, inside the same clips; else as usual, keeping the
// run for the next frame.
func (u *UI) paintKept(c Child, kp *keeping, p *paint.Painter, f Frame) {
	s := c.n
	same := kp.carry && s.toWindow == kp.toWindow && s.proj == nil && s.clip.Same(kp.clip)
	if same && !steadyDebug {
		mark := p.Mark()
		if p.Again(kp.run) {
			kp.run, kp.drawn = p.RunFrom(mark), u.seq
			stamp(s, u.seq, true)
			if u.chrome != nil {
				noteTitleBars(u, s)
			}
			return
		}
	}
	old := kp.run
	mark, floated, kept := p.Mark(), p.Floated(), u.keptPainted
	c.paintNode(p, f)
	kp.run, kp.drawn, kp.toWindow, kp.clip = p.RunFrom(mark), u.seq, s.toWindow, s.clip
	kp.ok = s.proj == nil && p.Floated() == floated && u.keptPainted == kept
	if steadyDebug && same && !p.SameAs(old, mark) {
		log.Printf("gunim steady: a %T's key held, but its subtree drew otherwise", s.node)
	}
}

// noteTitleBars notes the title bar's parts in s's subtree, carried
// unpainted, where they were painted.
func noteTitleBars(u *UI, s *state) {
	for _, k := range s.kids {
		u.noteTitleBar(k.node, k.toWindow, k.size)
		noteTitleBars(u, k)
	}
}
