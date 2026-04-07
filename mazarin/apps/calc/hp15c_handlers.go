// hp15c_handlers.go — Function handlers for HP 15C calculator buttons.
//
// Each handler implements the functionHandler interface. The Function(which)
// method dispatches to the correct operation based on shift state:
//   which=0: normal (primary label)
//   which=1: f-shift (gold label)
//   which=2: g-shift (blue label)
package main

// lookupHandler returns the functionHandler for a given primary label.
// Returns nil for buttons with no operations (spacers, etc.).
func lookupHandler(label string) functionHandler {
	switch label {
	case "\u221Ax":
		return &sqrtHandler{}
	case "e^x":
		return &expHandler{}
	case "10^x":
		return &pow10Handler{}
	case "y^x":
		return &powYXHandler{}
	case "1/x":
		return &recipHandler{}
	case "CHS":
		return &chsHandler{}
	case "7":
		return &digitHandler{d: 7}
	case "8":
		return &digitHandler{d: 8}
	case "9":
		return &digitHandler{d: 9}
	case "\u00F7":
		return &divHandler{}
	case "SST":
		return &nopHandler{}
	case "GTO":
		return &nopHandler{}
	case "SIN":
		return &sinHandler{}
	case "COS":
		return &cosHandler{}
	case "TAN":
		return &tanHandler{}
	case "EEX":
		return &eexHandler{}
	case "4":
		return &digitHandler{d: 4}
	case "5":
		return &digitHandler{d: 5}
	case "6":
		return &digitHandler{d: 6}
	case "\u00D7":
		return &mulHandler{}
	case "R/S":
		return &nopHandler{}
	case "GSB":
		return &nopHandler{}
	case "Rv":
		return &rollDownHandler{}
	case "x<>y":
		return &swapXYHandler{}
	case "<-":
		return &backspaceHandler{}
	case "E N T E R":
		return &enterHandler{}
	case "1":
		return &digitHandler{d: 1}
	case "2":
		return &digitHandler{d: 2}
	case "3":
		return &digitHandler{d: 3}
	case "-":
		return &subHandler{}
	case "ON":
		return &onHandler{}
	case "STO":
		return &stoHandler{}
	case "RCL":
		return &rclHandler{}
	case "0":
		return &digitHandler{d: 0}
	case "\u00B7":
		return &dotHandler{}
	case "E+":
		return &addHandler{}
	case "+":
		return &addHandler{}
	default:
		return &nopHandler{}
	}
}

// --- Handler implementations ---

type nopHandler struct{}

func (h *nopHandler) Function(which int) {}

type digitHandler struct{ d int }

func (h *digitHandler) Function(which int) {
	engine.Digit(h.d)
}

type sqrtHandler struct{}

func (h *sqrtHandler) Function(which int) {
	switch which {
	case 0:
		engine.Sqrt()
	case 2:
		engine.Square()
	}
}

type expHandler struct{}

func (h *expHandler) Function(which int) {
	switch which {
	case 0:
		engine.Exp()
	case 2:
		engine.Ln()
	}
}

type pow10Handler struct{}

func (h *pow10Handler) Function(which int) {
	switch which {
	case 0:
		engine.Pow10()
	case 2:
		engine.Log()
	}
}

type powYXHandler struct{}

func (h *powYXHandler) Function(which int) {
	switch which {
	case 0:
		engine.PowYX()
	case 2:
		engine.Percent()
	}
}

type recipHandler struct{}

func (h *recipHandler) Function(which int) {
	switch which {
	case 0:
		engine.Reciprocal()
	}
}

type chsHandler struct{}

func (h *chsHandler) Function(which int) {
	switch which {
	case 0:
		engine.CHS()
	case 2:
		engine.Abs()
	}
}

type divHandler struct{}

func (h *divHandler) Function(which int) {
	engine.Div()
}

type sinHandler struct{}

func (h *sinHandler) Function(which int) {
	switch which {
	case 0:
		engine.Sin()
	case 2:
		engine.Asin()
	}
}

type cosHandler struct{}

func (h *cosHandler) Function(which int) {
	switch which {
	case 0:
		engine.Cos()
	case 2:
		engine.Acos()
	}
}

type tanHandler struct{}

func (h *tanHandler) Function(which int) {
	switch which {
	case 0:
		engine.Tan()
	case 2:
		engine.Atan()
	}
}

type eexHandler struct{}

func (h *eexHandler) Function(which int) {
	switch which {
	case 0:
		engine.EEX()
	}
}

type mulHandler struct{}

func (h *mulHandler) Function(which int) {
	engine.Mul()
}

type rollDownHandler struct{}

func (h *rollDownHandler) Function(which int) {
	switch which {
	case 0:
		engine.RollDown()
	case 2:
		engine.RollUp()
	}
}

type swapXYHandler struct{}

func (h *swapXYHandler) Function(which int) {
	engine.SwapXY()
}

type backspaceHandler struct{}

func (h *backspaceHandler) Function(which int) {
	switch which {
	case 0:
		engine.Backspace()
	case 2:
		engine.ClearX()
	}
}

type enterHandler struct{}

func (h *enterHandler) Function(which int) {
	switch which {
	case 0:
		engine.Enter()
	case 2:
		engine.RecallLastX()
	}
}

type subHandler struct{}

func (h *subHandler) Function(which int) {
	engine.Sub()
}

type onHandler struct{}

func (h *onHandler) Function(which int) {
	engine.ClearX()
	engine.Y = 0
	engine.Z = 0
	engine.T = 0
	engine.LastX = 0
}

type stoHandler struct{}

func (h *stoHandler) Function(which int) {
	engine.StoreTo(0)
}

type rclHandler struct{}

func (h *rclHandler) Function(which int) {
	engine.RecallFrom(0)
}

type dotHandler struct{}

func (h *dotHandler) Function(which int) {
	engine.Dot()
}

type addHandler struct{}

func (h *addHandler) Function(which int) {
	engine.Add()
}
