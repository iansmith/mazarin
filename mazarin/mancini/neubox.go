package mancini

import (
	"image"
	"image/color"
	"math"

	"github.com/fogleman/gg"
)

// NeuBox is a decorative parent interactor with a single child.
// It draws neumorphic decoration (shadows/edges) around its child,
// using inside-out sizing: own Width = child Width + 2*margin,
// Height = child Height + 2*margin. The margin is the max shadow
// padding across all depth states, so state transitions never
// change the interactor's bounds.
type NeuBox struct {
	Theme  *Theme
	Name   string
	Depth  NeuDepth
	Params NeuParams
	Face   color.NRGBA
	Radius float64 // corner radius (logical pixels)
	Child   Drawer
	MaxSize float64 // max dimension in logical pixels (0 = default 800)
	Layout  *LayoutHandles

	lastChildHash int64
	lastDepth     NeuDepth
	margin        float64
}

func (n *NeuBox) GetLayout() *LayoutHandles { return n.Layout }

// neuMaxPad computes the maximum shadow padding across all depth states.
func neuMaxPad(p NeuParams) float64 {
	// Raised: external shadows
	rOff := math.Max(p.Raised.DarkOff, p.Raised.LightOff)
	rBlur := math.Max(p.Raised.DarkBlur, p.Raised.LightBlur)
	rPad := rOff + math.Ceil(rBlur*3) + 2

	// Inset: internal shadows (extend beyond box for blur)
	iBlur := math.Max(p.Inset.DarkBlur, p.Inset.LightBlur)
	iPad := p.Inset.Off + math.Ceil(iBlur*3) + 2

	// Flush: edge stroke
	fPad := p.Flush.EdgeW + 2

	return math.Max(rPad, math.Max(iPad, fPad))
}

// Draw implements the Drawer interface.
func (n *NeuBox) Draw(canvas *image.RGBA, x, y, w, h float64) {
	publishLayout(n.Layout, x, y, w, h)

	// No child: pink error indicator.
	if n.Child == nil {
		dc := gg.NewContextForRGBA(canvas)
		pc := errPink
		if n.Theme != nil {
			pc = n.Theme.C(pc)
		}
		dc.SetColor(pc)
		dc.DrawRectangle(x, y, w, h)
		dc.Fill()
		return
	}

	m := n.margin
	childX := x + m
	childY := y + m
	childW := w - 2*m
	childH := h - 2*m

	// Check if decoration needs redrawing: depth changed or child layout changed.
	needDecoration := true
	if n.Depth == n.lastDepth {
		if layouter, ok := n.Child.(Layouter); ok {
			hash := layouter.GetLayout().boundsHashValue()
			if hash == n.lastChildHash && n.lastChildHash != 0 {
				needDecoration = false
			}
			n.lastChildHash = hash
		}
	}
	n.lastDepth = n.Depth

	if needDecoration {
		r := n.Radius
		if n.Theme != nil {
			r = n.Theme.Px(r)
		}
		face := n.Face
		if face == (color.NRGBA{}) && n.Theme != nil {
			face = n.Theme.Pal.Surface
		}
		NeuBoxWith(n.Theme, canvas, n.Depth, x, y, x+w, y+h, r, face, n.Params, nil)
	}

	n.Child.Draw(canvas, childX, childY, childW, childH)
}
