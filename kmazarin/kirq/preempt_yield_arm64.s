//go:build !test_stubs

#include "textflag.h"

// asmYieldSVC issues SVC #124 (sched_yield syscall) to trigger a voluntary yield.
// This is used by kernel code to yield when NeedsThreadPreempt is set,
// simulating what would happen if direct function calls also checked for preemption.
//
// func asmYieldSVC()
TEXT ·asmYieldSVC(SB), NOSPLIT, $0-0
	// Issue sched_yield syscall (syscall number 124)
	MOVD	$124, R8    // syscall number in X8
	SVC	$0          // Trigger supervisor call
	RET
