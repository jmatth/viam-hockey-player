package hockeyplayer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTToMM(t *testing.T) {
	assert.Equal(t, 10.0, tToMM(0, 10, 100))
	assert.Equal(t, 100.0, tToMM(1, 10, 100))
	assert.Equal(t, 55.0, tToMM(0.5, 10, 100))
}

func TestMMToT(t *testing.T) {
	assert.InDelta(t, 0.0, mmToT(10, 10, 100), 1e-9)
	assert.InDelta(t, 1.0, mmToT(100, 10, 100), 1e-9)
	assert.InDelta(t, 0.5, mmToT(55, 10, 100), 1e-9)
	assert.Equal(t, 0.0, mmToT(50, 50, 50), "degenerate range returns 0")
}

func TestApplyTInvert(t *testing.T) {
	assert.Equal(t, 0.3, applyTInvert(0.3, false), "invert=false is identity")
	assert.InDelta(t, 0.7, applyTInvert(0.3, true), 1e-9)
	assert.InDelta(t, 1.0, applyTInvert(0.0, true), 1e-9)
	assert.InDelta(t, 0.0, applyTInvert(1.0, true), 1e-9)
}

func TestGantryFrameMM(t *testing.T) {
	assert.Equal(t, 30.0, gantryFrameMM(30, 120, false), "identity when not inverted")
	assert.Equal(t, 90.0, gantryFrameMM(30, 120, true), "mirrored across the axis length")
	assert.Equal(t, 30.0, gantryFrameMM(gantryFrameMM(30, 120, true), 120, true), "self-inverse")
}

func TestBuildPositions_Valid(t *testing.T) {
	current := []float64{10, 20, 30}
	got, err := buildPositions(current, 1, 99)
	require.NoError(t, err)
	assert.Equal(t, []float64{10, 99, 30}, got)
	assert.Equal(t, []float64{10, 20, 30}, current, "input unchanged")
}

func TestBuildPositions_OutOfRange(t *testing.T) {
	_, err := buildPositions([]float64{1, 2}, 5, 99)
	require.Error(t, err)
	_, err = buildPositions([]float64{1, 2}, -1, 99)
	require.Error(t, err)
}
