package theme

import "mazzy/mazarin/mancini"

var _ mancini.NeumorphicParams = (*DefaultNeumorphicParams)(nil)

// DefaultNeumorphicParams implements [mancini.NeumorphicParams] with two
// parameter sets:
//
//   - Heavy — window-weight shadows with larger offsets and blur radii,
//     used by [std.AppWindow], [std.FreeFloatingWindow], and [std.RadialMenu].
//   - Light — control-weight shadows with smaller, tighter parameters,
//     used by [std.Button], [std.Checkbox], [std.Scrollbar],
//     [std.NOfMChooser], [std.RadialNOfMChooser], and [std.SingleLineText].
//
// Either Heavy() or Light() may be overridden to return nil in a custom
// implementation, disabling neumorphic rendering for that weight class.
type DefaultNeumorphicParams struct {
	heavy mancini.NeuParams
	light mancini.NeuParams
}

// Heavy returns the window-weight neumorphic shadow parameters.
func (d *DefaultNeumorphicParams) Heavy() *mancini.NeuParams { return &d.heavy }

// Light returns the control-weight neumorphic shadow parameters.
func (d *DefaultNeumorphicParams) Light() *mancini.NeuParams { return &d.light }

// NewDefaultNeumorphicParams returns the standard neumorphic shadow parameters.
func NewDefaultNeumorphicParams() *DefaultNeumorphicParams {
	return &DefaultNeumorphicParams{
		heavy: mancini.NeuParams{
			Raised: mancini.RaisedParams{
				LightOff:   4,
				LightBlur:  8,
				DarkOff:    14,
				DarkBlur:   14,
				DarkAlpha:  120,
				LightAlpha: 255,
			},
			Flush: mancini.FlushParams{
				EdgeW:     2,
				EdgeAlpha: 140,
			},
			Inset: mancini.InsetParams{
				Off:      3,
				DarkBlur: 7,
				LightBlur: 4,
			},
		},
		light: mancini.NeuParams{
			Raised: mancini.RaisedParams{
				LightOff:   1.5,
				LightBlur:  3,
				DarkOff:    4,
				DarkBlur:   4,
				DarkAlpha:  100,
				LightAlpha: 220,
			},
			Flush: mancini.FlushParams{
				EdgeW:     1.5,
				EdgeAlpha: 140,
			},
			Inset: mancini.InsetParams{
				Off:       1.5,
				DarkBlur:  3,
				LightBlur: 2,
			},
		},
	}
}
