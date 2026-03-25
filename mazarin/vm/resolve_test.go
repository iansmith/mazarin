package vm

import "testing"

// mockResolver implements AttrResolver for testing.
type mockResolver struct {
	attrs map[string]mockAttr
}

type mockAttr struct {
	slot  uint16
	value Value
}

func newMockResolver() *mockResolver {
	return &mockResolver{attrs: make(map[string]mockAttr)}
}

func (r *mockResolver) Set(uri string, slot uint16, v Value) {
	r.attrs[uri] = mockAttr{slot: slot, value: v}
}

func (r *mockResolver) Find(pattern string) ([]string, uint16) {
	// Simple wildcard matching: replace '*' with any segment.
	var results []string
	for uri := range r.attrs {
		if matchPattern(pattern, uri) {
			results = append(results, uri)
		}
	}
	return results, 9999 // query slot
}

func (r *mockResolver) FindWhere(pattern string, value string) ([]string, uint16) {
	uris, slot := r.Find(pattern)
	var filtered []string
	for _, uri := range uris {
		a, ok := r.attrs[uri]
		if ok && a.value.typ == TypeStr && a.value.str == value {
			filtered = append(filtered, uri)
		}
	}
	return filtered, slot
}

func (r *mockResolver) Deref(uri string, expectedType uint8) (Value, uint16, bool) {
	a, ok := r.attrs[uri]
	if !ok {
		return Value{}, 0, false
	}
	if a.value.typ != expectedType {
		return Value{}, a.slot, false
	}
	return a.value, a.slot, true
}

func (r *mockResolver) IncrementI64(uri string) (int64, bool) {
	a, ok := r.attrs[uri]
	if !ok {
		return 0, false
	}
	newVal := a.value.AsI64() + 1
	r.attrs[uri] = mockAttr{slot: a.slot, value: I64(newVal)}
	return newVal, true
}

func (r *mockResolver) Exists(prefix string) (bool, uint16) {
	for uri := range r.attrs {
		if len(uri) >= len(prefix) && uri[:len(prefix)] == prefix {
			return true, 8888
		}
	}
	return false, 8888
}

// matchPattern does simple segment-level wildcard matching.
func matchPattern(pattern, uri string) bool {
	const prefix = "attr:///"
	if len(pattern) < len(prefix) || len(uri) < len(prefix) {
		return false
	}
	pRest := pattern[len(prefix):]
	uRest := uri[len(prefix):]
	pSegs := splitSegments(pRest)
	uSegs := splitSegments(uRest)
	if len(pSegs) != len(uSegs) {
		return false
	}
	for i := range pSegs {
		if pSegs[i] != "*" && pSegs[i] != uSegs[i] {
			return false
		}
	}
	return true
}

func splitSegments(s string) []string {
	var segs []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '/' {
			if i > start {
				segs = append(segs, s[start:i])
			}
			start = i + 1
		}
	}
	return segs
}

func TestFind(t *testing.T) {
	r := newMockResolver()
	r.Set("attr:///shepherd/cal/str/title", 1, Str("hello"))
	r.Set("attr:///shepherd/mail/str/subject", 2, Str("world"))

	prog := &Program{
		Strings: []string{"attr:///shepherd/*/str/title"},
		Code: []Inst{
			{Opcode: OpConstStr, Op1: 0},
			InstCallBuiltin(BuiltinFind, 1),
			InstCallBuiltin(BuiltinCollLen, 1),
			InstRet(1),
		},
	}
	results, readSet, err := RunWithResolver(prog, r)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 1 {
		t.Fatalf("expected 1 match, got %d", results[0].AsI64())
	}
	if readSet.Count != 1 || readSet.Slots[0] != 9999 {
		t.Fatalf("expected read set {9999}, got count=%d", readSet.Count)
	}
}

func TestDerefI64(t *testing.T) {
	r := newMockResolver()
	r.Set("attr:///kernel/int64/mouse/x", 10, I64(500))

	prog := &Program{
		Strings: []string{"attr:///kernel/int64/mouse/x"},
		Code: []Inst{
			{Opcode: OpConstStr, Op1: 0},
			InstCallBuiltin(BuiltinDerefI64, 1),
			InstRet(1),
		},
	}
	results, readSet, err := RunWithResolver(prog, r)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 500 {
		t.Fatalf("expected 500, got %d", results[0].AsI64())
	}
	if readSet.Count != 1 || readSet.Slots[0] != 10 {
		t.Fatalf("expected read set {10}, got count=%d slots[0]=%d", readSet.Count, readSet.Slots[0])
	}
}

func TestDerefMissing(t *testing.T) {
	r := newMockResolver()

	prog := &Program{
		Strings: []string{"attr:///kernel/int64/nonexistent"},
		Code: []Inst{
			{Opcode: OpConstStr, Op1: 0},
			InstCallBuiltin(BuiltinDerefI64, 1),
			InstCallBuiltin(BuiltinIsUnknown, 1),
			InstRet(1),
		},
	}
	results, _, err := RunWithResolver(prog, r)
	if err != nil {
		t.Fatal(err)
	}
	if !results[0].AsBool() {
		t.Fatal("expected is_unknown=true for missing attr")
	}
}

func TestDerefTypeMismatch(t *testing.T) {
	r := newMockResolver()
	r.Set("attr:///kernel/str/name", 20, Str("hello"))

	prog := &Program{
		Strings: []string{"attr:///kernel/str/name"},
		Code: []Inst{
			{Opcode: OpConstStr, Op1: 0},
			InstCallBuiltin(BuiltinDerefI64, 1), // asking for I64 but it's a Str
			InstCallBuiltin(BuiltinIsUnknown, 1),
			InstRet(1),
		},
	}
	results, _, err := RunWithResolver(prog, r)
	if err != nil {
		t.Fatal(err)
	}
	if !results[0].AsBool() {
		t.Fatal("expected is_unknown=true for type mismatch")
	}
}

func TestExists(t *testing.T) {
	r := newMockResolver()
	r.Set("attr:///shepherd/cal/str/title", 1, Str("hello"))

	prog := &Program{
		Strings: []string{"attr:///shepherd/cal"},
		Code: []Inst{
			{Opcode: OpConstStr, Op1: 0},
			InstCallBuiltin(BuiltinExists, 1),
			InstRet(1),
		},
	}
	results, readSet, err := RunWithResolver(prog, r)
	if err != nil {
		t.Fatal(err)
	}
	if !results[0].AsBool() {
		t.Fatal("expected exists=true")
	}
	if readSet.Count != 1 {
		t.Fatalf("expected 1 read set entry, got %d", readSet.Count)
	}
}

func TestExistsMissing(t *testing.T) {
	r := newMockResolver()

	prog := &Program{
		Strings: []string{"attr:///shepherd/nonexistent"},
		Code: []Inst{
			{Opcode: OpConstStr, Op1: 0},
			InstCallBuiltin(BuiltinExists, 1),
			InstRet(1),
		},
	}
	results, _, err := RunWithResolver(prog, r)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsBool() {
		t.Fatal("expected exists=false")
	}
}

func TestURISegment(t *testing.T) {
	prog := &Program{
		Strings: []string{"attr:///shepherd/calendar.elf/str/currentlyDisplayed/eventTitle"},
		Code: []Inst{
			{Opcode: OpConstStr, Op1: 0},
			InstConstI64(0),
			InstCallBuiltin(BuiltinURISegment, 2),
			InstStore(0),
			{Opcode: OpConstStr, Op1: 0},
			InstConstI64(3),
			InstCallBuiltin(BuiltinURISegment, 2),
			InstStore(1),
			InstLoad(0),
			InstLoad(1),
			InstRet(2),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsStr() != "shepherd" {
		t.Fatalf("expected segment[0]='shepherd', got %q", results[0].AsStr())
	}
	if results[1].AsStr() != "currentlyDisplayed" {
		t.Fatalf("expected segment[3]='currentlyDisplayed', got %q", results[1].AsStr())
	}
}

func TestIsUnknown(t *testing.T) {
	prog := &Program{
		Code: []Inst{
			InstConstI64(42),
			InstCallBuiltin(BuiltinIsUnknown, 1),
			InstStore(0),
			{Opcode: OpConstTri, Imm: 2}, // tribool(unknown)
			InstCallBuiltin(BuiltinIsUnknown, 1),
			InstStore(1),
			InstLoad(0),
			InstLoad(1),
			InstRet(2),
		},
	}
	results, err := Run(prog)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsBool() {
		t.Fatal("is_unknown(42) should be false")
	}
	if !results[1].AsBool() {
		t.Fatal("is_unknown(tribool(unknown)) should be true")
	}
}

func TestReadSetDedup(t *testing.T) {
	var rs ReadSet
	rs.Add(10)
	rs.Add(20)
	rs.Add(10) // duplicate
	rs.Add(30)
	rs.Add(20) // duplicate

	if rs.Count != 3 {
		t.Fatalf("expected 3 unique slots, got %d", rs.Count)
	}
}

func TestReadSetEqual(t *testing.T) {
	a := &ReadSet{}
	a.Add(10)
	a.Add(20)

	b := &ReadSet{}
	b.Add(20)
	b.Add(10)

	if !a.Equal(b) {
		t.Fatal("expected equal read sets")
	}

	c := &ReadSet{}
	c.Add(10)
	c.Add(30)

	if a.Equal(c) {
		t.Fatal("expected unequal read sets")
	}
}

func TestIncrementAtomic(t *testing.T) {
	r := newMockResolver()
	r.Set("attr:///clocks/int64/eagerCount", 50, I64(0))

	prog := &Program{
		Strings: []string{"attr:///clocks/int64/eagerCount"},
		Code: []Inst{
			{Opcode: OpConstStr, Op1: 0},
			InstCallBuiltin(BuiltinIncrementAtomic, 1),
			InstStore(0),
			{Opcode: OpConstStr, Op1: 0},
			InstCallBuiltin(BuiltinIncrementAtomic, 1),
			InstStore(1),
			InstLoad(0),
			InstLoad(1),
			InstRet(2),
		},
	}
	results, readSet, err := RunWithResolver(prog, r)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsI64() != 1 {
		t.Fatalf("expected first increment=1, got %d", results[0].AsI64())
	}
	if results[1].AsI64() != 2 {
		t.Fatalf("expected second increment=2, got %d", results[1].AsI64())
	}
	// increment_atomic does NOT add to read set.
	if readSet.Count != 0 {
		t.Fatalf("expected empty read set, got count=%d", readSet.Count)
	}
}

// Integration test: cross-shepherd service discovery workflow.
func TestServiceDiscoveryWorkflow(t *testing.T) {
	r := newMockResolver()
	r.Set("attr:///shepherd/calendar.elf/str/currentlyDisplayed/eventTitle", 42, Str("Team Standup"))

	// Program: find calendars, get first, build URI, deref title.
	prog := &Program{
		Strings: []string{
			"attr:///shepherd/*/str/currentlyDisplayed/eventTitle", // 0: find pattern
			"No calendar available",                              // 1: fallback
		},
		Code: []Inst{
			// find(pattern)
			{Opcode: OpConstStr, Op1: 0},
			InstCallBuiltin(BuiltinFind, 1),
			InstDup(),
			InstCallBuiltin(BuiltinCollLen, 1),
			InstConstI64(0),
			InstCmp(OpEq, TypeI64),
			InstIf(),
			{Opcode: OpConstStr, Op1: 1},
			InstRet(1),
			InstEndIf(),
			// Get first result and deref as string.
			InstConstI64(0),
			InstCallBuiltin(BuiltinCollGet, 2),
			InstCallBuiltin(BuiltinDerefStr, 1),
			InstDup(),
			InstCallBuiltin(BuiltinIsUnknown, 1),
			InstIf(),
			{Opcode: OpConstStr, Op1: 1},
			InstRet(1),
			InstEndIf(),
			InstRet(1),
		},
	}
	results, readSet, err := RunWithResolver(prog, r)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].AsStr() != "Team Standup" {
		t.Fatalf("expected 'Team Standup', got %q", results[0].AsStr())
	}
	// Read set should have the query slot + the deref slot.
	if readSet.Count != 2 {
		t.Fatalf("expected 2 read set entries, got %d", readSet.Count)
	}
}
