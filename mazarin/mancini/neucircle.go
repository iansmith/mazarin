package mancini

import (
	"image/color"
	"math"
)

// NeuCircle is a decorative parent interactor with a single child.
// It draws neumorphic circular decoration (shadows/edges) around its child,
// using inside-out sizing: own Width = Height = max(childW, childH) + 2*margin.
// The child receives the full inner circle diameter (outerDiam - 2*margin),
// which is correct for circular children like Clock.
// The margin is the max shadow padding across all depth states, so state
// transitions never change the interactor's bounds.
type NeuCircle struct {
	Pal    Palette
	Name   string
	Depth  NeuDepth
	Params NeuParams
	Face   color.NRGBA
	Child  Drawer
	Layout *LayoutHandles

	lastChildHash int64
	lastDepth     NeuDepth
	margin        float64
}

func (n *NeuCircle) GetLayout() *LayoutHandles { return n.Layout }
func (n *NeuCircle) GetChild() Drawer            { return n.Child }

// preferredDiameter computes the circle diameter from the child's preferred size.
// diameter = max(childW, childH) + 2*margin. The child receives the full inner
// circle diameter, so no sqrt(2) scaling is needed.
func (n *NeuCircle) preferredDiameter() float64 {
	if n.Child == nil {
		return 0
	}
	var childW, childH float64
	if ws, ok := n.Child.(WidthSizer); ok {
		childW = ws.PreferredWidth()
	}
	if hs, ok := n.Child.(Sizer); ok {
		childH = hs.PreferredHeight()
	}
	if childW == 0 && childH == 0 {
		return 0
	}
	side := math.Max(childW, childH)
	return math.Ceil(side + 2*n.margin)
}

// PreferredWidth returns the circle diameter (Width == Height for a circle).
func (n *NeuCircle) PreferredWidth() float64 { return n.preferredDiameter() }

// PreferredHeight returns the circle diameter (Width == Height for a circle).
func (n *NeuCircle) PreferredHeight() float64 { return n.preferredDiameter() }

// Draw implements the Drawer interface.
func (n *NeuCircle) Draw(dc DrawContext, x, y, w, h float64) {
	// Publish the preferred diameter as both width and height (circle is square).
	if diam := n.preferredDiameter(); diam > 0 {
		publishLayout(n.Layout, x, y, diam, diam)
	} else {
		publishLayout(n.Layout, x, y, w, h)
	}

	// No child: pink error indicator.
	if n.Child == nil {
		dc.SetColor(errPink)
		dc.DrawCircle(x+w/2, y+h/2, math.Min(w, h)/2)
		dc.Fill()
		return
	}

	// The circle's radius is half the smaller dimension.
	rad := math.Min(w, h) / 2
	cx := x + w/2
	cy := y + h/2

	// The child gets the full face circle. The margin only exists for
	// external shadow padding — it doesn't shrink the visible face area.
	childX := cx - rad
	childY := cy - rad

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
		face := n.Face
		if face == (color.NRGBA{}) {
			face = n.Pal.Surface
		}
		NeuCircleWith(n.Pal, dc, n.Depth, cx, cy, rad, face, n.Params, nil)
	}

	n.Child.Draw(dc, childX, childY, 2*rad, 2*rad)
}
