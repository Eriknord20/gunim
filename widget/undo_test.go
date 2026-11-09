package widget

import (
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/input"
)

// typeEach types s a character at a time, as keys do.
func (wr *writer) typeEach(s string) {
	for _, r := range s {
		wr.typeText(string(r))
	}
}

func TestUndoTakesTypingBackAWordAtATime(t *testing.T) {
	wr := newWriter(t, 400)
	wr.typeEach("hello world")
	wr.key(input.KeyZ, input.ModControl)
	wr.want("hello ", 6)
	wr.key(input.KeyZ, input.ModControl)
	wr.want("", 0)
	wr.key(input.KeyZ, input.ModControl)
	wr.want("", 0) // nothing left to undo
	wr.key(input.KeyY, input.ModControl)
	wr.want("hello ", 6)
	wr.key(input.KeyZ, input.ModControl|input.ModShift)
	wr.want("hello world", 11)
}

func TestUndoTakesARunOfDeletesBackAtOnce(t *testing.T) {
	wr := newWriter(t, 400)
	wr.typeEach("abcdef")
	wr.key(input.KeyBackspace, 0)
	wr.key(input.KeyBackspace, 0)
	wr.key(input.KeyBackspace, 0)
	wr.want("abc", 3)
	wr.key(input.KeyZ, input.ModControl)
	wr.want("abcdef", 6)
}

func TestMovingTheCaretStartsANewStep(t *testing.T) {
	wr := newWriter(t, 400)
	wr.typeEach("ab")
	wr.key(input.KeyLeft, 0)
	wr.typeEach("c")
	wr.want("acb", 2)
	wr.key(input.KeyZ, input.ModControl)
	wr.want("ab", 1)
}

func TestAPasteIsAStepOfItsOwn(t *testing.T) {
	wr := newWriter(t, 400)
	wr.typeEach("one ")
	if err := wr.w.Offscreen().SetClipboard("two"); err != nil {
		t.Fatal(err)
	}
	wr.key(input.KeyV, input.ModControl)
	wr.typeEach("x")
	wr.want("one twox", 8)
	wr.key(input.KeyZ, input.ModControl)
	wr.want("one two", 7)
	wr.key(input.KeyZ, input.ModControl)
	wr.want("one ", 4)
}

func TestNewTextForgetsTheHistory(t *testing.T) {
	wr := newWriter(t, 400)
	wr.typeEach("draft")
	wr.area.SetText("", nil)
	wr.run(1)
	wr.key(input.KeyZ, input.ModControl)
	wr.want("", 0)
}

func TestUndoSendsTheChange(t *testing.T) {
	wr := newWriter(t, 400)
	wr.area.OnChange = func(s string, u *gunim.UI) gunim.Intent { return changed{s} }
	wr.typeEach("hi")
	sent(wr.w)
	wr.key(input.KeyZ, input.ModControl)
	got := sent(wr.w)
	if len(got) != 1 || got[0] != (changed{""}) {
		t.Fatalf("intents %v after undo, want a change to nothing", got)
	}
}

func TestOnFrameTimeTypingFramesApartIsTwoStepsOfUndo(t *testing.T) {
	wr := newWriter(t, 400)
	wr.w.TimeByFrames(true)
	wr.typeEach("ab")
	// Frames longer than the pause that ends a step pass in far less
	// wall time: the window goes by its frames.
	wr.run(int(undoPause/(time.Second/60)) + 1)
	wr.typeEach("cd")
	wr.key(input.KeyZ, input.ModControl)
	wr.want("ab", 2)
}
