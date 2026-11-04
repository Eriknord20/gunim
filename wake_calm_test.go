package gunim

import (
	"testing"
	"time"
)

func TestWakerDeadlineKeepsUnrelatedSteadyNodesCalm(t *testing.T) {
	w := newTestWindow()
	row := &steadyRow{key: 1, ok: true, colour: rowBlue}
	w.ui.Insert(w.ui.Root(), row)
	run(w, 60)
	before := row.painted
	w.ui.wakeAfter(time.Second / 60)
	w.Frame(time.Second / 60)
	if row.painted != before {
		t.Fatalf("Waker repainted an unrelated steady subtree: %d -> %d", before, row.painted)
	}
}
func TestOrdinaryAfterTimerStillInvalidatesSteadyNodes(t *testing.T) {
	w := newTestWindow()
	row := &steadyRow{key: 1, ok: true, colour: rowBlue}
	w.ui.Insert(w.ui.Root(), row)
	run(w, 60)
	before := row.painted
	fired := false
	w.ui.After(time.Second/60, func(*UI) { row.colour.R = 255; fired = true })
	w.Frame(time.Second / 60)
	if !fired || row.painted == before || !hasColour(t, w, row.colour) {
		t.Fatal("ordinary timer left stale steady content")
	}
}
