// xfertest is the MAZ-29 Phase 1 boot-time smoke test for the two new
// ownership-primitive syscalls:
//
//	SysTransferDMAClump        — client→target page handoff (one whole DMA clump)
//	SysShareNetPageWithClient  — caller-owned page mapped RW into a client
//
// It is launched once per boot via /xfertest.maz and prints PASS/FAIL lines to
// the UART. Any regression in either syscall surfaces in the serial log at
// the next boot.
//
// The test uses the linux shepherd as the transfer target (guaranteed to be
// running before user programs start). Pages transferred to linux remain
// mapped at high VAs that linux's allocators never touch; the per-boot 8KB
// leak is acceptable for a boot smoke test.
package main

import (
	"os"
	"time"

	"mazzy/mazarin/mazhost"
	"mazzy/mazarin/mem"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	"mazzy/shared/ipc"
	"mazzy/shared/netproto"
)

func init() { mazhost.PinEntry(MazarinMain, nil) }

const (
	tag       = "[xfertest] "
	patternA  = byte(0xAB)
	patternB  = byte(0xCD)
	targetSym = "linux" // resolvable via sys.GetShepherdByName
)

//go:noinline
func MazarinMain() {
	sys.UartWriteString(tag + "start\n")

	// Wait for the target shepherd to be ready. Without this the smoke test
	// races the launcher; targetSym is a key shepherd brought up early but
	// the user-program group may still beat it on a fast boot.
	if err := sys.WaitForShepherdReady(targetSym, 5); err != nil {
		sys.UartWriteString(tag + "FAIL: " + targetSym + " not ready: " + err.Error() + "\n")
		return
	}
	targetSID, err := sys.GetShepherdByName(targetSym)
	if err != nil {
		sys.UartWriteString(tag + "FAIL: GetShepherdByName(" + targetSym + "): " + err.Error() + "\n")
		return
	}
	sys.UartWriteString(tag + "target " + targetSym + " sid=" + sys.Itoa(int64(targetSID)) + "\n")

	// --- Test 1: SyscallTransferDMAClump ---
	clump1, err := mem.AllocContiguous(4096)
	if err != nil {
		sys.UartWriteString(tag + "FAIL: AllocContiguous#1: " + err.Error() + "\n")
		return
	}
	for i := range clump1.Buf {
		clump1.Buf[i] = patternA
	}
	dstVA, err := sys.TransferDMAClump(targetSID, clump1.Addr, 0)
	if err != nil {
		sys.UartWriteString(tag + "FAIL: TransferDMAClump: " + err.Error() + "\n")
		return
	}
	if dstVA == 0 {
		sys.UartWriteString(tag + "FAIL: TransferDMAClump returned VA=0\n")
		return
	}
	sys.UartWriteString(tag + "PASS TransferDMAClump dstVA=0x" + sys.Hex64(uint64(dstVA)) + "\n")

	// --- Test 2: SyscallShareNetPageWithClient ---
	// The first clump's table entry is gone after the transfer, so we can
	// allocate a second clump without bumping into MaxDMAClumps.
	clump2, err := mem.AllocContiguous(4096)
	if err != nil {
		sys.UartWriteString(tag + "FAIL: AllocContiguous#2: " + err.Error() + "\n")
		return
	}
	for i := range clump2.Buf {
		clump2.Buf[i] = patternB
	}
	clientVA, err := sys.ShareNetPageWithClient(targetSID, clump2.Addr, 0)
	if err != nil {
		sys.UartWriteString(tag + "FAIL: ShareNetPageWithClient: " + err.Error() + "\n")
		return
	}
	if clientVA == 0 {
		sys.UartWriteString(tag + "FAIL: ShareNetPageWithClient returned VA=0\n")
		return
	}
	sys.UartWriteString(tag + "PASS ShareNetPageWithClient clientVA=0x" + sys.Hex64(uint64(clientVA)) + "\n")

	// --- Test 3: NetIPCConnect round-trip against net.elf ---
	testNetIPCConnect()

	sys.UartWriteString(tag + "all checks done\n")

	// Stay alive regardless of test outcome. The kernel's
	// CleanupShepherdDMAClumps currently BuddyFreeTyped's clump pages
	// without consulting PageDescriptor.RefCount, which would yank
	// linux's mapping out from under it (use-after-free) if we exit.
	// Fixing that is a separate ticket — see MAZ-29 findings — and is
	// orthogonal to whether the syscalls themselves work.
	sys.SetReady(true)
	for {
		time.Sleep(time.Hour)
	}
}

func main() { MazarinMain() }

// testNetIPCConnect sends a NetIPCConnect to net.elf and verifies the
// returned ConnectResp. The client requests Watermark=0 (default) and
// declares RespRing=0 (we just registered a dispatcher there).
//
// Returns true on PASS, false on FAIL. On FAIL, a FAIL line is already
// logged.
func testNetIPCConnect() bool {
	if err := sys.WaitForShepherdReady("net", 5); err != nil {
		sys.UartWriteString(tag + "FAIL: net not ready: " + err.Error() + "\n")
		return false
	}
	netSID, err := sys.GetShepherdByName("net")
	if err != nil {
		sys.UartWriteString(tag + "FAIL: GetShepherdByName(net): " + err.Error() + "\n")
		return false
	}

	// Use ring 0 for responses — xfertest doesn't have any other ring
	// listener, no head-of-line concern.
	respCh := make(chan any, 1)
	disp := uring.NewDispatcher()
	disp.On(ipc.ProtoNetIPCResp, decodeNetIPCResp, respCh)
	disp.Start()

	const reqID = 0xC0FFEE
	req := netproto.NetIPCConnectReq{
		ReqID:    reqID,
		RespRing: 0,
		// Watermark=0 → expect default (DefaultTxWatermark)
	}
	msg := netproto.EncodeConnect(&req, int16(os.Getpid()))
	if err := uring.Send(int(netSID), &msg); err != nil {
		sys.UartWriteString(tag + "FAIL: NetIPCConnect send: " + err.Error() + "\n")
		return false
	}

	select {
	case v := <-respCh:
		resp, ok := v.(*netproto.NetIPCConnectResp)
		if !ok {
			sys.UartWriteString(tag + "FAIL: NetIPCConnect resp wrong type\n")
			return false
		}
		if resp.ReqID != reqID {
			sys.UartWriteString(tag + "FAIL: NetIPCConnect reqID mismatch got=" + sys.Itoa(int64(resp.ReqID)) + "\n")
			return false
		}
		if resp.ErrCode != netproto.NetErrNone {
			sys.UartWriteString(tag + "FAIL: NetIPCConnect ErrCode=" + sys.Itoa(int64(resp.ErrCode)) + "\n")
			return false
		}
		if resp.Watermark != netproto.DefaultTxWatermark {
			sys.UartWriteString(tag + "FAIL: NetIPCConnect Watermark got=" + sys.Itoa(int64(resp.Watermark)) +
				" want=" + sys.Itoa(int64(netproto.DefaultTxWatermark)) + "\n")
			return false
		}
		sys.UartWriteString(tag + "PASS NetIPCConnect netSID=" + sys.Itoa(int64(netSID)) +
			" watermark=" + sys.Itoa(int64(resp.Watermark)) + "\n")
		return true
	case <-time.After(2 * time.Second):
		sys.UartWriteString(tag + "FAIL: NetIPCConnect response timeout\n")
		return false
	}
}

// decodeNetIPCResp is the uring Dispatcher decoder for ProtoNetIPCResp.
// For this smoke test we only care about NetIPCConnectResp; other types
// are returned untyped (caller's type-switch handles).
func decodeNetIPCResp(msg *ipc.UringIPCMsg) any {
	if netproto.MsgTypeOf(msg) == netproto.NetMsgConnectResp {
		// Copy by value — the dispatcher's slot may be reused after we return.
		r := *netproto.DecodeConnectResp(msg)
		return &r
	}
	return nil
}
