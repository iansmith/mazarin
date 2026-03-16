// constraint_syscall.go — Kernel syscall handlers for the constraint attribute system.
//
// SysAttrCreate (0x1021), SysAttrWrite (0x1022), SysAttrWriteURI (0x1023),
// SysAttrAddDep (0x1024), SysAttrUpdateDeps (0x1025), SysAttrRegisterQuery (0x1026).

package ksyscall

import (
	"mazzy/kmazarin/kmem"
	"mazzy/mazarin/vm/flat"
	"unsafe"
)

// SyscallAttrCreate creates an attribute with a URI in the shared namespace.
//
// Args: uriBufPtr, uriLen, valueType, attrKind, bytecodeBufPtr, bytecodeLen
// Returns: slot index (>= 0) on success, negative errno on failure.
//
func SyscallAttrCreate(uriBufPtr, uriLen, valueType, attrKind, bytecodeBufPtr, bytecodeLen uint64) int64 {
	if !attrMgr.initialized {
		return -12 // ENOMEM — not initialized
	}

	// Validate URI length.
	if uriLen == 0 || uriLen > maxURILen {
		return -22 // EINVAL
	}

	// Copy URI from user buffer.
	var uriBuf [maxURILen]byte
	if !kmem.CopyFromUser(uriBuf[:uriLen], uintptr(uriBufPtr), int(uriLen)) {
		return -14 // EFAULT
	}
	uri := unsafeStringFromBytes(uriBuf[:uriLen])

	// Validate URI format.
	var segments [maxURISegments]string
	nSeg := parseURI(uri, &segments)
	if nSeg < 2 {
		return -22 // EINVAL — need at least "priest/<name>" or "kernel/<name>"
	}

	// Validate ownership: priests can only create under "priest/<priestName>/..."
	if segments[0] == "kernel" {
		return -1 // EPERM — kernel namespace not accessible via syscall
	}
	if segments[0] != "priest" {
		return -22 // EINVAL — must be under "priest" or "kernel"
	}

	// Validate value type.
	if valueType == 0 || uint8(valueType) > flat.MaxTypeTag {
		return -22 // EINVAL
	}

	// Validate attr kind.
	if attrKind != uint64(flat.AttrKindValue) && attrKind != uint64(flat.AttrKindConstraint) {
		return -22 // EINVAL
	}

	// Check for duplicate URI.
	if _, exists := attrMgr.trieLookup(uri); exists {
		return -17 // EEXIST
	}

	// Allocate a node slot.
	slot := attrMgr.allocNode()
	if slot == 0xFFFF {
		return -12 // ENOMEM
	}

	// Get caller's priest ID.
	pid, _ := getCurrentThreadPIDAndTID()

	// Initialize the node.
	node := attrMgr.node(slot)
	node.Owner = uint16(pid)
	node.Kind = uint8(attrKind)
	node.ValueType = uint8(valueType)
	if attrKind == uint64(flat.AttrKindConstraint) {
		node.Flags = flat.FlagDirty // constraints start dirty
	}

	// Copy bytecode if constraint.
	if attrKind == uint64(flat.AttrKindConstraint) && bytecodeLen > 0 {
		bcOff := attrMgr.bytecodeBumpOff
		needed := uint32(bytecodeLen)
		if bcOff+needed > uint32(kmem.RegionBytecodeSize) {
			attrMgr.freeNode(slot)
			return -12 // ENOMEM
		}
		var bcBuf [4096]byte
		copyLen := int(bytecodeLen)
		if copyLen > len(bcBuf) {
			copyLen = len(bcBuf)
		}
		if !kmem.CopyFromUser(bcBuf[:copyLen], uintptr(bytecodeBufPtr), copyLen) {
			attrMgr.freeNode(slot)
			return -14 // EFAULT
		}
		dst := attrMgr.baseVA + uintptr(attrMgr.bytecodeRegionOff) + uintptr(bcOff)
		for i := 0; i < copyLen; i++ {
			*(*byte)(unsafe.Pointer(dst + uintptr(i))) = bcBuf[i]
		}
		attrMgr.bytecodeBumpOff += needed
		node.ProgramOffset = bcOff
		node.ProgramLen = uint16(bytecodeLen / 16) // instructions are 16 bytes
	}

	// Allocate string slot for the URI name.
	nameOff, ok := attrMgr.allocString(uri)
	if !ok {
		attrMgr.freeNode(slot)
		return -12 // ENOMEM
	}
	node.NameOffset = nameOff

	// Insert into namespace trie.
	rc := attrMgr.trieInsert(uri, slot)
	if rc < 0 {
		attrMgr.freeNode(slot)
		return rc
	}

	// Check registered query patterns for matches.
	attrMgr.updateQueryResultsForURI(uri)

	return int64(slot)
}

// SyscallAttrWrite writes a value to a value attribute by slot index.
//
// Args: slotIndex, valueBufPtr, valueLen, _, _, _
// Returns: 0 on success, negative errno on failure.
//
func SyscallAttrWrite(slotIndex, valueBufPtr, valueLen, _, _, _ uint64) int64 {
	if !attrMgr.initialized {
		return -12
	}

	slot := uint16(slotIndex)
	if !attrMgr.isNodeAllocated(slot) {
		return -22 // EINVAL
	}

	node := attrMgr.node(slot)
	if node.IsTombstoned() {
		return -22 // EINVAL
	}

	// Check ownership.
	pid, _ := getCurrentThreadPIDAndTID()
	if node.Owner != uint16(pid) {
		return -1 // EPERM
	}

	// Cannot write to constraint attributes.
	if node.Kind == flat.AttrKindConstraint {
		return -1 // EPERM
	}

	// Copy FlatValue from user buffer (32 bytes).
	if valueLen != flat.FlatValueSize {
		return -22 // EINVAL
	}
	var valBuf [flat.FlatValueSize]byte
	if !kmem.CopyFromUser(valBuf[:], uintptr(valueBufPtr), flat.FlatValueSize) {
		return -14 // EFAULT
	}
	newVal := *(*flat.FlatValue)(unsafe.Pointer(&valBuf[0]))

	// Validate type matches.
	if newVal.Typ != node.ValueType {
		return -22 // EINVAL
	}

	// Seqlock write: odd = in progress, even = stable.
	node.SeqCounter++
	node.CachedValue = newVal
	node.SeqCounter++

	// Clear dirty flag on this node (it now has a fresh value).
	node.SetDirty(false)

	// Dirty-propagate to dependents.
	attrMgr.dirtyPropagate(slot)

	return 0
}

// SyscallAttrWriteURI writes a value to an attribute by URI string.
//
// Args: uriBufPtr, uriLen, valueBufPtr, valueLen, _, _
// Returns: 0 on success, negative errno on failure.
//
func SyscallAttrWriteURI(uriBufPtr, uriLen, valueBufPtr, valueLen, _, _ uint64) int64 {
	if !attrMgr.initialized {
		return -12
	}

	if uriLen == 0 || uriLen > maxURILen {
		return -22
	}

	var uriBuf [maxURILen]byte
	if !kmem.CopyFromUser(uriBuf[:uriLen], uintptr(uriBufPtr), int(uriLen)) {
		return -14 // EFAULT
	}
	uri := unsafeStringFromBytes(uriBuf[:uriLen])

	slot, found := attrMgr.trieLookup(uri)
	if !found {
		return -2 // ENOENT
	}

	return SyscallAttrWrite(uint64(slot), valueBufPtr, valueLen, 0, 0, 0)
}

// SyscallAttrAddDep adds a single dependency edge: fromSlot depends on toSlot.
//
// Args: fromSlot, toSlot, _, _, _, _
// Returns: 0 on success, negative errno on failure.
//
func SyscallAttrAddDep(fromSlotArg, toSlotArg, _, _, _, _ uint64) int64 {
	if !attrMgr.initialized {
		return -12
	}

	fromSlot := uint16(fromSlotArg)
	toSlot := uint16(toSlotArg)

	if !attrMgr.isNodeAllocated(fromSlot) || !attrMgr.isNodeAllocated(toSlot) {
		return -22 // EINVAL
	}

	fromNode := attrMgr.node(fromSlot)
	toNode := attrMgr.node(toSlot)
	if fromNode.IsTombstoned() || toNode.IsTombstoned() {
		return -22
	}

	// Add forward edge: fromSlot.Deps += toSlot
	rc := attrMgr.addForwardEdge(fromSlot, toSlot)
	if rc < 0 {
		return rc
	}

	// Add reverse edge: toSlot.Dependents += fromSlot
	rc = attrMgr.addReverseEdge(toSlot, fromSlot)
	if rc < 0 {
		return rc
	}

	return 0
}

// SyscallAttrUpdateDeps atomically replaces the full dependency set of a constraint.
//
// Args: constraintSlot, readSetBufPtr, readSetCount, _, _, _
// Returns: 0 on success, negative errno on failure.
//
func SyscallAttrUpdateDeps(constraintSlotArg, readSetBufPtr, readSetCount, _, _, _ uint64) int64 {
	if !attrMgr.initialized {
		return -12
	}

	slot := uint16(constraintSlotArg)
	if !attrMgr.isNodeAllocated(slot) {
		return -22
	}
	node := attrMgr.node(slot)
	if node.IsTombstoned() {
		return -22
	}

	count := int(readSetCount)
	if count > 256 {
		return -22 // sanity limit
	}

	// Copy new read set from user.
	var newDeps [256]uint16
	if count > 0 {
		var buf [512]byte // 256 * 2
		needed := count * 2
		if !kmem.CopyFromUser(buf[:needed], uintptr(readSetBufPtr), needed) {
			return -14 // EFAULT
		}
		for i := 0; i < count; i++ {
			newDeps[i] = *(*uint16)(unsafe.Pointer(&buf[i*2]))
		}
	}

	// Compare with current deps — no-op fast path.
	if count == int(node.DepsCount) {
		same := true
		for i := 0; i < count; i++ {
			if attrMgr.readEdge(node.DepsOffset, i) != newDeps[i] {
				same = false
				break
			}
		}
		if same {
			return 0
		}
	}

	// Remove this constraint from old deps' dependents lists.
	oldCount := int(node.DepsCount)
	for i := 0; i < oldCount; i++ {
		depSlot := attrMgr.readEdge(node.DepsOffset, i)
		attrMgr.removeReverseEdge(depSlot, slot)
	}

	// Write new forward edges.
	if count > 0 {
		off, ok := attrMgr.writeEdgeBlock(newDeps[:count])
		if !ok {
			return -12 // ENOMEM
		}
		node.DepsOffset = off
		node.DepsCount = uint16(count)
	} else {
		node.DepsOffset = 0
		node.DepsCount = 0
	}

	// Add this constraint to new deps' dependents lists.
	for i := 0; i < count; i++ {
		attrMgr.addReverseEdge(newDeps[i], slot)
	}

	return 0
}

// SyscallAttrRegisterQuery registers a find pattern and returns a query result slot.
//
// Args: patternBufPtr, patternLen, _, _, _, _
// Returns: slot index (>= 0) on success, negative errno on failure.
//
func SyscallAttrRegisterQuery(patternBufPtr, patternLen, _, _, _, _ uint64) int64 {
	if !attrMgr.initialized {
		return -12
	}

	if patternLen == 0 || patternLen > 255 {
		return -22
	}

	// Copy pattern from user.
	var patBuf [256]byte
	if !kmem.CopyFromUser(patBuf[:patternLen], uintptr(patternBufPtr), int(patternLen)) {
		return -14 // EFAULT
	}

	// Dedup: check if already registered.
	for i := uint16(0); i < attrMgr.queryCount; i++ {
		q := &attrMgr.queries[i]
		if q.patternLen == uint16(patternLen) {
			match := true
			for j := uint16(0); j < q.patternLen; j++ {
				if q.pattern[j] != patBuf[j] {
					match = false
					break
				}
			}
			if match {
				return int64(q.resultSlot)
			}
		}
	}

	if attrMgr.queryCount >= MaxQueryPatterns {
		return -12 // ENOMEM
	}

	// Create a collection-typed query result attribute.
	slot := attrMgr.allocNode()
	if slot == 0xFFFF {
		return -12
	}

	pid, _ := getCurrentThreadPIDAndTID()
	node := attrMgr.node(slot)
	node.Owner = uint16(pid)
	node.Kind = flat.AttrKindValue
	node.ValueType = flat.TypeCollection

	// Store the pattern in the kernel-side registry.
	q := &attrMgr.queries[attrMgr.queryCount]
	for i := uint16(0); i < uint16(patternLen); i++ {
		q.pattern[i] = patBuf[i]
	}
	q.patternLen = uint16(patternLen)
	q.resultSlot = slot
	q.ownerPID = uint16(pid)
	attrMgr.queryCount++

	// Evaluate the pattern against the current trie.
	patStr := unsafeStringFromBytes(patBuf[:patternLen])
	var matches [256]uint16
	matchCount := attrMgr.trieMatchPattern(patStr, matches[:])
	_ = matchCount // collection write deferred to Phase 4 (requires collection region management)

	return int64(slot)
}

// --- Internal helpers ---

// addForwardEdge adds toSlot to fromSlot's Deps list.
// Allocates a new edge block, copies old + new.
//
func (mgr *KernelAttrManager) addForwardEdge(fromSlot, toSlot uint16) int64 {
	node := mgr.node(fromSlot)
	oldCount := int(node.DepsCount)

	// Build new edge list: old deps + toSlot
	var newEdges [257]uint16
	for i := 0; i < oldCount; i++ {
		newEdges[i] = mgr.readEdge(node.DepsOffset, i)
	}
	newEdges[oldCount] = toSlot

	off, ok := mgr.writeEdgeBlock(newEdges[:oldCount+1])
	if !ok {
		return -12
	}
	node.DepsOffset = off
	node.DepsCount = uint16(oldCount + 1)
	return 0
}

// addReverseEdge adds depSlot to toSlot's Dependents list.
//
func (mgr *KernelAttrManager) addReverseEdge(toSlot, depSlot uint16) int64 {
	node := mgr.node(toSlot)
	oldCount := int(node.DependentsCount)

	var newEdges [257]uint16
	for i := 0; i < oldCount; i++ {
		newEdges[i] = mgr.readEdge(node.DependentsOffset, i)
	}
	newEdges[oldCount] = depSlot

	off, ok := mgr.writeEdgeBlock(newEdges[:oldCount+1])
	if !ok {
		return -12
	}
	node.DependentsOffset = off
	node.DependentsCount = uint16(oldCount + 1)
	return 0
}

// removeReverseEdge removes depSlot from toSlot's Dependents list.
//
func (mgr *KernelAttrManager) removeReverseEdge(depSlot, constraintSlot uint16) {
	node := mgr.node(depSlot)
	oldCount := int(node.DependentsCount)
	if oldCount == 0 {
		return
	}

	// Build new list without constraintSlot.
	var newEdges [256]uint16
	newCount := 0
	for i := 0; i < oldCount; i++ {
		s := mgr.readEdge(node.DependentsOffset, i)
		if s != constraintSlot {
			newEdges[newCount] = s
			newCount++
		}
	}

	if newCount == oldCount {
		return // wasn't in the list
	}

	if newCount > 0 {
		off, ok := mgr.writeEdgeBlock(newEdges[:newCount])
		if !ok {
			return // edge region full — cannot shrink, just leave stale
		}
		node.DependentsOffset = off
	} else {
		node.DependentsOffset = 0
	}
	node.DependentsCount = uint16(newCount)
}

// updateQueryResultsForURI re-evaluates query patterns after a URI is created/removed.
//
func (mgr *KernelAttrManager) updateQueryResultsForURI(uri string) {
	for i := uint16(0); i < mgr.queryCount; i++ {
		q := &mgr.queries[i]
		patStr := unsafeStringFromBytes(q.pattern[:q.patternLen])

		// Quick reject: if the pattern has more segments than the URI, skip.
		var patSegs [maxURISegments]string
		patCount := parseURI(patStr, &patSegs)
		var uriSegs [maxURISegments]string
		uriCount := parseURI(uri, &uriSegs)
		if patCount < 0 || uriCount < 0 || patCount != uriCount {
			continue
		}

		// Check if the URI matches the pattern.
		match := true
		for j := 0; j < patCount; j++ {
			if patSegs[j] != "*" && patSegs[j] != uriSegs[j] {
				match = false
				break
			}
		}
		if match {
			// The newly created/removed URI matches this query pattern.
			// Dirty-propagate from the query result slot so dependents know
			// the collection changed.
			mgr.dirtyPropagate(q.resultSlot)
		}
	}
}

// unsafeStringFromBytes creates a string from a byte slice without allocation.
// The caller must ensure the bytes remain valid for the string's lifetime.
//
//go:nosplit
func unsafeStringFromBytes(b []byte) string {
	return *(*string)(unsafe.Pointer(&b))
}
