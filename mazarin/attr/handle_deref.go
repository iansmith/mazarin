// handle_deref.go — Public Deref functions for reading attribute values by URI.
//
// These functions read directly from the shared constraint pages using seqlock
// protocol. They are the public API for the Deref capability that
// sharedResolver.Deref provides to constraint programs.

package attr

import (
	"mazzy/mazarin/vm/flat"
)

// DerefStr reads a string attribute value by URI from the shared pages.
// Returns the string value and true if found, or "" and false if the URI
// doesn't exist or isn't a string type.
func DerefStr(uri string) (string, bool) {
	slot, found := trieLookupShared(uri)
	if !found {
		return "", false
	}

	// Cascade: if the target slot is a local dirty constraint, evaluate it first.
	if fn, ok := constraintEvaluators[slot]; ok {
		fn()
	}

	node := sharedPR.Node(int16(slot))

	// Seqlock read.
	var fv flat.FlatValue
	for {
		seq := node.SeqCounter
		if seq&1 != 0 {
			continue
		}
		fv = node.CachedValue
		if node.SeqCounter == seq {
			break
		}
	}

	if fv.Typ != flat.TypeStr {
		return "", false
	}
	ref := fv.AsStrRef()
	return sharedPR.ReadString(ref), true
}

// DerefI64 reads an int64 attribute value by URI from the shared pages.
// Returns the value and true if found, or 0 and false if the URI
// doesn't exist or isn't an int64 type.
func DerefI64(uri string) (int64, bool) {
	slot, found := trieLookupShared(uri)
	if !found {
		return 0, false
	}

	// Cascade: if the target slot is a local dirty constraint, evaluate it first.
	if fn, ok := constraintEvaluators[slot]; ok {
		fn()
	}

	node := sharedPR.Node(int16(slot))

	// Seqlock read.
	var fv flat.FlatValue
	for {
		seq := node.SeqCounter
		if seq&1 != 0 {
			continue
		}
		fv = node.CachedValue
		if node.SeqCounter == seq {
			break
		}
	}

	if fv.Typ != flat.TypeI64 {
		return 0, false
	}
	return fv.AsI64(), true
}
