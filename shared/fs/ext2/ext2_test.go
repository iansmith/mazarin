package ext2

import (
	"testing"
)

func TestSuperblockRoundTrip(t *testing.T) {
	sb := &Superblock{
		InodesCount:     1024,
		BlocksCount:     4096,
		RBlocksCount:    204,
		FreeBlocksCount: 3800,
		FreeInodesCount: 1013,
		FirstDataBlock:  0,
		LogBlockSize:    LogBlockSize4K,
		LogFragSize:     LogBlockSize4K,
		BlocksPerGroup:  32768,
		FragsPerGroup:   32768,
		InodesPerGroup:  1024,
		MTime:           1711000000,
		WTime:           1711000001,
		MntCount:        1,
		MaxMntCount:     20,
		Magic:           Magic,
		State:           StateClean,
		Errors:          ErrorsContinue,
		LastCheck:        1711000000,
		CheckInterval:   15552000, // 180 days
		CreatorOS:       OSLinux,
		RevLevel:        RevDynamic,
		FirstIno:        InodeFirstNonRs,
		InodeSize:       InodeSize128,
		FeatureIncompat: FeatureIncompatFileType,
		FeatureROCompat: FeatureROCompatSparseSuper,
	}
	sb.UUID = [16]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}
	copy(sb.VolumeName[:], "mazzyfs")

	buf := sb.Marshal()
	sb2, err := UnmarshalSuperblock(buf[:])
	if err != nil {
		t.Fatalf("UnmarshalSuperblock: %v", err)
	}

	if sb2.InodesCount != sb.InodesCount {
		t.Errorf("InodesCount: got %d, want %d", sb2.InodesCount, sb.InodesCount)
	}
	if sb2.BlocksCount != sb.BlocksCount {
		t.Errorf("BlocksCount: got %d, want %d", sb2.BlocksCount, sb.BlocksCount)
	}
	if sb2.FreeBlocksCount != sb.FreeBlocksCount {
		t.Errorf("FreeBlocksCount: got %d, want %d", sb2.FreeBlocksCount, sb.FreeBlocksCount)
	}
	if sb2.FreeInodesCount != sb.FreeInodesCount {
		t.Errorf("FreeInodesCount: got %d, want %d", sb2.FreeInodesCount, sb.FreeInodesCount)
	}
	if sb2.Magic != Magic {
		t.Errorf("Magic: got 0x%X, want 0x%X", sb2.Magic, Magic)
	}
	if sb2.LogBlockSize != LogBlockSize4K {
		t.Errorf("LogBlockSize: got %d, want %d", sb2.LogBlockSize, LogBlockSize4K)
	}
	if sb2.RevLevel != RevDynamic {
		t.Errorf("RevLevel: got %d, want %d", sb2.RevLevel, RevDynamic)
	}
	if sb2.InodeSize != InodeSize128 {
		t.Errorf("InodeSize: got %d, want %d", sb2.InodeSize, InodeSize128)
	}
	if sb2.FeatureIncompat != FeatureIncompatFileType {
		t.Errorf("FeatureIncompat: got 0x%X, want 0x%X", sb2.FeatureIncompat, FeatureIncompatFileType)
	}
	if sb2.UUID != sb.UUID {
		t.Errorf("UUID mismatch")
	}
	volName := string(sb2.VolumeName[:7])
	if volName != "mazzyfs" {
		t.Errorf("VolumeName: got %q, want %q", volName, "mazzyfs")
	}
	if sb2.BlocksPerGroup != sb.BlocksPerGroup {
		t.Errorf("BlocksPerGroup: got %d, want %d", sb2.BlocksPerGroup, sb.BlocksPerGroup)
	}
	if sb2.InodesPerGroup != sb.InodesPerGroup {
		t.Errorf("InodesPerGroup: got %d, want %d", sb2.InodesPerGroup, sb.InodesPerGroup)
	}
	if sb2.WTime != sb.WTime {
		t.Errorf("WTime: got %d, want %d", sb2.WTime, sb.WTime)
	}
}

func TestSuperblockBadMagic(t *testing.T) {
	var buf [SuperblockSize]byte
	// Set wrong magic
	buf[56] = 0x00
	buf[57] = 0x00
	_, err := UnmarshalSuperblock(buf[:])
	if err != ErrNotExt2 {
		t.Errorf("expected ErrNotExt2, got %v", err)
	}
}

func TestGroupDescRoundTrip(t *testing.T) {
	gd := &GroupDesc{
		BlockBitmap:     3,
		InodeBitmap:     4,
		InodeTable:      5,
		FreeBlocksCount: 32700,
		FreeInodesCount: 1020,
		UsedDirsCount:   2,
	}

	buf := gd.Marshal()
	gd2, err := UnmarshalGroupDesc(buf[:])
	if err != nil {
		t.Fatalf("UnmarshalGroupDesc: %v", err)
	}

	if gd2.BlockBitmap != 3 {
		t.Errorf("BlockBitmap: got %d, want 3", gd2.BlockBitmap)
	}
	if gd2.InodeBitmap != 4 {
		t.Errorf("InodeBitmap: got %d, want 4", gd2.InodeBitmap)
	}
	if gd2.InodeTable != 5 {
		t.Errorf("InodeTable: got %d, want 5", gd2.InodeTable)
	}
	if gd2.FreeBlocksCount != 32700 {
		t.Errorf("FreeBlocksCount: got %d, want 32700", gd2.FreeBlocksCount)
	}
	if gd2.FreeInodesCount != 1020 {
		t.Errorf("FreeInodesCount: got %d, want 1020", gd2.FreeInodesCount)
	}
	if gd2.UsedDirsCount != 2 {
		t.Errorf("UsedDirsCount: got %d, want 2", gd2.UsedDirsCount)
	}
}

func TestInodeRoundTrip(t *testing.T) {
	in := &Inode{
		Mode:       TypeDir | PermAll755,
		UID:        0,
		Size:       4096,
		ATime:      1711000000,
		CTime:      1711000000,
		MTime:      1711000000,
		GID:        0,
		LinksCount: 2,
		Blocks:     8, // 4096 bytes / 512 = 8 sectors
		Block:      [15]uint32{100, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	}

	buf := in.Marshal()
	in2, err := UnmarshalInode(buf[:])
	if err != nil {
		t.Fatalf("UnmarshalInode: %v", err)
	}

	if !in2.IsDir() {
		t.Error("expected IsDir() = true")
	}
	if in2.IsFile() {
		t.Error("expected IsFile() = false")
	}
	if in2.Permissions() != PermAll755 {
		t.Errorf("Permissions: got 0x%X, want 0x%X", in2.Permissions(), PermAll755)
	}
	if in2.Size != 4096 {
		t.Errorf("Size: got %d, want 4096", in2.Size)
	}
	if in2.LinksCount != 2 {
		t.Errorf("LinksCount: got %d, want 2", in2.LinksCount)
	}
	if in2.Block[0] != 100 {
		t.Errorf("Block[0]: got %d, want 100", in2.Block[0])
	}
	if in2.ATime != 1711000000 {
		t.Errorf("ATime: got %d, want 1711000000", in2.ATime)
	}
	if in2.Blocks != 8 {
		t.Errorf("Blocks: got %d, want 8", in2.Blocks)
	}
}

func TestInodeFileType(t *testing.T) {
	tests := []struct {
		mode   uint16
		isDir  bool
		isFile bool
		isLink bool
	}{
		{TypeDir | PermAll755, true, false, false},
		{TypeFile | PermAll644, false, true, false},
		{TypeLink | PermAll755, false, false, true},
	}
	for _, tt := range tests {
		in := &Inode{Mode: tt.mode}
		if in.IsDir() != tt.isDir {
			t.Errorf("mode 0x%X: IsDir() = %v, want %v", tt.mode, in.IsDir(), tt.isDir)
		}
		if in.IsFile() != tt.isFile {
			t.Errorf("mode 0x%X: IsFile() = %v, want %v", tt.mode, in.IsFile(), tt.isFile)
		}
		if in.IsSymlink() != tt.isLink {
			t.Errorf("mode 0x%X: IsSymlink() = %v, want %v", tt.mode, in.IsSymlink(), tt.isLink)
		}
	}
}

func TestDirEntryRoundTrip(t *testing.T) {
	de := NewDirEntry(2, "hello.txt", FTFile)

	buf := make([]byte, de.RecLen)
	n := MarshalDirEntry(buf, de)
	if n != int(de.RecLen) {
		t.Errorf("MarshalDirEntry returned %d, want %d", n, de.RecLen)
	}

	de2, consumed, err := UnmarshalDirEntry(buf)
	if err != nil {
		t.Fatalf("UnmarshalDirEntry: %v", err)
	}
	if consumed != int(de.RecLen) {
		t.Errorf("consumed %d bytes, want %d", consumed, de.RecLen)
	}

	if de2.Inode != 2 {
		t.Errorf("Inode: got %d, want 2", de2.Inode)
	}
	if de2.Name != "hello.txt" {
		t.Errorf("Name: got %q, want %q", de2.Name, "hello.txt")
	}
	if de2.FileType != FTFile {
		t.Errorf("FileType: got %d, want %d", de2.FileType, FTFile)
	}
	if de2.NameLen != 9 {
		t.Errorf("NameLen: got %d, want 9", de2.NameLen)
	}
}

func TestDirEntryRealSize(t *testing.T) {
	tests := []struct {
		nameLen  int
		expected uint16
	}{
		{1, 12},  // 8 + 1 = 9, round up to 12
		{2, 12},  // 8 + 2 = 10, round up to 12
		{3, 12},  // 8 + 3 = 11, round up to 12
		{4, 12},  // 8 + 4 = 12, already aligned
		{5, 16},  // 8 + 5 = 13, round up to 16
		{8, 16},  // 8 + 8 = 16, already aligned
		{9, 20},  // 8 + 9 = 17, round up to 20
		{255, 264}, // 8 + 255 = 263, round up to 264
	}
	for _, tt := range tests {
		got := DirEntryRealSize(tt.nameLen)
		if got != tt.expected {
			t.Errorf("DirEntryRealSize(%d) = %d, want %d", tt.nameLen, got, tt.expected)
		}
	}
}

func TestDirEntryDotAndDotDot(t *testing.T) {
	dot := NewDirEntry(2, ".", FTDir)
	dotdot := NewDirEntry(2, "..", FTDir)

	if dot.NameLen != 1 {
		t.Errorf("dot NameLen: got %d, want 1", dot.NameLen)
	}
	if dotdot.NameLen != 2 {
		t.Errorf("dotdot NameLen: got %d, want 2", dotdot.NameLen)
	}

	// Both should round up to 12 bytes
	if dot.RecLen != 12 {
		t.Errorf("dot RecLen: got %d, want 12", dot.RecLen)
	}
	if dotdot.RecLen != 12 {
		t.Errorf("dotdot RecLen: got %d, want 12", dotdot.RecLen)
	}

	// Verify round-trip
	buf := make([]byte, 12)
	MarshalDirEntry(buf, dot)
	dot2, _, err := UnmarshalDirEntry(buf)
	if err != nil {
		t.Fatal(err)
	}
	if dot2.Name != "." || dot2.Inode != 2 || dot2.FileType != FTDir {
		t.Errorf("dot round-trip: got %+v", dot2)
	}
}

func TestBlockSize(t *testing.T) {
	if BlockSize(LogBlockSize1K) != 1024 {
		t.Errorf("BlockSize(0) = %d, want 1024", BlockSize(LogBlockSize1K))
	}
	if BlockSize(LogBlockSize2K) != 2048 {
		t.Errorf("BlockSize(1) = %d, want 2048", BlockSize(LogBlockSize2K))
	}
	if BlockSize(LogBlockSize4K) != 4096 {
		t.Errorf("BlockSize(2) = %d, want 4096", BlockSize(LogBlockSize4K))
	}
}
