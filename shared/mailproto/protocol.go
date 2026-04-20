// Package mailproto defines the v2 uring IPC protocol between the mail
// shepherd and the maildb shepherd.
//
// The protocol is built around HOT collections: named, ordered result sets
// that maildb pushes unsolicited CollectionAdd / CollectionRemove
// notifications into as the live message set changes. Every message reference
// is (collectionId, msgNumber). Data-returning requests (KeyHeaders,
// AllHeaders, Body) transfer page ownership to the caller via TransferPages.
//
// Protocol IDs: ProtoMailReq=13 (mail→maildb), ProtoMailResp=14 (maildb→mail).
// All messages: [0:4] MsgType uint32, [4:112] message body (≤108 bytes).
// Every request and response struct starts with RequestId [16]byte (raw UUID).
// Unsolicited notification structs carry RequestId as the last field.
package mailproto

import (
	"mazzy/shared/ipc"
	"unsafe"
)

// --- Error codes ---

const (
	ErrNone              uint32 = 0
	ErrCollectionExpired uint32 = 1 // collId not in live set; client must CreateCollection again
	ErrInvalidMsgNumber  uint32 = 2 // msgNum out of range for collection
	ErrMessageNotFound   uint32 = 3 // messageId not in badger
	ErrBadgerError       uint32 = 4 // internal DB error
	ErrFilterInvalid     uint32 = 5 // unknown filter type or malformed FilterArg
)

// --- Filter types ---

const (
	FilterAll     uint32 = 0 // all messages; FilterArg unused; count from count:all
	FilterUnread  uint32 = 1 // unread messages only; FilterArg unused; count from count:unread
	FilterFrom    uint32 = 2 // sender contains substring; FilterArg = query bytes; via fti
	FilterSubject uint32 = 3 // subject contains substring; FilterArg = query bytes; via fti
)

// --- Sort order ---

const (
	SortDesc uint32 = 0 // newest-first (default inbox); badger Reverse:true iterator
	SortAsc  uint32 = 1 // oldest-first; forward badger iterator
)

// --- Request MsgType constants (mail → maildb) ---

const (
	MsgTypeMessageCount     uint32 = 10
	MsgTypeKeyHeaders       uint32 = 11
	MsgTypeAllHeaders       uint32 = 12
	MsgTypeLatestUnread     uint32 = 13
	MsgTypeBody             uint32 = 14
	MsgTypeMarkRead         uint32 = 15
	MsgTypeMarkDeleted      uint32 = 16
	MsgTypeCreateCollection uint32 = 17
)

// --- Response MsgType constants (maildb → mail) ---

const (
	MsgTypeRespMessageCount     uint32 = 50
	MsgTypeRespKeyHeaders       uint32 = 51
	MsgTypeRespAllHeaders       uint32 = 52
	MsgTypeRespLatestUnread     uint32 = 53
	MsgTypeRespBody             uint32 = 54
	MsgTypeRespMarkRead         uint32 = 55
	MsgTypeRespMarkDeleted      uint32 = 56
	MsgTypeRespCreateCollection uint32 = 57
)

// --- Unsolicited notification MsgType constants (maildb → mail) ---

const (
	MsgTypeCollectionAdd    uint32 = 60
	MsgTypeCollectionRemove uint32 = 61
)

// --- Request structs (mail → maildb) ---
// All start with RequestId [16]byte, then fields.

// MessageCountReq requests the total number of messages in the DB.
// Layout: RequestId[16] = 16 bytes. Total with MsgType: 20 bytes.
type MessageCountReq struct {
	RequestId [16]byte
}

// KeyHeadersReq requests key headers (Sender/Subject/Date) for a range
// of message numbers within a collection.
// Layout: RequestId[16] + CollId(4) + From(4) + To(4) = 28 bytes. Total: 32.
type KeyHeadersReq struct {
	RequestId [16]byte
	CollId    uint32
	From      uint32
	To        uint32
}

// AllHeadersReq requests full RFC headers for one message.
// Layout: RequestId[16] + CollId(4) + MsgNum(4) = 24 bytes. Total: 28.
type AllHeadersReq struct {
	RequestId [16]byte
	CollId    uint32
	MsgNum    uint32
}

// LatestUnreadReq requests the most recent unread message.
// Layout: RequestId[16] = 16 bytes. Total: 20.
type LatestUnreadReq struct {
	RequestId [16]byte
}

// BodyReq requests the raw body for one message.
// Layout: RequestId[16] + CollId(4) + MsgNum(4) = 24 bytes. Total: 28.
type BodyReq struct {
	RequestId [16]byte
	CollId    uint32
	MsgNum    uint32
}

// MarkReadReq marks one message as read.
// Layout: RequestId[16] + CollId(4) + MsgNum(4) = 24 bytes. Total: 28.
type MarkReadReq struct {
	RequestId [16]byte
	CollId    uint32
	MsgNum    uint32
}

// MarkDeletedReq marks one message as deleted and fans out CollectionRemove.
// Layout: RequestId[16] + CollId(4) + MsgNum(4) = 24 bytes. Total: 28.
type MarkDeletedReq struct {
	RequestId [16]byte
	CollId    uint32
	MsgNum    uint32
}

// CreateCollectionReq creates a new named, ordered result set.
// FilterAll+SortDesc is the standard inbox view.
// Layout: RequestId[16] + FilterType(4) + SortOrder(4) + FilterArg[64] = 88 bytes. Total: 92.
type CreateCollectionReq struct {
	RequestId  [16]byte
	FilterType uint32
	SortOrder  uint32
	FilterArg  [64]byte
}

// --- Response structs (maildb → mail) ---
// All echo the RequestId of the originating request.

// RespMessageCount is the reply to MessageCountReq.
// Layout: RequestId[16] + Count(8) + ErrCode(4) = 28 bytes. Total: 32.
type RespMessageCount struct {
	RequestId [16]byte
	Count     uint64
	ErrCode   uint32
	_pad      uint32
}

// RespKeyHeaders is the reply to KeyHeadersReq.
// TargetVA is the VA in the mail shepherd's address space (TransferPages result).
// Layout: RequestId[16] + TargetVA(8) + NumBytes(4) + Count(4) + ErrCode(4) = 36 bytes. Total: 40.
type RespKeyHeaders struct {
	RequestId [16]byte
	TargetVA  uint64
	NumBytes  uint32
	Count     uint32
	ErrCode   uint32
	_pad      uint32
}

// RespAllHeaders is the reply to AllHeadersReq.
// Layout: RequestId[16] + TargetVA(8) + NumBytes(4) + ErrCode(4) = 32 bytes. Total: 36.
type RespAllHeaders struct {
	RequestId [16]byte
	TargetVA  uint64
	NumBytes  uint32
	ErrCode   uint32
}

// RespLatestUnread is the reply to LatestUnreadReq.
// On success, TargetVA points to one AllHeaderEntry page.
// Layout: RequestId[16] + CollId(4) + MsgNum(4) + TargetVA(8) + NumBytes(4) + ErrCode(4) = 40 bytes. Total: 44.
type RespLatestUnread struct {
	RequestId [16]byte
	CollId    uint32
	MsgNum    uint32
	TargetVA  uint64
	NumBytes  uint32
	ErrCode   uint32
}

// RespBody is the reply to BodyReq.
// Layout: RequestId[16] + TargetVA(8) + NumBytes(4) + ErrCode(4) = 32 bytes. Total: 36.
type RespBody struct {
	RequestId [16]byte
	TargetVA  uint64
	NumBytes  uint32
	ErrCode   uint32
}

// RespMarkRead is the reply to MarkReadReq.
// Layout: RequestId[16] + ErrCode(4) = 20 bytes. Total: 24.
type RespMarkRead struct {
	RequestId [16]byte
	ErrCode   uint32
	_pad      uint32
}

// RespMarkDeleted is the reply to MarkDeletedReq.
// Layout: RequestId[16] + ErrCode(4) + NewSize(4) = 24 bytes. Total: 28.
type RespMarkDeleted struct {
	RequestId [16]byte
	ErrCode   uint32
	NewSize   uint32
}

// RespCreateCollection is the reply to CreateCollectionReq.
// Layout: RequestId[16] + CollId(4) + Size(4) + ErrCode(4) = 28 bytes. Total: 32.
type RespCreateCollection struct {
	RequestId [16]byte
	CollId    uint32
	Size      uint32
	ErrCode   uint32
	_pad      uint32
}

// --- Unsolicited notification structs (maildb → mail) ---
// RequestId is the last field; set to the originating client's UUID when the
// change came from a client operation, or zero when from an external source
// (e.g. new mail delivery). Clients MUST NOT assume RequestId matches one of
// their own outstanding requests.

// CollectionAdd notifies that a new message arrived in a collection.
// Client should issue KeyHeaders(CollId, MsgNum, MsgNum) to fetch header data.
// Layout: CollId(4) + MsgNum(4) + NewSize(4) + RequestId[16] = 28 bytes. Total: 32.
type CollectionAdd struct {
	CollId    uint32
	MsgNum    uint32
	NewSize   uint32
	_pad      uint32
	RequestId [16]byte
}

// CollectionRemove notifies that a message was removed from a collection.
// MsgId is included so the client can locate the row even with a stale msgNum.
// Layout: CollId(4) + MsgNum(4) + NewSize(4) + _pad(4) + MsgId[64] + RequestId[16] = 96 bytes. Total: 100.
type CollectionRemove struct {
	CollId    uint32
	MsgNum    uint32
	NewSize   uint32
	_pad      uint32
	MsgId     [64]byte
	RequestId [16]byte
}

// --- Page layout structs ---

// KeyHeaderEntry is the fixed-size page record for a key header result.
// Pages from RespKeyHeaders contain a packed array of KeyHeaderEntry.
// sizeof = 240 bytes; 50 entries = 12,000 bytes ≈ 3 pages.
//
// Flags bit 0 = IsRead, bit 1 = IsDeleted.
type KeyHeaderEntry struct {
	Sender  [64]byte  // null-terminated UTF-8
	Subject [128]byte // null-terminated UTF-8
	Date    [32]byte  // RFC3339, null-terminated
	MsgNum  uint32
	Flags   uint32
	_pad    [8]byte
}

// KeyHeaderEntrySize is the byte size of a KeyHeaderEntry.
const KeyHeaderEntrySize = 240

// AllHeaderEntry is the fixed-size page record for a full-header result.
// sizeof = 1,232 bytes; fits in a single 4096-byte page.
//
// Flags bit 0 = IsRead, bit 1 = IsDeleted.
type AllHeaderEntry struct {
	From        [128]byte
	To          [256]byte
	CC          [256]byte
	Subject     [256]byte
	Date        [64]byte
	MessageId   [128]byte
	ContentType [128]byte
	MsgNum      uint32
	Flags       uint32
	_pad        [8]byte
}

// AllHeaderEntrySize is the byte size of an AllHeaderEntry.
const AllHeaderEntrySize = 1232

// --- Encode functions (requests) ---

func EncodeMessageCount(r *MessageCountReq) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoMailReq
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeMessageCount
	*(*MessageCountReq)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

func EncodeKeyHeaders(r *KeyHeadersReq) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoMailReq
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeKeyHeaders
	*(*KeyHeadersReq)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

func EncodeAllHeaders(r *AllHeadersReq) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoMailReq
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeAllHeaders
	*(*AllHeadersReq)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

func EncodeLatestUnread(r *LatestUnreadReq) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoMailReq
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeLatestUnread
	*(*LatestUnreadReq)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

func EncodeBody(r *BodyReq) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoMailReq
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeBody
	*(*BodyReq)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

func EncodeMarkRead(r *MarkReadReq) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoMailReq
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeMarkRead
	*(*MarkReadReq)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

func EncodeMarkDeleted(r *MarkDeletedReq) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoMailReq
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeMarkDeleted
	*(*MarkDeletedReq)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

func EncodeCreateCollection(r *CreateCollectionReq) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoMailReq
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeCreateCollection
	*(*CreateCollectionReq)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

// --- Encode functions (responses) ---

func EncodeRespMessageCount(r *RespMessageCount) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoMailResp
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeRespMessageCount
	*(*RespMessageCount)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

func EncodeRespKeyHeaders(r *RespKeyHeaders) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoMailResp
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeRespKeyHeaders
	*(*RespKeyHeaders)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

func EncodeRespAllHeaders(r *RespAllHeaders) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoMailResp
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeRespAllHeaders
	*(*RespAllHeaders)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

func EncodeRespLatestUnread(r *RespLatestUnread) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoMailResp
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeRespLatestUnread
	*(*RespLatestUnread)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

func EncodeRespBody(r *RespBody) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoMailResp
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeRespBody
	*(*RespBody)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

func EncodeRespMarkRead(r *RespMarkRead) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoMailResp
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeRespMarkRead
	*(*RespMarkRead)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

func EncodeRespMarkDeleted(r *RespMarkDeleted) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoMailResp
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeRespMarkDeleted
	*(*RespMarkDeleted)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

func EncodeRespCreateCollection(r *RespCreateCollection) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoMailResp
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeRespCreateCollection
	*(*RespCreateCollection)(unsafe.Pointer(&msg.Payload[4])) = *r
	return msg
}

// --- Encode functions (notifications) ---

func EncodeCollectionAdd(n *CollectionAdd) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoMailResp
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeCollectionAdd
	*(*CollectionAdd)(unsafe.Pointer(&msg.Payload[4])) = *n
	return msg
}

func EncodeCollectionRemove(n *CollectionRemove) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoMailResp
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeCollectionRemove
	*(*CollectionRemove)(unsafe.Pointer(&msg.Payload[4])) = *n
	return msg
}

// --- Decode functions ---

// DecodeMailReq decodes a ProtoMailReq message into the appropriate struct.
// Returns nil for unknown MsgType values.
func DecodeMailReq(msg *ipc.UringIPCMsg) any {
	msgType := *(*uint32)(unsafe.Pointer(&msg.Payload[0]))
	switch msgType {
	case MsgTypeMessageCount:
		return *(*MessageCountReq)(unsafe.Pointer(&msg.Payload[4]))
	case MsgTypeKeyHeaders:
		return *(*KeyHeadersReq)(unsafe.Pointer(&msg.Payload[4]))
	case MsgTypeAllHeaders:
		return *(*AllHeadersReq)(unsafe.Pointer(&msg.Payload[4]))
	case MsgTypeLatestUnread:
		return *(*LatestUnreadReq)(unsafe.Pointer(&msg.Payload[4]))
	case MsgTypeBody:
		return *(*BodyReq)(unsafe.Pointer(&msg.Payload[4]))
	case MsgTypeMarkRead:
		return *(*MarkReadReq)(unsafe.Pointer(&msg.Payload[4]))
	case MsgTypeMarkDeleted:
		return *(*MarkDeletedReq)(unsafe.Pointer(&msg.Payload[4]))
	case MsgTypeCreateCollection:
		return *(*CreateCollectionReq)(unsafe.Pointer(&msg.Payload[4]))
	default:
		return nil
	}
}

// DecodeMailResp decodes a ProtoMailResp message (response or notification).
// Returns nil for unknown MsgType values.
func DecodeMailResp(msg *ipc.UringIPCMsg) any {
	msgType := *(*uint32)(unsafe.Pointer(&msg.Payload[0]))
	switch msgType {
	case MsgTypeRespMessageCount:
		return *(*RespMessageCount)(unsafe.Pointer(&msg.Payload[4]))
	case MsgTypeRespKeyHeaders:
		return *(*RespKeyHeaders)(unsafe.Pointer(&msg.Payload[4]))
	case MsgTypeRespAllHeaders:
		return *(*RespAllHeaders)(unsafe.Pointer(&msg.Payload[4]))
	case MsgTypeRespLatestUnread:
		return *(*RespLatestUnread)(unsafe.Pointer(&msg.Payload[4]))
	case MsgTypeRespBody:
		return *(*RespBody)(unsafe.Pointer(&msg.Payload[4]))
	case MsgTypeRespMarkRead:
		return *(*RespMarkRead)(unsafe.Pointer(&msg.Payload[4]))
	case MsgTypeRespMarkDeleted:
		return *(*RespMarkDeleted)(unsafe.Pointer(&msg.Payload[4]))
	case MsgTypeRespCreateCollection:
		return *(*RespCreateCollection)(unsafe.Pointer(&msg.Payload[4]))
	case MsgTypeCollectionAdd:
		return *(*CollectionAdd)(unsafe.Pointer(&msg.Payload[4]))
	case MsgTypeCollectionRemove:
		return *(*CollectionRemove)(unsafe.Pointer(&msg.Payload[4]))
	default:
		return nil
	}
}

// --- Helper functions ---

// PackCreateCollection builds a CreateCollectionReq.
// filterArg is the search query for FilterFrom/FilterSubject; ignored for FilterAll/FilterUnread.
func PackCreateCollection(reqId [16]byte, filterType, sortOrder uint32, filterArg string) CreateCollectionReq {
	var r CreateCollectionReq
	r.RequestId = reqId
	r.FilterType = filterType
	r.SortOrder = sortOrder
	copy(r.FilterArg[:], filterArg)
	return r
}

// UnpackFilterArg extracts the filter query string from a CreateCollectionReq.
func UnpackFilterArg(r *CreateCollectionReq) string {
	n := 0
	for n < len(r.FilterArg) && r.FilterArg[n] != 0 {
		n++
	}
	return string(r.FilterArg[:n])
}

// UnpackMsgId extracts the message ID string from a CollectionRemove notification.
func UnpackMsgId(n *CollectionRemove) string {
	end := 0
	for end < len(n.MsgId) && n.MsgId[end] != 0 {
		end++
	}
	return string(n.MsgId[:end])
}

// PackKeyHeaderEntry fills a KeyHeaderEntry from string fields.
// Fields are truncated to fit their fixed-size arrays.
func PackKeyHeaderEntry(msgNum uint32, flags uint32, sender, subject, date string) KeyHeaderEntry {
	var e KeyHeaderEntry
	e.MsgNum = msgNum
	e.Flags = flags
	copy(e.Sender[:], sender)
	copy(e.Subject[:], subject)
	copy(e.Date[:], date)
	return e
}

// UnpackKeyHeaderEntry extracts null-terminated strings from a KeyHeaderEntry.
func UnpackKeyHeaderEntry(e *KeyHeaderEntry) (sender, subject, date string) {
	sender = nullTermString(e.Sender[:])
	subject = nullTermString(e.Subject[:])
	date = nullTermString(e.Date[:])
	return
}

// PackAllHeaderEntry fills an AllHeaderEntry from string fields.
func PackAllHeaderEntry(msgNum uint32, flags uint32, from, to, cc, subject, date, messageId, contentType string) AllHeaderEntry {
	var e AllHeaderEntry
	e.MsgNum = msgNum
	e.Flags = flags
	copy(e.From[:], from)
	copy(e.To[:], to)
	copy(e.CC[:], cc)
	copy(e.Subject[:], subject)
	copy(e.Date[:], date)
	copy(e.MessageId[:], messageId)
	copy(e.ContentType[:], contentType)
	return e
}

// UnpackAllHeaderEntry extracts null-terminated strings from an AllHeaderEntry.
func UnpackAllHeaderEntry(e *AllHeaderEntry) (from, to, cc, subject, date, messageId, contentType string) {
	from = nullTermString(e.From[:])
	to = nullTermString(e.To[:])
	cc = nullTermString(e.CC[:])
	subject = nullTermString(e.Subject[:])
	date = nullTermString(e.Date[:])
	messageId = nullTermString(e.MessageId[:])
	contentType = nullTermString(e.ContentType[:])
	return
}

func nullTermString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
