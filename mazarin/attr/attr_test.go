package attr

import "testing"

func TestValueGetSet(t *testing.T) {
	a := NewValue[int64](42)
	if got := a.Get(); got != 42 {
		t.Fatalf("expected 42, got %d", got)
	}
	a.Set(99)
	if got := a.Get(); got != 99 {
		t.Fatalf("expected 99, got %d", got)
	}
}

func TestConstraintBasic(t *testing.T) {
	foo := NewValue[int64](3)
	bar := NewValue[int64](7)
	baz := NewConstraint(func() int64 {
		return foo.Get() + bar.Get()
	}, foo, bar)

	if got := baz.Get(); got != 10 {
		t.Fatalf("expected 10, got %d", got)
	}
}

func TestConstraintChain(t *testing.T) {
	foo := NewValue[int64](0)
	bar := NewValue[int64](0)
	baz := NewConstraint(func() int64 {
		return foo.Get() + bar.Get()
	}, foo, bar)

	grik := NewValue[int64](0)
	grak := NewConstraint(func() int64 {
		return (grik.Get() + baz.Get()) / 2
	}, grik, baz)

	foo.Set(12)
	bar.Set(4)
	grik.Set(9)

	if got := grak.Get(); got != 12 {
		t.Fatalf("expected 12, got %d", got)
	}
}

func TestLaziness(t *testing.T) {
	computeCount := 0
	foo := NewValue[int64](0)
	bar := NewConstraint(func() int64 {
		computeCount++
		return foo.Get() * 2
	}, foo)

	foo.Set(1)
	foo.Set(2)
	foo.Set(3)
	if computeCount != 0 {
		t.Fatalf("expected 0 computes before Get(), got %d", computeCount)
	}

	got := bar.Get()
	if got != 6 {
		t.Fatalf("expected 6, got %d", got)
	}
	if computeCount != 1 {
		t.Fatalf("expected 1 compute after Get(), got %d", computeCount)
	}
}

func TestCleanNoRecompute(t *testing.T) {
	computeCount := 0
	foo := NewValue[int64](5)
	bar := NewConstraint(func() int64 {
		computeCount++
		return foo.Get() * 2
	}, foo)

	bar.Get()
	if computeCount != 1 {
		t.Fatalf("expected 1, got %d", computeCount)
	}

	// Get() again without Set() — should not recompute.
	bar.Get()
	if computeCount != 1 {
		t.Fatalf("expected still 1, got %d", computeCount)
	}
}

func TestEager(t *testing.T) {
	drawCalls := 0
	source := NewValue[int64](0)
	display := NewConstraint(func() int64 {
		drawCalls++
		return source.Get()
	}, source)
	display.SetEager(true)

	source.Set(100)
	if drawCalls != 1 {
		t.Fatalf("expected 1 eager compute, got %d", drawCalls)
	}

	source.Set(200)
	if drawCalls != 2 {
		t.Fatalf("expected 2 eager computes, got %d", drawCalls)
	}
}

func TestDiamondDependency(t *testing.T) {
	//     A
	//    / \
	//   B   C
	//    \ /
	//     D
	computeD := 0
	a := NewValue[int64](1)
	b := NewConstraint(func() int64 { return a.Get() * 2 }, a)
	c := NewConstraint(func() int64 { return a.Get() + 10 }, a)
	d := NewConstraint(func() int64 {
		computeD++
		return b.Get() + c.Get()
	}, b, c)

	a.Set(5)
	got := d.Get()
	if got != 25 {
		t.Fatalf("expected 25, got %d", got)
	}
	if computeD != 1 {
		t.Fatalf("expected 1 compute of D, got %d", computeD)
	}

	a.Set(7)
	got = d.Get()
	if got != 31 {
		t.Fatalf("expected 31, got %d", got)
	}
	if computeD != 2 {
		t.Fatalf("expected 2 computes of D, got %d", computeD)
	}
}

func TestSetOnConstraintPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on Set() of constraint")
		}
	}()
	a := NewValue[int64](1)
	b := NewConstraint(func() int64 { return a.Get() }, a)
	b.Set(42)
}

func TestEagerWithDiamond(t *testing.T) {
	//     A
	//    / \
	//   B   C
	//    \ /
	//     D (eager)
	computeD := 0
	a := NewValue[int64](0)
	b := NewConstraint(func() int64 { return a.Get() * 3 }, a)
	c := NewConstraint(func() int64 { return a.Get() + 1 }, a)
	d := NewConstraint(func() int64 {
		computeD++
		return b.Get() + c.Get()
	}, b, c)
	d.SetEager(true)

	a.Set(10)
	// D should have been eagerly computed: (10*3) + (10+1) = 30+11 = 41
	if computeD != 1 {
		t.Fatalf("expected 1 eager compute of D, got %d", computeD)
	}
	// Get() should return cached value without recomputing.
	got := d.Get()
	if got != 41 {
		t.Fatalf("expected 41, got %d", got)
	}
	if computeD != 1 {
		t.Fatalf("expected still 1 compute, got %d", computeD)
	}
}

type point struct{ X, Y int64 }

func TestStructAttribute(t *testing.T) {
	pos := NewValue(point{0, 0})
	doubled := NewConstraint(func() point {
		p := pos.Get()
		return point{p.X * 2, p.Y * 2}
	}, pos)

	pos.Set(point{5, 10})
	got := doubled.Get()
	if got.X != 10 || got.Y != 20 {
		t.Fatalf("expected {10 20}, got %+v", got)
	}
}

func TestMultipleEagerNodes(t *testing.T) {
	calls1, calls2 := 0, 0
	src := NewValue[int64](0)
	e1 := NewConstraint(func() int64 {
		calls1++
		return src.Get() + 1
	}, src)
	e2 := NewConstraint(func() int64 {
		calls2++
		return src.Get() + 2
	}, src)
	e1.SetEager(true)
	e2.SetEager(true)

	src.Set(5)
	if calls1 != 1 || calls2 != 1 {
		t.Fatalf("expected both eager nodes computed once, got %d and %d", calls1, calls2)
	}
	if e1.Get() != 6 || e2.Get() != 7 {
		t.Fatalf("expected 6 and 7, got %d and %d", e1.Get(), e2.Get())
	}
}
