// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// DIPLOMAT OVERLAY: Stub out Windows system calls to eliminate DLL imports
//
// This file replaces runtime/sys_windows_amd64.s
// It removes all kernel32.dll imports and provides stub implementations

#include "textflag.h"
#include "go_asm.h"

// Stubs for exception handling

TEXT runtime·exceptiontramp(SB),NOSPLIT,$0
	RET

TEXT runtime·firstcontinuetramp(SB),NOSPLIT,$0
	RET

TEXT runtime·lastcontinuetramp(SB),NOSPLIT,$0
	RET

TEXT runtime·callbackasm(SB),NOSPLIT,$0
	RET

TEXT runtime·callbackasm1(SB),NOSPLIT,$0
	RET

// Stub for sigreturn (not used on Windows but may be referenced)
TEXT runtime·sigreturn(SB),NOSPLIT,$0
	RET

// Stubs for stdcall functions
// These replace the Windows API calling stubs

TEXT runtime·stdcall0(SB),NOSPLIT,$0
	MOVQ	$0, AX
	RET

TEXT runtime·stdcall1(SB),NOSPLIT,$0
	MOVQ	$0, AX
	RET

TEXT runtime·stdcall2(SB),NOSPLIT,$0
	MOVQ	$0, AX
	RET

TEXT runtime·stdcall3(SB),NOSPLIT,$0
	MOVQ	$0, AX
	RET

TEXT runtime·stdcall4(SB),NOSPLIT,$0
	MOVQ	$0, AX
	RET

TEXT runtime·stdcall5(SB),NOSPLIT,$0
	MOVQ	$0, AX
	RET

TEXT runtime·stdcall6(SB),NOSPLIT,$0
	MOVQ	$0, AX
	RET

TEXT runtime·stdcall7(SB),NOSPLIT,$0
	MOVQ	$0, AX
	RET

TEXT runtime·stdcall8(SB),NOSPLIT,$0
	MOVQ	$0, AX
	RET

// NO kernel32.dll imports - everything is stubbed for UEFI
