package textshape

import (
	"fmt"

	goFont "github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/font/opentype"
	"github.com/go-text/typesetting/harfbuzz"
	"github.com/go-text/typesetting/language"
)

// HarfBuzzShaper performs text shaping using go-text/typesetting's
// HarfBuzz implementation.
type HarfBuzzShaper struct {
	fonts [maxFonts]*harfbuzz.Font
}

const maxFonts = 32

// NewHarfBuzzShaper creates a new HarfBuzz-based shaper.
func NewHarfBuzzShaper() *HarfBuzzShaper {
	return &HarfBuzzShaper{}
}

// RegisterFont registers a parsed font face with the shaper at the given
// fontID and point size. The font scale is set to size*64 so that
// shaping output is in fixed.Int26_6 units.
func (s *HarfBuzzShaper) RegisterFont(fontID int32, face *goFont.Face, size int32) {
	if fontID < 0 || fontID >= maxFonts {
		return
	}
	hbFont := harfbuzz.NewFont(face)
	scale := int32(size) * 64
	hbFont.XScale = scale
	hbFont.YScale = scale
	s.fonts[fontID] = hbFont
}

// Shape performs text shaping using HarfBuzz.
func (s *HarfBuzzShaper) Shape(params ShapingParams) (ShapedRun, error) {
	if params.FontID < 0 || params.FontID >= maxFonts || s.fonts[params.FontID] == nil {
		return ShapedRun{}, fmt.Errorf("font %d not registered with shaper", params.FontID)
	}

	hbFont := s.fonts[params.FontID]

	buf := harfbuzz.NewBuffer()
	runes := []rune(params.Text)
	buf.AddRunes(runes, 0, len(runes))

	// Set segment properties. Explicit values override; ScriptCommon (zero)
	// and zero Direction signal "auto-detect via GuessSegmentProperties".
	if params.Direction != 0 {
		buf.Props.Direction = toHBDirection(params.Direction)
	}
	if params.Script != ScriptCommon {
		buf.Props.Script = language.Script(params.Script)
	}
	if params.Language != "" {
		buf.Props.Language = language.NewLanguage(params.Language)
	}
	buf.GuessSegmentProperties()

	// Convert FontFeature slice to harfbuzz.Feature slice.
	var hbFeatures []harfbuzz.Feature
	for _, f := range params.Features {
		tag := opentype.NewTag(f.Tag[0], f.Tag[1], f.Tag[2], f.Tag[3])
		hbFeatures = append(hbFeatures, harfbuzz.Feature{
			Tag:   tag,
			Value: f.Value,
			Start: harfbuzz.FeatureGlobalStart,
			End:   harfbuzz.FeatureGlobalEnd,
		})
	}

	// Shape with requested features (nil when no features specified).
	buf.Shape(hbFont, hbFeatures)

	// Convert output.
	glyphs := make([]ShapedGlyph, len(buf.Info))
	for i, info := range buf.Info {
		pos := buf.Pos[i]
		glyphs[i] = ShapedGlyph{
			GID:      uint32(info.Glyph),
			Cluster:  uint32(info.Cluster),
			XAdvance: pos.XAdvance,
			YAdvance: pos.YAdvance,
			XOffset:  pos.XOffset,
			YOffset:  pos.YOffset,
		}
	}

	return ShapedRun{
		FontID:    params.FontID,
		Direction: params.Direction,
		Glyphs:   glyphs,
	}, nil
}

func toHBDirection(d Direction) harfbuzz.Direction {
	switch d {
	case RTL:
		return harfbuzz.RightToLeft
	case TTB:
		return harfbuzz.TopToBottom
	case BTT:
		return harfbuzz.BottomToTop
	default:
		return harfbuzz.LeftToRight
	}
}
