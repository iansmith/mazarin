package ext2_test

import (
	"bytes"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"mazzy/shared/blockdev"
	"mazzy/shared/fs/ext2"
)

// counting4kDev wraps an image file as a counting BlockDevice/BatchBlockDevice
// whose BlockSize is 4096, matching production asyncBlockDev
// (maz/fs/main.go: asyncBlockSize = 4096). The 512-sector fileBlockDev in
// reader_test.go must NOT be used for round-trip counting: it splits every
// 4 KB fs-block read into 8 device calls and invalidates the counts this
// ticket's contract is written in.
type counting4kDev struct {
	f      *os.File
	size   int64
	single atomic.Int64 // ReadBlock calls
	batch  atomic.Int64 // ReadBlocks calls
}

func (d *counting4kDev) Name() string                            { return d.f.Name() }
func (d *counting4kDev) Close() error                            { return d.f.Close() }
func (d *counting4kDev) BlockSize() uint64                       { return 4096 }
func (d *counting4kDev) NumBlocks() uint64                       { return uint64(d.size) / 4096 }
func (d *counting4kDev) WriteBlock(lba uint64, buf []byte) error { return nil } // read-only

func (d *counting4kDev) ReadBlock(lba uint64, buf []byte) error {
	d.single.Add(1)
	n := min(len(buf), 4096)
	_, err := d.f.ReadAt(buf[:n], int64(lba)*4096)
	return err
}

func (d *counting4kDev) ReadBlocks(lbas []uint64, dst []byte) error {
	d.batch.Add(1)
	for i, lba := range lbas {
		off := i * 4096
		end := min(off+4096, len(dst))
		if _, err := d.f.ReadAt(dst[off:end], int64(lba)*4096); err != nil {
			return err
		}
	}
	return nil
}

func (d *counting4kDev) resetCounters() { d.single.Store(0); d.batch.Store(0) }

var _ blockdev.BatchBlockDevice = (*counting4kDev)(nil)

// TestReadAtDoubleIndirectDeviceRoundTrips pins MAZ-165's contract: serving a
// 64 KB window in the double-indirect range of a large file must cost at most
// 3 device round trips once the file is open, at least one of them batched.
//
// Expected decomposition after the fix (window = 64 KB at byte offset 20 MB
// of a 24 MB file; data blocks 5120-5135, straddling L2 tables idx1=3 and
// idx1=4): L1 table (1) + both L2 tables in one batched ReadBlocks (1) + all
// 16 data blocks in one batched ReadBlocks (1) = 3.
//
// On the pre-fix slow path the same window costs 48 round trips, all single
// (16 data + 16 L1 re-reads + 16 L2 reads), because inodeBlockNum re-walks
// the uncached indirect chain for every 4 KB block.
func TestReadAtDoubleIndirectDeviceRoundTrips(t *testing.T) {
	dir := t.TempDir()

	const fileSize = 24 << 20 // 24 MB — deep into the double-indirect range
	content := make([]byte, fileSize)
	for i := range content {
		content[i] = byte(i % 251)
	}
	hostFile := filepath.Join(dir, "big.dat")
	if err := os.WriteFile(hostFile, content, 0644); err != nil {
		t.Fatalf("write host file: %v", err)
	}

	img := buildImage(t, dir, 48, "maz165", hostFile)

	f0, err := os.Open(img)
	if err != nil {
		t.Fatalf("open image: %v", err)
	}
	info, err := f0.Stat()
	if err != nil {
		t.Fatalf("stat image: %v", err)
	}
	dev := &counting4kDev{f: f0, size: info.Size()}
	defer dev.Close()

	fs, err := ext2.Mount(dev)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}

	inum, err := fs.ResolveInode("/big.dat")
	if err != nil {
		t.Fatalf("ResolveInode: %v", err)
	}
	f, err := fs.OpenInum(inum)
	if err != nil {
		t.Fatalf("OpenInum: %v", err)
	}
	defer f.Close()

	// The open loaded the inode; the serve path caches the open file per
	// handle (MAZ-165 behavior 1a), so the per-chunk budget starts here.
	dev.resetCounters()

	const windowOff = 20 << 20 // block 5120; idx1 straddles 3 -> 4
	const windowLen = 64 << 10 // one fsclient chunk (dataPages=16 x 4 KB)
	got := make([]byte, windowLen)
	n, err := f.ReadAt(got, windowOff)
	if err != nil {
		t.Fatalf("ReadAt: %v", err)
	}
	if n != windowLen {
		t.Fatalf("ReadAt short read: got %d, want %d", n, windowLen)
	}
	if !bytes.Equal(got, content[windowOff:windowOff+windowLen]) {
		t.Fatal("ReadAt returned wrong bytes for the window")
	}

	single := dev.single.Load()
	batch := dev.batch.Load()
	total := single + batch
	t.Logf("device round trips: total=%d (single=%d, batched=%d)", total, single, batch)

	if total > 3 {
		t.Errorf("64 KB double-indirect ReadAt cost %d device round trips, want <= 3 "+
			"(L1 + batched L2 pair + batched data)", total)
	}
	if batch < 1 {
		t.Errorf("64 KB double-indirect ReadAt made %d batched ReadBlocks calls, want >= 1", batch)
	}
}
