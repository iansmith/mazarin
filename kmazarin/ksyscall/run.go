package ksyscall

// Mazzy ErrorCode values — must stay in sync with mazarin/error/codes.go.
// These are returned directly as syscall results (not negative errno).
const (
	errNotWritableData     uint32 = 0x1000
	errNotInExec           uint32 = 0x1001
	errNotAccessibleMemory uint32 = 0x1002
	errNullPointer         uint32 = 0x1003
	errInvalidFilename     uint32 = 0x1004
	errTooLarge            uint32 = 0x1005
	errTooSmall            uint32 = 0x1006
	errInvalidELF          uint32 = 0x1007
	errWrongArch           uint32 = 0x1008
	errNoSymbol            uint32 = 0x1009
	errAlreadyLoaded       uint32 = 0x100A
	errNoSpace             uint32 = 0x100B
	errFileNotFound        uint32 = 0x100C
	errNoDelegate          uint32 = 0x100D
	errNotReady            uint32 = 0x100E
	errTransferFailed      uint32 = 0x100F
)
