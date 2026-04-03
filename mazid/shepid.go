package mazid

import (
	"errors"
	"fmt"
	"strconv"
)

// ShepherdId is the userspace representation of a shepherd identifier.
// It carries up to three interchangeable forms:
//
//   - Sid:  numeric SID (int64); kernel-reserved values 0–3, or bit-22 set for user SIDs
//   - Id:   word-triple string (e.g. "clever-beatles-smith")
//   - Name: TOML launch name (e.g. "fs")
//
// At least one field must be set. Sid and Id must be consistent when both are present.
// Fields are unexported; use BySid, ById, ByName, or ByIdAsString to construct.
type ShepherdId struct {
	sid  int64  // 0 = unset
	id   string // "" = unset
	name string // "" = unset
}

// Sentinel SID values reserved for kernel use.
const (
	KernelSIDNone    = int64(0)
	KernelSIDReserv1 = int64(1)
	KernelSIDReserv2 = int64(2)
	KernelSIDReserv3 = int64(3)
)

var (
	ErrNoSid         = errors.New("mazid: no SID set")
	ErrInvalidSid    = errors.New("mazid: SID is not kernel-reserved (0–3) and bit 22 is not set")
	ErrNoName        = errors.New("mazid: no name set")
	ErrSidIdMismatch = errors.New("mazid: Sid and Id do not represent the same value")
	ErrNothingSet    = errors.New("mazid: ShepherdId has no fields set")
)

// isValidSid returns true for kernel-reserved SIDs (0–3) or properly formed
// non-kernel SIDs (bit 14 set, bits 0–1 clear).
//
// Non-kernel SID bit layout (int16 in kernel, represented as int64 here):
//
//	bit 15:    0  (always positive, avoids collision with -1 sentinel)
//	bit 14:    1  (non-kernel marker)
//	bits 13–2: 12 random bits
//	bits 1–0:  00
func isValidSid(sid int64) bool {
	if sid >= 0 && sid <= 3 {
		return true
	}
	return (sid&(1<<14)) != 0 && (sid&3) == 0
}

// encode12 encodes a 12-bit index (0–4095) into a word-triple string using a
// 4+4+4 bit split: bits 11–8 → adjective (0–15), bits 7–4 → band (0–15),
// bits 3–0 → person (0–15). Gives 16×16×16 = 4096 unique combinations.
func encode12(index uint32) (string, error) {
	if index > 0xFFF {
		return "", fmt.Errorf("%w: 12-bit index %d out of range", ErrOutOfRange, index)
	}
	i1 := (index >> 8) & 0xF // adjective
	i2 := (index >> 4) & 0xF // band
	i3 := index & 0xF        // person
	return wordlist1[i1] + "-" + wordlist2[i2] + "-" + wordlist3[i3], nil
}

// decode12 parses a word-triple string encoded by encode12 and returns its
// 12-bit index. Returns an error if any word is not in the first 16 entries
// of its wordlist (i.e. not a valid 12-bit triple).
func decode12(id string) (uint32, error) {
	w1, w2, w3, err := split(id)
	if err != nil {
		return 0, err
	}
	i1, ok := wl1Index[w1]
	if !ok {
		return 0, fmt.Errorf("%w: adjective %q", ErrInvalidWord, w1)
	}
	if i1 > 15 {
		return 0, fmt.Errorf("%w: adjective %q (index %d) not in 12-bit range (0–15)", ErrInvalidWord, w1, i1)
	}
	i2, ok := wl2Index[w2]
	if !ok {
		return 0, fmt.Errorf("%w: band %q", ErrInvalidWord, w2)
	}
	if i2 > 15 {
		return 0, fmt.Errorf("%w: band %q (index %d) not in 12-bit range (0–15)", ErrInvalidWord, w2, i2)
	}
	i3, ok := wl3Index[w3]
	if !ok {
		return 0, fmt.Errorf("%w: person %q", ErrInvalidWord, w3)
	}
	if i3 > 15 {
		return 0, fmt.Errorf("%w: person %q (index %d) not in 12-bit range (0–15)", ErrInvalidWord, w3, i3)
	}
	return (i1 << 8) | (i2 << 4) | i3, nil
}

// SidToId converts a non-kernel SID to its word-triple string.
// Extracts the 12-bit index from bits 13–2 and encodes it as adjective-band-person
// using the first 16 entries of each wordlist (4+4+4 bit split).
func SidToId(sid int64) (string, error) {
	if !isValidSid(sid) {
		return "", ErrInvalidSid
	}
	if sid <= 3 {
		return "", fmt.Errorf("mazid: kernel-reserved SID %d has no word-triple form", sid)
	}
	index12 := uint32((sid >> 2) & 0xFFF)
	return encode12(index12)
}

// IdToSid converts a word-triple string (encoded by SidToId) to a non-kernel SID.
// The 12-bit index decoded from the triple is placed in bits 13–2 with bit 14 set.
func IdToSid(id string) (int64, error) {
	index, err := decode12(id)
	if err != nil {
		return 0, err
	}
	return int64(1<<14) | int64(index<<2), nil
}

// BySid constructs a ShepherdId from a numeric SID.
// Panics if sid is not 0–3 (kernel-reserved) and does not have bit 22 set.
func BySid(sid int64) ShepherdId {
	if !isValidSid(sid) {
		panic(fmt.Sprintf("mazid.BySid: invalid SID %d: not kernel-reserved (0–3) and bit 22 not set", sid))
	}
	return ShepherdId{sid: sid}
}

// ById constructs a ShepherdId from a word-triple string.
// Returns an error if the string is not a valid word-triple.
func ById(id string) (ShepherdId, error) {
	// Validate by attempting conversion.
	_, err := Parse(id)
	if err != nil {
		return ShepherdId{}, err
	}
	return ShepherdId{id: id}, nil
}

// ByIdAsString constructs a ShepherdId from either:
//   - a numeric string (e.g. "4194308"), interpreted as a decimal SID
//   - a word-triple string (e.g. "clever-beatles-smith")
//
// Panics if the value is a numeric SID that is neither 0–3 nor has bit 22 set.
func ByIdAsString(s string) ShepherdId {
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		// Numeric: treat as SID — BySid will panic on invalid values.
		return BySid(n)
	}
	// Word-triple form.
	si, err := ById(s)
	if err != nil {
		panic(fmt.Sprintf("mazid.ByIdAsString: %v", err))
	}
	return si
}

// ByName constructs a ShepherdId from a TOML launch name.
// No SID or Id is inferred; call Resolve to fill those in.
func ByName(name string) ShepherdId {
	return ShepherdId{name: name}
}

// Sid returns the numeric SID, or 0 if not set.
func (si ShepherdId) Sid() int64 { return si.sid }

// Id returns the word-triple string, or "" if not set.
func (si ShepherdId) Id() string { return si.id }

// Name returns the TOML launch name, or "" if not set.
func (si ShepherdId) Name() string { return si.name }

// WithName returns a copy of si with the name field set.
func (si ShepherdId) WithName(name string) ShepherdId {
	si.name = name
	return si
}

// Validate checks internal consistency:
//   - At least one of Sid, Id, or Name must be set.
//   - If both Sid and Id are set, they must encode the same 20-bit index.
//   - Name is independent and may coexist with Sid/Id.
func (si ShepherdId) Validate() error {
	if si.sid == 0 && si.id == "" && si.name == "" {
		return ErrNothingSet
	}
	if si.sid != 0 && si.id != "" {
		expected, err := SidToId(si.sid)
		if err != nil {
			return err
		}
		if expected != si.id {
			return fmt.Errorf("%w: sid %d → %q, stored id %q", ErrSidIdMismatch, si.sid, expected, si.id)
		}
	}
	return nil
}

// ShepherdInfoFunc is the callback type used by Resolve.
// It must iterate all live shepherds and call yield for each one,
// passing the shepherd's TOML name and numeric SID.
// Iteration stops early if yield returns false.
type ShepherdInfoFunc func(yield func(name string, sid int64) bool)

// Resolve fills in the SID (and derived Id) from si.Name by scanning the
// live shepherd table via infoFn. Returns the resolved SID, or an error if
// the name is not found or matches more than one entry.
func (si ShepherdId) Resolve(infoFn ShepherdInfoFunc) (int64, error) {
	if si.name == "" {
		if si.sid != 0 {
			return si.sid, nil
		}
		return 0, ErrNoName
	}
	found := int64(-1)
	count := 0
	infoFn(func(name string, sid int64) bool {
		if name == si.name {
			found = sid
			count++
			if count > 1 {
				return false
			}
		}
		return true
	})
	switch count {
	case 0:
		return 0, fmt.Errorf("mazid: no shepherd with name %q", si.name)
	case 1:
		return found, nil
	default:
		return 0, fmt.Errorf("mazid: ambiguous: %d shepherds with name %q", count, si.name)
	}
}
