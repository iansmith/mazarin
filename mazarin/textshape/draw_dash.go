package textshape

import (
	"math"
	"sort"
)

// This file ports Skia's path dashing: SkDashPath::ValidDashPath and
// SkDashPath::InternalFilter (src/utils/SkDashPath.cpp:309-458 @ skia
// d19e557ac3175dc4f9e52d86dd1476c4dfe06a3e, Chromium a9f50e52's
// skia_revision), with SkContourMeasure::getSegment
// (src/core/SkContourMeasure.cpp, same SHA) cutting each dash out of the
// contour. The phase is always 0: DrawContext.SetDash takes no offset.
//
// Dashing runs on strokeExpand's flattened contours, which are in device
// space, so the intervals are device lengths — like lineWidth.

// validDashIntervals is SkDashPath::ValidDashPath for phase 0: an even,
// non-empty count of non-negative intervals with a positive sum. Any other
// pattern strokes solid.
func validDashIntervals(intervals []float64) bool {
	if len(intervals) < 2 || len(intervals)%2 != 0 {
		return false
	}
	sum := 0.0
	for _, v := range intervals {
		if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
		sum += v
	}
	return sum > 0
}

// dashLines replaces strokeExpand's flattened contours (lines, with
// nextLine linking each line to the next in its contour: -1 ends an open
// contour, the contour's first index closes it) by their dashes, per
// InternalFilter. Each contour restarts the pattern. An "on" interval is
// emitted while it starts before the contour's end, the last one cut at the
// end; a zero-length one becomes a zero-length line, which strokeExpand
// caps as a dot. On a closed contour the first dash is skipped and appended
// after the last, joined to it when the last dash ran to the end.
func dashLines(lines [][2][2]float64, nextLine []int, intervals []float64) ([][2][2]float64, []int) {
	outLines := make([][2][2]float64, 0, len(lines))
	outNext := make([]int, 0, len(lines))
	var cum []float64

	// endPiece ends the open dash at the last line added.
	endPiece := func() {
		if n := len(outNext); n > 0 {
			outNext[n-1] = -1
		}
	}
	addLine := func(a, b [2]float64) {
		outLines = append(outLines, [2][2]float64{a, b})
		outNext = append(outNext, len(outLines)) // linked to the next line in the piece
	}

	for start := 0; start < len(lines); {
		end := start // last line of this contour
		for nextLine[end] == end+1 {
			end++
		}
		closed := nextLine[end] == start
		contour := lines[start : end+1]

		// cum[i] is the distance along the contour to contour[i]'s start.
		cum = append(cum[:0], 0)
		for i, l := range contour {
			cum = append(cum, cum[i]+math.Hypot(l[1][0]-l[0][0], l[1][1]-l[0][1]))
		}
		length := cum[len(contour)]

		// lineAt is the index of the line that d falls in, the later one
		// at a vertex, so a dash starting there does not open with a
		// zero-length line (which would get no cap).
		lineAt := func(d float64) int {
			return min(sort.Search(len(contour), func(k int) bool { return cum[k+1] > d }), len(contour)-1)
		}
		pointAt := func(i int, d float64) [2]float64 {
			l := contour[i]
			t := 0.0
			if seg := cum[i+1] - cum[i]; seg > 0 {
				t = (d - cum[i]) / seg
			}
			return [2]float64{l[0][0] + t*(l[1][0]-l[0][0]), l[0][1] + t*(l[1][1]-l[0][1])}
		}

		// getSegment is SkContourMeasure::getSegment: the contour from s
		// to e (e clamped to its length), starting a new piece when
		// startWithMoveTo, else continuing the open one.
		getSegment := func(s, e float64, startWithMoveTo bool) {
			e = math.Min(e, length)
			if startWithMoveTo {
				endPiece()
			}
			i := lineAt(s)
			p := pointAt(i, s)
			if s == e {
				addLine(p, p) // a zero-length dash: capped as a dot
				return
			}
			for ; i < len(contour)-1 && cum[i+1] < e; i++ {
				q := contour[i][1]
				addLine(p, q)
				p = q
			}
			addLine(p, pointAt(i, e))
		}

		skipFirstSegment := closed
		addedSegment := false
		distance, index := 0.0, 0
		for distance < length {
			addedSegment = index%2 == 0 && !skipFirstSegment
			if addedSegment {
				getSegment(distance, distance+intervals[index], true)
			}
			distance += intervals[index]
			skipFirstSegment = false
			index = (index + 1) % len(intervals)
		}
		if closed {
			getSegment(0, intervals[0], !addedSegment)
		}
		endPiece()
		start = end + 1
	}
	return outLines, outNext
}
