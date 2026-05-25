package ipc

import "testing"

func TestTransferReq_EncodeDecodeRoundtrip(t *testing.T) {
	want := TransferReqPayload{
		Op:       TransferOpReserve,
		Kind:     0xcafebabe,
		Size:     4096,
		VA:       0,
		Pages:    0,
		ReqID:    42,
		RespRing: 3,
	}
	msg := EncodeTransferReq(&want, 17)
	if msg.Protocol != ProtoTransferReq {
		t.Fatalf("Protocol: want %d got %d", ProtoTransferReq, msg.Protocol)
	}
	if msg.SenderSID != 17 {
		t.Fatalf("SenderSID: want 17 got %d", msg.SenderSID)
	}
	got := DecodeTransferReq(&msg)
	if *got != want {
		t.Fatalf("roundtrip: want %+v got %+v", want, *got)
	}
}

func TestTransferResp_EncodeDecodeRoundtrip(t *testing.T) {
	want := TransferRespPayload{
		ReqID: 42,
		Err:   0,
		VA:    0x4000_0000,
		Pages: 1,
	}
	msg := EncodeTransferResp(&want, 17)
	if msg.Protocol != ProtoTransferResp {
		t.Fatalf("Protocol: want %d got %d", ProtoTransferResp, msg.Protocol)
	}
	got := DecodeTransferResp(&msg)
	if *got != want {
		t.Fatalf("roundtrip: want %+v got %+v", want, *got)
	}
}

func TestTransferResp_NegativeErrno(t *testing.T) {
	// Err is signed; verify that round-tripping a negative errno preserves it.
	want := TransferRespPayload{
		ReqID: 99,
		Err:   -12, // ENOMEM
	}
	msg := EncodeTransferResp(&want, 0)
	got := DecodeTransferResp(&msg)
	if got.Err != -12 {
		t.Fatalf("Err: want -12 got %d", got.Err)
	}
}
