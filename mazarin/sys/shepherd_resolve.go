package sys

import "strconv"

// GetShepherdByName resolves a shepherd name to its SID.
//
// The name can be either a base name (e.g., "rachel", "disk") which is matched
// against running shepherds' filenames (/<name>.elf), or a numeric SID string
// (e.g., "5") which is validated against running shepherds.
//
// Returns:
//   - (sid, nil) if exactly one matching shepherd is found and it is ready
//   - (sid, ErrNotReady) if the shepherd exists but has not called SetReady(true)
//   - (0, ErrNoShepherd) if no matching shepherd is found
//   - (0, ErrAmbiguousShepherd) if multiple shepherds match the name
func GetShepherdByName(name string) (int, error) {
	entries, err := ShepherdInfo()
	if err != nil {
		return 0, ErrNoShepherd
	}

	// Try matching by name: "rachel" matches "/rachel.elf"
	target := "/" + name + ".elf"
	var matches []int
	for _, e := range entries {
		fn := string(e.Filename[:e.FilenameLen])
		if fn == target {
			matches = append(matches, int(e.SID))
		}
	}

	if len(matches) == 0 {
		// Try parsing as a numeric SID.
		sid, err := strconv.Atoi(name)
		if err != nil {
			return 0, ErrNoShepherd
		}
		// Validate that this SID exists.
		for _, e := range entries {
			if int(e.SID) == sid {
				if !GetReady(sid) {
					return sid, ErrNotReady
				}
				return sid, nil
			}
		}
		return 0, ErrNoShepherd
	}

	if len(matches) > 1 {
		return 0, ErrAmbiguousShepherd
	}

	sid := matches[0]
	if !GetReady(sid) {
		return sid, ErrNotReady
	}
	return sid, nil
}

// MustGetShepherdByName resolves a shepherd name to its SID, panicking on any error.
// Use this at init time when a missing or unready shepherd is fatal.
func MustGetShepherdByName(name string) int {
	sid, err := GetShepherdByName(name)
	if err != nil {
		panic("MustGetShepherdByName(" + name + "): " + err.Error())
	}
	return sid
}
