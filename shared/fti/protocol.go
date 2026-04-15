// Package fti defines the uring IPC protocol for the full-text index shepherd.
//
// The fti shepherd owns a bleve index and accepts IndexDocument requests
// from other shepherds. Document content is passed via shared memory pages
// with a small header (SharedPageHeader) followed by structured fields and
// raw body bytes. The uring message carries metadata (doc ID, type, page
// info); the actual content lives in the shared pages.
//
// Payload layout: [0:4] MsgType uint32, [4:112] message body (108 bytes max).
package fti

import (
	"mazzy/shared/ipc"
	"unsafe"
)

// IndexType constants identify the kind of document being indexed.
// Each type maps to a different bleve DocumentMapping with appropriate
// field analyzers and boost values.
const (
	IndexTypeMailMessage   uint32 = 1
	IndexTypeCalendarEvent uint32 = 2
	IndexTypeReminder      uint32 = 3
)

// MsgType values for ProtoFTIReq / ProtoFTIResp.
const (
	// Requests (shepherd -> fti)
	MsgTypeIndexDocument uint32 = 1 // Index a document (content in shared pages)

	// Responses (fti -> shepherd)
	MsgTypeIndexingCompleted uint32 = 10 // Indexing finished successfully
	MsgTypeIndexError        uint32 = 11 // Indexing failed
)

// IndexDocument is the request to index a document. The actual content
// (structured fields + body) lives in shared pages at TargetVA.
//
// Fields ordered largest-first to avoid alignment padding.
// Layout: 8+4+4+4+2+2+80 = 104 bytes. Fits in 108-byte payload body.
type IndexDocument struct {
	TargetVA uint64   // VA of shared pages in fti's address space
	DocType  uint32   // IndexTypeMailMessage, etc.
	BodyLen  uint32   // byte length of raw body (after header+fields in shared pages)
	NumPages uint32   // total shared pages
	IdLen    uint16   // length of document ID string
	_pad     uint16
	DocId    [80]byte // document ID (e.g. Message-Id for mail)
}

// IndexingCompleted is sent by fti after a document has been indexed.
// The sender can free the shared pages after receiving this.
type IndexingCompleted struct {
	IdLen uint16
	_pad  [2]byte
	DocId [80]byte
}

// IndexError is sent by fti if indexing fails.
type IndexError struct {
	IdLen  uint16
	MsgLen uint16
	DocId  [48]byte
	Msg    [56]byte
}

// SharedPageHeader is written at byte 0 of the first shared page.
// Variable-length string fields (Subject, From, Sender, Date) follow
// immediately after the fixed header. Body bytes start after the strings.
//
// Total body offset = SharedPageHeaderSize + SubjectLen + FromLen + SenderLen + DateLen.
type SharedPageHeader struct {
	SubjectLen uint16
	FromLen    uint16
	SenderLen  uint16
	DateLen    uint16
	_reserved  [48]byte // room for future document types
}

// SharedPageHeaderSize is the fixed size of SharedPageHeader.
const SharedPageHeaderSize = 56

// --- Encode functions ---

func EncodeIndexDocument(d *IndexDocument) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoFTIReq
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeIndexDocument
	*(*IndexDocument)(unsafe.Pointer(&msg.Payload[4])) = *d
	return msg
}

func EncodeIndexingCompleted(c *IndexingCompleted) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoFTIResp
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeIndexingCompleted
	*(*IndexingCompleted)(unsafe.Pointer(&msg.Payload[4])) = *c
	return msg
}

func EncodeIndexError(e *IndexError) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoFTIResp
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeIndexError
	*(*IndexError)(unsafe.Pointer(&msg.Payload[4])) = *e
	return msg
}

// --- Decode functions ---

// DecodeFTIReq decodes a ProtoFTIReq message.
func DecodeFTIReq(msg *ipc.UringIPCMsg) any {
	msgType := *(*uint32)(unsafe.Pointer(&msg.Payload[0]))
	switch msgType {
	case MsgTypeIndexDocument:
		return *(*IndexDocument)(unsafe.Pointer(&msg.Payload[4]))
	default:
		return nil
	}
}

// DecodeFTIResp decodes a ProtoFTIResp message.
func DecodeFTIResp(msg *ipc.UringIPCMsg) any {
	msgType := *(*uint32)(unsafe.Pointer(&msg.Payload[0]))
	switch msgType {
	case MsgTypeIndexingCompleted:
		return *(*IndexingCompleted)(unsafe.Pointer(&msg.Payload[4]))
	case MsgTypeIndexError:
		return *(*IndexError)(unsafe.Pointer(&msg.Payload[4]))
	default:
		return nil
	}
}

// --- Helper functions ---

// PackIndexDocument creates an IndexDocument from individual fields.
func PackIndexDocument(docType uint32, docId string, bodyLen, numPages uint32, targetVA uint64) IndexDocument {
	var d IndexDocument
	d.DocType = docType
	d.BodyLen = bodyLen
	d.NumPages = numPages
	d.TargetVA = targetVA
	n := copy(d.DocId[:], docId)
	d.IdLen = uint16(n)
	return d
}

// UnpackDocId extracts the document ID string from an IndexDocument.
func UnpackDocId(d *IndexDocument) string {
	return string(d.DocId[:d.IdLen])
}

// UnpackCompletedId extracts the document ID from an IndexingCompleted.
func UnpackCompletedId(c *IndexingCompleted) string {
	return string(c.DocId[:c.IdLen])
}

// PackIndexingCompleted creates an IndexingCompleted from a document ID.
func PackIndexingCompleted(docId string) IndexingCompleted {
	var c IndexingCompleted
	n := copy(c.DocId[:], docId)
	c.IdLen = uint16(n)
	return c
}

// UnpackIndexError extracts the document ID and error message from an IndexError.
func UnpackIndexError(e *IndexError) (docId, errMsg string) {
	docId = string(e.DocId[:e.IdLen])
	errMsg = string(e.Msg[:e.MsgLen])
	return
}

// PackIndexError creates an IndexError from a document ID and error message.
func PackIndexError(docId, errMsg string) IndexError {
	var e IndexError
	n := copy(e.DocId[:], docId)
	e.IdLen = uint16(n)
	n = copy(e.Msg[:], errMsg)
	e.MsgLen = uint16(n)
	return e
}

// WriteSharedPageHeader writes a SharedPageHeader and the variable-length
// string fields into dst. Returns the offset where body bytes should start.
// dst must be at least SharedPageHeaderSize + len(subject) + len(from) +
// len(sender) + len(date) bytes.
func WriteSharedPageHeader(dst []byte, subject, from, sender, date string) int {
	// Clamp field lengths to reasonable maximums.
	if len(subject) > 512 {
		subject = subject[:512]
	}
	if len(from) > 256 {
		from = from[:256]
	}
	if len(sender) > 256 {
		sender = sender[:256]
	}
	if len(date) > 64 {
		date = date[:64]
	}

	h := (*SharedPageHeader)(unsafe.Pointer(&dst[0]))
	h.SubjectLen = uint16(len(subject))
	h.FromLen = uint16(len(from))
	h.SenderLen = uint16(len(sender))
	h.DateLen = uint16(len(date))

	off := SharedPageHeaderSize
	off += copy(dst[off:], subject)
	off += copy(dst[off:], from)
	off += copy(dst[off:], sender)
	off += copy(dst[off:], date)
	return off
}

// ReadSharedPageHeader reads the SharedPageHeader and extracts the
// variable-length string fields from src. Returns subject, from, sender,
// date, and the offset where body bytes begin.
func ReadSharedPageHeader(src []byte) (subject, from, sender, date string, bodyOffset int) {
	h := (*SharedPageHeader)(unsafe.Pointer(&src[0]))
	off := SharedPageHeaderSize

	subjLen := int(h.SubjectLen)
	subject = string(src[off : off+subjLen])
	off += subjLen

	fromLen := int(h.FromLen)
	from = string(src[off : off+fromLen])
	off += fromLen

	senderLen := int(h.SenderLen)
	sender = string(src[off : off+senderLen])
	off += senderLen

	dateLen := int(h.DateLen)
	date = string(src[off : off+dateLen])
	off += dateLen

	bodyOffset = off
	return
}
