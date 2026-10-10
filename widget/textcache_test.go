package widget

import (
	"testing"

	"github.com/marrasen/gunim/text"
)

func TestCutRunLeavesNothingWhereEvenTheEllipsisIsTooWide(t *testing.T) {
	face, size := faceIn(Font, nil), TextSize.Default()
	run, ell := face.Shape("Hello", size), face.Shape("…", size)
	if got := cutRunWith(run, ell, ell.Advance/2); len(got.Glyphs) != 0 || got.Advance != 0 {
		t.Fatalf("in half an ellipsis's room the cut text is %d glyphs, %v wide", len(got.Glyphs), got.Advance)
	}
	if got := cutRunWith(run, ell, ell.Advance); len(got.Glyphs) != len(ell.Glyphs) || got.Advance != ell.Advance {
		t.Fatalf("in an ellipsis's room the cut text is %d glyphs, %v wide; want the ellipsis alone", len(got.Glyphs),
			got.Advance)
	}
}

func TestCutRunEndsTextTooLongInAnEllipsis(t *testing.T) {
	face := text.Default()
	run := face.Shape("Master of Ceremonies – Unnamed", 14)
	if got := CutRun(run, run.Advance); len(got.Glyphs) != len(run.Glyphs) {
		t.Fatalf("text that fits lost %d glyphs", len(run.Glyphs)-len(got.Glyphs))
	}
	room := run.Advance / 2
	got := CutRun(run, room)
	ell := face.Shape("…", 14)
	if got.Advance > room || len(got.Glyphs) >= len(run.Glyphs) {
		t.Fatalf("cut to %v wide with %d glyphs, for room %v", got.Advance, len(got.Glyphs), room)
	}
	if last := got.Glyphs[len(got.Glyphs)-1]; last.ID != ell.Glyphs[0].ID {
		t.Fatal("the text cut short does not end in an ellipsis")
	}
}
