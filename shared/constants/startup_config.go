package constants

// StartupShepherd describes one shepherd to launch at boot.
type StartupShepherd struct {
	Name       string `toml:"name"`
	Path       string `toml:"path"`
	GCPercent  int    `toml:"gc_percent"`  // 0 = use system default
	MemLimitMB int    `toml:"memlimit_mb"` // 0 = use system default
}

// StartupConfig holds the shepherd launch sequence parsed from startup.toml.
// Read by the fs shepherd from the FAT32 disk at boot.
type StartupConfig struct {
	Shepherds []StartupShepherd `toml:"shepherd"`
}
