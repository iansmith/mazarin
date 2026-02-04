//go:build !test_stubs

#include "textflag.h"

// asmYieldSVC triggers a voluntary yield via ECALL with syscall number 124.
// This is the RISC-V equivalent of ARM64's SVC.
//
// func asmYieldSVC()
TEXT ·asmYieldSVC(SB), NOSPLIT, $0-0
	MOV	$124, A7		// syscall number in a7
	WORD	$0x00000073		// ECALL
	RET
