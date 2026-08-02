package ext2_test

// Adversary gap tests for MAZ-165 Phase 0 (Step 0f).

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"mazzy/shared/fs/ext2"
)

// buildBigFileDev builds a 48 MB image holding a 24 MB patterned file and
// returns the counting device, mounted fs, the open file, and the pattern.
func buildBigFileDev(t *testing.T) (*counting4kDev, *ext2.FileSystem, *ext2.File, []byte) {
	t.Helper()
	dir := t.TempDir()

	const fileSize = 24 << 20
	content := make([]byte, fileSize)
	for i := range content {
		content[i] = byte(i % 251)
	}
	hostFile := filepath.Join(dir, "big.dat")
	if err := os.WriteFile(hostFile, content, 0644); err != nil {
		t.Fatalf("write host file: %v", err)
	}

	img := buildImage(t, dir, 48, "maz165gap", hostFile)

	f0, err := os.Open(img)
	if err != nil {
		t.Fatalf("open image: %v", err)
	}
	info, err := f0.Stat()
	if err != nil {
		t.Fatalf("stat image: %v", err)
	}
	dev := &counting4kDev{f: f0, size: info.Size()}
	t.Cleanup(func() { dev.Close() })

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
	return dev, fs, f, content
}

// TestReadAtSecondCallOnWarmHandleStaysWithinBudget guards against an
// implementation that only hits the round-trip budget on the first call
// after open. The serve path issues hundreds of sequential chunk reads per
// handle (fsclient dataPages=16 chunking), so EVERY call must stay within
// the <= 3 / >= 1-batched contract, not just the cold one.
func TestReadAtSecondCallOnWarmHandleStaysWithinBudget(t *testing.T) {
	dev, _, f, content := buildBigFileDev(t)

	const windowLen = 64 << 10
	first := make([]byte, windowLen)
	if _, err := f.ReadAt(first, 20<<20); err != nil {
		t.Fatalf("first ReadAt: %v", err)
	}

	// Second window, still double-indirect, far enough that the first
	// call's cached state (whatever the implementation keeps) cannot
	// satisfy it without a fresh resolution.
	dev.resetCounters()
	const secondOff = 21 << 20
	second := make([]byte, windowLen)
	n, err := f.ReadAt(second, secondOff)
	if err != nil {
		t.Fatalf("second ReadAt: %v", err)
	}
	if n != windowLen {
		t.Fatalf("second ReadAt short read: got %d, want %d", n, windowLen)
	}
	if !bytes.Equal(second, content[secondOff:secondOff+windowLen]) {
		t.Fatal("second ReadAt returned wrong bytes")
	}

	single := dev.single.Load()
	batch := dev.batch.Load()
	total := single + batch
	t.Logf("warm-handle second ReadAt: total=%d (single=%d, batched=%d)", total, single, batch)

	if total > 3 {
		t.Errorf("second ReadAt on a warm handle cost %d device round trips, want <= 3", total)
	}
	if batch < 1 {
		t.Errorf("second ReadAt made %d batched ReadBlocks calls, want >= 1", batch)
	}
}

// TestReadAtConcurrentCallsAreIndependent pins the io.ReaderAt contract:
// parallel ReadAt calls on the same file must not interfere. The slow path
// funnels every reader through the File's shared blockBuf/blockIdx/pos
// fields under only an RLock, so concurrent readers can copy each other's
// blocks. Run with -race to make the interference deterministic.
func TestReadAtConcurrentCallsAreIndependent(t *testing.T) {
	_, _, f, content := buildBigFileDev(t)

	const chunk = 4096
	const iters = 50
	offsets := []int64{0, 5 << 20, 12 << 20, 20 << 20}

	for it := range iters {
		var wg sync.WaitGroup
		results := make([][]byte, len(offsets))
		errs := make([]error, len(offsets))
		for i, off := range offsets {
			wg.Add(1)
			go func(i int, off int64) {
				defer wg.Done()
				buf := make([]byte, chunk)
				n, err := f.ReadAt(buf, off)
				results[i], errs[i] = buf[:n], err
			}(i, off)
		}
		wg.Wait()
		for i, off := range offsets {
			if errs[i] != nil {
				t.Fatalf("iter %d offset %d: ReadAt error: %v", it, off, errs[i])
			}
			if !bytes.Equal(results[i], content[off:off+chunk]) {
				t.Fatalf("iter %d offset %d: wrong bytes — cross-goroutine buffer clobber", it, off)
			}
		}
	}
}
