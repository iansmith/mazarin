package ext2_test

// Byte-equality guards for MAZ-165's File.ReadAt (WI-3). These are
// correctness guards, not Phase 0 red tests: they pass against both the
// slow path and the batched implementation, pinning ReadAt == Read on every
// region shape the resolver distinguishes.

import (
	"bytes"
	"os"
	"testing"

	"mazzy/shared/fs/ext2"
)

// readViaSlowPath reads a window with Seek+Read, the pre-existing reference
// implementation.
func readViaSlowPath(t *testing.T, f *ext2.File, off int64, length int) []byte {
	t.Helper()
	if err := f.Seek(uint64(off)); err != nil {
		t.Fatalf("Seek(%d): %v", off, err)
	}
	buf := make([]byte, length)
	n, err := f.Read(buf)
	if err != nil && err != ext2.ErrEndOfFile {
		t.Fatalf("Read at %d: %v", off, err)
	}
	return buf[:n]
}

func TestReadAtMatchesReadAcrossRegions(t *testing.T) {
	_, _, f, content := buildBigFileDev(t)

	const (
		blockSize = 4096
		directEnd = 12 * blockSize             // 48 KB
		singleEnd = directEnd + 1024*blockSize // ~4.05 MB
		fileSize  = 24 << 20
	)

	cases := []struct {
		name string
		off  int64
		len  int
	}{
		{"pure_direct", 0, 8 * 1024},
		{"direct_single_boundary", directEnd - 6*1024, 12 * 1024},
		{"pure_single_indirect", 1 << 20, 64 * 1024},
		{"single_double_boundary", singleEnd - 8*1024, 64 * 1024},
		{"deep_double_l2_straddle", 20 << 20, 64 * 1024},
		{"unaligned_odd", (5 << 20) + 1234, 30_001},
		{"single_byte", 17, 1},
		{"eof_clamped", fileSize - 10*1024, 64 * 1024},
		{"whole_first_block", 0, blockSize},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := make([]byte, tc.len)
			n, err := f.ReadAt(got, tc.off)
			if err != nil {
				t.Fatalf("ReadAt(%d, %d): %v", tc.off, tc.len, err)
			}
			want := content[tc.off:min(int(tc.off)+tc.len, len(content))]
			if n != len(want) {
				t.Fatalf("n: got %d, want %d", n, len(want))
			}
			if !bytes.Equal(got[:n], want) {
				t.Fatalf("bytes differ from source content")
			}
			slow := readViaSlowPath(t, f, tc.off, tc.len)
			if !bytes.Equal(got[:n], slow) {
				t.Fatalf("ReadAt disagrees with Read")
			}
		})
	}

	// Past-EOF: ErrEndOfFile, zero bytes — matching Read's contract.
	buf := make([]byte, 16)
	if n, err := f.ReadAt(buf, fileSize); err != ext2.ErrEndOfFile || n != 0 {
		t.Fatalf("ReadAt past EOF: got (%d, %v), want (0, ErrEndOfFile)", n, err)
	}
	// Position independence: the ReadAt calls above must not have moved pos.
	if err := f.Seek(0); err != nil {
		t.Fatalf("Seek(0): %v", err)
	}
	head := make([]byte, 32)
	if _, err := f.Read(head); err != nil {
		t.Fatalf("Read after ReadAt sequence: %v", err)
	}
	if !bytes.Equal(head, content[:32]) {
		t.Fatal("Read after ReadAt returned wrong bytes — pos was disturbed")
	}
}

// sparseTailOff is where buildSparseImage writes the tail data, leaving a
// ~5 MB unallocated hole (spanning the direct/single/double boundaries)
// between the 4 KB head and the 8 KB tail.
const sparseTailOff = 5 << 20

// buildSparseImage creates an image holding /holey.dat with a real sparse
// hole (forward seek + write leaves the middle unallocated) and returns the
// expected file contents. The write phase needs a device whose WriteBlock
// actually writes — counting4kDev and fileBlockDev both no-op writes.
func buildSparseImage(t *testing.T, dir string) (img string, want []byte) {
	t.Helper()
	img = buildEmptyImage(t, dir, 16, "sparse165")

	wdev := openRWBlockDev(t, img)
	wfs, err := ext2.MountRW(wdev)
	if err != nil {
		t.Fatalf("MountRW: %v", err)
	}
	wf, err := wfs.Create("/holey.dat", 0644)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	headData := bytes.Repeat([]byte{0xAB}, 4096)
	tailData := bytes.Repeat([]byte{0xCD}, 8192)
	if _, err := wf.Write(headData); err != nil {
		t.Fatalf("write head: %v", err)
	}
	if err := wf.Seek(sparseTailOff); err != nil {
		t.Fatalf("seek: %v", err)
	}
	if _, err := wf.Write(tailData); err != nil {
		t.Fatalf("write tail: %v", err)
	}
	wf.Close()
	if err := wfs.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	wdev.Close()

	want = make([]byte, sparseTailOff+len(tailData))
	copy(want, headData)
	copy(want[sparseTailOff:], tailData)
	return img, want
}

func TestReadAtSparseHoles(t *testing.T) {
	img, want := buildSparseImage(t, t.TempDir())

	// Read phase: reopen through the counting 4 KB device (read-only).
	f0, err := os.Open(img)
	if err != nil {
		t.Fatalf("reopen image: %v", err)
	}
	info, err := f0.Stat()
	if err != nil {
		t.Fatalf("stat image: %v", err)
	}
	dev := &counting4kDev{f: f0, size: info.Size()}
	defer dev.Close()

	rfs, err := ext2.Mount(dev)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}
	rf, err := rfs.Open("/holey.dat")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rf.Close()

	cases := []struct {
		name string
		off  int64
		len  int
	}{
		{"head_into_hole", 0, 16 * 1024},
		{"pure_hole_single_indirect", 1 << 20, 64 * 1024},
		{"hole_into_tail", sparseTailOff - 32*1024, 48 * 1024},
		{"hole_direct_single_boundary", 40 * 1024, 32 * 1024},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := make([]byte, tc.len)
			n, err := rf.ReadAt(got, tc.off)
			if err != nil {
				t.Fatalf("ReadAt(%d, %d): %v", tc.off, tc.len, err)
			}
			expect := want[tc.off:min(int(tc.off)+tc.len, len(want))]
			if n != len(expect) {
				t.Fatalf("n: got %d, want %d", n, len(expect))
			}
			if !bytes.Equal(got[:n], expect) {
				t.Fatal("sparse window bytes differ")
			}
			slow := readViaSlowPath(t, rf, tc.off, tc.len)
			if !bytes.Equal(got[:n], slow) {
				t.Fatal("ReadAt disagrees with Read on sparse window")
			}
		})
	}
}
