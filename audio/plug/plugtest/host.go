package plugtest

import (
	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/plug"
)

// Host is a [plug.Host] for a view's test: it keeps the parameters' values, plays [Music] in and out, and reads
// traces from Trace.
type Host struct {
	// D is the plugin the host runs.
	D *plug.Descriptor
	// Values holds the parameters set, by ID. One left out is at its default.
	Values map[uint32]float64
	// In and Out are the sound the views hear.
	In, Out plug.Sound
	// Trace, where set, reads a value the processor keeps for a frame, as [plug.Host.Traced] does.
	Trace func(id uint32, f int64) (float32, bool)
}

// NewHost returns a host for a plugin of d, its parameters at their defaults and music playing through it.
func NewHost(d *plug.Descriptor) *Host {
	m := &Music{}
	return &Host{D: d, Values: map[uint32]float64{}, In: m, Out: m}
}

// Param implements [plug.Host].
func (h *Host) Param(id uint32) float64 {
	if v, ok := h.Values[id]; ok {
		return v
	}
	p, _ := h.D.Param(id)
	return p.Default
}

// Begin implements [plug.Host].
func (h *Host) Begin(uint32, *gunim.UI) {}

// Set implements [plug.Host].
func (h *Host) Set(id uint32, v float64, _ *gunim.UI) { h.Values[id] = v }

// End implements [plug.Host].
func (h *Host) End(uint32, *gunim.UI) {}

// BeginEdit implements [plug.Host].
func (h *Host) BeginEdit(string, *gunim.UI) {}

// Changed implements [plug.Host].
func (h *Host) Changed(*gunim.UI) {}

// EndEdit implements [plug.Host].
func (h *Host) EndEdit(*gunim.UI) {}

// Sound implements [plug.Host].
func (h *Host) Sound() (in, out plug.Sound) { return h.In, h.Out }

// Traced implements [plug.Host].
func (h *Host) Traced(id uint32, f int64) (float32, bool) {
	if h.Trace == nil {
		return 0, false
	}
	return h.Trace(id, f)
}

// WindowView returns the view of p a host shows in a window of its own: its [plug.Full] view, or its [plug.Panel]
// view where it has no Full one, or nil where it has neither.
func WindowView(p plug.Plugin, h plug.Host) gunim.Node {
	v, ok := p.(plug.Viewer)
	if !ok {
		return nil
	}
	if n := v.View(plug.Full, h); n != nil {
		return n
	}
	return v.View(plug.Panel, h)
}
