// fsipc.go — Payload structs for uring-based shepherd↔fs IPC.
//
// Any shepherd can send file operation requests to fs via ProtoFSIPCReq.
// Bulk data (paths, read/write buffers, stat results) flows through a
// pre-shared data area; the uring message carries a VA pointer into that
// area (in fs's address space) so fs can access the data directly.
//
// Connection setup:
//  1. Caller allocates data pages, calls SharePages to map into fs
//  2. Caller sends OpConnect with DataVA (fs's VA) and DataLen (area size)
//  3. fs records the connection, responds with success
//
// Both payloads fit within the 112-byte UringIPCMsg.Payload field.
package ipc

import "unsafe"

// Operation codes for FSIPCReqPayload.Op.
// Reuses the same semantics as shared/fs/fsipc but in a uring context.
const (
	FSOpConnect  uint16 = 0  // establish connection (DataVA + size)
	FSOpOpen     uint16 = 1  // open file → handle
	FSOpClose    uint16 = 2  // close handle
	FSOpRead     uint16 = 3  // read from handle → data area
	FSOpWrite    uint16 = 4  // write data area → handle
	FSOpStat     uint16 = 5  // stat path → data area (128-byte stat buf)
	FSOpFstat    uint16 = 6  // stat handle → data area
	FSOpMkdir    uint16 = 7  // create directory
	FSOpRemove   uint16 = 8  // remove file/dir
	FSOpRename   uint16 = 9  // rename oldpath→newpath (both packed in data area)
	FSOpReadDir  uint16 = 10 // read dirents → data area
	FSOpAccess   uint16 = 11 // check file existence
	FSOpSetMode  uint16 = 12 // chmod
	FSOpSetTimes uint16 = 13 // utimens
	FSOpSync     uint16 = 14 // flush metadata
	FSOpResolve  uint16 = 15 // resolve path → isDir + size
	FSOpTruncate uint16 = 16 // truncate handle to size (Arg0)
)

// FSIPCReqPayload is the payload for ProtoFSIPCReq messages.
// Sent by any shepherd to request a file operation from fs.
//
// Data area convention:
//   - Path operations: caller writes null-terminated path at DataVA[0:PathLen]
//   - Write operations: caller writes data at DataVA[0:DataLen]
//   - fs reads the data area, then may overwrite it with response data
//
// Layout (56 bytes, padded to 8-byte alignment):
//
//	[0:2]   Op       — operation code (FSOpOpen, etc.)
//	[2:4]   PathLen  — path length in data area (0 = no path)
//	[4:8]   Handle   — fs-side handle (from FSOpOpen)
//	[8:12]  Flags    — open flags (O_RDONLY, O_CREAT, etc.)
//	[12:16] Mode     — permission bits
//	[16:24] Arg0     — offset, startIdx, etc.
//	[24:32] Arg1     — count, etc.
//	[32:40] DataVA   — shared data area VA in fs's address space
//	[40:44] DataLen  — bytes of data in data area (for writes)
//	[44:48] ReqID    — request ID for response matching
//	[48:49] RespRing — ring for fs responses (FSOpConnect only, 1..3)
//	[49:50] _pad1
//	[50:56] (trailing alignment padding)
type FSIPCReqPayload struct {
	Op       uint16
	PathLen  uint16
	Handle   uint32
	Flags    uint32
	Mode     uint32
	Arg0     uint64
	Arg1     uint64
	DataVA   uint64
	DataLen  uint32
	ReqID    uint32
	RespRing uint8 // ring for fs to send responses on (FSOpConnect only)
	_pad1    uint8
}

// FSIPCRespPayload is the payload for ProtoFSIPCResp messages.
// Sent by fs back to the requesting shepherd.
//
// Layout (32 bytes):
//
//	[0:4]   ReqID   — matching request ID
//	[4:8]   Err     — 0 on success, negative errno on error
//	[8:16]  Result0 — primary result (handle, bytes read, etc.)
//	[16:24] Result1 — secondary result (ftype<<32|size, etc.)
//	[24:28] DataLen — bytes of response data in shared area
//	[28:32] _pad
type FSIPCRespPayload struct {
	ReqID   uint32
	Err     int32
	Result0 uint64
	Result1 uint64
	DataLen uint32
	_pad    uint32
}

// Compile-time size assertions.
var _ [1]struct{} = [1]struct{}{}              // always true
var _ = (*FSIPCReqPayload)(nil)                // type exists
var _ = (*FSIPCRespPayload)(nil)               // type exists
var _ [112]byte = [unsafe.Sizeof(FSIPCReqPayload{}) + 56]byte{}  // 56 + 56 = 112 (struct padded to 8-byte alignment)
var _ [112]byte = [unsafe.Sizeof(FSIPCRespPayload{}) + 80]byte{} // 32 + 80 = 112

// EncodeFSIPCReq packs a request payload into a UringIPCMsg.
func EncodeFSIPCReq(p *FSIPCReqPayload, senderSID int16) UringIPCMsg {
	var msg UringIPCMsg
	msg.Protocol = ProtoFSIPCReq
	msg.SenderSID = senderSID
	*(*FSIPCReqPayload)(unsafe.Pointer(&msg.Payload[0])) = *p
	return msg
}

// DecodeFSIPCReq extracts the request payload from a UringIPCMsg.
func DecodeFSIPCReq(msg *UringIPCMsg) *FSIPCReqPayload {
	return (*FSIPCReqPayload)(unsafe.Pointer(&msg.Payload[0]))
}

// EncodeFSIPCResp packs a response payload into a UringIPCMsg.
func EncodeFSIPCResp(p *FSIPCRespPayload) UringIPCMsg {
	var msg UringIPCMsg
	msg.Protocol = ProtoFSIPCResp
	*(*FSIPCRespPayload)(unsafe.Pointer(&msg.Payload[0])) = *p
	return msg
}

// DecodeFSIPCResp extracts the response payload from a UringIPCMsg.
func DecodeFSIPCResp(msg *UringIPCMsg) *FSIPCRespPayload {
	return (*FSIPCRespPayload)(unsafe.Pointer(&msg.Payload[0]))
}
