// clocktest demonstrates the full constraint pipeline with kernel-published
// attributes. It discovers attr:///kernel/int64/time/utc_seconds via a deref
// constraint, marks it eager, and enters a WaitDirty loop that prints HH:MM:SS
// once per second.
package main

import (
	"fmt"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/vm"
)

func main() {
	attr.Init()

	fmt.Println("clocktest: starting...")

	// Create an identity constraint that derefs the kernel time attribute.
	// No explicit deps — the deref discovers the kernel attr dynamically,
	// and AttrUpdateDeps wires the dependency edge on first evaluation.
	prog := &vm.Program{
		Code: []vm.Inst{
			vm.InstConstStr(0),                            // push URI
			vm.InstCallBuiltin(vm.BuiltinDerefI64, 1),    // deref → I64
			vm.InstRet(1),                                 // return
		},
		Strings: []string{
			"attr:///kernel/int64/time/utc_seconds",
		},
	}

	timeSec := attr.ConstraintI64("attr:///priest/clocktest/time_sec", prog)

	// Force initial evaluation to wire dependency edges.
	sec := timeSec.Get()
	h := (sec / 3600) % 24
	m := (sec / 60) % 60
	s := sec % 60
	fmt.Printf("clocktest: initial %02d:%02d:%02d (epoch=%d)\n", h, m, s, sec)

	// Enable eager notification so WaitDirty wakes on time changes.
	timeSec.SetEager(true)

	fmt.Println("clocktest: entering WaitDirty loop...")
	for {
		slots := attr.WaitDirty()
		if slots == nil {
			continue // overflow, re-scan
		}
		sec = timeSec.Get()
		h = (sec / 3600) % 24
		m = (sec / 60) % 60
		s = sec % 60
		fmt.Printf("clocktest: %02d:%02d:%02d (epoch=%d)\n", h, m, s, sec)
	}
}
