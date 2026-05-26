// chainipc.go — payload for ProtoChainReq (MAZ-53 chained-share boot test).
//
// sharetest sends a ProtoChainReq to test-fixture-http to trigger the
// A → B → C chain: test-fixture-http (A) allocates a Slab, fills it with
// PatternA, and shares it back to sharetest (B). sharetest then reshares
// to shareprobe (C). See design/mazarin-transfer-state-machine.md.
//
// There is no application payload: test-fixture-http derives the target
// ShepherdID for the return share from msg.SenderSID.
package ipc

import "unsafe"

// ChainReqPayload carries no application data. Declared for size-assertion
// symmetry with other payload types; the only meaningful field is the
// SenderSID in the enclosing UringIPCMsg header.
type ChainReqPayload struct {
	_pad [8]byte // reserved, must be zero
}

// Compile-time size assertion.
var _ [unsafe.Sizeof(UringIPCMsg{}.Payload) - unsafe.Sizeof(ChainReqPayload{})]byte

// EncodeChainReq builds a ProtoChainReq UringIPCMsg.
func EncodeChainReq(senderSID int16) UringIPCMsg {
	var msg UringIPCMsg
	msg.Protocol = ProtoChainReq
	msg.SenderSID = senderSID
	return msg
}
