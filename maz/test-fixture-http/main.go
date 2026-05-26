// test-fixture-http is the A-node (allocator) in the MAZ-53 chained-share
// boot test (A → B → C: test-fixture-http → sharetest → shareprobe).
//
// Protocol: on receiving a ProtoChainReq from any shepherd, allocate a
// 2-page Slab, fill with PatternA, and share the whole Slab back to the
// requester. When the requester returns a Release (after having reshared
// with shareprobe and confirmed PatternB propagation), verify PatternB is
// visible in the Slab and print PASS.
//
// Expected output in serial log:
//
//	[test-fixture-http] PASS chain A→B→C
//
// Any failure prints `[test-fixture-http] FAIL ...`.
package main

import (
	"sync"

	"mazzy/maz/internal/sharepayload"
	"mazzy/mazarin/mazhost"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/transfer"
	"mazzy/mazarin/uring"
	"mazzy/shared/ipc"
)

const tag = "[test-fixture-http] "

// pendingSlabs maps the ShareID (assigned by slab.Share) to the Slab still
// owned by test-fixture-http. The releaseHook looks up and verifies PatternB.
var (
	pendingMu    sync.Mutex
	pendingSlabs = make(map[transfer.ShareID]*transfer.Slab)
)

func init() { mazhost.PinEntry(MazarinMain, nil) }

//go:noinline
func MazarinMain() {
	sys.UartWriteString(tag + "start\n")

	disp := uring.NewDispatcher()

	// Sender side: handle ProtoShareRelease IPCs from sharetest (B) after
	// sharetest has finished the B→C leg and releases back to us.
	transfer.RegisterShareRelease(disp)
	transfer.SetReleaseHook(func(id transfer.ShareID) {
		pendingMu.Lock()
		slab, ok := pendingSlabs[id]
		if ok {
			delete(pendingSlabs, id)
		}
		pendingMu.Unlock()
		if !ok {
			sys.UartWriteString(tag + "FAIL: Release for unknown ShareID\n")
			return
		}
		// Verify PatternB — shareprobe (C) wrote it through the chain, and both
		// sharetest's and our mappings point to the same physical frames.
		b := slab.Bytes()
		for i, v := range b {
			if v != sharepayload.PatternB {
				sys.UartWriteString(tag + "FAIL chain A→B→C: byte[" + sys.Itoa(int64(i)) +
					"]=0x" + sys.Hex64(uint64(v)) + " want PatternB\n")
				_ = slab.Release()
				return
			}
		}
		_ = slab.Release()
		sys.UartWriteString(tag + "PASS chain A→B→C\n")
	})

	// Handle ProtoChainReq IPCs from sharetest (B).
	disp.OnFunc(ipc.ProtoChainReq, decodeChainReq, handleChainReq)

	disp.Start()
	sys.SetReady(true)
	select {}
}

type chainReqMsg struct {
	senderSID transfer.ShepherdID
}

func decodeChainReq(msg *ipc.UringIPCMsg) any {
	return chainReqMsg{senderSID: transfer.ShepherdID(msg.SenderSID)}
}

// handleChainReq is called by the dispatcher when a ProtoChainReq arrives.
// It allocates a 2-page Slab, fills with PatternA, and shares the whole
// Slab back to the requester (sharetest). The ShareID is stored in
// pendingSlabs so the releaseHook can verify PatternB when sharetest
// returns the pages after the full A→B→C round-trip.
func handleChainReq(v any) {
	req := v.(chainReqMsg)

	slab, err := transfer.Allocate(2*transfer.PageSize, 0)
	if err != nil {
		sys.UartWriteString(tag + "FAIL: Allocate: " + err.Error() + "\n")
		return
	}

	b := slab.Bytes()
	for i := range b {
		b[i] = sharepayload.PatternA
	}

	id, err := slab.Share(req.senderSID)
	if err != nil {
		sys.UartWriteString(tag + "FAIL: Share: " + err.Error() + "\n")
		_ = slab.Release()
		return
	}

	pendingMu.Lock()
	pendingSlabs[id] = slab
	pendingMu.Unlock()
}

func main() { MazarinMain() }
