// shareipc.go — Payload structs for mazarin/transfer Mode 2 Share IPC (MAZ-53).
//
// Two messages:
//
//   - ProtoShareReq (sender → consumer): announces a newly-mapped Share.
//     The sender has already called sys.SharePagesWithTarget; the pages are
//     in the consumer's address space at VA. The consumer constructs a
//     Share{VA, Bytes, id, senderSID} from this payload + the msg's SenderSID.
//
//   - ProtoShareRelease (consumer → sender): consumer signals done. The
//     sender looks up ShareID in its outstanding-shares table and calls
//     sys.UnshareFromTarget to revoke the mapping.
//
// Both are fire-and-forget at the IPC layer; sender retains ownership of
// the underlying physical pages throughout.
//
// See design/mazarin-transfer-state-machine.md for the full lifecycle.
package ipc

import "unsafe"

// ShareReqPayload is the payload for ProtoShareReq messages.
//
// Layout (24 bytes):
//
//	[0:4]   ShareID — sender-assigned correlation token, echoed on Release.
//	[4:8]   Bytes   — byte count visible to the consumer (may be sub-page).
//	[8:16]  VA      — start of the consumer's mapped region.
//	[16:24] _pad
type ShareReqPayload struct {
	ShareID uint32
	Bytes   uint32
	VA      uint64
	_pad    [8]byte
}

// ShareReleasePayload is the payload for ProtoShareRelease messages.
//
// Layout (8 bytes):
//
//	[0:4]   ShareID — the sender-assigned token from the original ProtoShareReq.
//	[4:8]   _pad
type ShareReleasePayload struct {
	ShareID uint32
	_pad    [4]byte
}

// Compile-time guarantees that both payloads fit in UringIPCMsg.Payload.
// Self-updating against UringIPCMsg.Payload's actual size, so a future
// Payload resize re-checks automatically.
var _ [unsafe.Sizeof(UringIPCMsg{}.Payload) - unsafe.Sizeof(ShareReqPayload{})]byte
var _ [unsafe.Sizeof(UringIPCMsg{}.Payload) - unsafe.Sizeof(ShareReleasePayload{})]byte

// EncodeShareReq packs a Share announcement into a UringIPCMsg.
func EncodeShareReq(p *ShareReqPayload, senderSID int16) UringIPCMsg {
	var msg UringIPCMsg
	msg.Protocol = ProtoShareReq
	msg.SenderSID = senderSID
	*(*ShareReqPayload)(unsafe.Pointer(&msg.Payload[0])) = *p
	return msg
}

// DecodeShareReq extracts the Share announcement from a UringIPCMsg.
func DecodeShareReq(msg *UringIPCMsg) *ShareReqPayload {
	return (*ShareReqPayload)(unsafe.Pointer(&msg.Payload[0]))
}

// EncodeShareRelease packs a Release notification into a UringIPCMsg.
func EncodeShareRelease(p *ShareReleasePayload, senderSID int16) UringIPCMsg {
	var msg UringIPCMsg
	msg.Protocol = ProtoShareRelease
	msg.SenderSID = senderSID
	*(*ShareReleasePayload)(unsafe.Pointer(&msg.Payload[0])) = *p
	return msg
}

// DecodeShareRelease extracts the Release notification from a UringIPCMsg.
func DecodeShareRelease(msg *UringIPCMsg) *ShareReleasePayload {
	return (*ShareReleasePayload)(unsafe.Pointer(&msg.Payload[0]))
}
