package hockeyplayer

import "math"

// normalizeAngle converts a monotonic motor revolution count to a bounded
// player angle in [0, 360). The double-mod handles negative accumulated
// revolutions (the motor has been driven backward past its power-on zero).
func normalizeAngle(positionRev float64) float64 {
	return math.Mod(math.Mod(positionRev*360.0, 360.0)+360.0, 360.0)
}

// computeDelta returns the signed degrees the rotation axis should move to
// go from currentDeg to targetDeg.
//
// wrap = true  → pick the shortest path; may cross the 0/360 seam.
// wrap = false → never cross the 0/360 seam; if the shortest path would cross
// it, take the long way around instead.
func computeDelta(currentDeg, targetDeg float64, wrap bool) float64 {
	deltaShort := math.Mod(targetDeg-currentDeg+540.0, 360.0) - 180.0
	if wrap {
		return deltaShort
	}
	end := currentDeg + deltaShort
	if end < 0 || end > 360 {
		if deltaShort > 0 {
			return deltaShort - 360
		}
		return deltaShort + 360
	}
	return deltaShort
}
