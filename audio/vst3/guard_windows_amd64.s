#include "textflag.h"

// continueGuard is a vectored continue handler, called by Windows, not by Go:
// the pointer to the EXCEPTION_POINTERS comes in CX, and the answer goes out
// in AX. It runs in the midst of exception dispatch, perhaps on a driver's
// thread, so it uses volatile registers alone and calls nothing.
//
// See guard_windows_amd64.go for what it decides, and why.
TEXT ·continueGuard(SB),NOSPLIT|NOFRAME,$0-0
	MOVQ	0(CX), AX		// EXCEPTION_POINTERS.ExceptionRecord
	MOVQ	8(CX), DX		// EXCEPTION_POINTERS.ContextRecord
	MOVQ	16(AX), R8		// EXCEPTION_RECORD.ExceptionAddress
	MOVQ	0xF8(DX), R9		// CONTEXT.Rip
	CMPQ	R8, R9
	JEQ	search			// Nothing moved it: no one recovered.
	MOVQ	·imageLo(SB), R10
	MOVQ	·imageHi(SB), R11
	CMPQ	R8, R10
	JCS	faultOutside
	CMPQ	R8, R11
	JCS	search			// The fault is Go's.
faultOutside:
	CMPQ	R9, R10
	JCS	resumeOutside
	CMPQ	R9, R11
	JCS	search			// It resumes in Go: Go's own doing.
resumeOutside:
	MOVQ	R8, ·recoveredAt(SB)
	LOCK
	INCQ	·recoveredN(SB)
	MOVL	$-1, AX			// EXCEPTION_CONTINUE_EXECUTION
	RET
search:
	XORL	AX, AX			// EXCEPTION_CONTINUE_SEARCH
	RET

// func continueGuardPC() uintptr
TEXT ·continueGuardPC(SB),NOSPLIT,$0-8
	LEAQ	·continueGuard(SB), AX
	MOVQ	AX, ret+0(FP)
	RET
