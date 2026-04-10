// Package font defines constants for font family names and style variants.
// These match the entries in /fonts/fonts.csv on the FAT32 disk.
package font

// Font family names.
const (
	Ahem                    = "Ahem"
	AtkinsonHyperlegible    = "AtkinsonHyperlegible"
	AtkinsonHyperlegibleMono = "AtkinsonHyperlegibleMono"
	LiberationMono          = "LiberationMono"
	LiberationSans          = "LiberationSans"
	LiberationSerif         = "LiberationSerif"
	NotoSerif               = "NotoSerif"
	NotoSerifCondensed      = "NotoSerifCondensed"
	NotoSerifExtraCondensed = "NotoSerifExtraCondensed"
	NotoSerifSemiCondensed  = "NotoSerifSemiCondensed"
	CrimsonPro              = "CrimsonPro"
	LatinModernRoman        = "LatinModernRoman"
)

// Default font families by category.
const (
	DefaultMono  = AtkinsonHyperlegibleMono
	DefaultSans  = AtkinsonHyperlegible
	DefaultSerif = LiberationSerif
)

// Style variants.
const (
	Regular          = "Regular"
	Bold             = "Bold"
	Italic           = "Italic"
	BoldItalic       = "BoldItalic"
	Light            = "Light"
	LightItalic      = "LightItalic"
	Medium           = "Medium"
	MediumItalic     = "MediumItalic"
	SemiBold         = "SemiBold"
	SemiBoldItalic   = "SemiBoldItalic"
	ExtraBold        = "ExtraBold"
	ExtraBoldItalic  = "ExtraBoldItalic"
	ExtraLight       = "ExtraLight"
	ExtraLightItalic = "ExtraLightItalic"
	Thin             = "Thin"
	ThinItalic       = "ThinItalic"
	Black            = "Black"
	BlackItalic      = "BlackItalic"
)
