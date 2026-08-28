package hockeyplayer

import "fmt"

// tToMM maps a normalized translation value in [0,1] to a position in mm
// using the configured subrange [minMM, maxMM].
func tToMM(t, minMM, maxMM float64) float64 {
	return minMM + t*(maxMM-minMM)
}

// mmToT inverts tToMM. If the range is degenerate (min == max), returns 0.
func mmToT(mm, minMM, maxMM float64) float64 {
	if maxMM == minMM {
		return 0
	}
	return (mm - minMM) / (maxMM - minMM)
}

// applyTInvert returns t when invert is false, otherwise 1-t. Used at the
// boundary between user-facing and internal frames so the rest of the code
// can reason in a single frame.
func applyTInvert(t float64, invert bool) float64 {
	if invert {
		return 1.0 - t
	}
	return t
}

// gantryFrameMM converts a translation position between the configured frame
// (the one min/max_translation_mm were calibrated in, zero at the logical
// start of the rod) and the raw gantry frame. When invert is set, the gantry
// homes to the far end of travel, so its coordinates are mirrored across the
// axis: frame mm = length - gantry mm. The transform is its own inverse, so
// the same call converts in either direction. With invert unset it is the
// identity.
func gantryFrameMM(mm, axisLengthMM float64, invert bool) float64 {
	if invert {
		return axisLengthMM - mm
	}
	return mm
}

// buildPositions returns a copy of currentPositions with the value at axisIdx
// replaced by newMM. Used to construct the positions_mm vector for
// gantry.MoveToPosition while leaving non-target axes at their current values.
func buildPositions(currentPositions []float64, axisIdx int, newMM float64) ([]float64, error) {
	if axisIdx < 0 || axisIdx >= len(currentPositions) {
		return nil, fmt.Errorf("axis index %d out of range (gantry has %d axes)", axisIdx, len(currentPositions))
	}
	out := make([]float64, len(currentPositions))
	copy(out, currentPositions)
	out[axisIdx] = newMM
	return out, nil
}
