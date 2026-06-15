// Package bootmem holds architecture-neutral physical-memory-map helpers shared
// between diplomat (the bootloader) and its tests.
package bootmem

// Region is one half-open span [Start, End) of usable physical RAM, as reported
// by the firmware memory map.
type Region struct {
	Start uint64
	End   uint64
}

// LargestContiguousRAM returns the base and size of the largest run of usable
// RAM that is contiguous and lies below limit. Region ends are clamped to limit,
// and regions that start at or above limit are ignored. Returns ok=false when no
// usable region lies below limit.
//
// This replaces the naive "lowest start .. highest end" span. On q35 with more
// than 2GB of RAM the firmware reports usable RAM as two ranges — low [0,2GB)
// and high [4GB,…) — separated by the 2–4GB PCI MMIO hole; spanning them would
// treat the hole as RAM. limit is the caller's maximum linearly-mappable
// physical address: amd64 passes the 4GB linear-map cap (which also drops the
// unaddressable high RAM); callers that can map all RAM pass ^uint64(0).
func LargestContiguousRAM(regions []Region, limit uint64) (base, size uint64, ok bool) {
	for i := range regions {
		s, e := clamp(regions[i], limit)
		if s >= e {
			continue // degenerate or entirely at/above limit
		}
		// Grow [s,e) into the maximal contiguous run containing region i by
		// repeatedly merging any region that overlaps or abuts it. O(n^2) over a
		// firmware map of at most a few hundred entries.
		for {
			grew := false
			for j := range regions {
				rs, re := clamp(regions[j], limit)
				if rs >= re {
					continue
				}
				if rs <= e && re >= s { // overlaps or is adjacent to [s,e)
					if rs < s {
						s, grew = rs, true
					}
					if re > e {
						e, grew = re, true
					}
				}
			}
			if !grew {
				break
			}
		}
		if e-s > size {
			base, size, ok = s, e-s, true
		}
	}
	return base, size, ok
}

// clamp returns the region's span with End limited to limit.
func clamp(r Region, limit uint64) (start, end uint64) {
	return r.Start, min(r.End, limit)
}
