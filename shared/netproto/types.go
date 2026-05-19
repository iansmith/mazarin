package netproto

import "unsafe"

// Addr is a Phase-3 IPv4-only socket address. IPv6 is deferred; when it
// lands, a new variant struct will be introduced rather than widening this
// one so existing UDP wire format stays untouched.
//
// Layout (8 bytes): IP4[0:4], Port[4:6], _pad[6:8].
type Addr struct {
	IP4  [4]byte
	Port uint16
	_pad [2]byte
}

// --- Request structs (client → net.elf) ---

// BindUDPReq creates a UDP endpoint at (LocalIP, LocalPort). LocalPort=0
// requests an ephemeral port; LocalIP={0,0,0,0} binds to all interfaces.
// Watermark=0 requests the default TX page limit; non-zero values are
// clamped to MaxTxWatermark by net.elf and echoed back in BindUDPResp.
//
// Layout (16 bytes): ReqID(4) + LocalPort(2) + _pad(2) + LocalIP(4) +
// Watermark(1) + _pad2(3).
type BindUDPReq struct {
	ReqID     uint32
	LocalPort uint16
	_pad      [2]byte
	LocalIP   [4]byte
	Watermark uint8
	_pad2     [3]byte
}

// SendDgramReq sends one datagram from an already-transferred TX page.
// The client has already called sys.TransferDMAClump(net.elfSID, ...);
// PageVA is the net.elf-side VA returned by that call. Offset/Length
// delimit the payload within the page (Offset = Headroom for the layered
// header stack; Length = bytes of payload). Dst is the destination
// (ignored for connected sockets — Phase 3 always uses the explicit-Dst
// shape since UDP doesn't connect).
//
// Layout (32 bytes): ReqID(4) + ConnID(4) + Dst(8) + PageVA(8) + Offset(2)
// + Length(2) + _pad(4).
type SendDgramReq struct {
	ReqID  uint32
	ConnID uint32
	Dst    Addr
	PageVA uint64
	Offset uint16
	Length uint16
	_pad   [4]byte
}

// ReleaseReq returns a loaned RX page to net.elf. ConnID identifies which
// endpoint the page belongs to; PageVA is the client-side VA from the
// matching RecvDgram notification.
//
// Layout (16 bytes): ReqID(4) + ConnID(4) + PageVA(8).
type ReleaseReq struct {
	ReqID  uint32
	ConnID uint32
	PageVA uint64
}

// CloseReq tears down a connID. Any pages still loaned to the client at
// close time are reclaimed by net.elf at its discretion (typically lazily
// on the next RX scan).
//
// Layout (8 bytes): ReqID(4) + ConnID(4).
type CloseReq struct {
	ReqID  uint32
	ConnID uint32
}

// --- Response structs (net.elf → client) ---

// BindUDPResp echoes the granted ConnID and bound port. ErrCode is one of
// the NetErr* constants; on error, ConnID is 0 and LocalPort is undefined.
// Watermark is the per-client TX page limit actually granted (after clamp).
//
// Layout (16 bytes): ReqID(4) + ConnID(4) + LocalPort(2) + ErrCode(2) +
// Watermark(1) + _pad(3).
type BindUDPResp struct {
	ReqID     uint32
	ConnID    uint32
	LocalPort uint16
	ErrCode   int16
	Watermark uint8
	_pad      [3]byte
}

// SendDgramResp confirms the kernel handed the page to gvisor for TX.
// ErrCode != NetErrNone means the page was rejected (still owned by
// net.elf, will be returned to its pool — the client should not try to
// re-transfer the same VA).
//
// Layout (12 bytes): ReqID(4) + ConnID(4) + ErrCode(2) + _pad(2).
type SendDgramResp struct {
	ReqID   uint32
	ConnID  uint32
	ErrCode int16
	_pad    [2]byte
}

// CloseResp confirms a Close. Errors here are rare (unknown ConnID) and
// non-fatal — the client already forgot the handle.
//
// Layout (8 bytes): ReqID(4) + ErrCode(2) + _pad(2).
type CloseResp struct {
	ReqID   uint32
	ErrCode int16
	_pad    [2]byte
}

// --- Unsolicited (net.elf → client) ---

// RecvDgram delivers one inbound UDP datagram. PageVA is the client-side
// VA where the payload page is mapped (via sys.SharePagesWithTarget done
// by net.elf before sending this message). Offset/Length delimit the
// payload bytes within the page. Src is the sender's address.
//
// The client MUST send a matching ReleaseReq (with the same ConnID and
// PageVA) when done; otherwise net.elf reclaims the page lazily, but
// holding pages indefinitely counts against the per-client watermark.
//
// Layout (24 bytes): ConnID(4) + _pad(4) + Src(8) + PageVA(8). Note: no
// ReqID — this is unsolicited, not a response.
type RecvDgram struct {
	ConnID uint32
	_pad   [4]byte
	Src    Addr
	PageVA uint64
}

// --- Compile-time size assertions ---
//
// Each message struct occupies Payload[4:108] (108 bytes after the 4-byte
// MsgType discriminator). The asserts below catch a future field addition
// that would overflow the slot.

const maxNetIPCMsgBody = 108

var _ [maxNetIPCMsgBody]byte = [maxNetIPCMsgBody]byte{}
var _ [8]byte = [unsafe.Sizeof(Addr{})]byte{}
var _ [maxNetIPCMsgBody - unsafe.Sizeof(BindUDPReq{})]byte
var _ [maxNetIPCMsgBody - unsafe.Sizeof(SendDgramReq{})]byte
var _ [maxNetIPCMsgBody - unsafe.Sizeof(ReleaseReq{})]byte
var _ [maxNetIPCMsgBody - unsafe.Sizeof(CloseReq{})]byte
var _ [maxNetIPCMsgBody - unsafe.Sizeof(BindUDPResp{})]byte
var _ [maxNetIPCMsgBody - unsafe.Sizeof(SendDgramResp{})]byte
var _ [maxNetIPCMsgBody - unsafe.Sizeof(CloseResp{})]byte
var _ [maxNetIPCMsgBody - unsafe.Sizeof(RecvDgram{})]byte
