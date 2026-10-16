package vst3

import (
	"encoding/binary"
	"math"
	"sync"
	"testing"
	"unsafe"
)

// A fake plugin, written in Go as a plugin's objects are laid out, for
// the tests of what the host hands a plugin: its buses, its events and
// its changes. It is its own controller, of no parameters, and records
// every block it processes.

type fakeBus struct {
	name     string
	channels int
	aux      bool
}

type fakePlugin struct {
	ins, outs []fakeBus
	// events says it has an event input; tuning that it bends notes of
	// their own; midi maps controllers, by channel and number, to
	// parameters.
	events bool
	tuning bool
	midi   map[[2]int]uint32
	// state is what it saves, and latency and tail what it says;
	// tells says it tells its parameters changed as its state is set,
	// and rewrites that it saves a state set otherwise than it was
	// given, as a plugin that writes its own anew does.
	state    []byte
	tells    bool
	rewrites bool
	// fails says its process fails, writing nothing.
	fails   bool
	latency int
	tail    uint32

	comp, proc, ctrl, mm, ne uintptr
	handler                  uintptr

	mu         sync.Mutex
	blocks     []fakeBlock
	processing []bool
	active     map[[3]int]bool
	setStates  int
}

// fakeBlock is a block as the fake heard it.
type fakeBlock struct {
	frames int
	// ins holds each input bus's channels' first frames.
	ins     [][]float32
	events  []vstEvent
	changes map[uint32][]point
	ctx     processContext
}

type fakeObject struct {
	object
	f *fakePlugin
}

func fakeOf(this uintptr) *fakePlugin { return self[fakeObject](this).f }

// other returns the fake's object id, for a query of one of its others.
func (f *fakePlugin) other(id uid) uintptr {
	var p uintptr
	switch id {
	case iidComponent:
		p = f.comp
	case iidAudioProcessor:
		p = f.proc
	case iidEditController:
		p = f.ctrl
	case iidMidiMapping:
		p = f.mm
	case iidNoteExpression:
		if f.tuning {
			p = f.ne
		}
	}
	if p != 0 {
		selfObject(p).refs.Add(1)
	}
	return p
}

func fakeOther(this uintptr, id uid) uintptr { return fakeOf(this).other(id) }

func ok() uintptr { return resultOf(resultOK) }

// arrangement returns the speakers of n channels.
func arrangement(n int) uint64 {
	if n == 1 {
		return mono
	}
	return 1<<n - 1
}

var fakeCompKind = &kind{iids: []uid{iidComponent}, other: fakeOther, make: func() []uintptr {
	return []uintptr{
		newCallback(func(this, ctx uintptr) uintptr { return ok() }),
		newCallback(func(this uintptr) uintptr { return ok() }),
		newCallback(func(this, cid uintptr) uintptr { return resultOf(resultFalse) }),
		newCallback(func(this, mode uintptr) uintptr { return ok() }),
		newCallback(func(this, media, dir uintptr) uintptr {
			f := fakeOf(this)
			switch {
			case i32(media) == mediaEvent && i32(dir) == dirInput && f.events:
				return 1
			case i32(media) == mediaAudio && i32(dir) == dirInput:
				return uintptr(len(f.ins))
			case i32(media) == mediaAudio && i32(dir) == dirOutput:
				return uintptr(len(f.outs))
			}
			return 0
		}),
		newCallback(func(this, media, dir, index, info uintptr) uintptr {
			f := fakeOf(this)
			buses := f.ins
			if i32(dir) == dirOutput {
				buses = f.outs
			}
			i := int(i32(index))
			if i32(media) != mediaAudio || i < 0 || i >= len(buses) {
				return resultOf(resultFalse)
			}
			bi := ptr[busInfo](info)
			*bi = busInfo{mediaType: mediaAudio, direction: i32(dir), channels: int32(buses[i].channels)}
			if buses[i].aux {
				bi.busType = 1
			}
			copy(bi.name[:127], utf16Of(buses[i].name))
			return ok()
		}),
		newCallback(func(this, in, out uintptr) uintptr { return resultOf(resultFalse) }),
		newCallback(func(this, media, dir, index, state uintptr) uintptr {
			f := fakeOf(this)
			f.mu.Lock()
			f.active[[3]int{int(i32(media)), int(i32(dir)), int(i32(index))}] = state&0xff != 0
			f.mu.Unlock()
			return ok()
		}),
		newCallback(func(this, state uintptr) uintptr { return ok() }),
		newCallback(func(this, stream uintptr) uintptr {
			f := fakeOf(this)
			buf := make([]byte, 256)
			var n int32
			call(stream, 3, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&n)))
			f.mu.Lock()
			f.state = buf[:n]
			if f.rewrites && (n == 0 || buf[n-1] != '!') {
				f.state = append(f.state, '!')
			}
			f.setStates++
			tells, h := f.tells, f.handler
			f.mu.Unlock()
			if tells && h != 0 {
				callFloat(h, 4, 5, 0.25)
				call(h, 6, restartParamValues)
			}
			return ok()
		}),
		newCallback(func(this, stream uintptr) uintptr {
			f := fakeOf(this)
			f.mu.Lock()
			st := append([]byte(nil), f.state...)
			f.mu.Unlock()
			if len(st) > 0 {
				var n int32
				call(stream, 4, uintptr(unsafe.Pointer(&st[0])), uintptr(len(st)), uintptr(unsafe.Pointer(&n)))
			}
			return ok()
		}),
	}
}}

var fakeProcKind = &kind{iids: []uid{iidAudioProcessor}, other: fakeOther, make: func() []uintptr {
	return []uintptr{
		newCallback(func(this, ins, nIn, outs, nOut uintptr) uintptr {
			f := fakeOf(this)
			// It takes the main buses in any of one or two channels.
			take := func(arr uintptr, n int, buses []fakeBus) {
				for i := range n {
					a := *ptr[uint64](arr + uintptr(i)*8)
					if !buses[i].aux && (a == mono || a == stereo) {
						buses[i].channels = map[uint64]int{mono: 1, stereo: 2}[a]
					}
				}
			}
			if i32(nIn) > 0 {
				take(ins, int(i32(nIn)), f.ins)
			}
			take(outs, int(i32(nOut)), f.outs)
			return ok()
		}),
		newCallback(func(this, dir, index, arr uintptr) uintptr {
			f := fakeOf(this)
			buses := f.ins
			if i32(dir) == dirOutput {
				buses = f.outs
			}
			*ptr[uint64](arr) = arrangement(buses[int(i32(index))].channels)
			return ok()
		}),
		newCallback(func(this, size uintptr) uintptr { return ok() }),
		newCallback(func(this uintptr) uintptr { return uintptr(fakeOf(this).latency) }),
		newCallback(func(this, setup uintptr) uintptr { return ok() }),
		newCallback(func(this, state uintptr) uintptr {
			f := fakeOf(this)
			f.mu.Lock()
			f.processing = append(f.processing, state&0xff != 0)
			f.mu.Unlock()
			return ok()
		}),
		newCallback(func(this, data uintptr) uintptr {
			f := fakeOf(this)
			if f.fails {
				return resultOf(resultFalse)
			}
			f.process(ptr[processData](data))
			return ok()
		}),
		newCallback(func(this uintptr) uintptr { return uintptr(fakeOf(this).tail) }),
	}
}}

// process records the block, and plays: each main output channel twice
// the main input's, or 1 and 2 without one, and channel c of aux output
// i, 10i+c+1.
func (f *fakePlugin) process(d *processData) {
	n := int(d.frames)
	blk := fakeBlock{frames: n, changes: map[uint32][]point{}, ctx: *ptr[processContext](d.context)}
	bus := func(at uintptr, i int) [][]float32 {
		bb := ptr[busBuffers](at + uintptr(i)*unsafe.Sizeof(busBuffers{}))
		out := make([][]float32, bb.channels)
		for c := range out {
			out[c] = unsafe.Slice(*ptr[*float32](bb.buffers + uintptr(c)*unsafe.Sizeof(uintptr(0))), n)
		}
		return out
	}
	var mainIn [][]float32
	for i := range int(d.numInputs) {
		chans := bus(d.inputs, i)
		first := make([]float32, len(chans))
		for c := range chans {
			first[c] = chans[c][0]
		}
		blk.ins = append(blk.ins, first)
		if i == 0 && !f.ins[0].aux {
			mainIn = chans
		}
	}
	for i := range int(d.numOutputs) {
		for c, ch := range bus(d.outputs, i) {
			for k := range ch {
				switch {
				case i > 0:
					ch[k] = float32(10*i + c + 1)
				case mainIn != nil:
					ch[k] = 2 * mainIn[c%len(mainIn)][k]
				default:
					ch[k] = float32(c + 1)
				}
			}
		}
	}
	for j := range int(i32(call(d.inEvents, 3))) {
		var e vstEvent
		call(d.inEvents, 4, uintptr(j), uintptr(unsafe.Pointer(&e)))
		blk.events = append(blk.events, e)
	}
	for j := range int(i32(call(d.inParams, 3))) {
		q := call(d.inParams, 4, uintptr(j))
		id := uint32(call(q, 3))
		for k := range int(i32(call(q, 4))) {
			var pt point
			call(q, 5, uintptr(k), uintptr(unsafe.Pointer(&pt.offset)), uintptr(unsafe.Pointer(&pt.value)))
			blk.changes[id] = append(blk.changes[id], pt)
		}
	}
	f.mu.Lock()
	f.blocks = append(f.blocks, blk)
	f.mu.Unlock()
}

var fakeCtrlKind = &kind{iids: []uid{iidEditController}, other: fakeOther, make: func() []uintptr {
	none := func(this uintptr) uintptr { return resultOf(resultFalse) }
	return []uintptr{
		newCallback(func(this, ctx uintptr) uintptr { return ok() }),
		newCallback(func(this uintptr) uintptr { return ok() }),
		newCallback(func(this, stream uintptr) uintptr { return ok() }),
		newCallback(func(this, stream uintptr) uintptr { return ok() }),
		newCallback(func(this, stream uintptr) uintptr { return ok() }),
		newCallback(func(this uintptr) uintptr { return 0 }),
		newCallback(func(this, index, info uintptr) uintptr { return resultOf(resultFalse) }),
		newFloatCallback4(func(this, id uintptr, v float64, s uintptr) uintptr { return resultOf(resultFalse) }),
		newCallback(func(this, id, s, v uintptr) uintptr { return resultOf(resultFalse) }),
		// normalizedParamToPlain, plainParamToNormalized and
		// getParamNormalized return doubles, of parameters it has none of.
		newCallback(none),
		newCallback(none),
		newCallback(none),
		newFloatCallback(func(this, id uintptr, v float64) uintptr { return ok() }),
		newCallback(func(this, h uintptr) uintptr {
			fakeOf(this).handler = h
			return ok()
		}),
		newCallback(func(this, name uintptr) uintptr { return 0 }),
	}
}}

var fakeMidiKind = &kind{iids: []uid{iidMidiMapping}, other: fakeOther, make: func() []uintptr {
	return []uintptr{
		newCallback(func(this, busIndex, channel, cc, id uintptr) uintptr {
			f := fakeOf(this)
			p, ok := f.midi[[2]int{int(int16(channel)), int(int16(cc))}]
			if !ok || i32(busIndex) != 0 {
				return resultOf(resultFalse)
			}
			*ptr[uint32](id) = p
			return resultOf(resultOK)
		}),
	}
}}

var fakeNoteExprKind = &kind{iids: []uid{iidNoteExpression}, other: fakeOther, make: func() []uintptr {
	return []uintptr{
		newCallback(func(this, busIndex, channel uintptr) uintptr { return 1 }),
		newCallback(func(this, busIndex, channel, index, info uintptr) uintptr {
			*ptr[noteExpressionInfo](info) = noteExpressionInfo{typeID: noteExprTuning, def: 0.5, max: 1}
			return ok()
		}),
		newCallback(func(this, a, b, c, d uintptr) uintptr { return resultOf(resultFalse) }),
		newCallback(func(this, a, b, c, d uintptr) uintptr { return resultOf(resultFalse) }),
	}
}}

func utf16Of(s string) []uint16 {
	out := make([]uint16, 0, len(s))
	for _, r := range s {
		out = append(out, uint16(r))
	}
	return out
}

// open makes a plugin of the fake, set up as cfg says.
func (f *fakePlugin) open(t *testing.T, cfg Config) (*Plugin, error) {
	t.Helper()
	f.makeObjects()
	return start(f.class(), cfg, func(p *Plugin) error { return p.build(f.comp, nil) })
}

// makeObjects makes the fake's objects. They stay live: a plugin of
// them may close after the test's own cleanups.
func (f *fakePlugin) makeObjects() {
	f.active = map[[3]int]bool{}
	mk := func(k *kind) uintptr { return keep(&fakeObject{f: f}, k, false) }
	f.comp, f.proc, f.ctrl, f.mm, f.ne = mk(fakeCompKind), mk(fakeProcKind), mk(fakeCtrlKind), mk(fakeMidiKind),
		mk(fakeNoteExprKind)
}

// class is the fake's class.
func (f *fakePlugin) class() Class {
	kind := "Fx"
	if f.events {
		kind = "Instrument|Synth"
	}
	return Class{Name: "Fake", Category: "Audio Module Class", SubCategories: kind}
}

// fakeSynth is a fake instrument: notes in, a stereo output, an aux
// output of one channel, a sidechain in, the pedal and the mod wheel
// mapped on every channel and the bend on channel 0.
func fakeSynth() *fakePlugin {
	f := &fakePlugin{events: true,
		ins:  []fakeBus{{name: "Sidechain", channels: 2, aux: true}},
		outs: []fakeBus{{name: "Main", channels: 2}, {name: "Kick", channels: 1, aux: true}},
		midi: map[[2]int]uint32{{0, ccPitchBend}: 900}}
	for c := range channels {
		f.midi[[2]int{c, ccSustain}] = uint32(1000 + c)
		f.midi[[2]int{c, ccModWheel}] = uint32(2000 + c)
	}
	return f
}

// last returns the last block the fake processed.
func (f *fakePlugin) last(t *testing.T) fakeBlock {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.blocks) == 0 {
		t.Fatal("the fake processed no block")
	}
	return f.blocks[len(f.blocks)-1]
}

// Fields of an event as the fake reads them.
func (e vstEvent) channel() int16 { return int16(binary.LittleEndian.Uint16(e.data[0:])) }
func (e vstEvent) pitch() int16   { return int16(binary.LittleEndian.Uint16(e.data[2:])) }
func (e vstEvent) f32(at int) float32 {
	return math.Float32frombits(binary.LittleEndian.Uint32(e.data[at:]))
}
func (e vstEvent) i32(at int) int32 { return int32(binary.LittleEndian.Uint32(e.data[at:])) }
