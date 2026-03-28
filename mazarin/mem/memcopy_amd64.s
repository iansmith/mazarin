// memcopy_linux_amd64.s — fast userspace memory copy with store fence.
//
// Uses REP MOVSB which is hardware-optimized on all modern x86_64 processors
// with ERMS (Enhanced REP MOVSB/STOSB). A trailing SFENCE flushes the store
// buffer so all written bytes are globally visible before the function returns.

#include "textflag.h"

// func MemCopy(dst, src unsafe.Pointer, n int64)
TEXT ·MemCopy(SB), NOSPLIT, $0-24
	MOVQ	dst+0(FP), DI		// dst → DI (destination for MOVSB)
	MOVQ	src+8(FP), SI		// src → SI (source for MOVSB)
	MOVQ	n+16(FP), CX		// n → CX (count for MOVSB)

	TESTQ	CX, CX
	JLE	done

	// REP MOVSB: copy CX bytes from [SI] to [DI].
	// On ERMS-capable processors (Ivy Bridge+) the microcode handles
	// alignment and cache-line boundaries internally, making this the
	// fastest general-purpose memcpy for any size.
	CLD				// ensure forward direction
	REP
	MOVSB

	// SFENCE: store fence — drains the store buffer so all preceding
	// stores are globally visible. x86 TSO already orders regular stores,
	// but SFENCE provides an explicit visibility guarantee for the caller.
	SFENCE

done:
	RET
