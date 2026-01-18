# Plan: FAT32-based Program Loader

## Overview

Create a simple loader that reads ELF programs from a FAT32 filesystem attached via VirtIO block device. This mirrors how real operating systems load programs.

## Architecture

```
Build Time:
  priest.elf, helloworld.elf
           ↓
  create-disk tool (go-diskfs)
           ↓
  mazzy-disk.img (FAT32, ~16MB)
           ↓
  QEMU: -drive file=mazzy-disk.img,if=virtio,format=raw

Runtime:
  kmazarin boots
       ↓
  VirtIO block driver discovers disk
       ↓
  FAT32 filesystem mounted
       ↓
  Priest loaded from /priest.elf
       ↓
  Priest runs, loads /helloworld.elf
```

## Components

### 1. VirtIO Block Driver

**Status**: Partially exists in `kmazarin/virtio/`

**Location**: `src/kmazarin/golang/virtio/block.go`

**Required capabilities**:
- Device discovery via MMIO
- Read sectors (512-byte blocks)
- Write sectors (for future use)

**Implementation sketch**:
```go
package virtio

type BlockDevice struct {
    base      uint64     // MMIO base address
    capacity  uint64     // Total sectors
    blockSize uint32     // Bytes per sector (512)
}

func (d *BlockDevice) ReadSectors(startSector, count uint64, buf []byte) error
func (d *BlockDevice) WriteSectors(startSector, count uint64, buf []byte) error
```

### 2. FAT32 Filesystem

**Library**: `github.com/diskfs/go-diskfs/filesystem/fat32`

**Challenge**: go-diskfs uses `io.ReadSeeker` interface, needs block device adapter.

**Adapter**:
```go
type BlockReadSeeker struct {
    device *virtio.BlockDevice
    pos    int64
}

func (b *BlockReadSeeker) Read(p []byte) (n int, err error)
func (b *BlockReadSeeker) Seek(offset int64, whence int) (int64, error)
```

**Alternative**: Minimal FAT32 implementation
- FAT32 is relatively simple
- Only need: open file, read file, list directory
- Could write ~500 lines of Go instead of importing library

**Recommendation**: Start with minimal implementation, consider go-diskfs later.

### 3. Disk Image Creation Tool

**Tool**: `tools/create-disk.go`

**Function**: Creates a FAT32 disk image with ELF files

**Usage**:
```bash
build/tools/create-disk -o build/mazzy-disk.img \
    -add build/priest.elf:/priest.elf \
    -add build/helloworld.elf:/helloworld.elf
```

**Implementation**:
```go
package main

import (
    "github.com/diskfs/go-diskfs"
    "github.com/diskfs/go-diskfs/filesystem/fat32"
)

func main() {
    // Create 16MB disk image
    disk, _ := diskfs.Create(outputPath, 16*1024*1024, diskfs.Raw)

    // Create FAT32 filesystem
    fs, _ := disk.CreateFilesystem(0, fat32.Type)

    // Add files
    for _, file := range filesToAdd {
        f, _ := fs.OpenFile(file.destPath, os.O_CREATE|os.O_RDWR)
        io.Copy(f, srcFile)
        f.Close()
    }
}
```

### 4. ELF Loader

**Location**: `src/kmazarin/golang/loader/elf.go`

**Function**: Loads ELF binary into memory, sets up execution

**Responsibilities**:
1. Read ELF header from filesystem
2. Parse program headers (PT_LOAD segments)
3. Allocate memory pages
4. Copy segments to memory
5. Set up entry point
6. Handle relocations (for PIE)

**Implementation sketch**:
```go
package loader

type LoadedProgram struct {
    Entry      uint64           // Entry point address
    BaseAddr   uint64           // Load base address
    Segments   []LoadedSegment  // Loaded PT_LOAD segments
    StackTop   uint64           // Top of allocated stack
}

func LoadELF(fs filesystem.FileSystem, path string) (*LoadedProgram, error) {
    // Open file
    f, _ := fs.OpenFile(path, os.O_RDONLY)

    // Read ELF header
    var ehdr elf.Header64
    binary.Read(f, binary.LittleEndian, &ehdr)

    // Read program headers
    phdrs := make([]elf.Prog64, ehdr.Phnum)
    // ...

    // For each PT_LOAD segment:
    for _, phdr := range phdrs {
        if phdr.Type != PT_LOAD {
            continue
        }

        // Allocate pages
        pages := kmem.AllocPages((phdr.Memsz + 4095) / 4096)

        // Read segment from file
        f.Seek(phdr.Off, 0)
        f.Read(pages[:phdr.Filesz])

        // Zero BSS portion
        memset(pages[phdr.Filesz:phdr.Memsz], 0)

        // Map pages at correct virtual address
        kmem.MapPages(phdr.Vaddr, pages, phdr.Flags)
    }

    return &LoadedProgram{Entry: ehdr.Entry}, nil
}
```

### 5. Integration Flow

**Boot sequence**:

```
1. Cardinal boots, loads kmazarin
2. kmazarin initializes:
   - Memory management
   - VirtIO device discovery
   - Block device driver
   - FAT32 filesystem mount
3. kmazarin loads priest:
   - LoadELF("/priest.elf")
   - Allocate stack
   - Create thread context
   - Switch to priest
4. Priest runs:
   - Initialize Go runtime
   - Open filesystem (via syscall)
   - Load helloworld
   - Run helloworld as goroutine
```

## Minimal FAT32 Implementation

For initial simplicity, implement minimal FAT32 directly:

```go
package fat32

// Boot sector at sector 0
type BootSector struct {
    BytesPerSector    uint16  // offset 11
    SectorsPerCluster uint8   // offset 13
    ReservedSectors   uint16  // offset 14
    NumFATs           uint8   // offset 16
    TotalSectors32    uint32  // offset 32
    FATSize32         uint32  // offset 36
    RootCluster       uint32  // offset 44
}

// Directory entry (32 bytes)
type DirEntry struct {
    Name       [11]byte  // 8.3 format
    Attr       uint8     // 0x10 = directory, 0x20 = file
    // ...
    ClusterHi  uint16    // High 16 bits of cluster
    // ...
    ClusterLo  uint16    // Low 16 bits of cluster
    Size       uint32    // File size
}

type FileSystem struct {
    device        BlockReader
    boot          BootSector
    fatStart      uint64  // First FAT sector
    dataStart     uint64  // First data sector
    bytesPerClus  uint64
}

func (fs *FileSystem) Open(path string) (*File, error)
func (fs *FileSystem) ReadDir(path string) ([]DirEntry, error)

type File struct {
    fs           *FileSystem
    firstCluster uint32
    size         uint32
    pos          uint64
}

func (f *File) Read(p []byte) (n int, err error)
func (f *File) Seek(offset int64, whence int) (int64, error)
```

**FAT32 key concepts**:
- Data stored in clusters (multiple sectors)
- FAT = array of cluster chain links
- Root directory at cluster N (from boot sector)
- Files are chains of clusters linked via FAT

## QEMU Configuration

Add to run script:
```bash
QEMU_ARGS="$QEMU_ARGS -drive file=build/mazzy-disk.img,if=virtio,format=raw"
```

VirtIO block device will appear in device tree at discovery.

## Files to Create

| File | Purpose |
|------|---------|
| `tools/create-disk.go` | Build tool to create FAT32 disk image |
| `src/kmazarin/golang/virtio/block.go` | VirtIO block device driver |
| `src/kmazarin/golang/fs/fat32/fat32.go` | Minimal FAT32 implementation |
| `src/kmazarin/golang/loader/elf.go` | ELF loader |

## Files to Modify

| File | Change |
|------|--------|
| `Makefile` | Add disk image creation target |
| `tools/cmd-run.go` | Add -drive argument to QEMU |
| `src/kmazarin/golang/kmazarin/main.go` | Initialize filesystem, load priest |

## Milestones

### Milestone 1: VirtIO Block Read
- [ ] Discover VirtIO block device
- [ ] Read single sector
- [ ] Read multiple sectors
- [ ] Test: read first sector of disk, print bytes

### Milestone 2: FAT32 Read
- [ ] Parse boot sector
- [ ] Read root directory
- [ ] Open file by path
- [ ] Read file contents
- [ ] Test: list files in root, read file contents

### Milestone 3: ELF Loading
- [ ] Parse ELF header
- [ ] Load PT_LOAD segments
- [ ] Allocate stack
- [ ] Create thread context
- [ ] Test: load simple ELF, jump to entry

### Milestone 4: Full Integration
- [ ] Build disk image with priest + helloworld
- [ ] Boot kmazarin, mount disk
- [ ] Load and run priest
- [ ] Priest loads and runs helloworld

## Alternative Approaches Considered

### 1. Embed binaries in kmazarin
- **Pro**: Simple, no filesystem needed
- **Con**: Not extensible, not realistic

### 2. Use initrd/ramfs
- **Pro**: Standard Linux approach
- **Con**: More complex than FAT32 for our needs

### 3. 9P filesystem over VirtIO
- **Pro**: Share host directory directly
- **Con**: More complex protocol, harder to debug

### 4. Simple custom filesystem
- **Pro**: Minimal code
- **Con**: Non-standard, no tooling support

**Recommendation**: FAT32 is the best balance of simplicity and realism.

## Estimated Complexity

| Component | Lines of Go | Notes |
|-----------|-------------|-------|
| VirtIO block driver | ~200 | Mostly register manipulation |
| Minimal FAT32 | ~400 | Boot sector, FAT chain, directory |
| ELF loader | ~300 | Header parsing, segment loading |
| Disk creation tool | ~100 | Uses go-diskfs |
| **Total** | ~1000 | Spread across multiple files |
