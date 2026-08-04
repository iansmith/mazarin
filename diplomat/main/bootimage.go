// diplomat/main/bootimage.go - Boot splash image loading (MAZ-178)
//
// Reads /EFI/Linux/bootimg.bin (8.3-safe name) from the ESP into
// UEFI-allocated pages and records its address/size for BuildStartupEnv to
// pass to the kernel via AT_BOOT_IMAGE_PHYS/SIZE. The kernel's GPU init
// renders it once (RenderBootImage). A missing or unreadable image is
// non-fatal: the splash is skipped with one log line.
//
// Image format (matches gpu.RenderBootImage): [4B width][4B height][BGRA].

package main

import (
	"mazzy/shared/fs/fat32"
	"unsafe"
)

// Boot image location, zero when absent. Read by BuildStartupEnv.
var bootImagePhys uint64
var bootImageSize uint64

// LoadBootImage loads the splash image if present. Never fails the boot.
func LoadBootImage(fs *fat32.FileSystem) {
	file := findBootImageFile(fs)
	if file == nil || file.Size == 0 {
		printString("Boot image: none (splash skipped)\r\n")
		return
	}

	pages := (uint64(file.Size) + PageSize - 1) / PageSize
	phys, err := allocatePhysPages(pages)
	if err != nil {
		printString("Boot image: alloc failed (splash skipped)\r\n")
		return
	}

	buf := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(phys))), file.Size)
	n, rerr := readFileAt(fs, file, 0, buf)
	if rerr != nil || uint32(n) != file.Size {
		printString("Boot image: read failed (splash skipped)\r\n")
		return
	}

	bootImagePhys = phys
	bootImageSize = uint64(file.Size)
	printString("Boot image loaded: ")
	printHex(uint64(file.Size))
	printString(" bytes @ ")
	printHex(phys)
	printString("\r\n")
}

// findBootImageFile looks for /EFI/Linux/bootimg.bin. Returns nil when the
// path is absent — callers treat that as "no splash", not an error.
func findBootImageFile(fs *fat32.FileSystem) *SimpleFile {
	cluster := fs.RootCluster()

	entry, err := findInDir(fs, cluster, "EFI")
	if err != nil || !entry.IsDir {
		return nil
	}
	cluster = entry.Cluster

	entry, err = findInDir(fs, cluster, "LINUX")
	if err != nil || !entry.IsDir {
		return nil
	}
	cluster = entry.Cluster

	entry, err = findInDir(fs, cluster, "BOOTIMG.BIN")
	if err != nil {
		return nil
	}

	// Use bump allocator - Go heap allocation crashes in UEFI
	sf := dNew[SimpleFile]()
	if sf == nil {
		return nil
	}
	sf.Cluster = entry.Cluster
	sf.Size = entry.Size
	return sf
}
