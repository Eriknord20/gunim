package plugui

import (
	"image"
	"sync"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
)

// sizingEditor is an editor that can be told to take a size of itself,
// as one with a resize handle of its own does.
type sizingEditor struct {
	mu     sync.Mutex
	w, h   int
	sized  func(w, h int)
	placed image.Rectangle
}

func (e *sizingEditor) EmbedEditor(_ uintptr, sized func(w, h int)) (w, h int, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sized = sized
	return e.w, e.h, nil
}

func (e *sizingEditor) PlaceEditor(r image.Rectangle) (w, h int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.placed = r
	if !r.Empty() {
		// Resizable: it takes the room it is given.
		e.w, e.h = r.Dx(), r.Dy()
	}
	return e.w, e.h
}

func (e *sizingEditor) CloseEditor() {}

// resize has the editor take w×h of itself.
func (e *sizingEditor) resize(w, h int) {
	e.mu.Lock()
	e.w, e.h = w, h
	sized := e.sized
	e.mu.Unlock()
	sized(w, h)
}

func TestTheWindowFitsAnEditorThatSizesItself(t *testing.T) {
	ed := &sizingEditor{w: 400, h: 300}
	slot := NewEditorSlot(ed)
	slot.Fit = true
	w := gunimtest.NewWith(t, gunim.WindowOptions{Size: geom.Sz(400, 300), Instant: true})
	gunim.RegisterView(w, "slot", func(struct{}) gunim.Node { return slot }, nil)
	if err := w.Client().Mount(gunim.Root, "slot", "slot", nil); err != nil {
		t.Fatal(err)
	}
	frames := func(d time.Duration) {
		for end := time.Now().Add(d); time.Now().Before(end); {
			w.Frame(time.Second / 60)
			time.Sleep(time.Millisecond)
		}
	}
	frames(100 * time.Millisecond)
	if !slot.Open() {
		t.Fatal("the editor did not open")
	}
	// Its own handle dragged, the editor grows: the window follows.
	ed.resize(520, 360)
	frames(500 * time.Millisecond)
	if got := w.Offscreen().Size(); got != geom.Sz(520, 360) {
		t.Fatalf("the editor at 520×360, the window is %v", got)
	}
	ed.mu.Lock()
	placed := ed.placed
	ed.mu.Unlock()
	if placed != image.Rect(0, 0, 520, 360) {
		t.Fatalf("the editor is placed at %v", placed)
	}
}
