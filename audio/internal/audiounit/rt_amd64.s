//go:build (darwin && !ios) || (linux && !android)

#include "textflag.h"
#include "go_asm.h"

// The callbacks Core Audio calls, in the System V calling convention,
// which macOS follows on amd64: arguments in DI, SI, DX, CX, R8 and R9,
// the result in AX, and BX, BP and R12 to R15 kept for the caller. They
// run on Core Audio's threads, with no Go behind them: no g, no stack
// checks, no frame of Go's.
//
// On amd64 a store is seen after the stores before it, and a load
// after the loads before it, so plain MOVs publish and read in order.

// An AudioTimeStamp holds the host time at 8 and its flags at 56; bit 1
// of the flags says the host time is valid.
#define TS_HOST 8
#define TS_FLAGS 56
#define TS_HOST_VALID 2

// OSStatus auRender(rtState *st, flags *, const AudioTimeStamp *ts,
//	UInt32 bus, UInt32 n, AudioBufferList *data)
//
// It fills each of data's buffers, one channel each, from the ring's
// channel of the same index: what the ring holds, a buffer at most, then
// silence; nothing from the ring while off. Buffers past the ring's
// channels play silence.
//
// It keeps:
//	DI	st
//	R8	n, the buffer's frames
//	R9	data
//	R10	outR as the buffer began
//	R11	take, the frames the ring gives
//	CX	the buffers in data
//	SI	k, the buffer
//	DX	the rings' mask
//	R12	m, the frames buffer k holds
//	R13	buffer k's samples
//	R14	i, the frame
//	R15	the ring's channel k
TEXT ·auRender(SB), NOSPLIT|NOFRAME, $0-0
	PUSHQ	BX
	PUSHQ	R12
	PUSHQ	R13
	PUSHQ	R14
	PUSHQ	R15
	MOVL	R8, R8

	// The log first: the host time, where Core Audio gave one, and
	// the counts as the buffer begins.
	MOVQ	rtState_cycles(DI), AX
	ANDQ	$(const_logSize-1), AX
	XORQ	R10, R10
	TESTQ	DX, DX
	JEQ	nohost
	MOVL	TS_FLAGS(DX), R11
	TESTL	$TS_HOST_VALID, R11
	JEQ	nohost
	MOVQ	TS_HOST(DX), R10

nohost:
	MOVQ	R10, rtState_logHost(DI)(AX*8)
	MOVQ	rtState_played(DI), R10
	MOVQ	R10, rtState_logPlayed(DI)(AX*8)
	MOVQ	rtState_outR(DI), R10
	MOVQ	R10, rtState_logR(DI)(AX*8)

	// The ring gives what it holds, a buffer at most; nothing while
	// off.
	XORQ	R11, R11
	CMPQ	rtState_on(DI), $0
	JEQ	taken
	MOVQ	rtState_outW(DI), R11
	SUBQ	R10, R11
	CMPQ	R11, R8
	JLS	fits
	MOVQ	R8, R11

fits:
	CMPQ	R11, R8
	JEQ	taken
	INCQ	rtState_short(DI)

taken:
	MOVQ	R11, rtState_logTook(DI)(AX*8)
	XORQ	CX, CX
	TESTQ	R9, R9
	JEQ	bufdone
	MOVL	0(R9), CX
	MOVQ	rtState_mask(DI), DX
	XORQ	SI, SI

bufloop:
	CMPQ	SI, CX
	JAE	bufdone

	// Buffer k is 16 bytes at 8+16k: its channels, its bytes, and its
	// samples.
	MOVQ	SI, AX
	SHLQ	$4, AX
	LEAQ	8(R9)(AX*1), AX
	MOVL	4(AX), R12
	SHRQ	$2, R12
	CMPQ	R12, R8
	JLS	sized
	MOVQ	R8, R12

sized:
	MOVQ	8(AX), R13
	TESTQ	R13, R13
	JEQ	bufnext
	XORQ	R14, R14
	CMPQ	SI, rtState_outs(DI)
	JAE	zero
	LEAQ	1(DX), R15
	IMULQ	SI, R15
	SHLQ	$2, R15
	ADDQ	rtState_outBuf(DI), R15

copy:
	CMPQ	R14, R11
	JAE	zero
	CMPQ	R14, R12
	JAE	bufnext
	LEAQ	(R10)(R14*1), AX
	ANDQ	DX, AX
	MOVL	(R15)(AX*4), BX
	MOVL	BX, (R13)(R14*4)
	INCQ	R14
	JMP	copy

zero:
	CMPQ	R14, R12
	JAE	bufnext
	MOVL	$0, (R13)(R14*4)
	INCQ	R14
	JMP	zero

bufnext:
	INCQ	SI
	JMP	bufloop

bufdone:
	// Published after the samples were read, then the cycle, which
	// tells the log's entry is whole.
	ADDQ	R11, R10
	MOVQ	R10, rtState_outR(DI)
	ADDQ	R8, rtState_played(DI)
	INCQ	rtState_cycles(DI)

	POPQ	R15
	POPQ	R14
	POPQ	R13
	POPQ	R12
	POPQ	BX
	XORL	AX, AX
	RET

// OSStatus auInput(rtState *st, flags *, const AudioTimeStamp *ts,
//	UInt32 bus, UInt32 n, AudioBufferList *data)
//
// It has the input unit render the buffer into the scratch, through
// st's own list, and copies it into the ring as far as it has room.
//
// It keeps:
//	BX	st
//	BP	bus
//	R12	n
//	R13	ts, then put, the frames the ring takes
//	R14	flags, then the log's index
//	R15	inW as the buffer began
TEXT ·auInput(SB), NOSPLIT|NOFRAME, $0-0
	PUSHQ	BX
	PUSHQ	BP
	PUSHQ	R12
	PUSHQ	R13
	PUSHQ	R14
	PUSHQ	R15
	SUBQ	$8, SP // 16-byte aligned for the call
	MOVQ	DI, BX
	MOVL	CX, BP
	MOVL	R8, R12
	MOVQ	DX, R13
	MOVQ	SI, R14
	CMPQ	rtState_ins(BX), $0
	JEQ	indone
	CMPQ	R12, $const_scratchFrames
	JLS	room
	ADDQ	R12, rtState_lost(BX)
	JMP	indone

room:
	// The list: a buffer of one channel and n frames for each input,
	// in the scratch.
	MOVQ	rtState_ins(BX), AX
	MOVL	AX, rtState_inList(BX)
	LEAQ	rtState_inList(BX), DX
	ADDQ	$8, DX
	MOVQ	R12, R8
	SHLQ	$2, R8
	MOVQ	rtState_scratch(BX), R9
	XORQ	CX, CX

list:
	CMPQ	CX, AX
	JAE	listed
	MOVL	$1, 0(DX)
	MOVL	R8, 4(DX)
	MOVQ	R9, 8(DX)
	ADDQ	$16, DX
	ADDQ	$(const_scratchFrames*4), R9
	INCQ	CX
	JMP	list

listed:
	// AudioUnitRender(inUnit, flags, ts, bus, n, &inList)
	MOVQ	rtState_inUnit(BX), DI
	MOVQ	R14, SI
	MOVQ	R13, DX
	MOVL	BP, CX
	MOVL	R12, R8
	LEAQ	rtState_inList(BX), R9
	MOVQ	rtState_render(BX), AX
	CALL	AX
	TESTL	AX, AX
	JEQ	rendered
	INCQ	rtState_failed(BX)
	JMP	indone

rendered:
	// The log: the host time, the ring's count, and the frames put.
	MOVQ	rtState_inCycles(BX), R14
	ANDQ	$(const_logSize-1), R14
	XORQ	AX, AX
	TESTQ	R13, R13
	JEQ	inhost
	MOVL	TS_FLAGS(R13), CX
	TESTL	$TS_HOST_VALID, CX
	JEQ	inhost
	MOVQ	TS_HOST(R13), AX

inhost:
	MOVQ	AX, rtState_inLogHost(BX)(R14*8)
	MOVQ	rtState_inW(BX), R15
	MOVQ	R15, rtState_inLogW(BX)(R14*8)

	// put is n, as far as the ring has room.
	MOVQ	rtState_mask(BX), CX
	INCQ	CX
	MOVQ	R15, AX
	SUBQ	rtState_inR(BX), AX
	SUBQ	AX, CX
	MOVQ	R12, R13
	CMPQ	R13, CX
	JLS	putfits
	MOVQ	CX, R13

putfits:
	MOVQ	R12, AX
	SUBQ	R13, AX
	ADDQ	AX, rtState_lost(BX)
	MOVQ	R13, rtState_inLogN(BX)(R14*8)

	// Each channel from the buffer the unit rendered it into, which
	// may be its own: the list says where.
	MOVQ	rtState_mask(BX), DX
	XORQ	SI, SI

inloop:
	CMPQ	SI, rtState_ins(BX)
	JAE	incopied
	MOVQ	SI, AX
	SHLQ	$4, AX
	LEAQ	rtState_inList(BX), R9
	MOVQ	16(R9)(AX*1), R9
	TESTQ	R9, R9
	JEQ	innext
	LEAQ	1(DX), R8
	IMULQ	SI, R8
	SHLQ	$2, R8
	ADDQ	rtState_inBuf(BX), R8
	XORQ	CX, CX

incopy:
	CMPQ	CX, R13
	JAE	innext
	LEAQ	(R15)(CX*1), AX
	ANDQ	DX, AX
	MOVL	(R9)(CX*4), R10
	MOVL	R10, (R8)(AX*4)
	INCQ	CX
	JMP	incopy

innext:
	INCQ	SI
	JMP	inloop

incopied:
	ADDQ	R13, R15
	MOVQ	R15, rtState_inW(BX)
	INCQ	rtState_inCycles(BX)

indone:
	ADDQ	$8, SP
	POPQ	R15
	POPQ	R14
	POPQ	R13
	POPQ	R12
	POPQ	BP
	POPQ	BX
	XORL	AX, AX
	RET

// OSStatus auChanged(AudioObjectID id, UInt32 n,
//	const AudioObjectPropertyAddress *addrs, uint64_t *changes)
TEXT ·auChanged(SB), NOSPLIT|NOFRAME, $0-0
	MOVQ	$1, AX
	LOCK
	XADDQ	AX, 0(CX)
	XORL	AX, AX
	RET

// OSStatus auTestRender(testSource *src, flags *, const AudioTimeStamp *ts,
//	UInt32 bus, UInt32 n, AudioBufferList *data)
TEXT ·auTestRender(SB), NOSPLIT|NOFRAME, $0-0
	MOVL	R8, R8
	MOVL	0(R9), CX
	CMPQ	CX, testSource_chans(DI)
	JLS	chans
	MOVQ	testSource_chans(DI), CX

chans:
	XORQ	SI, SI

tloop:
	CMPQ	SI, CX
	JAE	tdone
	MOVQ	SI, AX
	SHLQ	$4, AX
	MOVQ	16(R9)(AX*1), R10
	MOVQ	testSource_src(DI)(SI*8), R11
	XORQ	DX, DX

tcopy:
	CMPQ	DX, R8
	JAE	tnext
	MOVL	(R11)(DX*4), AX
	MOVL	AX, (R10)(DX*4)
	INCQ	DX
	JMP	tcopy

tnext:
	INCQ	SI
	JMP	tloop

tdone:
	// What a C function may leave changed, changed.
	MOVQ	testSource_status(DI), AX
	MOVQ	$-1, CX
	MOVQ	CX, DX
	MOVQ	CX, SI
	MOVQ	CX, DI
	MOVQ	CX, R8
	MOVQ	CX, R9
	MOVQ	CX, R10
	MOVQ	CX, R11
	RET

// func callbackAddrs(a *[4]uintptr)
TEXT ·callbackAddrs(SB), NOSPLIT, $0-8
	MOVQ	a+0(FP), DI
	MOVQ	$·auRender(SB), AX
	MOVQ	AX, 0(DI)
	MOVQ	$·auInput(SB), AX
	MOVQ	AX, 8(DI)
	MOVQ	$·auChanged(SB), AX
	MOVQ	AX, 16(DI)
	MOVQ	$·auTestRender(SB), AX
	MOVQ	AX, 24(DI)
	RET
