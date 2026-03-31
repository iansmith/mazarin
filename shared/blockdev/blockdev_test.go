package blockdev_test

import (
	"bytes"
	"mazzy/shared/blockdev"
	"testing"
)

func TestMemBlockDeviceImplementsBatchBlockDevice(t *testing.T) {
	dev := blockdev.NewMemBlockDevice("test", 512, 64)
	var _ blockdev.BatchBlockDevice = dev
}

func TestReadBlocksMatchesReadBlock(t *testing.T) {
	dev := blockdev.NewMemBlockDevice("test", 512, 64)

	// Write distinct patterns to several blocks.
	for lba := uint64(0); lba < 64; lba++ {
		buf := make([]byte, 512)
		for i := range buf {
			buf[i] = byte(lba ^ uint64(i))
		}
		if err := dev.WriteBlock(lba, buf); err != nil {
			t.Fatalf("WriteBlock(%d): %v", lba, err)
		}
	}

	// Read scattered LBAs with ReadBlocks.
	lbas := []uint64{7, 0, 63, 32, 1}
	dst := make([]byte, len(lbas)*512)
	if err := dev.ReadBlocks(lbas, dst); err != nil {
		t.Fatalf("ReadBlocks: %v", err)
	}

	// Verify each block matches individual ReadBlock.
	for i, lba := range lbas {
		expected := make([]byte, 512)
		if err := dev.ReadBlock(lba, expected); err != nil {
			t.Fatalf("ReadBlock(%d): %v", lba, err)
		}
		got := dst[i*512 : (i+1)*512]
		if !bytes.Equal(got, expected) {
			t.Errorf("block %d (lba %d): mismatch at first differing byte", i, lba)
		}
	}
}

func TestReadBlocksEmpty(t *testing.T) {
	dev := blockdev.NewMemBlockDevice("test", 512, 64)
	if err := dev.ReadBlocks(nil, nil); err != nil {
		t.Fatalf("ReadBlocks(nil): %v", err)
	}
}

func TestReadBlocksSingleLBA(t *testing.T) {
	dev := blockdev.NewMemBlockDevice("test", 512, 64)
	buf := make([]byte, 512)
	for i := range buf {
		buf[i] = 0xAB
	}
	dev.WriteBlock(5, buf)

	dst := make([]byte, 512)
	if err := dev.ReadBlocks([]uint64{5}, dst); err != nil {
		t.Fatalf("ReadBlocks: %v", err)
	}
	if !bytes.Equal(dst, buf) {
		t.Error("single-LBA ReadBlocks mismatch")
	}
}

func TestReadBlocksOutOfRange(t *testing.T) {
	dev := blockdev.NewMemBlockDevice("test", 512, 64)
	dst := make([]byte, 512)
	err := dev.ReadBlocks([]uint64{100}, dst)
	if err == nil {
		t.Error("expected error for out-of-range LBA")
	}
}

func TestReadBlocksFromBacking(t *testing.T) {
	backing := make([]byte, 512*32)
	// Write pattern directly into backing.
	for i := range backing {
		backing[i] = byte(i % 251)
	}
	dev := blockdev.NewMemBlockDeviceFromBacking("test", 512, backing)

	var _ blockdev.BatchBlockDevice = dev

	lbas := []uint64{0, 15, 31}
	dst := make([]byte, len(lbas)*512)
	if err := dev.ReadBlocks(lbas, dst); err != nil {
		t.Fatalf("ReadBlocks: %v", err)
	}

	// Verify against direct backing store reads.
	for i, lba := range lbas {
		off := int(lba) * 512
		expected := backing[off : off+512]
		got := dst[i*512 : (i+1)*512]
		if !bytes.Equal(got, expected) {
			t.Errorf("block %d (lba %d): mismatch", i, lba)
		}
	}
}
