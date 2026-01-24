//go:build !test_stubs

package main

// Assembly functions (defined in runtime_arm64.s)
func readTTBR0() uint64
func dsb()
func isb()
func invalidateTLB()
