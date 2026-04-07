// rpn.go — RPN stack engine for HP 15C calculator.
package main

import (
	"math"
	"strconv"
	"strings"
)

// RPNEngine implements a 4-register RPN stack (X, Y, Z, T) with
// HP 15C-compatible behavior: stack lift, LAST X, digit entry.
type RPNEngine struct {
	X, Y, Z, T float64
	LastX       float64
	Store       [10]float64

	// Digit entry state.
	entry    string // current digit buffer ("", "3.14", "-2.5e3", etc.)
	entering bool   // true while user is typing

	// Stack lift: enabled after most operations. When enabled, the next
	// digit entry pushes the stack before replacing X. Disabled after
	// ENTER and CLx so digits replace X in-place.
	liftEnabled bool

	// Shift state: f (gold) or g (blue). Cleared after one operation.
	FShift bool
	GShift bool

	// Display format.
	FixDigits int // decimal places in FIX mode (default 4)
}

func NewRPNEngine() *RPNEngine {
	return &RPNEngine{FixDigits: 4, liftEnabled: true}
}

// --- Display ---

func (e *RPNEngine) Display() string {
	if e.entering {
		s := e.entry
		if s == "" {
			s = "0"
		}
		return s
	}
	return e.formatX()
}

func (e *RPNEngine) formatX() string {
	x := e.X
	if math.IsInf(x, 1) {
		return "Overflow"
	}
	if math.IsInf(x, -1) {
		return "-Overflow"
	}
	if math.IsNaN(x) {
		return "Error"
	}

	// Try fixed-point first.
	s := strconv.FormatFloat(x, 'f', e.FixDigits, 64)
	// If too long for display, switch to scientific.
	if len(s) > 13 {
		s = strconv.FormatFloat(x, 'e', e.FixDigits, 64)
	}
	return s
}

// --- Digit Entry ---

func (e *RPNEngine) finishEntry() {
	if !e.entering {
		return
	}
	e.entering = false
	v, err := strconv.ParseFloat(e.entry, 64)
	if err != nil {
		v = 0
	}
	e.X = v
	e.entry = ""
	e.liftEnabled = true
}

func (e *RPNEngine) startEntry() {
	if e.liftEnabled {
		e.pushStack()
	}
	e.entering = true
	e.entry = ""
	e.liftEnabled = false
}

func (e *RPNEngine) Digit(d int) {
	e.clearShift()
	if !e.entering {
		e.startEntry()
	}
	// Prevent leading zeros (but allow "0.")
	if e.entry == "0" {
		e.entry = ""
	}
	e.entry += strconv.Itoa(d)
}

func (e *RPNEngine) Dot() {
	e.clearShift()
	if !e.entering {
		e.startEntry()
		e.entry = "0"
	}
	if !strings.Contains(e.entry, ".") {
		if e.entry == "" {
			e.entry = "0"
		}
		e.entry += "."
	}
}

func (e *RPNEngine) CHS() {
	e.clearShift()
	if e.entering {
		if strings.HasPrefix(e.entry, "-") {
			e.entry = e.entry[1:]
		} else {
			e.entry = "-" + e.entry
		}
	} else {
		e.X = -e.X
	}
}

func (e *RPNEngine) EEX() {
	e.clearShift()
	if !e.entering {
		e.startEntry()
		e.entry = "1"
	}
	if !strings.Contains(e.entry, "e") {
		e.entry += "e"
	}
}

func (e *RPNEngine) Backspace() {
	e.clearShift()
	if e.entering && len(e.entry) > 0 {
		e.entry = e.entry[:len(e.entry)-1]
		if e.entry == "" || e.entry == "-" {
			e.entry = "0"
		}
	}
}

func (e *RPNEngine) ClearX() {
	e.clearShift()
	e.entering = false
	e.entry = ""
	e.X = 0
	e.liftEnabled = false
}

// --- Stack Operations ---

func (e *RPNEngine) pushStack() {
	e.T = e.Z
	e.Z = e.Y
	e.Y = e.X
}

func (e *RPNEngine) dropStack() {
	e.Y = e.Z
	e.Z = e.T
	// T stays (HP convention)
}

func (e *RPNEngine) Enter() {
	e.clearShift()
	e.finishEntry()
	e.pushStack()
	// X stays the same (duplicated into Y)
	e.liftEnabled = false
}

func (e *RPNEngine) SwapXY() {
	e.clearShift()
	e.finishEntry()
	e.X, e.Y = e.Y, e.X
}

func (e *RPNEngine) RollDown() {
	e.clearShift()
	e.finishEntry()
	old := e.X
	e.X = e.Y
	e.Y = e.Z
	e.Z = e.T
	e.T = old
}

func (e *RPNEngine) RollUp() {
	e.clearShift()
	e.finishEntry()
	old := e.T
	e.T = e.Z
	e.Z = e.Y
	e.Y = e.X
	e.X = old
}

func (e *RPNEngine) RecallLastX() {
	e.clearShift()
	e.finishEntry()
	if e.liftEnabled {
		e.pushStack()
	}
	e.X = e.LastX
	e.liftEnabled = true
}

// --- Binary Operations ---

func (e *RPNEngine) binaryOp(f func(y, x float64) float64) {
	e.finishEntry()
	e.LastX = e.X
	e.X = f(e.Y, e.X)
	e.dropStack()
	e.liftEnabled = true
}

func (e *RPNEngine) Add()  { e.clearShift(); e.binaryOp(func(y, x float64) float64 { return y + x }) }
func (e *RPNEngine) Sub()  { e.clearShift(); e.binaryOp(func(y, x float64) float64 { return y - x }) }
func (e *RPNEngine) Mul()  { e.clearShift(); e.binaryOp(func(y, x float64) float64 { return y * x }) }
func (e *RPNEngine) Div()  { e.clearShift(); e.binaryOp(func(y, x float64) float64 { return y / x }) }
func (e *RPNEngine) PowYX() {
	e.clearShift()
	e.binaryOp(func(y, x float64) float64 { return math.Pow(y, x) })
}
func (e *RPNEngine) Percent() {
	e.clearShift()
	e.finishEntry()
	e.LastX = e.X
	e.X = e.Y * e.X / 100.0
	e.liftEnabled = true
}

// --- Unary Operations ---

func (e *RPNEngine) unaryOp(f func(float64) float64) {
	e.finishEntry()
	e.LastX = e.X
	e.X = f(e.X)
	e.liftEnabled = true
}

func (e *RPNEngine) Sqrt()       { e.clearShift(); e.unaryOp(math.Sqrt) }
func (e *RPNEngine) Square()     { e.clearShift(); e.unaryOp(func(x float64) float64 { return x * x }) }
func (e *RPNEngine) Reciprocal() { e.clearShift(); e.unaryOp(func(x float64) float64 { return 1.0 / x }) }
func (e *RPNEngine) Exp()        { e.clearShift(); e.unaryOp(math.Exp) }
func (e *RPNEngine) Ln()         { e.clearShift(); e.unaryOp(math.Log) }
func (e *RPNEngine) Pow10()      { e.clearShift(); e.unaryOp(func(x float64) float64 { return math.Pow(10, x) }) }
func (e *RPNEngine) Log()        { e.clearShift(); e.unaryOp(math.Log10) }
func (e *RPNEngine) Sin()        { e.clearShift(); e.unaryOp(math.Sin) }
func (e *RPNEngine) Cos()        { e.clearShift(); e.unaryOp(math.Cos) }
func (e *RPNEngine) Tan()        { e.clearShift(); e.unaryOp(math.Tan) }
func (e *RPNEngine) Asin()       { e.clearShift(); e.unaryOp(math.Asin) }
func (e *RPNEngine) Acos()       { e.clearShift(); e.unaryOp(math.Acos) }
func (e *RPNEngine) Atan()       { e.clearShift(); e.unaryOp(math.Atan) }
func (e *RPNEngine) Abs()        { e.clearShift(); e.unaryOp(math.Abs) }
func (e *RPNEngine) Factorial() {
	e.clearShift()
	e.unaryOp(func(x float64) float64 { return math.Gamma(x + 1) })
}

func (e *RPNEngine) Pi() {
	e.clearShift()
	e.finishEntry()
	if e.liftEnabled {
		e.pushStack()
	}
	e.X = math.Pi
	e.liftEnabled = true
}

// --- Memory ---

func (e *RPNEngine) StoreTo(reg int) {
	e.clearShift()
	if reg >= 0 && reg < 10 {
		e.finishEntry()
		e.Store[reg] = e.X
	}
}

func (e *RPNEngine) RecallFrom(reg int) {
	e.clearShift()
	if reg >= 0 && reg < 10 {
		e.finishEntry()
		if e.liftEnabled {
			e.pushStack()
		}
		e.X = e.Store[reg]
		e.liftEnabled = true
	}
}

// --- Shift ---

func (e *RPNEngine) SetFShift() {
	e.FShift = !e.FShift
	e.GShift = false
}

func (e *RPNEngine) SetGShift() {
	e.GShift = !e.GShift
	e.FShift = false
}

func (e *RPNEngine) clearShift() {
	e.FShift = false
	e.GShift = false
}
