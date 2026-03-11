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
	NoDelegate                                     // no delegate registered for this syscall
	NotReady                                       // delegate priest not ready
	TransferFailed                                 // page transfer failed

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
	context string // optional context (e.g., filename)
}

func (e *Error) Error() string {
	if e.context != "" {
		return e.message + ": " + e.context
	}
	return e.message
}

// Wrap returns a new Error with the same code but additional context.
// The original pre-defined Error is not modified.
func (e *Error) Wrap(context string) *Error {
	return &Error{Code: e.Code, message: e.message, context: context}
}

// Pre-defined errors (allocated once at init, never on the hot path).
var (
	ErrNotWritableData     = &Error{Code: NotWritableData, message: "pointer not in writable memory"}
	ErrNotInExec           = &Error{Code: NotInExec, message: "address not in executable segment"}
	ErrNotAccessibleMemory = &Error{Code: NotAccessibleMemory, message: "pointer not in valid memory"}
	ErrNullPointer         = &Error{Code: NullPointer, message: "unexpected null pointer"}
	ErrInvalidFilename     = &Error{Code: InvalidFilename, message: "invalid filename"}
	ErrTooLarge            = &Error{Code: TooLarge, message: "value exceeds maximum"}
	ErrTooSmall            = &Error{Code: TooSmall, message: "value below minimum"}
	ErrInvalidELF          = &Error{Code: InvalidELF, message: "not a valid ELF file"}
	ErrWrongArch           = &Error{Code: WrongArch, message: "wrong architecture"}
	ErrNoSymbol            = &Error{Code: NoSymbol, message: "required symbol not found"}
	ErrAlreadyLoaded       = &Error{Code: AlreadyLoaded, message: "program already loaded"}
	ErrNoSpace             = &Error{Code: NoSpace, message: "no address space available"}
	ErrFileNotFound        = &Error{Code: FileNotFound, message: "file not found"}
	ErrNoDelegate          = &Error{Code: NoDelegate, message: "no delegate registered"}
	ErrNotReady            = &Error{Code: NotReady, message: "delegate priest not ready"}
	ErrTransferFailed      = &Error{Code: TransferFailed, message: "page transfer failed"}
	ErrExceedsCapacity     = &Error{Code: ExceedsCapacity, message: "request exceeds size of MemBlob"}
	ErrReadOnlyBlob        = &Error{Code: ReadOnlyBlob, message: "MemBlob is read-only"}
	ErrMmapFailed          = &Error{Code: MmapFailed, message: "mmap failed"}
	ErrInvalidSize         = &Error{Code: InvalidSize, message: "invalid size"}
	ErrNilBuffer           = &Error{Code: NilBuffer, message: "nil buffer"}
	ErrNotImplemented      = &Error{Code: NotImplemented, message: "not implemented"}
	ErrPriestInitFailed    = &Error{Code: PriestInitFailed, message: "MazarinPriest() returned an error"}
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
	NoDelegate - 0x1000:          &ErrNoDelegate,
	NotReady - 0x1000:            &ErrNotReady,
	TransferFailed - 0x1000:      &ErrTransferFailed,
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
