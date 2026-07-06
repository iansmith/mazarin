// Package embedcheck audits built kernel images for architecture-consistent
// embedded payloads. The kernel embeds the fs shepherd ELF (launchEmbeddedFS)
// and an arch-specific kernel.toml; both are staged by the build system, and a
// wrong-arch staging bug ships a kernel that panics at fs launch (MAZ-153) or
// silently runs with the other architecture's configuration.
package embedcheck

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
)

// ELF e_machine values for the two supported architectures.
const (
	MachineX8664   uint16 = 62
	MachineAARCH64 uint16 = 183
)

// Config markers: the first line of each arch's kernel config. The build-time
// audit and these checks match on the marker line, so the header comments in
// config/kernel.{arm64,amd64}.toml must keep these prefixes.
const (
	ConfigMarkerARM64 = "# kernel.arm64.toml — ARM64 kernel configuration"
	ConfigMarkerAMD64 = "# kernel.amd64.toml — x86_64 kernel configuration"
)

var elfMagic = []byte{0x7f, 'E', 'L', 'F'}

// MachineName renders an e_machine value for error messages.
func MachineName(m uint16) string {
	switch m {
	case MachineX8664:
		return "x86-64"
	case MachineAARCH64:
		return "aarch64"
	default:
		return fmt.Sprintf("unknown(%d)", m)
	}
}

// outerMachine parses the ELF header at the start of data and returns its
// e_machine. Errors if data is not a 64-bit little-endian ELF image.
func outerMachine(data []byte) (uint16, error) {
	if len(data) < 64 || !bytes.Equal(data[:4], elfMagic) {
		return 0, fmt.Errorf("embedcheck: not an ELF image")
	}
	if data[4] != 2 || data[5] != 1 {
		return 0, fmt.Errorf("embedcheck: not a 64-bit little-endian ELF image")
	}
	return binary.LittleEndian.Uint16(data[18:20]), nil
}

// plausibleEmbeddedELF reports whether data[off:] looks like a real ELF64
// header rather than a stray magic constant in code or rodata. The extra
// field checks (pad bytes zero, sane e_type/e_version/e_ehsize) make an
// accidental match in arbitrary binary data astronomically unlikely.
func plausibleEmbeddedELF(data []byte, off int) (machine uint16, ok bool) {
	if off+64 > len(data) {
		return 0, false
	}
	h := data[off : off+64]
	if h[4] != 2 || h[5] != 1 || h[6] != 1 {
		return 0, false // not ELFCLASS64 / little-endian / EV_CURRENT
	}
	for _, b := range h[9:16] {
		if b != 0 {
			return 0, false // EI_PAD must be zero
		}
	}
	etype := binary.LittleEndian.Uint16(h[16:18])
	if etype != 2 && etype != 3 {
		return 0, false // not ET_EXEC / ET_DYN
	}
	if binary.LittleEndian.Uint32(h[20:24]) != 1 {
		return 0, false // e_version
	}
	if binary.LittleEndian.Uint16(h[52:54]) != 64 {
		return 0, false // e_ehsize
	}
	return binary.LittleEndian.Uint16(h[18:20]), true
}

// CheckKernelELFBytes verifies that every embedded ELF payload inside the
// kernel image matches the kernel's own architecture, and that at least one
// embedded ELF with a known machine is present (the fs shepherd must be
// embedded — zero findings means the scan is broken, not that the image is
// clean).
func CheckKernelELFBytes(data []byte) error {
	outer, err := outerMachine(data)
	if err != nil {
		return err
	}
	found := 0
	for off := bytes.Index(data[1:], elfMagic) + 1; off > 0; {
		if m, ok := plausibleEmbeddedELF(data, off); ok && (m == MachineX8664 || m == MachineAARCH64) {
			found++
			if m != outer {
				return fmt.Errorf("embedcheck: %s kernel embeds a %s ELF at offset %#x",
					MachineName(outer), MachineName(m), off)
			}
		}
		next := bytes.Index(data[off+1:], elfMagic)
		if next < 0 {
			break
		}
		off += 1 + next
	}
	if found == 0 {
		return fmt.Errorf("embedcheck: no embedded ELF payload found in %s kernel", MachineName(outer))
	}
	return nil
}

// CheckKernelConfigBytes verifies the embedded kernel.toml matches the
// kernel's architecture: the matching arch's marker line must be present and
// the other arch's must be absent.
func CheckKernelConfigBytes(data []byte) error {
	outer, err := outerMachine(data)
	if err != nil {
		return err
	}
	var want, reject string
	switch outer {
	case MachineAARCH64:
		want, reject = ConfigMarkerARM64, ConfigMarkerAMD64
	case MachineX8664:
		want, reject = ConfigMarkerAMD64, ConfigMarkerARM64
	default:
		return fmt.Errorf("embedcheck: unsupported kernel machine %s", MachineName(outer))
	}
	hasWant := bytes.Contains(data, []byte(want))
	hasReject := bytes.Contains(data, []byte(reject))
	switch {
	case hasReject:
		return fmt.Errorf("embedcheck: %s kernel embeds the other architecture's kernel config (found %q)",
			MachineName(outer), reject)
	case !hasWant:
		return fmt.Errorf("embedcheck: %s kernel is missing its kernel config marker %q",
			MachineName(outer), want)
	}
	return nil
}

// CheckKernelELF is CheckKernelELFBytes reading from a file path.
func CheckKernelELF(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return CheckKernelELFBytes(data)
}

// CheckKernelConfig is CheckKernelConfigBytes reading from a file path.
func CheckKernelConfig(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return CheckKernelConfigBytes(data)
}
