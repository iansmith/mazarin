// handle_program.go — Bytecode loading from ELF sections.
//
// MustGetProgram reads a named program from the .constraint ELF section
// of the current binary. For Phase 5 development, programs can also be
// compiled inline using compile.Compile.

package attr

import (
	"mazzy/mazarin/vm"
)

// MustGetProgram loads a named compiled constraint program from the current
// binary's .constraint ELF section. Panics if the program is not found.
//
// For Phase 5 development, this is a stub that panics — use inline
// compile.Compile or hand-assembled vm.Program instances instead.
// Full ELF section reading (via debug/elf on /proc/self/exe or equivalent)
// will be implemented when the constraint compiler is integrated.
func MustGetProgram(name string) *vm.Program {
	panic("attr.MustGetProgram: not yet implemented (use inline vm.Program for Phase 5) — requested: " + name)
}
