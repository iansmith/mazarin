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
	"time"

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
	return testNetClient()
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
