// Typed font message structs for uring-based IPC between shepherds and fontsvc.
//
// Payload layout: [0:4] MsgType uint32, [4:112] message body (108 bytes).
package wm

import (
	"mazzy/shared/ipc"
	"unsafe"
)

// Font MsgType values stored at Payload[0:4].
const (
	MsgTypeOpenFont      uint32 = 100
	MsgTypeOpenFontReply uint32 = 101
	MsgTypeRequestGlyph  uint32 = 102
	MsgTypeGlyphReply    uint32 = 103
)

// --- Typed message structs (font protocol) ---

// OpenFont is sent by a shepherd to fontsvc to open a font.
// Path is the font family name (null-terminated, max 100 bytes).
type OpenFont struct {
	Variant int32
	Size    int32
	Path    [100]byte
}

// OpenFontReply is sent by fontsvc to a shepherd after opening a font.
type OpenFontReply struct {
	FontID        int32
	NumCachePages int32
	CacheAddr     uint64
	CacheSize     uint64
	Height        int32
	Ascent        int32
	Descent       int32
	NumFontPages  int32
	FontAddr      uint64
	FontSize      uint64
}

// RequestGlyph is sent by a shepherd to fontsvc for a tier-2 glyph.
type RequestGlyph struct {
	FontID    int32
	GID       int32
	Codepoint int32
}

// GlyphReply is sent by fontsvc to a shepherd after rendering a tier-2 glyph.
type GlyphReply struct {
	FontID      int32
	GID         int32
	ScratchAddr uint64
	GlyphSize   uint32
}

// --- Encode functions ---

func EncodeOpenFont(of *OpenFont) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoFontRequest
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeOpenFont
	*(*OpenFont)(unsafe.Pointer(&msg.Payload[4])) = *of
	return msg
}

func EncodeOpenFontReply(ofr *OpenFontReply) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoFontResponse
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeOpenFontReply
	*(*OpenFontReply)(unsafe.Pointer(&msg.Payload[4])) = *ofr
	return msg
}

func EncodeRequestGlyph(rg *RequestGlyph) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoFontRequest
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeRequestGlyph
	*(*RequestGlyph)(unsafe.Pointer(&msg.Payload[4])) = *rg
	return msg
}

func EncodeGlyphReply(gr *GlyphReply) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoFontResponse
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeGlyphReply
	*(*GlyphReply)(unsafe.Pointer(&msg.Payload[4])) = *gr
	return msg
}

// --- Decode functions ---

// FontRequestMsg wraps a decoded font request with the sender's SID,
// extracted from the uring message header. Fontsvc needs SenderSID
// to send replies and map pages to the requesting shepherd.
type FontRequestMsg struct {
	SenderSID int16
	Msg       any // OpenFont or RequestGlyph
}

// DecodeFontRequest decodes a ProtoFontRequest message (shepherd→fontsvc).
// Returns a FontRequestMsg wrapping the typed struct with the sender's SID.
// Panics on unknown message type.
func DecodeFontRequest(msg *ipc.UringIPCMsg) any {
	senderSID := msg.SenderSID
	msgType := *(*uint32)(unsafe.Pointer(&msg.Payload[0]))
	switch msgType {
	case MsgTypeOpenFont:
		return FontRequestMsg{
			SenderSID: senderSID,
			Msg:       *(*OpenFont)(unsafe.Pointer(&msg.Payload[4])),
		}
	case MsgTypeRequestGlyph:
		return FontRequestMsg{
			SenderSID: senderSID,
			Msg:       *(*RequestGlyph)(unsafe.Pointer(&msg.Payload[4])),
		}
	default:
		panic("wm.DecodeFontRequest: unknown message type")
	}
}

// DecodeFontResponse decodes a ProtoFontResponse message (fontsvc→shepherd).
// Panics on unknown message type.
func DecodeFontResponse(msg *ipc.UringIPCMsg) any {
	msgType := *(*uint32)(unsafe.Pointer(&msg.Payload[0]))
	switch msgType {
	case MsgTypeOpenFontReply:
		return *(*OpenFontReply)(unsafe.Pointer(&msg.Payload[4]))
	case MsgTypeGlyphReply:
		return *(*GlyphReply)(unsafe.Pointer(&msg.Payload[4]))
	default:
		panic("wm.DecodeFontResponse: unknown message type")
	}
}
