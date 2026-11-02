//go:build (darwin && !ios) || (linux && !android)

#include "textflag.h"
#include "go_asm.h"

// The callbacks Core Audio calls, in the AAPCS64 calling convention:
// arguments in R0 to R5, the result in R0, and R19 to R30 kept for the
// caller. Apple's platform reserves R18, so nothing here touches it.
// They run on Core Audio's threads, with no Go behind them: no g, no
// stack checks, no frame of Go's. R27 is the assembler's own scratch
// register, for offsets too large for one instruction, so each callback
// that may use it keeps it too.
//
// A store-release (STLR) publishes what was written before it, and a
// load-acquire (LDAR) reads what was published, as package atomic
// stores and loads do.

// An AudioTimeStamp holds the host time at 8 and its flags at 56; bit 1
// of the flags says the host time is valid.
#define TS_HOST 8
#define TS_FLAGS 56
#define TS_HOST_VALID 2

// OSStatus auRender(rtState *st, flags *, const AudioTimeStamp *ts,
//	UInt32 bus, UInt32 n, AudioBufferList *data)
//
// It keeps:
//	R0	st
//	R4	n, the buffer's frames
//	R5	data
//	R6	outR as the buffer began
//	R7	take, the frames the ring gives
//	R8	the buffers in data
//	R9	k, the buffer
//	R10	the rings' mask
//	R11	m, the frames buffer k holds
//	R12	buffer k's samples
//	R13	i, the frame
//	R14	the ring's channel k
TEXT ·auRender(SB), NOSPLIT|NOFRAME, $0-0
	SUB	$16, RSP
	MOVD	R27, 0(RSP)
	MOVWU	R4, R4

	// The log first: the host time, where Core Audio gave one, and
	// the counts as the buffer begins.
	MOVD	rtState_cycles(R0), R1
	AND	$(const_logSize-1), R1, R1
	MOVD	$0, R6
	CBZ	R2, nohost
	MOVWU	TS_FLAGS(R2), R7
	TBZ	$1, R7, nohost
	MOVD	TS_HOST(R2), R6

nohost:
	ADD	$rtState_logHost, R0, R15
	MOVD	R6, (R15)(R1<<3)
	MOVD	rtState_played(R0), R6
	ADD	$rtState_logPlayed, R0, R15
	MOVD	R6, (R15)(R1<<3)
	MOVD	rtState_outR(R0), R6
	ADD	$rtState_logR, R0, R15
	MOVD	R6, (R15)(R1<<3)

	// The ring gives what it holds, a buffer at most; nothing while
	// off.
	MOVD	$0, R7
	MOVD	rtState_on(R0), R15
	CBZ	R15, taken
	ADD	$rtState_outW, R0, R15
	LDAR	(R15), R7
	SUB	R6, R7, R7
	CMP	R4, R7
	CSEL	HI, R4, R7, R7
	CMP	R4, R7
	BEQ	taken
	MOVD	rtState_short(R0), R15
	ADD	$1, R15, R15
	MOVD	R15, rtState_short(R0)

taken:
	ADD	$rtState_logTook, R0, R15
	MOVD	R7, (R15)(R1<<3)
	MOVD	$0, R8
	CBZ	R5, bufdone
	MOVWU	0(R5), R8
	MOVD	rtState_mask(R0), R10
	MOVD	$0, R9

bufloop:
	CMP	R8, R9
	BHS	bufdone

	// Buffer k is 16 bytes at 8+16k: its channels, its bytes, and its
	// samples.
	ADD	R9<<4, R5, R15
	MOVWU	12(R15), R11
	LSR	$2, R11, R11
	CMP	R4, R11
	CSEL	HI, R4, R11, R11
	MOVD	16(R15), R12
	CBZ	R12, bufnext
	MOVD	$0, R13
	MOVD	rtState_outs(R0), R15
	CMP	R15, R9
	BHS	zero
	ADD	$1, R10, R14
	MUL	R9, R14, R14
	MOVD	rtState_outBuf(R0), R15
	ADD	R14<<2, R15, R14

copy:
	CMP	R7, R13
	BHS	zero
	CMP	R11, R13
	BHS	bufnext
	ADD	R6, R13, R15
	AND	R10, R15, R15
	MOVWU	(R14)(R15<<2), R16
	MOVW	R16, (R12)(R13<<2)
	ADD	$1, R13, R13
	B	copy

zero:
	CMP	R11, R13
	BHS	bufnext
	MOVW	ZR, (R12)(R13<<2)
	ADD	$1, R13, R13
	B	zero

bufnext:
	ADD	$1, R9, R9
	B	bufloop

bufdone:
	// Published after the samples were read, then the cycle, which
	// tells the log's entry is whole.
	ADD	R7, R6, R6
	ADD	$rtState_outR, R0, R15
	STLR	R6, (R15)
	MOVD	rtState_played(R0), R15
	ADD	R4, R15, R15
	MOVD	R15, rtState_played(R0)
	MOVD	rtState_cycles(R0), R15
	ADD	$1, R15, R15
	ADD	$rtState_cycles, R0, R16
	STLR	R15, (R16)

	MOVD	0(RSP), R27
	ADD	$16, RSP
	MOVD	$0, R0
	RET

// OSStatus auInput(rtState *st, flags *, const AudioTimeStamp *ts,
//	UInt32 bus, UInt32 n, AudioBufferList *data)
//
// It keeps:
//	R19	st
//	R20	n
//	R21	ts
//	R22	put, the frames the ring takes
//	R23	inW as the buffer began
//	R24	the log's index
TEXT ·auInput(SB), NOSPLIT|NOFRAME, $0-0
	SUB	$96, RSP
	STP	(R19, R20), 0(RSP)
	STP	(R21, R22), 16(RSP)
	STP	(R23, R24), 32(RSP)
	STP	(R25, R26), 48(RSP)
	STP	(R27, R29), 64(RSP)
	MOVD	R30, 80(RSP)
	MOVD	R0, R19
	MOVWU	R4, R20
	MOVD	R2, R21
	MOVD	rtState_ins(R19), R6
	CBZ	R6, indone
	CMP	$const_scratchFrames, R20
	BLS	room
	MOVD	rtState_lost(R19), R7
	ADD	R20, R7, R7
	MOVD	R7, rtState_lost(R19)
	B	indone

room:
	// The list: a buffer of one channel and n frames for each input,
	// in the scratch.
	ADD	$rtState_inList, R19, R7
	MOVW	R6, (R7)
	ADD	$8, R7, R7
	LSL	$2, R20, R8
	MOVD	rtState_scratch(R19), R9
	MOVD	$1, R10
	MOVD	$0, R11

list:
	CMP	R6, R11
	BHS	listed
	MOVW	R10, 0(R7)
	MOVW	R8, 4(R7)
	MOVD	R9, 8(R7)
	ADD	$16, R7, R7
	ADD	$(const_scratchFrames*4), R9, R9
	ADD	$1, R11, R11
	B	list

listed:
	// AudioUnitRender(inUnit, flags, ts, bus, n, &inList), flags and
	// bus still in R1 and R3.
	MOVD	rtState_inUnit(R19), R0
	MOVD	R21, R2
	MOVWU	R3, R3
	MOVWU	R20, R4
	ADD	$rtState_inList, R19, R5
	MOVD	rtState_render(R19), R6
	CALL	(R6)
	CBZW	R0, rendered
	MOVD	rtState_failed(R19), R0
	ADD	$1, R0, R0
	MOVD	R0, rtState_failed(R19)
	B	indone

rendered:
	// The log: the host time, the ring's count, and the frames put.
	MOVD	rtState_inCycles(R19), R24
	AND	$(const_logSize-1), R24, R24
	MOVD	$0, R0
	CBZ	R21, inhost
	MOVWU	TS_FLAGS(R21), R1
	TBZ	$1, R1, inhost
	MOVD	TS_HOST(R21), R0

inhost:
	ADD	$rtState_inLogHost, R19, R1
	MOVD	R0, (R1)(R24<<3)
	MOVD	rtState_inW(R19), R23
	ADD	$rtState_inLogW, R19, R1
	MOVD	R23, (R1)(R24<<3)

	// put is n, as far as the ring has room.
	ADD	$rtState_inR, R19, R0
	LDAR	(R0), R1
	SUB	R1, R23, R1
	MOVD	rtState_mask(R19), R2
	ADD	$1, R2, R2
	SUB	R1, R2, R2
	CMP	R2, R20
	CSEL	HI, R2, R20, R22
	SUB	R22, R20, R0
	MOVD	rtState_lost(R19), R1
	ADD	R0, R1, R1
	MOVD	R1, rtState_lost(R19)
	ADD	$rtState_inLogN, R19, R1
	MOVD	R22, (R1)(R24<<3)

	// Each channel from the buffer the unit rendered it into, which
	// may be its own: the list says where.
	MOVD	rtState_mask(R19), R3
	MOVD	$0, R4

inloop:
	MOVD	rtState_ins(R19), R0
	CMP	R0, R4
	BHS	incopied
	ADD	$rtState_inList, R19, R0
	ADD	R4<<4, R0, R0
	MOVD	16(R0), R0
	CBZ	R0, innext
	ADD	$1, R3, R5
	MUL	R4, R5, R5
	MOVD	rtState_inBuf(R19), R6
	ADD	R5<<2, R6, R5
	MOVD	$0, R6

incopy:
	CMP	R22, R6
	BHS	innext
	ADD	R23, R6, R7
	AND	R3, R7, R7
	MOVWU	(R0)(R6<<2), R8
	MOVW	R8, (R5)(R7<<2)
	ADD	$1, R6, R6
	B	incopy

innext:
	ADD	$1, R4, R4
	B	inloop

incopied:
	ADD	R22, R23, R23
	ADD	$rtState_inW, R19, R0
	STLR	R23, (R0)
	MOVD	rtState_inCycles(R19), R0
	ADD	$1, R0, R0
	ADD	$rtState_inCycles, R19, R1
	STLR	R0, (R1)

indone:
	LDP	0(RSP), (R19, R20)
	LDP	16(RSP), (R21, R22)
	LDP	32(RSP), (R23, R24)
	LDP	48(RSP), (R25, R26)
	LDP	64(RSP), (R27, R29)
	MOVD	80(RSP), R30
	ADD	$96, RSP
	MOVD	$0, R0
	RET

// OSStatus auChanged(AudioObjectID id, UInt32 n,
//	const AudioObjectPropertyAddress *addrs, uint64_t *changes)
TEXT ·auChanged(SB), NOSPLIT|NOFRAME, $0-0
again:
	LDAXR	(R3), R4
	ADD	$1, R4, R4
	STLXR	R4, (R3), R5
	CBNZ	R5, again
	MOVD	$0, R0
	RET

// OSStatus auTestRender(testSource *src, flags *, const AudioTimeStamp *ts,
//	UInt32 bus, UInt32 n, AudioBufferList *data)
TEXT ·auTestRender(SB), NOSPLIT|NOFRAME, $0-0
	SUB	$16, RSP
	MOVD	R27, 0(RSP)
	MOVWU	R4, R4
	MOVWU	0(R5), R6
	MOVD	testSource_chans(R0), R7
	CMP	R7, R6
	CSEL	HI, R7, R6, R6
	MOVD	$0, R8

tloop:
	CMP	R6, R8
	BHS	tdone
	ADD	R8<<4, R5, R9
	MOVD	16(R9), R9
	ADD	$testSource_src, R0, R10
	MOVD	(R10)(R8<<3), R10
	MOVD	$0, R11

tcopy:
	CMP	R4, R11
	BHS	tnext
	MOVWU	(R10)(R11<<2), R12
	MOVW	R12, (R9)(R11<<2)
	ADD	$1, R11, R11
	B	tcopy

tnext:
	ADD	$1, R8, R8
	B	tloop

tdone:
	// What a C function may leave changed, changed: all but R18.
	MOVD	testSource_status(R0), R0
	MOVD	$-1, R1
	MOVD	R1, R2
	MOVD	R1, R3
	MOVD	R1, R4
	MOVD	R1, R5
	MOVD	R1, R6
	MOVD	R1, R7
	MOVD	R1, R8
	MOVD	R1, R9
	MOVD	R1, R10
	MOVD	R1, R11
	MOVD	R1, R12
	MOVD	R1, R13
	MOVD	R1, R14
	MOVD	R1, R15
	MOVD	R1, R16
	MOVD	R1, R17
	MOVD	0(RSP), R27
	ADD	$16, RSP
	RET

// func callbackAddrs(a *[4]uintptr)
TEXT ·callbackAddrs(SB), NOSPLIT, $0-8
	MOVD	a+0(FP), R0
	MOVD	$·auRender(SB), R1
	MOVD	R1, 0(R0)
	MOVD	$·auInput(SB), R1
	MOVD	R1, 8(R0)
	MOVD	$·auChanged(SB), R1
	MOVD	R1, 16(R0)
	MOVD	$·auTestRender(SB), R1
	MOVD	R1, 24(R0)
	RET
