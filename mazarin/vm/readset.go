package vm

// MaxReadSetSize is the maximum number of attribute slots that can be
// recorded in a single evaluation's read set.
const MaxReadSetSize = 256

// ReadSet tracks attribute slot indices accessed during VM evaluation.
// After evaluation, the read set is compared with stored dependency edges
// to determine if SysAttrUpdateDeps needs to be called.
type ReadSet struct {
	Slots [MaxReadSetSize]uint16
	Count uint16
}

// Add records a slot index in the read set, deduplicating.
func (rs *ReadSet) Add(slot uint16) {
	for i := uint16(0); i < rs.Count; i++ {
		if rs.Slots[i] == slot {
			return
		}
	}
	if rs.Count < MaxReadSetSize {
		rs.Slots[rs.Count] = slot
		rs.Count++
	}
}

// Reset clears the read set.
func (rs *ReadSet) Reset() {
	rs.Count = 0
}

// Equal returns true if two read sets contain the same slots (order-independent).
func (rs *ReadSet) Equal(other *ReadSet) bool {
	if rs.Count != other.Count {
		return false
	}
	// For small sets, brute force is fine.
	for i := uint16(0); i < rs.Count; i++ {
		found := false
		for j := uint16(0); j < other.Count; j++ {
			if rs.Slots[i] == other.Slots[j] {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
