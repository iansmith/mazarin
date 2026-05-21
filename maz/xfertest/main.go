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
	"syscall"
	"time"
	"unsafe"

	"mazzy/mazarin/mazhost"
	"mazzy/mazarin/mem"
	"mazzy/mazarin/netclient"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/uring"
	"mazzy/shared/ipc"
	"mazzy/shared/netproto"
)

// loopbackV4 matches gvisor's IPv4Loopback; declared here so xfertest
// doesn't need the gvisor import path.
var loopbackV4 = [4]byte{127, 0, 0, 1}

// stageSendDgramDst aims at a known-unbound port so gvisor accepts the
// Write and discards at the local UDP demux — keeps the next stage's
// recv queue free of unsolicited deliveries from this stage's send.
var stageSendDgramDst = netproto.Addr{IP4: loopbackV4, Port: 9999}

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
	if runSmokeTests() {
		sys.UartWriteString(tag + "all checks done\n")
	} else {
		sys.UartWriteString(tag + "checks failed; staying alive\n")
	}
	// MUST reach this unconditionally. xfertest can have live clump
	// entries in linux's address space (post-TransferDMAClump) or pages
	// shared into linux (post-ShareNetPageWithClient) at this point. The
	// kernel's CleanupShepherdDMAClumps BuddyFreeTyped's clump pages
	// without consulting PageDescriptor.RefCount, so exiting would yank
	// linux's mappings (use-after-free). Tracked as a separate ticket.
	sys.SetReady(true)
	select {}
}

// runSmokeTests returns true if every stage passed. Early returns are
// fine; MazarinMain's keep-alive loop runs regardless of outcome.
//
// Stages called from runSmokeTests follow one of two conventions:
//   - `func testXxx() bool` — blocking; `false` halts subsequent stages.
//     Use when later stages depend on this one succeeding.
//   - `func testXxx()` — non-blocking; failure prints FAIL but stages continue.
//     Use for loud-but-ignorable stages.
//
// The no-return signature is a contract; don't promote a non-blocking stage
// to blocking without explicit ticket approval.
func runSmokeTests() bool {
	// Wait for the target shepherd to be ready. Without this the smoke test
	// races the launcher; targetSym is a key shepherd brought up early but
	// the user-program group may still beat it on a fast boot.
	if err := sys.WaitForShepherdReady(targetSym, 5); err != nil {
		sys.UartWriteString(tag + "FAIL: " + targetSym + " not ready: " + err.Error() + "\n")
		return false
	}
	targetSID, err := sys.GetShepherdByName(targetSym)
	if err != nil {
		sys.UartWriteString(tag + "FAIL: GetShepherdByName(" + targetSym + "): " + err.Error() + "\n")
		return false
	}
	sys.UartWriteString(tag + "target " + targetSym + " sid=" + sys.Itoa(int64(targetSID)) + "\n")

	// --- Test 1: SyscallTransferDMAClump ---
	clump1, err := mem.AllocContiguous(4096)
	if err != nil {
		sys.UartWriteString(tag + "FAIL: AllocContiguous#1: " + err.Error() + "\n")
		return false
	}
	for i := range clump1.Buf {
		clump1.Buf[i] = patternA
	}
	dstVA, err := sys.TransferDMAClump(targetSID, clump1.Addr, 0)
	if err != nil {
		sys.UartWriteString(tag + "FAIL: TransferDMAClump: " + err.Error() + "\n")
		return false
	}
	if dstVA == 0 {
		sys.UartWriteString(tag + "FAIL: TransferDMAClump returned VA=0\n")
		return false
	}
	sys.UartWriteString(tag + "PASS TransferDMAClump dstVA=0x" + sys.Hex64(uint64(dstVA)) + "\n")

	// --- Test 1b: TransferDMAClump rollback on MapPageInProcess failure ---
	// Arm a one-shot MapPageInProcess failure via the kernel debug knob,
	// then attempt a transfer that must fail mid-Pass-2 and roll back. The
	// rollback restores caller ownership + mapping; the source clump entry
	// stays intact, so CleanupShepherdDMAClumps remains safe on caller exit.
	clumpRB, err := mem.AllocContiguous(4096)
	if err != nil {
		sys.UartWriteString(tag + "FAIL: AllocContiguous#1b: " + err.Error() + "\n")
		return false
	}
	for i := range clumpRB.Buf {
		clumpRB.Buf[i] = patternA
	}
	sys.SetMapFailInjection(true)
	_, rbErr := sys.TransferDMAClump(targetSID, clumpRB.Addr, 0)
	sys.SetMapFailInjection(false)
	if rbErr == nil {
		sys.UartWriteString(tag + "FAIL: rollback test — TransferDMAClump should have returned ENOMEM (got nil)\n")
		return false
	}
	if rbErr != syscall.ENOMEM {
		sys.UartWriteString(tag + "FAIL: rollback test — expected ENOMEM, got: " + rbErr.Error() + "\n")
		return false
	}
	// Caller-side rollback verification: clump page is remapped and readable,
	// and the pattern we wrote pre-transfer is intact (page wasn't zeroed or
	// replaced during the transfer→rollback round-trip).
	if clumpRB.Buf[0] != patternA {
		sys.UartWriteString(tag + "FAIL: rollback test — source page content corrupted (got 0x" +
			sys.Hex64(uint64(clumpRB.Buf[0])) + ")\n")
		return false
	}
	sys.UartWriteString(tag + "PASS TransferDMAClump rollback on MapPageInProcess failure\n")

	// --- Test 1c: TransferDMAClump rollback preserves caller's PTE flags (MAZ-39) ---
	// Pass a non-default elfFlags to the syscall (R+W+X = 7). With the buggy
	// rollback this would restore the caller's mapping using elfFlags=7
	// (mismatch with the caller's pre-transfer R+W = 6). With the fix, rollback
	// uses origFlags captured in Pass 1 and the caller's mapping comes back
	// with exactly its pre-transfer flags.
	clumpFlags, err := mem.AllocContiguous(4096)
	if err != nil {
		sys.UartWriteString(tag + "FAIL: AllocContiguous#1c: " + err.Error() + "\n")
		return false
	}
	for i := range clumpFlags.Buf {
		clumpFlags.Buf[i] = patternA
	}
	flagsBefore := sys.GetPTEFlags(clumpFlags.Addr)
	if flagsBefore == 0 {
		sys.UartWriteString(tag + "FAIL: flag-preserve test — caller page unmapped before transfer\n")
		return false
	}
	const nonDefaultElfFlags uint32 = 0x7 // PF_R | PF_W | PF_X
	sys.SetMapFailInjection(true)
	_, fpErr := sys.TransferDMAClump(targetSID, clumpFlags.Addr, nonDefaultElfFlags)
	sys.SetMapFailInjection(false)
	if fpErr == nil {
		sys.UartWriteString(tag + "FAIL: flag-preserve test — TransferDMAClump should have returned ENOMEM (got nil)\n")
		return false
	}
	if fpErr != syscall.ENOMEM {
		sys.UartWriteString(tag + "FAIL: flag-preserve test — expected ENOMEM, got: " + fpErr.Error() + "\n")
		return false
	}
	flagsAfter := sys.GetPTEFlags(clumpFlags.Addr)
	if flagsAfter != flagsBefore {
		sys.UartWriteString(tag + "FAIL: flag-preserve test — caller flags drifted: before=0x" +
			sys.Hex64(uint64(flagsBefore)) + " after=0x" + sys.Hex64(uint64(flagsAfter)) +
			" (expected rollback to restore pre-transfer flags, not elfFlags=0x7)\n")
		return false
	}
	if clumpFlags.Buf[0] != patternA {
		sys.UartWriteString(tag + "FAIL: flag-preserve test — page contents corrupted (got 0x" +
			sys.Hex64(uint64(clumpFlags.Buf[0])) + ")\n")
		return false
	}
	sys.UartWriteString(tag + "PASS TransferDMAClump rollback preserves caller PTE flags (flags=0x" +
		sys.Hex64(uint64(flagsBefore)) + ")\n")

	// --- Test 2: SyscallShareNetPageWithClient ---
	// The first clump's table entry is gone after the transfer, so we can
	// allocate a second clump without bumping into MaxDMAClumps.
	clump2, err := mem.AllocContiguous(4096)
	if err != nil {
		sys.UartWriteString(tag + "FAIL: AllocContiguous#2: " + err.Error() + "\n")
		return false
	}
	for i := range clump2.Buf {
		clump2.Buf[i] = patternB
	}
	clientVA, err := sys.ShareNetPageWithClient(targetSID, clump2.Addr, 0)
	if err != nil {
		sys.UartWriteString(tag + "FAIL: ShareNetPageWithClient: " + err.Error() + "\n")
		return false
	}
	if clientVA == 0 {
		sys.UartWriteString(tag + "FAIL: ShareNetPageWithClient returned VA=0\n")
		return false
	}
	sys.UartWriteString(tag + "PASS ShareNetPageWithClient clientVA=0x" + sys.Hex64(uint64(clientVA)) + "\n")

	// --- Test 3: NetIPC round-trip against net.elf via NetClient ---
	if !testNetClient() {
		return false
	}

	// --- Test 4: TransferPages partial-failure rollback (MAZ-37 Phase 0 red test) ---
	// Allocate caller-owned pages, arm a one-shot MapPageInProcess failure,
	// call sys.TransferPages, expect ENOMEM, then VERIFY THE SOURCE PAGES ARE
	// STILL READABLE post-call. On current code (no rollback for TransferPages),
	// the forward Pass-2 unmap drops the caller's mapping before the failing
	// map call, so reading pagesTP[0] page-faults xfertest and we never reach
	// the PASS line. After MAZ-37 lands, rollback restores caller mappings and
	// this stage passes cleanly.
	pagesTP, allocErr := mem.AllocPagesSlice(2, mem.PageShared)
	if allocErr != nil {
		sys.UartWriteString(tag + "FAIL: AllocPagesSlice#TP: " + allocErr.Error() + "\n")
		return false
	}
	for i := range pagesTP {
		pagesTP[i] = patternA
	}
	tpSourceVA := uintptr(unsafe.Pointer(&pagesTP[0]))
	sys.SetMapFailInjection(true)
	_, tpErr := sys.TransferPages(int(targetSID), tpSourceVA, 2, 0)
	sys.SetMapFailInjection(false)
	if tpErr == nil {
		sys.UartWriteString(tag + "FAIL: TransferPages rollback — should have returned ENOMEM (got nil)\n")
		return false
	}
	if tpErr != syscall.ENOMEM {
		sys.UartWriteString(tag + "FAIL: TransferPages rollback — expected ENOMEM, got: " + tpErr.Error() + "\n")
		return false
	}
	// The next read crashes xfertest on current code (no rollback). After
	// MAZ-37, the rollback restores the mapping and the pattern survives.
	if pagesTP[0] != patternA {
		sys.UartWriteString(tag + "FAIL: TransferPages rollback — source page contents corrupted (got 0x" +
			sys.Hex64(uint64(pagesTP[0])) + ")\n")
		return false
	}
	sys.UartWriteString(tag + "PASS TransferPages rollback on MapPageInProcess failure\n")

	// --- Test 4b: TransferPages MULTI-CHUNK rollback ---
	// Allocate transferChunkPages+8 = 4104 caller-owned pages (slightly larger
	// than one kernel chunk so we exercise cross-chunk rollback). Arm
	// SetMapFailAfter(4097) so the first 4096 forward maps (all of chunk 0)
	// succeed, then call #4097 — chunk 1's first map — fails. Verify ENOMEM
	// AND that EVERY page survived the rollback (cross-chunk walk-back must
	// restore caller mappings for chunk 0's entire contents in addition to
	// chunk 1's prefix).
	//
	// 4097 is hardcoded against kmazarin/ksyscall/share_pages.go's
	// transferChunkPages = 4096. If that constant changes, this number must
	// change too. Documented here so the linkage is discoverable.
	const tpMultiPages = 4104
	pagesTP2, allocErr2 := mem.AllocPagesSlice(tpMultiPages, mem.PageShared)
	if allocErr2 != nil {
		sys.UartWriteString(tag + "FAIL: AllocPagesSlice#TP2: " + allocErr2.Error() + "\n")
		return false
	}
	for i := range pagesTP2 {
		pagesTP2[i] = patternB
	}
	tpSourceVA2 := uintptr(unsafe.Pointer(&pagesTP2[0]))
	sys.SetMapFailAfter(4097)
	_, tpErr2 := sys.TransferPages(int(targetSID), tpSourceVA2, tpMultiPages, 0)
	sys.SetMapFailAfter(-1) // explicit disarm (also clears any residual)
	if tpErr2 == nil {
		sys.UartWriteString(tag + "FAIL: TransferPages multi-chunk rollback — should have returned ENOMEM (got nil)\n")
		return false
	}
	if tpErr2 != syscall.ENOMEM {
		sys.UartWriteString(tag + "FAIL: TransferPages multi-chunk rollback — expected ENOMEM, got: " + tpErr2.Error() + "\n")
		return false
	}
	// Spot-check the first byte of every page. If cross-chunk rollback worked,
	// every page should still show patternB. A FAIL here would mean either a
	// chunk-0 page wasn't rolled back (cross-chunk walk-back broken) or a
	// chunk-1 prefix page wasn't restored (per-chunk-prefix rollback broken).
	for i := 0; i < tpMultiPages; i++ {
		if pagesTP2[i*4096] != patternB {
			sys.UartWriteString(tag + "FAIL: TransferPages multi-chunk rollback — page " +
				sys.Itoa(int64(i)) + " corrupted (got 0x" + sys.Hex64(uint64(pagesTP2[i*4096])) + ")\n")
			return false
		}
	}
	sys.UartWriteString(tag + "PASS TransferPages multi-chunk rollback (chunks restored)\n")

	// --- Test 4c: TransferPages rollback preserves caller's PTE flags (MAZ-39) ---
	// Sibling to Test 1c, exercising SyscallTransferPages's two rollback paths
	// (transferPagesChunkInner Pass-2 prefix rollback AND
	// rollbackPagesChunkInner cross-chunk rollback). Same shape: pass a
	// non-default elfFlags to the syscall, arm injection, expect ENOMEM,
	// verify caller's PTE flags are restored to their pre-transfer value (R+W)
	// rather than the target's elfFlags (R+W+X).
	//
	// Use 2 pages: one-chunk rollback. tpMultiPages would also work but adds
	// cost without testing anything new about the flag-preservation contract
	// — the same origFlags slice feeds both rollback walks.
	pagesFP, allocErrFP := mem.AllocPagesSlice(2, mem.PageShared)
	if allocErrFP != nil {
		sys.UartWriteString(tag + "FAIL: AllocPagesSlice#FP: " + allocErrFP.Error() + "\n")
		return false
	}
	for i := range pagesFP {
		pagesFP[i] = patternA
	}
	fpSourceVA := uintptr(unsafe.Pointer(&pagesFP[0]))
	fpFlagsBefore := sys.GetPTEFlags(fpSourceVA)
	if fpFlagsBefore == 0 {
		sys.UartWriteString(tag + "FAIL: TransferPages flag-preserve — caller page unmapped before transfer\n")
		return false
	}
	sys.SetMapFailInjection(true)
	_, fpTpErr := sys.TransferPages(int(targetSID), fpSourceVA, 2, nonDefaultElfFlags)
	sys.SetMapFailInjection(false)
	if fpTpErr == nil {
		sys.UartWriteString(tag + "FAIL: TransferPages flag-preserve — should have returned ENOMEM (got nil)\n")
		return false
	}
	if fpTpErr != syscall.ENOMEM {
		sys.UartWriteString(tag + "FAIL: TransferPages flag-preserve — expected ENOMEM, got: " + fpTpErr.Error() + "\n")
		return false
	}
	fpFlagsAfter := sys.GetPTEFlags(fpSourceVA)
	if fpFlagsAfter != fpFlagsBefore {
		sys.UartWriteString(tag + "FAIL: TransferPages flag-preserve — caller flags drifted: before=0x" +
			sys.Hex64(uint64(fpFlagsBefore)) + " after=0x" + sys.Hex64(uint64(fpFlagsAfter)) +
			" (expected rollback to restore pre-transfer flags, not elfFlags=0x7)\n")
		return false
	}
	if pagesFP[0] != patternA {
		sys.UartWriteString(tag + "FAIL: TransferPages flag-preserve — page contents corrupted (got 0x" +
			sys.Hex64(uint64(pagesFP[0])) + ")\n")
		return false
	}
	sys.UartWriteString(tag + "PASS TransferPages rollback preserves caller PTE flags (flags=0x" +
		sys.Hex64(uint64(fpFlagsBefore)) + ")\n")

	// --- Test 5: MAZ-38 realTcpExample — real-network smoke test ---
	// Non-blocking per MAZ-38 DoD: a FAIL inside testRealTCPExample MUST NOT
	// prevent earlier stages from being trusted. The signature has no return,
	// so runSmokeTests still returns true and MazarinMain prints "all checks done".
	testRealTCPExample()

	return true
}

func main() { MazarinMain() }

// testNetClient drives the Phase 4 NetClient against net.elf and prints
// PASS/FAIL per stage. NetClient's pending-map handles ReqID matching;
// stages just call ergonomic methods.
func testNetClient() bool {
	if err := sys.WaitForShepherdReady("net", 5); err != nil {
		sys.UartWriteString(tag + "FAIL: net not ready: " + err.Error() + "\n")
		return false
	}
	netSID, err := sys.GetShepherdByName("net")
	if err != nil {
		sys.UartWriteString(tag + "FAIL: GetShepherdByName(net): " + err.Error() + "\n")
		return false
	}

	nc := netclient.New(netSID)
	disp := uring.NewDispatcher()
	disp.OnFunc(ipc.ProtoNetIPCResp, netclient.DecodeAny, nc.HandleResp)
	disp.Start()

	if err := nc.Connect(0, 0); err != nil {
		sys.UartWriteString(tag + "FAIL: Connect: " + err.Error() + "\n")
		return false
	}
	sys.UartWriteString(tag + "PASS NetIPCConnect netSID=" + sys.Itoa(int64(netSID)) + "\n")

	connA, portA, err := nc.BindUDP([4]byte{}, 0)
	if err != nil {
		sys.UartWriteString(tag + "FAIL: BindUDP A: " + err.Error() + "\n")
		return false
	}
	sys.UartWriteString(tag + "PASS NetIPCBindUDP connID=" + sys.Itoa(int64(connA)) +
		" port=" + sys.Itoa(int64(portA)) + "\n")

	if err := nc.SendTo(connA, stageSendDgramDst, []byte("xfertest sendto")); err != nil {
		sys.UartWriteString(tag + "FAIL: SendTo (no-listener): " + err.Error() + "\n")
		return false
	}
	sys.UartWriteString(tag + "PASS NetIPCSendDgram connID=" + sys.Itoa(int64(connA)) + "\n")

	connB, portB, err := nc.BindUDP([4]byte{}, 0)
	if err != nil {
		sys.UartWriteString(tag + "FAIL: BindUDP B: " + err.Error() + "\n")
		return false
	}
	sys.UartWriteString(tag + "PASS NetIPCBindUDP connID=" + sys.Itoa(int64(connB)) +
		" port=" + sys.Itoa(int64(portB)) + "\n")

	if !udpRoundTrip(nc, "RecvDgram", connA,
		netproto.Addr{IP4: loopbackV4, Port: portB}, connB, portA, 24, 'R') {
		return false
	}

	connC, _, err := nc.BindUDP([4]byte{}, 0)
	if err != nil {
		sys.UartWriteString(tag + "FAIL: BindUDP C: " + err.Error() + "\n")
		return false
	}
	sys.UartWriteString(tag + "PASS NetIPCBindUDP connID=" + sys.Itoa(int64(connC)) + "\n")
	const echoPort uint16 = 7
	if !udpRoundTrip(nc, "UDPEcho", connC,
		netproto.Addr{IP4: loopbackV4, Port: echoPort}, connC, echoPort, 32, 'E') {
		return false
	}

	for _, c := range []uint32{connA, connB, connC} {
		if err := nc.Close(c); err != nil {
			sys.UartWriteString(tag + "FAIL: Close connID=" + sys.Itoa(int64(c)) + ": " + err.Error() + "\n")
			return false
		}
		sys.UartWriteString(tag + "PASS NetIPCClose connID=" + sys.Itoa(int64(c)) + "\n")
	}

	return testNetClientTCP(nc)
}

// tcpEchoRoundTrip drives the full TCP echo exchange and EOF half-close
// on a connected stream. Caller owns the Close. On failure logs FAIL
// via UART and returns false (caller bails out without closing —
// leaks acceptable for a boot smoke test).
func tcpEchoRoundTrip(nc netclient.NetClient, connID uint32) bool {
	streamPayload := []byte("hello tcp echo")
	sent, err := nc.StreamSend(connID, streamPayload)
	if err != nil {
		sys.UartWriteString(tag + "FAIL: StreamSend to echo: " + err.Error() + "\n")
		return false
	}
	if sent != len(streamPayload) {
		sys.UartWriteString(tag + "FAIL: StreamSend short write sent=" +
			sys.Itoa(int64(sent)) + " want=" + sys.Itoa(int64(len(streamPayload))) + "\n")
		return false
	}
	sys.UartWriteString(tag + "PASS NetIPCStreamSend connID=" + sys.Itoa(int64(connID)) +
		" sent=" + sys.Itoa(int64(sent)) + "\n")

	// Drain echo bytes — gvisor on loopback typically delivers the
	// full 14 bytes in one chunk, but loop in case it fragments.
	gotBytes := 0
	for gotBytes < len(streamPayload) {
		chunk, err := nc.ReadStream(connID)
		if err != nil {
			sys.UartWriteString(tag + "FAIL: ReadStream: " + err.Error() + "\n")
			return false
		}
		if chunk.EOF {
			sys.UartWriteString(tag + "FAIL: unexpected EOF before all bytes (got=" +
				sys.Itoa(int64(gotBytes)) + ")\n")
			return false
		}
		payload := chunk.Payload()
		// Bounds-check before indexing streamPayload — a malformed/oversized
		// chunk should fail with FAIL, not panic-on-out-of-bounds.
		if gotBytes+len(payload) > len(streamPayload) {
			sys.UartWriteString(tag + "FAIL: echo payload longer than expected (gotBytes=" +
				sys.Itoa(int64(gotBytes)) + " chunk=" + sys.Itoa(int64(len(payload))) +
				" want=" + sys.Itoa(int64(len(streamPayload))) + ")\n")
			return false
		}
		for i, b := range payload {
			if b != streamPayload[gotBytes+i] {
				sys.UartWriteString(tag + "FAIL: echo mismatch at i=" +
					sys.Itoa(int64(gotBytes+i)) + "\n")
				return false
			}
		}
		gotBytes += len(payload)
		if err := nc.ReleaseRX(connID, chunk.Page); err != nil {
			sys.UartWriteString(tag + "FAIL: ReleaseRX: " + err.Error() + "\n")
			return false
		}
	}
	sys.UartWriteString(tag + "PASS NetIPCStreamRx connID=" + sys.Itoa(int64(connID)) +
		" bytes=" + sys.Itoa(int64(gotBytes)) + "\n")

	// Half-close write side — peer's tcpEchoServe will see EOF and
	// drop its end, sending FIN back so the bridge emits StreamRx EOF.
	if err := nc.Shutdown(connID, netproto.ShutdownWrite); err != nil {
		sys.UartWriteString(tag + "FAIL: Shutdown(Write): " + err.Error() + "\n")
		return false
	}
	sys.UartWriteString(tag + "PASS NetIPCStreamShutdown connID=" + sys.Itoa(int64(connID)) + "\n")

	eof, err := nc.ReadStream(connID)
	if err != nil {
		sys.UartWriteString(tag + "FAIL: ReadStream EOF: " + err.Error() + "\n")
		return false
	}
	if !eof.EOF {
		sys.UartWriteString(tag + "FAIL: expected EOF, got Length=" +
			sys.Itoa(int64(eof.Length)) + "\n")
		return false
	}
	sys.UartWriteString(tag + "PASS NetIPCStreamRx EOF connID=" + sys.Itoa(int64(connID)) + "\n")
	return true
}

// testNetClientTCP exercises the Phase 6 step-3 TCP handshake surface:
// BindTCP+Listen, TCPConnect to the in-stack echo server, and an
// Accept against a self-bound listener (using a goroutine to drive the
// inbound TCPConnect). No data flow yet — that lands in step 4-5.
func testNetClientTCP(nc netclient.NetClient) bool {
	const echoPort uint16 = 7
	const tcpBacklog uint16 = 4

	// Stage A: BindTCP + Listen + Close (listener plumbing).
	listenA, listenPortA, err := nc.BindTCP([4]byte{}, 0)
	if err != nil {
		sys.UartWriteString(tag + "FAIL: BindTCP A: " + err.Error() + "\n")
		return false
	}
	sys.UartWriteString(tag + "PASS NetIPCBindTCP connID=" + sys.Itoa(int64(listenA)) +
		" port=" + sys.Itoa(int64(listenPortA)) + "\n")
	if err := nc.Listen(listenA, tcpBacklog); err != nil {
		sys.UartWriteString(tag + "FAIL: Listen A: " + err.Error() + "\n")
		return false
	}
	sys.UartWriteString(tag + "PASS NetIPCListen connID=" + sys.Itoa(int64(listenA)) + "\n")
	if err := nc.Close(listenA); err != nil {
		sys.UartWriteString(tag + "FAIL: Close listener A: " + err.Error() + "\n")
		return false
	}
	sys.UartWriteString(tag + "PASS NetIPCClose listener connID=" + sys.Itoa(int64(listenA)) + "\n")

	// Stage B: TCPConnect to the in-stack echo server, send a payload,
	// confirm the per-stream send page round-trip lands the bytes in
	// gvisor's send queue (BytesWritten == len(payload)). RX side is
	// not yet exercised here — see the StreamRx round-trip stage.
	echoDst := netproto.Addr{IP4: loopbackV4, Port: echoPort}
	streamB, localPortB, err := nc.TCPConnect([4]byte{}, 0, echoDst)
	if err != nil {
		sys.UartWriteString(tag + "FAIL: TCPConnect to echo: " + err.Error() + "\n")
		return false
	}
	sys.UartWriteString(tag + "PASS NetIPCTCPConnect connID=" + sys.Itoa(int64(streamB)) +
		" localPort=" + sys.Itoa(int64(localPortB)) + "\n")

	if !tcpEchoRoundTrip(nc, streamB) {
		return false
	}

	if err := nc.Close(streamB); err != nil {
		sys.UartWriteString(tag + "FAIL: Close stream B: " + err.Error() + "\n")
		return false
	}
	sys.UartWriteString(tag + "PASS NetIPCClose stream connID=" + sys.Itoa(int64(streamB)) + "\n")

	// Stage C: BindTCP + Listen + concurrent TCPConnect-to-self + Accept.
	listenC, listenPortC, err := nc.BindTCP([4]byte{}, 0)
	if err != nil {
		sys.UartWriteString(tag + "FAIL: BindTCP C: " + err.Error() + "\n")
		return false
	}
	if err := nc.Listen(listenC, tcpBacklog); err != nil {
		sys.UartWriteString(tag + "FAIL: Listen C: " + err.Error() + "\n")
		return false
	}
	sys.UartWriteString(tag + "PASS NetIPCBindTCP+Listen self connID=" + sys.Itoa(int64(listenC)) +
		" port=" + sys.Itoa(int64(listenPortC)) + "\n")

	// Connector goroutine drives the inbound TCPConnect; main path Accepts.
	type connectResult struct {
		connID uint32
		err    error
	}
	resCh := make(chan connectResult, 1)
	go func() {
		dst := netproto.Addr{IP4: loopbackV4, Port: listenPortC}
		cid, _, cerr := nc.TCPConnect([4]byte{}, 0, dst)
		resCh <- connectResult{connID: cid, err: cerr}
	}()

	acceptedID, peer, err := nc.Accept(listenC)
	if err != nil {
		sys.UartWriteString(tag + "FAIL: Accept C: " + err.Error() + "\n")
		return false
	}
	// Bound the wait on the connector goroutine — a wedged TCPConnect
	// would otherwise hang the boot since xfertest is a serial smoke
	// test with no other path to surface failure.
	var cres connectResult
	select {
	case cres = <-resCh:
	case <-time.After(5 * time.Second):
		sys.UartWriteString(tag + "FAIL: connector TCPConnect timeout\n")
		return false
	}
	if cres.err != nil {
		sys.UartWriteString(tag + "FAIL: connector TCPConnect: " + cres.err.Error() + "\n")
		return false
	}
	sys.UartWriteString(tag + "PASS NetIPCAccept connID=" + sys.Itoa(int64(acceptedID)) +
		" peer.port=" + sys.Itoa(int64(peer.Port)) +
		" connector.connID=" + sys.Itoa(int64(cres.connID)) + "\n")

	for _, c := range []uint32{listenC, acceptedID, cres.connID} {
		if err := nc.Close(c); err != nil {
			sys.UartWriteString(tag + "FAIL: Close TCP connID=" + sys.Itoa(int64(c)) + ": " + err.Error() + "\n")
			return false
		}
	}
	sys.UartWriteString(tag + "PASS NetIPC TCP self-accept teardown\n")
	return true
}

// udpRoundTrip sends a patterned payload via nc.SendTo and asserts the
// matching RecvDgram arrives on expectRecvConn from expectSrcPort.
// Verifies length, src port, and the byte pattern; releases the loaned
// RX page before returning. ok is the named return so the ReleaseRX
// defer can flip it to false if the release itself errors.
func udpRoundTrip(nc netclient.NetClient, label string, senderConn uint32,
	dst netproto.Addr, expectRecvConn uint32, expectSrcPort uint16,
	payloadSz int, pattern byte) (ok bool) {

	payload := make([]byte, payloadSz)
	for i := range payload {
		payload[i] = pattern + byte(i)
	}
	if err := nc.SendTo(senderConn, dst, payload); err != nil {
		sys.UartWriteString(tag + "FAIL: " + label + " SendTo: " + err.Error() + "\n")
		return false
	}
	rx, err := nc.RecvFrom(expectRecvConn)
	if err != nil {
		sys.UartWriteString(tag + "FAIL: " + label + " RecvFrom: " + err.Error() + "\n")
		return false
	}
	defer func() {
		if relErr := nc.ReleaseRX(expectRecvConn, rx.Page); relErr != nil {
			sys.UartWriteString(tag + "FAIL: " + label + " ReleaseRX: " + relErr.Error() + "\n")
			ok = false
		}
	}()

	if int(rx.Length) != payloadSz {
		sys.UartWriteString(tag + "FAIL: " + label + " wrong Length=" +
			sys.Itoa(int64(rx.Length)) + "\n")
		return false
	}
	if rx.Src.Port != expectSrcPort {
		sys.UartWriteString(tag + "FAIL: " + label + " wrong Src.Port=" +
			sys.Itoa(int64(rx.Src.Port)) + " want=" + sys.Itoa(int64(expectSrcPort)) + "\n")
		return false
	}
	for i, b := range rx.Payload() {
		if b != pattern+byte(i) {
			sys.UartWriteString(tag + "FAIL: " + label + " payload mismatch at i=" +
				sys.Itoa(int64(i)) + " got=" + sys.Itoa(int64(b)) + "\n")
			return false
		}
	}
	sys.UartWriteString(tag + "PASS NetIPC" + label +
		" connID=" + sys.Itoa(int64(expectRecvConn)) +
		" len=" + sys.Itoa(int64(rx.Length)) +
		" srcPort=" + sys.Itoa(int64(rx.Src.Port)) +
		" pageVA=0x" + sys.Hex64(rx.Page) + "\n")
	return true
}

// testRealTCPExample is the MAZ-38 boot-time smoke test for the actual outbound
// data path: NetClient → net.elf → gvisor TX → virtio-net-pci → QEMU SLIRP NAT
// → host network. All other xfertest stages terminate inside gvisor's local
// demux (loopbackV4 = 127.0.0.1); this stage is the only one that exercises
// the device-boundary crossing.
//
// Stub implementation — prints a FAIL line so the absence of real-network
// coverage is visible in the serial log until the real connect/HTTP/recv flow
// lands. Intentionally has no return value so callers cannot accidentally
// promote a stage failure into a gate on subsequent stages.
func testRealTCPExample() {
	sys.UartWriteString(tag + "FAIL: realTcpExample — stub, not yet implemented (MAZ-38)\n")
}
