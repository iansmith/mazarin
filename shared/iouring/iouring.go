// Package iouring defines the shared-memory ring structures for io_uring-style
// async I/O between userspace shepherds and the kernel. The ring layout fits in
// a single 4KB page with cache-line-padded headers to avoid false sharing on
// multicore.
//
// Linux io_uring nomenclature is used throughout (SQEntry, CQEntry, etc.) with
// Go-style identifiers.
package iouring

// CacheLineSize is the padding unit for ring headers. SQ and CQ headers are
// each padded to a full cache line to prevent false sharing between the kernel
// (which writes SQHead/CQTail) and userspace (which writes SQTail/CQHead).
const CacheLineSize = 64

// Ring capacities. CQ is 2x SQ following the Linux default — the kernel may
// produce more completions than there are outstanding submissions (e.g., error
// CQEs alongside normal completions).
const (
	SQCapacity = 32
	CQCapacity = 64
	SQMask     = SQCapacity - 1
	CQMask     = CQCapacity - 1
)

// Opcodes for SQEntry.Opcode, matching Linux IORING_OP_* naming.
const (
	IOUringOpNop   uint8 = 0
	IOUringOpRead  uint8 = 1
	IOUringOpWrite uint8 = 2
)

// SQEntry is a submission queue entry. Userspace writes these to the SQ ring
// to request I/O operations. 40 bytes, matching the Linux io_uring_sqe layout
// conceptually.
//
// FD is a device index (0 = block device), not a file descriptor.
// Off is the starting LBA (sector number).
// Addr is the DMA buffer VA (must be in a MAZARIN_CONTIGUOUS clump).
// Len is the number of sectors.
// UserData is opaque — returned unchanged in the corresponding CQEntry.
type SQEntry struct {
	Opcode   uint8
	Flags    uint8
	Ioprio   uint16
	FD       int32
	Off      uint64
	Addr     uint64
	Len      uint32
	Pad0     uint32
	UserData uint64
}

// CQEntry is a completion queue entry. The kernel IRQ top-half writes these to
// the CQ ring when I/O completes. 16 bytes, matching Linux io_uring_cqe.
//
// UserData is copied from the corresponding SQEntry.
// Res is the result: bytes transferred on success, or negative errno on error.
type CQEntry struct {
	UserData uint64
	Res      int32
	Flags    uint32
}

// IORing is the shared-memory ring page layout. Exactly 4096 bytes.
//
// Memory layout:
//
//	0x000: SQ header (padded to 64 bytes)
//	0x040: CQ header (padded to 64 bytes)
//	0x080: SQEntries[32] — 32 × 40 = 1280 bytes
//	0x580: CQEntries[64] — 64 × 16 = 1024 bytes
//	0x980: unused (1664 bytes padding to page boundary)
//
// Producer/consumer discipline (SPSC per ring):
//
//	SQ: userspace writes SQTail (producer), kernel writes SQHead (consumer)
//	CQ: kernel writes CQTail (producer), userspace writes CQHead (consumer)
type IORing struct {
	// SQ header — cache-line 0
	SQHead     uint32 // kernel advances after consuming SQEs
	SQTail     uint32 // userspace advances after writing SQEs
	SQRingMask uint32 // SQCapacity - 1
	SQCount    uint32 // SQCapacity
	_sqPad     [48]byte

	// CQ header — cache-line 1
	CQHead     uint32 // userspace advances after consuming CQEs
	CQTail     uint32 // kernel advances after writing CQEs
	CQRingMask uint32 // CQCapacity - 1
	CQCount    uint32 // CQCapacity
	CQFlags    uint32 // overflow counter (low bits)
	_cqPad     [44]byte

	// Entry arrays
	SQEntries [SQCapacity]SQEntry
	CQEntries [CQCapacity]CQEntry
}

// Compile-time size checks.
var _ [40]byte = [unsafe_Sizeof_SQEntry]byte{}
var _ [16]byte = [unsafe_Sizeof_CQEntry]byte{}

// These constants are used for compile-time size verification.
// They must match unsafe.Sizeof(SQEntry{}) and unsafe.Sizeof(CQEntry{}).
const (
	unsafe_Sizeof_SQEntry = 40
	unsafe_Sizeof_CQEntry = 16
)
