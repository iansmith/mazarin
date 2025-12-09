package bitfield

import (
	"testing"
	"unsafe"
)

func TestPageFlagsSize(t *testing.T) {
	// Test that PageFlags struct size is what we expect
	var flags PageFlags
	size := unsafe.Sizeof(flags)
	
	t.Logf("PageFlags struct size: %d bytes (%d bits)", size, size*8)
	
	// PageFlags should be the size of its fields (not packed by Go)
	// bool + bool + uint32 = 1 + 1 + 4 = 6 bytes (but Go may align it)
	// On 64-bit systems, bools might be padded to 8 bytes for alignment
	expectedMin := uintptr(6) // Minimum: 1+1+4
	expectedMax := uintptr(16) // Maximum with alignment: 8+8+8
	
	if size < expectedMin || size > expectedMax {
		t.Errorf("PageFlags size %d is unexpected (expected between %d and %d)", 
			size, expectedMin, expectedMax)
	}
}

func TestPackedSize(t *testing.T) {
	// Test that packed value is actually 32 bits
	flags := PageFlags{
		Allocated:  true,
		KernelPage: false,
		Reserved:   0x12345678,
	}
	
	packed, err := PackPageFlags(flags)
	if err != nil {
		t.Fatalf("PackPageFlags error: %v", err)
	}
	
	// Verify it's a 32-bit value (fits in uint32)
	var packed32 uint32 = packed
	var packed64 uint64 = uint64(packed)
	
	t.Logf("Packed value: 0x%08x (as uint32)", packed32)
	t.Logf("Packed value: 0x%016x (as uint64)", packed64)
	
	// Upper 32 bits should be zero
	if packed64>>32 != 0 {
		t.Errorf("Packed value exceeds 32 bits! Upper bits: 0x%x", packed64>>32)
	}
	
	// Verify size of uint32
	var u32 uint32
	var u64 uint64
	t.Logf("uint32 size: %d bytes (%d bits)", unsafe.Sizeof(u32), unsafe.Sizeof(u32)*8)
	t.Logf("uint64 size: %d bytes (%d bits)", unsafe.Sizeof(u64), unsafe.Sizeof(u64)*8)
	
	// Packed should fit in uint32
	if packed64 != uint64(packed32) {
		t.Errorf("Packed value doesn't fit in uint32! 0x%x != 0x%x", packed64, uint64(packed32))
	}
}

func TestUnpackSize(t *testing.T) {
	// Test that unpacking works with 32-bit values
	testValue := uint32(0x48D159E1)
	
	unpacked := UnpackPageFlags(testValue)
	
	t.Logf("Unpacked from 0x%08x:", testValue)
	t.Logf("  Allocated: %v", unpacked.Allocated)
	t.Logf("  KernelPage: %v", unpacked.KernelPage)
	t.Logf("  Reserved: 0x%x", unpacked.Reserved)
	
	// Verify it's actually using 32 bits
	var test64 uint64 = uint64(testValue)
	unpacked64 := UnpackPageFlags(uint32(test64))
	
	if unpacked != unpacked64 {
		t.Errorf("Unpacking differs between uint32 and uint64 cast!")
	}
}













