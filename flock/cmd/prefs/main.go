// prefs is a .maz module loaded by rachel that publishes user preferences
// (display density, palette colors) as constraint attributes.
//
// Since prefs runs as a goroutine inside rachel's address space, all attributes
// are published under rachel's SID namespace.
package main

import (
	"image/color"
	"mazzy/mazarin/attr"
	"mazzy/mazarin/mancini"
	"mazzy/mazarin/sys"
)

// MazEntryPoint holds a reference to MazarinMain to prevent DCE.
var MazEntryPoint func() = MazarinMain

func init() {
	if MazEntryPoint == nil {
		panic("unreachable")
	}
}

// MazarinMain is the entry point called by the host shepherd when this .maz is loaded.
//
//go:noinline
func MazarinMain() {
	rawPuts("[prefs] starting\n")

	attr.Init()
	sid := attr.SID()
	pal := mancini.DefaultPalette()

	// Publish display density (1.0 = no scaling)
	attr.ValueF64("attr:///shepherd/"+sid+"/f64/prefs/displayDensity", 1.0)

	// Publish palette colors as packed int64 values (R<<24 | G<<16 | B<<8 | A)
	publishColor(sid, "Surface", pal.Surface)
	publishColor(sid, "SurfaceTint", pal.SurfaceTint)
	publishColor(sid, "DarkSh", pal.DarkSh)
	publishColor(sid, "LightSh", pal.LightSh)
	publishColor(sid, "Text", pal.Text)
	publishColor(sid, "Icon", pal.Icon)

	rawPuts("[prefs] attributes published, setting ready\n")
	sys.SetReady(true)

	// Block forever — prefs stays alive for attribute reads.
	select {}
}

// publishColor publishes a single palette color as a packed int64 attribute.
func publishColor(sid, name string, c color.NRGBA) {
	packed := int64(c.R)<<24 | int64(c.G)<<16 | int64(c.B)<<8 | int64(c.A)
	attr.ValueI64("attr:///shepherd/"+sid+"/i64/prefs/palette/"+name, packed)
}

// rawPuts writes a string to stdout one byte at a time.
// In .maz modules, fmt.Print and os.Stdout are not available.
func rawPuts(s string) {
	for i := 0; i < len(s); i++ {
		sys.RawWrite(1, s[i])
	}
}

func main() {
	MazarinMain()
}
