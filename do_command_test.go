package hockeyplayer

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.viam.com/rdk/components/motor"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/testutils/inject"
)

// newTestInstance wires up a hockeyPlayerHockeyPlayer around inject fakes,
// bypassing NewHockeyPlayer's dep resolution so tests can drive the fakes.
func newTestInstance(t *testing.T) (*hockeyPlayerHockeyPlayer, *inject.Gantry, *inject.Motor, *inject.Motor) {
	t.Helper()
	logger := logging.NewTestLogger(t)
	g := &inject.Gantry{}
	m := &inject.Motor{}
	mt := &inject.Motor{}

	cfg := &Config{
		Gantry:               "g",
		RotationMotor:        "m",
		TranslationMotor:     "m_trans",
		TranslationAxisIndex: 0,
		MinTranslationMM:     0,
		MaxTranslationMM:     100,
		DefaultRPMRotation:   60,
		DefaultSpeedMMPerSec: 50,
		DefaultWrap:          false,
	}
	cancelCtx, cancelFunc := context.WithCancel(context.Background())

	s := &hockeyPlayerHockeyPlayer{
		name:             resource.Name{Name: "hp"},
		logger:           logger,
		cfg:              cfg,
		gantry:           g,
		rotationMotor:    m,
		translationMotor: mt,
		cancelCtx:        cancelCtx,
		cancelFunc:       cancelFunc,
	}
	s.rotationAxis = newAxisController("rotation", func(c context.Context) error { return m.Stop(c, nil) }, logger)
	s.translationAxis = newAxisController("translation", func(c context.Context) error {
		if err := mt.Stop(c, nil); err != nil {
			_ = g.Stop(c, nil)
			return err
		}
		return g.Stop(c, nil)
	}, logger)

	t.Cleanup(func() {
		cancelFunc()
		s.rotationAxis.close()
		s.translationAxis.close()
	})

	// Sensible default fakes; individual tests override.
	g.PositionFunc = func(ctx context.Context, extra map[string]interface{}) ([]float64, error) {
		return []float64{0}, nil
	}
	g.StopFunc = func(ctx context.Context, extra map[string]interface{}) error { return nil }
	g.MoveToPositionFunc = func(ctx context.Context, positionsMm []float64, speedsMmPerSec []float64, extra map[string]interface{}) error {
		return nil
	}
	g.IsMovingFunc = func(ctx context.Context) (bool, error) { return false, nil }
	m.PositionFunc = func(ctx context.Context, extra map[string]interface{}) (float64, error) { return 0, nil }
	m.StopFunc = func(ctx context.Context, extra map[string]interface{}) error { return nil }
	m.GoForFunc = func(ctx context.Context, rpm, revolutions float64, extra map[string]interface{}) error {
		return nil
	}
	m.GoToFunc = func(ctx context.Context, rpm, positionRevolutions float64, extra map[string]interface{}) error {
		return nil
	}
	m.IsMovingFunc = func(ctx context.Context) (bool, error) { return false, nil }
	m.PropertiesFunc = func(ctx context.Context, extra map[string]interface{}) (motor.Properties, error) {
		return motor.Properties{PositionReporting: true}, nil
	}
	mt.StopFunc = func(ctx context.Context, extra map[string]interface{}) error { return nil }
	mt.SetPowerFunc = func(ctx context.Context, powerPct float64, extra map[string]interface{}) error { return nil }
	mt.PositionFunc = func(ctx context.Context, extra map[string]interface{}) (float64, error) { return 0, nil }
	mt.IsMovingFunc = func(ctx context.Context) (bool, error) { return false, nil }
	mt.PropertiesFunc = func(ctx context.Context, extra map[string]interface{}) (motor.Properties, error) {
		return motor.Properties{PositionReporting: true}, nil
	}

	return s, g, m, mt
}

func TestDoMotion_RejectsEmpty(t *testing.T) {
	s, _, _, _ := newTestInstance(t)
	_, err := s.DoCommand(context.Background(), map[string]interface{}{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no motion fields")
}

func TestDoMotion_RejectsOutOfRange(t *testing.T) {
	s, _, _, _ := newTestInstance(t)
	_, err := s.DoCommand(context.Background(), map[string]interface{}{"t": 1.5})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "'t'")

	_, err = s.DoCommand(context.Background(), map[string]interface{}{"r": -10.0})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "'r'")

	_, err = s.DoCommand(context.Background(), map[string]interface{}{"r": 90.0, "rpm": 0.0})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rpm")
}

func TestDoMotion_RotationOnly(t *testing.T) {
	s, g, m, _ := newTestInstance(t)

	var gotRPM, gotTargetRev float64
	called := make(chan struct{})
	m.GoToFunc = func(ctx context.Context, rpm, positionRevolutions float64, extra map[string]interface{}) error {
		gotRPM, gotTargetRev = rpm, positionRevolutions
		close(called)
		return nil
	}
	m.PositionFunc = func(ctx context.Context, extra map[string]interface{}) (float64, error) { return 0, nil }

	moveCalled := false
	g.MoveToPositionFunc = func(ctx context.Context, positionsMm, speedsMmPerSec []float64, extra map[string]interface{}) error {
		moveCalled = true
		return nil
	}

	resp, err := s.DoCommand(context.Background(), map[string]interface{}{"r": 90.0, "rpm": 30.0})
	require.NoError(t, err)
	assert.Equal(t, "done", resp["status"])
	_, hasTFinal := resp["t_final"]
	assert.False(t, hasTFinal, "rotation-only should not include t_final")

	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("GoTo not called")
	}
	assert.InDelta(t, 30.0, gotRPM, 1e-9)
	// posRev=0, target=90°, delta=+90°, targetRev = 0 + 90/360 = 0.25
	assert.InDelta(t, 0.25, gotTargetRev, 1e-9)
	assert.False(t, moveCalled, "translation should not be touched")
}

func TestDoMotion_TranslationOnly(t *testing.T) {
	s, g, m, _ := newTestInstance(t)

	var gotPositions, gotSpeeds []float64
	called := make(chan struct{})
	g.MoveToPositionFunc = func(ctx context.Context, positionsMm, speedsMmPerSec []float64, extra map[string]interface{}) error {
		gotPositions = positionsMm
		gotSpeeds = speedsMmPerSec
		close(called)
		return nil
	}
	g.PositionFunc = func(ctx context.Context, extra map[string]interface{}) ([]float64, error) {
		return []float64{0}, nil
	}
	goToCalled := false
	m.GoToFunc = func(ctx context.Context, rpm, positionRevolutions float64, extra map[string]interface{}) error {
		goToCalled = true
		return nil
	}

	resp, err := s.DoCommand(context.Background(), map[string]interface{}{"t": 0.5})
	require.NoError(t, err)
	assert.Equal(t, "done", resp["status"])
	_, hasRFinal := resp["r_final"]
	assert.False(t, hasRFinal, "translation-only should not include r_final")

	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("MoveToPosition not called")
	}
	assert.Equal(t, []float64{50.0}, gotPositions)
	assert.Equal(t, []float64{50.0}, gotSpeeds)
	assert.False(t, goToCalled, "rotation should not be touched")
}

func TestDoMotion_BothAxes(t *testing.T) {
	s, g, m, _ := newTestInstance(t)

	rotCalled := make(chan struct{})
	transCalled := make(chan struct{})
	m.GoToFunc = func(ctx context.Context, rpm, positionRevolutions float64, extra map[string]interface{}) error {
		close(rotCalled)
		return nil
	}
	g.MoveToPositionFunc = func(ctx context.Context, positionsMm, speedsMmPerSec []float64, extra map[string]interface{}) error {
		close(transCalled)
		return nil
	}

	resp, err := s.DoCommand(context.Background(), map[string]interface{}{"t": 0.3, "r": 45.0})
	require.NoError(t, err)
	assert.Equal(t, "done", resp["status"])
	_, hasTFinal := resp["t_final"]
	assert.True(t, hasTFinal, "both-axes response should include t_final")
	_, hasRFinal := resp["r_final"]
	assert.True(t, hasRFinal, "both-axes response should include r_final")

	for _, ch := range []chan struct{}{rotCalled, transCalled} {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatal("one axis did not run")
		}
	}
}

func TestDoMotion_AlreadyAtTarget(t *testing.T) {
	s, _, m, _ := newTestInstance(t)
	goToCalled := false
	m.GoToFunc = func(ctx context.Context, rpm, positionRevolutions float64, extra map[string]interface{}) error {
		goToCalled = true
		return nil
	}
	m.PositionFunc = func(ctx context.Context, extra map[string]interface{}) (float64, error) {
		return 0.25, nil // 90°
	}

	resp, err := s.DoCommand(context.Background(), map[string]interface{}{"r": 90.0})
	require.NoError(t, err)
	assert.Equal(t, "done", resp["status"])
	// Already at target: r_final should still be present (reads current position).
	assert.InDelta(t, 90.0, resp["r_final"], 0.01)
	assert.False(t, goToCalled)
}

func TestDoMotion_PerAxisPreemption(t *testing.T) {
	s, g, m, _ := newTestInstance(t)

	first := make(chan struct{})
	m.GoToFunc = func(ctx context.Context, rpm, positionRevolutions float64, extra map[string]interface{}) error {
		close(first)
		<-ctx.Done()
		return ctx.Err()
	}
	var stopCalls sync.Mutex
	var stopCount int
	m.StopFunc = func(ctx context.Context, extra map[string]interface{}) error {
		stopCalls.Lock()
		stopCount++
		stopCalls.Unlock()
		return nil
	}

	// First DoCommand blocks (GoTo never returns until preempted), run in background.
	firstDone := make(chan error, 1)
	go func() {
		_, err := s.DoCommand(context.Background(), map[string]interface{}{"r": 45.0})
		firstDone <- err
	}()

	// Wait for first GoTo to be entered before issuing the preempting command.
	select {
	case <-first:
	case <-time.After(time.Second):
		t.Fatal("first GoTo never started")
	}

	gMoved := false
	g.MoveToPositionFunc = func(ctx context.Context, positionsMm, speedsMmPerSec []float64, extra map[string]interface{}) error {
		gMoved = true
		return nil
	}

	secondRan := make(chan struct{})
	m.GoToFunc = func(ctx context.Context, rpm, positionRevolutions float64, extra map[string]interface{}) error {
		close(secondRan)
		return nil
	}
	_, err := s.DoCommand(context.Background(), map[string]interface{}{"r": 180.0})
	require.NoError(t, err)

	select {
	case <-secondRan:
	case <-time.After(time.Second):
		t.Fatal("preempted rotation did not start new move")
	}

	assert.False(t, gMoved, "omitting t must not touch translation")
	stopCalls.Lock()
	assert.GreaterOrEqual(t, stopCount, 1)
	stopCalls.Unlock()
}

func TestDoMotion_PositionReadError(t *testing.T) {
	s, _, m, _ := newTestInstance(t)
	m.PositionFunc = func(ctx context.Context, extra map[string]interface{}) (float64, error) {
		return 0, errors.New("boom")
	}
	_, err := s.DoCommand(context.Background(), map[string]interface{}{"r": 90.0})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rotation motor position")
}

func TestGetPosition_HappyPath(t *testing.T) {
	s, g, m, _ := newTestInstance(t)
	g.PositionFunc = func(ctx context.Context, extra map[string]interface{}) ([]float64, error) {
		return []float64{50}, nil
	}
	m.PositionFunc = func(ctx context.Context, extra map[string]interface{}) (float64, error) {
		return 0.5, nil // 180°
	}
	g.IsMovingFunc = func(ctx context.Context) (bool, error) { return true, nil }
	m.IsMovingFunc = func(ctx context.Context) (bool, error) { return false, nil }

	resp, err := s.DoCommand(context.Background(), map[string]interface{}{"cmd": "get_position"})
	require.NoError(t, err)
	assert.InDelta(t, 0.5, resp["t"], 1e-9)
	assert.InDelta(t, 180.0, resp["r"], 0.01)
	assert.Equal(t, true, resp["t_moving"])
	assert.Equal(t, false, resp["r_moving"])
}

func TestDoCommand_UnknownCmd(t *testing.T) {
	s, _, _, _ := newTestInstance(t)
	_, err := s.DoCommand(context.Background(), map[string]interface{}{"cmd": "party"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown cmd")
}

func TestDoMotion_RejectsBothRPMAndPower(t *testing.T) {
	s, _, _, _ := newTestInstance(t)
	_, err := s.DoCommand(context.Background(), map[string]interface{}{
		"t":     0.5,
		"rpm":   30.0,
		"power": 0.5,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot specify both")
}

func TestDoMotion_RejectsPowerOutOfRange(t *testing.T) {
	s, _, _, _ := newTestInstance(t)
	cases := []float64{0, -0.1, 1.5}
	for _, p := range cases {
		_, err := s.DoCommand(context.Background(), map[string]interface{}{
			"t":     0.5,
			"power": p,
		})
		require.Error(t, err, "power=%v", p)
		assert.Contains(t, err.Error(), "power")
	}
}

func TestDoMotion_PowerRotation_UsesSetPowerPollStop(t *testing.T) {
	s, _, m, _ := newTestInstance(t)

	// Motor sweep: simulate position advancing each time it's read.
	var posRev atomic.Value // float64
	posRev.Store(0.25)      // 90° accumulated
	m.PositionFunc = func(ctx context.Context, extra map[string]interface{}) (float64, error) {
		return posRev.Load().(float64), nil
	}

	var setPowerCalls []float64
	var mu sync.Mutex
	m.SetPowerFunc = func(ctx context.Context, powerPct float64, extra map[string]interface{}) error {
		mu.Lock()
		setPowerCalls = append(setPowerCalls, powerPct)
		mu.Unlock()
		// Advance the fake motor toward the target quickly.
		posRev.Store(0.5) // 180° accumulated
		return nil
	}
	stopCalls := 0
	m.StopFunc = func(ctx context.Context, extra map[string]interface{}) error {
		stopCalls++
		return nil
	}
	// Guard: rpm-mode APIs must not be called.
	m.GoToFunc = func(ctx context.Context, rpm, pos float64, extra map[string]interface{}) error {
		t.Fatalf("unexpected GoTo in power mode")
		return nil
	}

	_, err := s.DoCommand(context.Background(), map[string]interface{}{
		"r":     180.0,
		"power": 0.4,
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(setPowerCalls) >= 1 && stopCalls >= 1
	}, time.Second, 10*time.Millisecond)

	// Positive power (short path from 90° to 180° is +90°).
	mu.Lock()
	defer mu.Unlock()
	assert.Greater(t, setPowerCalls[0], 0.0)
	assert.InDelta(t, 0.4, setPowerCalls[0], 1e-9)
}

func TestDoMotion_PowerTranslation_SetPowerOnTranslationMotor(t *testing.T) {
	s, g, _, mt := newTestInstance(t)

	// Gantry reports position along axis 0; advance it when SetPower is called.
	var posMM atomic.Value // float64
	posMM.Store(10.0)
	g.PositionFunc = func(ctx context.Context, extra map[string]interface{}) ([]float64, error) {
		return []float64{posMM.Load().(float64)}, nil
	}

	var setPowerCalls []float64
	var mu sync.Mutex
	mt.SetPowerFunc = func(ctx context.Context, p float64, extra map[string]interface{}) error {
		mu.Lock()
		setPowerCalls = append(setPowerCalls, p)
		mu.Unlock()
		// Jump near the target in mm (cfg: min=0, max=100; t=0.5 → 50mm).
		posMM.Store(50.0)
		return nil
	}
	stopCalls := 0
	mt.StopFunc = func(ctx context.Context, extra map[string]interface{}) error {
		stopCalls++
		return nil
	}
	// Gantry MoveToPosition must NOT be called in power mode.
	g.MoveToPositionFunc = func(ctx context.Context, pos []float64, speeds []float64, extra map[string]interface{}) error {
		t.Fatalf("unexpected MoveToPosition in power mode")
		return nil
	}

	_, err := s.DoCommand(context.Background(), map[string]interface{}{
		"t":     0.5,
		"power": 0.6,
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(setPowerCalls) >= 1 && stopCalls >= 1
	}, time.Second, 10*time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	// Forward direction: pos 10 → target 50, so positive power.
	assert.Greater(t, setPowerCalls[0], 0.0)
	assert.InDelta(t, 0.6, setPowerCalls[0], 1e-9)
}

func TestDoMotion_Blocking_ReturnsAfterMoveCompletes(t *testing.T) {
	s, _, m, _ := newTestInstance(t)

	gotoStarted := make(chan struct{})
	gotoUnblock := make(chan struct{})
	m.GoToFunc = func(ctx context.Context, rpm, pos float64, extra map[string]interface{}) error {
		close(gotoStarted)
		select {
		case <-gotoUnblock:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	// After GoTo returns, position equals target.
	var posRev atomic.Value
	posRev.Store(0.0)
	m.PositionFunc = func(ctx context.Context, extra map[string]interface{}) (float64, error) {
		return posRev.Load().(float64), nil
	}

	done := make(chan struct {
		resp map[string]interface{}
		err  error
	})
	go func() {
		r, err := s.DoCommand(context.Background(), map[string]interface{}{
			"r":   180.0,
			"rpm": 60.0,
		})
		done <- struct {
			resp map[string]interface{}
			err  error
		}{r, err}
	}()

	<-gotoStarted
	// DoCommand must still be blocked.
	select {
	case <-done:
		t.Fatal("DoCommand returned before motion finished")
	case <-time.After(50 * time.Millisecond):
	}

	posRev.Store(0.5) // 180° accumulated
	close(gotoUnblock)

	select {
	case result := <-done:
		require.NoError(t, result.err)
		assert.Equal(t, "done", result.resp["status"])
		assert.InDelta(t, 180.0, result.resp["r_final"], 0.01)
		_, hasTFinal := result.resp["t_final"]
		assert.False(t, hasTFinal)
	case <-time.After(time.Second):
		t.Fatal("DoCommand never returned after move completed")
	}
}

func TestDoMotion_Blocking_BothAxesConcurrent(t *testing.T) {
	s, g, m, _ := newTestInstance(t)

	gotoEntry := make(chan struct{}, 1)
	moveEntry := make(chan struct{}, 1)
	m.GoToFunc = func(ctx context.Context, rpm, pos float64, extra map[string]interface{}) error {
		gotoEntry <- struct{}{}
		<-ctx.Done() // wait for cancel — or the test won't be able to assert concurrency
		return nil
	}
	g.MoveToPositionFunc = func(ctx context.Context, p []float64, s []float64, extra map[string]interface{}) error {
		moveEntry <- struct{}{}
		<-ctx.Done()
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() {
		_, err := s.DoCommand(ctx, map[string]interface{}{
			"t": 0.5, "r": 180.0, "rpm": 60.0,
		})
		done <- err
	}()

	// Both should have entered their move fns.
	select {
	case <-gotoEntry:
	case <-time.After(time.Second):
		t.Fatal("GoTo never called")
	}
	select {
	case <-moveEntry:
	case <-time.After(time.Second):
		t.Fatal("MoveToPosition never called")
	}

	cancel()
	<-done
}

func TestDoMotion_Blocking_ErrorPropagatesAndPreemptsOtherAxis(t *testing.T) {
	s, g, m, _ := newTestInstance(t)

	wantErr := errors.New("rotation exploded")
	m.GoToFunc = func(ctx context.Context, rpm, pos float64, extra map[string]interface{}) error {
		return wantErr
	}
	gantryStopped := make(chan struct{}, 1)
	g.MoveToPositionFunc = func(ctx context.Context, p []float64, s []float64, extra map[string]interface{}) error {
		<-ctx.Done()
		return ctx.Err()
	}
	g.StopFunc = func(ctx context.Context, extra map[string]interface{}) error {
		select {
		case gantryStopped <- struct{}{}:
		default:
		}
		return nil
	}

	_, err := s.DoCommand(context.Background(), map[string]interface{}{
		"t": 0.5, "r": 180.0, "rpm": 60.0,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)

	select {
	case <-gantryStopped:
	case <-time.After(time.Second):
		t.Fatal("gantry was never stopped after rotation error")
	}
}

func TestDoMotion_RPMRotation_UsesGoToWithAbsoluteTarget(t *testing.T) {
	s, _, m, _ := newTestInstance(t)

	// Motor currently at 2.25 revolutions = 810° accumulated → normalize to 90°.
	m.PositionFunc = func(ctx context.Context, extra map[string]interface{}) (float64, error) { return 2.25, nil }

	var gotRPM, gotTargetRev float64
	callCount := 0
	m.GoToFunc = func(ctx context.Context, rpm, positionRevolutions float64, extra map[string]interface{}) error {
		callCount++
		gotRPM = rpm
		gotTargetRev = positionRevolutions
		return nil
	}
	// Make GoFor fatal so we catch accidental old-path calls:
	m.GoForFunc = func(ctx context.Context, rpm, revolutions float64, extra map[string]interface{}) error {
		t.Fatalf("unexpected GoFor call (rpm=%v revs=%v)", rpm, revolutions)
		return nil
	}

	_, err := s.DoCommand(context.Background(), map[string]interface{}{
		"r":   180.0,
		"rpm": 45.0,
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool { return callCount == 1 }, time.Second, 10*time.Millisecond)

	assert.InDelta(t, 45.0, gotRPM, 1e-9)
	// Current 90°, target 180°, wrap=false default, short path is +90°. Absolute target:
	// current_rev + 90/360 = 2.25 + 0.25 = 2.5.
	assert.InDelta(t, 2.5, gotTargetRev, 1e-9)
}

func TestDoMotion_Blocking_CallerCancelStopsHardware(t *testing.T) {
	s, _, m, _ := newTestInstance(t)

	started := make(chan struct{})
	stopCalls := 0
	m.GoToFunc = func(ctx context.Context, rpm, pos float64, extra map[string]interface{}) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	m.StopFunc = func(ctx context.Context, extra map[string]interface{}) error {
		stopCalls++
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() {
		_, err := s.DoCommand(ctx, map[string]interface{}{"r": 180.0, "rpm": 60.0})
		done <- err
	}()

	<-started
	cancel()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("DoCommand did not return after caller cancel")
	}

	// Stop should have been called on the rotation motor due to preemption.
	assert.GreaterOrEqual(t, stopCalls, 1)
}
