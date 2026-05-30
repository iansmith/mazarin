// execve_layout.go — host-testable core of the execve→clone_exec env-merge
// (MAZ-120), plus the proc-side re-export of the shared argv/envp wire packing.
//
// MergeExecEnv is the env-merge policy locked in MAZ-120 — the child's env is
// the caller's envp OVERLAID with the mandatory mazzy runtime env, and the
// mandatory entry WINS on a key conflict. This is the one necessary deviation
// from pure Linux execve (GODEBUG=gccheckmark=1 et al. are non-negotiable
// runtime invariants; see CLAUDE.md). It is pure (a function over its inputs)
// so it is unit-testable under `task test` (kmazarin/proc is in the test set;
// kmazarin/ksyscall is not), and it runs on the worker thread / SVC entry
// (growable stack, not an IRQ-off path) so slice allocation is fine.
//
// PackArgv / UnpackArgv are SINGLE-SOURCED in shared/linuxabi (the leaf package
// both the kernel and the userspace marshaler import) so the wire format cannot
// drift — same discipline as the IntentOp encoding. They are re-exported here
// via Go function values so the MAZ-120 proc-side spec tests keep their
// unqualified proc.PackArgv / proc.UnpackArgv references.
package proc

import (
	"bytes"

	"mazzy/shared/linuxabi"
)

// PackArgv / UnpackArgv re-export the shared argv/envp packing (single source
// of truth in shared/linuxabi). See linuxabi.PackArgv / linuxabi.UnpackArgv.
var (
	PackArgv   = linuxabi.PackArgv
	UnpackArgv = linuxabi.UnpackArgv
)

// MergeExecEnv builds the child's environment as caller ∪ mandatory with the
// mandatory entry winning on a key conflict (the env-merge policy locked in
// MAZ-120). The result preserves a deterministic order: caller-survivors first
// (in caller order), then every mandatory entry (in mandatory order). No key
// appears twice.
//
// A "key" is the bytes before the first '=' in an entry (an entry with no '='
// is treated as a whole-entry key). Mandatory is assumed already deduped by key
// (it is built by ProcessEnv.SetEnv, which replaces on duplicate key).
func MergeExecEnv(caller, mandatory [][]byte) [][]byte {
	// Index the mandatory keys so we can drop the caller's conflicting entries.
	mandKeys := make(map[string]struct{}, len(mandatory))
	for _, e := range mandatory {
		mandKeys[string(envKey(e))] = struct{}{}
	}

	merged := make([][]byte, 0, len(caller)+len(mandatory))
	// Caller-survivors: every caller entry whose key is NOT overridden by a
	// mandatory entry, in caller order.
	for _, e := range caller {
		if _, overridden := mandKeys[string(envKey(e))]; overridden {
			continue
		}
		merged = append(merged, e)
	}
	// Mandatory always wins: append every mandatory entry verbatim, in order.
	merged = append(merged, mandatory...)
	return merged
}

// envKey returns the key portion of an "key=value" entry (the bytes before the
// first '='), or the whole entry if it contains no '='.
func envKey(entry []byte) []byte {
	if i := bytes.IndexByte(entry, '='); i >= 0 {
		return entry[:i]
	}
	return entry
}
