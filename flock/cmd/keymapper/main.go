// keymapper.maz provides keyboard layout translation. Rachel loads this
// .maz at startup and receives a KeyMapper for the layout specified in
// the TOML config. Supported layouts: "identity", "us", "dvorak-qwerty".
package main

import (
	"fmt"
	"mazzy/mazarin/mancini"
	"strings"
)

// MazEntryPoint holds a reference to MazarinMain to prevent DCE.
var MazEntryPoint func() = MazarinMain

// MazarinShepherdAddr holds a reference to MazarinShepherd to prevent DCE.
var MazarinShepherdAddr func(interface{}) error = MazarinShepherd

func init() {
	if MazEntryPoint == nil {
		panic("unreachable")
	}
	if MazarinShepherdAddr == nil {
		panic("unreachable")
	}
}

// MazarinShepherd receives a KeyMapperInjector from rachel. It reads the
// keymap name, constructs the appropriate KeyMapper, and passes it back
// via SetKeyMapper.
//
//go:noinline
func MazarinShepherd(arg interface{}) error {
	inj, ok := arg.(mancini.KeyMapperInjector)
	if !ok {
		return fmt.Errorf("keymapper: expected KeyMapperInjector, got %T", arg)
	}
	name := inj.GetKeymapName()
	km := NewKeyMapper(name)
	inj.RegisterKeyMapper(km)
	return nil
}

// MazarinMain is the .maz entry point. For keymapper this is a no-op —
// the KeyMapper is a passive object, not a long-running service.
//
//go:noinline
func MazarinMain() {
	// Nothing to do — keymapper is not an event-loop service.
}

// NewKeyMapper creates a KeyMapper for the given layout name.
// Names are case-insensitive. Unknown names fall back to identity.
func NewKeyMapper(name string) mancini.KeyMapper {
	switch strings.ToLower(name) {
	case "us":
		return &usKeyMapper{}
	case "dvorak-qwerty":
		return &dvorakQwertyKeyMapper{}
	default:
		return &identityKeyMapper{}
	}
}

// forceKeyMapperMethods ensures the linker includes the KeyMapper
// interface method implementations for usKeyMapper, dvorakQwertyKeyMapper,
// and identityKeyMapper. Without this, the linker may set Ifn=-1
// causing "unreachable method called" panics.
//
//go:noinline
func forceKeyMapperMethods(km mancini.KeyMapper) {
	km.Name()
	km.Map(0, false, 0)
}

func main() {
	// Force the linker to include method implementations.
	forceKeyMapperMethods(&usKeyMapper{})
	forceKeyMapperMethods(&dvorakQwertyKeyMapper{})
	forceKeyMapperMethods(&identityKeyMapper{})
}
