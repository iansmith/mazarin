package ipc

import "testing"

func TestHttpDoReq_EncodeDecodeRoundtrip(t *testing.T) {
	want := HttpDoReqPayload{
		Method:           HttpMethodPOST,
		URLLen:           12,
		ReqShareID:       0x1111_2222,
		RespShareID:      0x3333_4444,
		HeadersMaxOffset: 4096,
		ReqBodyLen:       128,
		ReqID:            0xdeadbeef,
		RespRing:         2,
		HostLen:          17,
		EndpointFamily:   HttpAddrIPv4,
		EndpointPort:     443,
	}
	// IPv4: first 4 bytes of EndpointAddr; rest stays zero.
	copy(want.EndpointAddr[:4], []byte{1, 2, 3, 4})
	copy(want.Host[:], "api.anthropic.com")
	// URL no longer travels inline — it lives in the request Slab.
	// URLLen is the only IPC-side data we track for the URL.

	msg := EncodeHttpDoReq(&want, 17)
	if msg.Protocol != ProtoHttpIPCReq {
		t.Fatalf("Protocol: want %d got %d", ProtoHttpIPCReq, msg.Protocol)
	}
	if msg.SenderSID != 17 {
		t.Fatalf("SenderSID: want 17 got %d", msg.SenderSID)
	}
	got := DecodeHttpDoReq(&msg)
	if *got != want {
		t.Fatalf("roundtrip mismatch:\n  want %+v\n  got  %+v", want, *got)
	}
	if got.URLLen != 12 {
		t.Fatalf("URLLen: want 12 got %d", got.URLLen)
	}
	if string(got.Host[:got.HostLen]) != "api.anthropic.com" {
		t.Fatalf("Host: want api.anthropic.com got %q", string(got.Host[:got.HostLen]))
	}
	if got.EndpointFamily != HttpAddrIPv4 {
		t.Fatalf("EndpointFamily: want IPv4 (4), got %d", got.EndpointFamily)
	}
	wantAddr := [HttpEndpointAddrSize]byte{1, 2, 3, 4}
	if got.EndpointAddr != wantAddr {
		t.Fatalf("EndpointAddr: want %v, got %v", wantAddr, got.EndpointAddr)
	}
	if got.EndpointPort != 443 {
		t.Fatalf("EndpointPort: want 443 got %d", got.EndpointPort)
	}
}

func TestHttpDoReq_IPv6AddressRoundtrip(t *testing.T) {
	// 2606:4700:4700::1111 (Cloudflare IPv6 DNS) — a real v6 address.
	// Verifies the IPC payload carries all 16 bytes intact when the
	// family flips to IPv6 (the actual TCPConnect side comes later
	// under MAZ-33).
	v6 := [HttpEndpointAddrSize]byte{
		0x26, 0x06, 0x47, 0x00, 0x47, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x11, 0x11,
	}
	want := HttpDoReqPayload{
		Method:         HttpMethodGET,
		EndpointFamily: HttpAddrIPv6,
		EndpointAddr:   v6,
		EndpointPort:   443,
	}
	msg := EncodeHttpDoReq(&want, 0)
	got := DecodeHttpDoReq(&msg)
	if got.EndpointFamily != HttpAddrIPv6 {
		t.Fatalf("EndpointFamily: want IPv6 (6), got %d", got.EndpointFamily)
	}
	if got.EndpointAddr != v6 {
		t.Fatalf("EndpointAddr IPv6: want %v, got %v", v6, got.EndpointAddr)
	}
}

func TestHttpDoReq_AllMethodsRoundtrip(t *testing.T) {
	methods := []HttpMethod{
		HttpMethodGET, HttpMethodPOST, HttpMethodPUT,
		HttpMethodDELETE, HttpMethodHEAD, HttpMethodPATCH,
	}
	for _, m := range methods {
		t.Run(methodName(m), func(t *testing.T) {
			want := HttpDoReqPayload{Method: m, URLLen: 1}
			msg := EncodeHttpDoReq(&want, 0)
			got := DecodeHttpDoReq(&msg)
			if got.Method != m {
				t.Fatalf("Method: want %d got %d", m, got.Method)
			}
		})
	}
}

func TestHttpDoReq_URLLenIsU16(t *testing.T) {
	// URLLen is a uint16 — large URLs (REST paths with embedded IDs +
	// query strings) are supported by writing them into the request
	// Slab itself; only the length travels in the IPC. Verify a
	// realistically long URLLen roundtrips intact.
	const longURL = 4000 // ~4 KiB, what a real request Slab might hold
	want := HttpDoReqPayload{Method: HttpMethodGET, URLLen: longURL}
	msg := EncodeHttpDoReq(&want, 0)
	got := DecodeHttpDoReq(&msg)
	if got.URLLen != longURL {
		t.Fatalf("URLLen: want %d got %d", longURL, got.URLLen)
	}
}

func TestHttpDoResp_EncodeDecodeRoundtrip(t *testing.T) {
	want := HttpDoRespPayload{
		ReqID:       0xdeadbeef,
		StatusCode:  200,
		HeaderEnd:   384,
		BodyLen:     1024,
		Err:         HttpDoOK,
		BytesNeeded: 0,
	}
	msg := EncodeHttpDoResp(&want, 42)
	if msg.Protocol != ProtoHttpIPCResp {
		t.Fatalf("Protocol: want %d got %d", ProtoHttpIPCResp, msg.Protocol)
	}
	if msg.SenderSID != 42 {
		t.Fatalf("SenderSID: want 42 got %d", msg.SenderSID)
	}
	got := DecodeHttpDoResp(&msg)
	if *got != want {
		t.Fatalf("roundtrip mismatch:\n  want %+v\n  got  %+v", want, *got)
	}
}

func TestHttpDoResp_ErrCases(t *testing.T) {
	cases := []struct {
		name string
		err  HttpDoErr
	}{
		{"RespTooBig", HttpDoErrRespTooBig},
		{"Parse", HttpDoErrParse},
		{"TLS", HttpDoErrTLS},
		{"Connect", HttpDoErrConnect},
		{"Timeout", HttpDoErrTimeout},
		{"Generic", HttpDoErrGeneric},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want := HttpDoRespPayload{ReqID: 7, Err: c.err}
			if c.err == HttpDoErrRespTooBig {
				want.BytesNeeded = 8192
			}
			msg := EncodeHttpDoResp(&want, 0)
			got := DecodeHttpDoResp(&msg)
			if got.Err != c.err {
				t.Fatalf("Err: want %d got %d", c.err, got.Err)
			}
			if got.BytesNeeded != want.BytesNeeded {
				t.Fatalf("BytesNeeded: want %d got %d", want.BytesNeeded, got.BytesNeeded)
			}
		})
	}
}

func TestHttpDoReq_DifferentSenderSIDsDoNotClobberPayload(t *testing.T) {
	// Sanity: the Encode path must leave the payload bytes alone even when
	// SenderSID varies. Catches future regressions where Encode mistakenly
	// writes into UringIPCMsg.Payload before the typed copy.
	want := HttpDoReqPayload{Method: HttpMethodPOST, ReqID: 0xcafef00d, URLLen: 1}
	for _, sid := range []int16{0, 1, -1, 32767, -32768} {
		msg := EncodeHttpDoReq(&want, sid)
		got := DecodeHttpDoReq(&msg)
		if got.ReqID != 0xcafef00d || got.Method != HttpMethodPOST {
			t.Fatalf("SenderSID=%d clobbered payload: %+v", sid, *got)
		}
	}
}

func methodName(m HttpMethod) string {
	switch m {
	case HttpMethodGET:
		return "GET"
	case HttpMethodPOST:
		return "POST"
	case HttpMethodPUT:
		return "PUT"
	case HttpMethodDELETE:
		return "DELETE"
	case HttpMethodHEAD:
		return "HEAD"
	case HttpMethodPATCH:
		return "PATCH"
	default:
		return "INVALID"
	}
}
