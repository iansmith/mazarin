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
	}
	copy(want.URLPath[:], "/v1/messages")

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
	if string(got.URLPath[:got.URLLen]) != "/v1/messages" {
		t.Fatalf("URLPath: want /v1/messages got %q", string(got.URLPath[:got.URLLen]))
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
			want.URLPath[0] = '/'
			msg := EncodeHttpDoReq(&want, 0)
			got := DecodeHttpDoReq(&msg)
			if got.Method != m {
				t.Fatalf("Method: want %d got %d", m, got.Method)
			}
		})
	}
}

func TestHttpDoReq_MaxURLLengthInline(t *testing.T) {
	// A URL exactly HttpURLMaxInline bytes long roundtrips without truncation.
	want := HttpDoReqPayload{Method: HttpMethodGET, URLLen: HttpURLMaxInline}
	for i := range HttpURLMaxInline {
		want.URLPath[i] = byte('a' + (i % 26))
	}
	msg := EncodeHttpDoReq(&want, 0)
	got := DecodeHttpDoReq(&msg)
	if got.URLLen != HttpURLMaxInline {
		t.Fatalf("URLLen: want %d got %d", HttpURLMaxInline, got.URLLen)
	}
	if got.URLPath != want.URLPath {
		t.Fatalf("URLPath mismatch at max length")
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
	want.URLPath[0] = '/'
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
