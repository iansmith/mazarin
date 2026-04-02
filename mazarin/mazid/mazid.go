// Package mazid converts between "word-word-word" human-readable identifiers
// and compact integers.
//
// Identifiers have the form {adjective}-{band}-{person}, drawn from three
// fixed wordlists of 128, 64, and 128 entries respectively. This yields
// 2^20 = 1,048,576 unique IDs, each fitting comfortably in a uint32.
//
// Bit layout of a uint32 ID:
//
//	bits  0– 6  wordlist3 index (person,    7 bits, 128 values)
//	bits  7–12  wordlist2 index (band,      6 bits,  64 values)
//	bits 13–19  wordlist1 index (adjective, 7 bits, 128 values)
//	bits 20–31  unused (always zero)
package mazid

import (
	"errors"
	"fmt"
	"strings"
)

const (
	wl3Bits = 7
	wl2Bits = 6
	wl1Bits = 7

	wl3Shift = 0
	wl2Shift = wl3Bits
	wl1Shift = wl3Bits + wl2Bits

	wl3Mask = uint32((1 << wl3Bits) - 1)
	wl2Mask = uint32((1 << wl2Bits) - 1)
	wl1Mask = uint32((1 << wl1Bits) - 1)

	// MaxID is the largest valid uint32 ID (2^20 - 1 = 1,048,575).
	MaxID = uint32((1 << (wl1Bits + wl2Bits + wl3Bits)) - 1)
)

var (
	ErrInvalidWord   = errors.New("mazid: word not found in wordlist")
	ErrInvalidFormat = errors.New("mazid: invalid identifier format")
	ErrOutOfRange    = errors.New("mazid: value out of range")
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

// Parse parses a "word-word-word" identifier and returns its uint32 value.
func Parse(s string) (uint32, error) {
	w1, w2, w3, err := split(s)
	if err != nil {
		return 0, err
	}
	return encode(w1, w2, w3)
}

// String converts a uint32 ID to its "word-word-word" string representation.
func String(n uint32) (string, error) {
	w1, w2, w3, err := decode(n)
	if err != nil {
		return "", err
	}
	return w1 + "-" + w2 + "-" + w3, nil
}

// Encode returns the uint32 ID for explicit words from each wordlist.
// w1 must be in the adjective list, w2 in the band list, w3 in the person list.
func Encode(w1, w2, w3 string) (uint32, error) {
	return encode(w1, w2, w3)
}

// Decode returns the three component words for a uint32 ID.
func Decode(n uint32) (adjective, band, person string, err error) {
	return decode(n)
}

// ToUint64 widens a uint32 ID to uint64 for storage or transmission in
// contexts that require 64-bit values. The upper 32 bits are always zero.
func ToUint64(n uint32) uint64 { return uint64(n) }

// ToUint32 narrows a uint64 back to a uint32 ID.
// Returns ErrOutOfRange if the value exceeds MaxID.
func ToUint32(n uint64) (uint32, error) {
	if n > uint64(MaxID) {
		return 0, fmt.Errorf("%w: %d > %d", ErrOutOfRange, n, MaxID)
	}
	return uint32(n), nil
}

// split splits "w1-w2-w3" into its three components.
func split(s string) (string, string, string, error) {
	parts := strings.SplitN(s, "-", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", fmt.Errorf("%w: %q", ErrInvalidFormat, s)
	}
	return parts[0], parts[1], parts[2], nil
}

func encode(w1, w2, w3 string) (uint32, error) {
	i1, ok := wl1Index[w1]
	if !ok {
		return 0, fmt.Errorf("%w: adjective %q", ErrInvalidWord, w1)
	}
	i2, ok := wl2Index[w2]
	if !ok {
		return 0, fmt.Errorf("%w: band %q", ErrInvalidWord, w2)
	}
	i3, ok := wl3Index[w3]
	if !ok {
		return 0, fmt.Errorf("%w: person %q", ErrInvalidWord, w3)
	}
	return (i1 << wl1Shift) | (i2 << wl2Shift) | i3, nil
}

func decode(n uint32) (string, string, string, error) {
	if n > MaxID {
		return "", "", "", fmt.Errorf("%w: %d > %d", ErrOutOfRange, n, MaxID)
	}
	i1 := (n >> wl1Shift) & wl1Mask
	i2 := (n >> wl2Shift) & wl2Mask
	i3 := n & wl3Mask
	return wordlist1[i1], wordlist2[i2], wordlist3[i3], nil
}
