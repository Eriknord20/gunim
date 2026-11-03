//go:build linux && !android

#include "textflag.h"
#include "go_asm.h"

// The callbacks PipeWire calls, in the System V calling convention:
// arguments in DI, SI, DX and CX, the result in AX, and BX, BP and R12
// to R15 kept for the caller. They run on PipeWire's threads, with no Go
// behind them: no g, no stack checks, no frame of Go's.
//
// On amd64 a store is seen after the stores before it, and a load
// after the loads before it, so plain MOVs publish and read in order.
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
//	BX	st
//	R12	the pw_buffer
//	R13	its first spa_data
//	R14	its samples
//	R15	n, the frames it plays
//	BP	outR, the output ring's count
TEXT ·pwProcess(SB), NOSPLIT|NOFRAME, $0-0
	PUSHQ	BX
	PUSHQ	BP
	PUSHQ	R12
	PUSHQ	R13
	PUSHQ	R14
	PUSHQ	R15
	SUBQ	$8, SP // 16-byte aligned for the calls
	MOVQ	DI, BX

	MOVQ	rtState_outStream(BX), DI
	MOVQ	rtState_dequeue(BX), AX
	CALL	AX
	TESTQ	AX, AX
	JNE	got
	INCQ	rtState_empty(BX)
	JMP	done

got:
	MOVQ	AX, R12
	MOVQ	0(R12), AX
	CMPL	4(AX), $0
	JEQ	give
	MOVQ	16(AX), R13
	MOVQ	24(R13), R14
	TESTQ	R14, R14
	JEQ	give
	CMPQ	32(R13), $0
	JEQ	give

	// n is what the stream asks for, else the fallback, as far as the
	// buffer holds.
	MOVL	20(R13), R15
	SHRQ	$3, R15
	MOVQ	24(R12), AX
	TESTQ	AX, AX
	JNE	asked
	MOVQ	rtState_fallback(BX), AX

asked:
	CMPQ	AX, R15
	JAE	sized
	MOVQ	AX, R15

sized:
	// The sequence lock: odd while outR, took and outTime change.
	INCQ	rtState_seq(BX)

	// The ring gives what it holds, n frames at most; nothing while off.
	MOVQ	rtState_outR(BX), BP
	XORQ	CX, CX
	CMPQ	rtState_on(BX), $0
	JEQ	fill
	MOVQ	rtState_outW(BX), CX
	SUBQ	BP, CX
	CMPQ	CX, R15
	JLS	took
	MOVQ	R15, CX

took:
	CMPQ	CX, R15
	JEQ	fill
	INCQ	rtState_short(BX)

fill:
	MOVQ	rtState_outBuf(BX), R10
	MOVQ	rtState_outMask(BX), R11
	XORQ	R8, R8

copy:
	CMPQ	R8, CX
	JAE	zero
	LEAQ	(BP)(R8*1), R9
	ANDQ	R11, R9
	MOVQ	(R10)(R9*8), R9
	MOVQ	R9, (R14)(R8*8)
	INCQ	R8
	JMP	copy

zero:
	CMPQ	R8, R15
	JAE	filled
	MOVQ	$0, (R14)(R8*8)
	INCQ	R8
	JMP	zero

filled:
	// Published after the frames were read.
	ADDQ	CX, BP
	MOVQ	BP, rtState_outR(BX)
	MOVQ	R15, rtState_took(BX)
	MOVQ	32(R13), AX
	MOVL	$0, 0(AX)
	MOVQ	R15, DX
	SHLQ	$3, DX
	MOVL	DX, 4(AX)
	MOVL	$8, 8(AX)
	MOVQ	R15, 16(R12)

	MOVQ	rtState_outStream(BX), DI
	MOVQ	R12, SI
	MOVQ	rtState_queue(BX), AX
	CALL	AX

	// The time report, read once the buffer is queued, so its queued
	// counts the buffer.
	MOVQ	rtState_outStream(BX), DI
	LEAQ	rtState_outTime(BX), SI
	MOVQ	$const_timeSize, DX
	MOVQ	rtState_getTime(BX), AX
	CALL	AX
	INCQ	rtState_seq(BX)
	INCQ	rtState_cycles(BX)
	JMP	done

give:
	// A buffer with nothing to fill goes back as it came.
	MOVQ	rtState_outStream(BX), DI
	MOVQ	R12, SI
	MOVQ	rtState_queue(BX), AX
	CALL	AX

done:
	ADDQ	$8, SP
	POPQ	R15
	POPQ	R14
	POPQ	R13
	POPQ	R12
	POPQ	BP
	POPQ	BX
	RET

// void pwCapture(rtState *st)
//
// It takes each buffer the input stream holds, oldest first, and keeps:
//	BX	st
//	R12	the pw_buffer, then the log's entry
//	R13	inW as the cycle began
//	R14	the frames written to the ring this cycle
//	R15	the frames handed this cycle
//	BP	the buffer's first spa_data
TEXT ·pwCapture(SB), NOSPLIT|NOFRAME, $0-0
	PUSHQ	BX
	PUSHQ	BP
	PUSHQ	R12
	PUSHQ	R13
	PUSHQ	R14
	PUSHQ	R15
	SUBQ	$8, SP
	MOVQ	DI, BX
	MOVQ	rtState_inW(BX), R13
	XORQ	R14, R14
	XORQ	R15, R15

next:
	MOVQ	rtState_inStream(BX), DI
	MOVQ	rtState_dequeue(BX), AX
	CALL	AX
	TESTQ	AX, AX
	JEQ	logit
	MOVQ	AX, R12
	MOVQ	0(R12), AX
	CMPL	4(AX), $0
	JEQ	back
	MOVQ	16(AX), BP
	MOVQ	24(BP), SI
	TESTQ	SI, SI
	JEQ	back
	MOVQ	32(BP), AX
	TESTQ	AX, AX
	JEQ	back

	// The chunk's frames, kept within the buffer.
	MOVL	0(AX), CX
	MOVL	4(AX), DX
	MOVL	20(BP), R8
	CMPQ	CX, R8
	JLS	inside
	MOVQ	R8, CX

inside:
	SUBQ	CX, R8
	CMPQ	DX, R8
	JLS	fits
	MOVQ	R8, DX

fits:
	ADDQ	CX, SI
	SHRQ	$3, DX
	ADDQ	DX, R15

	// As many as the ring has room for; the rest are lost.
	LEAQ	(R13)(R14*1), R9
	MOVQ	R9, R11
	SUBQ	rtState_inR(BX), R11
	MOVQ	rtState_inMask(BX), R8
	INCQ	R8
	SUBQ	R11, R8
	MOVQ	DX, CX
	CMPQ	CX, R8
	JLS	room
	MOVQ	R8, CX

room:
	MOVQ	DX, R11
	SUBQ	CX, R11
	ADDQ	R11, rtState_lost(BX)
	MOVQ	rtState_inBuf(BX), R10
	MOVQ	rtState_inMask(BX), R11
	XORQ	R8, R8

incopy:
	CMPQ	R8, CX
	JAE	incopied
	MOVQ	(SI)(R8*8), DX
	LEAQ	(R9)(R8*1), DI
	ANDQ	R11, DI
	MOVQ	DX, (R10)(DI*8)
	INCQ	R8
	JMP	incopy

incopied:
	ADDQ	CX, R14

back:
	MOVQ	rtState_inStream(BX), DI
	MOVQ	R12, SI
	MOVQ	rtState_queue(BX), AX
	CALL	AX
	JMP	next

logit:
	// The log, then the ring's count, then the cycles, in that order:
	// Read trusts what a cycle counted shows.
	TESTQ	R15, R15
	JEQ	indone
	MOVQ	rtState_inCycles(BX), AX
	ANDQ	$(const_logSize-1), AX
	IMULQ	$logEntry__size, AX
	LEAQ	rtState_log(BX)(AX*1), R12
	MOVQ	R13, logEntry_in(R12)
	MOVQ	R14, logEntry_n(R12)
	MOVQ	R15, logEntry_got(R12)
	MOVQ	rtState_inStream(BX), DI
	LEAQ	logEntry_time(R12), SI
	MOVQ	$const_timeSize, DX
	MOVQ	rtState_getTime(BX), AX
	CALL	AX
	ADDQ	R14, R13
	MOVQ	R13, rtState_inW(BX)
	INCQ	rtState_inCycles(BX)

indone:
	ADDQ	$8, SP
	POPQ	R15
	POPQ	R14
	POPQ	R13
	POPQ	R12
	POPQ	BP
	POPQ	BX
	RET

// void pwOutState(rtState *st, int old, int state, const char *error)
TEXT ·pwOutState(SB), NOSPLIT|NOFRAME, $0-0
	MOVLQSX	DX, AX
	MOVQ	AX, rtState_outState(DI)
	INCQ	rtState_outStates(DI)
	RET

// void pwInState(rtState *st, int old, int state, const char *error)
TEXT ·pwInState(SB), NOSPLIT|NOFRAME, $0-0
	MOVLQSX	DX, AX
	MOVQ	AX, rtState_inState(DI)
	INCQ	rtState_inStates(DI)
	RET

// struct pw_buffer *fakeDequeue(fakeStream *s)
TEXT ·fakeDequeue(SB), NOSPLIT|NOFRAME, $0-0
	MOVQ	fakeStream_next(DI), CX
	MOVQ	(CX), AX
	TESTQ	AX, AX
	JEQ	none
	ADDQ	$8, fakeStream_next(DI)

none:
	RET

// int fakeQueue(fakeStream *s, struct pw_buffer *b)
TEXT ·fakeQueue(SB), NOSPLIT|NOFRAME, $0-0
	MOVQ	SI, fakeStream_queued(DI)
	INCQ	fakeStream_queues(DI)
	XORL	AX, AX
	RET

// int fakeGetTime(fakeStream *s, struct pw_time *t, size_t size)
TEXT ·fakeGetTime(SB), NOSPLIT|NOFRAME, $0-0
	LEAQ	fakeStream_time(DI), DI
	MOVQ	0(DI), AX
	MOVQ	AX, 0(SI)
	MOVQ	8(DI), AX
	MOVQ	AX, 8(SI)
	MOVQ	16(DI), AX
	MOVQ	AX, 16(SI)
	MOVQ	24(DI), AX
	MOVQ	AX, 24(SI)
	MOVQ	32(DI), AX
	MOVQ	AX, 32(SI)
	MOVQ	40(DI), AX
	MOVQ	AX, 40(SI)
	MOVQ	48(DI), AX
	MOVQ	AX, 48(SI)
	MOVQ	56(DI), AX
	MOVQ	AX, 56(SI)
	XORL	AX, AX
	RET

// func callbackAddrs(a *[7]uintptr)
TEXT ·callbackAddrs(SB), NOSPLIT, $0-8
	MOVQ	a+0(FP), DI
	MOVQ	$·pwProcess(SB), AX
	MOVQ	AX, 0(DI)
	MOVQ	$·pwCapture(SB), AX
	MOVQ	AX, 8(DI)
	MOVQ	$·pwOutState(SB), AX
	MOVQ	AX, 16(DI)
	MOVQ	$·pwInState(SB), AX
	MOVQ	AX, 24(DI)
	MOVQ	$·fakeDequeue(SB), AX
	MOVQ	AX, 32(DI)
	MOVQ	$·fakeQueue(SB), AX
	MOVQ	AX, 40(DI)
	MOVQ	$·fakeGetTime(SB), AX
	MOVQ	AX, 48(DI)
	RET
