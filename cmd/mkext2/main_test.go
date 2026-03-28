package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"mazzy/shared/fs/ext2"
)

const e2fsckPath = "/opt/homebrew/Cellar/e2fsprogs/1.47.4/sbin/e2fsck"

// runE2fsck runs e2fsck -fn on the given image file and returns the output.
// Fails the test if e2fsck reports errors.
func runE2fsck(t *testing.T, imgPath string) string {
	t.Helper()

	if _, err := os.Stat(e2fsckPath); err != nil {
		t.Skipf("e2fsck not found at %s: %v", e2fsckPath, err)
	}

	// -f = force check even if clean
	// -n = read-only, don't modify
	cmd := exec.Command(e2fsckPath, "-fn", imgPath)
	out, err := cmd.CombinedOutput()
	output := string(out)

	if err != nil {
		// e2fsck exit codes:
		// 0 = no errors
		// 1 = errors corrected (shouldn't happen with -n)
		// 2 = errors corrected, reboot needed
		// 4 = errors left uncorrected
		// 8 = operational error
		t.Fatalf("e2fsck failed (exit: %v):\n%s", err, output)
	}

	return output
}

func TestEmptyImage4MB(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "test.img")

	if err := createImage(img, 4, "test4mb", nil, nil); err != nil {
		t.Fatalf("createImage: %v", err)
	}

	// Verify file size
	info, err := os.Stat(img)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() != 4*1024*1024 {
		t.Errorf("image size: got %d, want %d", info.Size(), 4*1024*1024)
	}

	output := runE2fsck(t, img)
	t.Logf("e2fsck output:\n%s", output)
}

func TestEmptyImage16MB(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "test.img")

	if err := createImage(img, 16, "test16mb", nil, nil); err != nil {
		t.Fatalf("createImage: %v", err)
	}

	output := runE2fsck(t, img)
	t.Logf("e2fsck output:\n%s", output)
}

func TestEmptyImage64MB(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "test.img")

	if err := createImage(img, 64, "test64mb", nil, nil); err != nil {
		t.Fatalf("createImage: %v", err)
	}

	output := runE2fsck(t, img)
	t.Logf("e2fsck output:\n%s", output)
}

func TestEmptyImage256MB(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "test.img")

	if err := createImage(img, 256, "test256mb", nil, nil); err != nil {
		t.Fatalf("createImage: %v", err)
	}

	output := runE2fsck(t, img)
	t.Logf("e2fsck output:\n%s", output)
}

func TestSuperblockReadback(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "test.img")

	if err := createImage(img, 4, "readback", nil, nil); err != nil {
		t.Fatalf("createImage: %v", err)
	}

	// Read back and verify superblock
	f, err := os.Open(img)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	var sbBuf [ext2.SuperblockSize]byte
	if _, err := f.ReadAt(sbBuf[:], ext2.SuperblockOffset); err != nil {
		t.Fatalf("reading superblock: %v", err)
	}

	sb, err := ext2.UnmarshalSuperblock(sbBuf[:])
	if err != nil {
		t.Fatalf("UnmarshalSuperblock: %v", err)
	}

	if sb.Magic != ext2.Magic {
		t.Errorf("Magic: got 0x%X, want 0x%X", sb.Magic, ext2.Magic)
	}
	if sb.RevLevel != ext2.RevDynamic {
		t.Errorf("RevLevel: got %d, want %d", sb.RevLevel, ext2.RevDynamic)
	}
	if sb.InodeSize != ext2.InodeSize128 {
		t.Errorf("InodeSize: got %d, want %d", sb.InodeSize, ext2.InodeSize128)
	}
	if sb.State != ext2.StateClean {
		t.Errorf("State: got %d, want %d", sb.State, ext2.StateClean)
	}
	if sb.FeatureIncompat&ext2.FeatureIncompatFileType == 0 {
		t.Error("FeatureIncompatFileType not set")
	}

	expectedBlocks := uint32(4 * 1024 * 1024 / 4096)
	if sb.BlocksCount != expectedBlocks {
		t.Errorf("BlocksCount: got %d, want %d", sb.BlocksCount, expectedBlocks)
	}

	if sb.FreeBlocksCount == 0 {
		t.Error("FreeBlocksCount is 0, expected nonzero")
	}
	if sb.FreeInodesCount == 0 {
		t.Error("FreeInodesCount is 0, expected nonzero")
	}

	volName := ""
	for i, b := range sb.VolumeName {
		if b == 0 {
			volName = string(sb.VolumeName[:i])
			break
		}
	}
	if volName != "readback" {
		t.Errorf("VolumeName: got %q, want %q", volName, "readback")
	}
}

func TestRootInodeReadback(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "test.img")

	if err := createImage(img, 4, "rootinode", nil, nil); err != nil {
		t.Fatalf("createImage: %v", err)
	}

	f, err := os.Open(img)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	// Read superblock to find inode table
	var sbBuf [ext2.SuperblockSize]byte
	if _, err := f.ReadAt(sbBuf[:], ext2.SuperblockOffset); err != nil {
		t.Fatalf("reading superblock: %v", err)
	}
	sb, err := ext2.UnmarshalSuperblock(sbBuf[:])
	if err != nil {
		t.Fatalf("UnmarshalSuperblock: %v", err)
	}

	// Read group descriptor to find inode table block
	var gdBuf [ext2.GroupDescSize]byte
	blockSize := int64(ext2.BlockSize(sb.LogBlockSize))
	gdOffset := blockSize // GDT is in block 1 for group 0
	if _, err := f.ReadAt(gdBuf[:], gdOffset); err != nil {
		t.Fatalf("reading GDT: %v", err)
	}
	gd, err := ext2.UnmarshalGroupDesc(gdBuf[:])
	if err != nil {
		t.Fatalf("UnmarshalGroupDesc: %v", err)
	}

	// Read inode 2 (root directory)
	// Inode 2 is at offset (2-1)*InodeSize within the inode table
	inodeOffset := int64(gd.InodeTable)*blockSize + int64(1)*int64(sb.InodeSize)
	var inBuf [ext2.InodeSize128]byte
	if _, err := f.ReadAt(inBuf[:], inodeOffset); err != nil {
		t.Fatalf("reading root inode: %v", err)
	}
	root, err := ext2.UnmarshalInode(inBuf[:])
	if err != nil {
		t.Fatalf("UnmarshalInode: %v", err)
	}

	if !root.IsDir() {
		t.Error("root inode is not a directory")
	}
	if root.LinksCount != 2 {
		t.Errorf("root LinksCount: got %d, want 2", root.LinksCount)
	}
	if root.Size != uint32(blockSize) {
		t.Errorf("root Size: got %d, want %d", root.Size, blockSize)
	}
	if root.Block[0] == 0 {
		t.Error("root Block[0] is 0, expected data block number")
	}
	if root.Permissions() != ext2.PermAll755 {
		t.Errorf("root Permissions: got 0o%o, want 0o%o", root.Permissions(), ext2.PermAll755)
	}
}

func TestRootDirEntries(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "test.img")

	if err := createImage(img, 4, "rootdir", nil, nil); err != nil {
		t.Fatalf("createImage: %v", err)
	}

	f, err := os.Open(img)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	// Read superblock
	var sbBuf [ext2.SuperblockSize]byte
	f.ReadAt(sbBuf[:], ext2.SuperblockOffset)
	sb, _ := ext2.UnmarshalSuperblock(sbBuf[:])
	blockSize := int64(ext2.BlockSize(sb.LogBlockSize))

	// Read GDT
	var gdBuf [ext2.GroupDescSize]byte
	f.ReadAt(gdBuf[:], blockSize)
	gd, _ := ext2.UnmarshalGroupDesc(gdBuf[:])

	// Read root inode to get data block
	inodeOffset := int64(gd.InodeTable)*blockSize + int64(1)*int64(sb.InodeSize)
	var inBuf [ext2.InodeSize128]byte
	f.ReadAt(inBuf[:], inodeOffset)
	root, _ := ext2.UnmarshalInode(inBuf[:])

	// Read root directory data block
	dirData := make([]byte, blockSize)
	f.ReadAt(dirData, int64(root.Block[0])*blockSize)

	// Parse directory entries
	offset := 0
	var entries []ext2.DirEntry
	for offset < int(blockSize) {
		de, consumed, err := ext2.UnmarshalDirEntry(dirData[offset:])
		if err != nil {
			t.Fatalf("UnmarshalDirEntry at offset %d: %v", offset, err)
		}
		if de.Inode != 0 {
			entries = append(entries, *de)
		}
		offset += consumed
		if consumed == 0 {
			break
		}
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 directory entries (. and ..), got %d", len(entries))
	}

	if entries[0].Name != "." {
		t.Errorf("first entry: got %q, want %q", entries[0].Name, ".")
	}
	if entries[0].Inode != ext2.InodeRoot {
		t.Errorf("dot inode: got %d, want %d", entries[0].Inode, ext2.InodeRoot)
	}
	if entries[0].FileType != ext2.FTDir {
		t.Errorf("dot FileType: got %d, want %d", entries[0].FileType, ext2.FTDir)
	}

	if entries[1].Name != ".." {
		t.Errorf("second entry: got %q, want %q", entries[1].Name, "..")
	}
	if entries[1].Inode != ext2.InodeRoot {
		t.Errorf("dotdot inode: got %d, want %d", entries[1].Inode, ext2.InodeRoot)
	}
}

func TestImageWithFiles(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "test.img")

	// Create some test files
	file1 := filepath.Join(dir, "hello.txt")
	os.WriteFile(file1, []byte("Hello, ext2!\n"), 0644)
	file2 := filepath.Join(dir, "world.txt")
	os.WriteFile(file2, []byte("World data here.\n"), 0644)

	if err := createImage(img, 4, "withfiles", []string{file1, file2}, nil); err != nil {
		t.Fatalf("createImage: %v", err)
	}

	output := runE2fsck(t, img)
	t.Logf("e2fsck output:\n%s", output)

	// Verify root directory now has . .. hello.txt world.txt
	f, err := os.Open(img)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	entries := readRootDirEntries(t, f)
	names := make(map[string]bool)
	for _, e := range entries {
		names[e.Name] = true
	}
	for _, want := range []string{".", "..", "hello.txt", "world.txt"} {
		if !names[want] {
			t.Errorf("missing directory entry %q", want)
		}
	}
	if len(entries) != 4 {
		t.Errorf("expected 4 entries, got %d", len(entries))
		for _, e := range entries {
			t.Logf("  entry: inode=%d name=%q type=%d", e.Inode, e.Name, e.FileType)
		}
	}
}

func TestImageWithSubdir(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "test.img")

	// Create a host directory with files
	subdir := filepath.Join(dir, "mydata")
	os.Mkdir(subdir, 0755)
	os.WriteFile(filepath.Join(subdir, "a.txt"), []byte("aaa\n"), 0644)
	os.WriteFile(filepath.Join(subdir, "b.bin"), []byte{0xDE, 0xAD, 0xBE, 0xEF}, 0755)

	dirs := dirFlags{{mountPoint: "/data", hostPath: subdir}}
	if err := createImage(img, 4, "withdir", nil, dirs); err != nil {
		t.Fatalf("createImage: %v", err)
	}

	output := runE2fsck(t, img)
	t.Logf("e2fsck output:\n%s", output)

	// Verify root has . .. data
	f, err := os.Open(img)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	entries := readRootDirEntries(t, f)
	names := make(map[string]bool)
	for _, e := range entries {
		names[e.Name] = true
	}
	if !names["data"] {
		t.Error("missing 'data' directory entry in root")
		for _, e := range entries {
			t.Logf("  entry: inode=%d name=%q type=%d", e.Inode, e.Name, e.FileType)
		}
	}
}

func TestImageWithLargeFile(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "test.img")

	// Create a file larger than 12 direct blocks (48KB) to require indirect blocks
	bigFile := filepath.Join(dir, "big.dat")
	data := make([]byte, 200*1024) // 200KB — will need indirect blocks
	for i := range data {
		data[i] = byte(i % 251) // deterministic pattern
	}
	os.WriteFile(bigFile, data, 0644)

	if err := createImage(img, 4, "bigfile", []string{bigFile}, nil); err != nil {
		t.Fatalf("createImage: %v", err)
	}

	output := runE2fsck(t, img)
	t.Logf("e2fsck output:\n%s", output)
}

func TestImageWithNestedDirs(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "test.img")

	// Create a nested host directory tree
	base := filepath.Join(dir, "tree")
	os.MkdirAll(filepath.Join(base, "sub1", "deep"), 0755)
	os.MkdirAll(filepath.Join(base, "sub2"), 0755)
	os.WriteFile(filepath.Join(base, "root.txt"), []byte("root file\n"), 0644)
	os.WriteFile(filepath.Join(base, "sub1", "one.txt"), []byte("sub1 file\n"), 0644)
	os.WriteFile(filepath.Join(base, "sub1", "deep", "buried.txt"), []byte("deep file\n"), 0644)
	os.WriteFile(filepath.Join(base, "sub2", "two.bin"), make([]byte, 8192), 0755)

	dirs := dirFlags{{mountPoint: "/tree", hostPath: base}}
	if err := createImage(img, 4, "nested", nil, dirs); err != nil {
		t.Fatalf("createImage: %v", err)
	}

	output := runE2fsck(t, img)
	t.Logf("e2fsck output:\n%s", output)
}

func TestImageWithMultipleDirs(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "test.img")

	// Two separate host dirs mounted at different points
	d1 := filepath.Join(dir, "fonts")
	d2 := filepath.Join(dir, "bin")
	os.Mkdir(d1, 0755)
	os.Mkdir(d2, 0755)
	os.WriteFile(filepath.Join(d1, "sans.ttf"), make([]byte, 1024), 0644)
	os.WriteFile(filepath.Join(d2, "hello"), []byte("#!/bin/sh\necho hi\n"), 0755)

	dirs := dirFlags{
		{mountPoint: "/fonts", hostPath: d1},
		{mountPoint: "/bin", hostPath: d2},
	}
	if err := createImage(img, 4, "multi", nil, dirs); err != nil {
		t.Fatalf("createImage: %v", err)
	}

	output := runE2fsck(t, img)
	t.Logf("e2fsck output:\n%s", output)

	// Verify root has both directories
	f, err := os.Open(img)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	entries := readRootDirEntries(t, f)
	names := make(map[string]bool)
	for _, e := range entries {
		names[e.Name] = true
	}
	for _, want := range []string{".", "..", "fonts", "bin"} {
		if !names[want] {
			t.Errorf("missing directory entry %q", want)
		}
	}
}

// readRootDirEntries is a test helper that reads all directory entries from the root directory.
func readRootDirEntries(t *testing.T, f *os.File) []ext2.DirEntry {
	t.Helper()

	var sbBuf [ext2.SuperblockSize]byte
	f.ReadAt(sbBuf[:], ext2.SuperblockOffset)
	sb, _ := ext2.UnmarshalSuperblock(sbBuf[:])
	blockSize := int64(ext2.BlockSize(sb.LogBlockSize))

	var gdBuf [ext2.GroupDescSize]byte
	f.ReadAt(gdBuf[:], blockSize)
	gd, _ := ext2.UnmarshalGroupDesc(gdBuf[:])

	inodeOffset := int64(gd.InodeTable)*blockSize + int64(1)*int64(sb.InodeSize)
	var inBuf [ext2.InodeSize128]byte
	f.ReadAt(inBuf[:], inodeOffset)
	root, _ := ext2.UnmarshalInode(inBuf[:])

	dirData := make([]byte, blockSize)
	f.ReadAt(dirData, int64(root.Block[0])*blockSize)

	offset := 0
	var entries []ext2.DirEntry
	for offset < int(blockSize) {
		de, consumed, err := ext2.UnmarshalDirEntry(dirData[offset:])
		if err != nil {
			t.Fatalf("UnmarshalDirEntry at offset %d: %v", offset, err)
		}
		if de.Inode != 0 {
			entries = append(entries, *de)
		}
		offset += consumed
		if consumed == 0 {
			break
		}
	}
	return entries
}

func TestHasSuperblockBackup(t *testing.T) {
	// Group 0 and 1 always have backups
	if !hasSuperblockBackup(0) {
		t.Error("group 0 should have backup")
	}
	if !hasSuperblockBackup(1) {
		t.Error("group 1 should have backup")
	}

	// Powers of 3: 3, 9, 27, 81, 243
	for _, g := range []uint32{3, 9, 27, 81, 243} {
		if !hasSuperblockBackup(g) {
			t.Errorf("group %d (power of 3) should have backup", g)
		}
	}

	// Powers of 5: 5, 25, 125, 625
	for _, g := range []uint32{5, 25, 125, 625} {
		if !hasSuperblockBackup(g) {
			t.Errorf("group %d (power of 5) should have backup", g)
		}
	}

	// Powers of 7: 7, 49, 343
	for _, g := range []uint32{7, 49, 343} {
		if !hasSuperblockBackup(g) {
			t.Errorf("group %d (power of 7) should have backup", g)
		}
	}

	// Groups that should NOT have backups
	for _, g := range []uint32{2, 4, 6, 8, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20} {
		if hasSuperblockBackup(g) {
			t.Errorf("group %d should NOT have backup", g)
		}
	}
}
