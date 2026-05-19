package main

import (
	"sync/atomic"
	"time"

	"gvisor.dev/gvisor/pkg/tcpip/stack"

	"mazzy/mazarin/linksurface"
	"mazzy/shared/constants"
)

// pageSize is the host Allocator's page size. Pulled from shared/constants
// rather than maz/net/host (host residency forbids linking host code into
// a .maz plugin).
const pageSize = constants.PAGE_SIZE

// txChanBuffer sizes the plugin→host TxChan. Drop-on-full at the
// rawEndpoint level when it overflows.
const txChanBuffer = 64

// Set by MazarinShepherd, populated by buildStack. RX dispatcher reads
// both. Single-threaded init means no synchronization needed.
var (
	globalRecvChan chan linksurface.RxEnvelope
	globalTxChan   chan linksurface.TxEnvelope
	globalRawEP    *rawEndpoint
	globalStack    *stack.Stack // set by MazarinMain after buildStack; read by NetIPC handlers
)

// Debug counters bumped via atomic.AddUint64.
var (
	dbgRxNoDispatcher uint64 // RX arrived before stack was Attach()ed
	dbgRxPanic        uint64 // deliverRx panicked; goroutine respawned
	dbgTxAllocFail    uint64 // AllocTx returned nil (pool / hard watermark)
	dbgTxChanFull     uint64 // txChan full, packet dropped
	dbgTxOversize     uint64 // PacketBuffer > pageSize - VirtIONetHdrSize
)

// runRxDispatcher drains RecvChan and hands each RxEnvelope to the
// rawEndpoint. A deferred recover catches panics from deliverRx (gvisor
// parsing bugs on adversarial input have bitten us on the @go branch);
// on panic we respawn after a short backoff so a recurring panic
// becomes a counter-flood rather than a CPU spin.
func runRxDispatcher() {
	defer func() {
		if r := recover(); r != nil {
			atomic.AddUint64(&dbgRxPanic, 1)
			time.Sleep(100 * time.Millisecond)
			go runRxDispatcher()
		}
	}()
	for env := range globalRecvChan {
		globalRawEP.deliverRx(env)
	}
}
