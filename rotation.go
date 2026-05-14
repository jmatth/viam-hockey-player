package hockeyplayer

import "math"

type rotationDirection int

const (
	directionShortest         rotationDirection = iota // take the shortest angular path
	directionClockwise                                 // always move in the positive (clockwise) direction
	directionCounterClockwise                          // always move in the negative (counter-clockwise) direction
)

// normalizeAngle converts a monotonic motor revolution count to a bounded
// player angle in [0, 360). The double-mod handles negative accumulated
// revolutions (the motor has been driven backward past its power-on zero).
func normalizeAngle(positionRev float64) float64 {
	return math.Mod(math.Mod(positionRev*360.0, 360.0)+360.0, 360.0)
}

// computeDelta returns the signed degrees the rotation axis should move to
// go from currentDeg to targetDeg.
//
// directionShortest         → pick the shortest angular path; may cross the 0/360 seam.
// directionClockwise        → always move in the positive (clockwise) direction.
// directionCounterClockwise → always move in the negative (counter-clockwise) direction.
func computeDelta(currentDeg, targetDeg float64, dir rotationDirection) float64 {
	deltaShort := math.Mod(targetDeg-currentDeg+540.0, 360.0) - 180.0
	switch dir {
	case directionClockwise:
		if deltaShort < 0 {
			return deltaShort + 360.0
		}
		return deltaShort
	case directionCounterClockwise:
		if deltaShort > 0 {
			return deltaShort - 360.0
		}
		return deltaShort
	default: // directionShortest
		return deltaShort
	}
}
