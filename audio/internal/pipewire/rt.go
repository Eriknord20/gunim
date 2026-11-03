//go:build linux && !android && (amd64 || arm64)

package pipewire

import "unsafe"

// logSize is how many cycles the input's process function notes, for
// Read to stamp what it recorded with: a power of two.
const logSize = 128

// timeSize is the size of a pw_time, as PipeWire 1.0 has it: the
// process functions ask pw_stream_get_time_n for this much, and an
// older PipeWire fills what it knows of.
const timeSize = 64

// pwTime is timeSize bytes: the index is out of range otherwise.
var _ = [1]struct{}{}[unsafe.Sizeof(pwTime{})-timeSize]

// pwTime is a struct pw_time: PipeWire's time report for a stream's
// cycle. now is when the cycle began, on CLOCK_MONOTONIC, in
// nanoseconds. delay is the time from the stream to the device, in
// ticks of the graph's rate, rateNum/rateDenom seconds each: for an
// output, until the next frame queued is heard, past those queued;
// for an input, from the device to the stream. queued is what the
// stream holds queued, in the units each pw_buffer's size gives, here
// frames; buffered the frames the stream's resampler holds.
type pwTime struct {
	now                   int64
	rateNum, rateDenom    uint32
	ticks                 uint64
	delay                 int64
	queued, buffered      uint64
	queuedBufs, availBufs uint32
	size                  uint64
}

// logEntry is what the input's process function notes of a cycle: the
// input ring's count as it began, the frames it wrote to the ring, the
// frames it was handed, of which those past n found the ring full, and
// the cycle's time report.
type logEntry struct {
	in, n, got uint64
	time       pwTime
}

// rtState is what the process functions and the state callbacks share
// with Go. PipeWire's threads run them with a pointer to it; they read
// and write it with plain loads and stores, release stores where Go
// reads what they wrote before, and Go uses package atomic. It holds no
// Go pointers: the rings are Go memory, held by the Client, so the
// collector neither frees nor moves them while the streams run.
//
// The rings hold frames of two float32s, interleaved, read and written
// as one 64-bit word each. Counters count frames ever, so a frame's
// place is its count masked.
type rtState struct {
	// dequeue, queue and getTime are pw_stream_dequeue_buffer,
	// pw_stream_queue_buffer and pw_stream_get_time_n.
	dequeue, queue, getTime uintptr
	// outStream and inStream are the streams, 0 where there is none.
	outStream, inStream uintptr
	// fallback is how many frames the output plays in a cycle whose
	// buffer asks for none.
	fallback uint64
	// on is 1 while the output plays; 0 plays silence and takes nothing.
	on uint64
	// outBuf is the output ring's first frame, outMask its size in
	// frames less one. outW counts the frames Go wrote to it, outR those
	// the process function took.
	outBuf     uintptr
	outMask    uint64
	outW, outR uint64
	// cycles counts the output's cycles; short those the ring could not
	// fill while on; empty those that found no buffer.
	cycles, short, empty uint64
	// seq is odd while the output's process function changes outR, took
	// and outTime, and even once they agree: a sequence lock.
	seq uint64
	// took is how many frames the output's last cycle played, silence
	// included, and outTime is its time report.
	took    uint64
	outTime pwTime
	// The input ring, as the output's: inW counts the frames the process
	// function wrote, inR those Go read. inCycles counts the cycles that
	// brought frames, and lost the frames that found the ring full.
	inBuf          uintptr
	inMask         uint64
	inW, inR       uint64
	inCycles, lost uint64
	// The streams' states, as their state callbacks last told, and how
	// many times each told.
	outState, outStates, inState, inStates uint64
	// The input's log: for each cycle that brought frames, at index
	// inCycles mod logSize.
	log [logSize]logEntry
}

// fakeStream is a test's stand-in for a pw_stream, as the fake
// functions below read it: next points to a list of pw_buffers to hand
// out, ended by a null; queued is the last buffer queued, and queues
// how many were; time is the report to give.
type fakeStream struct {
	next, queued uintptr
	queues       uint64
	time         pwTime
}

// The callbacks are in assembly, and have no Go declarations, as Go
// never calls them. Each follows the C calling convention of its
// processor:
//
//	void pwProcess(rtState *st);
//	void pwCapture(rtState *st);
//	void pwOutState(rtState *st, int old, int state, const char *error);
//	void pwInState(rtState *st, int old, int state, const char *error);
//	struct pw_buffer *fakeDequeue(fakeStream *s);
//	int fakeQueue(fakeStream *s, struct pw_buffer *b);
//	int fakeGetTime(fakeStream *s, struct pw_time *t, size_t size);
//
// pwProcess fills the output's buffer from its ring, and pwCapture
// empties the input's buffers into its ring. The fakes stand in for
// libpipewire's functions in a test.

// callbackAddrs fills a with the callbacks' addresses, in the order
// above.
//
//go:noescape
func callbackAddrs(a *[7]uintptr)

// The callbacks' addresses.
var procs = func() (p struct {
	process, capture, outState, inState, fakeDequeue, fakeQueue, fakeGetTime uintptr
}) {
	var a [7]uintptr
	callbackAddrs(&a)
	p.process, p.capture, p.outState, p.inState = a[0], a[1], a[2], a[3]
	p.fakeDequeue, p.fakeQueue, p.fakeGetTime = a[4], a[5], a[6]
	return p
}()
