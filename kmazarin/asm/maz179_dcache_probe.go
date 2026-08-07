package asm

// [MAZ-179 tier-24 / MAZ-187 detection — NOT FOR MERGE] DC IVAC
// neighbor-discard witness.
//
// MAZ-187 has two halves. The missed-tail half (a range whose start is not
// 64-aligned never reached its final line) is already fixed in this tree by
// the `AND $~63` in both range ops — though note that fix lives only in
// scratch commit 696db63d and is NOT on master.
//
// The half that is still live and unaudited is the neighbor-discard
// contract: `DC IVAC` operates on whole 64-byte lines, so invalidating a
// range whose start or end is not line-aligned DISCARDS whatever else
// shares those head/tail lines. If a DMA buffer abuts live non-DMA data —
// a Go heap object, GC metadata, a neighbouring struct field — that data
// silently reverts to whatever RAM holds, which is precisely the
// "memory reappears stale or zeroed" shape MAZ-179 keeps producing.
//
// These counters are incremented from the assembly in mmio_arm64.s before
// the alignment happens, so they see the caller's true request:
//
//	DCacheInvTotal     — every InvalidateDCacheRange call (denominator)
//	DCacheInvUnaligned — calls whose start OR end is not 64-aligned, i.e.
//	                     calls that necessarily discard neighbour bytes
//	DCacheInvFirstVA/Len — the first such request, to identify the caller
//
// amd64 keeps plain Go zero values here: its mmio_amd64.s range ops are
// no-op stubs (x86 is cache-coherent for DMA), so there is nothing to count.
var (
	DCacheInvTotal     uint64
	DCacheInvUnaligned uint64
	DCacheInvFirstVA   uint64
	DCacheInvFirstLen  uint64
)
