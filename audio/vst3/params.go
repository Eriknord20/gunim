package vst3

import (
	"math"
	"sync/atomic"
)

// paramBox carries parameters' values from one side of a plugin to the
// other, as from the plugins' thread to the processor, without either
// side waiting or making anything: a slot for each parameter the
// controller lists, holding the last value put and whether it waits to
// be taken. A value put while another waits takes its place, as the
// processor wants only the last.
type paramBox struct {
	// at finds a parameter's slot by its ID; it never changes once
	// made, so both sides read it at once.
	at    map[uint32]int
	slots []paramSlot
	// puts counts the values put since the taker last looked, so a
	// block with nothing new looks at no slot.
	puts atomic.Int64
}

type paramSlot struct {
	id      uint32
	bits    atomic.Uint64
	waiting atomic.Bool
}

func newParamBox(ids []uint32) *paramBox {
	b := &paramBox{at: make(map[uint32]int, len(ids)), slots: make([]paramSlot, len(ids))}
	for i, id := range ids {
		b.at[id] = i
		b.slots[i].id = id
	}
	return b
}

// put puts v as parameter id's value, and returns false where the
// plugin lists no such parameter.
func (b *paramBox) put(id uint32, v float64) bool {
	i, ok := b.at[id]
	if !ok {
		return false
	}
	s := &b.slots[i]
	s.bits.Store(math.Float64bits(v))
	s.waiting.Store(true)
	b.puts.Add(1)
	return true
}

// waits says a value waits to be taken.
func (b *paramBox) waits() bool { return b.puts.Load() > 0 }

// look starts taking the values waiting, and says whether any may:
// then take, for each slot in turn, takes its value where one waits.
// One goroutine takes.
func (b *paramBox) look() bool { return b.puts.Swap(0) > 0 }

func (b *paramBox) take(i int) (id uint32, v float64, ok bool) {
	s := &b.slots[i]
	if !s.waiting.Swap(false) {
		return 0, 0, false
	}
	return s.id, math.Float64frombits(s.bits.Load()), true
}
