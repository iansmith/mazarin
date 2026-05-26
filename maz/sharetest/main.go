// sharetest is the SENDER side of the MAZ-53 boot-time smoke for the
// mazarin/transfer Mode 2 (Share) IPC path.
//
// Stages run sequentially against shareprobe (the consumer). Each stage
// shares pages, waits for the Release IPC from shareprobe (signalled via
// transfer.SetReleaseHook → releaseDone channel), then validates the
// post-release state of the Slab.
//
// Expected output in serial log:
//
//	[sharetest] PASS basic share
//	[sharetest] PASS sub-range share
//	[sharetest] PASS bidirectional write
//	[sharetest] PASS multi-share isolation
//	[sharetest] all stages PASS
//
// Any failure prints `[sharetest] FAIL ...`.
package main

import (
	"os"
	"time"

	"mazzy/maz/internal/sharepayload"
	"mazzy/mazarin/mazhost"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/transfer"
	"mazzy/mazarin/uring"
	"mazzy/shared/ipc"
)

const tag = "[sharetest] "

// releaseDone is signalled by the SetReleaseHook callback after each Release
// is fully processed (UnshareFromTarget complete). Buffered so the dispatcher
// goroutine never blocks.
var releaseDone = make(chan transfer.ShareID, 16)

func init() { mazhost.PinEntry(MazarinMain, nil) }

//go:noinline
func MazarinMain() {
	sys.UartWriteString(tag + "start (sender)\n")

	transfer.SetReleaseHook(func(id transfer.ShareID) {
		select {
		case releaseDone <- id:
		default:
		}
	})

	disp := uring.NewDispatcher()
	transfer.RegisterShareRelease(disp)
	// RegisterShareConsumer is needed for stage 5 (chain): sharetest acts as
	// the B-node and receives a Share IPC from test-fixture-http (A).
	transfer.RegisterShareConsumer(disp)
	disp.Start()

	sys.SetReady(true)

	if err := sys.WaitForShepherdReady("shareprobe", 5); err != nil {
		sys.UartWriteString(tag + "FAIL shareprobe not ready: " + err.Error() + "\n")
		select {}
	}
	probeSID, err := sys.GetShepherdByName("shareprobe")
	if err != nil {
		sys.UartWriteString(tag + "FAIL GetShepherdByName(shareprobe): " + err.Error() + "\n")
		select {}
	}
	consumer := transfer.ShepherdID(probeSID)

	fixtureSID, err := sys.GetShepherdByName("test-fixture-http")
	if err != nil {
		sys.UartWriteString(tag + "FAIL GetShepherdByName(test-fixture-http): " + err.Error() + "\n")
		select {}
	}
	fixture := transfer.ShepherdID(fixtureSID)

	all := testBasicShare(consumer)
	all = testSubRangeShare(consumer) && all
	all = testBidirectionalWrite(consumer) && all
	all = testMultiShareIsolation(consumer) && all
	all = testChainShare(fixture, consumer) && all

	if all {
		sys.UartWriteString(tag + "all stages PASS\n")
	} else {
		sys.UartWriteString(tag + "one or more stages FAILED\n")
	}
	select {}
}

// waitRelease blocks until the Release for the given ShareID arrives or a
// 5-second timeout fires. Other ShareIDs are re-queued (shouldn't happen in
// sequential staging but handled defensively).
func waitRelease(id transfer.ShareID) bool {
	timeout := time.After(5 * time.Second)
	for {
		select {
		case got := <-releaseDone:
			if got == id {
				return true
			}
			// Unexpected ID (sequential staging, shouldn't happen). Re-queue
			// non-blocking; if the buffer is full we drop and rely on timeout.
			select {
			case releaseDone <- got:
			default:
			}
		case <-timeout:
			sys.UartWriteString(tag + "FAIL: Release timed out\n")
			return false
		}
	}
}

// fillSlab fills all bytes of the Slab with pattern.
func fillSlab(slab *transfer.Slab, pattern byte) {
	b := slab.Bytes()
	for i := range b {
		b[i] = pattern
	}
}

// --- Stage 1: basic whole-slab share ----------------------------------------

func testBasicShare(consumer transfer.ShepherdID) bool {
	slab, err := transfer.Allocate(transfer.PageSize, 0)
	if err != nil {
		sys.UartWriteString(tag + "FAIL basic share: Allocate: " + err.Error() + "\n")
		return false
	}
	defer slab.Release()
	fillSlab(slab, sharepayload.PatternA)

	id, err := slab.Share(consumer)
	if err != nil {
		sys.UartWriteString(tag + "FAIL basic share: Share: " + err.Error() + "\n")
		return false
	}
	if !waitRelease(id) {
		return false
	}

	// shareprobe writes PatternB before releasing — verify bidirectional mapping.
	b := slab.Bytes()
	for i, v := range b {
		if v != sharepayload.PatternB {
			sys.UartWriteString(tag + "FAIL basic share: byte[" + sys.Itoa(int64(i)) +
				"]=0x" + sys.Hex64(uint64(v)) + " want PatternB\n")
			return false
		}
	}
	sys.UartWriteString(tag + "PASS basic share\n")
	return true
}

// --- Stage 2: sub-range share -----------------------------------------------

func testSubRangeShare(consumer transfer.ShepherdID) bool {
	slab, err := transfer.Allocate(2*transfer.PageSize, 0)
	if err != nil {
		sys.UartWriteString(tag + "FAIL sub-range share: Allocate: " + err.Error() + "\n")
		return false
	}
	defer slab.Release()
	fillSlab(slab, sharepayload.PatternA)

	id, err := slab.ShareRange(consumer, sharepayload.SubRangeOffset, sharepayload.SubRangeLength)
	if err != nil {
		sys.UartWriteString(tag + "FAIL sub-range share: ShareRange: " + err.Error() + "\n")
		return false
	}
	if !waitRelease(id) {
		return false
	}

	// Verify consumer's writes are visible in the exact sub-range.
	b := slab.Bytes()
	for i := sharepayload.SubRangeOffset; i < sharepayload.SubRangeOffset+sharepayload.SubRangeLength; i++ {
		if b[i] != sharepayload.PatternB {
			sys.UartWriteString(tag + "FAIL sub-range share: slab[" + sys.Itoa(int64(i)) +
				"]=0x" + sys.Hex64(uint64(b[i])) + " want PatternB\n")
			return false
		}
	}
	sys.UartWriteString(tag + "PASS sub-range share\n")
	return true
}

// --- Stage 3: bidirectional write verification ------------------------------

// testBidirectionalWrite is an explicit named stage for the DoD §2 check:
// writes through the consumer's bytes view propagate to the sender's Slab view.
// This duplicates what testBasicShare already proves, surfacing it as a
// distinct log line for DoD traceability.
func testBidirectionalWrite(consumer transfer.ShepherdID) bool {
	slab, err := transfer.Allocate(transfer.PageSize, 0)
	if err != nil {
		sys.UartWriteString(tag + "FAIL bidirectional write: Allocate: " + err.Error() + "\n")
		return false
	}
	defer slab.Release()
	fillSlab(slab, sharepayload.PatternA)

	id, err := slab.Share(consumer)
	if err != nil {
		sys.UartWriteString(tag + "FAIL bidirectional write: Share: " + err.Error() + "\n")
		return false
	}
	if !waitRelease(id) {
		return false
	}

	b := slab.Bytes()
	for i, v := range b {
		if v != sharepayload.PatternB {
			sys.UartWriteString(tag + "FAIL bidirectional write: byte[" + sys.Itoa(int64(i)) +
				"]=0x" + sys.Hex64(uint64(v)) + " want PatternB\n")
			return false
		}
	}
	sys.UartWriteString(tag + "PASS bidirectional write\n")
	return true
}

// --- Stage 4: multi-share isolation -----------------------------------------

// testMultiShareIsolation shares two non-overlapping page-sized ranges of the
// same Slab with shareprobe. The shares are issued sequentially — share2 is
// only sent after share1 has been released and the isolation invariant verified.
// This prevents shareprobe from writing PatternB to page2 before sharetest can
// observe page2 still containing PatternA.
//
// Sequence:
//
//  1. Share page0 → shareprobe writes PatternB to page0 → releases share1.
//  2. Verify page1 still PatternA (share1's scope was page0 only).
//  3. Share page1 → shareprobe writes PatternB to page1 → releases share2.
//  4. Verify page1 is now PatternB.
func testMultiShareIsolation(consumer transfer.ShepherdID) bool {
	slab, err := transfer.Allocate(2*transfer.PageSize, 0)
	if err != nil {
		sys.UartWriteString(tag + "FAIL multi-share: Allocate: " + err.Error() + "\n")
		return false
	}
	defer slab.Release()
	fillSlab(slab, sharepayload.PatternA)

	// Phase 1: share only page0.
	id1, err := slab.ShareRange(consumer, 0, transfer.PageSize)
	if err != nil {
		sys.UartWriteString(tag + "FAIL multi-share: ShareRange#1: " + err.Error() + "\n")
		return false
	}
	if !waitRelease(id1) {
		return false
	}

	// After share1 is released, page1 must still be PatternA (isolation).
	b := slab.Bytes()
	for i := transfer.PageSize; i < 2*transfer.PageSize; i++ {
		if b[i] != sharepayload.PatternA {
			sys.UartWriteString(tag + "FAIL multi-share: page1[" + sys.Itoa(int64(i)) +
				"] corrupted after page0 Release: 0x" + sys.Hex64(uint64(b[i])) + "\n")
			return false
		}
	}

	// Phase 2: now share page1.
	id2, err := slab.ShareRange(consumer, transfer.PageSize, transfer.PageSize)
	if err != nil {
		sys.UartWriteString(tag + "FAIL multi-share: ShareRange#2: " + err.Error() + "\n")
		return false
	}
	if !waitRelease(id2) {
		return false
	}

	for i := transfer.PageSize; i < 2*transfer.PageSize; i++ {
		if b[i] != sharepayload.PatternB {
			sys.UartWriteString(tag + "FAIL multi-share: page1[" + sys.Itoa(int64(i)) +
				"] not PatternB after share2 Release: 0x" + sys.Hex64(uint64(b[i])) + "\n")
			return false
		}
	}
	sys.UartWriteString(tag + "PASS multi-share isolation\n")
	return true
}

// --- Stage 5: chained share (A → B → C) ------------------------------------

// testChainShare exercises the DoD §4 chained-share path end-to-end.
//
// test-fixture-http (A) allocates a Slab, fills PatternA, and shares it with
// sharetest (B, this shepherd). Sharetest reshares the pages to shareprobe (C)
// via Share.Reshare. Shareprobe verifies PatternA, writes PatternB, and
// releases. Sharetest confirms the reshare release arrives, then releases back
// to test-fixture-http. test-fixture-http verifies PatternB and prints its
// own PASS line; sharetest prints its PASS line once the Release is sent.
//
// Expected serial log:
//
//	[sharetest] PASS chain share (A→B→C)
//	[test-fixture-http] PASS chain A→B→C
func testChainShare(fixtureSID, probeSID transfer.ShepherdID) bool {
	// Trigger test-fixture-http (A) to allocate + share pages back to us.
	chainMsg := ipc.EncodeChainReq(int16(os.Getpid()))
	if err := uring.Send(int(fixtureSID), &chainMsg); err != nil {
		sys.UartWriteString(tag + "FAIL chain: SendChainReq: " + err.Error() + "\n")
		return false
	}

	// Receive the Share from test-fixture-http. Blocks until A's dispatcher
	// processes the ChainReq and calls slab.Share(us).
	sh, err := transfer.ReceiveShare(fixtureSID)
	if err != nil {
		sys.UartWriteString(tag + "FAIL chain: ReceiveShare: " + err.Error() + "\n")
		return false
	}

	// Verify PatternA is intact before resharing.
	inb := sh.AsBytes()
	for i, v := range inb {
		if v != sharepayload.PatternA {
			sys.UartWriteString(tag + "FAIL chain: incoming byte[" + sys.Itoa(int64(i)) +
				"]=0x" + sys.Hex64(uint64(v)) + " want PatternA\n")
			_ = sh.Release()
			return false
		}
	}

	// Reshare to shareprobe (C) using the same physical frames.
	reshareID, err := sh.Reshare(probeSID)
	if err != nil {
		sys.UartWriteString(tag + "FAIL chain: Reshare: " + err.Error() + "\n")
		_ = sh.Release()
		return false
	}

	// Wait for shareprobe (C) to write PatternB and release.
	if !waitRelease(reshareID) {
		_ = sh.Release()
		return false
	}

	// Release back to test-fixture-http (A). Its releaseHook verifies PatternB.
	if err := sh.Release(); err != nil {
		sys.UartWriteString(tag + "FAIL chain: Release: " + err.Error() + "\n")
		return false
	}

	sys.UartWriteString(tag + "PASS chain share (A→B→C)\n")
	return true
}

func main() { MazarinMain() }
