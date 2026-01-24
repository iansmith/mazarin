// Package error provides Mazzy error code definitions.
// Error codes are returned from syscalls as a single 64-bit value
// encoding two 32-bit integers: major (category) and minor (specific).
package error

import (
	"errors"
	"fmt"
)

// Major error codes - categories of errors
var Major = [...]uint32{
	0: 0,          // Success (no error)
	1: 0x00010000, // BadArg - invalid argument
	2: 0x00020000, // NotFound - resource not found
	3: 0x00030000, // IOError - I/O operation failed
	4: 0x00040000, // NoMem - out of memory
	5: 0x00050000, // Denied - permission denied
	6: 0x00060000, // Busy - resource busy
	7: 0x00070000, // Internal - internal kernel error
}

// Minor error codes - specific errors within categories
var Minor = [...]uint32{
	0:  0,  // None (no specific error)
	1:  1,  // NotWritableData - pointer not in caller's writable memory (heap/data/bss/stack)
	2:  2,  // NotInExec - address not in caller's executable segment
	3:  3,  // NotAccessibleMemory - pointer not in valid/accessible memory
	4:  4,  // NullPointer - unexpected null pointer
	5:  5,  // InvalidFilename - malformed filename
	6:  6,  // TooLarge - value exceeds maximum
	7:  7,  // TooSmall - value below minimum
	8:  8,  // InvalidELF - not a valid ELF file
	9:  9,  // WrongArch - wrong architecture
	10: 10, // NoSymbol - required symbol not found
	11: 11, // AlreadyLoaded - program already loaded at address
	12: 12, // NoSpace - no address space available
	13: 13, // FileNotFound - file does not exist
}

// Named constants for convenience
const (
	MajorSuccess  = 0
	MajorBadArg   = 1
	MajorNotFound = 2
	MajorIOError  = 3
	MajorNoMem    = 4
	MajorDenied   = 5
	MajorBusy     = 6
	MajorInternal = 7

	MinorNone              = 0
	MinorNotWritableData   = 1
	MinorNotInExec         = 2
	MinorNotAccessibleMem  = 3
	MinorNullPointer       = 4
	MinorInvalidFilename   = 5
	MinorTooLarge          = 6
	MinorTooSmall          = 7
	MinorInvalidELF        = 8
	MinorWrongArch         = 9
	MinorNoSymbol          = 10
	MinorAlreadyLoaded     = 11
	MinorNoSpace           = 12
	MinorFileNotFound      = 13
)

// Encode combines major and minor codes into a 64-bit error value
func Encode(major, minor uint32) uint64 {
	return (uint64(major) << 32) | uint64(minor)
}

// Decode extracts major and minor codes from a 64-bit error value.
// Returns error if the value doesn't represent a valid error code
// (i.e., major code is 0, which means success).
func Decode(value uint64) (major, minor uint32, err error) {
	major = uint32(value >> 32)
	minor = uint32(value & 0xFFFFFFFF)

	if major == 0 && minor == 0 {
		return 0, 0, errors.New("not an error: success code")
	}

	// Validate major code is known
	validMajor := false
	for i := 1; i < len(Major); i++ {
		if Major[i] == major {
			validMajor = true
			break
		}
	}
	if !validMajor {
		return major, minor, fmt.Errorf("unknown major code: 0x%08x", major)
	}

	return major, minor, nil
}

// IsError returns true if the value represents an error (major != 0)
func IsError(value uint64) bool {
	return (value >> 32) != 0
}

// String returns a human-readable description of the error
func String(value uint64) string {
	major, minor, err := Decode(value)
	if err != nil {
		return fmt.Sprintf("invalid error code: 0x%016x", value)
	}

	majorStr := majorToString(major)
	minorStr := minorToString(minor)

	return fmt.Sprintf("%s: %s", majorStr, minorStr)
}

func majorToString(code uint32) string {
	switch code {
	case Major[MajorBadArg]:
		return "BadArg"
	case Major[MajorNotFound]:
		return "NotFound"
	case Major[MajorIOError]:
		return "IOError"
	case Major[MajorNoMem]:
		return "NoMem"
	case Major[MajorDenied]:
		return "Denied"
	case Major[MajorBusy]:
		return "Busy"
	case Major[MajorInternal]:
		return "Internal"
	default:
		return fmt.Sprintf("Unknown(0x%08x)", code)
	}
}

func minorToString(code uint32) string {
	switch code {
	case Minor[MinorNone]:
		return "None"
	case Minor[MinorNotWritableData]:
		return "NotWritableData"
	case Minor[MinorNotInExec]:
		return "NotInExec"
	case Minor[MinorNotAccessibleMem]:
		return "NotAccessibleMemory"
	case Minor[MinorNullPointer]:
		return "NullPointer"
	case Minor[MinorInvalidFilename]:
		return "InvalidFilename"
	case Minor[MinorTooLarge]:
		return "TooLarge"
	case Minor[MinorTooSmall]:
		return "TooSmall"
	case Minor[MinorInvalidELF]:
		return "InvalidELF"
	case Minor[MinorWrongArch]:
		return "WrongArch"
	case Minor[MinorNoSymbol]:
		return "NoSymbol"
	case Minor[MinorAlreadyLoaded]:
		return "AlreadyLoaded"
	case Minor[MinorNoSpace]:
		return "NoSpace"
	case Minor[MinorFileNotFound]:
		return "FileNotFound"
	default:
		return fmt.Sprintf("Unknown(%d)", code)
	}
}
