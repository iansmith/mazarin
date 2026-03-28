// Package fsipc defines the IPC protocol between the linux shepherd and
// the fs shepherd. The linux shepherd sends file operation requests via a
// shared-memory ring buffer; the fs shepherd processes them and replies
// via a second ring buffer. A shared data page transfers bulk data
// (read/write buffers, stat results, directory entries).
//
// Setup (handshake):
//  1. linux creates request ring, maps into fs → sends NotifyInit
//  2. fs creates response ring + data page, maps into linux → sends NotifyReady
//  3. linux opens response ring + data page → IPC channel is live
//
// Request/response flow:
//  1. linux pushes Request to request ring → sends NotifyRequest
//  2. fs pops Request, processes, pushes Response → sends NotifyResponse
//  3. linux pops Response, reads data page if needed
package fsipc

// Notification codes for mailbox communication.
const (
	NotifyInit     int64 = 10 // linux→fs: IPC handshake (request ring addr)
	NotifyReady    int64 = 11 // fs→linux: IPC ready (response ring addr)
	NotifyRequest  int64 = 12 // linux→fs: request(s) in ring
	NotifyResponse int64 = 13 // fs→linux: response(s) in ring
)

// Ring buffer parameters.
const (
	SlotSize  uint32 = 128 // bytes per message
	SlotCount uint32 = 16  // number of slots (power of 2)
	DataPages        = 1   // shared data area pages (4KB total)
)

// Operation codes.
const (
	OpHandshake uint16 = 0  // initial handshake (no-op body)
	OpOpen      uint16 = 1  // open file → handle
	OpClose     uint16 = 2  // close handle
	OpRead      uint16 = 3  // read from handle → data area
	OpWrite     uint16 = 4  // write data area → handle
	OpStat      uint16 = 5  // stat path → data area (128-byte stat buf)
	OpFstat     uint16 = 6  // stat handle → data area
	OpMkdir     uint16 = 7  // create directory
	OpRemove    uint16 = 8  // remove file/dir
	OpRename    uint16 = 9  // rename (old\0new in Path)
	OpReadDir   uint16 = 10 // read dirents → data area (linux_dirent64 format)
	OpAccess    uint16 = 11 // check file existence
	OpSetMode   uint16 = 12 // chmod
	OpSetTimes  uint16 = 13 // utimens
	OpSync      uint16 = 14 // flush metadata
	OpResolve   uint16 = 15 // resolve path → isDir + size (for chdir)
)

// Request is the 128-byte wire format for an IPC request from linux to fs.
//
// Layout:
//
//	[0:8]    ID      — unique request ID
//	[8:10]   Op      — operation code
//	[10:12]  Pad0
//	[12:16]  Flags   — open flags (O_RDONLY, O_CREAT, etc.)
//	[16:20]  Mode    — permission bits
//	[20:24]  Handle  — fs-side handle (from OpOpen)
//	[24:32]  Arg0    — offset, startIdx, etc.
//	[32:40]  Arg1    — count, etc.
//	[40:44]  DataLen — bytes written to shared data area (for OpWrite)
//	[44:48]  Pad1
//	[48:128] Path    — null-terminated path (80 bytes max)
type Request struct {
	ID      uint64
	Op      uint16
	Pad0    uint16
	Flags   uint32
	Mode    uint32
	Handle  uint32
	Arg0    uint64
	Arg1    uint64
	DataLen uint32
	Pad1    uint32
	Path    [80]byte
}

// Response is the 128-byte wire format for an IPC response from fs to linux.
//
// Layout:
//
//	[0:8]    ID      — matching request ID
//	[8:12]   Err     — 0 on success, negative errno on error
//	[12:16]  Pad0
//	[16:24]  Result0 — primary result (handle, bytes read, etc.)
//	[24:32]  Result1 — secondary result (ftype<<32 | size, etc.)
//	[32:36]  DataLen — bytes in shared data area (for OpRead, OpStat, etc.)
//	[36:40]  Pad1
//	[40:128] Data    — inline data (88 bytes, for small results)
type Response struct {
	ID      uint64
	Err     int32
	Pad0    uint32
	Result0 uint64
	Result1 uint64
	DataLen uint32
	Pad1    uint32
	Data    [88]byte
}

// PathString extracts a null-terminated path from a byte array.
func PathString(path []byte) string {
	for i, b := range path {
		if b == 0 {
			return string(path[:i])
		}
	}
	return string(path)
}

// SetPath copies a Go string into a fixed-size path buffer with null termination.
func SetPath(dst []byte, s string) {
	n := len(s)
	if n >= len(dst) {
		n = len(dst) - 1
	}
	copy(dst[:n], s)
	dst[n] = 0
}
