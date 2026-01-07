//go:build qemuvirt && aarch64

package main

// Assembly function declarations
// Implementations are in exceptions_arm64.s

func GetExceptionVectorBase() uintptr
func ExceptionVectorTable()
func SetVBAR(addr uintptr)
