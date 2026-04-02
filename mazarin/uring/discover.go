package uring

import (
	"mazzy/mazarin/sys"
)

// FindUringID looks up the uring ID for a shepherd by its filename
// (e.g., "/rachel.elf"). Returns 0 if not found.
func FindUringID(filename string) uint64 {
	entries, err := sys.ShepherdInfo()
	if err != nil {
		return 0
	}
	for _, e := range entries {
		name := string(e.Filename[:e.FilenameLen])
		if name == filename {
			return e.UringID
		}
	}
	return 0
}

// FindUringIDBySID looks up the uring ID for a shepherd by its SID.
// Returns 0 if not found.
func FindUringIDBySID(sid int) uint64 {
	entries, err := sys.ShepherdInfo()
	if err != nil {
		return 0
	}
	for _, e := range entries {
		if int(e.SID) == sid {
			return e.UringID
		}
	}
	return 0
}
