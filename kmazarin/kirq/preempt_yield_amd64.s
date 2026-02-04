//go:build !test_stubs

#include "textflag.h"

// asmYieldSVC triggers a voluntary yield via INT 0x80 with syscall number 124.
// This is the x86_64 equivalent of ARM64's SVC #0 with X8=124.
//
// func asmYieldSVC()
TEXT ·asmYieldSVC(SB), NOSPLIT, $0-0
	MOVQ	$124, AX		// syscall number = sched_yield
	INT	$0x80			// Trigger software interrupt
	RET
