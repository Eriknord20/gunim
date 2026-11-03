package glfw

import "testing"

func TestTheAccentSettingReads(t *testing.T) {
	c, on, err := accentBorder()
	if err != nil {
		t.Fatal(err)
	}
	if on && c.A != 0xff {
		t.Fatalf("the accent colour %v is not opaque", c)
	}
}

func TestASmallWindowsShadowStripsDoNotOverlap(t *testing.T) {
	s := &drawnShadow{corner: 26, scale: 1, active: true}
	if err := s.draw(60, 40, 144, nil); err != nil {
		t.Fatal(err)
	}
	st := s.strips()
	if top, bottom := st[0], st[1]; top.y+top.h > bottom.y || st[2].h < 0 || st[3].h < 0 {
		t.Fatalf("the strips of a 60 by 40 window with round corners are %+v", st)
	}
}
