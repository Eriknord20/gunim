//go:build linux && !android

#include "textflag.h"
#include "go_asm.h"

// The callbacks the server calls, in the AAPCS64 calling convention:
// arguments in R0 and R1, the result in R0, and R19 to R30 kept for the
// caller. They run on the server's threads, with no Go behind them: no
// g, no stack checks, no frame of Go's. R27 is the assembler's own
// scratch register, so the process function keeps it too.
//
// A store-release (STLR) publishes what was written before it, and a
// load-acquire (LDAR) reads what was published, as package atomic
// stores and loads do.

// int jackProcess(jack_nframes_t nframes, rtState *st)
//
// It keeps:
//	R19	st
//	R20	n, the buffer's frames
//	R21	the ring's count at the buffer's start: outR, then inW
//	R22	the frames the ring gives or takes: take, then put
//	R23	outR as the buffer began
//	R24	k, the port
TEXT ·jackProcess(SB), NOSPLIT|NOFRAME, $0-0
	SUB	$96, RSP
	STP	(R19, R20), 0(RSP)
	STP	(R21, R22), 16(RSP)
	STP	(R23, R24), 32(RSP)
	STP	(R25, R26), 48(RSP)
	STP	(R27, R29), 64(RSP)
	MOVD	R30, 80(RSP)
	MOVD	R1, R19
	MOVWU	R0, R20
	MOVD	R20, rtState_nframes(R19)

	// The outputs take what the ring holds, a buffer at most; nothing
	// while off.
	MOVD	rtState_outR(R19), R21
	MOVD	R21, R23
	MOVD	$0, R22
	MOVD	rtState_on(R19), R0
	CBZ	R0, outports
	ADD	$rtState_outW, R19, R0
	LDAR	(R0), R22
	SUB	R21, R22, R22
	CMP	R20, R22
	CSEL	HI, R20, R22, R22
	CMP	R20, R22
	BEQ	outports
	MOVD	rtState_short(R19), R0
	ADD	$1, R0, R0
	MOVD	R0, rtState_short(R19)

outports:
	MOVD	$0, R24

outloop:
	MOVD	rtState_outs(R19), R0
	CMP	R0, R24
	BHS	outdone
	ADD	$rtState_outPort, R19, R0
	MOVD	(R0)(R24<<3), R0
	MOVWU	R20, R1
	MOVD	rtState_getBuffer(R19), R2
	BL	(R2)
	CBZ	R0, outnext

	// R5 is the channel's first sample in the ring, R3 the mask.
	MOVD	rtState_mask(R19), R3
	ADD	$1, R3, R4
	MUL	R24, R4, R4
	MOVD	rtState_outBuf(R19), R5
	ADD	R4<<2, R5, R5
	MOVD	$0, R6

outcopy:
	CMP	R22, R6
	BHS	outzero
	ADD	R21, R6, R7
	AND	R3, R7, R7
	MOVWU	(R5)(R7<<2), R8
	MOVW	R8, (R0)(R6<<2)
	ADD	$1, R6, R6
	B	outcopy

outzero:
	CMP	R20, R6
	BHS	outnext
	MOVW	ZR, (R0)(R6<<2)
	ADD	$1, R6, R6
	B	outzero

outnext:
	ADD	$1, R24, R24
	B	outloop

outdone:
	// Published after the samples were read.
	ADD	R22, R21, R21
	ADD	$rtState_outR, R19, R0
	STLR	R21, (R0)

	// The inputs write what their ports hold, as far as the ring has
	// room.
	MOVD	rtState_inW(R19), R21
	MOVD	$0, R22
	MOVD	rtState_ins(R19), R0
	CBZ	R0, indone
	ADD	$rtState_inR, R19, R0
	LDAR	(R0), R1
	SUB	R1, R21, R1
	MOVD	rtState_mask(R19), R2
	ADD	$1, R2, R2
	SUB	R1, R2, R2
	CMP	R2, R20
	CSEL	HI, R2, R20, R22
	SUB	R22, R20, R0
	MOVD	rtState_lost(R19), R1
	ADD	R0, R1, R1
	MOVD	R1, rtState_lost(R19)
	MOVD	$0, R24

inloop:
	MOVD	rtState_ins(R19), R0
	CMP	R0, R24
	BHS	indone
	ADD	$rtState_inPort, R19, R0
	MOVD	(R0)(R24<<3), R0
	MOVWU	R20, R1
	MOVD	rtState_getBuffer(R19), R2
	BL	(R2)
	CBZ	R0, innext
	MOVD	rtState_mask(R19), R3
	ADD	$1, R3, R4
	MUL	R24, R4, R4
	MOVD	rtState_inBuf(R19), R5
	ADD	R4<<2, R5, R5
	MOVD	$0, R6

incopy:
	CMP	R22, R6
	BHS	innext
	ADD	R21, R6, R7
	AND	R3, R7, R7
	MOVWU	(R0)(R6<<2), R8
	MOVW	R8, (R5)(R7<<2)
	ADD	$1, R6, R6
	B	incopy

innext:
	ADD	$1, R24, R24
	B	inloop

indone:
	// The log, then the input ring's count, then the cycles, in that
	// order: Read trusts what a cycle counted shows.
	MOVD	rtState_cycles(R19), R0
	AND	$(const_logSize-1), R0, R1
	ADD	$rtState_logIn, R19, R2
	MOVD	R21, (R2)(R1<<3)
	ADD	$rtState_logOut, R19, R2
	MOVD	R23, (R2)(R1<<3)
	ADD	$rtState_logN, R19, R2
	MOVD	R22, (R2)(R1<<3)
	ADD	R22, R21, R21
	ADD	$rtState_inW, R19, R2
	STLR	R21, (R2)
	ADD	$1, R0, R0
	ADD	$rtState_cycles, R19, R2
	STLR	R0, (R2)

	LDP	0(RSP), (R19, R20)
	LDP	16(RSP), (R21, R22)
	LDP	32(RSP), (R23, R24)
	LDP	48(RSP), (R25, R26)
	LDP	64(RSP), (R27, R29)
	MOVD	80(RSP), R30
	ADD	$96, RSP
	MOVD	$0, R0
	RET

// int jackBufferSize(jack_nframes_t nframes, rtState *st)
TEXT ·jackBufferSize(SB), NOSPLIT|NOFRAME, $0-0
	MOVWU	R0, R0
	ADD	$rtState_bufSize, R1, R2
	STLR	R0, (R2)
	MOVD	$0, R0
	RET

// int jackRate(jack_nframes_t nframes, rtState *st)
TEXT ·jackRate(SB), NOSPLIT|NOFRAME, $0-0
	MOVWU	R0, R0
	ADD	$rtState_rate, R1, R2
	STLR	R0, (R2)
	MOVD	$0, R0
	RET

// int jackXrun(rtState *st)
TEXT ·jackXrun(SB), NOSPLIT|NOFRAME, $0-0
	ADD	$rtState_xruns, R0, R1

again:
	LDAXR	(R1), R2
	ADD	$1, R2, R2
	STLXR	R2, (R1), R3
	CBNZ	R3, again
	MOVD	$0, R0
	RET

// int jackGraph(rtState *st)
TEXT ·jackGraph(SB), NOSPLIT|NOFRAME, $0-0
	ADD	$rtState_graphs, R0, R1

again:
	LDAXR	(R1), R2
	ADD	$1, R2, R2
	STLXR	R2, (R1), R3
	CBNZ	R3, again
	MOVD	$0, R0
	RET

// void jackShutdown(rtState *st)
TEXT ·jackShutdown(SB), NOSPLIT|NOFRAME, $0-0
	ADD	$rtState_shutdown, R0, R1
	MOVD	$1, R2
	STLR	R2, (R1)
	RET

// void jackSilent(const char *msg)
TEXT ·jackSilent(SB), NOSPLIT|NOFRAME, $0-0
	RET

// void *jackSame(void *port, jack_nframes_t nframes)
TEXT ·jackSame(SB), NOSPLIT|NOFRAME, $0-0
	RET

// func callbackAddrs(a *[8]uintptr)
TEXT ·callbackAddrs(SB), NOSPLIT, $0-8
	MOVD	a+0(FP), R0
	MOVD	$·jackProcess(SB), R1
	MOVD	R1, 0(R0)
	MOVD	$·jackBufferSize(SB), R1
	MOVD	R1, 8(R0)
	MOVD	$·jackRate(SB), R1
	MOVD	R1, 16(R0)
	MOVD	$·jackXrun(SB), R1
	MOVD	R1, 24(R0)
	MOVD	$·jackGraph(SB), R1
	MOVD	R1, 32(R0)
	MOVD	$·jackShutdown(SB), R1
	MOVD	R1, 40(R0)
	MOVD	$·jackSilent(SB), R1
	MOVD	R1, 48(R0)
	MOVD	$·jackSame(SB), R1
	MOVD	R1, 56(R0)
	RET
