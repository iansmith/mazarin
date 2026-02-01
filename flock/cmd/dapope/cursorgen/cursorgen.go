package cursorgen

import (
	"image"
	"image/color"
)

// GenerateArrowCursor returns a 32x32 RGBA image of a right-pointing arrow cursor.
// The hotspot is at pixel (31, 16) — the tip of the arrow.
func GenerateArrowCursor() *image.RGBA {
	// 32x32 right-pointing arrow bitmap.
	// '.' = transparent, 'W' = white (outline), 'B' = black (fill)
	rows := [32]string{
		"................................", // row 0
		"....W...........................", // row 1
		"....WW..........................", // row 2
		"....WBW.........................", // row 3
		"....WBBW........................", // row 4
		"....WBBBW.......................", // row 5
		"....WBBBBW......................", // row 6
		"....WBBBBBW.....................", // row 7
		"....WBBBBBBW....................", // row 8
		"....WBBBBBBBW...................", // row 9
		"....WBBBBBBBBW..................", // row 10
		"....WBBBBBBBBBW.................", // row 11
		"....WBBBBBBBBBBW................", // row 12
		"....WBBBBBBBBBBBW...............", // row 13
		"....WBBBBBBBBBBBBW..............", // row 14
		"....WBBBBBBBBBBBBBW.............", // row 15
		"....WBBBBBBBBBBBBBBWWWWWWWWWWWWW", // row 16 (tip at col 31)
		"....WBBBBBBBBBBBBBW.............", // row 17
		"....WBBBBBBBBBBBBW..............", // row 18
		"....WBBBBBBBBBBBW...............", // row 19
		"....WBBBBBBBBBBW................", // row 20
		"....WBBBBBBBBBW.................", // row 21
		"....WBBBBBBBBW..................", // row 22
		"....WBBBBBBBW...................", // row 23
		"....WBBBBBBW....................", // row 24
		"....WBBBBBW.....................", // row 25
		"....WBBBBW......................", // row 26
		"....WBBBW.......................", // row 27
		"....WBBW........................", // row 28
		"....WBW.........................", // row 29
		"....WW..........................", // row 30
		"....W...........................", // row 31
	}

	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	white := color.RGBA{255, 255, 255, 255}
	black := color.RGBA{0, 0, 0, 255}

	for y, row := range rows {
		for x, ch := range row {
			switch ch {
			case 'W':
				img.SetRGBA(x, y, white)
			case 'B':
				img.SetRGBA(x, y, black)
			}
		}
	}
	return img
}
