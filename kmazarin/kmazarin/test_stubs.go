//go:build test_stubs

package main

// Stub implementations for test builds — architecture-neutral.
// These functions are implemented in assembly and need stubs when assembly is excluded.
// ARM64-specific stubs are in test_stubs_arm64.go.

//go:nosplit
func GetExceptionVectorBase() uintptr { return 0 }

//go:nosplit
func ExceptionVectorTable() {}

//go:nosplit
func SetVBAR(addr uintptr) {}

//go:nosplit
func ReadVBAR() uintptr { return 0 }

//go:nosplit
func EnableIRQs() {}

//go:nosplit
func DisableIRQs() {}

//go:nosplit
func SaveAndDisableIRQs() uint64 { return 0 }

//go:nosplit
func RestoreIRQs(savedDAIF uint64) {}

//go:nosplit
func GetGRegister() uint64 { return 0 }

//go:nosplit
func GetPC() uint64 { return 0 }

//go:nosplit
func HandlePageFaultAsm(faultAddr uint64) uint64 { return 0 }

//go:nosplit
func HandleUserPageFaultAsm(faultAddr, isPermFault uint64) uint64 { return 0 }

//go:nosplit
func GetSyscallSwitchTarget() int64 { return -1 }

//go:nosplit
func DoContextSwitch(framePtr uint64, targetIdx int32) uint64 { return 0 }

//go:nosplit
func SetSyscallELR(elr uint64) {}

//go:nosplit
func SetSyscallSPSR(spsr uint64) {}

//go:nosplit
func SetSyscallCloneRegs(r12, r13, r9 uint64) {}

//go:nosplit
func CheckThreadPreemption(framePtr uint64) uint64 { return 0 }

//go:nosplit
func RunFirstThread() {}

//go:nosplit
func YieldToReadyThread() {}

//go:nosplit
func sigreturnTrampoline() {}

//go:nosplit
func getSigreturnTrampolineAddr() uintptr { return 0 }

//go:nosplit
func ThreadExitAsm() uint64 { return 0 }

//go:nosplit
func TerminatePriestAsm(pid uint64, status int64) uint64 { return 0 }

func HandleUnhandledExceptionAsm(excInfo, faultAddr, faultPC uint64) uint64 { return 0 }

// Additional stubs for functions called from Go code
func IRQDispatch()       {}
func SyscallDispatch()   {}
func TimerIRQHandler()   {}

// System control
func WaitForInterrupt()    {}
func EnableIRQsAndWait()   {}
func Exit()                {}

// MMU and TLB
func invalidateTLB()       {}
func readTTBR0() uint64    { return 0 }

// Barriers
func dsb() {}
func isb() {}
