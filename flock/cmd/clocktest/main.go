// clocktest demonstrates the Reactor-based constraint notification pipeline.
// It discovers attr:///kernel/int64/time/utc_seconds via a deref constraint
// and uses a Reactor to print HH:MM:SS whenever the second changes.
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
			vm.InstConstStr(0),                         // push URI
			vm.InstCallBuiltin(vm.BuiltinDerefI64, 1), // deref → I64
			vm.InstRet(1),                              // return
		},
		Strings: []string{
			"attr:///kernel/int64/time/utc_seconds",
		},
	}

	timeSec := attr.ConstraintI64(attr.ShepherdURI("int64", "time_sec"), prog)

	// Force initial evaluation to wire dependency edges.
	sec := timeSec.Get()
	h := (sec / 3600) % 24
	m := (sec / 60) % 60
	s := sec % 60
	fmt.Printf("clocktest: initial %02d:%02d:%02d (epoch=%d)\n", h, m, s, sec)

	// Create a Reactor and watch the time constraint.
	// WatchI64 automatically marks it eager.
	reactor := attr.NewReactor()
	reactor.WatchI64(timeSec, func(old, new int64) {
		h := (new / 3600) % 24
		m := (new / 60) % 60
		s := new % 60
		fmt.Printf("clocktest: %02d:%02d:%02d (epoch=%d)\n", h, m, s, new)
	})

	fmt.Println("clocktest: entering Reactor loop...")
	reactor.Run()
}
