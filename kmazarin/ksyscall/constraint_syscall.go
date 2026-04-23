// constraint_syscall.go — Kernel syscall handlers for the constraint attribute system.
//
// SysAttrCreate (0x1021), SysAttrWrite (0x1022), SysAttrWriteURI (0x1023),
// SysAttrAddDep (0x1024), SysAttrUpdateDeps (0x1025), SysAttrRegisterQuery (0x1026).

package ksyscall

import (
	"encoding/binary"
	"mazzy/kmazarin/klog"
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

	// Extract the no-trie flag from attrKind. When set, the node is allocated
	// but not inserted into the namespace trie and no URI string is stored.
	// Used for swap temporaries.
	noTrie := (attrKind & uint64(flat.AttrFlagNoTrie)) != 0
	attrKind &^= uint64(flat.AttrFlagNoTrie) // mask off flag, keep kind bits

	var uri string
	if noTrie {
		// No-trie mode: URI is optional. Skip validation, trie, and string alloc.
	} else {
		// Validate URI length.
		if uriLen == 0 || uriLen > maxURILen {
			return -22 // EINVAL
		}

		// Copy URI from user buffer.
		var uriBuf [maxURILen]byte
		if !kmem.CopyFromUser(uriBuf[:uriLen], uintptr(uriBufPtr), int(uriLen)) {
			return -14 // EFAULT
		}
		uri = unsafeStringFromBytes(uriBuf[:uriLen])

		// Validate URI format.
		var segments [maxURISegments]string
		nSeg := parseURI(uri, &segments)
		if nSeg < 2 {
			return -22 // EINVAL — need at least "shepherd/<name>" or "kernel/<name>"
		}

		// Validate ownership: shepherds can only create under "shepherd/<shepherdName>/..."
		if segments[0] == "kernel" {
			return -1 // EPERM — kernel namespace not accessible via syscall
		}
		if segments[0] != "shepherd" {
			return -22 // EINVAL — must be under "shepherd" or "kernel"
		}

		// Check for duplicate URI.
		if _, exists := attrMgr.trieLookup(uri); exists {
			return -17 // EEXIST
		}
	}

	// Validate value type.
	if valueType == 0 || uint8(valueType) > flat.MaxTypeTag {
		return -22 // EINVAL
	}

	// Validate attr kind.
	if attrKind != uint64(flat.AttrKindValue) && attrKind != uint64(flat.AttrKindConstraint) {
		return -22 // EINVAL
	}

	// Allocate a node slot.
	slot := attrMgr.allocNode()
	if slot == 0xFFFF {
		klog.Errf("[attr] ENOMEM: allocNode full (cap=%d)\n", attrMgr.nodeCapacity)
		return -12 // ENOMEM
	}

	// Get caller's shepherd ID.
	pid, _ := getCurrentThreadSIDAndTID()

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
			klog.Errf("[attr] ENOMEM: bytecode full (off=0x%x need=%d cap=0x%x)\n",
				uint64(bcOff), needed, uint64(kmem.RegionBytecodeSize))
			return -12 // ENOMEM
		}
		// Copy directly from userspace into the bytecode region (no stack buffer limit).
		dst := attrMgr.baseVA + uintptr(attrMgr.bytecodeRegionOff) + uintptr(bcOff)
		dstSlice := unsafe.Slice((*byte)(unsafe.Pointer(dst)), int(bytecodeLen))
		if !kmem.CopyFromUser(dstSlice, uintptr(bytecodeBufPtr), int(bytecodeLen)) {
			attrMgr.freeNode(slot)
			return -14 // EFAULT
		}
		attrMgr.bytecodeBumpOff += needed
		node.ProgramOffset = bcOff
		node.ProgramLen = uint16(bytecodeLen) // total byte length of serialized MZBC blob
	}

	if noTrie {
		// No URI storage, no trie insertion, no query updates.
		return int64(slot)
	}

	// Allocate string slot for the URI name.
	nameOff, ok := attrMgr.allocString(uri)
	if !ok {
		attrMgr.freeNode(slot)
		klog.Errf("[attr] ENOMEM: allocString full (cap=%d)\n", attrMgr.stringCap)
		return -12 // ENOMEM
	}
	node.NameOffset = nameOff

	// Insert into namespace trie.
	rc := attrMgr.trieInsert(uri, slot)
	if rc < 0 {
		attrMgr.freeNode(slot)
		if rc == -12 {
			klog.Errf("[attr] ENOMEM: trieInsert full (cap=%d)\n", attrMgr.trieCapacity)
		}
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
	pid, _ := getCurrentThreadSIDAndTID()
	if node.Owner != uint16(pid) {
		return -1 // EPERM
	}

	// Cannot write to constraint attributes.
	if node.Kind == flat.AttrKindConstraint {
		return -1 // EPERM
	}

	// Copy flat.Value from user buffer (32 bytes).
	if valueLen != flat.ValueSize {
		return -22 // EINVAL
	}
	var valBuf [flat.ValueSize]byte
	if !kmem.CopyFromUser(valBuf[:], uintptr(valueBufPtr), flat.ValueSize) {
		return -14 // EFAULT
	}
	newVal := *(*flat.Value)(unsafe.Pointer(&valBuf[0]))

	// Validate type matches.
	if newVal.Typ != node.ValueType {
		return -22 // EINVAL
	}

	// Change-gating: skip propagation if value is unchanged (bitwise equal).
	if node.CachedValue == newVal {
		return 0
	}

	// Seqlock write: odd = in progress, even = stable.
	node.SeqCounter++
	node.CachedValue = newVal
	node.SeqCounter++

	// Dirty-propagate to dependents. The source node is marked dirty by
	// dirtyWalk itself; no need to clear it first.
	attrMgr.dirtyPropagate(slot)
	flushPendingDirtyWakes()

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

	pid, _ := getCurrentThreadSIDAndTID()
	node := attrMgr.node(slot)
	node.Owner = uint16(pid)
	node.Kind = flat.AttrKindValue
	node.ValueType = flat.TypeCollection

	// Store the pattern in the kernel-side registry and assign this query
	// its dedicated fixed collection region (offset within collRegion).
	q := &attrMgr.queries[attrMgr.queryCount]
	for i := uint16(0); i < uint16(patternLen); i++ {
		q.pattern[i] = patBuf[i]
	}
	q.patternLen = uint16(patternLen)
	q.resultSlot = slot
	q.ownerSID = uint16(pid)
	q.collOff = uint32(attrMgr.queryCount) * MaxCollPerQuery * flat.ValueSize
	attrMgr.queryCount++

	// Evaluate the pattern against the current trie and write collection results.
	patStr := unsafeStringFromBytes(patBuf[:patternLen])
	matchCount := attrMgr.trieMatchPatternURIs(patStr, matchURIScratch[:])

	attrMgr.writeQueryCollection(q, matchURIScratch[:], matchCount)

	return int64(slot)
}

// matchURIScratch is a package-level reusable buffer for pattern-match
// results. Sized at MaxCollPerQuery to keep stack frames small; single-
// threaded kernel context means no concurrent-use race.
var matchURIScratch [MaxCollPerQuery]string

// SyscallAttrWriteResult writes a constraint evaluation result to a constraint slot.
// Only constraint attributes are accepted (values are rejected).
// Does NOT dirty-propagate — dependents were already dirtied by the original Set() walk.
//
// Args: slotIndex, valueBufPtr, valueLen, _, _, _
// Returns: 0 on success, negative errno on failure.
//
func SyscallAttrWriteResult(slotIndex, valueBufPtr, valueLen, _, _, _ uint64) int64 {
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
	pid, _ := getCurrentThreadSIDAndTID()
	if node.Owner != uint16(pid) {
		return -1 // EPERM
	}

	// Only constraint attributes can receive results.
	if node.Kind != flat.AttrKindConstraint {
		return -1 // EPERM
	}

	// Copy flat.Value from user buffer (32 bytes).
	if valueLen != flat.ValueSize {
		return -22 // EINVAL
	}
	var valBuf [flat.ValueSize]byte
	if !kmem.CopyFromUser(valBuf[:], uintptr(valueBufPtr), flat.ValueSize) {
		return -14 // EFAULT
	}
	newVal := *(*flat.Value)(unsafe.Pointer(&valBuf[0]))

	// Validate type matches.
	if newVal.Typ != node.ValueType {
		return -22 // EINVAL
	}

	// Seqlock write: odd = in progress, even = stable.
	node.SeqCounter++
	node.CachedValue = newVal
	node.SeqCounter++

	// Clear dirty flag — this constraint has been evaluated.
	node.SetDirty(false)

	// No dirty-propagation: dependents were already dirtied by the Set() that
	// caused this constraint to be dirty in the first place.

	return 0
}

// SyscallAttrWriteString writes a string value to an attribute. The kernel copies
// the string bytes from shepherd memory, allocates a string slot, and writes the
// resulting flat.StrRef into the attribute's CachedValue.
//
// Args: slotIndex, strBufPtr, strLen, isConstraintResult, _, _
//   - isConstraintResult=1: clears dirty, no propagation (constraint result path)
//   - isConstraintResult=0: dirty-propagates (value write path)
//
// Returns: 0 on success, negative errno on failure.
//
func SyscallAttrWriteString(slotIndex, strBufPtr, strLen, isConstraintResult, _, _ uint64) int64 {
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
	pid, _ := getCurrentThreadSIDAndTID()
	if node.Owner != uint16(pid) {
		return -1 // EPERM
	}

	// Type must be string.
	if node.ValueType != flat.TypeStr {
		return -22 // EINVAL
	}

	// Validate constraint/value path consistency.
	if isConstraintResult != 0 && node.Kind != flat.AttrKindConstraint {
		return -22 // EINVAL — can't write constraint result to a value attribute
	}
	if isConstraintResult == 0 && node.Kind == flat.AttrKindConstraint {
		return -1 // EPERM — use constraint result path for constraints
	}

	// Copy string from user.
	if strLen > flat.StringMaxLen {
		return -22 // EINVAL — string too long
	}
	var strBuf [flat.StringMaxLen]byte
	copyLen := int(strLen)
	if !kmem.CopyFromUser(strBuf[:copyLen], uintptr(strBufPtr), copyLen) {
		return -14 // EFAULT
	}

	// Change-gating for strings: compare new content against existing value.
	// Unlike scalar flat.Values, string flat.Values can't be compared bitwise
	// because each write allocates a new string slot (different RegionOffset).
	// Compare the actual string bytes instead.
	if isConstraintResult == 0 && node.CachedValue.Typ == flat.TypeStr {
		oldRef := node.CachedValue.AsStrRef()
		if oldRef.Len == uint16(copyLen) {
			oldBase := attrMgr.baseVA + uintptr(attrMgr.stringRegionOff) + uintptr(oldRef.RegionOffset)
			same := true
			for i := 0; i < copyLen; i++ {
				if *(*byte)(unsafe.Pointer(oldBase + uintptr(i))) != strBuf[i] {
					same = false
					break
				}
			}
			if same {
				return 0 // value unchanged, skip write and propagation
			}
		}
	}

	// Free the old string slot before allocating a new one.
	// This prevents string region exhaustion when values are repeatedly updated.
	if node.CachedValue.Typ == flat.TypeStr {
		oldRef := node.CachedValue.AsStrRef()
		if oldRef.Len > 0 || oldRef.RegionOffset > 0 {
			attrMgr.freeString(oldRef.RegionOffset)
		}
	}

	// Allocate string slot in shared page and write string content.
	strVal := unsafeStringFromBytes(strBuf[:copyLen])
	nameOff, ok := attrMgr.allocString(strVal)
	if !ok {
		return -12 // ENOMEM
	}

	// Build flat.StrRef and write to CachedValue via seqlock.
	ref := flat.StrRef{
		RegionOffset: nameOff,
		Len:          uint16(copyLen),
	}
	newVal := flat.NewStr(ref)

	node.SeqCounter++
	node.CachedValue = newVal
	node.SeqCounter++

	if isConstraintResult != 0 {
		// Constraint result path: clear dirty, no propagation.
		node.SetDirty(false)
	} else {
		// Value write path: propagate to dependents. The source node is
		// marked dirty by dirtyWalk; no need to clear first.
		attrMgr.dirtyPropagate(slot)
		flushPendingDirtyWakes()
	}

	return 0
}

// SyscallAttrWriteCollI64 writes a collection of int64 values to a TypeCollection attribute.
//
// Args: slotIndex, userVABuf ([]int64 backing array), count, isConstraintResult, _, _
//   - isConstraintResult=1: clears dirty, no propagation (constraint result path)
//   - isConstraintResult=0: dirty-propagates (value write path)
//
// The kernel allocates a ValueColl slot for this attribute on first write and reuses
// it on subsequent writes (slots are not freed; max RegionValueCollSlots live attrs).
//
// Returns: 0 on success, negative errno on failure.
func SyscallAttrWriteCollI64(slotIndex, userVABuf, count, isConstraintResult, _, _ uint64) int64 {
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

	pid, _ := getCurrentThreadSIDAndTID()
	if node.Owner != uint16(pid) {
		return -1 // EPERM
	}

	if node.ValueType != flat.TypeCollection {
		return -22 // EINVAL — must be TypeCollection
	}
	if isConstraintResult == 0 && node.Kind == flat.AttrKindConstraint {
		return -1 // EPERM — use constraint result path for constraints
	}
	if isConstraintResult != 0 && node.Kind != flat.AttrKindConstraint {
		return -22 // EINVAL
	}

	n := int(count)
	if n > kmem.MaxValueCollEntries {
		return -22 // EINVAL — too many entries; use sentinel path
	}

	// Find or allocate the ValueColl slot for this attribute.
	var slotByteOff uint32
	existing := node.CachedValue
	if existing.Typ == flat.TypeCollection && existing.AsCollRef().ElemType == flat.TypeI64 {
		slotByteOff = existing.AsCollRef().RegionOffset
	} else {
		slotByteOff = attrMgr.allocValueCollSlot()
		if slotByteOff == ^uint32(0) {
			klog.Errf("[attr] ENOMEM: allocValueCollSlot full (slots=%d)\n", uint64(attrMgr.valueCollSlotCount))
			return -12 // ENOMEM
		}
	}

	// Copy int64 values from user and write as flat.Value(TypeI64) items.
	base := attrMgr.baseVA + uintptr(attrMgr.valueCollRegionOff) + uintptr(slotByteOff)
	for i := 0; i < n; i++ {
		var buf [8]byte
		if !kmem.CopyFromUser(buf[:], uintptr(userVABuf)+uintptr(i)*8, 8) {
			return -14 // EFAULT
		}
		v := int64(binary.LittleEndian.Uint64(buf[:]))
		entry := flat.NewI64(v)
		*(*flat.Value)(unsafe.Pointer(base + uintptr(i)*flat.ValueSize)) = entry
	}

	newVal := flat.NewCollection(flat.CollRef{
		ElemType:     flat.TypeI64,
		RegionOffset: slotByteOff,
		Count:        uint16(n),
	})

	node.SeqCounter++
	node.CachedValue = newVal
	node.SeqCounter++

	if isConstraintResult != 0 {
		node.SetDirty(false)
	} else {
		attrMgr.dirtyPropagate(slot)
		flushPendingDirtyWakes()
	}

	return 0
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

// writeQueryCollection writes matched URIs into a query's dedicated collection
// region. Each query owns a fixed-size slot (MaxCollPerQuery entries) assigned
// at registration time, so writes are in-place and never exhaust a shared
// allocator. We borrow the existing NameOffset from each attribute's node —
// the URI is already stored in the string region from SyscallAttrCreate.
func (mgr *KernelAttrManager) writeQueryCollection(q *queryPattern, uris []string, count int) {
	node := mgr.node(q.resultSlot)

	if count == 0 {
		// Empty result: write an empty collection.
		node.SeqCounter++
		node.CachedValue = flat.NewCollection(flat.CollRef{
			ElemType: flat.TypeStr,
			Count:    0,
		})
		node.SeqCounter++
		return
	}

	// Cap at the per-query slot capacity.
	if count > MaxCollPerQuery {
		count = MaxCollPerQuery
	}

	// Look up each matched URI's attribute node and borrow its NameOffset
	// (the URI string is already stored in the string region from SyscallAttrCreate).
	collOff := q.collOff
	actualCount := 0
	for i := 0; i < count; i++ {
		attrSlot, found := mgr.trieLookup(uris[i])
		if !found {
			continue
		}
		attrNode := mgr.node(attrSlot)
		fv := flat.NewStr(flat.StrRef{
			RegionOffset: attrNode.NameOffset,
			Len:          uint16(len(uris[i])),
		})
		dst := mgr.baseVA + uintptr(mgr.collRegionOff) + uintptr(collOff) + uintptr(actualCount)*flat.ValueSize
		*(*flat.Value)(unsafe.Pointer(dst)) = fv
		actualCount++
	}

	if actualCount == 0 {
		node.SeqCounter++
		node.CachedValue = flat.NewCollection(flat.CollRef{ElemType: flat.TypeStr})
		node.SeqCounter++
		return
	}

	// Write the collection ref to the result node.
	node.SeqCounter++
	node.CachedValue = flat.NewCollection(flat.CollRef{
		ElemType:     flat.TypeStr,
		RegionOffset: collOff,
		Count:        uint16(actualCount),
	})
	node.SeqCounter++
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
			// Re-evaluate the full query and write updated collection results
			// into the query's dedicated fixed region (no bump allocation).
			matchCount := mgr.trieMatchPatternURIs(patStr, matchURIScratch[:])

			mgr.writeQueryCollection(q, matchURIScratch[:], matchCount)

			// Dirty-propagate from the query result slot so dependents know
			// the collection changed.
			mgr.dirtyPropagate(q.resultSlot)
			flushPendingDirtyWakes()
		}
	}
}

// SyscallAttrIncrementI64 atomically increments an int64 value attribute.
// No dirty propagation — intended for side-effect counters, not constraint inputs.
//
// Args: slotIndex, _, _, _, _, _
// Returns: new value on success, negative errno on failure.
func SyscallAttrIncrementI64(slotIndex, _, _, _, _, _ uint64) int64 {
	if !attrMgr.initialized {
		return -12 // ENOMEM
	}

	slot := uint16(slotIndex)
	if !attrMgr.isNodeAllocated(slot) {
		return -22 // EINVAL
	}

	node := attrMgr.node(slot)
	if node.IsTombstoned() {
		return -22 // EINVAL
	}

	// Ownership check.
	pid, _ := getCurrentThreadSIDAndTID()
	if node.Owner != uint16(pid) {
		return -1 // EPERM
	}

	// Must be a value attribute with int64 type.
	if node.Kind != flat.AttrKindValue || node.ValueType != flat.TypeI64 {
		return -22 // EINVAL
	}

	// Atomic read-increment-write under seqlock.
	cur := node.CachedValue.AsI64()
	newVal := flat.NewI64(cur + 1)
	node.SeqCounter++
	node.CachedValue = newVal
	node.SeqCounter++

	// No dirty propagation — counters are read-only side effects.
	return cur + 1
}

// SyscallAttrSwap atomically replaces the implementation behind targetSlot with
// the implementation from replacementSlot. The target keeps its URI, forward
// edges (dependents), and owner. It receives the replacement's kind, bytecode,
// cached value, and backward edges (dependencies). The replacement is tombstoned.
//
// Args: targetSlot, replacementSlot, _, _, _, _
// Returns: 0 on success, negative errno on failure.
//
func SyscallAttrSwap(targetSlotArg, replacementSlotArg, _, _, _, _ uint64) int64 {
	if !attrMgr.initialized {
		return -12
	}

	tSlot := uint16(targetSlotArg)
	rSlot := uint16(replacementSlotArg)

	if tSlot == rSlot {
		return -22 // EINVAL
	}

	if !attrMgr.isNodeAllocated(tSlot) || !attrMgr.isNodeAllocated(rSlot) {
		return -22 // EINVAL
	}

	tNode := attrMgr.node(tSlot)
	rNode := attrMgr.node(rSlot)
	if tNode.IsTombstoned() || rNode.IsTombstoned() {
		return -22 // EINVAL
	}

	// Ownership check — both must belong to the caller.
	pid, _ := getCurrentThreadSIDAndTID()
	if tNode.Owner != uint16(pid) || rNode.Owner != uint16(pid) {
		return -1 // EPERM
	}

	// Type must match.
	if tNode.ValueType != rNode.ValueType {
		return -22 // EINVAL
	}

	// Step 1: Remove target's backward edges.
	// For each of target's dependencies, remove target from their dependents list.
	oldDepCount := int(tNode.DepsCount)
	for i := 0; i < oldDepCount; i++ {
		depSlot := attrMgr.readEdge(tNode.DepsOffset, i)
		attrMgr.removeReverseEdge(depSlot, tSlot)
	}

	// Step 2: Copy replacement's implementation to target.
	tNode.Kind = rNode.Kind
	tNode.ProgramOffset = rNode.ProgramOffset
	tNode.ProgramLen = rNode.ProgramLen

	// Seqlock write for the cached value.
	tNode.SeqCounter++
	tNode.CachedValue = rNode.CachedValue
	tNode.SeqCounter++

	// Step 3: Transfer replacement's backward edges to target.
	tNode.DepsOffset = rNode.DepsOffset
	tNode.DepsCount = rNode.DepsCount

	// Update reverse edges: replacement's dependencies now have target as a dependent
	// instead of replacement.
	newDepCount := int(rNode.DepsCount)
	for i := 0; i < newDepCount; i++ {
		depSlot := attrMgr.readEdge(rNode.DepsOffset, i)
		attrMgr.replaceReverseEdge(depSlot, rSlot, tSlot)
	}

	// Step 4: Dirty-propagate from target through its forward edges (dependents).
	tNode.SetDirty(true)
	attrMgr.dirtyPropagate(tSlot)
	flushPendingDirtyWakes()

	// Step 5: Free replacement. If it has a URI in the trie, remove it.
	// Swap temps created with AttrFlagNoTrie have NameOffset=0 and no trie entry.
	if rNode.NameOffset != 0 {
		rURI := attrMgr.readNodeURI(rSlot)
		if rURI != "" {
			attrMgr.trieRemove(rURI)
			attrMgr.freeString(rNode.NameOffset)
		}
	}
	rNode.DepsOffset = 0
	rNode.DepsCount = 0
	rNode.DependentsOffset = 0
	rNode.DependentsCount = 0
	attrMgr.freeNode(rSlot)

	return 0
}

// replaceReverseEdge replaces oldSlot with newSlot in depSlot's Dependents list.
// If oldSlot is not found, does nothing.
func (mgr *KernelAttrManager) replaceReverseEdge(depSlot, oldSlot, newSlot uint16) {
	node := mgr.node(depSlot)
	count := int(node.DependentsCount)
	for i := 0; i < count; i++ {
		if mgr.readEdge(node.DependentsOffset, i) == oldSlot {
			mgr.writeEdge(node.DependentsOffset, i, newSlot)
			return
		}
	}
}

// SyscallAttrDelete deletes an attribute. Returns EBUSY (-16) if the attribute
// has dependents (DependentsCount > 0). The caller must unwire all dependents
// before deleting.
//
// Args: slotIndex, _, _, _, _, _
// Returns: 0 on success, negative errno on failure.
//
func SyscallAttrDelete(slotArg, _, _, _, _, _ uint64) int64 {
	if !attrMgr.initialized {
		return -12
	}

	slot := uint16(slotArg)
	if !attrMgr.isNodeAllocated(slot) {
		return -22 // EINVAL
	}

	node := attrMgr.node(slot)

	// Ownership check.
	pid, _ := getCurrentThreadSIDAndTID()
	if node.Owner != uint16(pid) {
		return -1 // EPERM
	}

	// Refuse deletion if anything depends on this attribute.
	if node.DependentsCount > 0 {
		logAttrDeleteBusy(slot, node, &attrMgr)
		return -16 // EBUSY
	}

	// Remove this node from its dependencies' dependents lists.
	depCount := int(node.DepsCount)
	for i := 0; i < depCount; i++ {
		depSlot := attrMgr.readEdge(node.DepsOffset, i)
		attrMgr.removeReverseEdge(depSlot, slot)
	}

	// Remove from trie and free string slot.
	if node.NameOffset != 0 {
		uri := attrMgr.readNodeURI(slot)
		if uri != "" {
			attrMgr.trieRemove(uri)
			attrMgr.updateQueryResultsForURI(uri)
		}
		attrMgr.freeString(node.NameOffset)
	}

	// Free the node.
	attrMgr.freeNode(slot)

	return 0
}

// logAttrDeleteBusy logs the EBUSY error when an attribute cannot be deleted
// because other attributes depend on it.
func logAttrDeleteBusy(slot uint16, node *flat.AttrNode, mgr *KernelAttrManager) {
	uri := mgr.readNodeURI(slot)
	deps := ""
	for i := 0; i < int(node.DependentsCount); i++ {
		depSlot := mgr.readEdge(node.DependentsOffset, i)
		if i > 0 {
			deps += ", "
		}
		depURI := mgr.readNodeURI(depSlot)
		if depURI != "" {
			deps += depURI
		} else {
			deps += "?"
		}
	}
	klog.Errf("[attr] EBUSY: AttrDelete slot=%d uri=%s has %d dependents: [%s]\n",
		slot, uri, node.DependentsCount, deps)
}

// unsafeStringFromBytes creates a string from a byte slice without allocation.
// The caller must ensure the bytes remain valid for the string's lifetime.
//
//go:nosplit
func unsafeStringFromBytes(b []byte) string {
	return *(*string)(unsafe.Pointer(&b))
}
