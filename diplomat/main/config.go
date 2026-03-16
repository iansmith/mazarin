// diplomat/main/config.go - TOML configuration parser for kmazarin boot config
//
// Reads /EFI/Linux/kmazarin.toml from FAT32 filesystem.
// Hand-written minimal TOML parser - no imports beyond unsafe and fat32.

package main

import (
	"mazzy/shared/fs/fat32"
)

// KmazarinConfig holds boot configuration from kmazarin.toml.
type KmazarinConfig struct {
	MinCPUs  uint64 // Minimum CPU count (abort if fewer)
	MaxCPUs  uint64 // Maximum CPU count (cap if more)
	MinRAMMB uint64 // Minimum RAM in MB (abort if less)
	MaxRAMMB uint64 // Maximum RAM in MB (cap if more)

	// Memory layout overrides (Stage 3 - escape hatches for future growth)
	FramebufferMB  uint64 // VirtIO GPU framebuffer size in MB (default 32)
	PageTableMB    uint64 // Page table pool size in MB (default 8)
	KernelBudgetMB   uint64 // Kernel memory budget warning threshold in MB (default 128)
	KernelMemLimitMB uint64 // GOMEMLIMIT for the kernel in MB (default 24)
	GCPercentKernel  uint64 // GOGC for the kernel (0 = Go default 100)
}

// defaultConfig returns the default configuration.
func defaultConfig() *KmazarinConfig {
	cfg := dNew[KmazarinConfig]()
	if cfg == nil {
		return nil
	}
	cfg.MinCPUs = 1
	cfg.MaxCPUs = 256
	cfg.MinRAMMB = 256
	cfg.MaxRAMMB = 65536
	cfg.FramebufferMB = 32  // 32MB — 1920×1080×4 = 8MB, room to grow
	cfg.PageTableMB = 8     // 8MB page table pool
	cfg.KernelBudgetMB = 32   // 32MB kernel memory warning threshold
	cfg.KernelMemLimitMB = 24 // 24MB GOMEMLIMIT for the kernel
	return cfg
}

// configFileBuf is a global buffer for reading kmazarin.toml (avoid allocation)
var configFileBuf [2048]byte

// ReadConfig reads and parses /EFI/Linux/kmazarin.toml from the filesystem.
// Returns default config if the file is not found.
func ReadConfig(fs *fat32.FileSystem) (*KmazarinConfig, error) {
	cfg := defaultConfig()
	if cfg == nil {
		return nil, &errConfigAllocFailed
	}

	// Try to find the config file
	file, err := findConfigFile(fs)
	if err != nil || file == nil {
		// Config file not found - use defaults
		printString("Config: using defaults (no kmazarin.toml)\r\n")
		return cfg, nil
	}

	// Read the file content
	n, err := readFileAt(fs, file, 0, configFileBuf[:])
	if err != nil {
		printString("Config: read error, using defaults\r\n")
		return cfg, nil
	}

	// Parse TOML key=value pairs
	parseToml(configFileBuf[:n], cfg)

	return cfg, nil
}

// findConfigFile looks for /EFI/Linux/kmazarin.toml
func findConfigFile(fs *fat32.FileSystem) (*SimpleFile, error) {
	cluster := fs.RootCluster()

	// Find EFI directory
	entry, err := findInDir(fs, cluster, "EFI")
	if err != nil {
		return nil, err
	}
	if !entry.IsDir {
		return nil, nil
	}
	cluster = entry.Cluster

	// Find Linux directory
	entry, err = findInDir(fs, cluster, "LINUX")
	if err != nil {
		return nil, err
	}
	if !entry.IsDir {
		return nil, nil
	}
	cluster = entry.Cluster

	// Find kmazarin.toml (stored as KMAZARIN.TOM in FAT32 8.3 format)
	entry, err = findInDir(fs, cluster, "KMAZARIN.TOM")
	if err != nil {
		return nil, nil // Not found is OK
	}

	sf := dNew[SimpleFile]()
	if sf == nil {
		return nil, nil
	}
	sf.Cluster = entry.Cluster
	sf.Size = entry.Size
	return sf, nil
}

// parseToml parses minimal TOML key=value pairs from the buffer.
// Supports: key = value (integer values only)
// Comments start with #
func parseToml(data []byte, cfg *KmazarinConfig) {
	i := 0
	for i < len(data) {
		// Skip whitespace and newlines
		for i < len(data) && (data[i] == ' ' || data[i] == '\t' || data[i] == '\r' || data[i] == '\n') {
			i++
		}
		if i >= len(data) {
			break
		}

		// Skip comment lines
		if data[i] == '#' {
			for i < len(data) && data[i] != '\n' {
				i++
			}
			continue
		}

		// Parse key
		keyStart := i
		for i < len(data) && data[i] != '=' && data[i] != ' ' && data[i] != '\t' && data[i] != '\n' {
			i++
		}
		keyEnd := i

		// Skip whitespace and '='
		for i < len(data) && (data[i] == ' ' || data[i] == '\t') {
			i++
		}
		if i >= len(data) || data[i] != '=' {
			// Skip to next line
			for i < len(data) && data[i] != '\n' {
				i++
			}
			continue
		}
		i++ // skip '='

		// Skip whitespace after '='
		for i < len(data) && (data[i] == ' ' || data[i] == '\t') {
			i++
		}

		// Parse integer value
		val := uint64(0)
		for i < len(data) && data[i] >= '0' && data[i] <= '9' {
			val = val*10 + uint64(data[i]-'0')
			i++
		}

		// Skip to end of line
		for i < len(data) && data[i] != '\n' {
			i++
		}

		// Match key to config field
		key := data[keyStart:keyEnd]
		if matchBytes(key, "min_cpus") {
			cfg.MinCPUs = val
		} else if matchBytes(key, "max_cpus") {
			cfg.MaxCPUs = val
		} else if matchBytes(key, "min_ram_mb") {
			cfg.MinRAMMB = val
		} else if matchBytes(key, "max_ram_mb") {
			cfg.MaxRAMMB = val
		} else if matchBytes(key, "framebuffer_mb") {
			cfg.FramebufferMB = val
		} else if matchBytes(key, "page_table_mb") {
			cfg.PageTableMB = val
		} else if matchBytes(key, "kernel_budget_mb") {
			cfg.KernelBudgetMB = val
		} else if matchBytes(key, "kernel_mem_limit") {
			cfg.KernelMemLimitMB = val
		} else if matchBytes(key, "gc_percent_kernel") {
			cfg.GCPercentKernel = val
		}
	}
}

// matchBytes compares a byte slice with a string (case-sensitive)
func matchBytes(a []byte, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ReadConfigDefaults returns default config without reading the config file.
// Used when the Go heap is not initialized (diplomat doesn't call schedinit/mallocinit)
// so any code path that does type assertions or interface conversions will crash.
func ReadConfigDefaults(fs *fat32.FileSystem) (*KmazarinConfig, error) {
	printString("Config: using defaults\r\n")
	cfg := defaultConfig()
	if cfg == nil {
		printString("ERROR: Failed to allocate config\r\n")
		for {}
	}
	return cfg, nil
}

var errConfigAllocFailed = blockDevError{"config: allocation failed"}
