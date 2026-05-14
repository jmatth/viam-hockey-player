package hockeyplayer

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeAngle(t *testing.T) {
	cases := []struct {
		posRev float64
		want   float64
	}{
		{0, 0},
		{0.25, 90},
		{0.972222, 350}, // 350/360
		{1.25, 90},      // 1 full rev + 90
		{-0.1, 324},     // negative: should wrap positive
		{-2.7, 108},     // -972 deg → 108
	}
	for _, tc := range cases {
		got := normalizeAngle(tc.posRev)
		assert.InDelta(t, tc.want, got, 0.01, "normalizeAngle(%v)", tc.posRev)
	}
}

func TestComputeDelta_Shortest(t *testing.T) {
	cases := []struct {
		current, target, want float64
	}{
		{350, 10, 20},
		{10, 350, -20},
		{100, 150, 50},
		{150, 100, -50},
		{0, 0, 0},
	}
	for _, tc := range cases {
		got := computeDelta(tc.current, tc.target, directionShortest)
		assert.InDelta(t, tc.want, got, 1e-9, "computeDelta(%v→%v, shortest)", tc.current, tc.target)
	}
}

func TestComputeDelta_Clockwise(t *testing.T) {
	cases := []struct {
		current, target, want float64
	}{
		{350, 10, 20},   // shortest is already CW (+20), keep it
		{10, 350, 340},  // shortest is CCW (-20), flip to CW (+340)
		{100, 150, 50},  // short path is CW, keep it
		{150, 100, 310}, // shortest is CCW (-50), flip to CW (+310)
		{0, 0, 0},
	}
	for _, tc := range cases {
		got := computeDelta(tc.current, tc.target, directionClockwise)
		assert.InDelta(t, tc.want, got, 1e-9, "computeDelta(%v→%v, clockwise)", tc.current, tc.target)
	}
}

func TestComputeDelta_CounterClockwise(t *testing.T) {
	cases := []struct {
		current, target, want float64
	}{
		{350, 10, -340}, // shortest is CW (+20), flip to CCW (-340)
		{10, 350, -20},  // shortest is already CCW (-20), keep it
		{100, 150, -310}, // shortest is CW (+50), flip to CCW (-310)
		{150, 100, -50}, // shortest is already CCW (-50), keep it
		{0, 0, 0},
	}
	for _, tc := range cases {
		got := computeDelta(tc.current, tc.target, directionCounterClockwise)
		assert.InDelta(t, tc.want, got, 1e-9, "computeDelta(%v→%v, counter-clockwise)", tc.current, tc.target)
	}
}

func TestComputeDelta_Symmetry(t *testing.T) {
	cases := []float64{0, 90, 180, 270, 359.99}
	for _, v := range cases {
		assert.InDelta(t, 0.0, computeDelta(v, v, directionShortest), 1e-9)
		assert.InDelta(t, 0.0, computeDelta(v, v, directionClockwise), 1e-9)
		assert.InDelta(t, 0.0, computeDelta(v, v, directionCounterClockwise), 1e-9)
	}
	d := computeDelta(0, 360, directionShortest)
	assert.InDelta(t, 0.0, math.Mod(d+360, 360), 0.01)
}

// At exactly 180° separation both directions are equally shortest.
// The formula may return either +180 or -180; both are physically equivalent.
func TestComputeDelta_Exactly180(t *testing.T) {
	got := computeDelta(0, 180, directionShortest)
	assert.InDelta(t, 180.0, math.Abs(got), 1e-9)
}
