//go:build !test_stubs

package kirq

// asmYieldSVC issues SVC #124 (sched_yield syscall) to trigger a voluntary yield.
// Implemented in assembly: preempt_yield_{arm64,amd64,riscv64}.s
func asmYieldSVC()
