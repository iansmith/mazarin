// Package linuxabi holds Linux-ABI value types and caps that must be shared
// VERBATIM between the kernel (kmazarin/proc) and the linux shepherd
// (maz/linux). It is a pure-leaf package: it imports nothing from mazzy, so
// both the kernel and userspace sides can depend on it without an import
// cycle (shared/ never imports kmazarin/).
//
// =====================================================================
// SINGLE SOURCE OF TRUTH (MAZ-118 locked decision).
//
// Historically the clone_exec buffered-intent encoding lived in
// kmazarin/proc, and any shepherd-side code (maz/linux) had to MIRROR it by
// convention — a latent drift class (cf. shared/ipc/uring_ring.go's
// NotifyType* mirror). MAZ-118 collapses that: the intent op (Kind +
// Arg0/1/2) and the caps (MaxStartupIntentOps, MaxStartupCwdBytes) are
// defined ONCE here and imported by BOTH sides:
//
//   - kmazarin/proc reconciles its CloneExecIntentOp / CloneExecIntentKind /
//     MaxStartupIntentOps / MaxStartupCwdBytes to these via Go TYPE ALIASES
//     and const re-exports, so the already-merged MAZ-75 kernel call sites
//     (Shepherd.StartupIntent array, ksyscall cap-checks) compile unchanged.
//   - maz/linux/internal/cloneexec imports these directly — NO local mirror.
//
// MAZ-79 / MAZ-113 / MAZ-120 import this package; they must NEVER redefine
// the op kinds, the arg layout, or the caps.
// =====================================================================
//
// This file is the Phase-0 STUB for MAZ-118. The types/consts below exist so
// the RED tests in cloneexec_test.go (and the proc-side / shepherd-side
// reconciliation tests) COMPILE and FAIL on their assertions. The
// implementation phase keeps these definitions (they are the spec) and wires
// the proc alias + the cloneexec import; the only thing "red" about this stub
// is that the alias/import reconciliation does not exist yet, so the
// cross-package drift guards fail.

package linuxabi

// Caps on the buffered in-child intent the kernel stores per shepherd.
// os/exec typically uses 3-5 FD ops (dup3 stdout + dup3 stderr + close pipe
// ends + F_SETFD on inherited FDs); 16 covers comfortably. The cwd cap
// follows the same "most paths < 256" reasoning as the kernel side. The
// shepherd cap-checks against these; the kernel RE-checks the same values
// (so a divergence would silently desync the two — which is exactly why they
// live here once).
const (
	MaxStartupIntentOps = 16
	MaxStartupCwdBytes  = 256
)

// IntentKind discriminates the buffered FD-flavored ops. IntentNone is the
// sentinel zero value so unused trailing slots in the kernel's
// Shepherd.StartupIntent array are never misinterpreted as real ops.
type IntentKind uint8

const (
	IntentNone   IntentKind = iota // sentinel / zero
	IntentDup3                     // Arg0=oldfd, Arg1=newfd, Arg2=flags
	IntentClose                    // Arg0=fd
	IntentFSetFD                   // Arg0=fd, Arg1=flags (FD_CLOEXEC, etc.)
)

// IntentOp is one buffered FD-flavored op. Its size is pinned at 16 bytes
// (see TestIntentOpSize); the kernel's per-Shepherd StartupIntent array
// budget assumes 16-byte ops, so any size change is deliberate. The explicit
// _pad keeps Arg0 4-byte-aligned and the struct at exactly 16 bytes.
//
// Chdir is NOT an op kind — at most one chdir per clone_exec is meaningful,
// so it lives in the window's cwd field / Shepherd.StartupCwd, not here.
type IntentOp struct {
	Kind IntentKind
	_pad [3]byte // alignment to 4 so Arg0 is 4-byte-aligned; pins size at 16
	Arg0 int32
	Arg1 int32
	Arg2 int32
}
