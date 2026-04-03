package sys

import (
	"mazzy/shep"
	"strconv"
)

// GetShepherdByName resolves a TOML shepherd name to a shep.Id.
//
// The name is matched against the Name field of running shepherds (set from
// the TOML launch configuration). A numeric SID string (e.g. "16388") is also
// accepted and matched by SID value.
//
// Returns:
//   - (Id, nil) if exactly one matching shepherd is found and it is ready
//   - (Id, ErrNotReady) if the shepherd exists but has not called SetReady(true)
//   - (zero, ErrNoShepherd) if no matching shepherd is found
//   - (zero, ErrAmbiguousShepherd) if multiple shepherds match the name
func GetShepherdByName(name string) (shep.Id, error) {
	entries, err := ShepherdInfo()
	if err != nil {
		return shep.Id{}, ErrNoShepherd
	}

	var matches []shep.Id

	// Try matching by TOML name field.
	for _, e := range entries {
		if e.Id.Name() == name {
			matches = append(matches, e.Id)
		}
	}

	if len(matches) == 0 {
		// Try parsing as a numeric SID string.
		if n, err := strconv.ParseInt(name, 10, 64); err == nil {
			for _, e := range entries {
				if e.Id.Sid() == n {
					matches = append(matches, e.Id)
				}
			}
		}
	}

	if len(matches) == 0 {
		return shep.Id{}, ErrNoShepherd
	}
	if len(matches) > 1 {
		return shep.Id{}, ErrAmbiguousShepherd
	}

	si := matches[0]
	if !GetReady(si) {
		return si, ErrNotReady
	}
	return si, nil
}

// MustGetShepherdByName resolves a shepherd name to a shep.Id, panicking on any error.
// Use this at init time when a missing or unready shepherd is fatal.
func MustGetShepherdByName(name string) shep.Id {
	si, err := GetShepherdByName(name)
	if err != nil {
		panic("MustGetShepherdByName(" + name + "): " + err.Error())
	}
	return si
}
