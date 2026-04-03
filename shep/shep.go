// Package shep provides the userspace representation of a shepherd identifier.
//
// A shepherd can be addressed in three interchangeable forms:
//
//   - Sid:  numeric SID (int64); kernel-reserved values 0–3, or bit 14 set for user SIDs
//   - Id:   word-triple string (e.g. "ebullient-eurythmics-villars")
//   - Name: TOML launch name (e.g. "fs", "rachel")
//
// Non-kernel SID bit layout (fits in int16 in the kernel):
//
//	bit 15:    0  (always positive, avoids -1 sentinel collision)
//	bit 14:    1  (non-kernel marker)
//	bits 13–2: 12 random bits (4096 unique values)
//	bits 1–0:  00
//
// Word encoding uses a 4+4+4 bit split: bits 11–8 → adjective (0–15),
// bits 7–4 → band (0–15), bits 3–0 → person (0–15).
package shep

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Sentinel SID values reserved for kernel use.
const (
	KernelSIDNone    = int64(0)
	KernelSIDReserv1 = int64(1)
	KernelSIDReserv2 = int64(2)
	KernelSIDReserv3 = int64(3)
)

var (
	ErrInvalidSid    = errors.New("shep: SID is not kernel-reserved (0–3) and bit 14 is not set")
	ErrInvalidWord   = errors.New("shep: word not found in wordlist or out of 12-bit range")
	ErrInvalidFormat = errors.New("shep: invalid word-triple format")
	ErrSidIdMismatch = errors.New("shep: Sid and Id do not represent the same value")
	ErrNothingSet    = errors.New("shep: Id has no fields set")
	ErrOutOfRange    = errors.New("shep: value out of range")
)

var wl1Index, wl2Index, wl3Index map[string]uint32

func init() {
	wl1Index = buildIndex(wordlist1[:])
	wl2Index = buildIndex(wordlist2[:])
	wl3Index = buildIndex(wordlist3[:])
}

func buildIndex(words []string) map[string]uint32 {
	m := make(map[string]uint32, len(words))
	for i, w := range words {
		m[w] = uint32(i)
	}
	return m
}

// Id is the userspace representation of a shepherd identifier.
// Construct with BySid, ById, ByIdAsString, or ByName.
// Fields are unexported to enforce consistent construction.
type Id struct {
	sid  int64  // 0 = unset
	id   string // "" = unset; word-triple form
	name string // "" = unset; TOML launch name
}

// isValidSid returns true for kernel-reserved SIDs (0–3) or properly formed
// non-kernel SIDs (bit 14 set, bits 0–1 clear).
func isValidSid(sid int64) bool {
	if sid >= 0 && sid <= 3 {
		return true
	}
	return (sid&(1<<14)) != 0 && (sid&3) == 0
}

// encode12 encodes a 12-bit index (0–4095) as adjective-band-person using
// the first 16 entries of each wordlist (4+4+4 bit split).
func encode12(index uint32) (string, error) {
	if index > 0xFFF {
		return "", fmt.Errorf("%w: 12-bit index %d", ErrOutOfRange, index)
	}
	i1 := (index >> 8) & 0xF
	i2 := (index >> 4) & 0xF
	i3 := index & 0xF
	return wordlist1[i1] + "-" + wordlist2[i2] + "-" + wordlist3[i3], nil
}

// decode12 parses a word-triple and returns its 12-bit index.
// Returns an error if any word is outside the first 16 entries of its list.
func decode12(s string) (uint32, error) {
	parts := strings.SplitN(s, "-", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return 0, fmt.Errorf("%w: %q", ErrInvalidFormat, s)
	}
	w1, w2, w3 := parts[0], parts[1], parts[2]

	i1, ok := wl1Index[w1]
	if !ok {
		return 0, fmt.Errorf("%w: adjective %q", ErrInvalidWord, w1)
	}
	if i1 > 15 {
		return 0, fmt.Errorf("%w: adjective %q (index %d) not in 12-bit range", ErrInvalidWord, w1, i1)
	}
	i2, ok := wl2Index[w2]
	if !ok {
		return 0, fmt.Errorf("%w: band %q", ErrInvalidWord, w2)
	}
	if i2 > 15 {
		return 0, fmt.Errorf("%w: band %q (index %d) not in 12-bit range", ErrInvalidWord, w2, i2)
	}
	i3, ok := wl3Index[w3]
	if !ok {
		return 0, fmt.Errorf("%w: person %q", ErrInvalidWord, w3)
	}
	if i3 > 15 {
		return 0, fmt.Errorf("%w: person %q (index %d) not in 12-bit range", ErrInvalidWord, w3, i3)
	}
	return (i1 << 8) | (i2 << 4) | i3, nil
}

// SidToWords converts a non-kernel SID to its word-triple string.
func SidToWords(sid int64) (string, error) {
	if !isValidSid(sid) {
		return "", ErrInvalidSid
	}
	if sid <= 3 {
		return "", fmt.Errorf("shep: kernel-reserved SID %d has no word-triple form", sid)
	}
	return encode12(uint32((sid >> 2) & 0xFFF))
}

// WordsToSid converts a word-triple string to a non-kernel SID.
func WordsToSid(words string) (int64, error) {
	index, err := decode12(words)
	if err != nil {
		return 0, err
	}
	return int64(1<<14) | int64(index<<2), nil
}

// NewFromInt16 constructs a fully-hydrated Id from a kernel int16 SID.
// Both Sid() and Id() are populated immediately — Id() will never return "".
// Panics if n is not a valid SID (not 0–3 and bit 14 not set).
func NewFromInt16(n int16) Id {
	sid := int64(n)
	if !isValidSid(sid) {
		panic(fmt.Sprintf("shep.NewFromInt16: invalid SID %d", n))
	}
	if sid <= 3 {
		return Id{sid: sid} // kernel-reserved: no word-triple form
	}
	words, err := encode12(uint32((sid >> 2) & 0xFFF))
	if err != nil {
		panic(fmt.Sprintf("shep.NewFromInt16: encode failed: %v", err))
	}
	return Id{sid: sid, id: words}
}

// NewFromWords constructs a fully-hydrated Id from a word-triple string.
// Both Id() and Sid() are populated immediately — Sid() will never return 0.
// Returns an error if the string is not a valid 12-bit word-triple.
func NewFromWords(words string) (Id, error) {
	sid, err := WordsToSid(words)
	if err != nil {
		return Id{}, err
	}
	return Id{sid: sid, id: words}, nil
}

// BySid constructs an Id with only the numeric SID set.
// Id() will return "" unless you need both forms; prefer NewFromInt16.
// Panics if sid is not 0–3 (kernel-reserved) and does not have bit 14 set.
func BySid(sid int64) Id {
	if !isValidSid(sid) {
		panic(fmt.Sprintf("shep.BySid: invalid SID %d", sid))
	}
	return Id{sid: sid}
}

// ByIdAsString constructs an Id from either a decimal SID string or a word-triple.
// The result is fully hydrated in both cases.
// Panics if the value is not a valid SID or word-triple.
func ByIdAsString(s string) Id {
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return NewFromInt16(int16(n))
	}
	si, err := NewFromWords(s)
	if err != nil {
		panic(fmt.Sprintf("shep.ByIdAsString: %v", err))
	}
	return si
}

// ByName constructs an Id from a TOML launch name.
// No Sid or word-triple is inferred; call Resolve to fill those in.
func ByName(name string) Id {
	return Id{name: name}
}

// Sid returns the numeric SID, or 0 if not set.
func (i Id) Sid() int64 { return i.sid }

// Id returns the word-triple string (e.g. "ebullient-eurythmics-villars"),
// or "" if not set. Always populated when constructed via NewFromInt16 or NewFromWords.
func (i Id) Id() string { return i.id }

// Name returns the TOML launch name, or "" if not set.
func (i Id) Name() string { return i.name }

// WithName returns a copy of i with the name field set.
func (i Id) WithName(name string) Id {
	i.name = name
	return i
}

// Validate checks internal consistency:
//   - At least one of Sid, Id, or Name must be set.
//   - If both Sid and Id are set, they must encode the same 12-bit index.
func (i Id) Validate() error {
	if i.sid == 0 && i.id == "" && i.name == "" {
		return ErrNothingSet
	}
	if i.sid != 0 && i.id != "" {
		expected, err := SidToWords(i.sid)
		if err != nil {
			return err
		}
		if expected != i.id {
			return fmt.Errorf("%w: sid %d → %q, stored id %q", ErrSidIdMismatch, i.sid, expected, i.id)
		}
	}
	return nil
}

// InfoFunc iterates live shepherds, yielding each one's TOML name and numeric SID.
// Returning false from yield stops iteration early.
type InfoFunc func(yield func(name string, sid int64) bool)

// Resolve fills in the SID from i.Name by scanning live shepherds via infoFn.
// Returns the resolved SID, or an error if the name is not found or is ambiguous.
func (i Id) Resolve(infoFn InfoFunc) (int64, error) {
	if i.name == "" {
		if i.sid != 0 {
			return i.sid, nil
		}
		return 0, fmt.Errorf("shep: no name set on Id")
	}
	found := int64(-1)
	count := 0
	infoFn(func(name string, sid int64) bool {
		if name == i.name {
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
		return 0, fmt.Errorf("shep: no shepherd with name %q", i.name)
	case 1:
		return found, nil
	default:
		return 0, fmt.Errorf("shep: ambiguous: %d shepherds named %q", count, i.name)
	}
}
