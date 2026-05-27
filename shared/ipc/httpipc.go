// httpipc.go — Payload structs for the protocol-http shepherd IPC (MAZ-49).
//
// Two messages:
//
//   - ProtoHttpIPCReq (client → protocol-http): a single HTTP/1.1 Do request.
//     The caller has already allocated:
//       * a request Slab containing a headers-prefix region followed by the
//         body, and called ShareRange(protoHttpSID, 0, ...) to grant
//         protocol-http R/W access (ReqShareID below).
//       * a response Slab pre-posted as a write destination, also shared
//         R/W (RespShareID below).
//     protocol-http writes HTTP/1.1 headers into the prefix region
//     right-aligned so the last header byte is adjacent to body[0], does
//     one TLS write covering [headers ‖ body], reads the response into the
//     pre-posted response Slab, then replies and Releases both shares.
//
//   - ProtoHttpIPCResp (protocol-http → client): the parsed status, the byte
//     bounds of the response body within respShare, and an error code.
//
// Both messages are correlated by ReqID. The protocol assumes a single
// in-flight request per client connection in v1; multi-request concurrency
// (ShareID-keyed lookup of the matching shares) is future work.
//
// Note that the actual response *bytes* live in the caller's pre-posted
// response Slab — they do NOT travel over IPC. The response IPC only
// carries metadata (status, body bounds, error).
package ipc

import "unsafe"

// HttpMethod is a wire-stable enum for HTTP/1.1 methods used by Do requests.
// Values are u16 so the layout aligns naturally.
type HttpMethod uint16

const (
	HttpMethodInvalid HttpMethod = 0
	HttpMethodGET     HttpMethod = 1
	HttpMethodPOST    HttpMethod = 2
	HttpMethodPUT     HttpMethod = 3
	HttpMethodDELETE  HttpMethod = 4
	HttpMethodHEAD    HttpMethod = 5
	HttpMethodPATCH   HttpMethod = 6
)

// HttpDoErr enumerates the error codes returned in HttpDoRespPayload.Err.
// Zero means success; all error values are negative so callers can
// distinguish them from valid status codes without a separate flag.
type HttpDoErr int32

const (
	HttpDoOK            HttpDoErr = 0
	HttpDoErrGeneric    HttpDoErr = -1 // unspecified failure; check serial log
	HttpDoErrRespTooBig HttpDoErr = -2 // response exceeded the pre-posted Slab — see BytesNeeded
	HttpDoErrParse      HttpDoErr = -3 // malformed HTTP/1.1 response
	HttpDoErrTLS        HttpDoErr = -4 // TLS handshake or record failure
	HttpDoErrConnect    HttpDoErr = -5 // TCP connect failed
	HttpDoErrTimeout    HttpDoErr = -6 // operation exceeded the configured timeout
)

// HttpURLMaxInline caps the inline URL path length carried in the request
// payload. Longer URLs would require a separate share and are rejected by
// protocol-http with HttpDoErrGeneric. 32 bytes covers realistic API
// endpoint paths (e.g. Anthropic's "/v1/messages" is 12 chars).
const HttpURLMaxInline = 32

// HttpHostMaxInline caps the inline hostname length. 32 bytes covers
// realistic hostnames (e.g. "api.anthropic.com" is 17 chars).
const HttpHostMaxInline = 32

// HttpEndpointAddrSize is the byte width of the endpoint address slot.
// Sized for IPv6; IPv4 lives in the first 4 bytes with EndpointFamily=4.
// Future MAZ-33 (IPv6 default) flips real addresses into the rest of
// the slot without a wire-format break.
const HttpEndpointAddrSize = 16

// HttpAddrFamily enumerates the address family carried in EndpointAddr.
// Values match the conventional POSIX AF_* numbering so future
// migrations can swap to AF_INET / AF_INET6 directly.
type HttpAddrFamily uint8

const (
	HttpAddrInvalid HttpAddrFamily = 0
	HttpAddrIPv4    HttpAddrFamily = 4
	HttpAddrIPv6    HttpAddrFamily = 6
)

// HttpDoReqPayload is the payload for ProtoHttpIPCReq messages.
//
// Layout (112 bytes — exact fit for UringIPCMsg.Payload):
//
//	[0:2]    Method            HttpMethod
//	[2:4]    URLLen            uint16   — number of valid bytes in URLPath
//	[4:8]    ReqShareID        uint32   — sender's ShareID for the request Slab
//	[8:12]   RespShareID       uint32   — sender's ShareID for the pre-posted response Slab
//	[12:16]  HeadersMaxOffset  uint32   — byte offset within reqShare where body begins
//	[16:20]  ReqBodyLen        uint32   — actual body length within reqShare
//	[20:24]  ReqID             uint32   — for response correlation
//	[24:25]  RespRing          uint8    — ring index for ProtoHttpIPCResp
//	[25:26]  HostLen           uint8    — number of valid bytes in Host
//	[26:27]  EndpointFamily    HttpAddrFamily — 4 (IPv4) or 6 (IPv6); MAZ-33 enables v6
//	[27:28]  _pad0             uint8
//	[28:30]  EndpointPort      uint16   — TCP port (443 for HTTPS)
//	[30:32]  _pad1             uint16   — align EndpointAddr to 4 bytes
//	[32:48]  EndpointAddr      [16]byte — v4 uses first 4 bytes; v6 uses all 16
//	[48:80]  Host              [32]byte — SNI + Host header value (Host[:HostLen] is valid)
//	[80:112] URLPath           [32]byte — request-target path (URLPath[:URLLen] is valid)
type HttpDoReqPayload struct {
	Method           HttpMethod
	URLLen           uint16
	ReqShareID       uint32
	RespShareID      uint32
	HeadersMaxOffset uint32
	ReqBodyLen       uint32
	ReqID            uint32
	RespRing         uint8
	HostLen          uint8
	EndpointFamily   HttpAddrFamily
	_pad0            uint8
	EndpointPort     uint16
	_pad1            uint16
	EndpointAddr     [HttpEndpointAddrSize]byte
	Host             [HttpHostMaxInline]byte
	URLPath          [HttpURLMaxInline]byte
}

// HttpDoRespPayload is the payload for ProtoHttpIPCResp messages.
//
// Layout (24 bytes; rest of UringIPCMsg.Payload is reserved):
//
//	[0:4]    ReqID         uint32   — echoes the request's ReqID
//	[4:8]    StatusCode    int32    — HTTP status (0 if Err != HttpDoOK)
//	[8:12]   HeaderEnd     uint32   — byte offset within respShare where body starts
//	[12:16]  BodyLen       uint32   — body length in bytes within respShare
//	[16:20]  Err           HttpDoErr
//	[20:24]  BytesNeeded   uint32   — set when Err == HttpDoErrRespTooBig
type HttpDoRespPayload struct {
	ReqID       uint32
	StatusCode  int32
	HeaderEnd   uint32
	BodyLen     uint32
	Err         HttpDoErr
	BytesNeeded uint32
}

// Compile-time guarantee that both payloads fit in UringIPCMsg.Payload.
// Self-updating against UringIPCMsg.Payload's actual size, so a future
// Payload resize re-checks automatically.
var _ [unsafe.Sizeof(UringIPCMsg{}.Payload) - unsafe.Sizeof(HttpDoReqPayload{})]byte
var _ [unsafe.Sizeof(UringIPCMsg{}.Payload) - unsafe.Sizeof(HttpDoRespPayload{})]byte

// EncodeHttpDoReq packs a request payload into a UringIPCMsg.
func EncodeHttpDoReq(p *HttpDoReqPayload, senderSID int16) UringIPCMsg {
	var msg UringIPCMsg
	msg.Protocol = ProtoHttpIPCReq
	msg.SenderSID = senderSID
	*(*HttpDoReqPayload)(unsafe.Pointer(&msg.Payload[0])) = *p
	return msg
}

// DecodeHttpDoReq extracts the request payload from a UringIPCMsg.
func DecodeHttpDoReq(msg *UringIPCMsg) *HttpDoReqPayload {
	return (*HttpDoReqPayload)(unsafe.Pointer(&msg.Payload[0]))
}

// EncodeHttpDoResp packs a response payload into a UringIPCMsg.
func EncodeHttpDoResp(p *HttpDoRespPayload, senderSID int16) UringIPCMsg {
	var msg UringIPCMsg
	msg.Protocol = ProtoHttpIPCResp
	msg.SenderSID = senderSID
	*(*HttpDoRespPayload)(unsafe.Pointer(&msg.Payload[0])) = *p
	return msg
}

// DecodeHttpDoResp extracts the response payload from a UringIPCMsg.
func DecodeHttpDoResp(msg *UringIPCMsg) *HttpDoRespPayload {
	return (*HttpDoRespPayload)(unsafe.Pointer(&msg.Payload[0]))
}
