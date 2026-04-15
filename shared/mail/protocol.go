// Package mail defines the uring IPC protocol between the mail shepherd
// and the maildb shepherd.
//
// Payload layout follows the same convention as shared/wm:
// [0:4] MsgType uint32, [4:112] message body (108 bytes max).
package mail

import (
	"mazzy/shared/ipc"
	"unsafe"
)

// MsgType values for ProtoMailReq / ProtoMailResp.
const (
	// Requests (mail → maildb)
	MsgTypeGetHeaders  uint32 = 1 // Request headers starting from a date, going backwards
	MsgTypeGetBody     uint32 = 2 // Request body text for a specific message
	MsgTypeBodyConfirm uint32 = 3 // Confirm body pages have been read, can be released

	// Responses (maildb → mail)
	MsgTypeHeadersResult uint32 = 10 // One header entry (repeated per result)
	MsgTypeHeadersEnd    uint32 = 11 // End of headers batch
	MsgTypeBodyResult    uint32 = 12 // Body available via shared pages
	MsgTypeErrorResult   uint32 = 13 // Error response
)

// GetHeaders requests up to Limit message headers starting from Date
// and going backwards in time. Date is YYYY-MM-DD format.
type GetHeaders struct {
	Limit int32
	Date  [10]byte // "YYYY-MM-DD"
}

// GetBody requests the full body text for a message by ID.
// MessageId is truncated to 96 bytes if longer.
type GetBody struct {
	MessageId [96]byte
	IdLen     int32
}

// BodyConfirm tells maildb that the mail shepherd is done reading
// the shared body pages and they can be released.
type BodyConfirm struct {
	MessageId [96]byte
	IdLen     int32
}

// HeaderEntry is one header result sent from maildb to mail.
// Sent once per matching message, followed by HeadersEnd.
type HeaderEntry struct {
	Timestamp int64    // Unix seconds
	BodyLen   int32    // body length in bytes
	FromLen   uint8    // length of From field
	SubjLen   uint8    // length of Subject field
	IdLen     uint8    // length of MessageId field
	_         uint8    // pad
	Data      [88]byte // From + Subject + MessageId packed sequentially
}

// HeadersEnd marks the end of a GetHeaders result batch.
type HeadersEnd struct {
	Count int32 // total number of HeaderEntry messages sent
}

// BodyResult tells the mail shepherd that body text is available
// in shared pages.
type BodyResult struct {
	BodyLen   int32    // total body length in bytes
	NumPages  int32    // number of shared pages
	TargetVA  uint64   // VA in the mail shepherd's address space
	MessageId [80]byte // message ID
	IdLen     int32    // length of MessageId
}

// ErrorResult carries an error string from maildb.
type ErrorResult struct {
	MsgLen int32
	Msg    [100]byte
}

// --- Encode functions ---

func EncodeGetHeaders(h *GetHeaders) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoMailReq
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeGetHeaders
	*(*GetHeaders)(unsafe.Pointer(&msg.Payload[4])) = *h
	return msg
}

func EncodeGetBody(b *GetBody) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoMailReq
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeGetBody
	*(*GetBody)(unsafe.Pointer(&msg.Payload[4])) = *b
	return msg
}

func EncodeBodyConfirm(c *BodyConfirm) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoMailReq
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeBodyConfirm
	*(*BodyConfirm)(unsafe.Pointer(&msg.Payload[4])) = *c
	return msg
}

func EncodeHeaderEntry(h *HeaderEntry) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoMailResp
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeHeadersResult
	*(*HeaderEntry)(unsafe.Pointer(&msg.Payload[4])) = *h
	return msg
}

func EncodeHeadersEnd(h *HeadersEnd) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoMailResp
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeHeadersEnd
	*(*HeadersEnd)(unsafe.Pointer(&msg.Payload[4])) = *h
	return msg
}

func EncodeBodyResult(b *BodyResult) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoMailResp
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeBodyResult
	*(*BodyResult)(unsafe.Pointer(&msg.Payload[4])) = *b
	return msg
}

func EncodeErrorResult(e *ErrorResult) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoMailResp
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeErrorResult
	*(*ErrorResult)(unsafe.Pointer(&msg.Payload[4])) = *e
	return msg
}

// --- Decode functions ---

// DecodeMailReq decodes a ProtoMailReq message.
func DecodeMailReq(msg *ipc.UringIPCMsg) any {
	msgType := *(*uint32)(unsafe.Pointer(&msg.Payload[0]))
	switch msgType {
	case MsgTypeGetHeaders:
		return *(*GetHeaders)(unsafe.Pointer(&msg.Payload[4]))
	case MsgTypeGetBody:
		return *(*GetBody)(unsafe.Pointer(&msg.Payload[4]))
	case MsgTypeBodyConfirm:
		return *(*BodyConfirm)(unsafe.Pointer(&msg.Payload[4]))
	default:
		return nil
	}
}

// DecodeMailResp decodes a ProtoMailResp message.
func DecodeMailResp(msg *ipc.UringIPCMsg) any {
	msgType := *(*uint32)(unsafe.Pointer(&msg.Payload[0]))
	switch msgType {
	case MsgTypeHeadersResult:
		return *(*HeaderEntry)(unsafe.Pointer(&msg.Payload[4]))
	case MsgTypeHeadersEnd:
		return *(*HeadersEnd)(unsafe.Pointer(&msg.Payload[4]))
	case MsgTypeBodyResult:
		return *(*BodyResult)(unsafe.Pointer(&msg.Payload[4]))
	case MsgTypeErrorResult:
		return *(*ErrorResult)(unsafe.Pointer(&msg.Payload[4]))
	default:
		return nil
	}
}

// --- Helper functions for packing/unpacking string fields ---

// PackGetBody creates a GetBody from a message ID string.
func PackGetBody(messageId string) GetBody {
	var b GetBody
	n := copy(b.MessageId[:], messageId)
	b.IdLen = int32(n)
	return b
}

// PackBodyConfirm creates a BodyConfirm from a message ID string.
func PackBodyConfirm(messageId string) BodyConfirm {
	var c BodyConfirm
	n := copy(c.MessageId[:], messageId)
	c.IdLen = int32(n)
	return c
}

// UnpackGetBody extracts the message ID string from a GetBody.
func UnpackGetBody(b *GetBody) string {
	return string(b.MessageId[:b.IdLen])
}

// UnpackBodyConfirm extracts the message ID string from a BodyConfirm.
func UnpackBodyConfirm(c *BodyConfirm) string {
	return string(c.MessageId[:c.IdLen])
}

// PackHeaderEntry fills a HeaderEntry from individual fields.
// Returns false if the packed data exceeds the 88-byte Data field.
func PackHeaderEntry(timestamp int64, bodyLen int32, from, subject, messageId string) (HeaderEntry, bool) {
	var h HeaderEntry
	h.Timestamp = timestamp
	h.BodyLen = bodyLen

	total := len(from) + len(subject) + len(messageId)
	if total > len(h.Data) {
		// Truncate subject to fit.
		avail := len(h.Data) - len(from) - len(messageId)
		if avail < 0 {
			avail = 0
		}
		if len(subject) > avail {
			subject = subject[:avail]
		}
	}

	off := 0
	n := copy(h.Data[off:], from)
	h.FromLen = uint8(n)
	off += n
	n = copy(h.Data[off:], subject)
	h.SubjLen = uint8(n)
	off += n
	n = copy(h.Data[off:], messageId)
	h.IdLen = uint8(n)
	return h, true
}

// UnpackHeaderEntry extracts From, Subject, and MessageId from a HeaderEntry.
func UnpackHeaderEntry(h *HeaderEntry) (from, subject, messageId string) {
	off := 0
	from = string(h.Data[off : off+int(h.FromLen)])
	off += int(h.FromLen)
	subject = string(h.Data[off : off+int(h.SubjLen)])
	off += int(h.SubjLen)
	messageId = string(h.Data[off : off+int(h.IdLen)])
	return
}
