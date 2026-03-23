package mancini

import (
	"image/color"
	"math"
)

// NeuBox is a decorative parent interactor with a single child.
// It draws neumorphic decoration (shadows/edges) around its child,
// using inside-out sizing: own Width = child Width + 2*margin,
// Height = child Height + 2*margin. The margin is the max shadow
// padding across all depth states, so state transitions never
// change the interactor's bounds.
type NeuBox struct {
	Pal    Palette
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
	margin        int64
}

func (n *NeuBox) GetLayout() *LayoutHandles { return n.Layout }

// NeuMaxPad computes the maximum shadow padding across all depth states.
// Returns the ceiling as an integer — all interactor dimensions are int64.
func NeuMaxPad(p NeuParams) int64 {
	// Raised: external shadows
	rOff := math.Max(p.Raised.DarkOff, p.Raised.LightOff)
	rBlur := math.Max(p.Raised.DarkBlur, p.Raised.LightBlur)
	rPad := rOff + math.Ceil(rBlur*3) + 2

	// Inset: internal shadows (extend beyond box for blur)
	iBlur := math.Max(p.Inset.DarkBlur, p.Inset.LightBlur)
	iPad := p.Inset.Off + math.Ceil(iBlur*3) + 2

	// Flush: edge stroke
	fPad := p.Flush.EdgeW + 2

	return int64(math.Ceil(math.Max(rPad, math.Max(iPad, fPad))))
}

// Draw implements the Drawer interface.
func (n *NeuBox) Draw(dc DrawContext, x, y, w, h float64) {
	publishLayout(n.Layout, x, y, w, h)

	// No child: pink error indicator.
	if n.Child == nil {
		dc.SetColor(errPink)
		dc.DrawRectangle(x, y, w, h)
		dc.Fill()
		return
	}

	m := float64(n.margin)
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
		face := n.Face
		if face == (color.NRGBA{}) {
			face = n.Pal.Surface
		}
		NeuBoxWith(n.Pal, dc, n.Depth, x, y, x+w, y+h, r, face, n.Params, nil)
	}

	n.Child.Draw(dc, childX, childY, childW, childH)
}
