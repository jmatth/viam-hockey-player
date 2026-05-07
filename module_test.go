package hockeyplayer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	"go.viam.com/rdk/components/gantry"
	"go.viam.com/rdk/components/motor"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/testutils/inject"
)

func makeDeps(t *testing.T, gName, mName string, g *inject.Gantry, m *inject.Motor) resource.Dependencies {
	t.Helper()
	deps := resource.Dependencies{}
	deps[gantry.Named(gName)] = g
	deps[motor.Named(mName)] = m
	return deps
}

func makeDepsWithTranslation(t *testing.T, gName, mRotName, mTransName string, g *inject.Gantry, mRot, mTrans *inject.Motor) resource.Dependencies {
	t.Helper()
	deps := resource.Dependencies{}
	deps[gantry.Named(gName)] = g
	deps[motor.Named(mRotName)] = mRot
	deps[motor.Named(mTransName)] = mTrans
	return deps
}

func baseCfg() *Config {
	return &Config{
		Gantry:               "g",
		RotationMotor:        "m",
		TranslationMotor:     "m_trans",
		TranslationAxisIndex: 0,
		MinTranslationMM:     10,
		MaxTranslationMM:     100,
		DefaultRPMRotation:   60,
		DefaultSpeedMMPerSec: 50,
	}
}

func makeFakeGantry(t *testing.T) *inject.Gantry {
	t.Helper()
	g := &inject.Gantry{}
	g.LengthsFunc = func(ctx context.Context, extra map[string]interface{}) ([]float64, error) {
		return []float64{200}, nil
	}
	g.StopFunc = func(ctx context.Context, extra map[string]interface{}) error { return nil }
	return g
}

func makeFakeMotor(t *testing.T, positionReporting bool) *inject.Motor {
	t.Helper()
	m := &inject.Motor{}
	m.PropertiesFunc = func(ctx context.Context, extra map[string]interface{}) (motor.Properties, error) {
		return motor.Properties{PositionReporting: positionReporting}, nil
	}
	m.StopFunc = func(ctx context.Context, extra map[string]interface{}) error { return nil }
	return m
}

func TestNewHockeyPlayer_HappyPath(t *testing.T) {
	defer goleak.VerifyNone(t, goleakOpts...)
	logger := logging.NewTestLogger(t)
	g := makeFakeGantry(t)
	m := makeFakeMotor(t, true)
	mt := makeFakeMotor(t, true)

	deps := makeDepsWithTranslation(t, "g", "m", "m_trans", g, m, mt)
	r, err := NewHockeyPlayer(context.Background(), deps, resource.Name{Name: "hp"}, baseCfg(), logger)
	require.NoError(t, err)
	require.NoError(t, r.Close(context.Background()))
}

func TestNewHockeyPlayer_RejectsMaxBeyondGantryLength(t *testing.T) {
	logger := logging.NewTestLogger(t)
	g := &inject.Gantry{}
	m := makeFakeMotor(t, true)
	mt := makeFakeMotor(t, true)
	g.LengthsFunc = func(ctx context.Context, extra map[string]interface{}) ([]float64, error) {
		return []float64{50}, nil
	}
	g.StopFunc = func(ctx context.Context, extra map[string]interface{}) error { return nil }
	cfg := baseCfg()
	cfg.MaxTranslationMM = 999
	deps := makeDepsWithTranslation(t, "g", "m", "m_trans", g, m, mt)
	_, err := NewHockeyPlayer(context.Background(), deps, resource.Name{Name: "hp"}, cfg, logger)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds gantry axis")
}

func TestNewHockeyPlayer_RejectsAxisIndexOutOfRange(t *testing.T) {
	logger := logging.NewTestLogger(t)
	g := makeFakeGantry(t)
	m := makeFakeMotor(t, true)
	mt := makeFakeMotor(t, true)
	cfg := baseCfg()
	cfg.TranslationAxisIndex = 5
	deps := makeDepsWithTranslation(t, "g", "m", "m_trans", g, m, mt)
	_, err := NewHockeyPlayer(context.Background(), deps, resource.Name{Name: "hp"}, cfg, logger)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "translation_axis_index")
}

func TestNewHockeyPlayer_RejectsNoPositionReporting(t *testing.T) {
	logger := logging.NewTestLogger(t)
	g := makeFakeGantry(t)
	m := makeFakeMotor(t, false /* no position reporting */)
	mt := makeFakeMotor(t, true)
	deps := makeDepsWithTranslation(t, "g", "m", "m_trans", g, m, mt)
	_, err := NewHockeyPlayer(context.Background(), deps, resource.Name{Name: "hp"}, baseCfg(), logger)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "position reporting")
}

func TestNewHockeyPlayer_GantryLengthsError(t *testing.T) {
	logger := logging.NewTestLogger(t)
	g := &inject.Gantry{}
	m := makeFakeMotor(t, true)
	mt := makeFakeMotor(t, true)
	g.LengthsFunc = func(ctx context.Context, extra map[string]interface{}) ([]float64, error) {
		return nil, errors.New("bus error")
	}
	g.StopFunc = func(ctx context.Context, extra map[string]interface{}) error { return nil }
	deps := makeDepsWithTranslation(t, "g", "m", "m_trans", g, m, mt)
	_, err := NewHockeyPlayer(context.Background(), deps, resource.Name{Name: "hp"}, baseCfg(), logger)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bus error")
}

func TestNewHockeyPlayer_MissingTranslationMotor(t *testing.T) {
	logger := logging.NewTestLogger(t)
	deps := resource.Dependencies{
		gantry.Named("g"):    makeFakeGantry(t),
		motor.Named("m_rot"): makeFakeMotor(t, true /* positionReporting */),
		// m_trans intentionally omitted
	}
	cfg := &Config{
		Gantry:               "g",
		RotationMotor:        "m_rot",
		TranslationMotor:     "m_trans",
		MinTranslationMM:     0,
		MaxTranslationMM:     100,
		DefaultRPMRotation:   60,
		DefaultSpeedMMPerSec: 50,
	}
	_, err := NewHockeyPlayer(context.Background(), deps, resource.Name{Name: "hp"}, cfg, logger)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "translation_motor")
}

func TestClose_InterruptsInFlightMotion(t *testing.T) {
	defer goleak.VerifyNone(t, goleakOpts...)
	s, _, m, _ := newTestInstance(t)

	started := make(chan struct{})
	m.GoToFunc = func(ctx context.Context, rpm, positionRevolutions float64, extra map[string]interface{}) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}

	// DoCommand now blocks until GoTo returns; run in background.
	go func() {
		_, _ = s.DoCommand(context.Background(), map[string]interface{}{"r": 90.0})
	}()
	<-started

	require.NoError(t, s.Close(context.Background()))
	require.NoError(t, s.Close(context.Background())) // idempotent
}

func TestClose_NoInFlightMotion(t *testing.T) {
	defer goleak.VerifyNone(t, goleakOpts...)
	s, _, _, _ := newTestInstance(t)
	require.NoError(t, s.Close(context.Background()))
}

func TestClose_IdleStopsAllResources(t *testing.T) {
	defer goleak.VerifyNone(t, goleakOpts...)
	logger := logging.NewTestLogger(t)

	rotStops, transStops, gantryStops := 0, 0, 0

	g := &inject.Gantry{
		LengthsFunc: func(ctx context.Context, extra map[string]interface{}) ([]float64, error) {
			return []float64{200}, nil
		},
		StopFunc: func(ctx context.Context, extra map[string]interface{}) error {
			gantryStops++
			return nil
		},
	}
	mRot := &inject.Motor{
		PropertiesFunc: func(ctx context.Context, extra map[string]interface{}) (motor.Properties, error) {
			return motor.Properties{PositionReporting: true}, nil
		},
		StopFunc: func(ctx context.Context, extra map[string]interface{}) error {
			rotStops++
			return nil
		},
	}
	mTrans := &inject.Motor{
		PropertiesFunc: func(ctx context.Context, extra map[string]interface{}) (motor.Properties, error) {
			return motor.Properties{PositionReporting: true}, nil
		},
		StopFunc: func(ctx context.Context, extra map[string]interface{}) error {
			transStops++
			return nil
		},
	}

	deps := resource.Dependencies{
		gantry.Named("g"):      g,
		motor.Named("m_rot"):   mRot,
		motor.Named("m_trans"): mTrans,
	}
	cfg := &Config{
		Gantry:               "g",
		RotationMotor:        "m_rot",
		TranslationMotor:     "m_trans",
		MinTranslationMM:     0,
		MaxTranslationMM:     100,
		DefaultRPMRotation:   60,
		DefaultSpeedMMPerSec: 50,
	}
	hp, err := NewHockeyPlayer(context.Background(), deps, resource.Name{Name: "hp"}, cfg, logger)
	require.NoError(t, err)

	require.NoError(t, hp.Close(context.Background()))
	assert.GreaterOrEqual(t, rotStops, 1, "rotation_motor Stop should be called at least once during Close")
	assert.GreaterOrEqual(t, transStops, 1, "translation_motor Stop should be called at least once during Close")
	assert.GreaterOrEqual(t, gantryStops, 1, "gantry Stop should be called at least once during Close")
}

func TestClose_WaitsForGoroutine(t *testing.T) {
	defer goleak.VerifyNone(t, goleakOpts...)
	s, _, m, _ := newTestInstance(t)

	started := make(chan struct{})
	exited := make(chan struct{})
	m.GoToFunc = func(ctx context.Context, rpm, positionRevolutions float64, extra map[string]interface{}) error {
		close(started)
		<-ctx.Done()
		close(exited)
		return ctx.Err()
	}
	// DoCommand now blocks until GoTo returns; run in background.
	go func() {
		_, _ = s.DoCommand(context.Background(), map[string]interface{}{"r": 90.0})
	}()
	<-started

	closeDone := make(chan struct{})
	go func() {
		_ = s.Close(context.Background())
		close(closeDone)
	}()

	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return in time")
	}
	select {
	case <-exited:
	default:
		t.Fatal("motion goroutine did not exit before Close returned")
	}
}
