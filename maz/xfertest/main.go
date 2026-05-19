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

	// --- Test 3: NetIPC round-trip against net.elf (Connect + BindUDP) ---
	testNetIPC()

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

// testNetIPC runs a Connect + BindUDP round-trip against net.elf and
// logs PASS / FAIL per stage. Stops at the first FAIL.
//
// All responses arrive on ring 0 (xfertest has no other listeners),
// through a single shared channel; stages run serially so ReqID
// disambiguation isn't needed.
func testNetIPC() bool {
	if err := sys.WaitForShepherdReady("net", 5); err != nil {
		sys.UartWriteString(tag + "FAIL: net not ready: " + err.Error() + "\n")
		return false
	}
	netSID, err := sys.GetShepherdByName("net")
	if err != nil {
		sys.UartWriteString(tag + "FAIL: GetShepherdByName(net): " + err.Error() + "\n")
		return false
	}

	respCh := make(chan any, 1)
	disp := uring.NewDispatcher()
	disp.On(ipc.ProtoNetIPCResp, decodeNetIPCResp, respCh)
	disp.Start()

	if !stageConnect(netSID, respCh) {
		return false
	}
	if !stageBindUDP(netSID, respCh) {
		return false
	}
	return true
}

func stageConnect(netSID int, respCh <-chan any) bool {
	const reqID = 0xC0FFEE
	req := netproto.NetIPCConnectReq{ReqID: reqID, RespRing: 0}
	msg := netproto.EncodeConnect(&req, int16(os.Getpid()))
	if err := uring.Send(netSID, &msg); err != nil {
		sys.UartWriteString(tag + "FAIL: Connect send: " + err.Error() + "\n")
		return false
	}
	select {
	case v := <-respCh:
		resp, ok := v.(*netproto.NetIPCConnectResp)
		if !ok {
			sys.UartWriteString(tag + "FAIL: Connect resp wrong type\n")
			return false
		}
		if resp.ReqID != reqID || resp.ErrCode != netproto.NetErrNone ||
			resp.Watermark != netproto.DefaultTxWatermark {
			sys.UartWriteString(tag + "FAIL: Connect bad fields reqID=" + sys.Itoa(int64(resp.ReqID)) +
				" err=" + sys.Itoa(int64(resp.ErrCode)) +
				" wm=" + sys.Itoa(int64(resp.Watermark)) + "\n")
			return false
		}
		sys.UartWriteString(tag + "PASS NetIPCConnect netSID=" + sys.Itoa(int64(netSID)) +
			" watermark=" + sys.Itoa(int64(resp.Watermark)) + "\n")
		return true
	case <-time.After(2 * time.Second):
		sys.UartWriteString(tag + "FAIL: Connect response timeout\n")
		return false
	}
}

func stageBindUDP(netSID int, respCh <-chan any) bool {
	const reqID = 0xBADBEEF
	req := netproto.BindUDPReq{
		ReqID:     reqID,
		LocalPort: 0, // ephemeral
		LocalIP:   [4]byte{0, 0, 0, 0},
	}
	msg := netproto.EncodeBindUDP(&req, int16(os.Getpid()))
	if err := uring.Send(netSID, &msg); err != nil {
		sys.UartWriteString(tag + "FAIL: BindUDP send: " + err.Error() + "\n")
		return false
	}
	select {
	case v := <-respCh:
		resp, ok := v.(*netproto.BindUDPResp)
		if !ok {
			sys.UartWriteString(tag + "FAIL: BindUDP resp wrong type\n")
			return false
		}
		if resp.ReqID != reqID || resp.ErrCode != netproto.NetErrNone || resp.ConnID == 0 || resp.LocalPort == 0 {
			sys.UartWriteString(tag + "FAIL: BindUDP bad fields reqID=" + sys.Itoa(int64(resp.ReqID)) +
				" err=" + sys.Itoa(int64(resp.ErrCode)) +
				" connID=" + sys.Itoa(int64(resp.ConnID)) +
				" port=" + sys.Itoa(int64(resp.LocalPort)) + "\n")
			return false
		}
		sys.UartWriteString(tag + "PASS NetIPCBindUDP connID=" + sys.Itoa(int64(resp.ConnID)) +
			" port=" + sys.Itoa(int64(resp.LocalPort)) + "\n")
		return true
	case <-time.After(2 * time.Second):
		sys.UartWriteString(tag + "FAIL: BindUDP response timeout\n")
		return false
	}
}

// decodeNetIPCResp is the uring Dispatcher decoder for ProtoNetIPCResp.
// Copy-by-value so the slot can be reused after the callback returns.
func decodeNetIPCResp(msg *ipc.UringIPCMsg) any {
	switch netproto.MsgTypeOf(msg) {
	case netproto.NetMsgConnectResp:
		r := *netproto.DecodeConnectResp(msg)
		return &r
	case netproto.NetMsgBindUDPResp:
		r := *netproto.DecodeBindUDPResp(msg)
		return &r
	}
	return nil
}
