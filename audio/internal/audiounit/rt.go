//go:build ((darwin && !ios) || (linux && !android)) && (amd64 || arm64)

package audiounit

// logSize is how many buffers each callback notes: a power of two. The
// arrays sit in rtState where arm64's assembly reaches them with one
// instruction.
const logSize = 128

// scratchFrames is the most frames the input callback takes at once: the
// room it has the input unit render into. Core Audio hands on 4096 at
// most, unless asked for more, and the client asks it for this many.
const scratchFrames = 8192

// rtState is what the callbacks share with Go. Core Audio's threads run
// the callbacks with a pointer to it; they read and write it with plain
// loads and stores, release stores where Go reads what they wrote
// before, and Go uses package atomic. It holds no Go pointers: the rings
// and the scratch are Go memory, held by the Client, so the collector
// neither frees nor moves them while the units run.
//
// The rings are planar: channel k of a ring of size frames starts k*size
// samples in. Counters count frames ever, so a frame's place is its
// count masked.
type rtState struct {
	// render is AudioUnitRender, which the input callback calls, and
	// inUnit the unit it calls it on.
	render, inUnit uintptr
	// outs and ins are how many channels the output plays and the input
	// records.
	outs, ins uint64
	// mask is the rings' size in frames, less one.
	mask uint64
	// outBuf and inBuf are the rings' first samples; scratch is the
	// input's room, ins channels of scratchFrames samples.
	outBuf, inBuf, scratch uintptr
	// outW counts the frames Go wrote to the output ring, outR those the
	// output took. inW counts the frames the input wrote to its ring,
	// inR those Go read.
	outW, outR, inW, inR uint64
	// played counts the frames the output handed the device, the
	// silence played past the ring's frames too.
	played uint64
	// on is 1 while the output plays; 0 plays silence and takes nothing.
	on uint64
	// cycles and inCycles count the buffers the output and the input
	// took; short those the ring could not fill while on; lost the input
	// frames the ring had no room for; failed the input's renders that
	// failed.
	cycles, inCycles, short, lost, failed uint64
	// inList is the AudioBufferList the input has the unit render into:
	// a count, then a buffer of 16 bytes for each channel.
	inList [1 + 2*MaxPorts]uint64
	// The output's log: for each buffer, at index cycles mod logSize,
	// the host time its first frame reaches the hardware (0 where Core
	// Audio gave none), the played count and the ring's count as it
	// began, and how many frames of the ring it took.
	logHost   [logSize]uint64
	logPlayed [logSize]uint64
	logR      [logSize]uint64
	logTook   [logSize]uint64
	// The input's log: for each buffer, at index inCycles mod logSize,
	// the host time its first frame reached the hardware, the ring's
	// count as it began, and how many frames it wrote.
	inLogHost [logSize]uint64
	inLogW    [logSize]uint64
	inLogN    [logSize]uint64
}

// changes counts the changes Core Audio tells the listener of. The
// listener's client data points at it: it is never freed, so a call
// Core Audio makes as a listener is removed finds it still there.
var changes uint64

// The callbacks are in assembly, and have no Go declarations, as Go
// never calls them. Each follows the C calling convention of its
// processor:
//
//	OSStatus auRender(rtState *st, AudioUnitRenderActionFlags *flags,
//		const AudioTimeStamp *ts, UInt32 bus, UInt32 frames, AudioBufferList *data);
//	OSStatus auInput(rtState *st, AudioUnitRenderActionFlags *flags,
//		const AudioTimeStamp *ts, UInt32 bus, UInt32 frames, AudioBufferList *data);
//	OSStatus auChanged(AudioObjectID id, UInt32 n,
//		const AudioObjectPropertyAddress *addrs, uint64_t *changes);
//	OSStatus auTestRender(testSource *src, AudioUnitRenderActionFlags *flags,
//		const AudioTimeStamp *ts, UInt32 bus, UInt32 frames, AudioBufferList *data);
//
// auTestRender is a test's stand-in for AudioUnitRender: it copies
// frames from the source's channels into the list's buffers, and
// clobbers the registers a C function may, as AudioUnitRender would.

// testSource is what auTestRender renders from: chans channels, each
// at src[k], and the status it returns.
type testSource struct {
	chans, status uint64
	src           [MaxPorts]uintptr
}

// callbackAddrs fills a with the callbacks' addresses, in the order
// above.
//
//go:noescape
func callbackAddrs(a *[4]uintptr)

// The callbacks' addresses.
var procs = func() (p struct{ render, input, changed, testRender uintptr }) {
	var a [4]uintptr
	callbackAddrs(&a)
	p.render, p.input, p.changed, p.testRender = a[0], a[1], a[2], a[3]
	return p
}()
