// dispatch.go — protocol-http's ProtoHttpIPCReq handler. Item 7 of MAZ-49.
//
// Flow per Do request (single-request-at-a-time in v1; MAZ-56 generalizes):
//
//  1. Decode the HttpDoReqPayload.
//  2. Receive two shares from the sender — the request body Slab and the
//     pre-posted response Slab. v1 pairs by arrival order (MAZ-56 will
//     key by ShareID).
//  3. TCPConnect via netclient → wrap with internal.Conn → TLS handshake.
//  4. Build HTTP/1.1 request line + headers into a small scratch buffer,
//     then memcpy them right-aligned into the request Slab's prefix
//     region (so the last header byte is adjacent to body[0]).
//  5. Single tls.Conn.Write covering [headers ‖ body] — zero body copy,
//     satisfying MAZ-49 DoD item 5.
//  6. Drain tls.Conn.Read directly into the response share's AsBytes()
//     view — zero-copy, satisfying MAZ-49 DoD item 6.
//  7. Parse status line + headers; locate the body offset within the
//     response share.
//  8. Reply with HttpDoRespPayload, then Release both shares.
//
// Errors are translated to HttpDoErr enum values and reported in the
// reply payload so the caller (mazarin/httpclient) can surface them
// without needing to interpret error strings.
package main

import (
	"io"
	"net"
	"os"
	"strconv"
	"time"

	"mazzy/maz/protocol-http/internal"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/transfer"
	"mazzy/mazarin/uring"
	"mazzy/shared/ipc"
	"mazzy/shared/netproto"
)

// handlerTLSDeadline bounds total time spent in the TLS handshake +
// request + response cycle. Without it a slow or non-closing peer
// would block the v1 single-request handler indefinitely (CodeRabbit
// review of PR #42, item #2). 15s is roomy for healthy long-haul
// connections and well below the caller-side defaultDoTimeout (30s)
// so a peer hang surfaces as HttpDoErrTLS rather than the caller
// timing out without diagnostic detail.
const handlerTLSDeadline = 15 * time.Second

// httpDoReqTagged carries the decoded payload plus the SenderSID so the
// handler can call ReceiveShare(senderSID).
type httpDoReqTagged struct {
	payload   ipc.HttpDoReqPayload
	senderSID int16
}

func decodeHttpDoReq(msg *ipc.UringIPCMsg) any {
	return httpDoReqTagged{
		payload:   *ipc.DecodeHttpDoReq(msg),
		senderSID: msg.SenderSID,
	}
}

func handleDo(v any) {
	tagged := v.(httpDoReqTagged)
	p := tagged.payload
	senderSID := tagged.senderSID

	// Reply construction is centralized in sendReply so every error
	// path produces exactly one outbound IPC and we don't leak shares.
	rs := newReplySender(p.ReqID, p.RespRing, senderSID)

	// 1. Claim both shares (req body + respDest). v1 arrival order
	// pairing — MAZ-56 will key by ShareID.
	reqShare, err := transfer.ReceiveShare(transfer.ShepherdID(senderSID))
	if err != nil {
		sys.UartWriteString(tag + "ReceiveShare(req): " + err.Error() + "\n")
		rs.fail(ipc.HttpDoErrGeneric, 0)
		return
	}
	defer reqShare.Release()

	respShare, err := transfer.ReceiveShare(transfer.ShepherdID(senderSID))
	if err != nil {
		sys.UartWriteString(tag + "ReceiveShare(resp): " + err.Error() + "\n")
		rs.fail(ipc.HttpDoErrGeneric, 0)
		return
	}
	defer respShare.Release()

	// 2. Validate IPC-supplied lengths before slicing. The IPC fields are
	// trusted (in-VM, same kernel), but a buggy client or future
	// fuzz-tested code path could produce malformed payloads — failing
	// fast with HttpDoErrGeneric is cheaper than a slice-panic that
	// tears down the shepherd.
	hostLen := int(p.HostLen)
	if hostLen == 0 || hostLen > len(p.Host) {
		sys.UartWriteString(tag + "HostLen out of range\n")
		rs.fail(ipc.HttpDoErrGeneric, 0)
		return
	}
	host := string(p.Host[:hostLen])
	if p.EndpointFamily != ipc.HttpAddrIPv4 {
		// IPv6 path lands when MAZ-33 widens netproto.Addr. Until then
		// we surface a clear error rather than silently truncating.
		sys.UartWriteString(tag + "EndpointFamily IPv6 not yet wired (MAZ-33)\n")
		rs.fail(ipc.HttpDoErrConnect, 0)
		return
	}
	var ip4 [4]byte
	copy(ip4[:], p.EndpointAddr[:4])
	dst := netproto.Addr{IP4: ip4, Port: p.EndpointPort}
	connID, _, err := nc.TCPConnect([4]byte{}, 0, dst)
	if err != nil {
		sys.UartWriteString(tag + "TCPConnect: " + err.Error() + "\n")
		rs.fail(ipc.HttpDoErrConnect, 0)
		return
	}
	netConn := internal.New(nc, connID)
	defer netConn.Close()

	cfg, err := internal.TLSConfig(host, caPool, 0)
	if err != nil {
		sys.UartWriteString(tag + "TLSConfig: " + err.Error() + "\n")
		rs.fail(ipc.HttpDoErrTLS, 0)
		return
	}
	tlsConn, err := internal.DialTLS(netConn, cfg)
	if err != nil {
		sys.UartWriteString(tag + "DialTLS: " + err.Error() + "\n")
		rs.fail(ipc.HttpDoErrTLS, 0)
		return
	}
	defer tlsConn.CloseWrite()
	// Bound the I/O window so a slow / non-closing peer can't park
	// the handler indefinitely (v1 processes one Do at a time).
	if err := tlsConn.SetDeadline(time.Now().Add(handlerTLSDeadline)); err != nil {
		sys.UartWriteString(tag + "SetDeadline: " + err.Error() + "\n")
		rs.fail(ipc.HttpDoErrTLS, 0)
		return
	}
	defer tlsConn.SetDeadline(time.Time{})

	// 3. Build the HTTP/1.1 headers + request line into a small scratch
	// buffer. The body length is known (p.ReqBodyLen) so we can stamp
	// Content-Length now.
	method := internal.MethodString(uint16(p.Method))
	if method == "" {
		rs.fail(ipc.HttpDoErrGeneric, 0)
		return
	}
	// URL lives in the request Slab at [0:URLLen], not inline in the
	// IPC payload — supports arbitrary-length paths (REST routes with
	// embedded IDs, long query strings).
	reqBytes := reqShare.AsBytes()
	// Up-front bounds checks on every IPC-supplied length we'll use to
	// slice: URLLen, HeadersMaxOffset, and HeadersMaxOffset+ReqBodyLen.
	// Saves us from sprinkling defensive checks at each slice site.
	urlLen := int(p.URLLen)
	hdrMax := int(p.HeadersMaxOffset)
	bodyLen := int(p.ReqBodyLen)
	if urlLen < 0 || hdrMax < 0 || bodyLen < 0 {
		rs.fail(ipc.HttpDoErrGeneric, 0)
		return
	}
	if urlLen > len(reqBytes) || hdrMax > len(reqBytes) || hdrMax+bodyLen > len(reqBytes) {
		rs.fail(ipc.HttpDoErrGeneric, 0)
		return
	}
	urlPath := string(reqBytes[:urlLen])
	headers := []internal.Header{
		{Name: "Content-Type", Value: "application/json"},
		{Name: "Content-Length", Value: strconv.FormatUint(uint64(p.ReqBodyLen), 10)},
		{Name: "Connection", Value: "close"},
	}
	var hdrScratch [internal.MaxHeaderSize]byte
	hdrLen, err := internal.BuildRequest(hdrScratch[:], method, urlPath, host, headers)
	if err != nil {
		sys.UartWriteString(tag + "BuildRequest: " + err.Error() + "\n")
		rs.fail(ipc.HttpDoErrGeneric, 0)
		return
	}

	// 4. Right-align headers into the prefix region. The body is already
	// at [HeadersMaxOffset : HeadersMaxOffset+ReqBodyLen]; we want the
	// last header byte at HeadersMaxOffset-1, so headers start at
	// HeadersMaxOffset - hdrLen. (hdrMax was bounds-checked above.)
	if hdrLen > hdrMax {
		sys.UartWriteString(tag + "headers exceed prefix region\n")
		rs.fail(ipc.HttpDoErrGeneric, 0)
		return
	}
	hdrStart := hdrMax - hdrLen
	if hdrStart < urlLen {
		// URL + headers don't both fit in the prefix region. Caller
		// needs a bigger HeadersMax. v1 surfaces as generic; future
		// dedicated error code if it shows up in practice.
		sys.UartWriteString(tag + "URL + headers overflow prefix region\n")
		rs.fail(ipc.HttpDoErrGeneric, 0)
		return
	}
	copy(reqBytes[hdrStart:hdrMax], hdrScratch[:hdrLen])

	// 5. Single TLS write: [headers ‖ body]. wireEnd is bounded by the
	// earlier hdrMax + bodyLen <= len(reqBytes) check.
	wireEnd := hdrMax + bodyLen
	if _, err := tlsConn.Write(reqBytes[hdrStart:wireEnd]); err != nil {
		sys.UartWriteString(tag + "tlsConn.Write: " + err.Error() + "\n")
		rs.fail(ipc.HttpDoErrTLS, 0)
		return
	}

	// 6. Drain the response into respShare. v1 reads until EOF (we sent
	// Connection: close, so the server closes after the response). If
	// the response exceeds respShare capacity we report RespTooBig —
	// BUT only when we actually ran out of room. An exact-fit response
	// terminated by EOF must not be misclassified as too-big.
	respBytes := respShare.AsBytes()
	n := 0
	sawEOF := false
	for n < len(respBytes) {
		nr, err := tlsConn.Read(respBytes[n:])
		n += nr
		if err == io.EOF {
			sawEOF = true
			break
		}
		if err != nil {
			sys.UartWriteString(tag + "tlsConn.Read: " + err.Error() + "\n")
			rs.fail(ipc.HttpDoErrTLS, 0)
			return
		}
	}
	if n == len(respBytes) && !sawEOF {
		// Filled to capacity AND there may be more on the wire we
		// didn't get to read. Surface as RespTooBig so the caller can
		// retry with a bigger Slab. We don't know exactly how much
		// more, so just report "+1 page" as the hint.
		rs.fail(ipc.HttpDoErrRespTooBig, uint32(len(respBytes))+4096)
		return
	}

	// 7. Parse status + headers.
	statusCode, headersStart, err := internal.ParseStatusLine(respBytes[:n])
	if err != nil {
		sys.UartWriteString(tag + "ParseStatusLine: " + err.Error() + "\n")
		rs.fail(ipc.HttpDoErrParse, 0)
		return
	}
	_, bodyStart, err := internal.ParseHeaders(respBytes[:n], headersStart)
	if err != nil {
		sys.UartWriteString(tag + "ParseHeaders: " + err.Error() + "\n")
		rs.fail(ipc.HttpDoErrParse, 0)
		return
	}

	// 8. Reply with success.
	rs.ok(int32(statusCode), uint32(bodyStart), uint32(n-bodyStart))
}

// replySender is the centralized exit point for handleDo. It guarantees
// exactly one HttpDoRespPayload is sent per Do request regardless of
// which branch terminated the handler.
type replySender struct {
	reqID     uint32
	respRing  uint8
	targetSID int16
}

func newReplySender(reqID uint32, respRing uint8, targetSID int16) *replySender {
	return &replySender{reqID: reqID, respRing: respRing, targetSID: targetSID}
}

func (r *replySender) ok(statusCode int32, headerEnd, bodyLen uint32) {
	p := ipc.HttpDoRespPayload{
		ReqID:      r.reqID,
		StatusCode: statusCode,
		HeaderEnd:  headerEnd,
		BodyLen:    bodyLen,
		Err:        ipc.HttpDoOK,
	}
	r.send(&p)
}

func (r *replySender) fail(code ipc.HttpDoErr, bytesNeeded uint32) {
	p := ipc.HttpDoRespPayload{
		ReqID:       r.reqID,
		Err:         code,
		BytesNeeded: bytesNeeded,
	}
	r.send(&p)
}

func (r *replySender) send(p *ipc.HttpDoRespPayload) {
	msg := ipc.EncodeHttpDoResp(p, int16(os.Getpid()))
	if err := uring.SendWithRing(int(r.targetSID), &msg, int(r.respRing)); err != nil {
		sys.UartWriteString(tag + "reply Send: " + err.Error() + "\n")
	}
}

// Compile-time net.Conn check on the netconn wrapper; here as a sanity
// hook so a future netconn redesign doesn't silently break TLS dial.
var _ net.Conn = (*internal.Conn)(nil)
