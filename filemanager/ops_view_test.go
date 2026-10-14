package filemanager

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/icon"
)

func TestPausingHoldsACopyUntilItCarriesOn(t *testing.T) {
	h := newHarness(t, "sub/")
	if err := os.WriteFile(filepath.Join(h.dir, "big.bin"), make([]byte, 3<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	h.a.ops.limit = 2 << 20
	h.a.startOp(job{kind: OpCopy, srcs: []string{filepath.Join(h.dir, "big.bin")}, dest: filepath.Join(h.dir, "sub")},
		"Copying big.bin")
	h.a.ops.limit = 0
	id := h.a.ops.next
	r := h.a.ops.running[id]
	h.until("a card shows the copy", func() bool { return h.b.ops.card(id) != nil && r.last.bytes > 0 })
	if c := h.b.ops.card(id); !c.pauses.on || c.from.Text != "From "+filepath.Base(h.dir) {
		t.Fatalf("the card offers pause %v and says %q", c.pauses.on, c.from.Text)
	}
	h.do(PauseOp{ID: id})
	h.until("the card says it is paused", func() bool { return h.b.ops.card(id).big.Text == "Paused" })
	if c := h.b.ops.card(id); c.pause.Icon != icon.Play || c.slimNote.Text != "Paused" || c.left.Text != "" {
		t.Fatalf("the paused card shows %v, %q and %q", c.pause.Icon.Name, c.slimNote.Text, c.left.Text)
	}
	// What was on its way lands; then nothing moves.
	time.Sleep(300 * time.Millisecond)
	h.pump()
	held := r.last.bytes
	time.Sleep(600 * time.Millisecond)
	h.pump()
	if r.last.bytes != held || len(h.a.ops.running) != 1 {
		t.Fatalf("the paused copy went from %d to %d bytes", held, r.last.bytes)
	}
	h.do(PauseOp{ID: id, Resume: true})
	if r.paused {
		t.Fatal("the copy is still paused")
	}
	h.until("the copy carries on to its end", func() bool { return len(h.a.ops.running) == 0 })
	if got := contents(t, filepath.Join(h.dir, "sub", "big.bin")); len(got) != 3<<20 {
		t.Fatalf("the copy has %d bytes", len(got))
	}
}

func TestCancellingAPausedCopyStopsIt(t *testing.T) {
	h := newHarness(t, "sub/")
	if err := os.WriteFile(filepath.Join(h.dir, "big.bin"), make([]byte, 3<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	h.a.ops.limit = 2 << 20
	h.a.startOp(job{kind: OpCopy, srcs: []string{filepath.Join(h.dir, "big.bin")}, dest: filepath.Join(h.dir, "sub")},
		"Copying big.bin")
	h.a.ops.limit = 0
	id := h.a.ops.next
	h.until("the copy runs", func() bool { return h.a.ops.running[id].last.bytes > 0 })
	h.do(PauseOp{ID: id})
	h.do(CancelOp{ID: id})
	h.until("the copy stops", func() bool { return len(h.a.ops.running) == 0 })
	if _, err := os.Stat(filepath.Join(h.dir, "sub", "big.bin")); err == nil {
		t.Fatal("the stopped copy left its file")
	}
}

func TestAThirdOperationFoldsTheOldest(t *testing.T) {
	h := newDndHarness(t)
	op := func(id int) OpView {
		return OpView{ID: id, Title: "Copying", OpProgress: OpProgress{Done: 0.5, BytesTotal: 10, Bytes: 5}}
	}
	h.ui(func(b *browser, u *gunim.UI) { b.ops.set(Ops{Ops: []OpView{op(1), op(2)}}, u) })
	h.ui(func(b *browser, u *gunim.UI) {
		if b.ops.card(1).folded.Target() != 0 || b.ops.card(2).folded.Target() != 0 {
			t.Error("two operations folded a card")
		}
		b.ops.set(Ops{Ops: []OpView{op(1), op(2), op(3)}}, u)
	})
	h.ui(func(b *browser, u *gunim.UI) {
		for id, want := range map[int]float32{1: 1, 2: 0, 3: 0} {
			if got := b.ops.card(id).folded.Target(); got != want {
				t.Errorf("card %d folds to %v, want %v", id, got, want)
			}
		}
		b.ops.set(Ops{Ops: []OpView{op(3)}}, u)
		if b.ops.card(1) != nil || len(b.ops.cards) != 1 {
			t.Errorf("%d cards stay once two operations ended", len(b.ops.cards))
		}
	})
	h.frames(60)
	h.ui(func(b *browser, u *gunim.UI) {
		if b.ops.height <= 0 {
			t.Error("the stack takes no room with a card in it")
		}
		b.ops.set(Ops{}, u)
	})
	h.frames(90)
	if h.b.ops.height != 0 {
		t.Fatalf("the empty stack is %v tall", h.b.ops.height)
	}
}

// The progress drawn moves on between reports at their pace, bends
// toward each report, and never goes back.
func TestTheProgressGlidesOnAndNeverBack(t *testing.T) {
	var g glide
	g.to(0, false, false)
	g.at = g.at.Add(-time.Second)
	g.to(0.1, false, false)
	if g.pace < 0.09 || g.pace > 0.11 {
		t.Fatalf("the pace from a tenth in a second is %v", g.pace)
	}
	prev := g.value()
	for range 20 {
		g.step(10 * time.Millisecond)
		v := g.value()
		if v < prev {
			t.Fatalf("the progress went back from %v to %v", prev, v)
		}
		if float64(v) > 0.1+g.pace*0.2+1e-6 {
			t.Fatalf("the progress ran to %v, far past the report", v)
		}
		prev = v
	}
	// A report behind what is drawn holds it, rather than pulling it back.
	g.to(0.05, false, false)
	g.step(10 * time.Millisecond)
	if g.value() < prev {
		t.Fatalf("an old report pulled the progress back to %v", g.value())
	}
	// Paused, it closes on the last report and stops there.
	g.to(0.5, false, true)
	for range 400 {
		g.step(10 * time.Millisecond)
	}
	if v := g.value(); v < 0.49 || v > 0.5 {
		t.Fatalf("paused, the progress stopped at %v, want the report's 0.5", v)
	}
}

func TestTheTimeLeftIsSaidPlainly(t *testing.T) {
	for secs, want := range map[float64]string{
		1: "1 s left", 31.4: "31 s left", 59.6: "1 min left", 125: "2 min 5 s left", 600: "10 min left",
		1830: "31 min left", 3900: "1 h 5 min left",
	} {
		if got := timeLeft(secs); got != want {
			t.Errorf("timeLeft(%v) = %q, want %q", secs, got, want)
		}
	}
	for d, want := range map[float32]string{0: "0%", 0.384: "38%", 0.996: "99%", 1: "100%"} {
		if got := percent(d); got != want {
			t.Errorf("percent(%v) = %q, want %q", d, got, want)
		}
	}
}
