package textshape

import "testing"

// TestSFNTVerticalMetrics_SkiaFreeTypeChoice pins how a scalable font's
// ascent, descent and leading are chosen, as Chrome on Linux chooses them:
// the OS/2 sTypo metrics only when the OS/2 table's version is not 0xFFFF
// and fsSelection bit 7 (USE_TYPO_METRICS) is set; otherwise the hhea
// metrics, with leading the hhea line gap, never negative.
// (SkScalerContext_FreeType::generateFontMetrics,
// src/ports/SkFontHost_FreeType.cpp:1597-1611, :1678-1681 @ skia
// d19e557ac3175dc4f9e52d86dd1476c4dfe06a3e.)
func TestSFNTVerticalMetrics_SkiaFreeTypeChoice(t *testing.T) {
	// Liberation Serif's tables: hhea 1825/-443/87, sTypo 1420/-442/307.
	liberation := sfntVerticalMetrics{
		hasOS2: true, os2Version: 4,
		typoAscender: 1420, typoDescender: -442, typoLineGap: 307,
		hheaAscender: 1825, hheaDescender: -443, hheaLineGap: 87,
	}
	withBit7 := liberation
	withBit7.fsSelection = 1 << 7
	bit7NoValidOS2 := withBit7
	bit7NoValidOS2.os2Version = 0xFFFF
	noOS2 := liberation
	noOS2.hasOS2 = false
	negativeGap := liberation
	negativeGap.hheaLineGap = -10

	for _, tc := range []struct {
		name                         string
		m                            sfntVerticalMetrics
		ascender, descender, leading float32
	}{
		{"no USE_TYPO_METRICS: hhea", liberation, 1825, -443, 87},
		{"USE_TYPO_METRICS: sTypo", withBit7, 1420, -442, 307},
		{"bit 7 on an invalid OS/2 table: hhea", bit7NoValidOS2, 1825, -443, 87},
		{"no OS/2 table: hhea", noOS2, 1825, -443, 87},
		{"negative line gap clamps to 0", negativeGap, 1825, -443, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asc, desc, leading := tc.m.ascenderDescenderLeading()
			if asc != tc.ascender || desc != tc.descender || leading != tc.leading {
				t.Errorf("got %v/%v/%v, want %v/%v/%v", asc, desc, leading, tc.ascender, tc.descender, tc.leading)
			}
		})
	}
}
