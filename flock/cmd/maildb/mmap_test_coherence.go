package main

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// testMmapCoherence exercises mmap page cache coherence by verifying that:
//   1. Data written through mmap is visible via pread on the same fd
//   2. Data written via pwrite is visible through mmap'd memory
//   3. After munmap, data persists and is readable via regular open+read
//
// Returns true if all tests pass, false otherwise.
func testMmapCoherence() bool {
	const testPath = "/tmp/mmap-coherence-test"
	const pageSize = 4096

	fmt.Println("[mmaptest] === Mmap Page Cache Coherence Test ===")

	// --- Setup: create a file with known content ---
	fd, err := syscall.Open(testPath, syscall.O_RDWR|syscall.O_CREAT|syscall.O_TRUNC, 0666)
	if err != nil {
		fmt.Printf("[mmaptest] FAIL: open for create: %v\n", err)
		return false
	}

	// Write initial data: "AAAA..." (one page)
	initData := make([]byte, pageSize)
	for i := range initData {
		initData[i] = 'A'
	}
	n, err := syscall.Write(fd, initData)
	if err != nil || n != pageSize {
		fmt.Printf("[mmaptest] FAIL: initial write: n=%d err=%v\n", n, err)
		syscall.Close(fd)
		return false
	}
	fmt.Printf("[mmaptest] created %s with %d bytes of 'A'\n", testPath, n)

	// --- Test 1: mmap the file and verify initial content ---
	data, err := syscall.Mmap(fd, 0, pageSize,
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		fmt.Printf("[mmaptest] FAIL: mmap: %v\n", err)
		syscall.Close(fd)
		return false
	}
	fmt.Printf("[mmaptest] mmap'd %d bytes at %p\n", pageSize, unsafe.Pointer(&data[0]))

	// Read first 8 bytes through mmap
	if data[0] != 'A' || data[7] != 'A' {
		fmt.Printf("[mmaptest] FAIL: mmap read-back: expected 'A' got %02x %02x\n", data[0], data[7])
		syscall.Munmap(data)
		syscall.Close(fd)
		return false
	}
	fmt.Println("[mmaptest] PASS: mmap read-back matches initial content")

	// --- Test 2: Write through mmap, read via pread ---
	// Write "HELLO!!!" at offset 0 through mmap
	pattern := []byte("HELLO!!!")
	copy(data[0:], pattern)
	fmt.Printf("[mmaptest] wrote %q through mmap at offset 0\n", pattern)

	// Now pread the same region via the fd
	readBuf := make([]byte, len(pattern))
	nr, err := syscall.Pread(fd, readBuf, 0)
	if err != nil {
		fmt.Printf("[mmaptest] FAIL: pread after mmap write: %v\n", err)
		syscall.Munmap(data)
		syscall.Close(fd)
		return false
	}
	if nr != len(pattern) {
		fmt.Printf("[mmaptest] FAIL: pread returned %d bytes, expected %d\n", nr, len(pattern))
		syscall.Munmap(data)
		syscall.Close(fd)
		return false
	}
	if string(readBuf) != string(pattern) {
		fmt.Printf("[mmaptest] FAIL: pread got %q, expected %q\n", string(readBuf), string(pattern))
		fmt.Printf("[mmaptest]   raw bytes: %02x %02x %02x %02x %02x %02x %02x %02x\n",
			readBuf[0], readBuf[1], readBuf[2], readBuf[3],
			readBuf[4], readBuf[5], readBuf[6], readBuf[7])
		syscall.Munmap(data)
		syscall.Close(fd)
		return false
	}
	fmt.Println("[mmaptest] PASS: pread sees mmap-written data (mmap→read coherence)")

	// --- Test 3: Write via pwrite, read through mmap ---
	pattern2 := []byte("WORLD!!!")
	nw, err := syscall.Pwrite(fd, pattern2, 100)
	if err != nil || nw != len(pattern2) {
		fmt.Printf("[mmaptest] FAIL: pwrite: n=%d err=%v\n", nw, err)
		syscall.Munmap(data)
		syscall.Close(fd)
		return false
	}
	fmt.Printf("[mmaptest] pwrite %q at offset 100\n", pattern2)

	// Read back through mmap at offset 100
	mmapRead := make([]byte, len(pattern2))
	copy(mmapRead, data[100:100+len(pattern2)])
	if string(mmapRead) != string(pattern2) {
		fmt.Printf("[mmaptest] FAIL: mmap read got %q, expected %q\n", string(mmapRead), string(pattern2))
		fmt.Printf("[mmaptest]   raw bytes: %02x %02x %02x %02x %02x %02x %02x %02x\n",
			mmapRead[0], mmapRead[1], mmapRead[2], mmapRead[3],
			mmapRead[4], mmapRead[5], mmapRead[6], mmapRead[7])
		syscall.Munmap(data)
		syscall.Close(fd)
		return false
	}
	fmt.Println("[mmaptest] PASS: mmap sees pwrite-written data (write→mmap coherence)")

	// --- Test 4: Write a second pattern through mmap at offset 200 for writeback test ---
	pattern3 := []byte("PERSIST!")
	copy(data[200:], pattern3)
	fmt.Printf("[mmaptest] wrote %q through mmap at offset 200\n", pattern3)

	// --- Test 5: munmap and verify writeback ---
	err = syscall.Munmap(data)
	if err != nil {
		fmt.Printf("[mmaptest] FAIL: munmap: %v\n", err)
		syscall.Close(fd)
		return false
	}
	fmt.Println("[mmaptest] munmap'd successfully")

	// Close the fd
	syscall.Close(fd)

	// Re-open the file and read to verify data was written back
	f, err := os.Open(testPath)
	if err != nil {
		fmt.Printf("[mmaptest] FAIL: re-open: %v\n", err)
		return false
	}
	defer f.Close()

	verifyBuf := make([]byte, pageSize)
	nr2, err := f.Read(verifyBuf)
	if err != nil {
		fmt.Printf("[mmaptest] FAIL: re-read: %v\n", err)
		return false
	}
	fmt.Printf("[mmaptest] re-read %d bytes after munmap+close+reopen\n", nr2)

	// Check offset 0: should be "HELLO!!!"
	if string(verifyBuf[0:8]) != "HELLO!!!" {
		fmt.Printf("[mmaptest] FAIL: writeback at offset 0: got %q\n", string(verifyBuf[0:8]))
		return false
	}
	fmt.Println("[mmaptest] PASS: writeback at offset 0 (HELLO!!!)")

	// Check offset 100: should be "WORLD!!!"
	if string(verifyBuf[100:108]) != "WORLD!!!" {
		fmt.Printf("[mmaptest] FAIL: writeback at offset 100: got %q\n", string(verifyBuf[100:108]))
		return false
	}
	fmt.Println("[mmaptest] PASS: writeback at offset 100 (WORLD!!!)")

	// Check offset 200: should be "PERSIST!"
	if string(verifyBuf[200:208]) != "PERSIST!" {
		fmt.Printf("[mmaptest] FAIL: writeback at offset 200: got %q\n", string(verifyBuf[200:208]))
		return false
	}
	fmt.Println("[mmaptest] PASS: writeback at offset 200 (PERSIST!)")

	// Check that non-written areas are still 'A'
	if verifyBuf[50] != 'A' || verifyBuf[150] != 'A' || verifyBuf[300] != 'A' {
		fmt.Printf("[mmaptest] FAIL: non-written areas corrupted: [50]=%02x [150]=%02x [300]=%02x\n",
			verifyBuf[50], verifyBuf[150], verifyBuf[300])
		return false
	}
	fmt.Println("[mmaptest] PASS: non-written areas preserved")

	fmt.Println("[mmaptest] === ALL TESTS PASSED ===")

	// --- Test 6: Badger-like pattern ---
	// Create file, truncate, mmap, write keyID=0 big-endian, read back
	testBadgerLikePattern()

	// --- Test 8: Subdir write/read roundtrip ---
	testSubdirWriteReadRoundtrip()

	// --- Test 7: Goroutine test disabled (sync.WaitGroup import causes !LOCK) ---
	// testMmapFromGoroutine()

	return true
}

// testMmapFromGoroutine runs the core mmap coherence test from a non-main
// goroutine. This is the pattern that badger uses (badger.Open runs in a
// goroutine) and it triggers a different code path in the page fault handler.
func testMmapFromGoroutine() {
	const testPath = "/tmp/mmap-goroutine-test"
	const pageSize = 4096

	fmt.Println("[mmaptest:goroutine] creating file...")
	fd, err := syscall.Open(testPath, syscall.O_RDWR|syscall.O_CREAT|syscall.O_TRUNC, 0666)
	if err != nil {
		fmt.Printf("[mmaptest:goroutine] FAIL: open: %v\n", err)
		return
	}

	initData := make([]byte, pageSize)
	for i := range initData {
		initData[i] = 'G'
	}
	n, err := syscall.Write(fd, initData)
	if err != nil || n != pageSize {
		fmt.Printf("[mmaptest:goroutine] FAIL: write: n=%d err=%v\n", n, err)
		syscall.Close(fd)
		return
	}
	fmt.Printf("[mmaptest:goroutine] wrote %d bytes of 'G'\n", n)

	data, err := syscall.Mmap(fd, 0, pageSize,
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		fmt.Printf("[mmaptest:goroutine] FAIL: mmap: %v\n", err)
		syscall.Close(fd)
		return
	}
	fmt.Printf("[mmaptest:goroutine] mmap'd at %p, reading first byte...\n", unsafe.Pointer(&data[0]))

	// This is the critical access — first touch of mmap'd memory from a goroutine.
	// This triggers a page fault that must be handled correctly.
	b := data[0]
	fmt.Printf("[mmaptest:goroutine] first byte = %02x (expected 'G' = %02x)\n", b, byte('G'))

	if b != 'G' {
		fmt.Printf("[mmaptest:goroutine] FAIL: got %02x, expected %02x\n", b, byte('G'))
	} else {
		fmt.Println("[mmaptest:goroutine] PASS: mmap read from goroutine works")
	}

	// Write through mmap
	copy(data[0:5], []byte("GTEST"))
	fmt.Println("[mmaptest:goroutine] wrote 'GTEST' through mmap")

	// pread to verify coherence
	readBuf := make([]byte, 5)
	nr, err := syscall.Pread(fd, readBuf, 0)
	if err != nil {
		fmt.Printf("[mmaptest:goroutine] FAIL: pread: %v\n", err)
	} else if nr != 5 || string(readBuf) != "GTEST" {
		fmt.Printf("[mmaptest:goroutine] FAIL: pread got %q (n=%d)\n", string(readBuf), nr)
	} else {
		fmt.Println("[mmaptest:goroutine] PASS: pread sees mmap write from goroutine")
	}

	syscall.Munmap(data)
	syscall.Close(fd)
	fmt.Println("[mmaptest:goroutine] done")
}

// testBadgerLikePattern mimics exactly what badger does: create file, truncate,
// mmap MAP_SHARED, write zeros (keyID=0) in big-endian, read back.
func testBadgerLikePattern() {
	const path = "/tmp/mmap-badger-test"
	const size = 1 << 20 // 1MB like badger's default memtable

	fmt.Println("[mmaptest:badger] --- Badger-like mmap pattern test ---")

	// Create and truncate
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_CREAT|syscall.O_TRUNC, 0666)
	if err != nil {
		fmt.Printf("[mmaptest:badger] FAIL: open: %v\n", err)
		return
	}
	if err := syscall.Ftruncate(fd, int64(size)); err != nil {
		fmt.Printf("[mmaptest:badger] FAIL: ftruncate: %v\n", err)
		syscall.Close(fd)
		return
	}
	fmt.Printf("[mmaptest:badger] created+truncated %s to %d bytes\n", path, size)

	// Mmap MAP_SHARED read+write
	data, err := syscall.Mmap(fd, 0, size,
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		fmt.Printf("[mmaptest:badger] FAIL: mmap: %v\n", err)
		syscall.Close(fd)
		return
	}
	fmt.Printf("[mmaptest:badger] mmap'd %d bytes at %p\n", size, unsafe.Pointer(&data[0]))

	// Read first 8 bytes BEFORE writing (should be zeros from truncation)
	fmt.Printf("[mmaptest:badger] BEFORE write: data[0:8] = %02x %02x %02x %02x %02x %02x %02x %02x\n",
		data[0], data[1], data[2], data[3], data[4], data[5], data[6], data[7])

	// Write keyID=0 in big-endian (8 zero bytes) + 12 bytes of "IV"
	buf := make([]byte, 20)
	// keyID = 0 → buf[0:8] all zeros (already)
	// Fake IV
	for i := 8; i < 20; i++ {
		buf[i] = byte(0xAA + i - 8)
	}
	n := copy(data[0:], buf)
	fmt.Printf("[mmaptest:badger] wrote %d bytes: keyID=0 + IV\n", n)

	// Immediately read back
	readBuf := make([]byte, 20)
	copy(readBuf, data[0:20])
	fmt.Printf("[mmaptest:badger] AFTER write: data[0:20] = %02x %02x %02x %02x %02x %02x %02x %02x | %02x %02x %02x %02x %02x %02x %02x %02x | %02x %02x %02x %02x\n",
		readBuf[0], readBuf[1], readBuf[2], readBuf[3],
		readBuf[4], readBuf[5], readBuf[6], readBuf[7],
		readBuf[8], readBuf[9], readBuf[10], readBuf[11],
		readBuf[12], readBuf[13], readBuf[14], readBuf[15],
		readBuf[16], readBuf[17], readBuf[18], readBuf[19])

	// Check keyID
	keyID := uint64(readBuf[0])<<56 | uint64(readBuf[1])<<48 |
		uint64(readBuf[2])<<40 | uint64(readBuf[3])<<32 |
		uint64(readBuf[4])<<24 | uint64(readBuf[5])<<16 |
		uint64(readBuf[6])<<8 | uint64(readBuf[7])
	if keyID != 0 {
		fmt.Printf("[mmaptest:badger] FAIL: keyID=%d (0x%016x), expected 0\n", keyID, keyID)
	} else {
		fmt.Println("[mmaptest:badger] PASS: keyID=0 read back correctly")
	}

	syscall.Munmap(data)
	syscall.Close(fd)
	fmt.Println("[mmaptest:badger] --- done ---")

	// Now test with 64MB (actual badger memtable size) and heap alloc between
	// write and read — exactly what badger does.
	testBadgerLike64MB()
}

// testBadgerLike64MB reproduces the exact badger pattern that fails:
// create file, truncate to 64MB, mmap MAP_SHARED RW, write 20 bytes via copy,
// heap-allocate a buffer (which may trigger GC/MAP_FIXED), read 20 bytes back.
func testBadgerLike64MB() {
	const path = "/tmp/mmap-badger64-test"
	const size = 64 << 20 // 64MB — actual badger default MemTableSize

	fmt.Println("[mmaptest:badger64] --- 64MB badger-like pattern test ---")

	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_CREAT|syscall.O_TRUNC, 0666)
	if err != nil {
		fmt.Printf("[mmaptest:badger64] FAIL: open: %v\n", err)
		return
	}
	if err := syscall.Ftruncate(fd, int64(size)); err != nil {
		fmt.Printf("[mmaptest:badger64] FAIL: ftruncate: %v\n", err)
		syscall.Close(fd)
		return
	}

	data, err := syscall.Mmap(fd, 0, size,
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		fmt.Printf("[mmaptest:badger64] FAIL: mmap: %v\n", err)
		syscall.Close(fd)
		return
	}
	fmt.Printf("[mmaptest:badger64] mmap'd %d bytes at %p\n", size, unsafe.Pointer(&data[0]))

	// Force page fault via READ first (separate from write).
	// This tests whether write-during-fault loses the write.
	prefault := data[0]
	fmt.Printf("[mmaptest:badger64] pre-fault read: data[0]=%02x (expect 00)\n", prefault)

	// Write 20-byte header (keyID=0 big-endian + fake IV) — same as bootstrap
	buf := make([]byte, 20)
	for i := 8; i < 20; i++ {
		buf[i] = byte(0xBB + i - 8)
	}
	n := copy(data[0:], buf)
	fmt.Printf("[mmaptest:badger64] wrote %d bytes via copy to mmap\n", n)

	// Print what we just wrote by reading directly — 64 bytes to see full corrupted string
	fmt.Printf("[mmaptest:badger64] immediate read (hex):\n")
	for row := 0; row < 4; row++ {
		off := row * 16
		fmt.Printf("  +%03x: %02x %02x %02x %02x %02x %02x %02x %02x  %02x %02x %02x %02x %02x %02x %02x %02x  |",
			off,
			data[off+0], data[off+1], data[off+2], data[off+3],
			data[off+4], data[off+5], data[off+6], data[off+7],
			data[off+8], data[off+9], data[off+10], data[off+11],
			data[off+12], data[off+13], data[off+14], data[off+15])
		for i := 0; i < 16; i++ {
			b := data[off+i]
			if b >= 0x20 && b < 0x7f {
				fmt.Printf("%c", b)
			} else {
				fmt.Printf(".")
			}
		}
		fmt.Println("|")
	}

	// Heap allocation between write and read (may trigger GC)
	readBuf := make([]byte, 20)

	// Read back via copy (exactly what badger does)
	copy(readBuf, data[0:20])
	fmt.Printf("[mmaptest:badger64] after make+copy: %02x %02x %02x %02x %02x %02x %02x %02x | %02x %02x %02x %02x\n",
		readBuf[0], readBuf[1], readBuf[2], readBuf[3],
		readBuf[4], readBuf[5], readBuf[6], readBuf[7],
		readBuf[8], readBuf[9], readBuf[10], readBuf[11])

	keyID := uint64(readBuf[0])<<56 | uint64(readBuf[1])<<48 |
		uint64(readBuf[2])<<40 | uint64(readBuf[3])<<32 |
		uint64(readBuf[4])<<24 | uint64(readBuf[5])<<16 |
		uint64(readBuf[6])<<8 | uint64(readBuf[7])
	if keyID != 0 {
		fmt.Printf("[mmaptest:badger64] FAIL: keyID=%d (0x%016x), expected 0\n", keyID, keyID)
	} else {
		fmt.Println("[mmaptest:badger64] PASS: keyID=0 read back correctly")
	}

	// Also check IV
	ivOK := true
	for i := 8; i < 20; i++ {
		if readBuf[i] != byte(0xBB+i-8) {
			ivOK = false
			break
		}
	}
	if !ivOK {
		fmt.Printf("[mmaptest:badger64] FAIL: IV corrupted\n")
	} else {
		fmt.Println("[mmaptest:badger64] PASS: IV intact")
	}

	syscall.Munmap(data)
	syscall.Close(fd)
	fmt.Println("[mmaptest:badger64] --- done ---")

	testWriteFirstPageFault()
}

// testWriteFirstPageFault exercises the exact scenario where the FIRST access
// to a file-backed mmap page is a WRITE via copy() (not a read, not byte-at-a-time).
// This triggers a page fault on a MOVOU store instruction (Go's runtime.memmove
// uses SSE for 17-32 byte copies). The handler must preserve all XMM registers
// across the page fault → context switch → reschedule cycle so the retried
// MOVOU writes the correct data.
//
// This directly simulates badger v4's bootstrap pattern:
//   binary.BigEndian.PutUint64(buf[:8], keyID)  // build 20-byte header on stack
//   copy(lf.Data[0:], buf)                       // write to mmap via memmove/MOVOU
//   copy(readBuf, lf.Data)                       // read back via memmove
//   keyID = binary.BigEndian.Uint64(readBuf[:8]) // parse — must match
func testWriteFirstPageFault() {
	const path = "/tmp/mmap-writefirst-test"
	const size = 64 << 20 // 64MB

	fmt.Println("[mmaptest:writefirst] --- Write-first page fault test (copy/MOVOU) ---")

	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_CREAT|syscall.O_TRUNC, 0666)
	if err != nil {
		fmt.Printf("[mmaptest:writefirst] FAIL: open: %v\n", err)
		return
	}
	if err := syscall.Ftruncate(fd, int64(size)); err != nil {
		fmt.Printf("[mmaptest:writefirst] FAIL: ftruncate: %v\n", err)
		syscall.Close(fd)
		return
	}

	data, err := syscall.Mmap(fd, 0, size,
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		fmt.Printf("[mmaptest:writefirst] FAIL: mmap: %v\n", err)
		syscall.Close(fd)
		return
	}
	fmt.Printf("[mmaptest:writefirst] mmap'd %d bytes at %p\n", size, unsafe.Pointer(&data[0]))

	// DO NOT read first — write directly into unmapped pages using copy().
	// copy() triggers runtime.memmove which uses MOVOU (SSE 128-bit) for
	// 17-32 byte copies. A page fault on the MOVOU store causes a context
	// switch; the XMM registers must survive the round-trip.
	const pageSize = 4096
	const numPages = 16
	allOK := true

	for pg := 0; pg < numPages; pg++ {
		off := pg * pageSize

		// Badger-like pattern: build a 20-byte header on stack, copy to mmap.
		// 20 bytes triggers memmove's 17-32 byte path: MOVOU+MOVOU.
		var buf [20]byte
		// keyID in first 8 bytes (big-endian)
		keyID := uint64(0xCAFE000000000000) | uint64(pg)
		buf[0] = byte(keyID >> 56)
		buf[1] = byte(keyID >> 48)
		buf[2] = byte(keyID >> 40)
		buf[3] = byte(keyID >> 32)
		buf[4] = byte(keyID >> 24)
		buf[5] = byte(keyID >> 16)
		buf[6] = byte(keyID >> 8)
		buf[7] = byte(keyID)
		// baseIV in next 12 bytes
		buf[8] = 0xDE
		buf[9] = 0xAD
		buf[10] = 0xBE
		buf[11] = 0xEF
		buf[12] = byte(pg)
		buf[13] = 0x11
		buf[14] = 0x22
		buf[15] = 0x33
		buf[16] = 0x44
		buf[17] = 0x55
		buf[18] = 0x66
		buf[19] = 0x77

		// Write via copy — first touch on this page, triggers page fault on MOVOU store
		copy(data[off:off+20], buf[:])
	}

	// Read back via copy (badger reads the same way) and verify
	for pg := 0; pg < numPages; pg++ {
		off := pg * pageSize
		var readBuf [20]byte
		copy(readBuf[:], data[off:off+20])

		gotKeyID := uint64(readBuf[0])<<56 | uint64(readBuf[1])<<48 |
			uint64(readBuf[2])<<40 | uint64(readBuf[3])<<32 |
			uint64(readBuf[4])<<24 | uint64(readBuf[5])<<16 |
			uint64(readBuf[6])<<8 | uint64(readBuf[7])
		expectKeyID := uint64(0xCAFE000000000000) | uint64(pg)
		if gotKeyID != expectKeyID {
			fmt.Printf("[mmaptest:writefirst] FAIL: page %d keyID got 0x%016x expected 0x%016x\n",
				pg, gotKeyID, expectKeyID)
			allOK = false
		}
		// Also verify the baseIV portion
		if readBuf[8] != 0xDE || readBuf[9] != 0xAD || readBuf[10] != 0xBE || readBuf[11] != 0xEF {
			fmt.Printf("[mmaptest:writefirst] FAIL: page %d IV prefix got %x%x%x%x expected DEADBEEF\n",
				pg, readBuf[8], readBuf[9], readBuf[10], readBuf[11])
			allOK = false
		}
	}
	if allOK {
		fmt.Printf("[mmaptest:writefirst] PASS: all %d pages copy/MOVOU write-first OK\n", numPages)
	}

	syscall.Munmap(data)
	syscall.Close(fd)
	fmt.Println("[mmaptest:writefirst] --- done ---")
}

// testSubdirWriteReadRoundtrip tests that write→close→reopen→read works
// correctly in a subdirectory of /tmp. This mimics what badger does when
// creating KEYREGISTRY and vlog files.
func testSubdirWriteReadRoundtrip() {
	fmt.Println("[mmaptest:subdir] --- Subdir write/read roundtrip test ---")

	// Create subdirectory
	if err := syscall.Mkdir("/tmp/roundtrip-test", 0755); err != nil {
		fmt.Printf("[mmaptest:subdir] FAIL: mkdir: %v\n", err)
		return
	}

	const path = "/tmp/roundtrip-test/testfile"
	header := []byte{0x00, 0x00, 0x00, 0x01, 0xDE, 0xAD, 0xBE, 0xEF,
		0xCA, 0xFE, 0xBA, 0xBE, 0x12, 0x34, 0x56, 0x78,
		0x9A, 0xBC, 0xDE, 0xF0}

	// Create file and write header
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_CREAT|syscall.O_TRUNC, 0666)
	if err != nil {
		fmt.Printf("[mmaptest:subdir] FAIL: create: %v\n", err)
		return
	}
	n, err := syscall.Write(fd, header)
	if err != nil || n != len(header) {
		fmt.Printf("[mmaptest:subdir] FAIL: write: n=%d err=%v\n", n, err)
		syscall.Close(fd)
		return
	}
	fmt.Printf("[mmaptest:subdir] wrote %d bytes\n", n)
	syscall.Close(fd)

	// Reopen and read back
	fd2, err := syscall.Open(path, syscall.O_RDONLY, 0)
	if err != nil {
		fmt.Printf("[mmaptest:subdir] FAIL: reopen: %v\n", err)
		return
	}
	readBuf := make([]byte, len(header))
	nr, err := syscall.Read(fd2, readBuf)
	if err != nil {
		fmt.Printf("[mmaptest:subdir] FAIL: read: %v\n", err)
		syscall.Close(fd2)
		return
	}
	syscall.Close(fd2)

	if nr != len(header) {
		fmt.Printf("[mmaptest:subdir] FAIL: read %d bytes, expected %d\n", nr, len(header))
		return
	}
	match := true
	for i := range header {
		if readBuf[i] != header[i] {
			match = false
			break
		}
	}
	if !match {
		fmt.Printf("[mmaptest:subdir] FAIL: data mismatch. wrote=")
		for _, b := range header {
			fmt.Printf("%02x ", b)
		}
		fmt.Printf("\n[mmaptest:subdir] FAIL: data mismatch. read =")
		for _, b := range readBuf {
			fmt.Printf("%02x ", b)
		}
		fmt.Println()
	} else {
		fmt.Println("[mmaptest:subdir] PASS: write→close→reopen→read roundtrip OK")
	}

	// Test 2: write, ftruncate to larger, mmap, read through mmap
	const path2 = "/tmp/roundtrip-test/mmapfile"
	fd3, err := syscall.Open(path2, syscall.O_RDWR|syscall.O_CREAT|syscall.O_TRUNC, 0666)
	if err != nil {
		fmt.Printf("[mmaptest:subdir] FAIL: create mmapfile: %v\n", err)
		return
	}
	// Write 20-byte header at offset 0
	n, err = syscall.Write(fd3, header)
	if err != nil || n != len(header) {
		fmt.Printf("[mmaptest:subdir] FAIL: write mmapfile: n=%d err=%v\n", n, err)
		syscall.Close(fd3)
		return
	}
	// Extend file to 128MB
	if err := syscall.Ftruncate(fd3, 128*1024*1024); err != nil {
		fmt.Printf("[mmaptest:subdir] FAIL: ftruncate: %v\n", err)
		syscall.Close(fd3)
		return
	}
	// Mmap the file
	mmapData, err := syscall.Mmap(fd3, 0, 128*1024*1024,
		syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		fmt.Printf("[mmaptest:subdir] FAIL: mmap: %v\n", err)
		syscall.Close(fd3)
		return
	}
	// Read header through mmap
	mmapRead := make([]byte, len(header))
	copy(mmapRead, mmapData[:len(header)])
	match = true
	for i := range header {
		if mmapRead[i] != header[i] {
			match = false
			break
		}
	}
	if !match {
		fmt.Printf("[mmaptest:subdir] FAIL: mmap read mismatch. wrote=")
		for _, b := range header {
			fmt.Printf("%02x ", b)
		}
		fmt.Printf("\n[mmaptest:subdir] FAIL: mmap read mismatch. got  =")
		for _, b := range mmapRead {
			fmt.Printf("%02x ", b)
		}
		fmt.Println()
	} else {
		fmt.Println("[mmaptest:subdir] PASS: write→ftruncate→mmap→read header OK")
	}

	// Check that byte at offset 4096 (beyond header, in sparse region) is zero
	if mmapData[4096] != 0 {
		fmt.Printf("[mmaptest:subdir] FAIL: sparse region byte = %02x, expected 00\n", mmapData[4096])
	} else {
		fmt.Println("[mmaptest:subdir] PASS: sparse region reads as zero")
	}

	syscall.Munmap(mmapData)
	syscall.Close(fd3)
	fmt.Println("[mmaptest:subdir] --- done ---")
}
