package embedcheck

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// elfHeader builds a minimal plausible ELF64 header for synthetic images.
func elfHeader(machine uint16) []byte {
	h := make([]byte, 64)
	copy(h, elfMagic)
	h[4] = 2 // ELFCLASS64
	h[5] = 1 // little-endian
	h[6] = 1 // EI_VERSION
	binary.LittleEndian.PutUint16(h[16:18], 2) // ET_EXEC
	binary.LittleEndian.PutUint16(h[18:20], machine)
	binary.LittleEndian.PutUint32(h[20:24], 1)  // e_version
	binary.LittleEndian.PutUint16(h[52:54], 64) // e_ehsize
	return h
}

// syntheticKernel assembles an outer ELF image with the given payload blobs
// separated by zero filler.
func syntheticKernel(outer uint16, payloads ...[]byte) []byte {
	img := elfHeader(outer)
	img = append(img, make([]byte, 128)...)
	for _, p := range payloads {
		img = append(img, p...)
		img = append(img, make([]byte, 32)...)
	}
	return img
}

func TestMatchingEmbeddedELFPasses(t *testing.T) {
	img := syntheticKernel(MachineX8664, elfHeader(MachineX8664))
	if err := CheckKernelELFBytes(img); err != nil {
		t.Fatalf("matching embed rejected: %v", err)
	}
}

func TestMismatchedEmbeddedELFFails(t *testing.T) {
	img := syntheticKernel(MachineX8664, elfHeader(MachineAARCH64))
	if err := CheckKernelELFBytes(img); err == nil {
		t.Fatal("x86-64 kernel embedding an aarch64 ELF passed the check")
	}
	img = syntheticKernel(MachineAARCH64, elfHeader(MachineX8664))
	if err := CheckKernelELFBytes(img); err == nil {
		t.Fatal("aarch64 kernel embedding an x86-64 ELF passed the check")
	}
}

func TestNoEmbeddedELFFails(t *testing.T) {
	img := syntheticKernel(MachineX8664)
	if err := CheckKernelELFBytes(img); err == nil {
		t.Fatal("kernel with no embedded ELF passed the check — scan is vacuous")
	}
}

func TestStrayMagicConstantIgnored(t *testing.T) {
	// A bare "\x7fELF" literal (as found in loader code constants) followed by
	// non-header bytes must not count as an embedded ELF — but it must not
	// hide a real mismatched embed either.
	stray := append([]byte{}, elfMagic...)
	stray = append(stray, []byte{0xff, 0xff, 0xff, 0xff}...)
	img := syntheticKernel(MachineX8664, stray, elfHeader(MachineX8664))
	if err := CheckKernelELFBytes(img); err != nil {
		t.Fatalf("stray magic constant broke the scan: %v", err)
	}
	img = syntheticKernel(MachineX8664, stray, elfHeader(MachineAARCH64))
	if err := CheckKernelELFBytes(img); err == nil {
		t.Fatal("mismatched embed hidden behind a stray magic constant passed")
	}
}

func TestConfigMarkerMatchPasses(t *testing.T) {
	img := append(syntheticKernel(MachineAARCH64, elfHeader(MachineAARCH64)), []byte(ConfigMarkerARM64)...)
	if err := CheckKernelConfigBytes(img); err != nil {
		t.Fatalf("matching config marker rejected: %v", err)
	}
}

func TestConfigMarkerMismatchFails(t *testing.T) {
	img := append(syntheticKernel(MachineAARCH64, elfHeader(MachineAARCH64)), []byte(ConfigMarkerAMD64)...)
	if err := CheckKernelConfigBytes(img); err == nil {
		t.Fatal("aarch64 kernel carrying the amd64 config marker passed the check")
	}
}

func TestConfigMarkerMissingFails(t *testing.T) {
	img := syntheticKernel(MachineX8664, elfHeader(MachineX8664))
	if err := CheckKernelConfigBytes(img); err == nil {
		t.Fatal("kernel with no config marker passed the check")
	}
}

// TestBuiltKernelArtifacts audits the kernels actually produced by the build
// system, when present. This is the MAZ-153 regression tripwire: a kernel
// whose embedded fs shepherd or kernel.toml came from the other architecture
// (the `task all` shared-staging race) fails here.
func TestBuiltKernelArtifacts(t *testing.T) {
	kernels := []string{
		filepath.Join("..", "..", "build", "kmazarin.elf"),
		filepath.Join("..", "..", "build", "kmazarin-amd64.elf"),
	}
	for _, path := range kernels {
		t.Run(filepath.Base(path), func(t *testing.T) {
			if _, err := os.Stat(path); os.IsNotExist(err) {
				t.Skipf("%s not built — run the task build first", path)
			}
			if err := CheckKernelELF(path); err != nil {
				t.Errorf("embedded fs arch check failed: %v", err)
			}
			if err := CheckKernelConfig(path); err != nil {
				t.Errorf("embedded kernel config check failed: %v", err)
			}
		})
	}
}
