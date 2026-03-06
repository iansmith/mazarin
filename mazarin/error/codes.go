// Package error provides allocation-free error codes for Mazzy userspace.
//
// ErrorCode values are returned directly from kernel Mazzy-specific syscalls
// (not Linux syscalls, which use -errno). Client code compares the Code field
// of *Error to identify specific failures.
package error

// ErrorCode is a 32-bit error identifier shared between kernel and userspace.
// Values must stay in sync with kmazarin/ksyscall/run.go constants.
type ErrorCode uint32

const (
	// Validation errors (returned by kernel syscalls)
	NotWritableData     ErrorCode = 0x1000 + iota // pointer not in writable memory
	NotInExec                                      // address not in executable segment
	NotAccessibleMemory                            // pointer not in valid memory
	NullPointer                                    // unexpected null pointer
	InvalidFilename                                // malformed or empty filename
	TooLarge                                       // value exceeds maximum
	TooSmall                                       // value below minimum
	InvalidELF                                     // not a valid ELF file
	WrongArch                                      // wrong architecture
	NoSymbol                                       // required symbol not found
	AlreadyLoaded                                  // program already loaded
	NoSpace                                        // no address space available
	FileNotFound                                   // file does not exist

	// Client-side errors (memblob, mazhost, etc.)
	ExceedsCapacity  // request exceeds MemBlob size
	ReadOnlyBlob     // MemBlob is read-only
	MmapFailed       // mmap failed
	InvalidSize      // invalid size argument
	NilBuffer        // nil buffer argument
	NotImplemented   // feature not yet implemented
	PriestInitFailed // MazarinPriest() returned an error
)

// Error is a non-allocating error value. Pre-defined package-level
// vars are returned by pointer — callers compare the Code field.
type Error struct {
	Code    ErrorCode
	message string
}

func (e *Error) Error() string { return e.message }

// Pre-defined errors (allocated once at init, never on the hot path).
var (
	ErrNotWritableData     = &Error{NotWritableData, "pointer not in writable memory"}
	ErrNotInExec           = &Error{NotInExec, "address not in executable segment"}
	ErrNotAccessibleMemory = &Error{NotAccessibleMemory, "pointer not in valid memory"}
	ErrNullPointer         = &Error{NullPointer, "unexpected null pointer"}
	ErrInvalidFilename     = &Error{InvalidFilename, "invalid filename"}
	ErrTooLarge            = &Error{TooLarge, "value exceeds maximum"}
	ErrTooSmall            = &Error{TooSmall, "value below minimum"}
	ErrInvalidELF          = &Error{InvalidELF, "not a valid ELF file"}
	ErrWrongArch           = &Error{WrongArch, "wrong architecture"}
	ErrNoSymbol            = &Error{NoSymbol, "required symbol not found"}
	ErrAlreadyLoaded       = &Error{AlreadyLoaded, "program already loaded"}
	ErrNoSpace             = &Error{NoSpace, "no address space available"}
	ErrFileNotFound        = &Error{FileNotFound, "file not found"}
	ErrExceedsCapacity     = &Error{ExceedsCapacity, "request exceeds size of MemBlob"}
	ErrReadOnlyBlob        = &Error{ReadOnlyBlob, "MemBlob is read-only"}
	ErrMmapFailed          = &Error{MmapFailed, "mmap failed"}
	ErrInvalidSize         = &Error{InvalidSize, "invalid size"}
	ErrNilBuffer           = &Error{NilBuffer, "nil buffer"}
	ErrNotImplemented      = &Error{NotImplemented, "not implemented"}
	ErrPriestInitFailed    = &Error{PriestInitFailed, "MazarinPriest() returned an error"}
)

// codeToError maps ErrorCode values to pre-defined *Error values.
var codeToError = [...]**Error{
	NotWritableData - 0x1000:     &ErrNotWritableData,
	NotInExec - 0x1000:           &ErrNotInExec,
	NotAccessibleMemory - 0x1000: &ErrNotAccessibleMemory,
	NullPointer - 0x1000:         &ErrNullPointer,
	InvalidFilename - 0x1000:     &ErrInvalidFilename,
	TooLarge - 0x1000:            &ErrTooLarge,
	TooSmall - 0x1000:            &ErrTooSmall,
	InvalidELF - 0x1000:          &ErrInvalidELF,
	WrongArch - 0x1000:           &ErrWrongArch,
	NoSymbol - 0x1000:            &ErrNoSymbol,
	AlreadyLoaded - 0x1000:       &ErrAlreadyLoaded,
	NoSpace - 0x1000:             &ErrNoSpace,
	FileNotFound - 0x1000:        &ErrFileNotFound,
	ExceedsCapacity - 0x1000:     &ErrExceedsCapacity,
	ReadOnlyBlob - 0x1000:        &ErrReadOnlyBlob,
	MmapFailed - 0x1000:          &ErrMmapFailed,
	InvalidSize - 0x1000:         &ErrInvalidSize,
	NilBuffer - 0x1000:           &ErrNilBuffer,
	NotImplemented - 0x1000:      &ErrNotImplemented,
	PriestInitFailed - 0x1000:    &ErrPriestInitFailed,
}

// FromCode returns the pre-defined *Error for a given ErrorCode,
// or nil if the code is not recognized.
func FromCode(code ErrorCode) *Error {
	idx := code - 0x1000
	if int(idx) >= len(codeToError) {
		return nil
	}
	return *codeToError[idx]
}
