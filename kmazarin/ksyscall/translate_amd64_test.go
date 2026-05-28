//go:build amd64

package ksyscall

import (
	"testing"

	"mazzy/shared/sysid"
)

// MAZ-74 Phase 0: red tests for the amd64 native-syscall-number → SysID
// translation entries for execve and wait4. These will turn green once
// the entries are added to x86ToSysID in translate_amd64.go.
//
// Note: `task test` runs on the host (typically darwin/arm64). These amd64
// tests compile only when go test is invoked with GOARCH=amd64; they're
// the verification anchor for the amd64-side work in this ticket.

const (
	// Linux amd64 syscall numbers (from arch/x86/entry/syscalls/syscall_64.tbl).
	amd64SysExecve = 59
	amd64SysWait4  = 61
)

func TestAmd64TranslateExecve(t *testing.T) {
	got := translateSyscallNum(amd64SysExecve)
	if got != sysid.Execve {
		t.Errorf("translateSyscallNum(%d /* execve */) = %d, want sysid.Execve (%d)",
			amd64SysExecve, got, sysid.Execve)
	}
}

func TestAmd64TranslateWait4(t *testing.T) {
	got := translateSyscallNum(amd64SysWait4)
	if got != sysid.Wait4 {
		t.Errorf("translateSyscallNum(%d /* wait4 */) = %d, want sysid.Wait4 (%d)",
			amd64SysWait4, got, sysid.Wait4)
	}
}

func TestAmd64CloneExecNotReachableFromUserspace(t *testing.T) {
	// CloneExec is a kernel-internal combined-clone+execve call originated
	// by the linux shepherd (MAZ-62 design). It must never appear as the
	// translation target of a real Linux syscall number — userspace can't
	// invoke it directly.
	for num := uint64(0); num < 512; num++ {
		if translateSyscallNum(num) == sysid.CloneExec {
			t.Errorf("x86ToSysID[%d] = sysid.CloneExec, but CloneExec must not be reachable from userspace", num)
		}
	}
}
