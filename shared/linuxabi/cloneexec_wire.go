// cloneexec_wire.go — the clone_exec SVC parameter wire format (MAZ-120).
//
// SINGLE SOURCE OF TRUTH. The SyscallCloneExec SVC carries far more than the
// six scalar registers can hold: a staged ELF VA range, the faithful argv and
// envp (each a ragged byte-slice list), the buffered FD intent, the chdir
// target, and the diagnostic filename. The ELF rides in its own VA range
// (arg0-2); everything else is marshaled into ONE contiguous "params" region
// (arg3-5, a SharePages-style page array the kernel reads via copyPagesFromUser
// exactly like the ELF).
//
// This file defines that params encoding ONCE so the userspace marshaler
// (mazarin/sys.CloneExec) and the kernel unmarshaler (kmazarin/ksyscall's SVC
// entry) cannot drift — the same discipline the intent-op encoding follows
// (cloneexec.go). The argv/envp packing (PackArgv/UnpackArgv) lives here too
// for the same reason; kmazarin/proc re-exports it via Go aliases so the
// MAZ-120 proc-side spec tests keep their unqualified references.

package linuxabi

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// CloneExecParamsHeaderSize is the fixed-size header that prefixes the params
// region. All five lengths are uint32 LE; the trailing reserved word keeps the
// header 8-byte aligned so the IntentOp array (4-byte fields) that may follow
// the blobs stays naturally aligned if a future reader maps it in place.
const CloneExecParamsHeaderSize = 24

// CloneExecArgMax bounds the marshaled params region (argv + envp + intent +
// cwd + filename + header). It mirrors a Linux-like ARG_MAX ceiling so a
// runaway argv/envp is rejected with E2BIG instead of overflowing the child's
// user stack. 128 KiB matches the conservative end of Linux's ARG_MAX range
// and comfortably fits the largest realistic `go build` invocation.
const CloneExecArgMax = 128 * 1024

// ErrCloneExecParamsMalformed is returned by UnmarshalCloneExecParams when the
// blob is too short for its declared header or the declared section lengths run
// past the blob.
var ErrCloneExecParamsMalformed = errors.New("linuxabi: malformed clone_exec params blob")

// ErrCloneExecArgTooBig is returned by MarshalCloneExecParams when the marshaled
// region would exceed CloneExecArgMax (E2BIG at the syscall boundary).
var ErrCloneExecArgTooBig = errors.New("linuxabi: clone_exec params exceed ARG_MAX")

// PackArgv joins a ragged argv (or envp) into a single NUL-separated blob. A
// nil/empty input packs to a nil blob. UnpackArgv is its exact inverse for any
// input with no empty elements (v1 argv/envp never contain empty strings).
func PackArgv(argv [][]byte) []byte {
	if len(argv) == 0 {
		return nil
	}
	return bytes.Join(argv, []byte{0})
}

// UnpackArgv splits a NUL-separated blob back into its components — the inverse
// of PackArgv. Zero-length runs between NULs are skipped (no empty elements in
// v1) and a trailing NUL is optional, mirroring the kernel's readPackedArgs and
// maz/linux/internal/execve.UnpackArgs. An empty blob yields nil. The returned
// slices ALIAS blob; the caller must keep blob alive for their lifetime.
func UnpackArgv(blob []byte) [][]byte {
	return bytes.FieldsFunc(blob, func(r rune) bool { return r == 0 })
}

// intentOpBytes is the on-wire size of one IntentOp (pinned at 16 by
// TestIntentOpSize). The intent array is marshaled as a flat little-endian
// run of these.
const intentOpBytes = 16

// MarshalCloneExecParams serializes the non-ELF clone_exec parameters into one
// contiguous params region: a fixed header followed by packed argv, packed
// envp, the intent ops, the cwd, and the filename. argv/envp are passed
// already-packed (the marshaler is the terminal consumer of the caller's argv).
// vforkCallerTID is the kernel TID of the vfork transient thread that issued
// the execve, and vforkCallerSID is that thread's shepherd SID (the real parent,
// not the linux delegate). Both are 0 for non-vfork calls.
// Returns ErrCloneExecArgTooBig if the region would exceed CloneExecArgMax.
func MarshalCloneExecParams(packedArgv, packedEnvp []byte, intent []IntentOp, cwd, filename []byte, vforkCallerTID, vforkCallerSID int16) ([]byte, error) {
	total := CloneExecParamsHeaderSize + len(packedArgv) + len(packedEnvp) +
		len(intent)*intentOpBytes + len(cwd) + len(filename)
	if total > CloneExecArgMax {
		return nil, ErrCloneExecArgTooBig
	}

	out := make([]byte, total)
	binary.LittleEndian.PutUint32(out[0:4], uint32(len(packedArgv)))
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(packedEnvp)))
	binary.LittleEndian.PutUint32(out[8:12], uint32(len(intent)))
	binary.LittleEndian.PutUint32(out[12:16], uint32(len(cwd)))
	binary.LittleEndian.PutUint32(out[16:20], uint32(len(filename)))
	// [20:22] = vforkCallerTID (int16 LE), [22:24] = vforkCallerSID (int16 LE).
	binary.LittleEndian.PutUint16(out[20:22], uint16(vforkCallerTID))
	binary.LittleEndian.PutUint16(out[22:24], uint16(vforkCallerSID))


	off := CloneExecParamsHeaderSize
	off += copy(out[off:], packedArgv)
	off += copy(out[off:], packedEnvp)
	for i := range intent {
		putIntentOp(out[off:], &intent[i])
		off += intentOpBytes
	}
	off += copy(out[off:], cwd)
	copy(out[off:], filename)
	return out, nil
}

// CloneExecParams is the decoded view of a params region. Its byte slices ALIAS
// the source blob; the caller keeps the blob alive for their lifetime.
type CloneExecParams struct {
	Argv           [][]byte
	Envp           [][]byte
	Intent         []IntentOp
	Cwd            []byte
	Filename       []byte
	VforkCallerTID int16 // kernel TID of the transient vfork thread, or 0
	VforkCallerSID int16 // shepherd SID of the real parent (not linux delegate), or 0
}

// UnmarshalCloneExecParams decodes a params region produced by
// MarshalCloneExecParams. It validates that every declared section fits within
// blob; a short or over-running blob returns ErrCloneExecParamsMalformed.
func UnmarshalCloneExecParams(blob []byte) (CloneExecParams, error) {
	if len(blob) < CloneExecParamsHeaderSize {
		return CloneExecParams{}, ErrCloneExecParamsMalformed
	}
	argvLen := int(binary.LittleEndian.Uint32(blob[0:4]))
	envpLen := int(binary.LittleEndian.Uint32(blob[4:8]))
	intentCount := int(binary.LittleEndian.Uint32(blob[8:12]))
	cwdLen := int(binary.LittleEndian.Uint32(blob[12:16]))
	filenameLen := int(binary.LittleEndian.Uint32(blob[16:20]))
	vforkCallerTID := int16(binary.LittleEndian.Uint16(blob[20:22]))
	vforkCallerSID := int16(binary.LittleEndian.Uint16(blob[22:24]))

	off := CloneExecParamsHeaderSize
	end := off + argvLen + envpLen + intentCount*intentOpBytes + cwdLen + filenameLen
	if end > len(blob) || end < off {
		return CloneExecParams{}, ErrCloneExecParamsMalformed
	}

	argvBlob := blob[off : off+argvLen]
	off += argvLen
	envpBlob := blob[off : off+envpLen]
	off += envpLen
	intent := make([]IntentOp, intentCount)
	for i := 0; i < intentCount; i++ {
		getIntentOp(blob[off:], &intent[i])
		off += intentOpBytes
	}
	cwd := blob[off : off+cwdLen]
	off += cwdLen
	filename := blob[off : off+filenameLen]

	return CloneExecParams{
		Argv:           UnpackArgv(argvBlob),
		Envp:           UnpackArgv(envpBlob),
		Intent:         intent,
		Cwd:            cwd,
		Filename:       filename,
		VforkCallerTID: vforkCallerTID,
		VforkCallerSID: vforkCallerSID,
	}, nil
}

// putIntentOp serializes one IntentOp into dst[0:16] in little-endian order.
func putIntentOp(dst []byte, op *IntentOp) {
	dst[0] = byte(op.Kind)
	dst[1], dst[2], dst[3] = 0, 0, 0 // _pad
	binary.LittleEndian.PutUint32(dst[4:8], uint32(op.Arg0))
	binary.LittleEndian.PutUint32(dst[8:12], uint32(op.Arg1))
	binary.LittleEndian.PutUint32(dst[12:16], uint32(op.Arg2))
}

// getIntentOp deserializes one IntentOp from src[0:16].
func getIntentOp(src []byte, op *IntentOp) {
	op.Kind = IntentKind(src[0])
	op.Arg0 = int32(binary.LittleEndian.Uint32(src[4:8]))
	op.Arg1 = int32(binary.LittleEndian.Uint32(src[8:12]))
	op.Arg2 = int32(binary.LittleEndian.Uint32(src[12:16]))
}
