package mancini

// KeyMapperInjector is the interface used for cross-module injection
// between rachel and keymapper.maz. Rachel creates a KeyMapperInit,
// passes it to keymapper.maz's MazarinShepherd. Keymapper calls
// RegisterKeyMapper with the constructed mapper. Rachel reads it
// back from the struct's Mapper field.
//
// This uses the same callback-registration pattern as FontSvcInjector
// to ensure the linker keeps interface methods alive in both binaries.
type KeyMapperInjector interface {
	GetKeymapName() string
	RegisterKeyMapper(km KeyMapper)
}

// KeyMapperInit implements KeyMapperInjector. Rachel fills in KeymapName,
// passes it to keymapper.maz, and reads back the Mapper field.
type KeyMapperInit struct {
	KeymapName string
	Mapper     KeyMapper
}

// GetKeymapName implements KeyMapperInjector.
func (k *KeyMapperInit) GetKeymapName() string { return k.KeymapName }

// RegisterKeyMapper implements KeyMapperInjector.
func (k *KeyMapperInit) RegisterKeyMapper(km KeyMapper) { k.Mapper = km }
