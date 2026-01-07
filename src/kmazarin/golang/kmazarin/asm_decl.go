//go:build qemuvirt && aarch64

package main

// Assembly function declarations
// Implementations are in exceptions_arm64.s and runtime_arm64.s

func GetExceptionVectorBase() uintptr
func ExceptionVectorTable()
func SetVBAR(addr uintptr)
func EnableIRQs()
func getAuxval(tag uint64) uint64
func EnableGIC()
func EnableTimerIRQ()
func RearmTimerNow()
