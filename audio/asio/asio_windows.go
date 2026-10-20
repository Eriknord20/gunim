//go:build windows && amd64

package asio

import (
	"errors"
	"fmt"
	"math"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// The methods of a driver's interface, by their place in its table,
// after the three of IUnknown.
const (
	mRelease         = 2
	mInit            = 3
	mGetErrorMessage = 6
	mStart           = 7
	mStop            = 8
	mGetChannels     = 9
	mGetLatencies    = 10
	mGetBufferSize   = 11
	mCanSampleRate   = 12
	mGetSampleRate   = 13
	mSetSampleRate   = 14
	mGetChannelInfo  = 18
	mCreateBuffers   = 19
	mDisposeBuffers  = 20
	mControlPanel    = 21
	mOutputReady     = 23
)

// The messages a driver sends through asioMessage.
const (
	msgSelectorSupported = 1
	msgEngineVersion     = 2
	msgResetRequest      = 3
	msgBufferSizeChange  = 4
	msgResyncRequest     = 5
	msgLatenciesChanged  = 6
	msgSupportsTimeInfo  = 7
)

// messageNames names the messages a driver sends of a change, for the
// log; the rest are questions of what the host can do.
var messageNames = map[int32]string{
	msgResetRequest:     "open me again",
	msgBufferSizeChange: "my buffer size changed",
	msgResyncRequest:    "resync",
	msgLatenciesChanged: "my latencies changed",
}

// bufferInfo asks the driver for one channel's two buffers, and holds
// them once it has made them.
type bufferInfo struct {
	isInput int32
	channel int32
	buffers [2]uintptr
}

// channelInfo is what the driver says of one channel.
type channelInfo struct {
	channel  int32
	isInput  int32
	isActive int32
	group    int32
	typ      sampleType
	name     [32]byte
}

// An outSlot is an output a device plays on: output out of the
// driver, which takes samples of typ, plays channel ch of the frames
// Fill or FillN fills, or silence where ch is -1.
type outSlot struct {
	out, ch int
	typ     sampleType
}

// callbacks are the functions the driver calls.
type callbacks struct {
	bufferSwitch, sampleRateDidChange, asioMessage, bufferSwitchTimeInfo uintptr
}

var (
	// openMu guards opening and closing; playing is the device open, as
	// the callbacks, which carry no pointer of their own, find it.
	openMu  sync.Mutex
	playing atomic.Pointer[Device]
	// cbs and infos are handed to the driver, which keeps them; as
	// package variables they stay where they are.
	cbOnce sync.Once
	cbs    callbacks
	infos  [MaxOutputs + MaxInputs]bufferInfo
)

var (
	ole32                       = windows.NewLazySystemDLL("ole32.dll")
	procCoCreateInstance        = ole32.NewProc("CoCreateInstance")
	user32                      = windows.NewLazySystemDLL("user32.dll")
	procGetDesktopWindow        = user32.NewProc("GetDesktopWindow")
	procMsgWaitForMultipleObjEx = user32.NewProc("MsgWaitForMultipleObjectsEx")
	procPeekMessageW            = user32.NewProc("PeekMessageW")
	procTranslateMessage        = user32.NewProc("TranslateMessage")
	procDispatchMessageW        = user32.NewProc("DispatchMessageW")
)

// Drivers lists the ASIO drivers installed, by name.
func Drivers() ([]Driver, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\ASIO`, registry.ENUMERATE_SUB_KEYS|registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("asio: listing the drivers: %w", err)
	}
	defer func() { _ = k.Close() }()
	names, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return nil, fmt.Errorf("asio: listing the drivers: %w", err)
	}
	var out []Driver
	for _, n := range names {
		sk, err := registry.OpenKey(k, n, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		clsid, _, err := sk.GetStringValue("CLSID")
		_ = sk.Close()
		if err == nil && clsid != "" {
			out = append(out, Driver{Name: n, CLSID: clsid})
		}
	}
	slices.SortFunc(out, func(a, b Driver) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return out, nil
}

// A Device is an open ASIO driver, playing. Its methods are safe from
// any goroutine.
type Device struct {
	t     *sta
	obj   uintptr
	info  Info
	fill  func([]float32)
	fillN func([]float32, int)
	// record hears the inputs, ins of them, read as inTypes say into
	// inBuf, where Config.Record is set.
	record    func([]float32, int)
	recording func() bool
	ins       int
	inTypes   []sampleType
	inBuf     []float32
	reset     func()
	log       func(string, ...any)
	// outs are the outputs played, the first of infos, and chans how
	// many channels a frame of buf holds, as Fill or FillN fills it.
	// mono averages Fill's pair onto the one output played.
	outs  []outSlot
	chans int
	mono  bool
	buf   []float32
	// inNames names all the driver's inputs, for Channels.
	inNames []string
	// ready is the driver's outputReady, where it has one, which it
	// wants called once a buffer is filled.
	ready uintptr
	// made and started say the buffers are made and the driver runs;
	// live says so to the driver's thread, which hears resets from then.
	made, started bool
	live          atomic.Bool
	resetOnce     sync.Once
	closeOnce     sync.Once
}

// Open opens the driver c names and starts it playing.
func Open(c Config) (*Device, error) {
	if c.Fill == nil && c.FillN == nil {
		return nil, errors.New("asio: Config.Fill and FillN are nil")
	}
	openMu.Lock()
	defer openMu.Unlock()
	if playing.Load() != nil {
		return nil, ErrOpen
	}
	clsid, err := clsidOf(c.Name)
	if err != nil {
		return nil, err
	}
	t, err := newSTA()
	if err != nil {
		return nil, err
	}
	d := &Device{t: t, fill: c.Fill, fillN: c.FillN, record: c.Record, recording: c.Recording, reset: c.Reset, log: c.Log}
	cbOnce.Do(makeCallbacks)
	done := t.start(func() { err = d.open(clsid, c) })
	if !waitFor(done, openWait) {
		d.logf("asio: %s did not start within %v; let go", c.Name, openWait)
		d.live.Store(false)
		playing.CompareAndSwap(d, nil)
		go func() {
			<-done
			t.run(d.release)
			t.stop()
		}()
		return nil, ErrNotAnswering
	}
	if err != nil {
		t.run(d.release)
		t.stop()
		return nil, err
	}
	return d, nil
}

// clsidOf returns the class ID of the driver named.
func clsidOf(name string) (windows.GUID, error) {
	drivers, err := Drivers()
	if err != nil {
		return windows.GUID{}, err
	}
	i := slices.IndexFunc(drivers, func(d Driver) bool { return d.Name == name })
	if i < 0 {
		return windows.GUID{}, fmt.Errorf("asio: no driver is called %q", name)
	}
	clsid, err := windows.GUIDFromString(drivers[i].CLSID)
	if err != nil {
		return windows.GUID{}, fmt.Errorf("asio: %s: %w", name, err)
	}
	return clsid, nil
}

// ControlPanel opens the settings of the driver named, while no device
// plays through it: for a driver that refuses to start until its
// settings change. It loads the driver on a thread of its own, starts
// it where it starts, opens its panel, and lets it go once the panel
// closes.
func ControlPanel(name string) error {
	openMu.Lock()
	defer openMu.Unlock()
	if playing.Load() != nil {
		return ErrOpen
	}
	clsid, err := clsidOf(name)
	if err != nil {
		return err
	}
	t, err := newSTA()
	if err != nil {
		return err
	}
	defer t.stop()
	t.run(func() {
		d := &Device{}
		if hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsid)), 0, windows.CLSCTX_INPROC_SERVER,
			uintptr(unsafe.Pointer(&clsid)), uintptr(unsafe.Pointer(&d.obj))); hr != 0 {
			err = fmt.Errorf("asio: loading %s: %w", name, windows.Errno(hr))
			return
		}
		defer d.call(mRelease)
		// A driver that refuses to start may still show its panel.
		hwnd, _, _ := procGetDesktopWindow.Call()
		_ = d.call(mInit, hwnd)
		if r := d.call(mControlPanel); r != codeOK {
			err = &Error{Op: "opening the settings of " + name, Code: r, Message: d.message()}
		}
	})
	return err
}

// Channels lists the names of the inputs and outputs of the driver
// named, in order: an output's index in outputs is the one
// [Config.Outputs] takes. While a device plays through the driver it
// tells what the driver told as it opened; while none plays it loads
// the driver on a thread of its own, without starting it, and lets it
// go. It returns [ErrOpen] while a device plays through another driver.
func Channels(name string) (inputs, outputs []string, err error) {
	openMu.Lock()
	defer openMu.Unlock()
	if d := playing.Load(); d != nil {
		if d.info.Driver != name {
			return nil, nil, ErrOpen
		}
		return slices.Clone(d.inNames), slices.Clone(d.info.OutputNames), nil
	}
	clsid, err := clsidOf(name)
	if err != nil {
		return nil, nil, err
	}
	t, err := newSTA()
	if err != nil {
		return nil, nil, err
	}
	defer t.stop()
	t.run(func() {
		d := &Device{}
		if hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsid)), 0, windows.CLSCTX_INPROC_SERVER,
			uintptr(unsafe.Pointer(&clsid)), uintptr(unsafe.Pointer(&d.obj))); hr != 0 {
			err = fmt.Errorf("asio: loading %s: %w", name, windows.Errno(hr))
			return
		}
		defer d.call(mRelease)
		hwnd, _, _ := procGetDesktopWindow.Call()
		if d.call(mInit, hwnd) == 0 {
			err = &Error{Op: "starting " + name, Message: d.refused()}
			return
		}
		var ins, outs int32
		if r := d.call(mGetChannels, uintptr(unsafe.Pointer(&ins)), uintptr(unsafe.Pointer(&outs))); r != codeOK {
			err = d.fail("counting the channels", r)
			return
		}
		inInfos, _ := d.channels(int(ins), true)
		outInfos, _ := d.channels(int(outs), false)
		inputs, outputs = namesOf(inInfos), namesOf(outInfos)
	})
	return inputs, outputs, err
}

// refused says why the driver refused to start: as it says, or, where
// it says nothing, what is likely.
func (d *Device) refused() string {
	if m := d.message(); m != "" {
		return m
	}
	return "the driver refused to start, and says nothing of why; another program may hold the device, or the driver's settings may need a change"
}

// open loads the driver and starts it. It runs on the device's thread.
func (d *Device) open(clsid windows.GUID, c Config) error {
	if hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsid)), 0, windows.CLSCTX_INPROC_SERVER,
		uintptr(unsafe.Pointer(&clsid)), uintptr(unsafe.Pointer(&d.obj))); hr != 0 {
		return fmt.Errorf("asio: loading %s: %w", c.Name, windows.Errno(hr))
	}
	hwnd, _, _ := procGetDesktopWindow.Call()
	if d.call(mInit, hwnd) == 0 {
		return &Error{Op: "starting " + c.Name, Message: d.refused()}
	}
	var ins, outs int32
	if r := d.call(mGetChannels, uintptr(unsafe.Pointer(&ins)), uintptr(unsafe.Pointer(&outs))); r != codeOK {
		return d.fail("counting the channels", r)
	}
	if outs < 1 {
		return fmt.Errorf("asio: %s has no outputs", c.Name)
	}
	rate := d.rate()
	if c.Rate > 0 && rate != float64(c.Rate) && d.call(mCanSampleRate, uintptr(math.Float64bits(float64(c.Rate)))) == codeOK {
		// A driver that refuses still plays, at its own rate.
		_ = d.call(mSetSampleRate, uintptr(math.Float64bits(float64(c.Rate))))
		rate = d.rate()
	}
	if rate <= 0 {
		return fmt.Errorf("asio: %s plays at no rate it reports", c.Name)
	}
	var lo, hi, pref, gran int32
	if r := d.call(mGetBufferSize, uintptr(unsafe.Pointer(&lo)), uintptr(unsafe.Pointer(&hi)),
		uintptr(unsafe.Pointer(&pref)), uintptr(unsafe.Pointer(&gran))); r != codeOK {
		return d.fail("reading the buffer sizes", r)
	}
	size := fitBuffer(c.Buffer, int(lo), int(hi), int(pref), int(gran))
	// A driver that tells of fewer outputs than it counts plays on
	// those it tells of.
	outInfos, r := d.channels(int(outs), false)
	if len(outInfos) == 0 {
		return d.fail("reading the first output", r)
	}
	outNames := namesOf(outInfos)
	played, err := d.outputs(c, outInfos)
	if err != nil {
		return err
	}
	inInfos, _ := d.channels(int(ins), true)
	d.inNames = namesOf(inInfos)
	names := d.inputs(inInfos)
	first := d.outs[0].typ
	_, bits := first.size()
	// A driver may send messages as it makes its buffers.
	playing.Store(d)
	r = d.makeBuffers(size)
	if r != codeOK && d.ins > 0 {
		d.logf("asio: %s made no buffers for its inputs (%v); playing without them", c.Name, d.fail("making the buffers", r))
		d.ins, d.inTypes, names = 0, nil, nil
		r = d.makeBuffers(size)
	}
	if r != codeOK {
		return d.fail("making the buffers", r)
	}
	d.made = true
	d.inBuf = make([]float32, d.ins*size)
	var inLat, outLat int32
	_ = d.call(mGetLatencies, uintptr(unsafe.Pointer(&inLat)), uintptr(unsafe.Pointer(&outLat)))
	d.buf = make([]float32, d.chans*size)
	if d.call(mOutputReady) == codeOK {
		d.ready = method(d.obj, mOutputReady)
	}
	for i, o := range d.outs {
		bytes, _ := o.typ.size()
		for half := range 2 {
			clear(unsafe.Slice(ptr[byte](infos[i].buffers[half]), size*bytes))
		}
	}
	d.info = Info{Driver: c.Name, Rate: int(math.Round(rate)), Buffer: size, MinBuffer: int(lo), MaxBuffer: int(hi),
		PreferredBuffer: int(pref), Latency: int(outLat), Bits: bits, Float: first.float(), Outputs: int(outs),
		OutputNames: outNames, Played: played, Inputs: int(ins), Recorded: d.ins, InputNames: names, InputLatency: int(inLat)}
	if r := d.call(mStart); r != codeOK {
		return d.fail("starting to play", r)
	}
	d.started = true
	d.live.Store(true)
	return nil
}

// channels returns what the driver says of its first n inputs, or
// outputs, as many as it answers for, and its answer to the first it
// did not. It runs on the device's thread.
func (d *Device) channels(n int, input bool) (told []channelInfo, code int32) {
	in := int32(0)
	if input {
		in = 1
	}
	out := make([]channelInfo, 0, n)
	for ch := range n {
		ci := channelInfo{channel: int32(ch), isInput: in}
		if r := d.call(mGetChannelInfo, uintptr(unsafe.Pointer(&ci))); r != codeOK {
			return out, r
		}
		out = append(out, ci)
	}
	return out, codeOK
}

// namesOf returns what the driver calls each of chans.
func namesOf(chans []channelInfo) []string {
	names := make([]string, len(chans))
	for i, ci := range chans {
		n := 0
		for n < len(ci.name) && ci.name[n] != 0 {
			n++
		}
		names[i] = strings.TrimSpace(string(ci.name[:n]))
	}
	return names
}

// outputs readies the outputs c asks to play on, of those the driver
// told of, and returns the output each channel plays on, as
// Info.Played tells it. It runs on the device's thread.
func (d *Device) outputs(c Config, outInfos []channelInfo) ([]int, error) {
	played := layout(c.Outputs, len(outInfos))
	d.chans = 2
	if d.fillN != nil {
		d.chans = len(played)
	}
	d.mono = d.fillN == nil && len(played) == 1
	for ch, o := range played {
		if o < 0 {
			if ch < len(c.Outputs) {
				d.logf("asio: %s has no output %d to play channel %d on, or plays it already", c.Name, c.Outputs[ch], ch)
			}
			continue
		}
		typ := outInfos[o].typ
		if bytes, _ := typ.size(); bytes == 0 {
			return nil, fmt.Errorf("asio: %s takes samples of type %d on output %d, which this package plays none of", c.Name, typ, o)
		}
		if ch >= d.chans {
			// Past Fill's pair: it plays silence, as the buffers
			// are cleared.
			ch = -1
		}
		d.outs = append(d.outs, outSlot{out: o, ch: ch, typ: typ})
	}
	if len(d.outs) == 0 {
		return nil, fmt.Errorf("asio: %s has none of the outputs %v", c.Name, c.Outputs)
	}
	return played, nil
}

// inputs readies the first of the driver's inputs to record, as many as
// Record wants and the package reads, and returns their names. It runs
// on the device's thread.
func (d *Device) inputs(inInfos []channelInfo) []string {
	if d.record == nil {
		return nil
	}
	var names []string
	all := namesOf(inInfos)
	for ch, ci := range inInfos[:min(len(inInfos), MaxInputs)] {
		if bytes, _ := ci.typ.size(); bytes == 0 {
			d.logf("asio: input %d gives samples of type %d, which this package reads none of", ch, ci.typ)
			break
		}
		d.inTypes = append(d.inTypes, ci.typ)
		names = append(names, all[ch])
	}
	d.ins = len(names)
	return names
}

// makeBuffers asks the driver for the buffers of the outputs it plays
// and of the inputs it records, size frames each, and returns its
// answer. It runs on the device's thread.
func (d *Device) makeBuffers(size int) int32 {
	n := 0
	for _, o := range d.outs {
		infos[n] = bufferInfo{channel: int32(o.out)}
		n++
	}
	for ch := range d.ins {
		infos[n] = bufferInfo{isInput: 1, channel: int32(ch)}
		n++
	}
	return d.call(mCreateBuffers, uintptr(unsafe.Pointer(&infos[0])), uintptr(n), uintptr(size),
		uintptr(unsafe.Pointer(&cbs)))
}

// rate returns the rate the driver is at, or 0.
func (d *Device) rate() float64 {
	var r float64
	if d.call(mGetSampleRate, uintptr(unsafe.Pointer(&r))) != codeOK {
		return 0
	}
	return r
}

// release stops the driver and lets it go. It runs on the device's
// thread.
func (d *Device) release() {
	if d.obj == 0 {
		return
	}
	d.live.Store(false)
	if d.started {
		_ = d.call(mStop)
		d.started = false
	}
	// A buffer switch late after the stop finds no device to fill.
	playing.CompareAndSwap(d, nil)
	if d.made {
		_ = d.call(mDisposeBuffers)
		d.made = false
	}
	d.call(mRelease)
	d.obj = 0
}

// fail returns the error r, as the driver tells of it.
func (d *Device) fail(op string, r int32) error { return &Error{Op: op, Code: r, Message: d.message()} }

// message returns what the driver says of its last error.
func (d *Device) message() string {
	var b [128]byte
	_ = d.call(mGetErrorMessage, uintptr(unsafe.Pointer(&b[0])))
	n := 0
	for n < len(b) && b[n] != 0 {
		n++
	}
	return strings.TrimSpace(string(b[:n]))
}

// call calls method m of the driver, with integer arguments, and
// returns what it returns: a long, ASIO's 32 bits.
//
//go:uintptrescapes
func (d *Device) call(m int, args ...uintptr) int32 {
	r, _, _ := syscall.SyscallN(method(d.obj, m), append([]uintptr{d.obj}, args...)...)
	return int32(uint32(r))
}

// method returns the function at place m of obj's table.
func method(obj uintptr, m int) uintptr {
	vtbl := *ptr[unsafe.Pointer](obj)
	return *(*uintptr)(unsafe.Add(vtbl, uintptr(m)*unsafe.Sizeof(uintptr(0))))
}

// ptr returns p, an address in the driver's memory, as a pointer.
func ptr[T any](p uintptr) *T {
	return *(**T)(unsafe.Pointer(&p))
}

// Info says how the device plays.
func (d *Device) Info() Info { return d.info }

// ControlPanel opens the driver's own settings, and returns once they
// close. A change there that needs the device opened again calls
// Config.Reset.
func (d *Device) ControlPanel() error {
	var r int32
	if !d.t.run(func() {
		if d.obj != 0 {
			r = d.call(mControlPanel)
		}
	}) {
		return errors.New("asio: the driver is closed")
	}
	if r != codeOK {
		return &Error{Op: "opening the control panel", Code: r}
	}
	return nil
}

// Close stops the driver and lets it go. A driver that does not stop
// in time is let go as it is, and Close returns [ErrNotAnswering].
func (d *Device) Close() error {
	var err error
	d.closeOnce.Do(func() {
		openMu.Lock()
		defer openMu.Unlock()
		done := d.t.start(d.release)
		if done == nil {
			return
		}
		if !waitFor(done, closeWait) {
			d.logf("asio: %s did not stop within %v; let go", d.info.Driver, closeWait)
			d.live.Store(false)
			playing.CompareAndSwap(d, nil)
			go func() {
				<-done
				d.t.stop()
			}()
			err = ErrNotAnswering
			return
		}
		d.t.stop()
	})
	return err
}

// How long a driver has to start, and to stop, before it is let go.
const (
	openWait  = 10 * time.Second
	closeWait = 3 * time.Second
)

// waitFor waits up to wait for done, and reports whether it came.
func waitFor(done <-chan struct{}, wait time.Duration) bool {
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-done:
		return true
	case <-t.C:
		return false
	}
}

// logf tells the device's Log, where it has one.
func (d *Device) logf(format string, args ...any) {
	if d.log != nil {
		d.log(format, args...)
	}
}

// switchTo fills the buffers of half, 0 or 1, with what plays next.
// It runs on the driver's thread.
func (d *Device) switchTo(half int32) {
	if half != 0 && half != 1 {
		return
	}
	n := d.info.Buffer
	if d.ins > 0 && (d.recording == nil || d.recording()) {
		in := d.inBuf[:n*d.ins]
		for ch, typ := range d.inTypes {
			bytes, _ := typ.size()
			read(in, unsafe.Slice(ptr[byte](infos[len(d.outs)+ch].buffers[half]), n*bytes), ch, d.ins, typ)
		}
		d.record(in, d.ins)
	}
	frames := d.buf[:d.chans*n]
	if d.fillN != nil {
		d.fillN(frames, d.chans)
	} else {
		d.fill(frames)
	}
	if d.mono {
		mono(frames)
	}
	for i, o := range d.outs {
		if o.ch < 0 {
			continue
		}
		bytes, _ := o.typ.size()
		write(unsafe.Slice(ptr[byte](infos[i].buffers[half]), n*bytes), frames, o.ch, d.chans, o.typ)
	}
	if d.ready != 0 {
		_, _, _ = syscall.SyscallN(d.ready, d.obj)
	}
}

// askReset tells the device's user, once, that the driver wants to be
// opened again. Messages as the driver starts, before it plays, are
// let be.
func (d *Device) askReset() {
	if !d.live.Load() {
		d.logf("asio: an ask to be opened again, as the driver starts: let be")
		return
	}
	d.resetOnce.Do(func() {
		d.logf("asio: the driver asks to be opened again")
		if d.reset != nil {
			go d.reset()
		}
	})
}

// resetIfMoved asks for a reset where the driver's rate or latency
// moved from what the device plays with: a driver may tell of a change
// that leaves both as they were.
func (d *Device) resetIfMoved() {
	var rate float64
	var in, out int32
	if !d.t.run(func() {
		if d.started {
			rate = d.rate()
			_ = d.call(mGetLatencies, uintptr(unsafe.Pointer(&in)), uintptr(unsafe.Pointer(&out)))
		}
	}) {
		return
	}
	if int(math.Round(rate)) != d.info.Rate || int(out) != d.info.Latency {
		d.askReset()
	}
}

// makeCallbacks makes the functions the driver calls. A long, 32 bits,
// comes in a register of 64, the top half of which is left as it was:
// each is cut to its 32 bits.
//
// bufferSwitch and bufferSwitchTimeInfo come on the driver's own
// thread, which the driver has raised to the priority it plays at:
// they leave its priority as it is. The goroutine that fills the ring
// they take from raises its own, by package audio/rt.
func makeCallbacks() {
	cbs = callbacks{
		bufferSwitch: syscall.NewCallback(func(half, _ uintptr) uintptr {
			if d := playing.Load(); d != nil {
				d.switchTo(int32(uint32(half)))
			}
			return 0
		}),
		// The new rate comes as a double, in a register a callback of
		// Go's reads none of: the driver is asked for it.
		sampleRateDidChange: syscall.NewCallback(func(uintptr) uintptr {
			if d := playing.Load(); d != nil && d.live.Load() {
				d.logf("asio: the driver's rate changed")
				go d.resetIfMoved()
			}
			return 0
		}),
		asioMessage: syscall.NewCallback(func(selector, value, _, _ uintptr) uintptr {
			if d := playing.Load(); d != nil {
				if name := messageNames[int32(uint32(selector))]; name != "" {
					d.logf("asio: the driver says: %s", name)
				}
			}
			switch int32(uint32(selector)) {
			case msgSelectorSupported:
				switch int32(uint32(value)) {
				case msgEngineVersion, msgResetRequest, msgBufferSizeChange, msgResyncRequest,
					msgLatenciesChanged, msgSupportsTimeInfo:
					return 1
				}
				return 0
			case msgEngineVersion:
				return 2
			case msgResetRequest, msgBufferSizeChange:
				if d := playing.Load(); d != nil {
					d.askReset()
				}
				return 1
			case msgLatenciesChanged:
				if d := playing.Load(); d != nil && d.live.Load() {
					go d.resetIfMoved()
				}
				return 1
			case msgResyncRequest, msgSupportsTimeInfo:
				return 1
			}
			return 0
		}),
		bufferSwitchTimeInfo: syscall.NewCallback(func(params, half, _ uintptr) uintptr {
			if d := playing.Load(); d != nil {
				d.switchTo(int32(uint32(half)))
			}
			return params
		}),
	}
}

// sta is a thread of its own, in a single-threaded COM apartment, as
// ASIO drivers expect, that runs calls one at a time and passes on the
// window messages a driver's control panel needs between them.
type sta struct {
	calls chan func()
	wake  windows.Handle
	done  chan struct{}
	// mu guards stopped, so a call after stop finds the thread gone.
	mu      sync.Mutex
	stopped bool
}

func newSTA() (*sta, error) {
	wake, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		return nil, err
	}
	s := &sta{calls: make(chan func(), 1), wake: wake, done: make(chan struct{})}
	errc := make(chan error)
	go func() {
		defer close(s.done)
		// The thread ends with the goroutine, its COM state with it.
		runtime.LockOSThread()
		if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); err != nil && !errors.Is(err, syscall.Errno(windows.S_FALSE)) {
			errc <- err
			return
		}
		defer windows.CoUninitialize()
		errc <- nil
		s.loop()
	}()
	if err := <-errc; err != nil {
		_ = windows.CloseHandle(wake)
		return nil, err
	}
	return s, nil
}

// msg is a window message, as MSG lays it out.
type msg struct {
	hwnd     uintptr
	message  uint32
	wParam   uintptr
	lParam   uintptr
	time     uint32
	pt       [2]int32
	lPrivate uint32
}

func (s *sta) loop() {
	const (
		qsAllInput         = 0x04ff
		mwmoInputAvailable = 0x0004
		pmRemove           = 0x0001
	)
	for {
		for drained := false; !drained; {
			select {
			case f, ok := <-s.calls:
				if !ok {
					return
				}
				f()
			default:
				drained = true
			}
		}
		_, _, _ = procMsgWaitForMultipleObjEx.Call(1, uintptr(unsafe.Pointer(&s.wake)), uintptr(windows.INFINITE),
			qsAllInput, mwmoInputAvailable)
		var m msg
		for {
			if r, _, _ := procPeekMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, pmRemove); r == 0 {
				break
			}
			_, _, _ = procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
			_, _, _ = procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
		}
	}
}

// run calls f on the thread and waits for it. It reports false, f
// uncalled, once the thread has stopped.
func (s *sta) run(f func()) bool {
	done := s.start(f)
	if done == nil {
		return false
	}
	<-done
	return true
}

// start has the thread call f, and returns a channel closed once f
// returns, or nil, f uncalled, once the thread has stopped.
func (s *sta) start(f func()) <-chan struct{} {
	done := make(chan struct{})
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return nil
	}
	s.calls <- func() {
		defer close(done)
		f()
	}
	s.mu.Unlock()
	_ = windows.SetEvent(s.wake)
	return done
}

// stop ends the thread.
func (s *sta) stop() {
	s.mu.Lock()
	s.stopped = true
	close(s.calls)
	s.mu.Unlock()
	_ = windows.SetEvent(s.wake)
	<-s.done
	_ = windows.CloseHandle(s.wake)
}
