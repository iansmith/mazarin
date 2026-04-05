package ext2

import (
	"testing"

	"mazzy/shared/blockdev"
)

func TestFormatAndMount(t *testing.T) {
	// 4MB ramdisk with 512-byte sectors.
	dev := blockdev.NewMemBlockDevice("test", 512, 8192)
	if err := Format(dev, "testfs"); err != nil {
		t.Fatalf("Format: %v", err)
	}

	fs, err := MountRW(dev)
	if err != nil {
		t.Fatalf("MountRW: %v", err)
	}

	// Verify superblock.
	sb := fs.Superblock()
	if sb.Magic != Magic {
		t.Fatalf("bad magic: 0x%x", sb.Magic)
	}
	// ext2 block count is in 4096-byte blocks, not device sectors.
	expectedBlocks := uint32(4 * 1024 * 1024 / 4096) // 1024
	if sb.BlocksCount != expectedBlocks {
		t.Fatalf("block count: got %d, want %d", sb.BlocksCount, expectedBlocks)
	}
	if sb.FreeBlocksCount == 0 {
		t.Fatal("no free blocks")
	}
	if sb.FreeInodesCount == 0 {
		t.Fatal("no free inodes")
	}

	// Verify root directory exists with . and ..
	entries, err := fs.ReadDir(InodeRoot)
	if err != nil {
		t.Fatalf("ReadDir root: %v", err)
	}
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name] = true
	}
	if !names["."] || !names[".."] {
		t.Fatalf("root dir missing . or ..: %v", names)
	}
}

func TestFormatWriteRead(t *testing.T) {
	dev := blockdev.NewMemBlockDevice("test", 512, 8192)
	if err := Format(dev, "testfs"); err != nil {
		t.Fatalf("Format: %v", err)
	}
	fs, err := MountRW(dev)
	if err != nil {
		t.Fatalf("MountRW: %v", err)
	}

	// Write a file via WriteFile.
	testData := []byte("hello, ext2 ramdisk!")
	if err := fs.WriteFile("/test.txt", testData, PermAll644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Read it back via Open + Read.
	f, err := fs.Open("/test.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	buf := make([]byte, 1024)
	n, err := f.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(buf[:n]) != string(testData) {
		t.Fatalf("read back %q, want %q", buf[:n], testData)
	}
}

func TestFormatMkdir(t *testing.T) {
	dev := blockdev.NewMemBlockDevice("test", 512, 8192)
	if err := Format(dev, "testfs"); err != nil {
		t.Fatalf("Format: %v", err)
	}
	fs, err := MountRW(dev)
	if err != nil {
		t.Fatalf("MountRW: %v", err)
	}

	// Create a directory.
	if err := fs.Mkdir("/mydir", PermAll755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	// Write a file inside it.
	testData := []byte("nested file content")
	if err := fs.WriteFile("/mydir/file.txt", testData, PermAll644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Verify root has mydir.
	entries, err := fs.ReadDir(InodeRoot)
	if err != nil {
		t.Fatalf("ReadDir root: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Name == "mydir" && e.FileType == FTDir {
			found = true
		}
	}
	if !found {
		t.Fatal("mydir not found in root")
	}

	// Read nested file back.
	f, err := fs.Open("/mydir/file.txt")
	if err != nil {
		t.Fatalf("Open nested: %v", err)
	}
	defer f.Close()

	buf := make([]byte, 1024)
	n, err := f.Read(buf)
	if err != nil {
		t.Fatalf("Read nested: %v", err)
	}
	if string(buf[:n]) != string(testData) {
		t.Fatalf("nested read %q, want %q", buf[:n], testData)
	}
}

func TestFormatTmpDir(t *testing.T) {
	// Simulates the runtime path: ramdisk is root, create /tmp, write files there.
	dev := blockdev.NewMemBlockDevice("test", 512, 8192)
	if err := Format(dev, "testfs"); err != nil {
		t.Fatalf("Format: %v", err)
	}
	fs, err := MountRW(dev)
	if err != nil {
		t.Fatalf("MountRW: %v", err)
	}

	// Create /tmp.
	if err := fs.Mkdir("/tmp", PermAll755); err != nil {
		t.Fatalf("Mkdir /tmp: %v", err)
	}

	// Write a file into /tmp.
	testData := []byte("temp file content")
	if err := fs.WriteFile("/tmp/scratch.dat", testData, PermAll644); err != nil {
		t.Fatalf("WriteFile /tmp/scratch.dat: %v", err)
	}

	// Create a subdirectory inside /tmp.
	if err := fs.Mkdir("/tmp/subdir", PermAll755); err != nil {
		t.Fatalf("Mkdir /tmp/subdir: %v", err)
	}
	if err := fs.WriteFile("/tmp/subdir/nested.txt", []byte("nested"), PermAll644); err != nil {
		t.Fatalf("WriteFile nested: %v", err)
	}

	// Read /tmp/scratch.dat back.
	f, err := fs.Open("/tmp/scratch.dat")
	if err != nil {
		t.Fatalf("Open /tmp/scratch.dat: %v", err)
	}
	buf := make([]byte, 1024)
	n, err := f.Read(buf)
	f.Close()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(buf[:n]) != string(testData) {
		t.Fatalf("read back %q, want %q", buf[:n], testData)
	}

	// Read /tmp/subdir/nested.txt back.
	f2, err := fs.Open("/tmp/subdir/nested.txt")
	if err != nil {
		t.Fatalf("Open nested: %v", err)
	}
	n, err = f2.Read(buf)
	f2.Close()
	if err != nil {
		t.Fatalf("Read nested: %v", err)
	}
	if string(buf[:n]) != "nested" {
		t.Fatalf("nested read %q, want %q", buf[:n], "nested")
	}

	// Verify /tmp directory listing.
	inum, err := fs.ResolveInode("/tmp")
	if err != nil {
		t.Fatalf("ResolveInode /tmp: %v", err)
	}
	entries, err := fs.ReadDir(inum)
	if err != nil {
		t.Fatalf("ReadDir /tmp: %v", err)
	}
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name] = true
	}
	if !names["scratch.dat"] || !names["subdir"] || !names["."] || !names[".."] {
		t.Fatalf("/tmp entries: %v", names)
	}

	// Remove a file.
	if err := fs.Remove("/tmp/scratch.dat"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	_, err = fs.Open("/tmp/scratch.dat")
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound after remove, got %v", err)
	}
}

func TestFormatLargeFile(t *testing.T) {
	// 16MB device to test files spanning multiple blocks.
	dev := blockdev.NewMemBlockDevice("test", 512, 32768)
	if err := Format(dev, "testfs"); err != nil {
		t.Fatalf("Format: %v", err)
	}
	fs, err := MountRW(dev)
	if err != nil {
		t.Fatalf("MountRW: %v", err)
	}

	// Create a 64KB file (16 blocks).
	data := make([]byte, 64*1024)
	for i := range data {
		data[i] = byte(i % 251) // prime modulus for pattern
	}
	if err := fs.WriteFile("/big.bin", data, PermAll644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Read it back.
	f, err := fs.Open("/big.bin")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	readBack := make([]byte, len(data))
	n, err := f.Read(readBack)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if n != len(data) {
		t.Fatalf("read %d bytes, want %d", n, len(data))
	}
	for i := range data {
		if readBack[i] != data[i] {
			t.Fatalf("mismatch at byte %d: got %d, want %d", i, readBack[i], data[i])
		}
	}
}
