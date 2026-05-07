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
