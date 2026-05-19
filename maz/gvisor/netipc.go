// netipc.go — NetIPC dispatcher inside the gvisor.maz plugin.
//
// Net.elf accepts ProtoNetIPCReq on its default IPC ring 0 and forwards
// each message to the handler this file registers via
// LinkSurfaceInjector.RegisterNetIPCHandler. Handler runs in net.elf's
// NetIPC reader goroutine; the msg pointer is only valid for the
// lifetime of the call, so heavy work that wants to outlive the read
// must copy first.
//
// Phase 3 step 1 (this file) is plumbing only — every handler is a
// counter-bump + log line. Subsequent commits will fill in the
// real Connect / BindUDP / SendDgram / Release / Close logic, plus the
// per-endpoint RX reader goroutine that produces RecvDgram
// notifications.
package main

import (
	"fmt"
	"sync/atomic"

	"mazzy/shared/ipc"
	"mazzy/shared/netproto"
)

var (
	dbgNetIPCReceived uint64
	dbgNetIPCUnknown  uint64
)

func handleNetIPC(msg *ipc.UringIPCMsg) {
	atomic.AddUint64(&dbgNetIPCReceived, 1)
	mt := netproto.MsgTypeOf(msg)
	switch mt {
	case netproto.NetMsgConnect:
		fmt.Printf("[gvisor:netipc] Connect from SID=%d (Phase 3 stub)\n", msg.SenderSID)
	case netproto.NetMsgBindUDP,
		netproto.NetMsgSendDgram,
		netproto.NetMsgRelease,
		netproto.NetMsgClose:
		fmt.Printf("[gvisor:netipc] type=%d SID=%d (Phase 3 stub)\n", mt, msg.SenderSID)
	default:
		atomic.AddUint64(&dbgNetIPCUnknown, 1)
		fmt.Printf("[gvisor:netipc] unknown type=%d SID=%d\n", mt, msg.SenderSID)
	}
}
