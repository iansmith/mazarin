package ext2_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"mazzy/shared/fs/ext2"
)

const e2fsckPath = "/opt/homebrew/Cellar/e2fsprogs/1.47.4/sbin/e2fsck"

// rwBlockDev wraps an os.File as a read-write BlockDevice with 512-byte sectors.
type rwBlockDev struct {
	f    *os.File
	size int64
}

func (d *rwBlockDev) Name() string    { return d.f.Name() }
func (d *rwBlockDev) Close() error    { return d.f.Close() }
func (d *rwBlockDev) BlockSize() uint64 { return 512 }
func (d *rwBlockDev) NumBlocks() uint64 { return uint64(d.size) / 512 }

func (d *rwBlockDev) ReadBlock(lba uint64, buf []byte) error {
	_, err := d.f.ReadAt(buf[:512], int64(lba)*512)
	return err
}

func (d *rwBlockDev) WriteBlock(lba uint64, buf []byte) error {
	_, err := d.f.WriteAt(buf[:512], int64(lba)*512)
	return err
}

func openRWBlockDev(t *testing.T, path string) *rwBlockDev {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		t.Fatalf("stat %s: %v", path, err)
	}
	return &rwBlockDev{f: f, size: info.Size()}
}

// buildEmptyImage creates an empty ext2 image via mkext2.
func buildEmptyImage(t *testing.T, dir string, sizeMB int, label string) string {
	t.Helper()
	img := filepath.Join(dir, "test.img")
	args := []string{"run", "./cmd/mkext2", "-o", img, "-size", itoa(sizeMB), "-label", label}

	goCmd := os.Getenv("GO")
	if goCmd == "" {
		goCmd = "go"
	}
	cmd := exec.Command(goCmd, args...)
	cmd.Dir = projectRoot(t)
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=auto")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mkext2 failed: %v\n%s", err, out)
	}
	return img
}

func runE2fsck(t *testing.T, imgPath string) string {
	t.Helper()
	if _, err := os.Stat(e2fsckPath); err != nil {
		t.Skipf("e2fsck not found at %s: %v", e2fsckPath, err)
	}
	cmd := exec.Command(e2fsckPath, "-fn", imgPath)
	out, err := cmd.CombinedOutput()
	output := string(out)
	if err != nil {
		t.Fatalf("e2fsck failed (exit: %v):\n%s", err, output)
	}
	return output
}

func TestMountRW(t *testing.T) {
	dir := t.TempDir()
	img := buildEmptyImage(t, dir, 4, "rwtest")

	dev := openRWBlockDev(t, img)
	defer dev.Close()

	fs, err := ext2.MountRW(dev)
	if err != nil {
		t.Fatalf("MountRW: %v", err)
	}

	sb := fs.Superblock()
	if sb.Magic != ext2.Magic {
		t.Errorf("Magic: got 0x%X, want 0x%X", sb.Magic, ext2.Magic)
	}
}

func TestWriteFile(t *testing.T) {
	dir := t.TempDir()
	img := buildEmptyImage(t, dir, 4, "writefile")

	dev := openRWBlockDev(t, img)

	fs, err := ext2.MountRW(dev)
	if err != nil {
		t.Fatalf("MountRW: %v", err)
	}

	testData := []byte("Hello, ext2 writer!\n")
	if err := fs.WriteFile("/hello.txt", testData, ext2.PermAll644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Sync metadata
	if err := fs.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	dev.Close()

	// Verify with e2fsck
	output := runE2fsck(t, img)
	t.Logf("e2fsck output:\n%s", output)

	// Re-mount read-only and read back
	dev2 := openBlockDev(t, img)
	defer dev2.Close()

	fs2, err := ext2.Mount(dev2)
	if err != nil {
		t.Fatalf("Mount (re-read): %v", err)
	}

	f, err := fs2.Open("/hello.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	data, err := f.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(data, testData) {
		t.Errorf("ReadAll: got %q, want %q", data, testData)
	}
}

func TestWriteMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	img := buildEmptyImage(t, dir, 4, "multi")

	dev := openRWBlockDev(t, img)

	fs, err := ext2.MountRW(dev)
	if err != nil {
		t.Fatalf("MountRW: %v", err)
	}

	files := map[string][]byte{
		"/a.txt": []byte("file a\n"),
		"/b.txt": []byte("file b contents\n"),
		"/c.bin": {0xDE, 0xAD, 0xBE, 0xEF},
	}
	for path, data := range files {
		if err := fs.WriteFile(path, data, ext2.PermAll644); err != nil {
			t.Fatalf("WriteFile %s: %v", path, err)
		}
	}

	if err := fs.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	dev.Close()

	output := runE2fsck(t, img)
	t.Logf("e2fsck output:\n%s", output)

	// Read all files back
	dev2 := openBlockDev(t, img)
	defer dev2.Close()
	fs2, err := ext2.Mount(dev2)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}

	for path, expected := range files {
		f, err := fs2.Open(path)
		if err != nil {
			t.Fatalf("Open %s: %v", path, err)
		}
		data, err := f.ReadAll()
		if err != nil {
			t.Fatalf("ReadAll %s: %v", path, err)
		}
		if !bytes.Equal(data, expected) {
			t.Errorf("%s: got %q, want %q", path, data, expected)
		}
		f.Close()
	}
}

func TestMkdir(t *testing.T) {
	dir := t.TempDir()
	img := buildEmptyImage(t, dir, 4, "mkdir")

	dev := openRWBlockDev(t, img)

	fs, err := ext2.MountRW(dev)
	if err != nil {
		t.Fatalf("MountRW: %v", err)
	}

	if err := fs.Mkdir("/data", ext2.PermAll755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	// Write file inside new directory
	if err := fs.WriteFile("/data/test.txt", []byte("inside subdir\n"), ext2.PermAll644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := fs.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	dev.Close()

	output := runE2fsck(t, img)
	t.Logf("e2fsck output:\n%s", output)

	// Read back
	dev2 := openBlockDev(t, img)
	defer dev2.Close()
	fs2, err := ext2.Mount(dev2)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}

	f, err := fs2.Open("/data/test.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	data, err := f.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(data) != "inside subdir\n" {
		t.Errorf("got %q, want %q", data, "inside subdir\n")
	}
	f.Close()
}

func TestRemoveFile(t *testing.T) {
	dir := t.TempDir()
	img := buildEmptyImage(t, dir, 4, "remove")

	dev := openRWBlockDev(t, img)

	fs, err := ext2.MountRW(dev)
	if err != nil {
		t.Fatalf("MountRW: %v", err)
	}

	if err := fs.WriteFile("/todelete.txt", []byte("delete me\n"), ext2.PermAll644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := fs.Remove("/todelete.txt"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if err := fs.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	dev.Close()

	output := runE2fsck(t, img)
	t.Logf("e2fsck output:\n%s", output)

	// Verify file is gone
	dev2 := openBlockDev(t, img)
	defer dev2.Close()
	fs2, err := ext2.Mount(dev2)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}

	_, err = fs2.Open("/todelete.txt")
	if err != ext2.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestRemoveDir(t *testing.T) {
	dir := t.TempDir()
	img := buildEmptyImage(t, dir, 4, "rmdir")

	dev := openRWBlockDev(t, img)

	fs, err := ext2.MountRW(dev)
	if err != nil {
		t.Fatalf("MountRW: %v", err)
	}

	if err := fs.Mkdir("/empty", ext2.PermAll755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := fs.Remove("/empty"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if err := fs.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	dev.Close()

	output := runE2fsck(t, img)
	t.Logf("e2fsck output:\n%s", output)
}

func TestRemoveNonEmptyDir(t *testing.T) {
	dir := t.TempDir()
	img := buildEmptyImage(t, dir, 4, "rmnonempty")

	dev := openRWBlockDev(t, img)

	fs, err := ext2.MountRW(dev)
	if err != nil {
		t.Fatalf("MountRW: %v", err)
	}

	if err := fs.Mkdir("/notempty", ext2.PermAll755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := fs.WriteFile("/notempty/file.txt", []byte("x"), ext2.PermAll644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	err = fs.Remove("/notempty")
	if err != ext2.ErrNotEmpty {
		t.Errorf("expected ErrNotEmpty, got %v", err)
	}

	dev.Close()
}

func TestRename(t *testing.T) {
	dir := t.TempDir()
	img := buildEmptyImage(t, dir, 4, "rename")

	dev := openRWBlockDev(t, img)

	fs, err := ext2.MountRW(dev)
	if err != nil {
		t.Fatalf("MountRW: %v", err)
	}

	testData := []byte("rename test data\n")
	if err := fs.WriteFile("/old.txt", testData, ext2.PermAll644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := fs.Rename("/old.txt", "/new.txt"); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	if err := fs.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	dev.Close()

	output := runE2fsck(t, img)
	t.Logf("e2fsck output:\n%s", output)

	// Verify old is gone, new exists with correct data
	dev2 := openBlockDev(t, img)
	defer dev2.Close()
	fs2, err := ext2.Mount(dev2)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}

	_, err = fs2.Open("/old.txt")
	if err != ext2.ErrNotFound {
		t.Errorf("old.txt should be gone, got %v", err)
	}

	f, err := fs2.Open("/new.txt")
	if err != nil {
		t.Fatalf("Open new.txt: %v", err)
	}
	data, err := f.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(data, testData) {
		t.Errorf("new.txt: got %q, want %q", data, testData)
	}
	f.Close()
}

func TestRenameCrossDir(t *testing.T) {
	dir := t.TempDir()
	img := buildEmptyImage(t, dir, 4, "renamexdir")

	dev := openRWBlockDev(t, img)

	fs, err := ext2.MountRW(dev)
	if err != nil {
		t.Fatalf("MountRW: %v", err)
	}

	if err := fs.Mkdir("/src", ext2.PermAll755); err != nil {
		t.Fatalf("Mkdir /src: %v", err)
	}
	if err := fs.Mkdir("/dst", ext2.PermAll755); err != nil {
		t.Fatalf("Mkdir /dst: %v", err)
	}

	testData := []byte("cross-dir rename\n")
	if err := fs.WriteFile("/src/file.txt", testData, ext2.PermAll644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := fs.Rename("/src/file.txt", "/dst/moved.txt"); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	if err := fs.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	dev.Close()

	output := runE2fsck(t, img)
	t.Logf("e2fsck output:\n%s", output)

	dev2 := openBlockDev(t, img)
	defer dev2.Close()
	fs2, err := ext2.Mount(dev2)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}

	f, err := fs2.Open("/dst/moved.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	data, err := f.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(data, testData) {
		t.Errorf("got %q, want %q", data, testData)
	}
	f.Close()
}

func TestSetTimes(t *testing.T) {
	dir := t.TempDir()
	img := buildEmptyImage(t, dir, 4, "settimes")

	dev := openRWBlockDev(t, img)

	fs, err := ext2.MountRW(dev)
	if err != nil {
		t.Fatalf("MountRW: %v", err)
	}

	if err := fs.WriteFile("/timed.txt", []byte("test\n"), ext2.PermAll644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	atime := uint32(1711000000)
	mtime := uint32(1711000100)
	if err := fs.SetTimes("/timed.txt", atime, mtime); err != nil {
		t.Fatalf("SetTimes: %v", err)
	}

	if err := fs.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	dev.Close()

	output := runE2fsck(t, img)
	t.Logf("e2fsck output:\n%s", output)

	// Read back and verify timestamps
	dev2 := openBlockDev(t, img)
	defer dev2.Close()
	fs2, err := ext2.Mount(dev2)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}

	entries, err := fs2.ReadDir(ext2.InodeRoot)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var fileInum uint32
	for _, e := range entries {
		if e.Name == "timed.txt" {
			fileInum = e.Inode
		}
	}
	if fileInum == 0 {
		t.Fatal("timed.txt not found")
	}

	inode, err := fs2.ReadInode(fileInum)
	if err != nil {
		t.Fatalf("ReadInode: %v", err)
	}
	if inode.ATime != atime {
		t.Errorf("ATime: got %d, want %d", inode.ATime, atime)
	}
	if inode.MTime != mtime {
		t.Errorf("MTime: got %d, want %d", inode.MTime, mtime)
	}
}

func TestWriteLargeFile(t *testing.T) {
	dir := t.TempDir()
	img := buildEmptyImage(t, dir, 4, "writelarge")

	dev := openRWBlockDev(t, img)

	fs, err := ext2.MountRW(dev)
	if err != nil {
		t.Fatalf("MountRW: %v", err)
	}

	// 200KB — needs indirect blocks
	bigData := make([]byte, 200*1024)
	for i := range bigData {
		bigData[i] = byte(i % 251)
	}

	if err := fs.WriteFile("/big.dat", bigData, ext2.PermAll644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := fs.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	dev.Close()

	output := runE2fsck(t, img)
	t.Logf("e2fsck output:\n%s", output)

	// Read back and verify
	dev2 := openBlockDev(t, img)
	defer dev2.Close()
	fs2, err := ext2.Mount(dev2)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}

	f, err := fs2.Open("/big.dat")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	data, err := f.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(data, bigData) {
		for i := range data {
			if i >= len(bigData) || data[i] != bigData[i] {
				t.Fatalf("mismatch at byte %d: got 0x%02X, want 0x%02X", i, data[i], bigData[i])
			}
		}
		t.Fatalf("length mismatch: got %d, want %d", len(data), len(bigData))
	}
}

func TestOverwriteFile(t *testing.T) {
	dir := t.TempDir()
	img := buildEmptyImage(t, dir, 4, "overwrite")

	dev := openRWBlockDev(t, img)

	fs, err := ext2.MountRW(dev)
	if err != nil {
		t.Fatalf("MountRW: %v", err)
	}

	// Write initial version
	if err := fs.WriteFile("/data.txt", []byte("original content\n"), ext2.PermAll644); err != nil {
		t.Fatalf("WriteFile (original): %v", err)
	}

	// Overwrite with new content
	newData := []byte("updated content with more data\n")
	if err := fs.WriteFile("/data.txt", newData, ext2.PermAll644); err != nil {
		t.Fatalf("WriteFile (overwrite): %v", err)
	}

	if err := fs.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	dev.Close()

	output := runE2fsck(t, img)
	t.Logf("e2fsck output:\n%s", output)

	dev2 := openBlockDev(t, img)
	defer dev2.Close()
	fs2, err := ext2.Mount(dev2)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}

	f, err := fs2.Open("/data.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	data, err := f.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(data, newData) {
		t.Errorf("got %q, want %q", data, newData)
	}
	f.Close()
}

func TestFileWrite(t *testing.T) {
	dir := t.TempDir()
	img := buildEmptyImage(t, dir, 4, "filewrite")

	dev := openRWBlockDev(t, img)

	fs, err := ext2.MountRW(dev)
	if err != nil {
		t.Fatalf("MountRW: %v", err)
	}

	// Create file and write in chunks
	f, err := fs.Create("/streamed.txt", ext2.PermAll644)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	chunks := []string{"Hello, ", "world! ", "This is ", "streamed writing.\n"}
	for _, chunk := range chunks {
		n, err := f.Write([]byte(chunk))
		if err != nil {
			t.Fatalf("Write: %v", err)
		}
		if n != len(chunk) {
			t.Errorf("Write: got %d, want %d", n, len(chunk))
		}
	}
	f.Close()

	if err := fs.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	dev.Close()

	output := runE2fsck(t, img)
	t.Logf("e2fsck output:\n%s", output)

	// Read back
	dev2 := openBlockDev(t, img)
	defer dev2.Close()
	fs2, err := ext2.Mount(dev2)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}

	f2, err := fs2.Open("/streamed.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	data, err := f2.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	expected := "Hello, world! This is streamed writing.\n"
	if string(data) != expected {
		t.Errorf("got %q, want %q", data, expected)
	}
	f2.Close()
}
