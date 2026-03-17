package main

import (
	_ "embed"
	"fmt"

	"mazzy/mazarin/attr"
	"mazzy/mazarin/interactor"
	"mazzy/mazarin/sys"
	"mazzy/mazarin/vm"
)

//go:embed AtkinsonHyperlegibleMono-Regular.otf
var fontData []byte

const (
	// Right half of 1728×1117 display.
	regionX = 864
	regionY = 0
	regionW = 864
	regionH = 1117
)

func main() {
	sys.UartWriteString("[uitest] main() entered\n")

	// 1. Initialize constraint system and interactor library.
	attr.Init()
	interactor.Init("uitest")
	sys.UartWriteString("[uitest] attr + interactor init done\n")

	// 2. Create interactor tree: window → card → label.
	window := interactor.NewWindow("win", 400, 200, 0xFF2D2D2D)
	// Position window in the center of our region.
	window.OriginPoint.Set(vm.Point2DVal(
		int64(regionX)+(int64(regionW)-400)/2,
		int64(regionY)+(int64(regionH)-200)/2,
	))

	card := interactor.NewCard("card", window, 16, 0xFF3C3C3C)
	label := interactor.NewLabel("clock", card, 0xFFFFFFFF)

	// 3. Label content: Go goroutine formats HH:MM:SS from kernel time.
	label.SetContentValue("00:00:00")

	// Set up damage tracking chain: card→label, window→card.
	card.FinalizeDamage()
	window.FinalizeDamage()

	// 4. Create a deref handle for kernel time.
	timeProg := interactor.BindStrings(interactor.ProgIdentityI64,
		"attr:///kernel/int64/time/utc_seconds")
	timeSec := attr.ConstraintI64("attr:///priest/uitest/int64/time_sec", timeProg)
	timeSec.SetEager(true)

	// Mark the window's damageRect as eager for draw-loop wakeup.
	window.DamageRect.SetEager(true)

	// 5. Create draw context with screen region.
	dc := interactor.NewDrawContext(fontData, regionX, regionY, regionW, regionH)
	sys.UartWriteString("[uitest] draw context created\n")

	// 6. Initial full draw.
	fullClip := [4]int32{
		int32(regionX), int32(regionY),
		int32(regionX + regionW), int32(regionY + regionH),
	}
	dc.DrawTree(window, fullClip)
	dc.FlushRegion()
	sys.UartWriteString("[uitest] initial draw done, entering loop\n")

	// 7. Single WaitDirty loop — update clock AND redraw.
	// Only one goroutine can consume WaitDirty notifications per priest.
	for {
		attr.WaitDirty()

		// Update clock content from kernel time.
		// Kernel change-gates string writes: if the formatted string is
		// identical to the current value, no dirty propagation occurs.
		sec := timeSec.Get()
		h, m, s := (sec/3600)%24, (sec/60)%60, sec%60
		label.Content.Set(fmt.Sprintf("%02d:%02d:%02d", h, m, s))

		// Check damage and redraw.
		dmgVal := window.DamageRect.Get()
		if interactor.RectIsEmpty(dmgVal) {
			continue
		}
		x0, y0, x1, y1 := dmgVal.AsRectangle()

		dc.DrawTree(window, [4]int32{x0, y0, x1, y1})
		dc.Flush(x0, y0, x1, y1)
	}
}
