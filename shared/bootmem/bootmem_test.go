package bootmem

import "testing"

const fourGB = uint64(0x1_0000_0000)

func TestLargestContiguousRAM(t *testing.T) {
	tests := []struct {
		name     string
		regions  []Region
		limit    uint64
		wantBase uint64
		wantSize uint64
		wantOK   bool
	}{
		{
			// The MAZ-136 bug case: q35 with -m 8G reports usable RAM as a low
			// range [1MB,2GB) and a high range [4GB,10GB) with the 2-4GB PCI MMIO
			// hole between them. The old [min,max) span would return size=10GB,
			// swallowing the hole. With limit=4GB (the amd64 linear-map cap) we
			// must get ONLY the low contiguous run (~2GB).
			name: "q35 8GB excludes PCI hole and unaddressable high RAM",
			regions: []Region{
				{Start: 0, End: 0xa0000},                   // tiny low fragment (640KB)
				{Start: 0x100000, End: 0x8000_0000},        // low RAM 1MB..2GB
				{Start: 0x1_0000_0000, End: 0x2_8000_0000}, // high RAM 4GB..10GB
			},
			limit:    fourGB,
			wantBase: 0x100000,
			wantSize: 0x8000_0000 - 0x100000,
			wantOK:   true,
		},
		{
			// ARM64 qemu virt: one contiguous region from 1GB; no hole. With no
			// effective cap the whole run is returned (matches old [min,max)).
			name:     "arm64 contiguous RAM, no cap",
			regions:  []Region{{Start: 0x4000_0000, End: 0x2_4000_0000}},
			limit:    ^uint64(0),
			wantBase: 0x4000_0000,
			wantSize: 0x2_0000_0000,
			wantOK:   true,
		},
		{
			name:     "adjacent regions merge",
			regions:  []Region{{Start: 0x1000, End: 0x2000}, {Start: 0x2000, End: 0x3000}},
			limit:    ^uint64(0),
			wantBase: 0x1000,
			wantSize: 0x2000,
			wantOK:   true,
		},
		{
			name:     "gap splits; largest run wins",
			regions:  []Region{{Start: 0, End: 0x1000}, {Start: 0x2000, End: 0x5000}},
			limit:    ^uint64(0),
			wantBase: 0x2000,
			wantSize: 0x3000,
			wantOK:   true,
		},
		{
			name:     "limit caps a run end",
			regions:  []Region{{Start: 0x1000, End: 0x5000}},
			limit:    0x3000,
			wantBase: 0x1000,
			wantSize: 0x2000,
			wantOK:   true,
		},
		{
			name:    "region entirely at/above limit is skipped",
			regions: []Region{{Start: 0x1_0000_0000, End: 0x2_0000_0000}},
			limit:   fourGB,
			wantOK:  false,
		},
		{
			name:     "overlapping regions merge",
			regions:  []Region{{Start: 0, End: 0x3000}, {Start: 0x1000, End: 0x5000}},
			limit:    ^uint64(0),
			wantBase: 0,
			wantSize: 0x5000,
			wantOK:   true,
		},
		{
			name:    "empty input",
			regions: nil,
			limit:   ^uint64(0),
			wantOK:  false,
		},
		{
			name:    "degenerate regions (end<=start) ignored",
			regions: []Region{{Start: 0x5000, End: 0x5000}, {Start: 0x9000, End: 0x1000}},
			limit:   ^uint64(0),
			wantOK:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base, size, ok := LargestContiguousRAM(tc.regions, tc.limit)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if base != tc.wantBase || size != tc.wantSize {
				t.Errorf("got base=%#x size=%#x, want base=%#x size=%#x",
					base, size, tc.wantBase, tc.wantSize)
			}
		})
	}
}
