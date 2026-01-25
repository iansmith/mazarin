// pe-fixup converts a Go-produced Windows PE executable into a valid UEFI application.
//
// The Go compiler with GOOS=windows GOARCH=amd64 produces PE/COFF executables that are
// almost valid UEFI applications. The main difference is the PE subsystem field:
//   - Windows Console: 3 (IMAGE_SUBSYSTEM_WINDOWS_CUI)
//   - UEFI Application: 10 (IMAGE_SUBSYSTEM_EFI_APPLICATION)
//
// This tool patches that single field, making the PE loadable by UEFI firmware.
//
// Usage:
//
//	pe-fixup input.exe output.efi
package main

import (
	"debug/pe"
	"encoding/binary"
	"fmt"
	"os"
)

const (
	// PE Subsystem values from the PE/COFF specification
	IMAGE_SUBSYSTEM_UNKNOWN         = 0
	IMAGE_SUBSYSTEM_NATIVE          = 1
	IMAGE_SUBSYSTEM_WINDOWS_GUI     = 2
	IMAGE_SUBSYSTEM_WINDOWS_CUI     = 3
	IMAGE_SUBSYSTEM_EFI_APPLICATION = 10
	IMAGE_SUBSYSTEM_EFI_BOOT_DRIVER = 11
	IMAGE_SUBSYSTEM_EFI_RUNTIME     = 12
	IMAGE_SUBSYSTEM_EFI_ROM         = 13

	// PE Optional Header offset for Subsystem field
	// For PE32+ (64-bit), Subsystem is at offset 68 from start of optional header
	PE32PlusSubsystemOffset = 68
)

func main() {
	if len(os.Args) != 3 {
		printUsage()
		os.Exit(1)
	}

	inputPath := os.Args[1]
	outputPath := os.Args[2]

	if err := fixupPE(inputPath, outputPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "pe-fixup - Convert Go Windows PE to UEFI application\n\n")
	fmt.Fprintf(os.Stderr, "Usage: pe-fixup <input.exe> <output.efi>\n\n")
	fmt.Fprintf(os.Stderr, "Patches the PE subsystem field from %d (Windows Console) to %d (EFI Application).\n",
		IMAGE_SUBSYSTEM_WINDOWS_CUI, IMAGE_SUBSYSTEM_EFI_APPLICATION)
	fmt.Fprintf(os.Stderr, "\nThis allows Go binaries built with GOOS=windows GOARCH=amd64 CGO_ENABLED=0\n")
	fmt.Fprintf(os.Stderr, "to be loaded directly by UEFI firmware.\n")
}

func fixupPE(inputPath, outputPath string) error {
	// First pass: validate the PE file using Go's debug/pe package
	if err := validatePE(inputPath); err != nil {
		return err
	}

	// Second pass: read raw bytes and patch
	data, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("failed to read input file: %w", err)
	}

	// Locate and patch the subsystem field
	if err := patchSubsystem(data); err != nil {
		return err
	}

	// Write the patched file
	if err := os.WriteFile(outputPath, data, 0755); err != nil {
		return fmt.Errorf("failed to write output file: %w", err)
	}

	fmt.Printf("Created UEFI application: %s\n", outputPath)
	return nil
}

func validatePE(path string) error {
	peFile, err := pe.Open(path)
	if err != nil {
		return fmt.Errorf("failed to parse PE file: %w", err)
	}
	defer peFile.Close()

	// Verify it's PE32+ (64-bit)
	optHeader, ok := peFile.OptionalHeader.(*pe.OptionalHeader64)
	if !ok {
		return fmt.Errorf("not a PE32+ (64-bit) executable - UEFI x86_64 requires 64-bit PE")
	}

	// Report current subsystem
	subsystemName := subsystemToString(optHeader.Subsystem)
	fmt.Printf("Input: %s\n", path)
	fmt.Printf("  Architecture: PE32+ (x86_64)\n")
	fmt.Printf("  Current subsystem: %d (%s)\n", optHeader.Subsystem, subsystemName)

	// Warn if not the expected Windows Console subsystem
	if optHeader.Subsystem != IMAGE_SUBSYSTEM_WINDOWS_CUI {
		fmt.Printf("  Warning: Expected subsystem %d (Windows Console), got %d\n",
			IMAGE_SUBSYSTEM_WINDOWS_CUI, optHeader.Subsystem)
	}

	// Check for imports - UEFI applications must not have Windows DLL imports
	if optHeader.NumberOfRvaAndSizes > 1 {
		importDir := optHeader.DataDirectory[1] // Import Directory
		if importDir.VirtualAddress != 0 && importDir.Size != 0 {
			imports, err := peFile.ImportedSymbols()
			if err == nil && len(imports) > 0 {
				return fmt.Errorf("PE has Windows imports which are incompatible with UEFI:\n"+
					"  %v\n"+
					"  Build with CGO_ENABLED=0 to eliminate imports", imports)
			}
		}
	}
	fmt.Printf("  Imports: none (good for UEFI)\n")

	return nil
}

func patchSubsystem(data []byte) error {
	// DOS Header: e_lfanew at offset 0x3C points to PE signature
	if len(data) < 64 {
		return fmt.Errorf("file too small for DOS header")
	}

	peOffset := int(binary.LittleEndian.Uint32(data[0x3C:0x40]))

	// Verify PE signature "PE\0\0"
	if peOffset+4 > len(data) {
		return fmt.Errorf("invalid PE offset: %d", peOffset)
	}
	if string(data[peOffset:peOffset+4]) != "PE\x00\x00" {
		return fmt.Errorf("invalid PE signature at offset %d", peOffset)
	}

	// COFF Header is immediately after PE signature (20 bytes)
	// Optional Header follows COFF Header
	optHeaderOffset := peOffset + 4 + 20

	// For PE32+, the Subsystem field is at offset 68 in the Optional Header
	subsystemOffset := optHeaderOffset + PE32PlusSubsystemOffset

	if subsystemOffset+2 > len(data) {
		return fmt.Errorf("file too small for optional header subsystem field")
	}

	// Read and display current value
	currentSubsystem := binary.LittleEndian.Uint16(data[subsystemOffset : subsystemOffset+2])

	// Patch to EFI Application
	binary.LittleEndian.PutUint16(data[subsystemOffset:subsystemOffset+2], IMAGE_SUBSYSTEM_EFI_APPLICATION)

	fmt.Printf("Patched subsystem: %d (%s) -> %d (%s)\n",
		currentSubsystem, subsystemToString(currentSubsystem),
		IMAGE_SUBSYSTEM_EFI_APPLICATION, subsystemToString(IMAGE_SUBSYSTEM_EFI_APPLICATION))

	return nil
}

func subsystemToString(subsystem uint16) string {
	switch subsystem {
	case IMAGE_SUBSYSTEM_UNKNOWN:
		return "Unknown"
	case IMAGE_SUBSYSTEM_NATIVE:
		return "Native"
	case IMAGE_SUBSYSTEM_WINDOWS_GUI:
		return "Windows GUI"
	case IMAGE_SUBSYSTEM_WINDOWS_CUI:
		return "Windows Console"
	case IMAGE_SUBSYSTEM_EFI_APPLICATION:
		return "EFI Application"
	case IMAGE_SUBSYSTEM_EFI_BOOT_DRIVER:
		return "EFI Boot Driver"
	case IMAGE_SUBSYSTEM_EFI_RUNTIME:
		return "EFI Runtime Driver"
	case IMAGE_SUBSYSTEM_EFI_ROM:
		return "EFI ROM"
	default:
		return "Other"
	}
}
