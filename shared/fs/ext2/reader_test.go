package ext2_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"mazzy/shared/fs/ext2"
)

const mkext2Bin = "" // we'll use go test -exec; instead, build the image externally

// fileBlockDev wraps an os.File as a blockdev.BlockDevice with 512-byte sectors.
type fileBlockDev struct {
	f    *os.File
	size int64
}

func (d *fileBlockDev) Name() string              { return d.f.Name() }
func (d *fileBlockDev) Close() error               { return d.f.Close() }
func (d *fileBlockDev) BlockSize() uint64           { return 512 }
func (d *fileBlockDev) NumBlocks() uint64           { return uint64(d.size) / 512 }
func (d *fileBlockDev) WriteBlock(lba uint64, buf []byte) error { return nil } // read-only

func (d *fileBlockDev) ReadBlock(lba uint64, buf []byte) error {
	_, err := d.f.ReadAt(buf[:512], int64(lba)*512)
	return err
}

func openBlockDev(t *testing.T, path string) *fileBlockDev {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		t.Fatalf("stat %s: %v", path, err)
	}
	return &fileBlockDev{f: f, size: info.Size()}
}

// buildImage calls mkext2 via `go run` to create an ext2 image.
func buildImage(t *testing.T, dir string, sizeMB int, label string, extraArgs ...string) string {
	t.Helper()
	img := filepath.Join(dir, "test.img")

	args := []string{"run", "./cmd/mkext2", "-o", img, "-size", itoa(sizeMB), "-label", label}
	args = append(args, extraArgs...)

	cmd := exec.Command(os.Getenv("GO"), args...)
	if cmd.Path == "" {
		cmd = exec.Command("go", args...)
	}
	cmd.Dir = projectRoot(t)
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=auto")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mkext2 failed: %v\n%s", err, out)
	}
	return img
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 10)
	for n > 0 {
		buf = append(buf, byte('0'+n%10))
		n /= 10
	}
	// reverse
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}

func projectRoot(t *testing.T) string {
	t.Helper()
	// Walk up from the test file to find go.mod
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find project root (go.mod)")
		}
		dir = parent
	}
}

func TestMountEmpty(t *testing.T) {
	dir := t.TempDir()
	img := buildImage(t, dir, 4, "mounttest")

	dev := openBlockDev(t, img)
	defer dev.Close()

	fs, err := ext2.Mount(dev)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}

	sb := fs.Superblock()
	if sb.Magic != ext2.Magic {
		t.Errorf("Magic: got 0x%X, want 0x%X", sb.Magic, ext2.Magic)
	}
	if sb.BlocksCount != 4*1024*1024/4096 {
		t.Errorf("BlocksCount: got %d, want %d", sb.BlocksCount, 4*1024*1024/4096)
	}
}

func TestReadRootDir(t *testing.T) {
	dir := t.TempDir()
	img := buildImage(t, dir, 4, "rootdir")

	dev := openBlockDev(t, img)
	defer dev.Close()

	fs, err := ext2.Mount(dev)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}

	entries, err := fs.ReadDir(ext2.InodeRoot)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (. and ..), got %d", len(entries))
	}
	if entries[0].Name != "." {
		t.Errorf("first entry: got %q, want %q", entries[0].Name, ".")
	}
	if entries[1].Name != ".." {
		t.Errorf("second entry: got %q, want %q", entries[1].Name, "..")
	}
}

func TestReadFileContents(t *testing.T) {
	dir := t.TempDir()

	// Create a test file on host
	testData := []byte("Hello from ext2 reader test!\n")
	hostFile := filepath.Join(dir, "hello.txt")
	os.WriteFile(hostFile, testData, 0644)

	img := buildImage(t, dir, 4, "readfile", hostFile)

	dev := openBlockDev(t, img)
	defer dev.Close()

	fs, err := ext2.Mount(dev)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}

	// Verify root dir has the file
	entries, err := fs.ReadDir(ext2.InodeRoot)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	found := false
	for _, e := range entries {
		if e.Name == "hello.txt" {
			found = true
			if e.FileType != ext2.FTFile {
				t.Errorf("hello.txt FileType: got %d, want %d", e.FileType, ext2.FTFile)
			}
		}
	}
	if !found {
		t.Fatal("hello.txt not found in root directory")
	}

	// Open and read the file
	f, err := fs.Open("/hello.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	if f.Size() != uint32(len(testData)) {
		t.Errorf("Size: got %d, want %d", f.Size(), len(testData))
	}
	if f.Name() != "hello.txt" {
		t.Errorf("Name: got %q, want %q", f.Name(), "hello.txt")
	}

	data, err := f.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(data, testData) {
		t.Errorf("ReadAll: got %q, want %q", data, testData)
	}
}

func TestReadSubdir(t *testing.T) {
	dir := t.TempDir()

	// Create a host directory with files
	subdir := filepath.Join(dir, "mydata")
	os.Mkdir(subdir, 0755)
	fileA := []byte("file a contents\n")
	fileB := []byte{0xCA, 0xFE, 0xBA, 0xBE}
	os.WriteFile(filepath.Join(subdir, "a.txt"), fileA, 0644)
	os.WriteFile(filepath.Join(subdir, "b.bin"), fileB, 0755)

	img := buildImage(t, dir, 4, "subdir", "-dir", "/data="+subdir)

	dev := openBlockDev(t, img)
	defer dev.Close()

	fs, err := ext2.Mount(dev)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}

	// Open file via path
	f, err := fs.Open("/data/a.txt")
	if err != nil {
		t.Fatalf("Open /data/a.txt: %v", err)
	}
	data, err := f.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(data, fileA) {
		t.Errorf("a.txt: got %q, want %q", data, fileA)
	}
	f.Close()

	// Open binary file
	f2, err := fs.Open("/data/b.bin")
	if err != nil {
		t.Fatalf("Open /data/b.bin: %v", err)
	}
	data2, err := f2.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(data2, fileB) {
		t.Errorf("b.bin: got %x, want %x", data2, fileB)
	}
	f2.Close()
}

func TestReadLargeFile(t *testing.T) {
	dir := t.TempDir()

	// Create a file large enough to need indirect blocks (> 48KB)
	bigData := make([]byte, 200*1024) // 200KB
	for i := range bigData {
		bigData[i] = byte(i % 251)
	}
	hostFile := filepath.Join(dir, "big.dat")
	os.WriteFile(hostFile, bigData, 0644)

	img := buildImage(t, dir, 4, "bigread", hostFile)

	dev := openBlockDev(t, img)
	defer dev.Close()

	fs, err := ext2.Mount(dev)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}

	f, err := fs.Open("/big.dat")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	if f.Size() != uint32(len(bigData)) {
		t.Errorf("Size: got %d, want %d", f.Size(), len(bigData))
	}

	data, err := f.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(data, bigData) {
		// Find first difference
		for i := range data {
			if i >= len(bigData) || data[i] != bigData[i] {
				t.Fatalf("data mismatch at byte %d: got 0x%02X, want 0x%02X", i, data[i], bigData[i])
			}
		}
		t.Fatalf("data length mismatch: got %d, want %d", len(data), len(bigData))
	}
}

func TestPartialRead(t *testing.T) {
	dir := t.TempDir()

	testData := make([]byte, 8192) // 2 blocks
	for i := range testData {
		testData[i] = byte(i % 256)
	}
	hostFile := filepath.Join(dir, "partial.dat")
	os.WriteFile(hostFile, testData, 0644)

	img := buildImage(t, dir, 4, "partial", hostFile)

	dev := openBlockDev(t, img)
	defer dev.Close()

	fs, err := ext2.Mount(dev)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}

	f, err := fs.Open("/partial.dat")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	// Read in small chunks
	buf := make([]byte, 100)
	totalRead := 0
	for {
		n, err := f.Read(buf)
		totalRead += n
		if err != nil {
			break
		}
		// Verify the chunk
		offset := totalRead - n
		for i := 0; i < n; i++ {
			if buf[i] != testData[offset+i] {
				t.Fatalf("mismatch at %d: got 0x%02X, want 0x%02X", offset+i, buf[i], testData[offset+i])
			}
		}
	}
	if totalRead != len(testData) {
		t.Errorf("total read: got %d, want %d", totalRead, len(testData))
	}
}

func TestSeek(t *testing.T) {
	dir := t.TempDir()

	testData := []byte("0123456789ABCDEF")
	hostFile := filepath.Join(dir, "seek.dat")
	os.WriteFile(hostFile, testData, 0644)

	img := buildImage(t, dir, 4, "seek", hostFile)

	dev := openBlockDev(t, img)
	defer dev.Close()

	fs, err := ext2.Mount(dev)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}

	f, err := fs.Open("/seek.dat")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	// Seek to middle and read
	if err := f.Seek(10); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	buf := make([]byte, 6)
	n, err := f.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if n != 6 {
		t.Fatalf("Read: got %d bytes, want 6", n)
	}
	if string(buf) != "ABCDEF" {
		t.Errorf("Read after seek: got %q, want %q", buf, "ABCDEF")
	}
}

func TestOpenNotFound(t *testing.T) {
	dir := t.TempDir()
	img := buildImage(t, dir, 4, "notfound")

	dev := openBlockDev(t, img)
	defer dev.Close()

	fs, err := ext2.Mount(dev)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}

	_, err = fs.Open("/nonexistent.txt")
	if err != ext2.ErrNotFound {
		t.Errorf("Open nonexistent: got %v, want ErrNotFound", err)
	}
}

func TestNestedDirRead(t *testing.T) {
	dir := t.TempDir()

	// Create nested host directory
	base := filepath.Join(dir, "tree")
	os.MkdirAll(filepath.Join(base, "sub", "deep"), 0755)
	deepData := []byte("deep file content\n")
	os.WriteFile(filepath.Join(base, "sub", "deep", "buried.txt"), deepData, 0644)

	img := buildImage(t, dir, 4, "nested", "-dir", "/tree="+base)

	dev := openBlockDev(t, img)
	defer dev.Close()

	fs, err := ext2.Mount(dev)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}

	f, err := fs.Open("/tree/sub/deep/buried.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	data, err := f.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(data, deepData) {
		t.Errorf("got %q, want %q", data, deepData)
	}
}
