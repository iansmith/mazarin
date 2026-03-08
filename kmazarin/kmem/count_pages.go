package kmem

import "unsafe"

// CountMappedPages walks a priest's page table hierarchy and returns the
// number of valid leaf pages (4KB pages at L3 level). Block entries at
// L1 (1GB) and L2 (2MB) are counted as their equivalent 4KB page count.
func CountMappedPages(l0PA uintptr) int {
	count := 0

	l0VA := paToVAOrCache(l0PA)
	if l0VA == 0 {
		return 0
	}

	maxEntry := platformUserL0MaxEntry()

	for i := 0; i < maxEntry; i++ {
		l0e := *(*uint64)(unsafe.Pointer(l0VA + uintptr(i)*8))
		if !pteIsValid(l0e) {
			continue
		}

		l1PA := pteExtractPA(l0e)
		l1VA := paToVAOrCache(l1PA)
		if l1VA == 0 {
			continue
		}

		for j := 0; j < 512; j++ {
			l1e := *(*uint64)(unsafe.Pointer(l1VA + uintptr(j)*8))
			if !pteIsValid(l1e) {
				continue
			}
			if isBlockEntry(l1e, 1) {
				count += 512 * 512 // 1GB = 262144 4KB pages
				continue
			}

			l2PA := pteExtractPA(l1e)
			l2VA := paToVAOrCache(l2PA)
			if l2VA == 0 {
				continue
			}

			for k := 0; k < 512; k++ {
				l2e := *(*uint64)(unsafe.Pointer(l2VA + uintptr(k)*8))
				if !pteIsValid(l2e) {
					continue
				}
				if isBlockEntry(l2e, 2) {
					count += 512 // 2MB = 512 4KB pages
					continue
				}

				l3PA := pteExtractPA(l2e)
				l3VA := paToVAOrCache(l3PA)
				if l3VA == 0 {
					continue
				}

				for m := 0; m < 512; m++ {
					l3e := *(*uint64)(unsafe.Pointer(l3VA + uintptr(m)*8))
					if pteIsValid(l3e) {
						count++
					}
				}
			}
		}
	}

	return count
}
