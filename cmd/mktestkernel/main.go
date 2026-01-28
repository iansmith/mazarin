// cmd/mktestkernel/main.go
// Generates a minimal test kernel ELF for diplomat testing.
// The kernel writes a message to COM1 (0x3F8) and halts.
// No Go runtime, no dependencies - just raw x86_64 machine code.
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"os"
)

const (
	loadAddr   = 0x100000 // 1MB - where kernel is loaded
	com1Port   = 0x3F8    // Serial port
	elfMagic   = 0x464C457F
	elfClass64 = 2
	elfData2LE = 1
	elfTypeExe = 2
	elfMachX64 = 0x3E
	ptLoad     = 1
	pfX        = 1
	pfW        = 2
	pfR        = 4
)

// interpretEscapes converts escape sequences like \r\n to actual characters
func interpretEscapes(s string) string {
	result := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'r':
				result = append(result, '\r')
				i++
			case 'n':
				result = append(result, '\n')
				i++
			case 't':
				result = append(result, '\t')
				i++
			case '\\':
				result = append(result, '\\')
				i++
			default:
				result = append(result, s[i])
			}
		} else {
			result = append(result, s[i])
		}
	}
	return string(result)
}

func main() {
	output := flag.String("o", "testkernel.elf", "output file")
	message := flag.String("m", "=== KMAZARIN TEST KERNEL RUNNING ===\r\n", "message to print")
	addr := flag.Uint64("addr", loadAddr, "load address (entry point)")
	flag.Parse()

	// Interpret escape sequences in the message
	msg := interpretEscapes(*message)

	// Generate machine code
	code := generateCode(msg)

	// Create ELF
	elf := createELF(code, *addr)

	if err := os.WriteFile(*output, elf, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", *output, err)
		os.Exit(1)
	}

	fmt.Printf("Created %s (%d bytes)\n", *output, len(elf))
	fmt.Printf("  Entry point: 0x%X\n", *addr)
	fmt.Printf("  Message: %q\n", *message)
}

// generateCode creates x86_64 machine code to print a message to COM1 and halt
func generateCode(msg string) []byte {
	var code []byte

	// For each character, emit:
	//   mov al, <char>      ; B0 xx
	//   mov dx, 0x3F8       ; 66 BA F8 03
	//   out dx, al          ; EE
	for i := 0; i < len(msg); i++ {
		c := msg[i]
		code = append(code,
			0xB0, c,             // mov al, <char>
			0x66, 0xBA, 0xF8, 0x03, // mov dx, 0x3F8
			0xEE, // out dx, al
		)
	}

	// Infinite halt loop:
	//   cli                 ; FA
	//   hlt                 ; F4
	//   jmp -2              ; EB FE (jump back to hlt)
	code = append(code, 0xFA, 0xF4, 0xEB, 0xFE)

	return code
}

// ELF64 header (64 bytes)
type elf64Ehdr struct {
	Ident     [16]byte
	Type      uint16
	Machine   uint16
	Version   uint32
	Entry     uint64
	Phoff     uint64
	Shoff     uint64
	Flags     uint32
	Ehsize    uint16
	Phentsize uint16
	Phnum     uint16
	Shentsize uint16
	Shnum     uint16
	Shstrndx  uint16
}

// ELF64 program header (56 bytes)
type elf64Phdr struct {
	Type   uint32
	Flags  uint32
	Offset uint64
	Vaddr  uint64
	Paddr  uint64
	Filesz uint64
	Memsz  uint64
	Align  uint64
}

func createELF(code []byte, entry uint64) []byte {
	ehdrSize := uint64(64)
	phdrSize := uint64(56)
	// Code starts right after ELF header and one program header
	codeOffset := ehdrSize + phdrSize

	// Create ELF header
	ehdr := elf64Ehdr{
		Type:      elfTypeExe,
		Machine:   elfMachX64,
		Version:   1,
		Entry:     entry,
		Phoff:     ehdrSize,
		Shoff:     0, // no sections
		Flags:     0,
		Ehsize:    uint16(ehdrSize),
		Phentsize: uint16(phdrSize),
		Phnum:     1,
		Shentsize: 0,
		Shnum:     0,
		Shstrndx:  0,
	}

	// ELF magic and identification
	ehdr.Ident[0] = 0x7F
	ehdr.Ident[1] = 'E'
	ehdr.Ident[2] = 'L'
	ehdr.Ident[3] = 'F'
	ehdr.Ident[4] = elfClass64
	ehdr.Ident[5] = elfData2LE
	ehdr.Ident[6] = 1 // EV_CURRENT

	// Create program header for loadable segment
	phdr := elf64Phdr{
		Type:   ptLoad,
		Flags:  pfR | pfX,
		Offset: codeOffset,
		Vaddr:  entry,
		Paddr:  entry,
		Filesz: uint64(len(code)),
		Memsz:  uint64(len(code)),
		Align:  0x1000,
	}

	// Serialize to bytes
	buf := make([]byte, codeOffset+uint64(len(code)))

	// Write ELF header
	copy(buf[0:16], ehdr.Ident[:])
	binary.LittleEndian.PutUint16(buf[16:], ehdr.Type)
	binary.LittleEndian.PutUint16(buf[18:], ehdr.Machine)
	binary.LittleEndian.PutUint32(buf[20:], ehdr.Version)
	binary.LittleEndian.PutUint64(buf[24:], ehdr.Entry)
	binary.LittleEndian.PutUint64(buf[32:], ehdr.Phoff)
	binary.LittleEndian.PutUint64(buf[40:], ehdr.Shoff)
	binary.LittleEndian.PutUint32(buf[48:], ehdr.Flags)
	binary.LittleEndian.PutUint16(buf[52:], ehdr.Ehsize)
	binary.LittleEndian.PutUint16(buf[54:], ehdr.Phentsize)
	binary.LittleEndian.PutUint16(buf[56:], ehdr.Phnum)
	binary.LittleEndian.PutUint16(buf[58:], ehdr.Shentsize)
	binary.LittleEndian.PutUint16(buf[60:], ehdr.Shnum)
	binary.LittleEndian.PutUint16(buf[62:], ehdr.Shstrndx)

	// Write program header
	off := ehdrSize
	binary.LittleEndian.PutUint32(buf[off:], phdr.Type)
	binary.LittleEndian.PutUint32(buf[off+4:], phdr.Flags)
	binary.LittleEndian.PutUint64(buf[off+8:], phdr.Offset)
	binary.LittleEndian.PutUint64(buf[off+16:], phdr.Vaddr)
	binary.LittleEndian.PutUint64(buf[off+24:], phdr.Paddr)
	binary.LittleEndian.PutUint64(buf[off+32:], phdr.Filesz)
	binary.LittleEndian.PutUint64(buf[off+40:], phdr.Memsz)
	binary.LittleEndian.PutUint64(buf[off+48:], phdr.Align)

	// Write code
	copy(buf[codeOffset:], code)

	return buf
}
