package sys

// Itoa formats a signed int64 in base 10. Hand-rolled because .maz plugins
// can't rely on strconv's init-time setup (init doesn't run in plugin
// modules), and the standard formatter pulls in fmt machinery that bloats
// plugin binaries.
func Itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// Hex64 formats a uint64 as a 16-char zero-padded lowercase hex string.
// Fixed width keeps log lines aligned for easy scanning.
func Hex64(v uint64) string {
	const digits = "0123456789abcdef"
	var buf [16]byte
	for i := 15; i >= 0; i-- {
		buf[i] = digits[v&0xF]
		v >>= 4
	}
	return string(buf[:])
}
