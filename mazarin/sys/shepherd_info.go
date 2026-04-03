package sys

import (
	"errors"
	"mazzy/shared/hid"
	"mazzy/shared/mazzy"
	"mazzy/shep"
	"syscall"
	"unsafe"
)

// ShepherdEntry is the clean userspace representation of a running shepherd.
type ShepherdEntry struct {
	Id       shep.Id  // Fully-hydrated identifier (Sid, word-triple Id, and Name all populated)
	Filename string   // Launch filename (e.g. "/rachel.elf")
	Threads  []int16  // TIDs of threads belonging to this shepherd
}

// ShepherdInfo returns information about all running shepherds.
func ShepherdInfo() ([]ShepherdEntry, error) {
	raw, err := rawShepherdInfo()
	if err != nil {
		return nil, err
	}
	entries := make([]ShepherdEntry, len(raw))
	for i, e := range raw {
		si := shep.NewFromInt16(e.PID)
		if e.NameLen > 0 {
			name := string(e.Name[:e.NameLen])
			si = si.WithName(name)
		}
		var threads []int16
		for _, tid := range e.ThreadIDs {
			if tid != -1 {
				threads = append(threads, tid)
			}
		}
		entries[i] = ShepherdEntry{
			Id:      si,
			Filename: string(e.Filename[:e.FilenameLen]),
			Threads: threads,
		}
	}
	return entries, nil
}

// rawShepherdInfo returns the raw wire-format entries from the kernel.
func rawShepherdInfo() ([]hid.ShepherdInfoEntry, error) {
	var buf [32]hid.ShepherdInfoEntry
	entrySize := unsafe.Sizeof(buf[0])
	for i := range buf {
		p := (*byte)(unsafe.Pointer(uintptr(unsafe.Pointer(&buf[0])) + uintptr(i)*entrySize))
		*p = 0
	}
	r1, _, errno := syscall.RawSyscall6(
		mazzy.SysShepherdInfo,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		0, 0, 0, 0,
	)
	if errno != 0 {
		return nil, errors.New("ShepherdInfo failed")
	}
	n := int(r1)
	if n > len(buf) {
		n = len(buf)
	}
	return buf[:n], nil
}
