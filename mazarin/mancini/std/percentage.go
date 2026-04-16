package std

// normalizePercents adjusts a percentage slice so it sums to exactly 100.
//
//   - A value of -1 means "absorb whatever is left." At most one -1 is
//     allowed. The -1 entry receives 100 minus the sum of the other
//     entries.
//   - If the sum exceeds 100, all values are proportionally reduced. Any
//     value that reaches zero (< 0.001) is discarded and the remaining
//     values are re-normalized until the sum is 100 with no zero entries.
//   - If the sum is less than 100, the remainder is added to the last entry.
//   - An empty input returns an empty slice.
func normalizePercents(pcts []float64) []float64 {
	if len(pcts) == 0 {
		return nil
	}

	// Copy so we don't mutate the caller's slice.
	out := make([]float64, len(pcts))
	copy(out, pcts)

	// Handle -1 ("absorb remaining") entries.
	flexIdx := -1
	for i, v := range out {
		if v < 0 {
			flexIdx = i
			break
		}
	}
	if flexIdx >= 0 {
		sum := 0.0
		for i, v := range out {
			if i != flexIdx {
				sum += v
			}
		}
		remainder := 100.0 - sum
		if remainder < 0 {
			remainder = 0
		}
		out[flexIdx] = remainder
		return out
	}

	for {
		sum := 0.0
		for _, v := range out {
			sum += v
		}
		if sum < 0.001 {
			return nil
		}

		// Close enough to 100 — done.
		if sum >= 99.999 && sum <= 100.001 {
			return out
		}

		if sum < 100.0 {
			// Under 100: add remainder to last entry.
			out[len(out)-1] += 100.0 - sum
			return out
		}

		// Over 100: scale down proportionally.
		scale := 100.0 / sum
		pruned := false
		for i := range out {
			out[i] *= scale
			if out[i] < 0.001 {
				out[i] = 0
				pruned = true
			}
		}

		if !pruned {
			return out
		}

		// Remove zero entries and re-normalize.
		filtered := out[:0]
		for _, v := range out {
			if v >= 0.001 {
				filtered = append(filtered, v)
			}
		}
		if len(filtered) == 0 {
			return nil
		}
		out = filtered
	}
}

// assignPercents maps a normalized percentage slice to a child count.
//
//   - If there are more children than percentages, the returned slice has
//     len(pcts) entries — extra children should be hidden by the caller.
//   - If there are fewer children than percentages, the remaining
//     percentages are summed into the last child's percentage.
func assignPercents(pcts []float64, nChildren int) []float64 {
	if nChildren <= 0 || len(pcts) == 0 {
		return nil
	}

	if nChildren >= len(pcts) {
		// More children than percentages — return pcts as-is.
		// The caller hides children beyond len(pcts).
		return pcts
	}

	// Fewer children than percentages — collapse tail into last child.
	out := make([]float64, nChildren)
	copy(out, pcts[:nChildren-1])
	tail := 0.0
	for i := nChildren - 1; i < len(pcts); i++ {
		tail += pcts[i]
	}
	out[nChildren-1] = tail
	return out
}
