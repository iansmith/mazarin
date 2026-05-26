package ipc

import "testing"

func TestShareReq_EncodeDecodeRoundtrip(t *testing.T) {
	want := ShareReqPayload{
		ShareID: 0xdeadbeef,
		Bytes:   1024,
		VA:      0x4000_0000,
	}
	msg := EncodeShareReq(&want, 17)
	if msg.Protocol != ProtoShareReq {
		t.Fatalf("Protocol: want %d got %d", ProtoShareReq, msg.Protocol)
	}
	if msg.SenderSID != 17 {
		t.Fatalf("SenderSID: want 17 got %d", msg.SenderSID)
	}
	got := DecodeShareReq(&msg)
	if *got != want {
		t.Fatalf("roundtrip: want %+v got %+v", want, *got)
	}
}

func TestShareRelease_EncodeDecodeRoundtrip(t *testing.T) {
	want := ShareReleasePayload{ShareID: 42}
	msg := EncodeShareRelease(&want, 17)
	if msg.Protocol != ProtoShareRelease {
		t.Fatalf("Protocol: want %d got %d", ProtoShareRelease, msg.Protocol)
	}
	got := DecodeShareRelease(&msg)
	if got.ShareID != 42 {
		t.Fatalf("ShareID: want 42 got %d", got.ShareID)
	}
}

func TestShareReq_SubPageBytes(t *testing.T) {
	// A ShareRange of a sub-page region produces Bytes < 4096.
	// Roundtrip preserves the sub-page byte count.
	want := ShareReqPayload{ShareID: 99, Bytes: 137, VA: 0x9000_1234}
	msg := EncodeShareReq(&want, 0)
	got := DecodeShareReq(&msg)
	if got.Bytes != 137 || got.VA != 0x9000_1234 {
		t.Fatalf("sub-page roundtrip: want Bytes=137 VA=0x90001234, got %+v", *got)
	}
}
