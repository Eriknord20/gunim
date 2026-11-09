//go:build linux && !android

#include "textflag.h"
#include "go_asm.h"

// The callbacks the server calls, in the System V calling convention:
// arguments in DI and SI, the result in AX, and BX, BP and R12 to R15
// kept for the caller. They run on the server's threads, with no Go
// behind them: no g, no stack checks, no frame of Go's.
//
// On amd64 a store is seen after the stores before it, and a load
// after the loads before it, so plain MOVs publish and read in order.

// int jackProcess(jack_nframes_t nframes, rtState *st)
//
// It keeps:
//	BX	st
//	R12	n, the buffer's frames
//	R13	the ring's count at the buffer's start: outR, then inW
//	R14	the frames the ring gives or takes: take, then put
//	BP	outR as the buffer began
//	0(SP)	k, the port
TEXT ·jackProcess(SB), NOSPLIT|NOFRAME, $0-0
	PUSHQ	BX
	PUSHQ	BP
	PUSHQ	R12
	PUSHQ	R13
	PUSHQ	R14
	SUBQ	$16, SP // 16-byte aligned for the calls
	MOVQ	SI, BX
	MOVL	DI, R12
	MOVQ	R12, rtState_nframes(BX)

	// The outputs take what the ring holds, a buffer at most; nothing
	// while off.
	MOVQ	rtState_outR(BX), R13
	MOVQ	R13, BP
	XORQ	R14, R14
	CMPQ	rtState_on(BX), $0
	JEQ	outports
	MOVQ	rtState_outW(BX), R14
	SUBQ	R13, R14
	CMPQ	R14, R12
	JLS	took
	MOVQ	R12, R14

took:
	CMPQ	R14, R12
	JEQ	outports
	INCQ	rtState_short(BX)

outports:
	MOVQ	$0, 0(SP)

outloop:
	MOVQ	0(SP), AX
	CMPQ	AX, rtState_outs(BX)
	JAE	outdone
	MOVQ	rtState_outPort(BX)(AX*8), DI
	MOVL	R12, SI
	MOVQ	rtState_getBuffer(BX), AX
	CALL	AX
	TESTQ	AX, AX
	JEQ	outnext

	// CX is the channel's first sample in the ring, DX the mask.
	MOVQ	rtState_mask(BX), DX
	LEAQ	1(DX), CX
	IMULQ	0(SP), CX
	SHLQ	$2, CX
	ADDQ	rtState_outBuf(BX), CX
	XORQ	R8, R8

outcopy:
	CMPQ	R8, R14
	JAE	outzero
	LEAQ	(R13)(R8*1), R9
	ANDQ	DX, R9
	MOVL	(CX)(R9*4), R10
	MOVL	R10, (AX)(R8*4)
	INCQ	R8
	JMP	outcopy

outzero:
	CMPQ	R8, R12
	JAE	outnext
	MOVL	$0, (AX)(R8*4)
	INCQ	R8
	JMP	outzero

outnext:
	INCQ	0(SP)
	JMP	outloop

outdone:
	// Published after the samples were read.
	ADDQ	R14, R13
	MOVQ	R13, rtState_outR(BX)

	// The inputs write what their ports hold, as far as the ring has
	// room.
	MOVQ	rtState_inW(BX), R13
	XORQ	R14, R14
	CMPQ	rtState_ins(BX), $0
	JEQ	indone
	MOVQ	rtState_mask(BX), CX
	INCQ	CX
	MOVQ	R13, AX
	SUBQ	rtState_inR(BX), AX
	SUBQ	AX, CX
	MOVQ	R12, R14
	CMPQ	R14, CX
	JLS	fits
	MOVQ	CX, R14

fits:
	MOVQ	R12, AX
	SUBQ	R14, AX
	ADDQ	AX, rtState_lost(BX)
	MOVQ	$0, 0(SP)

inloop:
	MOVQ	0(SP), AX
	CMPQ	AX, rtState_ins(BX)
	JAE	indone
	MOVQ	rtState_inPort(BX)(AX*8), DI
	MOVL	R12, SI
	MOVQ	rtState_getBuffer(BX), AX
	CALL	AX
	TESTQ	AX, AX
	JEQ	innext
	MOVQ	rtState_mask(BX), DX
	LEAQ	1(DX), CX
	IMULQ	0(SP), CX
	SHLQ	$2, CX
	ADDQ	rtState_inBuf(BX), CX
	XORQ	R8, R8

incopy:
	CMPQ	R8, R14
	JAE	innext
	LEAQ	(R13)(R8*1), R9
	ANDQ	DX, R9
	MOVL	(AX)(R8*4), R10
	MOVL	R10, (CX)(R9*4)
	INCQ	R8
	JMP	incopy

innext:
	INCQ	0(SP)
	JMP	inloop

indone:
	// The log, then the input ring's count, then the cycles, in that
	// order: Read trusts what a cycle counted shows.
	MOVQ	rtState_cycles(BX), AX
	MOVQ	AX, CX
	ANDQ	$(const_logSize-1), CX
	MOVQ	R13, rtState_logIn(BX)(CX*8)
	MOVQ	BP, rtState_logOut(BX)(CX*8)
	MOVQ	R14, rtState_logN(BX)(CX*8)
	ADDQ	R14, R13
	MOVQ	R13, rtState_inW(BX)
	INCQ	AX
	MOVQ	AX, rtState_cycles(BX)

	ADDQ	$16, SP
	POPQ	R14
	POPQ	R13
	POPQ	R12
	POPQ	BP
	POPQ	BX
	XORL	AX, AX
	RET

// int jackBufferSize(jack_nframes_t nframes, rtState *st)
TEXT ·jackBufferSize(SB), NOSPLIT|NOFRAME, $0-0
	MOVL	DI, AX
	MOVQ	AX, rtState_bufSize(SI)
	XORL	AX, AX
	RET

// int jackRate(jack_nframes_t nframes, rtState *st)
TEXT ·jackRate(SB), NOSPLIT|NOFRAME, $0-0
	MOVL	DI, AX
	MOVQ	AX, rtState_rate(SI)
	XORL	AX, AX
	RET

// int jackXrun(rtState *st)
TEXT ·jackXrun(SB), NOSPLIT|NOFRAME, $0-0
	MOVQ	$1, AX
	LOCK
	XADDQ	AX, rtState_xruns(DI)
	XORL	AX, AX
	RET

// int jackGraph(rtState *st)
TEXT ·jackGraph(SB), NOSPLIT|NOFRAME, $0-0
	MOVQ	$1, AX
	LOCK
	XADDQ	AX, rtState_graphs(DI)
	XORL	AX, AX
	RET

// void jackShutdown(rtState *st)
TEXT ·jackShutdown(SB), NOSPLIT|NOFRAME, $0-0
	MOVQ	$1, AX
	XCHGQ	AX, rtState_shutdown(DI)
	RET

// void jackSilent(const char *msg)
TEXT ·jackSilent(SB), NOSPLIT|NOFRAME, $0-0
	RET

// void *jackSame(void *port, jack_nframes_t nframes)
TEXT ·jackSame(SB), NOSPLIT|NOFRAME, $0-0
	MOVQ	DI, AX
	RET

// func callbackAddrs(a *[8]uintptr)
TEXT ·callbackAddrs(SB), NOSPLIT, $0-8
	MOVQ	a+0(FP), DI
	MOVQ	$·jackProcess(SB), AX
	MOVQ	AX, 0(DI)
	MOVQ	$·jackBufferSize(SB), AX
	MOVQ	AX, 8(DI)
	MOVQ	$·jackRate(SB), AX
	MOVQ	AX, 16(DI)
	MOVQ	$·jackXrun(SB), AX
	MOVQ	AX, 24(DI)
	MOVQ	$·jackGraph(SB), AX
	MOVQ	AX, 32(DI)
	MOVQ	$·jackShutdown(SB), AX
	MOVQ	AX, 40(DI)
	MOVQ	$·jackSilent(SB), AX
	MOVQ	AX, 48(DI)
	MOVQ	$·jackSame(SB), AX
	MOVQ	AX, 56(DI)
	RET
