package vst3

import (
	"encoding/binary"
	"errors"
	"fmt"
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
	dirInput     = 0
	dirOutput    = 1
	stereo       = 0x3

	ctxPlaying   = 1 << 1
	ctxTempo     = 1 << 10
	ctxTimeSig   = 1 << 13
	ctxContTime  = 1 << 17
	ctxMusicTime = 1 << 9

	restartLatency = 1 << 3

	paramBypass = 1 << 16
)

// Config is how a plugin runs.
type Config struct {
	// Rate is the sample rate, in frames a second.
	Rate int
	// Block is the most frames processed at once; 0 means 1024.
	Block int
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
}

// A Plugin is an audio effect, made from a module, that sound runs
// through in stereo. Process runs on the audio's goroutine; the rest is
// safe from any.
type Plugin struct {
	Class Class
	cfg   Config

	// comp is the component, proc its processor, and ctrl its
	// controller, the component itself where it is both.
	comp, proc, ctrl uintptr
	compCP, ctrlCP   uintptr
	single           bool
	handler          *handler

	ins, outs     []busBuffers
	inPtr, outPtr []uintptr
	data          processData
	ctx           processContext
	inChanges     *paramChanges
	outChanges    *paramChanges
	chans         [][]float32
	// procArgs are the arguments of the call that processes, the same
	// each time, kept with the plugin so the call makes nothing.
	procArgs [2]uintptr
	latency  atomic.Int32
	restarts atomic.Int32

	// edits are the parameters set, by the editor or Set, waiting for
	// the processor, and outputs the processor's own, waiting for the
	// controller: neither side waits for the other.
	edits     *paramBox
	outputs   *paramBox
	editsMade atomic.Uint64

	closed bool
	// active says the plugin is set up to process, as New leaves it.
	active bool
	// ed is the editor, open, on the plugins' thread.
	ed         *editor
	editorOpen atomic.Bool
	// onEdit, set by a test, hears each change of the editor's.
	onEdit func(id uint32, v float64)
}

// New makes an effect of class c, set up as cfg says, ready to process.
func (m *Module) New(c Class, cfg Config) (*Plugin, error) {
	if cfg.Block <= 0 {
		cfg.Block = 1024
	}
	if cfg.Rate <= 0 {
		return nil, errors.New("vst3: no sample rate")
	}
	p := &Plugin{Class: c, cfg: cfg, edits: newParamBox(nil), outputs: newParamBox(nil)}
	var err error
	onUI(func() { err = p.create(m) })
	if err != nil {
		p.Close()
		return nil, err
	}
	tick(p, p.flushOutputs)
	return p, nil
}

func (p *Plugin) create(m *Module) error {
	p.comp = m.create(p.Class.ID, iidComponent)
	if p.comp == 0 {
		return fmt.Errorf("vst3: %s: the module made no component", p.Class.Name)
	}
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
	} else {
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
		h := &handler{p: p}
		keep(h, handlerKind, false)
		p.handler = h
		call(p.ctrl, mSetComponentHandler, uintptr(unsafe.Pointer(&h.object)))
		if !p.single {
			p.compCP, p.ctrlCP = query(p.comp, iidConnectionPoint), query(p.ctrl, iidConnectionPoint)
			if p.compCP != 0 && p.ctrlCP != 0 {
				call(p.compCP, mConnect, p.ctrlCP)
				call(p.ctrlCP, mConnect, p.compCP)
			}
			// The controller learns the component's state.
			if st, err := p.componentState(); err == nil {
				s, sp := newStream(st)
				call(p.ctrl, mSetComponentState, sp)
				_ = s
				drop(sp)
			}
		}
	}
	if p.ctrl != 0 {
		// A slot for each parameter, each way, made now, so that
		// processing makes none.
		n := int(i32(call(p.ctrl, mGetParameterCount)))
		ids := make([]uint32, 0, n)
		for i := range n {
			var info paramInfo
			if result(call(p.ctrl, mGetParameterInfo, uintptr(i), uintptr(unsafe.Pointer(&info)))) == resultOK {
				ids = append(ids, info.id)
			}
		}
		p.edits, p.outputs = newParamBox(ids), newParamBox(ids)
	}
	if err := p.setupBuses(); err != nil {
		return err
	}
	return p.activate()
}

// setupBuses asks for stereo in and out on the main buses, turns them
// on, and makes the buffers for every bus.
func (p *Plugin) setupBuses() error {
	count := func(dir int) int { return int(i32(call(p.comp, mGetBusCount, mediaAudio, uintptr(dir)))) }
	nIn, nOut := count(dirInput), count(dirOutput)
	if nOut == 0 {
		return fmt.Errorf("vst3: %s: no audio out", p.Class.Name)
	}
	arr := func(dir, n int) []uint64 {
		a := make([]uint64, max(n, 1))
		for i := range n {
			call(p.proc, mGetBusArrangement, uintptr(dir), uintptr(i), uintptr(unsafe.Pointer(&a[i])))
		}
		a[0] = stereo
		return a
	}
	ain, aout := arr(dirInput, nIn), arr(dirOutput, nOut)
	call(p.proc, mSetBusArrangements, uintptr(unsafe.Pointer(&ain[0])), uintptr(nIn),
		uintptr(unsafe.Pointer(&aout[0])), uintptr(nOut))
	buses := func(dir, n int) ([]busBuffers, []uintptr) {
		bs := make([]busBuffers, max(n, 1))
		ptrs := make([]uintptr, 0, 2*n+1)
		for i := range n {
			var info busInfo
			call(p.comp, mGetBusInfo, mediaAudio, uintptr(dir), uintptr(i), uintptr(unsafe.Pointer(&info)))
			ch := int(info.channels)
			if i == 0 {
				ch = 2
				call(p.comp, mActivateBus, mediaAudio, uintptr(dir), 0, 1)
			}
			bs[i].channels = int32(ch)
			start := len(ptrs)
			for range ch {
				buf := make([]float32, p.cfg.Block)
				p.chans = append(p.chans, buf)
				ptrs = append(ptrs, uintptr(unsafe.Pointer(&buf[0])))
			}
			bs[i].buffers = uintptr(start) // fixed below, once ptrs stops moving
		}
		ptrs = append(ptrs, 0)
		for i := range n {
			bs[i].buffers = uintptr(unsafe.Pointer(&ptrs[bs[i].buffers]))
		}
		return bs, ptrs
	}
	if nIn > 0 {
		p.ins, p.inPtr = buses(dirInput, nIn)
	}
	p.outs, p.outPtr = buses(dirOutput, nOut)
	var main busInfo
	call(p.comp, mGetBusInfo, mediaAudio, dirOutput, 0, uintptr(unsafe.Pointer(&main)))
	if main.channels != 2 {
		var a uint64
		call(p.proc, mGetBusArrangement, dirOutput, 0, uintptr(unsafe.Pointer(&a)))
		if a != stereo {
			return fmt.Errorf("vst3: %s: plays no stereo", p.Class.Name)
		}
	}
	p.inChanges, p.data.inParams = newChanges(64)
	p.outChanges, p.data.outParams = newChanges(64)
	p.data.inEvents, p.data.outEvents = theEvents(), theEvents()
	p.data.sampleSize = sample32
	p.data.numInputs, p.data.numOutputs = int32(nIn), int32(nOut)
	if nIn > 0 {
		p.data.inputs = uintptr(unsafe.Pointer(&p.ins[0]))
	}
	p.data.outputs = uintptr(unsafe.Pointer(&p.outs[0]))
	p.data.context = uintptr(unsafe.Pointer(&p.ctx))
	p.procArgs = [2]uintptr{p.proc, uintptr(unsafe.Pointer(&p.data))}
	p.ctx = processContext{state: ctxTempo | ctxTimeSig | ctxContTime | ctxMusicTime, rate: float64(p.cfg.Rate),
		tempo: 120, sigNumerator: 4, sigDenominator: 4}
	return nil
}

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
}

// SetPosition tells the plugin where in the song the sound processed next is,
// in frames.
func (p *Plugin) SetPosition(frame int64) { p.ctx.projectTime = frame }

// Process runs the stereo frames, interleaved, through the plugin, in
// place.
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

// block runs k frames through the plugin.
func (p *Plugin) block(frames []float32, k int) {
	// The main bus's channels: in first, out after, as setupBuses made
	// them.
	var inL, inR []float32
	if len(p.ins) > 0 {
		inL, inR = p.chans[0], p.chans[1]
	}
	first := 0
	for _, b := range p.ins {
		first += int(b.channels)
	}
	outL, outR := p.chans[first], p.chans[first+1]
	if inL == nil {
		inL, inR = outL, outR
	}
	for i := range k {
		inL[i], inR[i] = frames[2*i], frames[2*i+1]
	}
	p.inChanges.clear()
	if p.edits.look() {
		for i := range p.edits.slots {
			if id, v, ok := p.edits.take(i); ok {
				if q := p.inChanges.queue(id); q >= 0 {
					p.inChanges.queues[q].add(0, v)
				}
			}
		}
	}
	p.outChanges.clear()
	p.data.frames = int32(k)
	p.ctx.state |= ctxPlaying
	p.ctx.musicTime = float64(p.ctx.projectTime) / float64(p.cfg.Rate) * p.ctx.tempo / 60
	p.ctx.barPosition = float64(int(p.ctx.musicTime/4) * 4)
	callN(method(p.proc, mProcess), p.procArgs[:])
	p.ctx.projectTime += int64(k)
	p.ctx.contTime += int64(k)
	for i := range k {
		frames[2*i], frames[2*i+1] = outL[i], outR[i]
	}
	if p.outChanges.n > 0 && p.ctrl != 0 {
		for _, q := range p.outChanges.queues[:p.outChanges.n] {
			if q.n > 0 {
				p.outputs.put(q.id, q.points[q.n-1].value)
			}
		}
	}
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
// its state changed.
func (p *Plugin) Edits() uint64 { return p.editsMade.Load() }

// restart takes a plugin's request to be restarted, for what changed.
func (p *Plugin) restart(flags int32) { p.restarts.Or(flags) }

// flushOutputs passes the processor's changes to the controller, on the
// plugins' thread.
func (p *Plugin) flushOutputs() {
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
				Steps: int(info.steps), Default: info.def, Bypass: info.flags&paramBypass != 0})
		}
	})
	return out
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

// SetState sets the plugin's state, as State gave it.
func (p *Plugin) SetState(b []byte) error {
	bad := fmt.Errorf("vst3: %s: the state is no state of a plugin", p.Class.Name)
	if len(b) < 8 || string(b[:4]) != stateMagic {
		return bad
	}
	b = b[4:]
	n := int(binary.LittleEndian.Uint32(b))
	if len(b) < 4+n+4 {
		return bad
	}
	comp := b[4 : 4+n]
	b = b[4+n:]
	m := int(binary.LittleEndian.Uint32(b))
	if len(b) < 4+m {
		return bad
	}
	ctrl := b[4 : 4+m]
	var err error
	onUI(func() {
		s, sp := newStream(append([]byte(nil), comp...))
		defer drop(sp)
		if r := result(call(p.comp, mSetState, sp)); r != resultOK {
			err = fmt.Errorf("vst3: %s: setState failed (%d)", p.Class.Name, r)
			return
		}
		if p.ctrl == 0 || p.single {
			return
		}
		s.pos = 0
		call(p.ctrl, mSetComponentState, sp)
		if len(ctrl) > 0 {
			_, cp := newStream(append([]byte(nil), ctrl...))
			call(p.ctrl, mCtrlSetState, cp)
			drop(cp)
		}
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
		drop(uintptr(unsafe.Pointer(&p.handler.object)))
	}
	if p.inChanges != nil {
		p.inChanges.free()
		p.outChanges.free()
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
