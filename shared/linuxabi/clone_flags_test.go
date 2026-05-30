package linuxabi

import "testing"

// MAZ-78 Phase 0: RED tests for the clone-flavor classifier.
//
// IsProcessClone(flags) reports whether a clone(2) call is creating a PROCESS
// (forwarded to the linux shepherd) rather than a THREAD (the existing in-kernel
// path). The discriminator is the CLONE_THREAD bit: the Go runtime ALWAYS sets
// CLONE_THREAD|CLONE_VM when spawning an OS thread, and the os/exec path NEVER
// sets CLONE_THREAD (it uses CLONE_VFORK|CLONE_VM, or a bare SIGCHLD fork). So
// CLONE_THREAD set => thread; clear => process.
//
// These tests fail on current code (IsProcessClone is undefined) and turn green
// once the classifier is implemented.

// goThreadMask is the flag mask the Go runtime passes to clone() when creating
// an OS thread (runtime/os_linux.go cloneFlags), plus CLONE_SETTLS which the
// per-arch assembly ORs in. This MUST classify as a thread (not a process).
const goThreadMask = CLONE_VM | CLONE_FS | CLONE_FILES | CLONE_SIGHAND |
	CLONE_SYSVSEM | CLONE_THREAD | CLONE_SETTLS

func TestIsProcessClone_GoThreadMaskIsNotProcess(t *testing.T) {
	if IsProcessClone(goThreadMask) {
		t.Fatalf("Go thread clone mask %#x must NOT be classified as a process clone", goThreadMask)
	}
}

func TestIsProcessClone_VforkVMIsProcess(t *testing.T) {
	// os/exec via syscall.forkExec: flags |= CLONE_VFORK | CLONE_VM, with the
	// SIGCHLD exit signal in the low byte. CLONE_THREAD is clear => process.
	flags := uint64(CLONE_VFORK | CLONE_VM | SIGCHLD)
	if !IsProcessClone(flags) {
		t.Fatalf("os/exec vfork clone mask %#x must be classified as a process clone", flags)
	}
}

func TestIsProcessClone_BareSigchldIsProcess(t *testing.T) {
	// A plain fork() lowers to clone(SIGCHLD, ...): no CLONE_THREAD => process.
	flags := uint64(SIGCHLD)
	if !IsProcessClone(flags) {
		t.Fatalf("bare SIGCHLD fork mask %#x must be classified as a process clone", flags)
	}
}

func TestIsProcessClone_ThreadBitAloneIsNotProcess(t *testing.T) {
	// Isolate the discriminator: CLONE_THREAD set, nothing else => thread.
	if IsProcessClone(CLONE_THREAD) {
		t.Fatalf("CLONE_THREAD alone (%#x) must NOT be a process clone", CLONE_THREAD)
	}
}

func TestIsProcessClone_ThreadBitWithSigchldLowByteIsNotProcess(t *testing.T) {
	// Defends the discriminator against a false positive: even if a SIGCHLD-like
	// value appears in the low byte, the CLONE_THREAD bit must dominate so the
	// existing thread path is never misrouted to the shepherd.
	flags := uint64(CLONE_THREAD | CLONE_VM | SIGCHLD)
	if IsProcessClone(flags) {
		t.Fatalf("CLONE_THREAD must dominate the low-byte signal; %#x must NOT be a process clone", flags)
	}
}

func TestIsProcessClone_ZeroIsProcess(t *testing.T) {
	// flags == 0 has CLONE_THREAD clear, so it is NOT a thread clone. The
	// classifier routes it to the process path (where the shepherd validates it).
	if !IsProcessClone(0) {
		t.Fatalf("clone flags 0 (CLONE_THREAD clear) must be classified as a process clone")
	}
}
