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
// Why this lives in internal/ rather than maz/linux itself: maz/linux is
// `package main` (a shepherd binary), untestable on the host; internal/ gives
// it a testable home (cf. processrecord/record.go:17-20). The shepherd cannot
// import kmazarin/proc (kernel package, wrong address space), so the request
// the builder emits is a shepherd-side VALUE description that MAZ-120
// translates into the kernel's proc.CloneExecRequest at the SVC boundary.
//
// Single source of truth (MAZ-118 locked decision): the buffered-intent
// encoding (IntentOp / IntentKind) and the caps (MaxStartupIntentOps /
// MaxStartupCwdBytes) are IMPORTED from shared/linuxabi — the SAME types the
// kernel (kmazarin/proc) uses via aliases and the clone-buffering window
// (maz/linux/internal/cloneexec) uses directly. This package re-exports them
// via Go TYPE ALIASES and const re-exports (mirroring proc's reconciliation)
// so existing unqualified references keep compiling, but there is NO second
// copy to drift. NEVER redefine the op kinds, the arg layout, or the caps
// here.
//
// Boundaries (who owns what):
//
//   - MAZ-118 (clone buffering window): its Flush returns the buffered
//     []linuxabi.IntentOp + cwd that the builder consumes — handed in as
//     plain values, no kernel-package import.
//   - MAZ-120 (integration): consumes Build's CloneExecRequest, packs/marshals
//     it across the SVC, stages the ELF, and lays out faithful argv[0]/envp.
//   - MAZ-67 (future): fills the coreutils seam (CoreutilsRoute table).
//
// Caps: the builder enforces MaxStartupIntentOps (16) and MaxStartupCwdBytes
// (256), returning E2BIG / ENAMETOOLONG-equivalent errors that the kernel
// re-checks in kmazarin/ksyscall/clone_exec.go (ceE2BIG=-7,
// ceENAMETOOLONG=-36). Because the caps are imported from linuxabi, the
// shepherd's early reject and the kernel's authoritative re-check cannot
// drift.
//
// Concurrency: NOT goroutine-safe. The builder is a pure function over its
// inputs; CoreutilsRoute is a read-only table lookup. The linux shepherd's
// main event loop is the sole user.

package execve

import (
	"bytes"
	"errors"
	"path"

	"mazzy/shared/linuxabi"
)

// Sizing caps, re-exported from linuxabi (the single source of truth shared
// with kmazarin/proc and maz/linux/internal/cloneexec). The builder
// cap-checks against these; the kernel RE-checks the same shared values.
const (
	MaxStartupIntentOps = linuxabi.MaxStartupIntentOps
	MaxStartupCwdBytes  = linuxabi.MaxStartupCwdBytes
)

// IntentKind is an alias of linuxabi.IntentKind — the same type the kernel
// (proc.CloneExecIntentKind) and the clone-buffering window use, so the three
// sides cannot drift. IntentNone is the sentinel zero value.
type IntentKind = linuxabi.IntentKind

// Kind sentinels, re-exported from linuxabi so call sites keep using the
// unqualified names.
const (
	IntentNone   = linuxabi.IntentNone   // sentinel / zero
	IntentDup3   = linuxabi.IntentDup3   // Arg0=oldfd, Arg1=newfd, Arg2=flags
	IntentClose  = linuxabi.IntentClose  // Arg0=fd
	IntentFSetFD = linuxabi.IntentFSetFD // Arg0=fd, Arg1=flags (FD_CLOEXEC, etc.)
)

// IntentOp is an alias of linuxabi.IntentOp — one buffered FD-flavored op.
// Chdir is NOT an op kind — the chdir target rides in the builder's separate
// cwd input, matching Shepherd.StartupCwd.
type IntentOp = linuxabi.IntentOp

// CloneExecRequest is the shepherd-side VALUE description of an execve,
// produced by Build. It carries the marshaled argv/envp, the buffered
// intent + cwd, and the resolved target filename. MAZ-120 translates this
// into the kernel's proc.CloneExecRequest (adding the ELF VA/size/page
// scalars + CallerShepherd identity) at the SVC boundary; those kernel-only
// fields are intentionally absent here so this package stays pure.
type CloneExecRequest struct {
	Filename string     // resolved absolute target path (the kernel load path)
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

// UnpackArgs splits a NUL-separated packed blob (the inverse of MAZ-120's
// kernel-side packing and of readPackedArgs at runshepherd.go:107-131) into
// its component byte slices. Like readPackedArgs, zero-length runs between
// NULs are skipped (no empty elements in v1) and a trailing NUL is optional —
// a non-empty final element is emitted even without one. An empty blob yields
// a nil result.
//
// The returned slices ALIAS blob (no copy), matching Build's zero-copy
// ownership model: the caller (MAZ-120) hands ownership of blob to the
// request and must keep it alive for the request's lifetime.
func UnpackArgs(blob []byte) [][]byte {
	return bytes.FieldsFunc(blob, func(r rune) bool { return r == 0 })
}

// ResolvePath resolves a target path for execve. An absolute path (leading
// '/') is cleaned and returned (cmd/go hands absolute tool paths — design
// doc §1). A relative path is joined onto cwd and cleaned. There is no $PATH
// search in v1. Uses stdlib "path" (Linux "/"-separated targets), NOT
// path/filepath.
func ResolvePath(p, cwd string) string {
	if len(p) > 0 && p[0] == '/' {
		return path.Clean(p)
	}
	return path.Clean(cwd + "/" + p)
}

// CoreutilsRoute checks the coreutils.maz routing table BEFORE the ELF
// branch. v1 returns ("", false) for every path (empty table) so every
// target takes the ELF branch. MAZ-67 fills the table here.
func CoreutilsRoute(p string) (mazPath string, ok bool) {
	return "", false
}

// Build assembles a CloneExecRequest from the resolved target filename, the
// unpacked argv/envp, and the buffered intent + cwd flushed from the clone
// window (MAZ-118). It enforces the intent-op and cwd caps, returning
// ErrIntentOverflow / ErrCwdOverflow (no truncation) so the request is
// rejected before it reaches the kernel's authoritative re-check. argv must
// be non-empty (ErrEmptyArgv otherwise). The returned request's Filename is
// the resolved load path; argv[0] is left exactly as the caller supplied it
// (faithful execve argv[0] for MAZ-120 — never overwritten with Filename).
//
// The request aliases the caller's argv/envp/intent/cwd slices: Build is the
// terminal consumer of values flushed from the clone window and unpacked from
// the wire, and the caller hands ownership. UnpackArgs's args alias the packed
// blob and cloneexec.Flush returns the window's own slices, so the caller
// (MAZ-120) must keep those backing buffers alive for the request's lifetime.
func Build(filename string, argv, envp [][]byte, intent []IntentOp, cwd []byte) (CloneExecRequest, error) {
	if len(argv) == 0 {
		return CloneExecRequest{}, ErrEmptyArgv
	}
	if len(intent) > MaxStartupIntentOps {
		return CloneExecRequest{}, ErrIntentOverflow
	}
	if len(cwd) > MaxStartupCwdBytes {
		return CloneExecRequest{}, ErrCwdOverflow
	}
	return CloneExecRequest{
		Filename: filename,
		Argv:     argv,
		Envp:     envp,
		Intent:   intent,
		Cwd:      cwd,
	}, nil
}
