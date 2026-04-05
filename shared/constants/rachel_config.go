package constants

// RachelConfig holds rachel's configuration parsed from rachel.toml.
// Read by rachel from the ext2 disk at startup via fs IPC.
type RachelConfig struct {
	// Keymap is the keyboard layout name (e.g. "us", "dvorak-qwerty").
	// Case-insensitive. Defaults to "us" if empty.
	Keymap string `toml:"keymap"`

	// DefaultTextFontSize is the default font size (in pixels) for text
	// interactors. Published as an attribute so shepherds can bind to it.
	// Defaults to 24 if zero.
	DefaultTextFontSize int64 `toml:"defaultTextFontSize"`
}
