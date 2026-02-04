package main

// debugPrintHex32 prints a 32-bit hex value directly to the serial port.
// Architecture-neutral — uses ForceSerialCharacter for output.
//
//go:nosplit
func debugPrintHex32(val uint32) {
	hexChars := "0123456789ABCDEF"
	for i := 28; i >= 0; i -= 4 {
		nibble := (val >> i) & 0xF
		ForceSerialCharacter(hexChars[nibble])
	}
}

// debugPrintStr prints a string directly to the serial port.
// Architecture-neutral — uses ForceSerialCharacter for output.
//
//go:nosplit
func debugPrintStr(s string) {
	for i := 0; i < len(s); i++ {
		ForceSerialCharacter(s[i])
	}
}

// debugPrintHex64 prints a 64-bit hex value directly to the serial port.
// Architecture-neutral — uses ForceSerialCharacter for output.
//
//go:nosplit
func debugPrintHex64(val uint64) {
	hexChars := "0123456789ABCDEF"
	for i := 60; i >= 0; i -= 4 {
		nibble := (val >> i) & 0xF
		ForceSerialCharacter(byte(hexChars[nibble]))
	}
}

// debugPrintNewline prints CR+LF directly to the serial port.
//
//go:nosplit
func debugPrintNewline() {
	ForceSerialCharacter('\r')
	ForceSerialCharacter('\n')
}

// debugPrintLabeledHex32 prints "label: 0xVALUE\r\n" directly to serial.
//
//go:nosplit
func debugPrintLabeledHex32(label string, val uint32) {
	debugPrintStr(label)
	debugPrintStr("0x")
	debugPrintHex32(val)
	debugPrintNewline()
}

// debugPrintLabeledHex64 prints "label: 0xVALUE\r\n" directly to serial.
//
//go:nosplit
func debugPrintLabeledHex64(label string, val uint64) {
	debugPrintStr(label)
	debugPrintStr("0x")
	debugPrintHex64(val)
	debugPrintNewline()
}
