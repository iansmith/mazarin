// attrtest exercises the mazarin/attr Eval/Vite attribute library
// inside a priest. It prints results to stdout via fmt.
package main

import (
	"fmt"

	"mazzy/mazarin/attr"
)

func main() {
	pass := true
	pass = example1() && pass
	pass = example2() && pass
	pass = example3() && pass
	pass = example4() && pass

	if pass {
		fmt.Println("\nAll examples passed.")
	} else {
		fmt.Println("\nSome examples FAILED.")
	}
}

func example1() bool {
	fmt.Println("=== Example 1: Value + Constraint Chain ===")
	foo := attr.NewValue[int64](0)
	bar := attr.NewValue[int64](0)
	baz := attr.NewConstraint(func() int64 {
		return foo.Get() + bar.Get()
	}, foo, bar)

	grik := attr.NewValue[int64](0)
	grak := attr.NewConstraint(func() int64 {
		return (grik.Get() + baz.Get()) / 2
	}, grik, baz)

	foo.Set(12)
	bar.Set(4)
	grik.Set(9)

	got := grak.Get()
	fmt.Printf("grak = %d (expected 12)\n", got)
	if got != 12 {
		fmt.Println("FAIL")
		return false
	}
	fmt.Println("PASS")
	return true
}

func example2() bool {
	fmt.Println("\n=== Example 2: Verify Laziness ===")
	computeCount := 0
	foo := attr.NewValue[int64](0)
	bar := attr.NewConstraint(func() int64 {
		computeCount++
		return foo.Get() * 2
	}, foo)

	foo.Set(1)
	foo.Set(2)
	foo.Set(3)

	fmt.Printf("computes before Get(): %d (expected 0)\n", computeCount)
	if computeCount != 0 {
		fmt.Println("FAIL")
		return false
	}

	got := bar.Get()
	fmt.Printf("bar = %d (expected 6)\n", got)
	fmt.Printf("computes after Get(): %d (expected 1)\n", computeCount)
	if got != 6 || computeCount != 1 {
		fmt.Println("FAIL")
		return false
	}
	fmt.Println("PASS")
	return true
}

func example3() bool {
	fmt.Println("\n=== Example 3: Eager Constraint (Cursor Analogy) ===")
	type Point struct{ X, Y int64 }

	drawCalls := 0
	mousePos := attr.NewValue(Point{0, 0})
	cursor := attr.NewConstraint(func() Point {
		p := mousePos.Get()
		drawCalls++
		fmt.Printf("  [cursor] drawing at (%d, %d)\n", p.X, p.Y)
		return p
	}, mousePos)
	cursor.SetEager(true)

	fmt.Println("--- move mouse to (100, 200) ---")
	mousePos.Set(Point{100, 200})

	fmt.Println("--- move mouse to (150, 250) ---")
	mousePos.Set(Point{150, 250})

	fmt.Printf("total draws: %d (expected 2)\n", drawCalls)
	if drawCalls != 2 {
		fmt.Println("FAIL")
		return false
	}
	fmt.Println("PASS")
	return true
}

func example4() bool {
	fmt.Println("\n=== Example 4: Diamond Dependency (No Double Compute) ===")
	//     A
	//    / \
	//   B   C
	//    \ /
	//     D
	computeD := 0
	a := attr.NewValue[int64](1)
	b := attr.NewConstraint(func() int64 { return a.Get() * 2 }, a)
	c := attr.NewConstraint(func() int64 { return a.Get() + 10 }, a)
	d := attr.NewConstraint(func() int64 {
		computeD++
		return b.Get() + c.Get()
	}, b, c)

	a.Set(5)
	got := d.Get()
	fmt.Printf("d = %d (expected 25)\n", got)
	fmt.Printf("d computed: %d times (expected 1)\n", computeD)
	if got != 25 || computeD != 1 {
		fmt.Println("FAIL")
		return false
	}

	a.Set(7)
	got = d.Get()
	fmt.Printf("d = %d (expected 31)\n", got)
	fmt.Printf("d computed: %d times (expected 2)\n", computeD)
	if got != 31 || computeD != 2 {
		fmt.Println("FAIL")
		return false
	}
	fmt.Println("PASS")
	return true
}
