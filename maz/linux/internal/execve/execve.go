// Package execve holds the linux shepherd's PURE, host-testable execve
// dispatch core (MAZ-79): argv/envp unpacking, target-path resolution, the
// CloneExecRequest builder, and the coreutils.maz routing seam.
//
// Scope (MAZ-79, re-scoped 2026-05-29): this is the value-machine half of
// execve. It performs NO IPC and NO fs calls — every external input (the
// raw packed argv/envp blob, the resolved CWD, the buffered intent from the
// clone window, the ELF size) is HANDED to it. The integration half — the
// kernel SyscallCloneExec SVC entry, the execve delegate packing, the
// maz/linux dispatch case that stages the ELF into mmap pages and issues the
// SVC, and the faithful argv[0]/envp kernel stack layout — is MAZ-120.
//
// Why this lives in internal/ and mirrors kmazarin/proc rather than
// importing it: maz/linux is `package main` (a shepherd binary), untestable
// on the host; internal/ gives it a testable home (cf.
// processrecord/record.go:17-20). The shepherd cannot import kmazarin/proc
// (kernel package, wrong address space), so the request the builder emits is
// a shepherd-side VALUE description that MAZ-120 translates into the kernel's
// proc.CloneExecRequest at the SVC boundary.
//
// Boundaries (who owns what):
//
//   - MAZ-118 (clone buffering window): its Flush returns the buffered
//     []IntentOp + cwd that the builder consumes. To stay landable while
//     MAZ-118 is unmerged AND decoupled from a kernel-package import, this
//     package defines its own IntentOp mirror (see IntentOp). MAZ-120's
//     wiring converts cloneexec.IntentOp → execve.IntentOp at the call site.
//     The "define once in shared/" question (raised by MAZ-118 too) is an
//     open design decision for Ian — see findings.md.
//   - MAZ-120 (integration): consumes Build's CloneExecRequest, packs/marshals
//     it across the SVC, stages the ELF, and lays out faithful argv[0]/envp.
//   - MAZ-67 (future): fills the coreutils seam (CoreutilsRoute table).
//
// Caps: the builder enforces MaxStartupIntentOps (16) and MaxStartupCwdBytes
// (256), returning E2BIG / ENAMETOOLONG-equivalent errors that MIRROR the
// kernel's re-check in kmazarin/ksyscall/clone_exec.go:77-84 (ceE2BIG=-7,
// ceENAMETOOLONG=-36). A drift here desyncs the shepherd's early reject from
// the kernel's authoritative re-check.
//
// Concurrency: NOT goroutine-safe. The builder is a pure function over its
// inputs; CoreutilsRoute is a read-only table lookup. The linux shepherd's
// main event loop is the sole user.
//
// =====================================================================
// STUB — Phase 0 (MAZ-79). The types and function set below exist so the
// RED tests in execve_test.go COMPILE and FAIL on assertions (RED state),
// matching the processrecord (record.go) and cloneexec (window.go) Phase-0
// pattern. The bodies are deliberately non-functional (they return zero
// values, NOT panics, so the test binary runs and each test reports a clean
// per-assertion FAIL); the implementation phase replaces them with the real
// marshaling / path-resolve / builder / routing logic.
// =====================================================================

package execve

import "errors"

// Mirror sizing caps. These MUST equal kmazarin/proc.MaxStartupIntentOps
// (16) and proc.MaxStartupCwdBytes (256). They are duplicated here (not
// imported) because the shepherd cannot pull in the kernel package; same
// discipline as cloneexec/window.go. A drift desyncs the shepherd's
// cap-check from the kernel's authoritative re-check (clone_exec.go:77-84)
// — see findings.md for the "define once in shared/" open question.
const (
	MaxStartupIntentOps = 16
	MaxStartupCwdBytes  = 256
)

// IntentKind mirrors kmazarin/proc.CloneExecIntentKind (and
// cloneexec.IntentKind). IntentNone is the sentinel zero value.
type IntentKind uint8

const (
	IntentNone   IntentKind = iota // sentinel / zero
	IntentDup3                     // Arg0=oldfd, Arg1=newfd, Arg2=flags
	IntentClose                    // Arg0=fd
	IntentFSetFD                   // Arg0=fd, Arg1=flags (FD_CLOEXEC, etc.)
)

// IntentOp is one buffered FD-flavored op, mirroring
// kmazarin/proc.CloneExecIntentOp's kinds and arg slots exactly. Chdir is
// NOT an op kind — the chdir target rides in the builder's separate cwd
// input, matching Shepherd.StartupCwd.
type IntentOp struct {
	Kind IntentKind
	Arg0 int32
	Arg1 int32
	Arg2 int32
}

// CloneExecRequest is the shepherd-side VALUE description of an execve,
// produced by Build. It carries the marshaled argv/envp, the buffered
// intent + cwd, and the resolved target filename. MAZ-120 translates this
// into the kernel's proc.CloneExecRequest (adding the ELF VA/size/page
// scalars + CallerShepherd identity) at the SVC boundary; those kernel-only
// fields are intentionally absent here so this package stays pure.
type CloneExecRequest struct {
	Filename string     // resolved absolute target path (also argv[0] source for logging)
	Argv     [][]byte   // unpacked argument vector (argv[0] is the program name)
	Envp     [][]byte   // unpacked environment vector
	Intent   []IntentOp // buffered FD-flavored ops (≤ MaxStartupIntentOps)
	Cwd      []byte     // chdir target; nil/empty = no chdir (≤ MaxStartupCwdBytes)
}

// Sentinel errors. Distinct so callers (and tests) can branch via errors.Is.
// They map to the kernel's negative errnos that clone_exec.go re-checks.
var (
	// ErrIntentOverflow — intent op count exceeds MaxStartupIntentOps
	// (E2BIG-equiv; kernel ceE2BIG=-7).
	ErrIntentOverflow = errors.New("execve: buffered intent op count exceeds cap")
	// ErrCwdOverflow — cwd byte length exceeds MaxStartupCwdBytes
	// (ENAMETOOLONG-equiv; kernel ceENAMETOOLONG=-36).
	ErrCwdOverflow = errors.New("execve: chdir target path exceeds cap")
	// ErrEmptyArgv — Build called with no argv[0] (execve with empty argv is
	// invalid; the program name is required for the request Filename/argv[0]).
	ErrEmptyArgv = errors.New("execve: argv is empty (argv[0] required)")
)

// UnpackArgs splits a null-separated packed blob (the wire form MAZ-120
// produces kernel-side and the inverse of readPackedArgs at
// runshepherd.go:107-131) into its component byte slices. A trailing NUL is
// not required. An empty blob yields a nil/empty result.
func UnpackArgs(blob []byte) [][]byte {
	return nil // STUB
}

// ResolvePath resolves a target path for execve. An absolute path (leading
// '/') passes through unchanged (cmd/go hands absolute tool paths — design
// doc §1). A relative path is resolved against cwd (the per-PID CWD from
// processrecord.PerPIDRecord.CWD). No $PATH search in v1. The result is a
// cleaned absolute path.
func ResolvePath(path, cwd string) string {
	return "" // STUB
}

// CoreutilsRoute checks the coreutils.maz routing table BEFORE the ELF
// branch. v1 returns ("", false) for every path (empty table) so every
// target takes the ELF branch. MAZ-67 fills the table.
func CoreutilsRoute(path string) (mazPath string, ok bool) {
	return "", false // STUB
}

// Build assembles a CloneExecRequest from the resolved target filename, the
// unpacked argv/envp, and the buffered intent + cwd flushed from the clone
// window (MAZ-118). It enforces the intent-op and cwd caps, returning
// ErrIntentOverflow / ErrCwdOverflow (no truncation) so the request is
// rejected before it reaches the kernel's authoritative re-check. argv must
// be non-empty (ErrEmptyArgv otherwise). The returned request's Filename is
// the resolved path; argv[0] is left as the caller supplied it.
func Build(filename string, argv, envp [][]byte, intent []IntentOp, cwd []byte) (CloneExecRequest, error) {
	return CloneExecRequest{}, nil // STUB
}
