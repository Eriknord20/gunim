package plug

import (
	"slices"
	"testing"
)

// spanSource is a source of frames frames, with nothing in them.
type spanSource struct {
	id     int
	frames int64
}

func (s spanSource) ID() int                                { return s.id }
func (s spanSource) Version() int                           { return 1 }
func (s spanSource) Name() string                           { return "vocal" }
func (s spanSource) Rate() float64                          { return 48000 }
func (s spanSource) Channels() int                          { return 2 }
func (s spanSource) Frames() int64                          { return s.frames }
func (s spanSource) ReadAt([][]float32, int64) (int, error) { return 0, nil }

// loopDoc is a document of regions, with a loop.
type loopDoc struct {
	regions []Region
	loop    Loop
	on      bool
}

func (d loopDoc) Sources() []Source  { return nil }
func (d loopDoc) Regions() []Region  { return d.regions }
func (d loopDoc) Loop() (Loop, bool) { return d.loop, d.on }

// plainDoc is a document that tells no loop.
type plainDoc struct{ regions []Region }

func (d plainDoc) Sources() []Source { return nil }
func (d plainDoc) Regions() []Region { return d.regions }

func TestSpansAreTheLoopWhereItIsOnAndTheWholeSourceElse(t *testing.T) {
	src := spanSource{id: 1, frames: 1000}
	whole := []Span{{To: 1000}}
	// A region from frame 100 of the source, played at 500 in the track.
	regions := []Region{{Source: 1, From: 100, Length: 400, At: 500}, {Source: 2, Length: 1000}}
	for _, c := range []struct {
		name string
		d    Document
		want []Span
	}{
		{"no loop told", plainDoc{regions}, whole},
		{"a loop off", loopDoc{regions: regions, loop: Loop{From: 0, To: 600}}, whole},
		{"an empty loop", loopDoc{regions: regions, loop: Loop{From: 600, To: 600}, on: true}, whole},
		{"a loop over the region's start", loopDoc{regions: regions, loop: Loop{From: 0, To: 600}, on: true}, []Span{{From: 100, To: 200}}},
		{"a loop within the region", loopDoc{regions: regions, loop: Loop{From: 600, To: 700}, on: true}, []Span{{From: 200, To: 300}}},
		{"a loop past the region", loopDoc{regions: regions, loop: Loop{From: 900, To: 2000}, on: true}, nil},
	} {
		if got := Spans(c.d, src); !slices.Equal(got, c.want) {
			t.Errorf("%s: Spans = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestLoopOfSaysOnlyALoopOnWithFramesInIt(t *testing.T) {
	if _, on := LoopOf(loopDoc{loop: Loop{From: 10, To: 20}, on: true}); !on {
		t.Error("a loop on, of ten frames, reads as off")
	}
	if _, on := LoopOf(loopDoc{loop: Loop{From: 20, To: 10}, on: true}); on {
		t.Error("a loop that ends before it starts reads as on")
	}
	if _, on := LoopOf(plainDoc{}); on {
		t.Error("a document that tells no loop reads as looping")
	}
}
