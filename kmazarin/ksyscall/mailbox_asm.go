package ksyscall

import (
	_ "unsafe" // for go:linkname
)

// Forward declarations for VA cache functions in the main package.

//go:linkname addVACacheEntry main.AddVACacheEntry
func addVACacheEntry(ownerSID, targetSID int16, ownerVA, targetVA uintptr) bool

//go:linkname lookupVACache main.LookupVACache
func lookupVACache(ownerSID, targetSID int16, ownerPageVA uintptr) (uintptr, bool)
