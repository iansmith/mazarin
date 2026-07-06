package embedcheck

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
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

// --- adversary gap tests (Phase 0 Step 0f) ---
// These pin the checker apparatus itself: rejection paths, scan boundaries,
// and directional symmetry the initial suite left unexercised.

func TestOuterImageTooShortFails(t *testing.T) {
	for _, data := range [][]byte{nil, {}, make([]byte, 63)} {
		if err := CheckKernelELFBytes(data); err == nil {
			t.Errorf("%d-byte image passed the ELF check", len(data))
		}
		if err := CheckKernelConfigBytes(data); err == nil {
			t.Errorf("%d-byte image passed the config check", len(data))
		}
	}
}

func TestOuterNotELFFails(t *testing.T) {
	img := bytes.Repeat([]byte{0x42}, 256)
	if err := CheckKernelELFBytes(img); err == nil {
		t.Error("non-ELF image passed the ELF check")
	}
	if err := CheckKernelConfigBytes(img); err == nil {
		t.Error("non-ELF image passed the config check")
	}
}

func TestOuter32BitOrBigEndianFails(t *testing.T) {
	h32 := elfHeader(MachineX8664)
	h32[4] = 1 // ELFCLASS32
	if err := CheckKernelELFBytes(append(h32, make([]byte, 128)...)); err == nil {
		t.Error("32-bit outer ELF passed the check")
	}
	hbe := elfHeader(MachineX8664)
	hbe[5] = 2 // big-endian
	if err := CheckKernelELFBytes(append(hbe, make([]byte, 128)...)); err == nil {
		t.Error("big-endian outer ELF passed the check")
	}
}

func TestUnsupportedOuterMachineConfigFails(t *testing.T) {
	img := append(syntheticKernel(0x28 /* ARM32 */), []byte(ConfigMarkerARM64)...)
	err := CheckKernelConfigBytes(img)
	if err == nil || !strings.Contains(err.Error(), "unsupported kernel machine") {
		t.Fatalf("ARM32 outer machine not rejected as unsupported: %v", err)
	}
}

func TestSecondOfMultipleEmbeddedELFsMismatches(t *testing.T) {
	img := syntheticKernel(MachineX8664, elfHeader(MachineX8664), elfHeader(MachineAARCH64))
	if err := CheckKernelELFBytes(img); err == nil {
		t.Fatal("mismatched embed after a matching one passed — scan stops too early")
	}
}

func TestBothConfigMarkersPresentFails(t *testing.T) {
	img := append(syntheticKernel(MachineAARCH64, elfHeader(MachineAARCH64)),
		[]byte(ConfigMarkerARM64+ConfigMarkerAMD64)...)
	err := CheckKernelConfigBytes(img)
	if err == nil || !strings.Contains(err.Error(), "other architecture") {
		t.Fatalf("image with both config markers not rejected on the reject branch: %v", err)
	}
}

func TestEmbeddedELFAtImageEndDetected(t *testing.T) {
	// The embedded header's last byte is the final byte of the image
	// (off+64 == len(data) exactly — the boundary of the plausibility guard).
	img := append(elfHeader(MachineX8664), make([]byte, 32)...)
	img = append(img, elfHeader(MachineX8664)...)
	if err := CheckKernelELFBytes(img); err != nil {
		t.Fatalf("embed ending exactly at image end missed: %v", err)
	}
}

func TestTruncatedEmbeddedMagicNearEndSkipped(t *testing.T) {
	img := append(elfHeader(MachineX8664), make([]byte, 32)...)
	img = append(img, elfMagic...)
	img = append(img, make([]byte, 10)...) // no room for a full 64-byte header
	err := CheckKernelELFBytes(img)
	if err == nil || !strings.Contains(err.Error(), "no embedded ELF") {
		t.Fatalf("truncated trailing magic mishandled: %v", err)
	}
}

func TestNearMissEmbeddedHeadersRejected(t *testing.T) {
	// Each mutation violates exactly one plausibility check. The near-miss
	// header is aarch64 inside an x86-64 outer: if it were (wrongly) counted,
	// the error would be a mismatch, not "no embedded ELF".
	mutations := []struct {
		name   string
		mutate func([]byte)
	}{
		{"nonzero EI_PAD", func(h []byte) { h[9] = 1 }},
		{"bad e_type", func(h []byte) { binary.LittleEndian.PutUint16(h[16:18], 5) }},
		{"bad e_version", func(h []byte) { binary.LittleEndian.PutUint32(h[20:24], 2) }},
		{"bad e_ehsize", func(h []byte) { binary.LittleEndian.PutUint16(h[52:54], 52) }},
	}
	for _, m := range mutations {
		t.Run(m.name, func(t *testing.T) {
			h := elfHeader(MachineAARCH64)
			m.mutate(h)
			err := CheckKernelELFBytes(syntheticKernel(MachineX8664, h))
			if err == nil || !strings.Contains(err.Error(), "no embedded ELF") {
				t.Fatalf("near-miss header (%s) was counted as an embed: %v", m.name, err)
			}
		})
	}
}

func TestStrayMagicOnlyStillReportsNoEmbed(t *testing.T) {
	stray := append(append([]byte{}, elfMagic...), 0xff, 0xff, 0xff, 0xff)
	err := CheckKernelELFBytes(syntheticKernel(MachineX8664, stray))
	if err == nil || !strings.Contains(err.Error(), "no embedded ELF") {
		t.Fatalf("stray-magic-only image mishandled: %v", err)
	}
}

func TestExactly64ByteImageNoEmbedFails(t *testing.T) {
	err := CheckKernelELFBytes(elfHeader(MachineX8664))
	if err == nil || !strings.Contains(err.Error(), "no embedded ELF") {
		t.Fatalf("bare 64-byte outer header mishandled: %v", err)
	}
}

func TestFilePathWrappers(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "kernel.elf")
	img := append(syntheticKernel(MachineX8664, elfHeader(MachineX8664)), []byte(ConfigMarkerAMD64)...)
	if err := os.WriteFile(good, img, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CheckKernelELF(good); err != nil {
		t.Errorf("file wrapper rejected a good image: %v", err)
	}
	if err := CheckKernelConfig(good); err != nil {
		t.Errorf("config file wrapper rejected a good image: %v", err)
	}
	missing := filepath.Join(dir, "missing.elf")
	if err := CheckKernelELF(missing); err == nil {
		t.Error("missing file passed the ELF check")
	}
	if err := CheckKernelConfig(missing); err == nil {
		t.Error("missing file passed the config check")
	}
}

func TestConfigCheckIndependentOfELFPayload(t *testing.T) {
	// Config checking must not depend on a successful embedded-ELF scan.
	img := append(elfHeader(MachineAARCH64), []byte(ConfigMarkerARM64)...)
	if err := CheckKernelConfigBytes(img); err != nil {
		t.Fatalf("config check failed without an ELF payload present: %v", err)
	}
}

func TestPartialConfigMarkerTreatedAsMissing(t *testing.T) {
	img := append(elfHeader(MachineAARCH64), []byte("# kernel.arm64.toml")...)
	err := CheckKernelConfigBytes(img)
	if err == nil || !strings.Contains(err.Error(), "missing its kernel config marker") {
		t.Fatalf("truncated marker not treated as missing: %v", err)
	}
}

func TestConfigMarkerMissingFailsBothDirections(t *testing.T) {
	for _, outer := range []uint16{MachineX8664, MachineAARCH64} {
		t.Run(MachineName(outer), func(t *testing.T) {
			err := CheckKernelConfigBytes(elfHeader(outer))
			if err == nil || !strings.Contains(err.Error(), "missing its kernel config marker") {
				t.Fatalf("%s kernel with no marker mishandled: %v", MachineName(outer), err)
			}
		})
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
