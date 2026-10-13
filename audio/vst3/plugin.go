package vst3

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"sync/atomic"
	"time"
	"unicode/utf16"
	"unsafe"
)

// The structures processing passes, laid out as the interfaces' are.

type processSetup struct {
	mode, sampleSize, maxBlock int32
	rate                       float64
}

type busBuffers struct {
	channels int32
	silence  uint64
	buffers  uintptr
}

type processData struct {
	mode, sampleSize, frames, numInputs, numOutputs int32
	inputs, outputs                                 uintptr
	inParams, outParams                             uintptr
	inEvents, outEvents                             uintptr
	context                                         uintptr
}

type processContext struct {
	state                             uint32
	rate                              float64
	projectTime, systemTime, contTime int64
	musicTime, barPosition            float64
	cycleStart, cycleEnd, tempo       float64
	sigNumerator, sigDenominator      int32
	chord                             [4]byte
	smpteSubframes                    int32
	frameRate                         [2]uint32
	samplesToNextClock                int32
}

type busInfo struct {
	mediaType, direction, channels int32
	name                           [128]uint16
	busType                        int32
	flags                          uint32
}

type paramInfo struct {
	id                  uint32
	title, short, units [128]uint16
	steps               int32
	def                 float64
	unit                int32
	flags               int32
}

const (
	modeRealtime = 0
	modeOffline  = 2
	sample32     = 0
	mediaAudio   = 0
	mediaEvent   = 1
	dirInput     = 0
	dirOutput    = 1
	busMain      = 0
	stereo       = 0x3
	mono         = 1 << 19

	ctxPlaying   = 1 << 1
	ctxTempo     = 1 << 10
	ctxTimeSig   = 1 << 13
	ctxContTime  = 1 << 17
	ctxMusicTime = 1 << 9

	restartParamValues = 1 << 2
	restartLatency     = 1 << 3

	paramCanAutomate = 1 << 0
	paramReadOnly    = 1 << 1
	paramHidden      = 1 << 4
	paramBypass      = 1 << 16

	infiniteTail = math.MaxUint32
)

// Config is how a plugin runs.
type Config struct {
	// Rate is the sample rate, in frames a second.
	Rate int
	// Block is the most frames processed at once; 0 means 1024.
	Block int
	// Channels is how many channels the main buses carry, in and out:
	// 1 or 2; 0 means 2. Process, of interleaved frames, takes 2.
	Channels int
	// Offline runs the plugin as for an export, faster or slower than
	// time, as it may take more care in.
	Offline bool
}

// Param is one of a plugin's parameters.
type Param struct {
	ID           uint32
	Title, Units string
	// Steps is how many steps it has, 0 for continuous, and Default its
	// value at start, from 0 to 1.
	Steps   int
	Default float64
	// Bypass says it is the plugin's bypass.
	Bypass bool
	// Automatable says a host may change it as the sound plays,
	// ReadOnly that the plugin reports it and a host never sets it, as
	// a meter's, and Hidden that a host keeps it off its own views.
	Automatable, ReadOnly, Hidden bool
}

// Bus is one of a plugin's audio buses: its main input or output, or
// one beside them, as a sidechain input or an output for each drum.
type Bus struct {
	Name     string
	Channels int
	// Main says it is the main input or output; the rest are aux
	// buses.
	Main bool
}

// bus is a bus as processing feeds it: its channels' buffers, and their
// pointers as the plugin reads them.
type bus struct {
	Bus
	chans [][]float32
	ptrs  []uintptr
}

// A Plugin is an effect or an instrument, made from a module, that
// sound runs through or comes from. Process and ProcessBlock run on the
// audio's goroutine; the rest is safe from any.
type Plugin struct {
	Class Class
	cfg   Config

	// comp is the component, proc its processor, and ctrl its
	// controller, the component itself where it is both.
	comp, proc, ctrl uintptr
	compCP, ctrlCP   uintptr
	single           bool
	handler          *handler

	// ins and outs are the audio buses, the main ones first where the
	// plugin has them; inBufs and outBufs are their buffers as the
	// plugin reads them.
	ins, outs       []bus
	inBufs, outBufs []busBuffers
	// mainIn says ins[0] is the main input, where an instrument may
	// have only aux inputs, or none.
	mainIn bool
	data   processData
	ctx    processContext

	inChanges  *paramChanges
	outChanges *paramChanges
	// events are the notes going in, for a plugin with an event input;
	// notes says it has one.
	events *eventList
	notes  bool
	// keys translates notes to what the plugin takes.
	keys keyboard
	// procArgs are the arguments of the call that processes, the same
	// each time, kept with the plugin so the call makes nothing.
	procArgs [2]uintptr
	latency  atomic.Int32
	tail     atomic.Int64
	restarts atomic.Int32

	// edits are the parameters set, by the editor or Set, waiting for
	// the processor, and outputs the processor's own and the host's,
	// waiting for the controller: neither side waits for the other.
	edits     *paramBox
	outputs   *paramBox
	editsMade atomic.Uint64
	// values holds each parameter's value as last known, by its slot in
	// edits: set, edited, or reported by the processor.
	values []atomic.Uint64
	// gestures counts the editor's gestures under way.
	gestures atomic.Int32

	closed bool
	// active says the plugin is set up to process, as New leaves it.
	active bool
	// ed is the editor, open, on the plugins' thread.
	ed         *editor
	editorOpen atomic.Bool
	// onEdit, set by a test, hears each change of the editor's.
	onEdit func(id uint32, v float64)
}

// New makes an effect or an instrument of class c, set up as cfg says,
// ready to process.
func (m *Module) New(c Class, cfg Config) (*Plugin, error) {
	return start(c, cfg, func(p *Plugin) error { return p.create(m) })
}

// start makes a plugin of class c, set up as cfg says, with build, which
// runs on the plugins' thread.
func start(c Class, cfg Config, build func(p *Plugin) error) (*Plugin, error) {
	if cfg.Block <= 0 {
		cfg.Block = 1024
	}
	if cfg.Channels <= 0 {
		cfg.Channels = 2
	}
	if cfg.Rate <= 0 {
		return nil, errors.New("vst3: no sample rate")
	}
	if cfg.Channels > 2 {
		return nil, fmt.Errorf("vst3: %d channels, of the 1 or 2 a plugin runs in", cfg.Channels)
	}
	p := &Plugin{Class: c, cfg: cfg, edits: newParamBox(nil), outputs: newParamBox(nil)}
	var err error
	onUI(func() { err = build(p) })
	if err != nil {
		p.Close()
		return nil, err
	}
	tick(p, p.onTick)
	return p, nil
}

func (p *Plugin) create(m *Module) error {
	comp := m.create(p.Class.ID, iidComponent)
	if comp == 0 {
		return fmt.Errorf("vst3: %s: the module made no component", p.Class.Name)
	}
	return p.build(comp, m)
}

// build sets up the component comp, of module m, with its controller,
// its buses and its parameters, and starts it. A component that is its
// own controller needs no module.
func (p *Plugin) build(comp uintptr, m *Module) error {
	p.comp = comp
	if r := result(call(p.comp, mInitialize, theHost())); r != resultOK {
		release(p.comp)
		p.comp = 0
		return fmt.Errorf("vst3: %s: initialize failed (%d)", p.Class.Name, r)
	}
	p.proc = query(p.comp, iidAudioProcessor)
	if p.proc == 0 {
		return fmt.Errorf("vst3: %s: no audio processor", p.Class.Name)
	}
	// The controller: the component itself, or one of its own class.
	if ctrl := query(p.comp, iidEditController); ctrl != 0 {
		p.ctrl, p.single = ctrl, true
	} else if m != nil {
		var cid uid
		if result(call(p.comp, mGetControllerClassID, uintptr(unsafe.Pointer(&cid)))) == resultOK && cid != (uid{}) {
			if ctrl := m.create(cid, iidEditController); ctrl != 0 {
				if result(call(ctrl, mInitialize, theHost())) == resultOK {
					p.ctrl = ctrl
				} else {
					release(ctrl)
				}
			}
		}
	}
	if p.ctrl != 0 {
		p.handler = newHandler(p)
		call(p.ctrl, mSetComponentHandler, uintptr(unsafe.Pointer(&p.handler.object)))
		if !p.single {
			p.compCP, p.ctrlCP = query(p.comp, iidConnectionPoint), query(p.ctrl, iidConnectionPoint)
			if p.compCP != 0 && p.ctrlCP != 0 {
				call(p.compCP, mConnect, p.ctrlCP)
				call(p.ctrlCP, mConnect, p.compCP)
			}
			// The controller learns the component's state.
			if st, err := p.componentState(); err == nil {
				_, sp := newStream(st)
				call(p.ctrl, mSetComponentState, sp)
				drop(sp)
			}
		}
	}
	var ids []uint32
	if p.ctrl != 0 {
		// A slot for each parameter, each way, made now, so that
		// processing makes none.
		n := int(i32(call(p.ctrl, mGetParameterCount)))
		ids = make([]uint32, 0, n)
		for i := range n {
			var info paramInfo
			if result(call(p.ctrl, mGetParameterInfo, uintptr(i), uintptr(unsafe.Pointer(&info)))) == resultOK {
				ids = append(ids, info.id)
			}
		}
		p.edits, p.outputs = newParamBox(ids), newParamBox(ids)
		p.values = make([]atomic.Uint64, len(p.edits.slots))
		p.readValues()
	}
	// Room for a change of every parameter in a block, up to a bound.
	p.inChanges, p.data.inParams = newChanges(max(64, min(len(ids), 1024)))
	p.outChanges, p.data.outParams = newChanges(64)
	p.events, p.data.inEvents = newEventList()
	p.data.outEvents = theEvents()
	if err := p.setupBuses(); err != nil {
		return err
	}
	p.keys.learn(p)
	return p.activate()
}

// readValues reads each parameter's value from the controller, as it
// starts and as its state is set. It runs on the plugins' thread.
func (p *Plugin) readValues() {
	for i := range p.edits.slots {
		v := callFloatOut(p.ctrl, mGetParamNormalized, uintptr(p.edits.slots[i].id))
		p.values[i].Store(math.Float64bits(v))
	}
}

// setupBuses asks for the main buses in as many channels as cfg says,
// and the aux buses as the plugin has them, turns every bus on, and
// makes the buffers for each.
func (p *Plugin) setupBuses() error {
	count := func(media, dir int) int { return int(i32(call(p.comp, mGetBusCount, uintptr(media), uintptr(dir)))) }
	nIn, nOut := count(mediaAudio, dirInput), count(mediaAudio, dirOutput)
	if nOut == 0 {
		return fmt.Errorf("vst3: %s: no audio out", p.Class.Name)
	}
	want := uint64(stereo)
	if p.cfg.Channels == 1 {
		want = mono
	}
	infos := func(dir, n int) []busInfo {
		out := make([]busInfo, n)
		for i := range n {
			call(p.comp, mGetBusInfo, mediaAudio, uintptr(dir), uintptr(i), uintptr(unsafe.Pointer(&out[i])))
		}
		return out
	}
	inInfo, outInfo := infos(dirInput, nIn), infos(dirOutput, nOut)
	p.mainIn = nIn > 0 && inInfo[0].busType == busMain
	arr := func(dir int, info []busInfo, main bool) []uint64 {
		a := make([]uint64, max(len(info), 1))
		for i := range info {
			call(p.proc, mGetBusArrangement, uintptr(dir), uintptr(i), uintptr(unsafe.Pointer(&a[i])))
		}
		if main {
			a[0] = want
		}
		return a
	}
	ain, aout := arr(dirInput, inInfo, p.mainIn), arr(dirOutput, outInfo, true)
	call(p.proc, mSetBusArrangements, uintptr(unsafe.Pointer(&ain[0])), uintptr(nIn),
		uintptr(unsafe.Pointer(&aout[0])), uintptr(nOut))
	// What the plugin took: it may keep its own arrangement.
	buses := func(dir int, info []busInfo) []bus {
		out := make([]bus, len(info))
		for i := range info {
			var a uint64
			call(p.proc, mGetBusArrangement, uintptr(dir), uintptr(i), uintptr(unsafe.Pointer(&a)))
			ch := bits.OnesCount64(a)
			if ch == 0 {
				ch = int(info[i].channels)
			}
			b := &out[i]
			b.Name, b.Channels, b.Main = utf16String(info[i].name[:]), ch, info[i].busType == busMain
			b.chans = make([][]float32, ch)
			b.ptrs = make([]uintptr, ch+1)
			for c := range ch {
				b.chans[c] = make([]float32, p.cfg.Block)
				b.ptrs[c] = uintptr(unsafe.Pointer(&b.chans[c][0]))
			}
			call(p.comp, mActivateBus, mediaAudio, uintptr(dir), uintptr(i), 1)
		}
		return out
	}
	p.ins, p.outs = buses(dirInput, inInfo), buses(dirOutput, outInfo)
	if p.outs[0].Channels == 0 {
		return fmt.Errorf("vst3: %s: its main output has no channels", p.Class.Name)
	}
	bufs := func(bs []bus) []busBuffers {
		out := make([]busBuffers, max(len(bs), 1))
		for i := range bs {
			out[i] = busBuffers{channels: int32(bs[i].Channels), buffers: uintptr(unsafe.Pointer(&bs[i].ptrs[0]))}
		}
		return out
	}
	p.inBufs, p.outBufs = bufs(p.ins), bufs(p.outs)
	// The notes, on the first event input.
	p.notes = count(mediaEvent, dirInput) > 0
	if p.notes {
		call(p.comp, mActivateBus, mediaEvent, dirInput, 0, 1)
	}
	p.data.sampleSize = sample32
	p.data.numInputs, p.data.numOutputs = int32(nIn), int32(nOut)
	p.data.inputs = 0
	if nIn > 0 {
		p.data.inputs = uintptr(unsafe.Pointer(&p.inBufs[0]))
	}
	p.data.outputs = uintptr(unsafe.Pointer(&p.outBufs[0]))
	p.data.context = uintptr(unsafe.Pointer(&p.ctx))
	p.procArgs = [2]uintptr{p.proc, uintptr(unsafe.Pointer(&p.data))}
	p.ctx = processContext{state: ctxTempo | ctxTimeSig | ctxContTime | ctxMusicTime, rate: float64(p.cfg.Rate),
		tempo: 120, sigNumerator: 4, sigDenominator: 4}
	return nil
}

// Buses returns the plugin's audio buses, in and out, the main ones
// first where it has them: an instrument's inputs may be aux buses
// alone, or none.
func (p *Plugin) Buses() (in, out []Bus) {
	for _, b := range p.ins {
		in = append(in, b.Bus)
	}
	for _, b := range p.outs {
		out = append(out, b.Bus)
	}
	return in, out
}

// Notes says the plugin takes notes: it has an event input, as an
// instrument has.
func (p *Plugin) Notes() bool { return p.notes }

// activate sets processing up, as cfg says, and starts it.
func (p *Plugin) activate() error {
	mode := int32(modeRealtime)
	if p.cfg.Offline {
		mode = modeOffline
	}
	p.data.mode = mode
	setup := processSetup{mode: mode, sampleSize: sample32, maxBlock: int32(p.cfg.Block), rate: float64(p.cfg.Rate)}
	if result(call(p.proc, mCanProcessSampleSize, sample32)) != resultOK {
		return fmt.Errorf("vst3: %s: processes no 32-bit floats", p.Class.Name)
	}
	if r := result(call(p.proc, mSetupProcessing, uintptr(unsafe.Pointer(&setup)))); r != resultOK {
		return fmt.Errorf("vst3: %s: setupProcessing failed (%d)", p.Class.Name, r)
	}
	if r := result(call(p.comp, mSetActive, 1)); r != resultOK {
		return fmt.Errorf("vst3: %s: setActive failed (%d)", p.Class.Name, r)
	}
	p.latency.Store(int32(call(p.proc, mGetLatencySamples)))
	p.readTail()
	call(p.proc, mSetProcessing, 1)
	p.active = true
	return nil
}

// deactivate stops processing.
func (p *Plugin) deactivate() {
	call(p.proc, mSetProcessing, 0)
	call(p.comp, mSetActive, 0)
	p.active = false
}

// SetActive starts or stops the plugin's processing. A plugin stopped
// takes no time of the computer's, nor sound; started again, it starts
// from silence. Process must not run meanwhile.
func (p *Plugin) SetActive(on bool) error {
	var err error
	onUI(func() {
		switch {
		case on && !p.active:
			err = p.activate()
		case !on && p.active:
			p.deactivate()
		}
	})
	return err
}

// Configure sets the plugin up to run as cfg says, keeping its state:
// another rate, block or channel count. Process must not run meanwhile.
func (p *Plugin) Configure(cfg Config) error {
	if cfg.Block <= 0 {
		cfg.Block = 1024
	}
	if cfg.Channels <= 0 {
		cfg.Channels = 2
	}
	if cfg.Rate <= 0 || cfg.Channels > 2 {
		return fmt.Errorf("vst3: %s: no plugin runs at %d frames a second in %d channels", p.Class.Name, cfg.Rate, cfg.Channels)
	}
	if cfg == p.cfg || p.closed {
		return nil
	}
	var err error
	onUI(func() {
		if p.active {
			p.deactivate()
		}
		p.cfg = cfg
		if err = p.setupBuses(); err == nil {
			err = p.activate()
		}
	})
	return err
}

// Latency is how many frames late the plugin's sound comes out.
func (p *Plugin) Latency() int {
	if p.restarts.Load()&restartLatency != 0 {
		onUI(func() {
			p.restarts.And(^int32(restartLatency))
			p.latency.Store(int32(call(p.proc, mGetLatencySamples)))
		})
	}
	return int(p.latency.Load())
}

// Tail is how many frames of sound come out after the sound going in
// stops, as a reverb's ringing, as the plugin last said: the package
// asks it every few milliseconds. A tail without end is
// [math.MaxInt32].
func (p *Plugin) Tail() int { return int(p.tail.Load()) }

// readTail asks the plugin its tail, on the plugins' thread.
func (p *Plugin) readTail() {
	t := uint32(call(p.proc, mGetTailSamples))
	if t == infiniteTail || t > math.MaxInt32 {
		p.tail.Store(math.MaxInt32)
		return
	}
	p.tail.Store(int64(t))
}

// Reset clears what the plugin holds of the sound before, as its
// filters' and delays' memories, for a jump in the sound.
func (p *Plugin) Reset() {
	onUI(func() {
		if p.active {
			p.deactivate()
			_ = p.activate()
		}
	})
	p.ctx.projectTime = 0
	p.keys.reset()
}

// resetNow clears what the plugin holds of the sound before, on the
// audio's goroutine, as VST3 lets processing be stopped and started
// there: the notes sounding end in the next block, and the pedals lift.
func (p *Plugin) resetNow() {
	if p.closed || !p.active {
		return
	}
	call(p.proc, mSetProcessing, 0)
	call(p.proc, mSetProcessing, 1)
	p.keys.reset()
}

// SetPosition tells the plugin where in the song the sound processed next is,
// in frames.
func (p *Plugin) SetPosition(frame int64) { p.ctx.projectTime = frame }

// Process runs the stereo frames, interleaved, through the plugin, in
// place: through its main buses, and into each aux input too, as a
// sidechain keys on the sound it changes.
func (p *Plugin) Process(frames []float32) {
	if p.closed || !p.active {
		return
	}
	n := len(frames) / 2
	for at := 0; at < n; {
		k := min(n-at, p.cfg.Block)
		p.block(frames[2*at:2*(at+k)], k)
		at += k
	}
}

// block runs k interleaved stereo frames through the plugin.
func (p *Plugin) block(frames []float32, k int) {
	for i := range p.ins {
		for c, ch := range p.ins[i].chans {
			for f := range k {
				ch[f] = frames[2*f+c%2]
			}
		}
	}
	p.inChanges.clear()
	p.events.n = 0
	p.takeEdits()
	p.keys.lift(p)
	p.ctx.state |= ctxPlaying
	p.ctx.musicTime = float64(p.ctx.projectTime) / float64(p.cfg.Rate) * p.ctx.tempo / 60
	p.ctx.barPosition = float64(int(p.ctx.musicTime/4) * 4)
	p.run(k)
	p.ctx.projectTime += int64(k)
	out := p.outs[0].chans
	for f := range k {
		frames[2*f], frames[2*f+1] = out[0][f], out[1%len(out)][f]
	}
}

// takeEdits passes the parameters set since the last block on to the
// processor, at its first frame.
func (p *Plugin) takeEdits() {
	if !p.edits.look() {
		return
	}
	for i := range p.edits.slots {
		if id, v, ok := p.edits.take(i); ok {
			p.values[i].Store(math.Float64bits(v))
			if q := p.inChanges.queue(id); q >= 0 {
				p.inChanges.queues[q].add(0, v)
			}
		}
	}
}

// run processes k frames, the buffers, changes and events filled, and
// passes the processor's changes on to the controller.
func (p *Plugin) run(k int) {
	p.outChanges.clear()
	p.data.frames = int32(k)
	callN(method(p.proc, mProcess), p.procArgs[:])
	p.ctx.contTime += int64(k)
	if p.outChanges.n > 0 && p.ctrl != 0 {
		for _, q := range p.outChanges.queues[:p.outChanges.n] {
			if q.n > 0 {
				v := q.points[q.n-1].value
				p.outputs.put(q.id, v)
				if i, ok := p.edits.at[q.id]; ok {
					p.values[i].Store(math.Float64bits(v))
				}
			}
		}
	}
}

// Value returns parameter id's value as last known, from 0 to 1: as set,
// edited, or reported by the processor, as a meter's. It is 0 for a
// parameter the plugin does not list.
func (p *Plugin) Value(id uint32) float64 {
	if i, ok := p.edits.at[id]; ok {
		return math.Float64frombits(p.values[i].Load())
	}
	return 0
}

// Values returns every parameter's value now, by ID, from 0 to 1, as
// the controller has them.
func (p *Plugin) Values() map[uint32]float64 {
	out := make(map[uint32]float64, len(p.edits.slots))
	onUI(func() {
		if p.ctrl == 0 {
			return
		}
		for i := range p.edits.slots {
			id := p.edits.slots[i].id
			out[id] = callFloatOut(p.ctrl, mGetParamNormalized, uintptr(id))
		}
	})
	return out
}

// Flush passes the changes waiting for the processor on, as a plugin
// not processing takes them, so its state holds them: with a moment of
// silence, as not every plugin takes them without sound, as VST3 allows.
// It must not run alongside Process.
func (p *Plugin) Flush() {
	if p.closed || !p.active {
		return
	}
	if !p.edits.waits() {
		return
	}
	p.Process(make([]float32, 2*32))
}

// edited takes a change of the editor's, for the processor.
func (p *Plugin) edited(id uint32, v float64) {
	if p.onEdit != nil {
		p.onEdit(id, v)
	}
	p.edits.put(id, v)
	p.editsMade.Add(1)
}

// Edits counts the changes made in the plugin's editor, so a host knows
// its state changed: each parameter it changes, and each time it says
// its state changed otherwise, as a sample loaded.
func (p *Plugin) Edits() uint64 { return p.editsMade.Load() }

// Editing reports whether a gesture in the editor is under way, as a
// knob held: a host that keeps the editor's changes keeps them once it
// ends.
func (p *Plugin) Editing() bool { return p.gestures.Load() > 0 }

// restart takes a plugin's request to be restarted, for what changed.
func (p *Plugin) restart(flags int32) {
	p.restarts.Or(flags)
	if flags&restartParamValues != 0 {
		p.editsMade.Add(1)
	}
}

// onTick passes the processor's changes to the controller, and asks the
// plugin its tail, on the plugins' thread.
func (p *Plugin) onTick() {
	if p.active {
		p.readTail()
	}
	if !p.outputs.look() {
		return
	}
	for i := range p.outputs.slots {
		if id, v, ok := p.outputs.take(i); ok {
			callFloat(p.ctrl, mSetParamNormalized, uintptr(id), v)
		}
	}
}

// Params returns the plugin's parameters.
func (p *Plugin) Params() []Param {
	var out []Param
	onUI(func() {
		if p.ctrl == 0 {
			return
		}
		n := int(i32(call(p.ctrl, mGetParameterCount)))
		for i := range n {
			var info paramInfo
			if result(call(p.ctrl, mGetParameterInfo, uintptr(i), uintptr(unsafe.Pointer(&info)))) != resultOK {
				continue
			}
			out = append(out, Param{ID: info.id, Title: utf16String(info.title[:]), Units: utf16String(info.units[:]),
				Steps: int(info.steps), Default: info.def, Bypass: info.flags&paramBypass != 0,
				Automatable: info.flags&paramCanAutomate != 0, ReadOnly: info.flags&paramReadOnly != 0,
				Hidden: info.flags&paramHidden != 0})
		}
	})
	return out
}

// Text returns parameter id's value v, from 0 to 1, as the plugin
// writes it to show, as "-3.0 dB"; empty where it writes none.
func (p *Plugin) Text(id uint32, v float64) string {
	var s [128]uint16
	onUI(func() {
		if p.ctrl != 0 && !p.closed {
			callFloatArg(p.ctrl, mGetParamStringByValue, uintptr(id), v, uintptr(unsafe.Pointer(&s[0])))
		}
	})
	return utf16String(s[:])
}

// Set sets parameter id to v, from 0 to 1, in the controller and the
// processor. A parameter the controller does not list is not set.
func (p *Plugin) Set(id uint32, v float64) {
	if p.ctrl != 0 {
		onUI(func() { callFloat(p.ctrl, mSetParamNormalized, uintptr(id), v) })
	}
	p.edits.put(id, v)
}

func utf16String(u []uint16) string {
	for i, c := range u {
		if c == 0 {
			u = u[:i]
			break
		}
	}
	return string(utf16.Decode(u))
}

// componentState returns the component's state. It runs on the plugins'
// thread.
func (p *Plugin) componentState() ([]byte, error) {
	s, sp := newStream(nil)
	defer drop(sp)
	if r := result(call(p.comp, mGetState, sp)); r != resultOK {
		return nil, fmt.Errorf("vst3: %s: getState failed (%d)", p.Class.Name, r)
	}
	return s.b, nil
}

// stateMagic starts a plugin's state as State gives it.
const stateMagic = "gVS3"

// State returns the plugin's state, its component's and its
// controller's, to set again with SetState.
func (p *Plugin) State() ([]byte, error) {
	var comp, ctrl []byte
	var err error
	onUI(func() {
		if comp, err = p.componentState(); err != nil {
			return
		}
		if p.ctrl != 0 && !p.single {
			s, sp := newStream(nil)
			if result(call(p.ctrl, mCtrlGetState, sp)) == resultOK {
				ctrl = s.b
			}
			drop(sp)
		}
	})
	if err != nil {
		return nil, err
	}
	out := []byte(stateMagic)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(comp)))
	out = append(out, comp...)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(ctrl)))
	return append(out, ctrl...), nil
}

// splitState returns the component's and the controller's parts of a
// state as State gives it.
func splitState(name string, b []byte) (comp, ctrl []byte, err error) {
	bad := fmt.Errorf("vst3: %s: the state is no state of a plugin", name)
	if len(b) < 8 || string(b[:4]) != stateMagic {
		return nil, nil, bad
	}
	b = b[4:]
	n := int(binary.LittleEndian.Uint32(b))
	if len(b) < 4+n+4 {
		return nil, nil, bad
	}
	comp = b[4 : 4+n]
	b = b[4+n:]
	m := int(binary.LittleEndian.Uint32(b))
	if len(b) < 4+m {
		return nil, nil, bad
	}
	return comp, b[4 : 4+m], nil
}

// SetState sets the plugin's state, as State gave it. The changes the
// plugin tells as it takes the state are the host's own, so Edits
// counts none of them.
func (p *Plugin) SetState(b []byte) error {
	comp, ctrl, err := splitState(p.Class.Name, b)
	if err != nil {
		return err
	}
	onUI(func() {
		edits := p.editsMade.Load()
		defer p.editsMade.Store(edits)
		s, sp := newStream(append([]byte(nil), comp...))
		defer drop(sp)
		if r := result(call(p.comp, mSetState, sp)); r != resultOK {
			err = fmt.Errorf("vst3: %s: setState failed (%d)", p.Class.Name, r)
			return
		}
		if p.ctrl == 0 {
			return
		}
		if !p.single {
			s.pos = 0
			call(p.ctrl, mSetComponentState, sp)
			if len(ctrl) > 0 {
				_, cp := newStream(append([]byte(nil), ctrl...))
				call(p.ctrl, mCtrlSetState, cp)
				drop(cp)
			}
		}
		p.readValues()
	})
	return err
}

// Close stops the plugin and lets it go. Process must have returned.
func (p *Plugin) Close() {
	if p.closed {
		return
	}
	p.closed = true
	untick(p)
	onUI(func() {
		if p.ed != nil {
			p.ed.close()
		}
		if p.proc != 0 && p.active {
			p.deactivate()
		}
		if p.compCP != 0 && p.ctrlCP != 0 {
			call(p.compCP, mDisconnect, p.ctrlCP)
			call(p.ctrlCP, mDisconnect, p.compCP)
		}
		release(p.compCP)
		release(p.ctrlCP)
		if p.ctrl != 0 {
			call(p.ctrl, mSetComponentHandler, 0)
			if !p.single {
				call(p.ctrl, mTerminate)
			}
			release(p.ctrl)
		}
		release(p.proc)
		if p.comp != 0 {
			call(p.comp, mTerminate)
			release(p.comp)
		}
	})
	if p.handler != nil {
		p.handler.drop()
	}
	if p.inChanges != nil {
		p.inChanges.free()
	}
	if p.outChanges != nil {
		p.outChanges.free()
	}
	if p.events != nil {
		p.events.free()
	}
}

// Bypass returns the plugin's bypass parameter, where it has one: set to
// 1, the plugin passes the sound by itself, keeping its latency.
func (p *Plugin) Bypass() (id uint32, ok bool) {
	for _, q := range p.Params() {
		if q.Bypass {
			return q.ID, true
		}
	}
	return 0, false
}

// Prime runs a block of silence through the plugin, and waits up to
// wait for it to tell a change of its latency, as some plugins tell
// only once they process; then it resets the plugin. A host that must
// know the latency from the start, as an offline render does, primes.
func (p *Plugin) Prime(wait time.Duration) {
	p.Process(make([]float32, 2*p.cfg.Block))
	for end := time.Now().Add(wait); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		if p.restarts.Load()&restartLatency != 0 {
			break
		}
	}
	p.Reset()
}
