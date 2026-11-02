//go:build ((darwin && !ios) || (linux && !android)) && (amd64 || arm64)

package audiounit

import (
	"errors"
	"fmt"
	"math"
	"math/bits"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// A Client plays through the default output device, and records the
// default input device where Config.Record says. Write, Read and Poll
// are one goroutine's; the rest are safe from any.
type Client struct {
	a   *api
	st  *rtState
	log func(format string, args ...any)
	// out and in are the rings' samples, and scratch the input's room,
	// held here while Core Audio's threads write and read them through
	// st.
	out, in, scratch []float32
	size             uint64
	// chans is how many channels the output plays, ins how many the
	// input records.
	chans, ins int
	// dev and inDev are the devices played and recorded, by their
	// AudioObjectID; inDev is 0 where nothing records. record says
	// Config.Record asked to.
	dev, inDev uint32
	record     bool
	// inDefault is the default input device as the client opened, 0
	// where there was none. refused is the input device refused for
	// recording at a rate other than the output's, 0 where none was: the
	// client listens to its rate, to open again once the two agree.
	inDefault, refused uint32
	rate               int
	// mu guards info, closed and the units: each call into a unit is
	// made with it held, after closed is read.
	mu     sync.Mutex
	info   Info
	units  []*unit
	closed bool
	// listened are the properties the listener listens to.
	listened []listening
	// latency is Info.Latency, buffer Info.Buffer, static the frames
	// from a frame written as a buffer starts to its being heard, and
	// inLatency Info.InputLatency, for any goroutine.
	latency, buffer, static, inLatency atomic.Int64
	// heard is the frame Heard last returned, the most it has.
	heard atomic.Int64
	// What Poll last read, and Read: the counts read, the changes seen,
	// and the input's buffers read.
	polled  struct{ short, lost, failed uint64 }
	changed uint64
	seen    uint64
}

// A unit is an AudioUnit the client made, and how far it got.
type unit struct {
	u              uintptr
	ready, running bool
}

// A listening is a property the listener was added to.
type listening struct {
	obj uint32
	a   address
}

// Open opens the default output device, and the default input where
// cfg.Record says, and starts them.
func Open(cfg Config) (*Client, error) {
	a, err := getAPI()
	if err != nil {
		return nil, err
	}
	if cfg.Outputs != nil && len(cfg.Outputs) == 0 {
		return nil, errors.New("audiounit: Outputs names no channel to play on")
	}
	cl := &Client{a: a, st: new(rtState), log: cfg.Log, record: cfg.Record}
	a.masked(func() {
		if err = cl.open(cfg); err != nil {
			err = errors.Join(err, cl.teardown())
		}
	})
	if err != nil {
		return nil, err
	}
	playing.Store(cl.dev)
	return cl, nil
}

// playing is the output device of the client opened last, until it
// closes.
var playing atomic.Uint32

// Playing returns the output device, by its AudioObjectID, that a
// client plays through now, or 0 where none plays. A program that sets
// a device's rate or buffer leaves this one's be: either change would
// have the client open again.
func Playing() uint32 { return playing.Load() }

func (cl *Client) logf(format string, args ...any) {
	if cl.log != nil {
		cl.log(format, args...)
	}
}

// open opens the output, and the input, and starts them. It runs on a
// thread with signals blocked.
func (cl *Client) open(cfg Config) error {
	a, st := cl.a, cl.st
	dev, ok := get32(a, systemObject, propDefaultOutputDevice, scopeGlobal)
	if !ok || dev == 0 {
		return ErrNoOutput
	}
	cl.dev = dev
	info := Info{Device: deviceName(a, dev)}
	rate, ok := rateOf(a, dev)
	if !ok || rate <= 0 {
		return fmt.Errorf("audiounit: %s reports no rate", info.Device)
	}
	info.Rate = int(math.Round(rate))
	cl.rate = info.Rate
	outs := channels(a, dev, scopeOutput)
	if outs == 0 {
		return fmt.Errorf("audiounit: %s has no outputs", info.Device)
	}
	info.Outputs = channelNames(a, dev, scopeOutput, info.Device, outs)
	info.Played = layout(cfg.Outputs, outs)
	cl.chans = len(info.Played)

	var r valueRange
	size := uint32(unsafe.Sizeof(r))
	if a.get(dev, address{propBufferFrameRange, scopeGlobal, elementMain}, &size, unsafe.Pointer(&r)) == noErr {
		info.MinBuffer, info.MaxBuffer = int(r.min), int(r.max)
	}
	cl.setBuffer(dev, info.Device, cfg.Buffer, info.MinBuffer, info.MaxBuffer)
	cl.readLatencies(&info)
	if info.Buffer == 0 {
		return fmt.Errorf("audiounit: %s reports no buffer size", info.Device)
	}

	hold := max(cfg.Hold, 0)
	cl.size = 1 << bits.Len64(uint64(int64(hold)*int64(info.Rate)/int64(time.Second)+2*scratchFrames+4096-1))
	cl.out = make([]float32, int(cl.size)*cl.chans)
	st.outs, st.mask, st.on = uint64(cl.chans), cl.size-1, 1
	st.outBuf = uintptr(unsafe.Pointer(&cl.out[0]))

	u, err := cl.newUnit()
	if err != nil {
		return err
	}
	format := planar(info.Rate, cl.chans)
	if err := setProp(cl, u, "current device", unitCurrentDevice, unitGlobal, busOut, dev); err != nil {
		return err
	}
	if err := setProp(cl, u, "stream format", unitStreamFormat, unitInput, busOut, format); err != nil {
		return err
	}
	if cfg.Outputs != nil {
		// The device's channel j plays the client's channel m[j].
		m := make([]int32, outs)
		for j := range m {
			m[j] = -1
		}
		for k, p := range info.Played {
			if p >= 0 {
				m[p] = int32(k)
			}
		}
		if st := a.setUnit(u.u, unitChannelMap, unitInput, busOut, unsafe.Pointer(&m[0]), uint32(4*len(m))); st != noErr {
			return fmt.Errorf("audiounit: %s refuses the channels %v: %d", info.Device, cfg.Outputs, st)
		}
	}
	if err := setProp(cl, u, "most frames", unitMaxFrames, unitGlobal, busOut, uint32(scratchFrames)); err != nil {
		return err
	}
	cb := callbackStruct{procs.render, uintptr(unsafe.Pointer(st))}
	if err := setProp(cl, u, "render callback", unitRenderCallback, unitInput, busOut, cb); err != nil {
		return err
	}
	if err := cl.initialize(u); err != nil {
		return err
	}

	if cfg.Record {
		if err := cl.openInput(cfg, &info); err != nil {
			info.InputProblem = err
			cl.logf("audiounit: recording nothing: %v", err)
		}
	}
	cl.info = info
	if err := cl.listen(); err != nil {
		return err
	}
	for _, u := range cl.units {
		if st := a.start(u.u); st != noErr {
			return fmt.Errorf("audiounit: AudioOutputUnitStart: %d", st)
		}
		u.running = true
	}
	cl.logf("audiounit: %s plays at %d Hz, %d frames a buffer (it allows %d to %d), on %v of %d channels; "+
		"%d frames of latency and %d of safety offset",
		info.Device, info.Rate, info.Buffer, info.MinBuffer, info.MaxBuffer, info.Played, outs, info.Latency, info.Safety)
	if info.Recorded > 0 {
		cl.logf("audiounit: %s records %d channels, %d frames a buffer, %d frames of latency",
			info.InputDevice, info.Recorded, info.InputBuffer, info.InputLatency)
	}
	return nil
}

// openInput opens the default input device, at the output's rate, and
// lets go of what it made where it fails. It runs as open does.
func (cl *Client) openInput(cfg Config, info *Info) error {
	dev, _ := get32(cl.a, systemObject, propDefaultInputDevice, scopeGlobal)
	cl.inDefault = dev
	units := len(cl.units)
	err := cl.openInputOf(dev, cfg, info)
	if err == nil {
		return nil
	}
	for _, u := range cl.units[units:] {
		if u.ready {
			cl.a.uninitialize(u.u)
		}
		cl.a.dispose(u.u)
	}
	cl.units = cl.units[:units]
	st := cl.st
	st.ins, st.inUnit, st.inBuf, st.scratch = 0, 0, 0, 0
	cl.ins, cl.in, cl.scratch = 0, nil, nil
	return err
}

// openInputOf opens input device dev, as openInput does.
func (cl *Client) openInputOf(dev uint32, cfg Config, info *Info) error {
	a, st := cl.a, cl.st
	if err := a.mayRecord(); err != nil {
		return err
	}
	if dev == 0 {
		return errors.New("audiounit: the system has no input device")
	}
	name := deviceName(a, dev)
	rate, ok := rateOf(a, dev)
	if !ok || rate <= 0 {
		return fmt.Errorf("audiounit: %s reports no rate", name)
	}
	if int(math.Round(rate)) != info.Rate {
		cl.refused = dev
		return fmt.Errorf("audiounit: %s records at %v Hz and %s plays at %d Hz: set them to one rate in "+
			"Audio MIDI Setup", name, rate, info.Device, info.Rate)
	}
	all := channels(a, dev, scopeInput)
	if all == 0 {
		return fmt.Errorf("audiounit: %s has no inputs", name)
	}
	n := min(all, MaxPorts)
	if dev != cl.dev {
		var r valueRange
		size := uint32(unsafe.Sizeof(r))
		if a.get(dev, address{propBufferFrameRange, scopeGlobal, elementMain}, &size, unsafe.Pointer(&r)) == noErr {
			cl.setBuffer(dev, name, cfg.Buffer, int(r.min), int(r.max))
		}
	}
	buffer, _ := get32(a, dev, propBufferFrameSize, scopeGlobal)

	u, err := cl.newUnit()
	if err != nil {
		return err
	}
	if err := setProp(cl, u, "input", unitEnableIO, unitInput, busIn, uint32(1)); err != nil {
		return err
	}
	if err := setProp(cl, u, "output", unitEnableIO, unitOutput, busOut, uint32(0)); err != nil {
		return err
	}
	if err := setProp(cl, u, "current device", unitCurrentDevice, unitGlobal, busOut, dev); err != nil {
		return err
	}
	if err := setProp(cl, u, "stream format", unitStreamFormat, unitOutput, busIn, planar(info.Rate, n)); err != nil {
		return err
	}
	if err := setProp(cl, u, "most frames", unitMaxFrames, unitGlobal, busOut, uint32(scratchFrames)); err != nil {
		return err
	}
	cl.ins = n
	cl.in = make([]float32, int(cl.size)*n)
	cl.scratch = make([]float32, scratchFrames*n)
	st.render, st.inUnit, st.ins = a.render, u.u, uint64(n)
	st.inBuf = uintptr(unsafe.Pointer(&cl.in[0]))
	st.scratch = uintptr(unsafe.Pointer(&cl.scratch[0]))
	cb := callbackStruct{procs.input, uintptr(unsafe.Pointer(st))}
	if err := setProp(cl, u, "input callback", unitInputCallback, unitGlobal, busOut, cb); err != nil {
		return err
	}
	if err := cl.initialize(u); err != nil {
		return err
	}
	cl.inDev = dev
	info.InputDevice = name
	info.Inputs = channelNames(a, dev, scopeInput, name, all)
	info.Recorded = n
	info.InputBuffer = int(buffer)
	lat, _ := get32(a, dev, propLatency, scopeInput)
	info.InputLatency = int(lat) + streamLatency(a, dev, scopeInput)
	cl.inLatency.Store(int64(info.InputLatency))
	return nil
}

// setBuffer sets device dev's buffer to want frames, brought into the
// range lo to hi, where want is set.
func (cl *Client) setBuffer(dev uint32, name string, want, lo, hi int) {
	if want <= 0 {
		return
	}
	if hi > 0 {
		want = min(max(want, lo), hi)
	}
	v := uint32(want)
	if st := cl.a.set(dev, address{propBufferFrameSize, scopeGlobal, elementMain}, 4, unsafe.Pointer(&v)); st != noErr {
		cl.logf("audiounit: %s refuses a buffer of %d frames: %d", name, want, st)
	}
}

// readLatencies reads the output's buffer size and latencies into info,
// and the client's copies of them.
func (cl *Client) readLatencies(info *Info) {
	a := cl.a
	buffer, _ := get32(a, cl.dev, propBufferFrameSize, scopeGlobal)
	lat, _ := get32(a, cl.dev, propLatency, scopeOutput)
	safety, _ := get32(a, cl.dev, propSafetyOffset, scopeOutput)
	info.Buffer = int(buffer)
	info.Latency = int(lat) + streamLatency(a, cl.dev, scopeOutput)
	info.Safety = int(safety)
	cl.buffer.Store(int64(info.Buffer))
	cl.latency.Store(int64(info.Latency))
	cl.static.Store(int64(info.Buffer + info.Safety + info.Latency))
	if cl.inDev != 0 {
		in, _ := get32(a, cl.inDev, propLatency, scopeInput)
		info.InputLatency = int(in) + streamLatency(a, cl.inDev, scopeInput)
		ib, _ := get32(a, cl.inDev, propBufferFrameSize, scopeGlobal)
		info.InputBuffer = int(ib)
		cl.inLatency.Store(int64(info.InputLatency))
	}
}

// deviceName returns the device's name, or its number.
func deviceName(a *api, dev uint32) string {
	if s, ok := a.str(dev, address{propName, scopeGlobal, elementMain}); ok && s != "" {
		return s
	}
	return fmt.Sprintf("device %d", dev)
}

// layout returns the device channel each of the client's plays on, of
// outs channels: as want names them, or, where it names none, the first
// two, or the one. A channel missing, or named before, plays on none:
// -1.
func layout(want []int, outs int) []int {
	if want == nil {
		if outs == 1 {
			return []int{0}
		}
		return []int{0, 1}
	}
	want = want[:min(len(want), MaxPorts)]
	played := make([]int, len(want))
	for i, o := range want {
		played[i] = -1
		if o < 0 || o >= outs {
			continue
		}
		dup := false
		for _, p := range played[:i] {
			dup = dup || p == o
		}
		if !dup {
			played[i] = o
		}
	}
	return played
}

// newUnit makes a HAL output unit, which teardown lets go.
func (cl *Client) newUnit() (*unit, error) {
	u, st := cl.a.newUnit()
	if st != noErr || u == 0 {
		return nil, fmt.Errorf("audiounit: making the HAL output unit: %d", st)
	}
	n := &unit{u: u}
	cl.units = append(cl.units, n)
	return n, nil
}

// setProp sets a property of u to v, and says which where it fails.
func setProp[T any](cl *Client, u *unit, what string, prop, scope, elem uint32, v T) error {
	if st := cl.a.setUnit(u.u, prop, scope, elem, unsafe.Pointer(&v), uint32(unsafe.Sizeof(v))); st != noErr {
		return fmt.Errorf("audiounit: setting the unit's %s: %d", what, st)
	}
	return nil
}

func (cl *Client) initialize(u *unit) error {
	if st := cl.a.initialize(u.u); st != noErr {
		return fmt.Errorf("audiounit: AudioUnitInitialize: %d", st)
	}
	u.ready = true
	return nil
}

// listen adds the listener to the properties whose change may have the
// client open again, or read its latencies again.
func (cl *Client) listen() error {
	add := func(obj, selector, scope uint32) error {
		l := listening{obj, address{selector, scope, elementMain}}
		if st := cl.a.listen(l.obj, l.a, procs.changed, uintptr(unsafe.Pointer(&changes))); st != noErr {
			return fmt.Errorf("audiounit: AudioObjectAddPropertyListener: %d", st)
		}
		cl.listened = append(cl.listened, l)
		return nil
	}
	cl.changed = atomic.LoadUint64(&changes)
	ls := []listening{
		{systemObject, address{propDefaultOutputDevice, scopeGlobal, 0}},
		{cl.dev, address{propDeviceIsAlive, scopeGlobal, 0}},
		{cl.dev, address{propNominalSampleRate, scopeGlobal, 0}},
		{cl.dev, address{propBufferFrameSize, scopeGlobal, 0}},
		{cl.dev, address{propLatency, scopeWildcard, 0}},
		{cl.dev, address{propSafetyOffset, scopeWildcard, 0}},
		{cl.dev, address{propStreams, scopeWildcard, 0}},
	}
	if cl.record {
		ls = append(ls, listening{systemObject, address{propDefaultInputDevice, scopeGlobal, 0}})
	}
	if cl.refused != 0 {
		ls = append(ls, listening{cl.refused, address{propNominalSampleRate, scopeGlobal, 0}})
	}
	if cl.inDev != 0 && cl.inDev != cl.dev {
		ls = append(ls,
			listening{cl.inDev, address{propDeviceIsAlive, scopeGlobal, 0}},
			listening{cl.inDev, address{propNominalSampleRate, scopeGlobal, 0}},
			listening{cl.inDev, address{propLatency, scopeWildcard, 0}},
			listening{cl.inDev, address{propStreams, scopeWildcard, 0}})
	}
	for _, l := range ls {
		if err := add(l.obj, l.a.selector, l.a.scope); err != nil {
			return err
		}
	}
	return nil
}

// teardown stops the units and lets them go, the input first, and
// removes the listener, each step taken where one before it fails. It
// runs on a thread with signals blocked, with mu held or before the
// client is shared.
func (cl *Client) teardown() error {
	a := cl.a
	var errs []error
	for i := len(cl.units) - 1; i >= 0; i-- {
		u := cl.units[i]
		// A unit stopped from outside its thread has stopped by the time
		// AudioOutputUnitStop returns: its callback runs no more.
		if u.running {
			if st := a.stop(u.u); st != noErr {
				errs = append(errs, fmt.Errorf("audiounit: AudioOutputUnitStop: %d", st))
			}
			u.running = false
		}
		if u.ready {
			if st := a.uninitialize(u.u); st != noErr {
				errs = append(errs, fmt.Errorf("audiounit: AudioUnitUninitialize: %d", st))
			}
		}
		if st := a.dispose(u.u); st != noErr {
			errs = append(errs, fmt.Errorf("audiounit: AudioComponentInstanceDispose: %d", st))
		}
	}
	cl.units = nil
	// A device gone took its listeners with it: Core Audio tells it is
	// gone, and the listener is removed all the same. Other failures
	// make one error, however many listeners fail.
	failed, first := 0, int32(noErr)
	for _, l := range cl.listened {
		if st := a.unlisten(l.obj, l.a, procs.changed, uintptr(unsafe.Pointer(&changes))); st != noErr && st != badObject {
			if failed == 0 {
				first = st
			}
			failed++
		}
	}
	if failed > 0 {
		errs = append(errs, fmt.Errorf("audiounit: AudioObjectRemovePropertyListener: %d, for %d of %d listeners",
			first, failed, len(cl.listened)))
	}
	cl.listened = nil
	// Core Audio's threads read these until the units were let go.
	runtime.KeepAlive(cl.st)
	runtime.KeepAlive(cl.out)
	runtime.KeepAlive(cl.in)
	runtime.KeepAlive(cl.scratch)
	return errors.Join(errs...)
}

// Info says how the client plays now.
func (cl *Client) Info() Info {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	i := cl.info
	i.Outputs = append([]string(nil), i.Outputs...)
	i.Played = append([]int(nil), i.Played...)
	i.Inputs = append([]string(nil), i.Inputs...)
	return i
}

// Rate returns the rate the client plays at.
func (cl *Client) Rate() int { return cl.rate }

// Channels returns how many channels a frame Write takes holds: one for
// each of Info's Played.
func (cl *Client) Channels() int { return cl.chans }

// Inputs returns how many channels Read's frames hold.
func (cl *Client) Inputs() int { return cl.ins }

// Buffer returns the frames the device hands the client at a time, as
// Poll last read it.
func (cl *Client) Buffer() int { return int(cl.buffer.Load()) }

// Delay returns the frames from a frame written as the device starts a
// buffer to its being heard: the buffer, the safety offset and the
// latency, as Poll last read them.
func (cl *Client) Delay() int { return int(cl.static.Load()) }

// Write adds frames, of Channels interleaved samples, to the ring the
// output plays from, as many as it has room for, and returns how many.
func (cl *Client) Write(frames []float32) int {
	st, ch := cl.st, cl.chans
	w := atomic.LoadUint64(&st.outW)
	n := min(uint64(len(frames)/ch), cl.size-(w-atomic.LoadUint64(&st.outR)))
	for k := range ch {
		ring := cl.out[uint64(k)*cl.size : uint64(k+1)*cl.size]
		for i := range n {
			ring[(w+i)&st.mask] = frames[int(i)*ch+k]
		}
	}
	atomic.StoreUint64(&st.outW, w+n)
	return int(n)
}

// Written returns how many frames Write has written, ever.
func (cl *Client) Written() int64 { return int64(atomic.LoadUint64(&cl.st.outW)) }

// Queued returns how many frames written the output has not taken.
func (cl *Client) Queued() int64 {
	r := atomic.LoadUint64(&cl.st.outR)
	return int64(atomic.LoadUint64(&cl.st.outW) - r)
}

// Room returns how many frames Write has room for.
func (cl *Client) Room() int64 { return int64(cl.size) - cl.Queued() }

// Heard returns the frame heard now, as Written counts them, and false
// before the output has taken a buffer Core Audio stamped with a time.
// It never goes back: where a buffer comes late, the frame heard runs on
// to the end of the frames taken, and the late buffer's time stamp may
// place it a little behind that; Heard keeps to the frame it returned
// until the stamps pass it.
func (cl *Client) Heard() (int64, bool) {
	at, ok := cl.heardAt(cl.a.now())
	if !ok {
		return 0, false
	}
	for {
		most := cl.heard.Load()
		if at <= most {
			return most, true
		}
		if cl.heard.CompareAndSwap(most, at) {
			return at, true
		}
	}
}

// stampedBack is how many buffers back from the newest heardAt looks for
// one Core Audio stamped with a time.
const stampedBack = 8

// heardAt returns the frame heard at host time t, in nanoseconds, as
// Heard does. The output's log places each buffer's first frame at the
// hardware at a host time, and the device's latency later at the ear;
// frames run on at the rate from there. The frames played as silence,
// as the ring ran dry or the output paused, count for no frame of the
// ring, and nothing is heard past the frames the output took.
func (cl *Client) heardAt(t int64) (int64, bool) {
	st, a := cl.st, cl.a
	lat := float64(cl.latency.Load())
	for range 8 {
		c := atomic.LoadUint64(&st.cycles)
		if c == 0 {
			return 0, false
		}
		// The newest buffer stamped with a time places the frames.
		k := c - 1
		host := atomic.LoadUint64(&st.logHost[k&(logSize-1)])
		for host == 0 && k > 0 && c-k < stampedBack {
			k--
			host = atomic.LoadUint64(&st.logHost[k&(logSize-1)])
		}
		if host == 0 {
			return 0, false
		}
		// p is the frame at the ear, counted as played counts them.
		p := float64(atomic.LoadUint64(&st.logPlayed[k&(logSize-1)])) + float64(t-a.hostNs(host))*float64(cl.rate)/1e9 - lat
		// The buffer that played it, the last if it is past them all:
		// walking back as far as the log holds.
		j := c - 1
		played := atomic.LoadUint64(&st.logPlayed[j&(logSize-1)])
		for float64(played) > p && j > 0 && c-j < logSize-1 {
			j--
			played = atomic.LoadUint64(&st.logPlayed[j&(logSize-1)])
		}
		r := atomic.LoadUint64(&st.logR[j&(logSize-1)])
		took := atomic.LoadUint64(&st.logTook[j&(logSize-1)])
		if atomic.LoadUint64(&st.cycles)-min(j, k) >= logSize {
			// Written over as it was read.
			continue
		}
		if float64(played) > p {
			return int64(r), true
		}
		return int64(r) + min(int64(p-float64(played)), int64(took)), true
	}
	return 0, false
}

// Read takes what the input recorded in the oldest buffer not yet read,
// as many frames as dst holds, of Inputs interleaved samples. It returns
// how many, and at, the frame heard, as Written counts them, as the
// first of them reached the input. ok is false where nothing waits.
// Where Read falls behind by logSize buffers, those buffers are lost.
func (cl *Client) Read(dst []float32) (n int, at int64, ok bool) {
	st, ch := cl.st, cl.ins
	if ch == 0 || len(dst) < ch {
		return 0, 0, false
	}
	for {
		cycles := atomic.LoadUint64(&st.inCycles)
		if cl.seen == cycles {
			return 0, 0, false
		}
		if cycles-cl.seen >= logSize {
			// The log has moved past the buffers not read.
			cl.seen = cycles - logSize/2
			continue
		}
		i := cl.seen & (logSize - 1)
		host := atomic.LoadUint64(&st.inLogHost[i])
		w0 := atomic.LoadUint64(&st.inLogW[i])
		k := atomic.LoadUint64(&st.inLogN[i])
		if atomic.LoadUint64(&st.inCycles)-cl.seen >= logSize {
			// Written over as it was read.
			continue
		}
		r := atomic.LoadUint64(&st.inR)
		if r < w0 {
			// Buffers skipped above: their frames go.
			r = w0
		}
		if r >= w0+k {
			cl.seen++
			atomic.StoreUint64(&st.inR, r)
			continue
		}
		m := min(w0+k-r, uint64(len(dst)/ch))
		for c := range ch {
			ring := cl.in[uint64(c)*cl.size : uint64(c+1)*cl.size]
			for j := range m {
				dst[int(j)*ch+c] = ring[(r+j)&st.mask]
			}
		}
		atomic.StoreUint64(&st.inR, r+m)
		if r+m == w0+k {
			cl.seen++
		}
		return int(m), cl.stamp(host, r-w0), true
	}
}

// stamp returns the frame heard as the frame off frames into a buffer
// the input stamped with host time host reached the input: the time
// less the input's latency. Where Core Audio told no time, it is the
// frame the output took last, less the delays.
func (cl *Client) stamp(host, off uint64) int64 {
	rate := float64(cl.rate)
	if host != 0 {
		t := cl.a.hostNs(host) + int64((float64(off)-float64(cl.inLatency.Load()))*1e9/rate)
		if at, ok := cl.heardAt(t); ok {
			return at
		}
	}
	taken := int64(atomic.LoadUint64(&cl.st.outR))
	return taken - cl.static.Load() - cl.inLatency.Load() - cl.buffer.Load()
}

// Poll returns what happened since it was last called. Where Core Audio
// told of a change, it reads the devices again, and says whether to open
// the client again.
func (cl *Client) Poll() Events {
	st := cl.st
	delta := func(v *uint64, last *uint64) int64 {
		now := atomic.LoadUint64(v)
		d := int64(now - *last)
		*last = now
		return d
	}
	e := Events{
		Short:  delta(&st.short, &cl.polled.short),
		Lost:   delta(&st.lost, &cl.polled.lost),
		Failed: delta(&st.failed, &cl.polled.failed),
		Cycles: int64(atomic.LoadUint64(&st.cycles)),
	}
	if c := atomic.LoadUint64(&changes); c != cl.changed {
		cl.changed = c
		e.Changed = true
		e.Reopen = cl.check()
	}
	return e
}

// check reads the devices again, and returns why the client plays
// otherwise than it opened, or "".
func (cl *Client) check() string {
	a := cl.a
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.closed {
		return ""
	}
	name := cl.info.Device
	if dev, ok := get32(a, systemObject, propDefaultOutputDevice, scopeGlobal); ok && dev != cl.dev {
		return "the default output device changed"
	}
	if alive, ok := get32(a, cl.dev, propDeviceIsAlive, scopeGlobal); !ok || alive == 0 {
		return name + " went away"
	}
	if rate, ok := rateOf(a, cl.dev); ok && int(math.Round(rate)) != cl.rate {
		return fmt.Sprintf("%s's rate changed from %d to %v Hz", name, cl.rate, rate)
	}
	if n := channels(a, cl.dev, scopeOutput); n != len(cl.info.Outputs) {
		return fmt.Sprintf("%s has %d outputs now, of %d", name, n, len(cl.info.Outputs))
	}
	if cl.record {
		if dev, ok := get32(a, systemObject, propDefaultInputDevice, scopeGlobal); ok && dev != cl.inDefault {
			return "the default input device changed"
		}
	}
	if cl.refused != 0 {
		if rate, ok := rateOf(a, cl.refused); ok && int(math.Round(rate)) == cl.rate {
			return fmt.Sprintf("%s records at %d Hz now, as %s plays", deviceName(a, cl.refused), cl.rate, name)
		}
	}
	if cl.inDev != 0 {
		in := cl.info.InputDevice
		if alive, ok := get32(a, cl.inDev, propDeviceIsAlive, scopeGlobal); !ok || alive == 0 {
			return in + " went away"
		}
		if rate, ok := rateOf(a, cl.inDev); ok && int(math.Round(rate)) != cl.rate {
			return fmt.Sprintf("%s's rate changed from %d to %v Hz", in, cl.rate, rate)
		}
	}
	was := cl.info
	cl.readLatencies(&cl.info)
	if cl.info.Buffer != was.Buffer || cl.info.Latency != was.Latency || cl.info.Safety != was.Safety ||
		cl.info.InputLatency != was.InputLatency {
		cl.logf("audiounit: %d frames a buffer, %d frames of latency, %d of safety offset, %d of input latency",
			cl.info.Buffer, cl.info.Latency, cl.info.Safety, cl.info.InputLatency)
	}
	return ""
}

// Pause stops the output taking from the ring, and plays silence, or
// starts it again.
func (cl *Client) Pause(paused bool) {
	on := uint64(1)
	if paused {
		on = 0
	}
	atomic.StoreUint64(&cl.st.on, on)
}

// Suspend pauses the output and stops both units, so the device sleeps
// and the microphone is let go. Resume starts them again.
func (cl *Client) Suspend() error {
	cl.Pause(true)
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.closed {
		return nil
	}
	var errs []error
	for i := len(cl.units) - 1; i >= 0; i-- {
		if u := cl.units[i]; u.running {
			if st := cl.a.stop(u.u); st != noErr {
				errs = append(errs, fmt.Errorf("audiounit: AudioOutputUnitStop: %d", st))
				continue
			}
			u.running = false
		}
	}
	return errors.Join(errs...)
}

// Resume starts the units Suspend stopped, and the output taking from
// the ring.
func (cl *Client) Resume() error {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.closed {
		return nil
	}
	var errs []error
	cl.a.masked(func() {
		for _, u := range cl.units {
			if !u.running {
				if st := cl.a.start(u.u); st != noErr {
					errs = append(errs, fmt.Errorf("audiounit: AudioOutputUnitStart: %d", st))
					continue
				}
				u.running = true
			}
		}
	})
	cl.Pause(false)
	return errors.Join(errs...)
}

// Close stops the units and lets them go. Calls after the first return
// nil.
func (cl *Client) Close() error {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.closed {
		return nil
	}
	cl.closed = true
	playing.CompareAndSwap(cl.dev, 0)
	var err error
	cl.a.masked(func() { err = cl.teardown() })
	return err
}
