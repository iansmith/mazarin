package main

import (
	"runtime"
)

// PrintTraceback prints a stack traceback for the faulting goroutine
// Uses Go's non-standard ARM64 frame layout with FP-based walking
// pc = faulting instruction's PC
// fp = saved frame pointer at time of exception
// lr = saved link register (x30) at time of exception
// gp = goroutine pointer (x28) at time of exception
//
// NOTE: Go's ARM64 frame layout is:
//   [FP+8] = saved LR (return address)
//   [FP+32] = previous FP
// Additionally, Go sometimes stores extra return addresses at [FP+40]+ for
// functions with complex control flow (defer, panic recovery, etc.)
//
//go:nosplit
func PrintTraceback(pc, fp, lr, gp uintptr) {
	print("\r\n=== Stack Traceback ===\r\n")
	print("Exception at PC=0x")
	printHex64(uint64(pc))
	print(" FP=0x")
	printHex64(uint64(fp))
	print(" LR=0x")
	printHex64(uint64(lr))
	print(" g=0x")
	printHex64(uint64(gp))
	print("\r\n\r\n")

	// Print first frame (the faulting PC)
	printFrame(1, pc)
	frameNum := 2

	// Print second frame (the LR - return address in the calling function)
	if lr != 0 {
		printFrame(frameNum, lr)
		frameNum++
	}

	// Now walk the stack using FP chain
	currentFP := fp
	currentPC := lr

	// Walk up to 20 frames
	for i := 0; i < 18 && currentFP != 0; i++ {
		// Safety check: make sure FP is in a valid range
		if currentFP < 0x40000000 || currentFP > 0x80000000 {
			break
		}

		// Read frame data using Go's ARM64 layout
		savedLR := uintptr(readMemory64(currentFP + 8))
		prevFP := uintptr(readMemory64(currentFP + 32))

		// Sanity checks
		if prevFP == 0 || prevFP == currentFP || savedLR == 0 {
			break
		}

		// WORKAROUND: Go sometimes stores additional return addresses in the same frame
		// Check [FP+40] through [FP+56] for valid code addresses that might be skipped frames
		// This handles cases where frame pointers don't form a perfect chain
		for offset := uintptr(40); offset <= 56; offset += 8 {
			extraPC := uintptr(readMemory64(currentFP + offset))
			// Check if it's a valid code address and different from savedLR
			if extraPC >= 0x300000 && extraPC < 0x600000 && extraPC != savedLR && extraPC != currentPC {
				// Verify this PC actually points to a function
				fn := runtime.FuncForPC(extraPC)
				if fn != nil {
					printFrame(frameNum, extraPC)
					frameNum++
				}
			}
		}

		// Print the next frame
		printFrame(frameNum, savedLR)
		frameNum++

		// Move to the previous frame
		currentFP = prevFP
		currentPC = savedLR
	}

	print("\r\n=== End Traceback ===\r\n")
}

// printFrame prints information about a single stack frame
//
//go:nosplit
func printFrame(frameNum int, pc uintptr) {
	// Print frame number
	print("#")
	printDecimal(frameNum)
	print(" PC=0x")
	printHex64(uint64(pc))

	// Look up function info for this PC using runtime.FuncForPC
	fn := runtime.FuncForPC(pc)
	if fn != nil {
		// Get function name
		funcName := fn.Name()
		if funcName != "" {
			print(" in ")
			print(funcName)
		}

		// Get file:line
		file, line := fn.FileLine(pc)
		if file != "" {
			print(" at ")
			print(file)
			print(":")
			printDecimal(line)
		}
	}

	print("\r\n")
}

// printDecimal prints a decimal number
//
//go:nosplit
func printDecimal(n int) {
	if n < 0 {
		printChar('-')
		n = -n
	}

	if n == 0 {
		printChar('0')
		return
	}

	// Convert to decimal digits
	var buf [20]byte
	i := 0
	for n > 0 {
		buf[i] = byte('0' + (n % 10))
		n /= 10
		i++
	}

	// Print in reverse order
	for i > 0 {
		i--
		printChar(buf[i])
	}
}
