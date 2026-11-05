package widget

import (
	"testing"
	"time"

	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
)

type openedLink struct{ URL string }

func TestRichTextWrapsAndFollowsItsLinks(t *testing.T) {
	r := NewRichText(
		RichSpan{Text: "Read the "},
		RichSpan{Text: "manual", OnClick: Sends(openedLink{"https://example.com"})},
		RichSpan{Text: " before you report a fault in the particle counter, please.", Face: BoldFont},
	)
	w, run := stage(t, &frame{child: r, size: geom.Sz(200, 300)})
	if len(r.laid.Lines) < 2 {
		t.Fatalf("at 200 wide the text took %d lines, want it wrapped", len(r.laid.Lines))
	}
	var at geom.Point
	for _, pc := range r.laid.Lines[0].Pieces {
		if pc.Span == 1 {
			at = geom.Pt(pc.At.X+pc.Run.Advance/2, pc.At.Y+pc.Run.Height()/2)
		}
	}
	w.Input(input.PointerMove{Pos: at})
	run(1)
	if r.hover != 1 {
		t.Fatalf("with the pointer on the link, hover is %d", r.hover)
	}
	click(w, at.X, at.Y)
	run(1)
	if got := sent(w); len(got) != 1 || got[0] != (openedLink{"https://example.com"}) {
		t.Fatalf("a click on the link sent %v", got)
	}
	click(w, 2, at.Y)
	run(1)
	if got := sent(w); len(got) != 0 {
		t.Fatalf("a click on plain text sent %v", got)
	}
}

// linkIn finds a point on span i of r.
func linkIn(r *RichText, i int) geom.Point {
	for _, l := range r.laid.Lines {
		for _, pc := range l.Pieces {
			if pc.Span == i {
				return geom.Pt(pc.At.X+pc.Run.Advance/2, pc.At.Y+pc.Run.Height()/2)
			}
		}
	}
	return geom.Point{}
}

// A finger on a link scrolls what the link is in, as anywhere else, and leaves nothing lit.
func TestAFingerDragFromALinkScrollsAndLeavesItUnlit(t *testing.T) {
	r := NewRichText(RichSpan{Text: "Read the "}, RichSpan{Text: "manual", OnClick: Sends(openedLink{"https://example.com"})})
	s := NewScroll(Column(r, NewSized(NewSpacer(), 200, 2000)))
	w, run := stage(t, &frame{child: s, size: geom.Sz(200, 400)})
	at := linkIn(r, 1)
	finger := func(ev any) { w.Input(ev); run(1) }
	now := time.Now()
	finger(input.PointerMove{Pos: at, Touch: true, Time: now})
	finger(input.PointerDown{Pos: at, Clicks: 1, Touch: true, Time: now})
	y := at.Y
	for range 10 {
		now = now.Add(16 * time.Millisecond)
		y -= 20
		// Stands for a drag upward, on a page with room to go.
		finger(input.PointerMove{Pos: geom.Pt(at.X, y), Touch: true, Time: now})
		if y < -150 {
			break
		}
	}
	if s.Offset() <= 0 {
		t.Errorf("a drag from a link left the page at %v", s.Offset())
	}
	if r.hover >= 0 {
		t.Errorf("mid scroll, link %d is still lit", r.hover)
	}
	finger(input.PointerUp{Pos: geom.Pt(at.X, y), Touch: true, Time: now})
	finger(input.PointerLeave{Time: now})
	if r.hover >= 0 {
		t.Errorf("after the finger lifted, link %d is still lit", r.hover)
	}
	if got := sent(w); len(got) != 0 {
		t.Errorf("the drag sent %v", got)
	}
	// A tap follows the link, and leaves it unlit once the finger has lifted.
	run(300) // The fling runs out.
	tap := time.Now()
	s.JumpTo(0)
	run(1)
	at = linkIn(r, 1)
	finger(input.PointerMove{Pos: at, Touch: true, Time: tap})
	finger(input.PointerDown{Pos: at, Clicks: 1, Touch: true, Time: tap})
	finger(input.PointerMove{Pos: at, Touch: true, Time: tap})
	finger(input.PointerUp{Pos: at, Touch: true, Time: tap})
	finger(input.PointerLeave{Time: tap})
	if got := sent(w); len(got) != 1 {
		t.Errorf("the tap sent %v", got)
	}
	if r.hover >= 0 {
		t.Errorf("after a tap, link %d is still lit", r.hover)
	}
}
