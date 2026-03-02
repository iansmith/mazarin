// Package memblob provides a MemBlob abstraction over mmap'd page regions.
// MemBlobs are contiguous page-aligned memory regions that can be read from
// and written to via copy semantics, suitable for shared-memory IPC.
package memblob

import (
	"syscall"
	"unsafe"

	merror "mazzy/mazarin/error"
)

const pageSize = 4096

// MemBlob is a page-aligned memory region that supports read/write by copy.
type MemBlob interface {
	Read(p []byte) (uint64, *merror.Error)
	ReadWithSize(p []byte, numBytes uint64) (uint64, *merror.Error)
	Write(p []byte) (uint64, *merror.Error)
	WriteWithSize(p []byte, numBytes uint64) (uint64, *merror.Error)
}

type memBlob struct {
	pagesStart uintptr
	numPages   uint8
	readOnly   bool
}

// New allocates a MemBlob backed by the given number of anonymous pages.
// pages must be 1..255. The returned MemBlob is read-write.
func New(pages uint8) (MemBlob, *merror.Error) {
	if pages == 0 {
		return nil, merror.ErrInvalidSize
	}

	size := uintptr(pages) * pageSize

	// mmap(NULL, size, PROT_READ|PROT_WRITE, MAP_PRIVATE|MAP_ANONYMOUS, -1, 0)
	addr, _, errno := syscall.RawSyscall6(
		syscall.SYS_MMAP,
		0,
		size,
		syscall.PROT_READ|syscall.PROT_WRITE,
		syscall.MAP_PRIVATE|syscall.MAP_ANONYMOUS,
		^uintptr(0), // fd = -1
		0,
	)
	if errno != 0 || int64(addr) < 0 {
		return nil, merror.ErrMmapFailed
	}

	return &memBlob{
		pagesStart: addr,
		numPages:   pages,
		readOnly:   false,
	}, nil
}

func (m *memBlob) blobSize() uint64 {
	return uint64(m.numPages) * pageSize
}

func (m *memBlob) asSlice() []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(m.pagesStart)), m.blobSize())
}

// Read copies from the blob into p. Returns the number of bytes copied.
func (m *memBlob) Read(p []byte) (uint64, *merror.Error) {
	if p == nil {
		return 0, merror.ErrNilBuffer
	}
	n := copy(p, m.asSlice())
	return uint64(n), nil
}

// ReadWithSize copies up to numBytes from the blob into p.
func (m *memBlob) ReadWithSize(p []byte, numBytes uint64) (uint64, *merror.Error) {
	if p == nil {
		return 0, merror.ErrNilBuffer
	}
	bs := m.blobSize()
	if numBytes > bs {
		numBytes = bs
	}
	if numBytes > uint64(len(p)) {
		numBytes = uint64(len(p))
	}
	n := copy(p[:numBytes], m.asSlice()[:numBytes])
	return uint64(n), nil
}

// Write copies from p into the blob. Returns the number of bytes copied.
func (m *memBlob) Write(p []byte) (uint64, *merror.Error) {
	if m.readOnly {
		return 0, merror.ErrReadOnlyBlob
	}
	if p == nil {
		return 0, merror.ErrNilBuffer
	}
	n := copy(m.asSlice(), p)
	return uint64(n), nil
}

// WriteWithSize copies up to numBytes from p into the blob.
func (m *memBlob) WriteWithSize(p []byte, numBytes uint64) (uint64, *merror.Error) {
	if m.readOnly {
		return 0, merror.ErrReadOnlyBlob
	}
	if p == nil {
		return 0, merror.ErrNilBuffer
	}
	bs := m.blobSize()
	if numBytes > bs {
		numBytes = bs
	}
	if numBytes > uint64(len(p)) {
		numBytes = uint64(len(p))
	}
	n := copy(m.asSlice()[:numBytes], p[:numBytes])
	return uint64(n), nil
}
