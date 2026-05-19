package mazhost

// pinnedEntry stores plugin entry-point function pointers passed in from
// each .maz plugin's init(). Writing the addresses through here defeats
// link-time DCE of MazarinMain/MazarinShepherd in plugin builds: without
// some reference path from init() to those symbols, the linker drops
// them and h.Sym lookup fails at load time.
//
// Plugins all link against the shepherd's resident copy of mazhost
// through GOT, so successive plugin loads overwrite the same slot —
// that's fine; the writes here are only a DCE anchor for the plugin's
// own linker pass, not durable state anyone reads.
var pinnedEntry struct {
	main     func()
	shepherd func(any) error
}

// PinEntry keeps MazarinMain (and optionally MazarinShepherd) alive in a
// .maz plugin's binary so the host's h.Sym("MazarinMain") /
// h.Sym("MazarinShepherd") lookups succeed after dlopen. Plugins must
// call this from init():
//
//	func init() { mazhost.PinEntry(MazarinMain, MazarinShepherd) }
//
// Plugins without a shepherd hook pass nil:
//
//	func init() { mazhost.PinEntry(MazarinMain, nil) }
//
// Panics if MazarinMain is nil — a guaranteed program bug, since the
// function is named at the call site.
func PinEntry(main func(), shepherd func(any) error) {
	if main == nil {
		panic("mazhost.PinEntry: MazarinMain is nil")
	}
	pinnedEntry.main = main
	pinnedEntry.shepherd = shepherd
}
