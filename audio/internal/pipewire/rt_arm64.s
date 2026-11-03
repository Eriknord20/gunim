//go:build linux && !android

#include "textflag.h"
#include "go_asm.h"

// The callbacks PipeWire calls, in the AAPCS64 calling convention:
// arguments in R0 to R3, the result in R0, and R19 to R30 kept for the
// caller. They run on PipeWire's threads, with no Go behind them: no g,
// no stack checks, no frame of Go's. R27 is the assembler's own scratch
// register, so the process functions keep it too.
//
// A store-release (STLR) publishes what was written before it, and a
// load-acquire (LDAR) reads what was published, as package atomic
// stores and loads do. DMB ISH keeps the stores after it from being
// seen before those ahead of it.
//
// The C structures they read, as PipeWire 0.3.50 to 1.x lay them out:
//
//	struct pw_buffer   { spa_buffer *buffer @0; ...; uint64 size @16; uint64 requested @24 }
//	struct spa_buffer  { uint32 n_metas @0; uint32 n_datas @4; ...; spa_data *datas @16 }
//	struct spa_data    { ...; uint32 maxsize @20; void *data @24; spa_chunk *chunk @32 }
//	struct spa_chunk   { uint32 offset @0; uint32 size @4; int32 stride @8; int32 flags @12 }

// void pwProcess(rtState *st)
//
// It keeps:
//	R19	st
//	R20	the pw_buffer
//	R21	its first spa_data
//	R22	its samples
//	R23	n, the frames it plays
//	R24	outR, the output ring's count
//	R25	the frames the ring gives
TEXT ·pwProcess(SB), NOSPLIT|NOFRAME, $0-0
	SUB	$80, RSP
	STP	(R19, R20), 0(RSP)
	STP	(R21, R22), 16(RSP)
	STP	(R23, R24), 32(RSP)
	STP	(R25, R27), 48(RSP)
	STP	(R29, R30), 64(RSP)
	MOVD	R0, R19

	MOVD	rtState_outStream(R19), R0
	MOVD	rtState_dequeue(R19), R2
	BL	(R2)
	CBNZ	R0, got
	MOVD	rtState_empty(R19), R0
	ADD	$1, R0, R0
	MOVD	R0, rtState_empty(R19)
	B	done

got:
	MOVD	R0, R20
	MOVD	0(R20), R0
	MOVWU	4(R0), R1
	CBZ	R1, give
	MOVD	16(R0), R21
	MOVD	24(R21), R22
	CBZ	R22, give
	MOVD	32(R21), R0
	CBZ	R0, give

	// n is what the stream asks for, else the fallback, as far as the
	// buffer holds.
	MOVWU	20(R21), R23
	LSR	$3, R23, R23
	MOVD	24(R20), R0
	CBNZ	R0, asked
	MOVD	rtState_fallback(R19), R0

asked:
	CMP	R23, R0
	CSEL	LO, R0, R23, R23

	// The sequence lock: odd while outR, took and outTime change.
	ADD	$rtState_seq, R19, R1
	MOVD	(R1), R0
	ADD	$1, R0, R0
	STLR	R0, (R1)
	DMB	$0xb

	// The ring gives what it holds, n frames at most; nothing while off.
	MOVD	rtState_outR(R19), R24
	MOVD	$0, R25
	MOVD	rtState_on(R19), R0
	CBZ	R0, fill
	ADD	$rtState_outW, R19, R0
	LDAR	(R0), R25
	SUB	R24, R25, R25
	CMP	R23, R25
	CSEL	HI, R23, R25, R25
	CMP	R23, R25
	BEQ	fill
	MOVD	rtState_short(R19), R0
	ADD	$1, R0, R0
	MOVD	R0, rtState_short(R19)

fill:
	MOVD	rtState_outBuf(R19), R3
	MOVD	rtState_outMask(R19), R4
	MOVD	$0, R6

copy:
	CMP	R25, R6
	BHS	zero
	ADD	R24, R6, R7
	AND	R4, R7, R7
	MOVD	(R3)(R7<<3), R8
	MOVD	R8, (R22)(R6<<3)
	ADD	$1, R6, R6
	B	copy

zero:
	CMP	R23, R6
	BHS	filled
	MOVD	ZR, (R22)(R6<<3)
	ADD	$1, R6, R6
	B	zero

filled:
	// Published after the frames were read.
	ADD	R25, R24, R24
	ADD	$rtState_outR, R19, R0
	STLR	R24, (R0)
	MOVD	R23, rtState_took(R19)
	MOVD	32(R21), R0
	MOVW	ZR, 0(R0)
	LSL	$3, R23, R1
	MOVW	R1, 4(R0)
	MOVD	$8, R1
	MOVW	R1, 8(R0)
	MOVD	R23, 16(R20)

	MOVD	rtState_outStream(R19), R0
	MOVD	R20, R1
	MOVD	rtState_queue(R19), R2
	BL	(R2)

	// The time report, read once the buffer is queued, so its queued
	// counts the buffer.
	MOVD	rtState_outStream(R19), R0
	ADD	$rtState_outTime, R19, R1
	MOVD	$const_timeSize, R2
	MOVD	rtState_getTime(R19), R3
	BL	(R3)
	ADD	$rtState_seq, R19, R1
	MOVD	(R1), R0
	ADD	$1, R0, R0
	STLR	R0, (R1)
	ADD	$rtState_cycles, R19, R1
	MOVD	(R1), R0
	ADD	$1, R0, R0
	STLR	R0, (R1)
	B	done

give:
	// A buffer with nothing to fill goes back as it came.
	MOVD	rtState_outStream(R19), R0
	MOVD	R20, R1
	MOVD	rtState_queue(R19), R2
	BL	(R2)

done:
	LDP	0(RSP), (R19, R20)
	LDP	16(RSP), (R21, R22)
	LDP	32(RSP), (R23, R24)
	LDP	48(RSP), (R25, R27)
	LDP	64(RSP), (R29, R30)
	ADD	$80, RSP
	RET

// void pwCapture(rtState *st)
//
// It takes each buffer the input stream holds, oldest first, and keeps:
//	R19	st
//	R20	the pw_buffer, then the log's entry
//	R21	inW as the cycle began
//	R22	the frames written to the ring this cycle
//	R23	the frames handed this cycle
//	R24	the buffer's first spa_data
TEXT ·pwCapture(SB), NOSPLIT|NOFRAME, $0-0
	SUB	$80, RSP
	STP	(R19, R20), 0(RSP)
	STP	(R21, R22), 16(RSP)
	STP	(R23, R24), 32(RSP)
	STP	(R25, R27), 48(RSP)
	STP	(R29, R30), 64(RSP)
	MOVD	R0, R19
	MOVD	rtState_inW(R19), R21
	MOVD	$0, R22
	MOVD	$0, R23

next:
	MOVD	rtState_inStream(R19), R0
	MOVD	rtState_dequeue(R19), R2
	BL	(R2)
	CBZ	R0, logit
	MOVD	R0, R20
	MOVD	0(R20), R0
	MOVWU	4(R0), R1
	CBZ	R1, back
	MOVD	16(R0), R24
	MOVD	24(R24), R5
	CBZ	R5, back
	MOVD	32(R24), R0
	CBZ	R0, back

	// The chunk's frames, kept within the buffer.
	MOVWU	0(R0), R1
	MOVWU	4(R0), R2
	MOVWU	20(R24), R3
	CMP	R3, R1
	CSEL	HI, R3, R1, R1
	SUB	R1, R3, R3
	CMP	R3, R2
	CSEL	HI, R3, R2, R2
	ADD	R1, R5, R5
	LSR	$3, R2, R2
	ADD	R2, R23, R23

	// As many as the ring has room for; the rest are lost.
	ADD	R22, R21, R6
	ADD	$rtState_inR, R19, R0
	LDAR	(R0), R7
	SUB	R7, R6, R7
	MOVD	rtState_inMask(R19), R4
	ADD	$1, R4, R8
	SUB	R7, R8, R8
	CMP	R8, R2
	CSEL	HI, R8, R2, R9
	SUB	R9, R2, R0
	MOVD	rtState_lost(R19), R1
	ADD	R0, R1, R1
	MOVD	R1, rtState_lost(R19)
	MOVD	rtState_inBuf(R19), R3
	MOVD	$0, R10

incopy:
	CMP	R9, R10
	BHS	incopied
	MOVD	(R5)(R10<<3), R11
	ADD	R6, R10, R12
	AND	R4, R12, R12
	MOVD	R11, (R3)(R12<<3)
	ADD	$1, R10, R10
	B	incopy

incopied:
	ADD	R9, R22, R22

back:
	MOVD	rtState_inStream(R19), R0
	MOVD	R20, R1
	MOVD	rtState_queue(R19), R2
	BL	(R2)
	B	next

logit:
	// The log, then the ring's count, then the cycles, in that order:
	// Read trusts what a cycle counted shows.
	CBZ	R23, indone
	MOVD	rtState_inCycles(R19), R0
	AND	$(const_logSize-1), R0, R0
	MOVD	$logEntry__size, R1
	MUL	R1, R0, R0
	ADD	$rtState_log, R0, R0
	ADD	R19, R0, R20
	MOVD	R21, logEntry_in(R20)
	MOVD	R22, logEntry_n(R20)
	MOVD	R23, logEntry_got(R20)
	MOVD	rtState_inStream(R19), R0
	ADD	$logEntry_time, R20, R1
	MOVD	$const_timeSize, R2
	MOVD	rtState_getTime(R19), R3
	BL	(R3)
	ADD	R22, R21, R21
	ADD	$rtState_inW, R19, R0
	STLR	R21, (R0)
	ADD	$rtState_inCycles, R19, R1
	MOVD	(R1), R0
	ADD	$1, R0, R0
	STLR	R0, (R1)

indone:
	LDP	0(RSP), (R19, R20)
	LDP	16(RSP), (R21, R22)
	LDP	32(RSP), (R23, R24)
	LDP	48(RSP), (R25, R27)
	LDP	64(RSP), (R29, R30)
	ADD	$80, RSP
	RET

// void pwOutState(rtState *st, int old, int state, const char *error)
TEXT ·pwOutState(SB), NOSPLIT|NOFRAME, $0-0
	MOVW	R2, R3
	ADD	$rtState_outState, R0, R4
	STLR	R3, (R4)
	ADD	$rtState_outStates, R0, R4
	MOVD	(R4), R5
	ADD	$1, R5, R5
	STLR	R5, (R4)
	RET

// void pwInState(rtState *st, int old, int state, const char *error)
TEXT ·pwInState(SB), NOSPLIT|NOFRAME, $0-0
	MOVW	R2, R3
	ADD	$rtState_inState, R0, R4
	STLR	R3, (R4)
	ADD	$rtState_inStates, R0, R4
	MOVD	(R4), R5
	ADD	$1, R5, R5
	STLR	R5, (R4)
	RET

// struct pw_buffer *fakeDequeue(fakeStream *s)
TEXT ·fakeDequeue(SB), NOSPLIT|NOFRAME, $0-0
	MOVD	fakeStream_next(R0), R1
	MOVD	(R1), R2
	CBZ	R2, none
	ADD	$8, R1, R1
	MOVD	R1, fakeStream_next(R0)

none:
	MOVD	R2, R0
	RET

// int fakeQueue(fakeStream *s, struct pw_buffer *b)
TEXT ·fakeQueue(SB), NOSPLIT|NOFRAME, $0-0
	MOVD	R1, fakeStream_queued(R0)
	MOVD	fakeStream_queues(R0), R2
	ADD	$1, R2, R2
	MOVD	R2, fakeStream_queues(R0)
	MOVD	$0, R0
	RET

// int fakeGetTime(fakeStream *s, struct pw_time *t, size_t size)
TEXT ·fakeGetTime(SB), NOSPLIT|NOFRAME, $0-0
	ADD	$fakeStream_time, R0, R0
	LDP	0(R0), (R2, R3)
	STP	(R2, R3), 0(R1)
	LDP	16(R0), (R2, R3)
	STP	(R2, R3), 16(R1)
	LDP	32(R0), (R2, R3)
	STP	(R2, R3), 32(R1)
	LDP	48(R0), (R2, R3)
	STP	(R2, R3), 48(R1)
	MOVD	$0, R0
	RET

// func callbackAddrs(a *[7]uintptr)
TEXT ·callbackAddrs(SB), NOSPLIT, $0-8
	MOVD	a+0(FP), R0
	MOVD	$·pwProcess(SB), R1
	MOVD	R1, 0(R0)
	MOVD	$·pwCapture(SB), R1
	MOVD	R1, 8(R0)
	MOVD	$·pwOutState(SB), R1
	MOVD	R1, 16(R0)
	MOVD	$·pwInState(SB), R1
	MOVD	R1, 24(R0)
	MOVD	$·fakeDequeue(SB), R1
	MOVD	R1, 32(R0)
	MOVD	$·fakeQueue(SB), R1
	MOVD	R1, 40(R0)
	MOVD	$·fakeGetTime(SB), R1
	MOVD	R1, 48(R0)
	RET
