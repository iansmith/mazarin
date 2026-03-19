// handletest exercises the Handle[T] API (mazarin/attr) through actual kernel
// syscalls. Unlike attrtest (which uses the in-process Attribute[T] API), this
// shepherd creates attributes via SysAttrCreate, writes via SysAttrWrite, and
// reads values via seqlock-protected shared page reads.
package main

import (
	"fmt"
	"sync"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/vm"
)

func main() {
	// Initialize the client attribute library (reads SharedPageHeader).
	attr.Init()

	pass := true
	pass = testValueI64() && pass
	pass = testValueStr() && pass
	pass = testMultiType() && pass
	pass = testConstraintAdd() && pass
	pass = testConstraintChain() && pass
	pass = testTrieExistence() && pass
	pass = testEagerNotification() && pass
	pass = testChangeGating() && pass

	if pass {
		fmt.Println("\nhandletest: All tests PASSED.")
	} else {
		fmt.Println("\nhandletest: Some tests FAILED.")
	}
}

// testValueI64 creates an int64 value handle and verifies Get/Set round-trip.
func testValueI64() bool {
	fmt.Println("=== Test 1: ValueI64 round-trip ===")
	h := attr.ValueI64(attr.ShepherdURI("int64", "foo"), 42)

	got := h.Get()
	fmt.Printf("initial Get() = %d (expected 42)\n", got)
	if got != 42 {
		fmt.Println("FAIL")
		return false
	}

	h.Set(99)
	got = h.Get()
	fmt.Printf("after Set(99), Get() = %d (expected 99)\n", got)
	if got != 99 {
		fmt.Println("FAIL")
		return false
	}

	fmt.Println("PASS")
	return true
}

// testValueStr creates a string value handle and verifies Get/Set round-trip.
func testValueStr() bool {
	fmt.Println("\n=== Test 2: ValueStr round-trip ===")
	h := attr.ValueStr(attr.ShepherdURI("str", "name"), "hello")

	got := h.Get()
	fmt.Printf("initial Get() = %q (expected \"hello\")\n", got)
	if got != "hello" {
		fmt.Println("FAIL")
		return false
	}

	h.Set("world")
	got = h.Get()
	fmt.Printf("after Set(\"world\"), Get() = %q (expected \"world\")\n", got)
	if got != "world" {
		fmt.Println("FAIL")
		return false
	}

	fmt.Println("PASS")
	return true
}

// testMultiType creates bool, float64, and tribool value handles.
func testMultiType() bool {
	fmt.Println("\n=== Test 3: Multi-type value handles ===")

	bh := attr.ValueBool(attr.ShepherdURI("bool", "flag"), true)
	if !bh.Get() {
		fmt.Println("bool initial: FAIL (expected true)")
		return false
	}
	bh.Set(false)
	if bh.Get() {
		fmt.Println("bool after Set(false): FAIL")
		return false
	}
	fmt.Println("bool: PASS")

	fh := attr.ValueF64(attr.ShepherdURI("f64", "ratio"), 3.14)
	got := fh.Get()
	fmt.Printf("f64 initial = %f (expected 3.14)\n", got)
	if got != 3.14 {
		fmt.Println("f64: FAIL")
		return false
	}
	fh.Set(2.718)
	got = fh.Get()
	fmt.Printf("f64 after Set(2.718) = %f (expected 2.718)\n", got)
	if got != 2.718 {
		fmt.Println("f64: FAIL")
		return false
	}
	fmt.Println("f64: PASS")

	th := attr.ValueTribool(attr.ShepherdURI("int64", "maybe"), 2) // unknown
	tgot := th.Get()
	fmt.Printf("tribool initial = %d (expected 2=unknown)\n", tgot)
	if tgot != 2 {
		fmt.Println("tribool: FAIL")
		return false
	}
	th.Set(1) // true
	tgot = th.Get()
	fmt.Printf("tribool after Set(1) = %d (expected 1=true)\n", tgot)
	if tgot != 1 {
		fmt.Println("tribool: FAIL")
		return false
	}
	fmt.Println("tribool: PASS")

	fmt.Println("PASS")
	return true
}

// testConstraintAdd creates two value handles and a constraint that adds them.
// The constraint is a hand-assembled VM program that uses BuiltinDerefI64 to
// read the dependency values by URI and then adds them.
func testConstraintAdd() bool {
	fmt.Println("\n=== Test 4: Constraint (add two values) ===")

	a := attr.ValueI64(attr.ShepherdURI("int64", "a"), 10)
	b := attr.ValueI64(attr.ShepherdURI("int64", "b"), 20)

	// Hand-assemble: deref(a_uri) + deref(b_uri) → return
	prog := &vm.Program{
		Code: []vm.Inst{
			vm.InstConstStr(0),                                 // push URI of a
			vm.InstCallBuiltin(vm.BuiltinDerefI64, 1),         // deref → I64
			vm.InstConstStr(1),                                 // push URI of b
			vm.InstCallBuiltin(vm.BuiltinDerefI64, 1),         // deref → I64
			vm.InstArith(vm.OpAdd, vm.TypeI64),                 // add
			vm.InstRet(1),                                      // return 1 value
		},
		Strings: []string{
			a.URI(),
			b.URI(),
		},
	}

	sum := attr.ConstraintI64(attr.ShepherdURI("int64", "sum"), prog, a, b)

	got := sum.Get()
	fmt.Printf("sum = %d (expected 30)\n", got)
	if got != 30 {
		fmt.Println("FAIL")
		return false
	}

	// Change a, verify lazy re-evaluation.
	a.Set(5)
	got = sum.Get()
	fmt.Printf("after a=5, sum = %d (expected 25)\n", got)
	if got != 25 {
		fmt.Println("FAIL")
		return false
	}

	// Change b too.
	b.Set(100)
	got = sum.Get()
	fmt.Printf("after b=100, sum = %d (expected 105)\n", got)
	if got != 105 {
		fmt.Println("FAIL")
		return false
	}

	fmt.Println("PASS")
	return true
}

// testConstraintChain creates a chain: a, b → sum(a+b) → doubled(sum*2).
func testConstraintChain() bool {
	fmt.Println("\n=== Test 5: Constraint chain (a+b → sum → doubled) ===")

	a := attr.ValueI64(attr.ShepherdURI("int64", "chain_a"), 3)
	b := attr.ValueI64(attr.ShepherdURI("int64", "chain_b"), 7)

	// sum = a + b
	sumProg := &vm.Program{
		Code: []vm.Inst{
			vm.InstConstStr(0),
			vm.InstCallBuiltin(vm.BuiltinDerefI64, 1),
			vm.InstConstStr(1),
			vm.InstCallBuiltin(vm.BuiltinDerefI64, 1),
			vm.InstArith(vm.OpAdd, vm.TypeI64),
			vm.InstRet(1),
		},
		Strings: []string{
			a.URI(),
			b.URI(),
		},
	}
	sum := attr.ConstraintI64(attr.ShepherdURI("int64", "chain_sum"), sumProg, a, b)

	// doubled = sum * 2
	doubledProg := &vm.Program{
		Code: []vm.Inst{
			vm.InstConstStr(0),
			vm.InstCallBuiltin(vm.BuiltinDerefI64, 1),
			vm.InstConstI64(2),
			vm.InstArith(vm.OpMul, vm.TypeI64),
			vm.InstRet(1),
		},
		Strings: []string{
			sum.URI(),
		},
	}
	doubled := attr.ConstraintI64(attr.ShepherdURI("int64", "chain_doubled"), doubledProg, sum)

	got := doubled.Get()
	fmt.Printf("doubled = %d (expected 20)\n", got)
	if got != 20 {
		fmt.Println("FAIL")
		return false
	}

	a.Set(10)
	got = doubled.Get()
	fmt.Printf("after a=10, doubled = %d (expected 34)\n", got)
	if got != 34 {
		fmt.Println("FAIL")
		return false
	}

	fmt.Println("PASS")
	return true
}

// testTrieExistence verifies trie walking for Exists().
func testTrieExistence() bool {
	fmt.Println("\n=== Test 6: Trie Exists() ===")

	// The previous tests created several attributes under "attr:///shepherd/{sid}/".
	// Exists should return true for the prefix.
	myPrefix := "attr:///shepherd/" + attr.SID()
	exists := attr.Exists(myPrefix)
	fmt.Printf("Exists(%q) = %v (expected true)\n", myPrefix, exists)
	if !exists {
		fmt.Println("FAIL")
		return false
	}

	// A non-existent prefix should return false.
	exists = attr.Exists("attr:///shepherd/nonexistent")
	fmt.Printf("Exists(\"attr:///shepherd/nonexistent\") = %v (expected false)\n", exists)
	if exists {
		fmt.Println("FAIL")
		return false
	}

	fmt.Println("PASS")
	return true
}

// testEagerNotification creates a value and a constraint, marks the constraint
// eager, writes the value, and verifies that WaitDirty returns the constraint's slot.
func testEagerNotification() bool {
	fmt.Println("\n=== Test 7: Eager notification round-trip ===")

	a := attr.ValueI64(attr.ShepherdURI("int64", "eager_a"), 5)

	// doubled = a * 2
	prog := &vm.Program{
		Code: []vm.Inst{
			vm.InstConstStr(0),
			vm.InstCallBuiltin(vm.BuiltinDerefI64, 1),
			vm.InstConstI64(2),
			vm.InstArith(vm.OpMul, vm.TypeI64),
			vm.InstRet(1),
		},
		Strings: []string{
			a.URI(),
		},
	}
	doubled := attr.ConstraintI64(attr.ShepherdURI("int64", "eager_doubled"), prog, a)

	// Force initial evaluation so the constraint is clean.
	got := doubled.Get()
	if got != 10 {
		fmt.Printf("initial doubled = %d (expected 10)\n", got)
		fmt.Println("FAIL")
		return false
	}

	// Enable eager notification on the constraint.
	doubled.SetEager(true)

	// Spawn goroutine that blocks on WaitDirty.
	var wg sync.WaitGroup
	var notifiedSlots []uint16
	wg.Add(1)
	go func() {
		defer wg.Done()
		notifiedSlots = attr.WaitDirty()
	}()

	// Write to the value — triggers dirty walk → eager notification.
	a.Set(10)

	// Wait for the goroutine to receive the notification.
	wg.Wait()

	// Verify we got notified about the doubled constraint's slot.
	foundSlot := false
	for _, s := range notifiedSlots {
		if s == doubled.Slot() {
			foundSlot = true
			break
		}
	}
	if !foundSlot {
		fmt.Printf("WaitDirty returned slots %v, expected to contain %d\n", notifiedSlots, doubled.Slot())
		fmt.Println("FAIL")
		return false
	}

	// Verify the constraint evaluates correctly after notification.
	got = doubled.Get()
	fmt.Printf("after a=10, doubled = %d (expected 20)\n", got)
	if got != 20 {
		fmt.Println("FAIL")
		return false
	}

	fmt.Println("PASS")
	return true
}

// testChangeGating verifies that writing the same value to an attribute does
// NOT trigger dirty propagation or eager notification.
func testChangeGating() bool {
	fmt.Println("\n=== Test 8: Change-gated propagation ===")

	a := attr.ValueI64(attr.ShepherdURI("int64", "gate_a"), 5)

	// sum = a + 0 (identity constraint)
	prog := &vm.Program{
		Code: []vm.Inst{
			vm.InstConstStr(0),
			vm.InstCallBuiltin(vm.BuiltinDerefI64, 1),
			vm.InstConstI64(0),
			vm.InstArith(vm.OpAdd, vm.TypeI64),
			vm.InstRet(1),
		},
		Strings: []string{
			a.URI(),
		},
	}
	sum := attr.ConstraintI64(attr.ShepherdURI("int64", "gate_sum"), prog, a)

	// Force initial evaluation.
	got := sum.Get()
	if got != 5 {
		fmt.Printf("initial sum = %d (expected 5)\n", got)
		fmt.Println("FAIL")
		return false
	}

	// Enable eager notification.
	sum.SetEager(true)

	// Write the SAME value — change-gating should skip propagation.
	a.Set(5)

	// The constraint should NOT be dirty (no propagation occurred).
	if sum.IsDirty() {
		fmt.Println("sum is dirty after Set(5) with same value — change gating failed")
		fmt.Println("FAIL")
		return false
	}
	fmt.Println("same-value Set(5): not dirty (change-gated) — PASS")

	// Now write a DIFFERENT value — should propagate.
	a.Set(6)
	if !sum.IsDirty() {
		fmt.Println("sum is NOT dirty after Set(6) — propagation failed")
		fmt.Println("FAIL")
		return false
	}

	got = sum.Get()
	fmt.Printf("after a=6, sum = %d (expected 6)\n", got)
	if got != 6 {
		fmt.Println("FAIL")
		return false
	}

	fmt.Println("PASS")
	return true
}

