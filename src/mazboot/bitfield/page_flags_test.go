package bitfield

import (
	"fmt"
	"testing"
)

func TestPackPageFlags(t *testing.T) {
	tests := []struct {
		name     string
		flags    PageFlags
		expected uint32
		wantErr  bool
	}{
		{
			name: "all flags false",
			flags: PageFlags{
				Allocated:  false,
				KernelPage: false,
				Reserved:   0,
			},
			expected: 0x00000000,
			wantErr:  false,
		},
		{
			name: "only allocated",
			flags: PageFlags{
				Allocated:  true,
				KernelPage: false,
				Reserved:   0,
			},
			expected: 0x00000001, // bit 0 set
			wantErr:  false,
		},
		{
			name: "only kernel page",
			flags: PageFlags{
				Allocated:  false,
				KernelPage: true,
				Reserved:   0,
			},
			expected: 0x00000002, // bit 1 set
			wantErr:  false,
		},
		{
			name: "both allocated and kernel",
			flags: PageFlags{
				Allocated:  true,
				KernelPage: true,
				Reserved:   0,
			},
			expected: 0x00000003, // bits 0 and 1 set
			wantErr:  false,
		},
		{
			name: "with reserved bits",
			flags: PageFlags{
				Allocated:  true,
				KernelPage: false,
				Reserved:   0x12345678, // Some value in reserved field
			},
			expected: 0x48D159E1, // bit 0 set + reserved shifted left by 2
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			packed, err := PackPageFlags(tt.flags)
			if (err != nil) != tt.wantErr {
				t.Errorf("PackPageFlags() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if packed != tt.expected {
				t.Errorf("PackPageFlags() = 0x%08x, want 0x%08x", packed, tt.expected)
				t.Logf("  Flags: Allocated=%v, KernelPage=%v, Reserved=0x%x", 
					tt.flags.Allocated, tt.flags.KernelPage, tt.flags.Reserved)
			}
		})
	}
}

func TestUnpackPageFlags(t *testing.T) {
	tests := []struct {
		name     string
		packed   uint32
		expected PageFlags
	}{
		{
			name:   "all zeros",
			packed: 0x00000000,
			expected: PageFlags{
				Allocated:  false,
				KernelPage: false,
				Reserved:   0,
			},
		},
		{
			name:   "bit 0 set (allocated)",
			packed: 0x00000001,
			expected: PageFlags{
				Allocated:  true,
				KernelPage: false,
				Reserved:   0,
			},
		},
		{
			name:   "bit 1 set (kernel page)",
			packed: 0x00000002,
			expected: PageFlags{
				Allocated:  false,
				KernelPage: true,
				Reserved:   0,
			},
		},
		{
			name:   "bits 0 and 1 set",
			packed: 0x00000003,
			expected: PageFlags{
				Allocated:  true,
				KernelPage: true,
				Reserved:   0,
			},
		},
		{
			name:   "with reserved bits",
			packed: 0x48D159E1,
			expected: PageFlags{
				Allocated:  true,
				KernelPage: false,
				Reserved:   0x12345678, // Extracted from bits 2-31
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := UnpackPageFlags(tt.packed)
			if got.Allocated != tt.expected.Allocated {
				t.Errorf("UnpackPageFlags() Allocated = %v, want %v", got.Allocated, tt.expected.Allocated)
			}
			if got.KernelPage != tt.expected.KernelPage {
				t.Errorf("UnpackPageFlags() KernelPage = %v, want %v", got.KernelPage, tt.expected.KernelPage)
			}
			if got.Reserved != tt.expected.Reserved {
				t.Errorf("UnpackPageFlags() Reserved = 0x%x, want 0x%x", got.Reserved, tt.expected.Reserved)
			}
		})
	}
}

func TestPackUnpackRoundTrip(t *testing.T) {
	// Test that packing and unpacking preserves all values
	testCases := []PageFlags{
		{Allocated: false, KernelPage: false, Reserved: 0},
		{Allocated: true, KernelPage: false, Reserved: 0},
		{Allocated: false, KernelPage: true, Reserved: 0},
		{Allocated: true, KernelPage: true, Reserved: 0},
		{Allocated: true, KernelPage: false, Reserved: 0x12345678},
		{Allocated: false, KernelPage: true, Reserved: 0x2ABCDEF0}, // Max 30 bits: 0x3FFFFFFF
		{Allocated: true, KernelPage: true, Reserved: 0x3FFFFFFF}, // Maximum 30-bit value
	}

	for i, original := range testCases {
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			packed, err := PackPageFlags(original)
			if err != nil {
				t.Fatalf("PackPageFlags() error = %v", err)
			}

			unpacked := UnpackPageFlags(packed)

			if unpacked.Allocated != original.Allocated {
				t.Errorf("RoundTrip Allocated: got %v, want %v", unpacked.Allocated, original.Allocated)
			}
			if unpacked.KernelPage != original.KernelPage {
				t.Errorf("RoundTrip KernelPage: got %v, want %v", unpacked.KernelPage, original.KernelPage)
			}
			// Note: Reserved might not preserve all 30 bits perfectly due to masking
			// But it should preserve the lower 30 bits
			expectedReserved := original.Reserved & 0x3FFFFFFF
			if unpacked.Reserved != expectedReserved {
				t.Errorf("RoundTrip Reserved: got 0x%x, want 0x%x", unpacked.Reserved, expectedReserved)
			}
		})
	}
}

func ExamplePackPageFlags() {
	// Create page flags
	flags := PageFlags{
		Allocated:  true,
		KernelPage: false,
		Reserved:   0,
	}

	// Pack into uint32
	packed, err := PackPageFlags(flags)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Packed flags: 0x%08x\n", packed)

	// Unpack back
	unpacked := UnpackPageFlags(packed)
	fmt.Printf("Unpacked - Allocated: %v, KernelPage: %v\n",
		unpacked.Allocated, unpacked.KernelPage)

	// Output:
	// Packed flags: 0x00000001
	// Unpacked - Allocated: true, KernelPage: false
}

