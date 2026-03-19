// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Process etc.
//
// Mazzy maz-overlay: os.Exit replaced with panic so the host shepherd
// can catch it via defer/recover instead of being killed.

package os

import (
	"runtime"
	"syscall"
)

// Args hold the command-line arguments, starting with the program name.
var Args []string

func init() {
	if runtime.GOOS == "windows" {
		// Initialized in exec_windows.go.
		return
	}
	Args = runtime_args()
}

func runtime_args() []string // in package runtime

// Getuid returns the numeric user id of the caller.
//
// On Windows, it returns -1.
func Getuid() int { return syscall.Getuid() }

// Geteuid returns the numeric effective user id of the caller.
//
// On Windows, it returns -1.
func Geteuid() int { return syscall.Geteuid() }

// Getgid returns the numeric group id of the caller.
//
// On Windows, it returns -1.
func Getgid() int { return syscall.Getgid() }

// Getegid returns the numeric effective group id of the caller.
//
// On Windows, it returns -1.
func Getegid() int { return syscall.Getegid() }

// Getgroups returns a list of the numeric ids of groups that the caller belongs to.
//
// On Windows, it returns [syscall.EWINDOWS]. See the [os/user] package
// for a possible alternative.
func Getgroups() ([]int, error) {
	gids, e := syscall.Getgroups()
	return gids, NewSyscallError("getgroups", e)
}

// Exit causes the current program to exit with the given status code.
// Conventionally, code zero indicates success, non-zero an error.
//
// Mazzy maz-overlay: In .maz modules, os.Exit is replaced with a panic
// so the host shepherd can recover from it instead of being terminated.
// The program terminates immediately; deferred functions are not run.
//
// For portability, the status code should be in the range [0, 125].
func Exit(code int) {
	// Maz overlay: skip runtime_beforeExit (race/coverage hooks not needed)
	// and just panic so the host shepherd can recover.
	panic("maz: os.Exit() called")
}
