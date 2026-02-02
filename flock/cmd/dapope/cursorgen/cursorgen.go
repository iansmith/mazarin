package cursorgen

import (
	"image"
	"image/color"
)

// GenerateArrowCursor returns a 32x32 RGBA image of a right-pointing arrow cursor.
// The hotspot is at pixel (31, 16) — the tip of the arrow.
func GenerateArrowCursor() *image.RGBA {
	// 32x32 right-pointing arrow bitmap.
	// '.' = transparent, 'W' = white (outline), 'B' = black (fill), 'R' = red (hotspot accent)
	rows := [32]string{
		"................................", // row 0
		"..W.............................", // row 1
		"..WW............................", // row 2
		"..WBBW..........................", // row 3  (2 B)
		"..WBBBBW........................", // row 4  (4 B)
		"..WBBBBBBW......................", // row 5  (6 B)
		"..WBBBBBBBBW....................", // row 6  (8 B)
		"..WBBBBBBBBBBW..................", // row 7  (10 B)
		"..WBBBBBBBBBBBBW................", // row 8  (12 B)
		"..WBBBBBBBBBBBBBBW..............", // row 9  (14 B)
		"..WBBBBBBBBBBBBBBBBW............", // row 10 (16 B)
		"..WBBBBBBBBBBBBBBBBBBW..........", // row 11 (18 B)
		"..WBBBBBBBBBBBBBBBBBBBBW........", // row 12 (20 B)
		"..WBBBBBBBBBBBBBBBBBBBBBBRW.....", // row 13 (22 B + 1 R)
		"..WBBBBBBBBBBBBBBBBBBBBBBRRRRW..", // row 14 (22 B + 4 R)
		"..WBBBBBBBBBBBBBBBBBBBBBBBRRRRW.", // row 15 (23 B + 4 R)
		"..WBBBBBBBBBBBBBBBBBBBBBBBBRRRRW", // row 16 (24 B + 4 R, tip)
		"..WBBBBBBBBBBBBBBBBBBBBBBBRRRRW.", // row 17 (23 B + 4 R)
		"..WBBBBBBBBBBBBBBBBBBBBBBRRRRW..", // row 18 (22 B + 4 R)
		"..WBBBBBBBBBBBBBBBBBBBBBBRW.....", // row 19 (22 B + 1 R)
		"..WBBBBBBBBBBBBBBBBBBBBW........", // row 20 (20 B)
		"..WBBBBBBBBBBBBBBBBBBW..........", // row 21 (18 B)
		"..WBBBBBBBBBBBBBBBBW............", // row 22 (16 B)
		"..WBBBBBBBBBBBBBBW..............", // row 23 (14 B)
		"..WBBBBBBBBBBBBW................", // row 24 (12 B)
		"..WBBBBBBBBBBW..................", // row 25 (10 B)
		"..WBBBBBBBBW....................", // row 26 (8 B)
		"..WBBBBBBW......................", // row 27 (6 B)
		"..WBBBBW........................", // row 28 (4 B)
		"..WBBW..........................", // row 29 (2 B)
		"..WW............................", // row 30
		"..W.............................", // row 31
	}

	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	white := color.RGBA{255, 255, 255, 255}
	black := color.RGBA{0, 0, 0, 255}
	red := color.RGBA{220, 40, 40, 255}

	for y, row := range rows {
		for x, ch := range row {
			switch ch {
			case 'W':
				img.SetRGBA(x, y, white)
			case 'B':
				img.SetRGBA(x, y, black)
			case 'R':
				img.SetRGBA(x, y, red)
			}
		}
	}
	return img
}
