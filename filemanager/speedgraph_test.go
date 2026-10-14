package filemanager

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTheSpeedComesFromACopysProgress(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "big.bin")
	if err := os.WriteFile(src, make([]byte, 4*copyBuffer), 0o644); err != nil {
		t.Fatal(err)
	}
	var reports []progress
	e := env{fs: LocalFS(), report: func(p progress) { reports = append(reports, p) }, reportEvery: time.Nanosecond}
	if _, err := runJob(context.Background(), job{kind: OpCopy, srcs: []string{src}, dest: root}, e); err != nil {
		t.Fatal(err)
	}
	// The copy moves a megabyte each quarter of a second.
	var m speedometer
	start := time.Now()
	var rates []float64
	for _, p := range reports {
		if m.add(start.Add(time.Duration(p.bytes/copyBuffer)*250*time.Millisecond), p.bytes) {
			rates = append(rates, m.rate)
		}
	}
	last := reports[len(reports)-1]
	if last.bytes != 4*copyBuffer || last.bytesTotal != 4*copyBuffer {
		t.Fatalf("the copy reported %d of %d bytes at the end", last.bytes, last.bytesTotal)
	}
	if len(rates) < 2 {
		t.Fatalf("%d samples from %d reports", len(rates), len(reports))
	}
	for _, r := range rates {
		if want := float64(copyBuffer) * 4; r != want {
			t.Fatalf("the samples are %v, want each a megabyte a quarter second, %v", rates, want)
		}
	}
	if left := m.left(start.Add(time.Second), copyBuffer, 4*copyBuffer); left <= 0 {
		t.Fatalf("with three quarters to go the time left is %v", left)
	}
}

func TestACopyDrawsItsSpeedAndCheersAsItEnds(t *testing.T) {
	h := newHarness(t, "sub/")
	if err := os.WriteFile(filepath.Join(h.dir, "big.bin"), make([]byte, 3<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	h.a.ops.limit = 3 << 20
	h.a.startOp(job{kind: OpCopy, srcs: []string{filepath.Join(h.dir, "big.bin")}, dest: filepath.Join(h.dir, "sub")},
		"Copying big.bin")
	h.a.ops.limit = 0
	card := func() *opCard { return h.b.ops.card(h.a.ops.next) }
	h.until("the graph has speeds", func() bool { return card() != nil && len(card().graph.samples) >= 2 })
	if c := card(); !strings.HasSuffix(c.big.Text, "/s") || c.moved.Text == "" {
		t.Fatalf("the running card says %q and %q", c.big.Text, c.moved.Text)
	}
	h.until("the copy ends with a cheer", func() bool { return card() != nil && card().badge.cheering })
	if c := card(); c.big.Text != "Done" || c.title.Text != "Copied 1 item to sub" || c.moved.Text != "100%  ·  3.0 MB of 3.0 MB" {
		t.Fatalf("the finished card says %q, %q and %q", c.title.Text, c.big.Text, c.moved.Text)
	}
	h.until("the card leaves", func() bool { return card() == nil })
}

// The time left counts down steadily while the speed swings, rather than
// leaping by seconds with every sample, and the
// speed the panel says holds for a second at a time.
func TestTheTimeLeftHoldsStillAsTheSpeedSwings(t *testing.T) {
	var m speedometer
	now := time.Now()
	const total = 400 << 20
	done := int64(0)
	m.add(now, done)
	prev, said, changes := -1.0, 0.0, 0
	for i := range 100 {
		now = now.Add(200 * time.Millisecond)
		// A fifth of a second at 2 MB a second, then one at 6.
		done += int64(2<<20+(i%2)*4<<20) / 5
		m.add(now, done)
		left := m.left(now, done, total)
		// It never goes up, and once it has settled on the speed, it
		// goes down about a fifth of a second.
		if d := prev - left; prev >= 0 && (d < 0 || i > 50 && (d < 0.05 || d > 0.35)) {
			t.Fatalf("sample %d: the time left went from %.2fs to %.2fs in a fifth of a second", i, prev, left)
		}
		prev = left
		if m.shown != said {
			said = m.shown
			changes++
		}
	}
	if changes > 21 {
		t.Fatalf("the speed shown changed %d times in 20 seconds", changes)
	}
}
