//go:build riscv64

package ksyscall

import (
	"mazzy/kmazarin/klog"
	"mazzy/kmazarin/kmem"
	"unsafe"
)

// RISC-V riscv_hwprobe syscall implementation.
// Parses ISA extensions from the DTB "riscv,isa" string at boot time and
// responds to hwprobe queries with the detected capabilities.

// hwprobe key constants (from Linux UAPI).
const (
	hwprobeKeyMVendorID     = 0
	hwprobeKeyMArchID       = 1
	hwprobeKeyMImpID        = 2
	hwprobeKeyBaseBehavior  = 3
	hwprobeKeyIMAExt0       = 4
	hwprobeKeyCPUPerf0      = 5
	hwprobeKeyZicbozBlkSize = 6
)

// IMA_EXT_0 extension bits (from Linux UAPI).
const (
	hwprobeIMAFD       uint64 = 0x1
	hwprobeIMAC        uint64 = 0x2
	hwprobeIMAV        uint64 = 0x4
	hwprobeExtZBA      uint64 = 0x8
	hwprobeExtZBB      uint64 = 0x10
	hwprobeExtZBS      uint64 = 0x20
	hwprobeExtZICBOZ   uint64 = 0x40
	hwprobeExtZBC      uint64 = 0x80
	hwprobeExtZBKB     uint64 = 0x100
	hwprobeExtZBKC     uint64 = 0x200
	hwprobeExtZBKX     uint64 = 0x400
	hwprobeExtZKND     uint64 = 0x800
	hwprobeExtZKNE     uint64 = 0x1000
	hwprobeExtZKNH     uint64 = 0x2000
	hwprobeExtZKSED    uint64 = 0x4000
	hwprobeExtZKSH     uint64 = 0x8000
	hwprobeExtZKT      uint64 = 0x10000
	hwprobeExtZFH      uint64 = 0x8000000
	hwprobeExtZFHMIN   uint64 = 0x10000000
	hwprobeExtZFA      uint64 = 0x100000000
	hwprobeExtZICOND   uint64 = 0x800000000
)

// BASE_BEHAVIOR value.
const hwprobeBaseBehaviorIMA uint64 = 1

// CPUPERF_0 values.
const hwprobeMisalignedFast uint64 = 3

// Cached hwprobe response values, populated at boot by InitHWProbe.
var (
	hwprobeIMAExt0  uint64 // IMA_EXT_0 bitmask
	hwprobeCPUPerf0 uint64 // CPUPERF_0 value
)

// hwprobeExtMap maps DTB ISA extension names to IMA_EXT_0 bits.
var hwprobeExtMap = [...]struct {
	name string
	bit  uint64
}{
	{"zba", hwprobeExtZBA},
	{"zbb", hwprobeExtZBB},
	{"zbs", hwprobeExtZBS},
	{"zbc", hwprobeExtZBC},
	{"zicboz", hwprobeExtZICBOZ},
	{"zbkb", hwprobeExtZBKB},
	{"zbkc", hwprobeExtZBKC},
	{"zbkx", hwprobeExtZBKX},
	{"zknd", hwprobeExtZKND},
	{"zkne", hwprobeExtZKNE},
	{"zknh", hwprobeExtZKNH},
	{"zksed", hwprobeExtZKSED},
	{"zksh", hwprobeExtZKSH},
	{"zkt", hwprobeExtZKT},
	{"zfh", hwprobeExtZFH},
	{"zfhmin", hwprobeExtZFHMIN},
	{"zfa", hwprobeExtZFA},
	{"zicond", hwprobeExtZICOND},
}

// InitHWProbe parses a RISC-V ISA string (from the DTB "riscv,isa" property)
// and builds the cached hwprobe response values.
//
// The ISA string has the form "rv64imafdch_zba_zbb_zbs_zicboz..." where
// single-letter extensions follow "rv64" and multi-letter extensions are
// separated by underscores.
func InitHWProbe(isa string) {
	if len(isa) < 4 {
		return
	}

	var ext uint64

	// Parse single-letter extensions after "rv64" or "rv32".
	// Skip the "rv" prefix and width digits.
	i := 0
	for i < len(isa) && isa[i] != 'i' {
		i++
	}
	for i < len(isa) && isa[i] != '_' {
		switch isa[i] {
		case 'f', 'd':
			ext |= hwprobeIMAFD
		case 'c':
			ext |= hwprobeIMAC
		case 'v':
			ext |= hwprobeIMAV
		}
		i++
	}

	// Parse multi-letter extensions separated by underscores.
	for i < len(isa) {
		if isa[i] == '_' {
			i++
		}
		// Extract extension name until next '_' or end.
		start := i
		for i < len(isa) && isa[i] != '_' {
			i++
		}
		if start == i {
			continue
		}
		extName := isa[start:i]

		// Strip version suffix (e.g., "zba1p0" → "zba").
		// Version starts at the first digit after at least one alpha char.
		nameEnd := 0
		for nameEnd < len(extName) && (extName[nameEnd] < '0' || extName[nameEnd] > '9') {
			nameEnd++
		}
		if nameEnd > 0 {
			extName = extName[:nameEnd]
		}

		for j := range hwprobeExtMap {
			if extName == hwprobeExtMap[j].name {
				ext |= hwprobeExtMap[j].bit
				break
			}
		}
	}

	hwprobeIMAExt0 = ext
	// QEMU virt machine supports fast misaligned access.
	hwprobeCPUPerf0 = hwprobeMisalignedFast
	klog.Logf("[HWProbe] IMA_EXT_0=0x%x CPUPERF_0=0x%x\n", hwprobeIMAExt0, hwprobeCPUPerf0)
}

// hwprobePair matches the Linux riscv_hwprobe struct layout.
type hwprobePair struct {
	Key   int64
	Value uint64
}

// SyscallRiscvHWProbe handles the riscv_hwprobe syscall (number 258).
// Reads an array of key/value pairs from userspace, fills in values for
// known keys, and writes the results back. Unknown keys get key=-1, value=0.
//
// Args: pairs_ptr, pair_count, cpu_count, cpus_ptr, flags
func SyscallRiscvHWProbe(pairsPtr, pairCount, cpuCount, cpusPtr, flags, _ uint64) int64 {
	if pairCount == 0 {
		return 0
	}
	// Reasonable limit to avoid excessive stack/heap usage.
	if pairCount > 64 {
		return -22 // EINVAL
	}

	pairSize := uint64(unsafe.Sizeof(hwprobePair{})) // 16 bytes
	totalSize := pairCount * pairSize

	// Read pairs from userspace.
	var buf [64 * 16]byte // max 64 pairs × 16 bytes
	if !kmem.CopyFromUser(buf[:totalSize], uintptr(pairsPtr), int(totalSize)) {
		return -14 // EFAULT
	}

	pairs := unsafe.Slice((*hwprobePair)(unsafe.Pointer(&buf[0])), int(pairCount))

	for idx := uint64(0); idx < pairCount; idx++ {
		switch pairs[idx].Key {
		case hwprobeKeyMVendorID:
			pairs[idx].Value = 0 // QEMU doesn't set a vendor ID
		case hwprobeKeyMArchID:
			pairs[idx].Value = 0
		case hwprobeKeyMImpID:
			pairs[idx].Value = 0
		case hwprobeKeyBaseBehavior:
			pairs[idx].Value = hwprobeBaseBehaviorIMA
		case hwprobeKeyIMAExt0:
			pairs[idx].Value = hwprobeIMAExt0
		case hwprobeKeyCPUPerf0:
			pairs[idx].Value = hwprobeCPUPerf0
		case hwprobeKeyZicbozBlkSize:
			if hwprobeIMAExt0&hwprobeExtZICBOZ != 0 {
				pairs[idx].Value = 64 // QEMU uses 64-byte cache blocks
			} else {
				pairs[idx].Value = 0
			}
		default:
			pairs[idx].Key = -1
			pairs[idx].Value = 0
		}
	}

	// Write results back to userspace.
	if !kmem.CopyToUser(uintptr(pairsPtr), buf[:totalSize]) {
		return -14 // EFAULT
	}

	return 0
}
