package constants

// StartupShepherd describes one shepherd to launch at boot.
type StartupShepherd struct {
	Name string `toml:"name"`
	Path string `toml:"path"`
}

// StartupConfig holds the shepherd launch sequence parsed from startup.toml.
// Read by the fs shepherd from the FAT32 disk at boot.
type StartupConfig struct {
	Shepherds []StartupShepherd `toml:"shepherd"`
}
