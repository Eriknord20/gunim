//go:build linux && !android && (amd64 || arm64)

package jack

import (
	"errors"
	"fmt"
	"math/bits"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// maxBuffer is the largest buffer the rings make room for, beyond Hold:
// JACK's and PipeWire's largest.
const maxBuffer = 8192

// A Client is a client of the JACK server, playing, and recording where
// Config.Record says. Write, Read and Poll are one goroutine's; the rest
// are safe from any.
type Client struct {
	a   *api
	c   uintptr
	st  *rtState
	log func(format string, args ...any)
	// out and in are the rings' samples, held here while the server
	// writes and reads them through st.
	out, in []float32
	size    uint64
	// chans is how many channels the outputs play, ins how many inputs
	// it records.
	chans, ins int
	outPorts   []uintptr
	inPorts    []uintptr
	// mu guards info.
	mu   sync.Mutex
	info Info
	// buffer, latency and inLatency are info's, for any goroutine.
	buffer, latency, inLatency atomic.Int64
	// What Poll last read, and Read: the cycles read, and where in the
	// input ring the next frame is.
	polled struct{ short, xruns, lost, graphs uint64 }
	seen   uint64
	// closing says Close has begun: SinceCycle, which any goroutine
	// calls, then calls libjack no more, and Close waits out the
	// readers in it before the client is let go.
	closing atomic.Bool
	readers atomic.Int32
}

// Available reports whether a JACK server answers.
func Available() bool {
	a, c, err := connect(clientName(""))
	if err != nil {
		return false
	}
	a.clientClose(c)
	return true
}

// Ports returns the names of the system's inputs and outputs, as the
// server lists its physical ports.
func Ports() (inputs, outputs []string, err error) {
	a, c, err := connect(clientName(""))
	if err != nil {
		return nil, nil, err
	}
	defer a.clientClose(c)
	return a.ports(c, portIsPhysical|portIsOutput), a.ports(c, portIsPhysical|portIsInput), nil
}

// Open opens a client of the server, as c says, and starts it.
func Open(cfg Config) (*Client, error) {
	a, c, err := connect(clientName(cfg.Name))
	if err != nil {
		return nil, err
	}
	cl := &Client{a: a, c: c, st: new(rtState), log: cfg.Log}
	if err := cl.start(cfg); err != nil {
		a.clientClose(c)
		return nil, err
	}
	return cl, nil
}

func (cl *Client) logf(format string, args ...any) {
	if cl.log != nil {
		cl.log(format, args...)
	}
}

// start registers the ports, sets the callbacks, activates the client
// and connects its ports.
func (cl *Client) start(cfg Config) error {
	a, c, st := cl.a, cl.c, cl.st
	rate, buffer := int(a.sampleRate(c)), int(a.bufferSize(c))
	if rate <= 0 {
		return errors.New("jack: the server reports no rate")
	}
	info := Info{Client: a.clientName(c), Library: a.path, Rate: rate, Buffer: buffer,
		Outputs: a.ports(c, portIsPhysical|portIsInput), Inputs: a.ports(c, portIsPhysical|portIsOutput)}
	info.Played = layout(cfg.Outputs, len(info.Outputs))
	if len(info.Played) == 0 {
		return fmt.Errorf("jack: the server has none of the outputs %v", cfg.Outputs)
	}
	if cfg.Record {
		info.Recorded = min(len(info.Inputs), MaxPorts)
	}
	cl.chans, cl.ins = len(info.Played), info.Recorded
	hold := max(cfg.Hold, 0)
	cl.size = 1 << bits.Len64(uint64(int64(hold)*int64(rate)/int64(time.Second)+2*maxBuffer+4096-1))
	cl.out = make([]float32, int(cl.size)*cl.chans)
	if cl.ins > 0 {
		cl.in = make([]float32, int(cl.size)*cl.ins)
	}
	for k := range cl.chans {
		p := a.portRegister(c, fmt.Sprintf("out_%d", k+1), audioType, portIsOutput, 0)
		if p == 0 {
			return fmt.Errorf("jack: the server refused output port %d", k+1)
		}
		cl.outPorts = append(cl.outPorts, p)
		st.outPort[k] = p
	}
	for k := range cl.ins {
		p := a.portRegister(c, fmt.Sprintf("in_%d", k+1), audioType, portIsInput, 0)
		if p == 0 {
			return fmt.Errorf("jack: the server refused input port %d", k+1)
		}
		cl.inPorts = append(cl.inPorts, p)
		st.inPort[k] = p
	}
	st.getBuffer = a.getBuffer
	st.outs, st.ins = uint64(cl.chans), uint64(cl.ins)
	st.mask = cl.size - 1
	st.outBuf = uintptr(unsafe.Pointer(&cl.out[0]))
	if cl.ins > 0 {
		st.inBuf = uintptr(unsafe.Pointer(&cl.in[0]))
	}
	st.on, st.bufSize, st.rate = 1, uint64(buffer), uint64(rate)
	arg := uintptr(unsafe.Pointer(st))
	for _, set := range []struct {
		name string
		f    func(c, fn, arg uintptr) int32
		fn   uintptr
	}{
		{"process", a.setProcess, procs.process},
		{"buffer size", a.setBufferSize, procs.bufSize},
		{"sample rate", a.setRate, procs.rate},
		{"xrun", a.setXrun, procs.xrun},
		{"graph order", a.setGraph, procs.graph},
	} {
		if set.f(c, set.fn, arg) != 0 {
			return fmt.Errorf("jack: the server refused the %s callback", set.name)
		}
	}
	a.onShutdown(c, procs.shutdown, arg)
	if a.activate(c) != 0 {
		return errors.New("jack: the server refused to activate the client")
	}
	for k, p := range info.Played {
		if p < 0 {
			continue
		}
		if a.connect(c, a.portName(cl.outPorts[k]), info.Outputs[p]) != 0 {
			cl.logf("jack: connecting %s to %s failed", a.portName(cl.outPorts[k]), info.Outputs[p])
			info.Played[k] = -1
		}
	}
	for k := range cl.ins {
		if a.connect(c, info.Inputs[k], a.portName(cl.inPorts[k])) != 0 {
			cl.logf("jack: connecting %s to %s failed", info.Inputs[k], a.portName(cl.inPorts[k]))
		}
	}
	cl.info = info
	cl.buffer.Store(int64(buffer))
	cl.readLatencies()
	return nil
}

// layout returns the system output each channel plays on, of outs
// outputs: as want names them, or, where it names none, the first two,
// or the one, or two unconnected. A channel whose output is missing, or
// named before, plays on none: -1.
func layout(want []int, outs int) []int {
	if want == nil {
		switch outs {
		case 0:
			return []int{-1, -1}
		case 1:
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

// readLatencies reads the ports' latencies anew: the largest of the
// client's own ports, as the server reckons them from the graph, and of
// the system's ports they connect to, which the server knows at once.
func (cl *Client) readLatencies() {
	if atomic.LoadUint64(&cl.st.shutdown) != 0 {
		return
	}
	a := cl.a
	most := func(ports []uintptr, mode int32) int {
		n := 0
		for _, p := range ports {
			var r latencyRange
			a.latency(p, mode, &r)
			n = max(n, int(r.max))
			for _, name := range a.connections(p) {
				if q := a.portByName(cl.c, name); q != 0 {
					a.latency(q, mode, &r)
					n = max(n, int(r.max))
				}
			}
		}
		return n
	}
	out, in := most(cl.outPorts, playbackLatency), most(cl.inPorts, captureLatency)
	cl.mu.Lock()
	changed := out != cl.info.Latency || in != cl.info.InputLatency
	cl.info.Latency, cl.info.InputLatency = out, in
	cl.mu.Unlock()
	cl.latency.Store(int64(out))
	cl.inLatency.Store(int64(in))
	if changed {
		cl.logf("jack: %d frames of output latency and %d of input, at %d frames a buffer", out, in, cl.buffer.Load())
	}
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

// Channels returns how many channels a frame Write takes holds: one for
// each output port.
func (cl *Client) Channels() int { return cl.chans }

// Buffer returns the frames the server hands the client at a time, as
// Poll last saw it.
func (cl *Client) Buffer() int { return int(cl.buffer.Load()) }

// Latency returns Info's Latency and InputLatency, as Poll last read
// them.
func (cl *Client) Latency() (out, in int) {
	return int(cl.latency.Load()), int(cl.inLatency.Load())
}

// Write adds frames, of Channels interleaved samples, to the ring the
// outputs play from, as many as it has room for, and returns how many.
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

// Queued returns how many frames written the server has not taken.
func (cl *Client) Queued() int64 {
	r := atomic.LoadUint64(&cl.st.outR)
	return int64(atomic.LoadUint64(&cl.st.outW) - r)
}

// Room returns how many frames Write has room for.
func (cl *Client) Room() int64 { return int64(cl.size) - cl.Queued() }

// Taken returns how many frames written the server has taken, ever.
func (cl *Client) Taken() int64 { return int64(atomic.LoadUint64(&cl.st.outR)) }

// Inputs returns how many channels Read's frames hold: one for each
// input recorded.
func (cl *Client) Inputs() int { return cl.ins }

// Read takes what the inputs recorded in the oldest buffer not yet
// read, as many frames as dst holds, of Inputs interleaved samples. It
// returns how many, and at, the frame the outputs took, as Taken counts
// them, at the same moment of the buffer as the first of them: the
// outputs took frame at as the inputs took it. ok is false where
// nothing waits. Where Read falls behind by logSize buffers, those
// buffers are lost.
func (cl *Client) Read(dst []float32) (n int, at int64, ok bool) {
	st, ch := cl.st, cl.ins
	if ch == 0 || len(dst) < ch {
		return 0, 0, false
	}
	for {
		cycles := atomic.LoadUint64(&st.cycles)
		if cl.seen == cycles {
			return 0, 0, false
		}
		if cycles-cl.seen >= logSize {
			// The log has moved past the buffers not read.
			cl.seen = cycles - logSize/2
			continue
		}
		i := cl.seen & (logSize - 1)
		in0 := atomic.LoadUint64(&st.logIn[i])
		out0 := atomic.LoadUint64(&st.logOut[i])
		k := atomic.LoadUint64(&st.logN[i])
		if atomic.LoadUint64(&st.cycles)-cl.seen >= logSize {
			// Written over as it was read.
			continue
		}
		r := atomic.LoadUint64(&st.inR)
		if r < in0 {
			// Buffers skipped above: their frames go.
			r = in0
		}
		if r >= in0+k {
			cl.seen++
			atomic.StoreUint64(&st.inR, r)
			continue
		}
		m := min(in0+k-r, uint64(len(dst)/ch))
		for c := range ch {
			ring := cl.in[uint64(c)*cl.size : uint64(c+1)*cl.size]
			for j := range m {
				dst[int(j)*ch+c] = ring[(r+j)&st.mask]
			}
		}
		atomic.StoreUint64(&st.inR, r+m)
		if r+m == in0+k {
			cl.seen++
		}
		return int(m), int64(out0 + (r - in0)), true
	}
}

// SinceCycle returns how many frames have passed since the server
// started the buffer it plays now: the server's frame time. It is 0
// once the server has shut down, or the client closes.
func (cl *Client) SinceCycle() int {
	cl.readers.Add(1)
	defer cl.readers.Add(-1)
	if cl.closing.Load() || atomic.LoadUint64(&cl.st.shutdown) != 0 {
		return 0
	}
	return int(cl.a.sinceCycle(cl.c))
}

// Poll returns what happened since it was last called. It reads the
// latencies again where the buffer size or the graph changed.
func (cl *Client) Poll() Events {
	st := cl.st
	delta := func(v *uint64, last *uint64) int64 {
		now := atomic.LoadUint64(v)
		d := int64(now - *last)
		*last = now
		return d
	}
	e := Events{
		Short:    delta(&st.short, &cl.polled.short),
		Xruns:    delta(&st.xruns, &cl.polled.xruns),
		Lost:     delta(&st.lost, &cl.polled.lost),
		Cycles:   int64(atomic.LoadUint64(&st.cycles)),
		Buffer:   int(atomic.LoadUint64(&st.bufSize)),
		Rate:     int(atomic.LoadUint64(&st.rate)),
		Shutdown: atomic.LoadUint64(&st.shutdown) != 0,
	}
	e.Graph = delta(&st.graphs, &cl.polled.graphs) > 0
	if e.Buffer > 0 && int64(e.Buffer) != cl.buffer.Load() {
		cl.buffer.Store(int64(e.Buffer))
		cl.mu.Lock()
		cl.info.Buffer = e.Buffer
		cl.mu.Unlock()
		e.Graph = true
	}
	if e.Graph {
		cl.readLatencies()
	}
	return e
}

// Pause stops the outputs taking from the ring, and plays silence, or
// starts them again.
func (cl *Client) Pause(paused bool) {
	on := uint64(1)
	if paused {
		on = 0
	}
	atomic.StoreUint64(&cl.st.on, on)
}

// Err returns ErrShutdown once the server has shut down.
func (cl *Client) Err() error {
	if atomic.LoadUint64(&cl.st.shutdown) != 0 {
		return ErrShutdown
	}
	return nil
}

// Close stops the client and lets it go.
// Calls after the first return nil.
func (cl *Client) Close() error {
	if !cl.closing.CompareAndSwap(false, true) {
		return nil
	}
	// A SinceCycle that read closing as false may be in libjack still.
	for cl.readers.Load() > 0 {
		time.Sleep(10 * time.Microsecond)
	}
	cl.a.deactivate(cl.c)
	err := error(nil)
	if cl.a.clientClose(cl.c) != 0 && atomic.LoadUint64(&cl.st.shutdown) == 0 {
		err = errors.New("jack: closing the client failed")
	}
	// The server's threads read these until the client closed.
	runtime.KeepAlive(cl.st)
	runtime.KeepAlive(cl.out)
	runtime.KeepAlive(cl.in)
	return err
}
