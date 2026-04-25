//go:build arm64 || amd64

package main

import (
	"mazzy/kmazarin/proc"
)

// ============================================================================
// VA Translation Cache — Cross-Shepherd Page Sharing
// ============================================================================
//
// Shepherds share memory pages for ring-buffer IPC and font caches.
// When a shepherd maps a page into another shepherd's space via
// SysSharePages, the kernel caches the VA↔VA translation so subsequent
// lookups (e.g. for uring IPC data areas) are fast.
//
// Maps (ownerSID, ownerPageVA) → (targetSID, targetPageVA).

// maxVACacheEntries is the maximum number of cached VA translations.
const maxVACacheEntries = 128

type vaCacheEntry struct {
	inUse     bool
	ownerSID  int16
	targetSID int16
	ownerVA   uintptr // page-aligned VA in owner's space
	targetVA  uintptr // page-aligned VA in target's space
}

var vaCache [maxVACacheEntries]vaCacheEntry

// addVACacheEntry adds a new VA translation to the cache.
// Returns false if cache is full.
func addVACacheEntry(ownerSID, targetSID int16, ownerVA, targetVA uintptr) bool {
	for i := range vaCache {
		if !vaCache[i].inUse {
			vaCache[i] = vaCacheEntry{
				inUse:     true,
				ownerSID:  ownerSID,
				targetSID: targetSID,
				ownerVA:   ownerVA,
				targetVA:  targetVA,
			}
			return true
		}
	}
	return false
}

// lookupVACache finds the target VA for a given owner's VA.
// Returns (targetVA, true) on hit, (0, false) on miss.
//
//go:nosplit
func lookupVACache(ownerSID, targetSID int16, ownerPageVA uintptr) (uintptr, bool) {
	for i := range vaCache {
		if vaCache[i].inUse &&
			vaCache[i].ownerSID == ownerSID &&
			vaCache[i].targetSID == targetSID &&
			vaCache[i].ownerVA == ownerPageVA {
			return vaCache[i].targetVA, true
		}
	}
	return 0, false
}

// cleanupVACacheForShepherd removes all cache entries involving a shepherd.
func cleanupVACacheForShepherd(sid int16) {
	for i := range vaCache {
		if vaCache[i].inUse && (vaCache[i].ownerSID == sid || vaCache[i].targetSID == sid) {
			vaCache[i].inUse = false
		}
	}
}

// ============================================================================
// Exported Functions (called from ksyscall via linkname)
// ============================================================================

// AddVACacheEntry adds a VA translation cache entry. Called from ksyscall.
func AddVACacheEntry(ownerSID, targetSID int16, ownerVA, targetVA uintptr) bool {
	return addVACacheEntry(ownerSID, targetSID, ownerVA, targetVA)
}

// LookupVACache looks up a cached VA translation. Called from ksyscall.
//
//go:nosplit
func LookupVACache(ownerSID, targetSID int16, ownerPageVA uintptr) (uintptr, bool) {
	return lookupVACache(ownerSID, targetSID, ownerPageVA)
}

// CleanupPageSharingForShepherd clears VA cache entries for a dying shepherd.
func CleanupPageSharingForShepherd(shepherdID int16) {
	sid := int(shepherdID)
	if sid < 0 || sid >= proc.MaxShepherds {
		return
	}
	cleanupVACacheForShepherd(shepherdID)
}
