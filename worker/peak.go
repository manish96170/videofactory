package worker

import "math"

// normalizePeak is shared so both OS semantic units can be tested on any host.
func normalizePeak(value int64, kibibytes bool) (int64, bool) {
	if value < 0 {
		return 0, false
	}
	if kibibytes {
		if value > math.MaxInt64/1024 {
			return 0, false
		}
		value *= 1024
	}
	return value, true
}
