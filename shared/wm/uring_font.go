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
	MsgTypeOpenFont               uint32 = 100
	MsgTypeOpenFontReply          uint32 = 101
	MsgTypeRequestGlyph           uint32 = 102
	MsgTypeGlyphReply             uint32 = 103
	MsgTypeOpenTemporaryFont      uint32 = 104
	MsgTypeOpenTemporaryFontReply uint32 = 105
	MsgTypeCloseTemporaryFont     uint32 = 106
	MsgTypeCloseTemporaryFontReply uint32 = 107
)

// TempFontIDBase is OR'd into temporary fontIDs returned across the IPC
// boundary so dumps and traces immediately distinguish the temp pool
// from the permanent pool. fontsvc and the IPC providers mask it off
// (fontID & TempFontIDMask) before indexing the local slot table.
const (
	TempFontIDBase = int32(0x1000)
	TempFontIDMask = int32(0x0FFF)
)

// IsTempFontID reports whether a fontID is in the temporary pool range.
func IsTempFontID(fontID int32) bool {
	return fontID >= TempFontIDBase && fontID < (TempFontIDBase|0x0FFF)+1
}

// --- Typed message structs (font protocol) ---

// OpenFont is sent by a shepherd to fontsvc to open a font.
// Path is the font family name (null-terminated, max 100 bytes).
// Variant: 0=Regular, 1=Bold, 2=Italic, 3=BoldItalic, 4=Light, 5=Condensed.
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

// OpenTemporaryFont opens a font for one render scope. Caller pairs
// with CloseTemporaryFont. Layout (108 bytes — fits in Payload[4:112]):
//
//	[ 0:80] Path        — family name, null-terminated
//	[80:84] Variant     — 0=Reg, 1=Bold, 2=Italic, ...
//	[84:88] Size        — point size
//	[88:96] FontDataVA  — fontsvc-side VA of shared font bytes (0 = none)
//	[96:104] FontDataLen — size of the font-data buffer in bytes
//	[104:108] NumPages  — number of pages backing FontDataVA (for unmap)
//
// FontDataVA == 0 means the font should be resolved from the registered
// map (CSS @font-face previously RegisterBuffer'd) or the filesystem;
// fontsvc returns -ENOENT if neither hits. When FontDataVA != 0 the
// caller has SharePagesWithTarget'd the bytes into fontsvc's address
// space; fontsvc parses Face directly from those pages and unmaps them
// on CloseTemporaryFont.
type OpenTemporaryFont struct {
	Path        [80]byte
	Variant     int32
	Size        int32
	FontDataVA  uint64
	FontDataLen uint64
	NumPages    uint32
}

// OpenTemporaryFontReply is fontsvc's response. FontID has TempFontIDBase
// (0x1000) OR'd in when the request hit the temp pool; FontID is in the
// regular [0, MaxFonts) range when the permanent-first check matched
// (caller's CloseTemporaryFont is then a no-op).
type OpenTemporaryFontReply struct {
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

// CloseTemporaryFont releases a temporary font. fontID must be in the
// temp range (TempFontIDBase..TempFontIDBase+MaxTempFonts-1); fontsvc
// validates this and returns -EINVAL otherwise. After successful close
// the caller is free to FreePages on the font-data buffer it shared.
type CloseTemporaryFont struct {
	FontID int32
}

// CloseTemporaryFontReply confirms a CloseTemporaryFont. ErrCode is 0
// on success, negative errno on failure (-EINVAL for out-of-range
// fontID, -ESRCH for unknown / already-closed).
type CloseTemporaryFontReply struct {
	FontID  int32
	ErrCode int32
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

func EncodeOpenTemporaryFont(otf *OpenTemporaryFont) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoFontRequest
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeOpenTemporaryFont
	*(*OpenTemporaryFont)(unsafe.Pointer(&msg.Payload[4])) = *otf
	return msg
}

func EncodeOpenTemporaryFontReply(otfr *OpenTemporaryFontReply) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoFontResponse
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeOpenTemporaryFontReply
	*(*OpenTemporaryFontReply)(unsafe.Pointer(&msg.Payload[4])) = *otfr
	return msg
}

func EncodeCloseTemporaryFont(ctf *CloseTemporaryFont) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoFontRequest
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeCloseTemporaryFont
	*(*CloseTemporaryFont)(unsafe.Pointer(&msg.Payload[4])) = *ctf
	return msg
}

func EncodeCloseTemporaryFontReply(ctfr *CloseTemporaryFontReply) ipc.UringIPCMsg {
	var msg ipc.UringIPCMsg
	msg.Protocol = ipc.ProtoFontResponse
	*(*uint32)(unsafe.Pointer(&msg.Payload[0])) = MsgTypeCloseTemporaryFontReply
	*(*CloseTemporaryFontReply)(unsafe.Pointer(&msg.Payload[4])) = *ctfr
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
	case MsgTypeOpenTemporaryFont:
		return FontRequestMsg{
			SenderSID: senderSID,
			Msg:       *(*OpenTemporaryFont)(unsafe.Pointer(&msg.Payload[4])),
		}
	case MsgTypeCloseTemporaryFont:
		return FontRequestMsg{
			SenderSID: senderSID,
			Msg:       *(*CloseTemporaryFont)(unsafe.Pointer(&msg.Payload[4])),
		}
	default:
		panic("wm.DecodeFontRequest: unknown message type")
	}
}

// DecodeFontResponse decodes a ProtoFontResponse message (fontsvc→shepherd).
// Panics on unknown message type.
func DecodeFontResponse(msg *ipc.UringIPCMsg) any {
	return DecodeFontResponseFromPayload(msg.Payload[:])
}

// DecodeFontResponseFromPayload decodes a font response from raw payload bytes.
// Used by .maz modules that receive raw payloads from the shepherd's uring
// dispatcher and need to decode in their own type namespace.
// Returns nil on unknown message type.
func DecodeFontResponseFromPayload(payload []byte) any {
	if len(payload) < 4 {
		return nil
	}
	msgType := *(*uint32)(unsafe.Pointer(&payload[0]))
	switch msgType {
	case MsgTypeOpenFontReply:
		return *(*OpenFontReply)(unsafe.Pointer(&payload[4]))
	case MsgTypeGlyphReply:
		return *(*GlyphReply)(unsafe.Pointer(&payload[4]))
	case MsgTypeOpenTemporaryFontReply:
		return *(*OpenTemporaryFontReply)(unsafe.Pointer(&payload[4]))
	case MsgTypeCloseTemporaryFontReply:
		return *(*CloseTemporaryFontReply)(unsafe.Pointer(&payload[4]))
	default:
		return nil
	}
}
