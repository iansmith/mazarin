package main

import (
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
)

// Debug counters bumped via atomic.AddUint64.
var (
	dbgRxNoDispatcher uint64 // RX arrived before stack was Attach()ed
	dbgTxAllocFail    uint64 // AllocTx returned nil (pool / hard watermark)
	dbgTxChanFull     uint64 // txChan full, packet dropped
	dbgTxOversize     uint64 // PacketBuffer > pageSize - VirtIONetHdrSize
)

// runRxDispatcher drains RecvChan and hands each RxEnvelope to the
// rawEndpoint. gvisor's NetworkDispatcher.DeliverNetworkPacket is
// concurrent-safe, so a worker fan-out is a drop-in if profiling
// warrants it. Single-threaded until then.
func runRxDispatcher() {
	for env := range globalRecvChan {
		globalRawEP.deliverRx(env)
	}
}
