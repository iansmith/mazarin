//go:build !linux

package mancini

// LayoutHandles is a stub for non-linux builds.
type LayoutHandles struct{}

// Init is a no-op on non-linux.
func Init(name string) {}

func setVisibleHandle(lh *LayoutHandles, v int64)       {}
func (lh *LayoutHandles) boundsHashValue() int64        { return 0 }
func (lh *LayoutHandles) GetSpacing() float64           { return 0 }
func setLayoutSpacing(lh *LayoutHandles, v float64)     {}

func publishLayout(l *LayoutHandles, x, y, w, h float64) {}

func (w *AppWindow) InitLayout(parent string)             {}
func (w *FreeFloatingWindow) InitLayout(parent string)    {}
func (b *Button) InitLayout(parent string)                {}
func (l *NeuLabel) InitLayout(parent string)              {}
func (l *Label) InitLayout(parent string)                 {}
func (s *Spacer) InitLayout(parent string)                {}
func (tb *AppTitleBar) InitLayout(parent string)          {}