//go:build linux && !android && (amd64 || arm64)

package jack

// logSize is how many buffers the process function notes, for Read to
// stamp the inputs with: a power of two. Its arrays sit in rtState
// where arm64's assembly reaches them with one instruction.
const logSize = 128

// rtState is what the process function and the other callbacks share
// with Go. The server's threads run the callbacks with a pointer to it;
// they read and write it with plain loads and stores, release stores
// where Go reads what they wrote before, and Go uses package atomic.
// It holds no Go pointers: the rings are Go memory, held by the Client,
// so the collector neither frees nor moves them while the server runs.
//
// The rings are planar: channel k of a ring of size frames starts k*size
// samples in. Counters count frames ever, so a frame's place is its
// count masked.
type rtState struct {
	// getBuffer is jack_port_get_buffer.
	getBuffer uintptr
	// outs and ins are how many output and input ports there are.
	outs, ins uint64
	// mask is the rings' size in frames, less one.
	mask uint64
	// outBuf and inBuf are the rings' first samples.
	outBuf, inBuf uintptr
	// outW counts the frames Go wrote to the output ring, outR those the
	// process function took. inW counts the frames it wrote to the input
	// ring, inR those Go read.
	outW, outR, inW, inR uint64
	// on is 1 while the outputs play; 0 plays silence and takes nothing.
	on uint64
	// cycles counts the buffers taken; short those the ring could not
	// fill while on; lost the input frames the ring had no room for.
	cycles, short, lost uint64
	// nframes is the size of the last buffer taken.
	nframes uint64
	// What the other callbacks set: the xruns and graph changes, the
	// buffer size and rate the server last told, and 1 once it shut
	// down.
	xruns, graphs, bufSize, rate, shutdown uint64
	// The ports, by their handles.
	outPort [MaxPorts]uintptr
	inPort  [MaxPorts]uintptr
	// The log: for each buffer, at index cycles mod logSize, the input
	// ring's count as it began, the output ring's, and how many frames
	// of input it wrote.
	logIn  [logSize]uint64
	logOut [logSize]uint64
	logN   [logSize]uint64
}

// The callbacks are in assembly, and have no Go declarations, as Go
// never calls them. Each follows the C calling convention of its
// processor:
//
//	int jackProcess(jack_nframes_t nframes, rtState *st);
//	int jackBufferSize(jack_nframes_t nframes, rtState *st);
//	int jackRate(jack_nframes_t nframes, rtState *st);
//	int jackXrun(rtState *st);
//	int jackGraph(rtState *st);
//	void jackShutdown(rtState *st);
//	void jackSilent(const char *msg);
//	void *jackSame(void *port, jack_nframes_t nframes);
//
// jackSilent is the error and info function: libjack's messages go
// nowhere, as jackd's tells of a server it found missing on stderr.
// jackSame returns its first argument: a test's stand-in for
// jack_port_get_buffer, its ports' handles their buffers.

// callbackAddrs fills a with the callbacks' addresses, in the order
// above.
//
//go:noescape
func callbackAddrs(a *[8]uintptr)

// The callbacks' addresses.
var procs = func() (p struct {
	process, bufSize, rate, xrun, graph, shutdown, silent, same uintptr
}) {
	var a [8]uintptr
	callbackAddrs(&a)
	p.process, p.bufSize, p.rate, p.xrun, p.graph, p.shutdown, p.silent, p.same = a[0], a[1], a[2], a[3], a[4], a[5], a[6], a[7]
	return p
}()
