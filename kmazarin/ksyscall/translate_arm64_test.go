//go:build arm64

package ksyscall

import (
	"testing"

	"mazzy/shared/sysid"
)

// MAZ-74 Phase 0: red tests for the ARM64 native-syscall-number → SysID
// translation entries for execve and wait4. These will turn green once
// the entries are added to arm64ToSysID in translate_arm64.go.

const (
	// Linux ARM64 syscall numbers (from include/uapi/asm-generic/unistd.h).
	arm64SysExecve = 221
	arm64SysWait4  = 260
)

func TestArm64TranslateExecve(t *testing.T) {
	got := translateSyscallNum(arm64SysExecve)
	if got != sysid.Execve {
		t.Errorf("translateSyscallNum(%d /* execve */) = %d, want sysid.Execve (%d)",
			arm64SysExecve, got, sysid.Execve)
	}
}

func TestArm64TranslateWait4(t *testing.T) {
	got := translateSyscallNum(arm64SysWait4)
	if got != sysid.Wait4 {
		t.Errorf("translateSyscallNum(%d /* wait4 */) = %d, want sysid.Wait4 (%d)",
			arm64SysWait4, got, sysid.Wait4)
	}
}

func TestArm64CloneExecNotReachableFromUserspace(t *testing.T) {
	// CloneExec is a kernel-internal combined-clone+execve call originated
	// by the linux shepherd (MAZ-62 design). It must never appear as the
	// translation target of a real Linux syscall number — userspace can't
	// invoke it directly.
	for num := uint64(0); num < 512; num++ {
		if translateSyscallNum(num) == sysid.CloneExec {
			t.Errorf("arm64ToSysID[%d] = sysid.CloneExec, but CloneExec must not be reachable from userspace", num)
		}
	}
}
