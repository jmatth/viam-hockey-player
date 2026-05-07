# Hockey Player v2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend the existing `nfranczak:generic:hockey-player` module so motion commands carry both a target (`t`, `r`) and a drive mode (`rpm` or `power`), so translation can be power-driven via a newly-configured `translation_motor`, and so `DoCommand` blocks until every commanded axis has arrived.

**Architecture:** Keep the per-axis `axisController` preemption model but make dispatch return an error channel so the top-level `DoCommand` can wait on every active axis before returning. Add a shared `powerMoveTo` helper that both axes reuse for the `SetPower` + poll + `Stop` path; it takes injected position-reader / power-setter / stopper functions so the two axes differ only in which resource backs which function. Rpm-mode rotation switches from `GoFor(delta_rev)` to `GoTo(target_rev)`; rpm-mode translation stays on `gantry.MoveToPosition`.

**Tech Stack:** Go · `go.viam.com/rdk` (gantry, motor, generic component) · `go.viam.com/rdk/testutils/inject` · `github.com/stretchr/testify` · `make test`.

**Spec:** `docs/superpowers/specs/2026-04-24-hockey-player-design-v2.md`.

**Repo state:** The working directory is not a git repo (verified). Tasks end with `make test` (or `go test ./... -run <pattern>`) instead of git commits; if a repo gets initialized later, commits can be added back in.

---

## File Structure

**Modify:**
- `config.go` — add `TranslationMotor`, `RotationArrivalTolDeg`, `TranslationArrivalTolMM`; update `Validate` and return the third dependency.
- `config_test.go` — extend `validCfg()` and failure table.
- `module.go` — resolve `translation_motor`; fan-out translation stop; update startup log; update `Close`.
- `axis_controller.go` — `dispatch` returns `<-chan error`; error-channel protocol; `close` unchanged.
- `do_command.go` — parse `rpm`/`power` exclusively; branch on mode per axis; wait on error channels; build `{status, t_final, r_final}` response; rotation rpm-mode uses `GoTo`.
- `do_command_test.go` — fake `translation_motor`; cover new scenarios.
- `module_test.go` — integration-ish tests of startup/shutdown with translation_motor.

**Create:**
- `power_move.go` — shared `powerMoveTo` helper (position reader, setter, stopper, target, tol, poll).
- `power_move_test.go` — unit tests with hand-rolled reader/setter functions (no viam fakes needed).

No files removed. No new external deps.

---

### Task 1: Extend Config with `translation_motor` and tolerances

**Files:**
- Modify: `config.go`
- Modify: `config_test.go`

- [ ] **Step 1: Write failing tests for new validation rules**

Extend `config_test.go`. Update `validCfg()` and add failure cases:

```go
func validCfg() *Config {
	return &Config{
		Gantry:                   "g",
		RotationMotor:            "m_rot",
		TranslationMotor:         "m_trans",
		TranslationAxisIndex:     0,
		MinTranslationMM:         10,
		MaxTranslationMM:         100,
		DefaultRPMRotation:       60,
		DefaultSpeedMMPerSec:     50,
		DefaultWrap:              false,
		RotationArrivalTolDeg:    0.5,
		TranslationArrivalTolMM:  2.0,
	}
}

func TestValidate_HappyPath(t *testing.T) {
	req, opt, err := validCfg().Validate("components.0")
	require.NoError(t, err)
	assert.Equal(t, []string{"g", "m_rot", "m_trans"}, req)
	assert.Nil(t, opt)
}
```

Add to the failure table in `TestValidate_Failures`:

```go
{"missing translation_motor", func(c *Config) { c.TranslationMotor = "" }, "translation_motor"},
{"negative rotation_arrival_tol", func(c *Config) { c.RotationArrivalTolDeg = -0.1 }, "rotation_arrival_tol_deg"},
{"zero translation_arrival_tol", func(c *Config) { c.TranslationArrivalTolMM = 0 }, "translation_arrival_tol_mm"},
```

Note: the tolerance validation rule is "if set, must be > 0". An *unset* (zero) value is allowed and will get defaulted at read time — we test the zero-is-fine path via the happy path setting them to non-zero, and the explicit-negative path as a failure. To test "unset is fine" explicitly, add:

```go
{"unset tolerances are ok", func(c *Config) {
    c.RotationArrivalTolDeg = 0
    c.TranslationArrivalTolMM = 0
}, ""}, // expect NO error
```

Then adjust the loop so an empty `match` string means "no error":

```go
for _, tc := range cases {
    t.Run(tc.name, func(t *testing.T) {
        c := validCfg()
        tc.mut(c)
        _, _, err := c.Validate("components.0")
        if tc.match == "" {
            require.NoError(t, err)
            return
        }
        require.Error(t, err)
        assert.Contains(t, err.Error(), tc.match)
        assert.Contains(t, err.Error(), "components.0")
    })
}
```

- [ ] **Step 2: Run tests to verify they fail**

```
go test ./... -run TestValidate
```

Expected: compile error — `Config` has no `TranslationMotor`, `RotationArrivalTolDeg`, `TranslationArrivalTolMM` fields.

- [ ] **Step 3: Update `Config` struct and `Validate`**

Replace `config.go`:

```go
package hockeyplayer

import "fmt"

type Config struct {
	Gantry                  string  `json:"gantry"`
	RotationMotor           string  `json:"rotation_motor"`
	TranslationMotor        string  `json:"translation_motor"`
	TranslationAxisIndex    int     `json:"translation_axis_index,omitempty"`
	MinTranslationMM        float64 `json:"min_translation_mm"`
	MaxTranslationMM        float64 `json:"max_translation_mm"`
	DefaultRPMRotation      float64 `json:"default_rpm_rotation"`
	DefaultSpeedMMPerSec    float64 `json:"default_speed_mm_per_sec"`
	DefaultWrap             bool    `json:"default_wrap,omitempty"`
	RotationArrivalTolDeg   float64 `json:"rotation_arrival_tol_deg,omitempty"`
	TranslationArrivalTolMM float64 `json:"translation_arrival_tol_mm,omitempty"`
}

func (c *Config) Validate(path string) ([]string, []string, error) {
	if c.Gantry == "" {
		return nil, nil, fmt.Errorf("%s: 'gantry' is required", path)
	}
	if c.RotationMotor == "" {
		return nil, nil, fmt.Errorf("%s: 'rotation_motor' is required", path)
	}
	if c.TranslationMotor == "" {
		return nil, nil, fmt.Errorf("%s: 'translation_motor' is required", path)
	}
	if c.TranslationAxisIndex < 0 {
		return nil, nil, fmt.Errorf("%s: 'translation_axis_index' must be >= 0", path)
	}
	if c.MinTranslationMM < 0 {
		return nil, nil, fmt.Errorf("%s: 'min_translation_mm' must be >= 0", path)
	}
	if c.MaxTranslationMM <= c.MinTranslationMM {
		return nil, nil, fmt.Errorf("%s: 'max_translation_mm' must be > 'min_translation_mm'", path)
	}
	if c.DefaultRPMRotation <= 0 {
		return nil, nil, fmt.Errorf("%s: 'default_rpm_rotation' must be > 0", path)
	}
	if c.DefaultSpeedMMPerSec <= 0 {
		return nil, nil, fmt.Errorf("%s: 'default_speed_mm_per_sec' must be > 0", path)
	}
	if c.RotationArrivalTolDeg < 0 {
		return nil, nil, fmt.Errorf("%s: 'rotation_arrival_tol_deg' must be > 0 if set", path)
	}
	if c.TranslationArrivalTolMM < 0 {
		return nil, nil, fmt.Errorf("%s: 'translation_arrival_tol_mm' must be > 0 if set", path)
	}
	return []string{c.Gantry, c.RotationMotor, c.TranslationMotor}, nil, nil
}

const (
	defaultRotationArrivalTolDeg  = 0.5
	defaultTranslationArrivalTolMM = 2.0
	defaultPowerPollInterval       = 20 // ms
)

// Resolved returns the tolerance values to use at runtime, substituting
// defaults when a field is zero-valued.
func (c *Config) RotationTolDeg() float64 {
	if c.RotationArrivalTolDeg <= 0 {
		return defaultRotationArrivalTolDeg
	}
	return c.RotationArrivalTolDeg
}

func (c *Config) TranslationTolMM() float64 {
	if c.TranslationArrivalTolMM <= 0 {
		return defaultTranslationArrivalTolMM
	}
	return c.TranslationArrivalTolMM
}
```

Note the validation treats "negative = error, zero = allowed (defaulted at read)". That matches the test table and the spec ("optional").

- [ ] **Step 4: Run tests to verify they pass**

```
go test ./... -run TestValidate
```

Expected: PASS.

- [ ] **Step 5: Checkpoint**

```
make test
```

Expected: all still-existing tests pass. Some `do_command_test.go` tests may fail to build because `newTestInstance` doesn't yet wire `TranslationMotor`; fix trivially in this step by adding `TranslationMotor: "m_trans"` to the cfg literal in `newTestInstance`. Do not yet add a second injected motor — that's Task 2.

---

### Task 2: Resolve `translation_motor` in the module; fan-out translation stop

**Files:**
- Modify: `module.go`
- Modify: `do_command_test.go` (test helper `newTestInstance`)
- Modify: `module_test.go` (add a case for translation_motor resolution)

- [ ] **Step 1: Write failing test for translation_motor resolution**

In `module_test.go`, add a test that `NewHockeyPlayer` fails when the translation_motor dependency is missing. Existing style (pattern already in the file — inspect before writing to match it). Example skeleton:

```go
func TestNewHockeyPlayer_MissingTranslationMotor(t *testing.T) {
	logger := logging.NewTestLogger(t)
	deps := resource.Dependencies{
		gantry.Named("g"):       makeFakeGantry(t),
		motor.Named("m_rot"):    makeFakeMotor(t, true /* positionReporting */),
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
```

If helper constructors `makeFakeGantry` / `makeFakeMotor` don't already exist in `module_test.go`, write them inline using `inject.Gantry` / `inject.Motor` with `LengthsFunc` returning `[]float64{200}`, `PropertiesFunc` returning `motor.Properties{PositionReporting: true}`, `StopFunc` returning nil. Keep them local to the file.

- [ ] **Step 2: Run test to verify failure**

```
go test ./... -run TestNewHockeyPlayer_MissingTranslationMotor
```

Expected: FAIL — currently `NewHockeyPlayer` doesn't look up `translation_motor`, so the function returns success and the assertion fires.

- [ ] **Step 3: Add `translationMotor` field and resolution**

Modify `module.go`:

```go
type hockeyPlayerHockeyPlayer struct {
	resource.AlwaysRebuild

	name   resource.Name
	logger logging.Logger
	cfg    *Config

	gantry           gantry.Gantry
	rotationMotor    motor.Motor
	translationMotor motor.Motor

	cancelCtx  context.Context
	cancelFunc func()

	rotationAxis    *axisController
	translationAxis *axisController
}
```

In `NewHockeyPlayer`, after resolving the rotation motor, resolve the translation motor:

```go
mt, err := motor.FromProvider(deps, conf.TranslationMotor)
if err != nil {
	return nil, fmt.Errorf("translation_motor %q: %w", conf.TranslationMotor, err)
}
```

After the existing `props` check on the rotation motor, add a non-fatal properties check on the translation motor (the spec says log a warning, don't fail):

```go
if tprops, err := mt.Properties(ctx, nil); err != nil {
	logger.Warnf("translation_motor %q: reading properties failed: %v", conf.TranslationMotor, err)
} else if !tprops.PositionReporting {
	logger.Warnf("translation_motor %q does not report position (this is OK — position comes from the gantry)", conf.TranslationMotor)
}
```

Store `mt` in the struct:

```go
s := &hockeyPlayerHockeyPlayer{
	name:             name,
	logger:           logger,
	cfg:              conf,
	gantry:           g,
	rotationMotor:    m,
	translationMotor: mt,
	cancelCtx:        cancelCtx,
	cancelFunc:       cancelFunc,
}
```

Update the translation axis's stop fan-out — stop *both* `translationMotor` and `gantry`, logging any error from either and returning only a combined error if both fail:

```go
s.rotationAxis = newAxisController(
	"rotation",
	func(c context.Context) error { return m.Stop(c, nil) },
	logger,
)
s.translationAxis = newAxisController(
	"translation",
	func(c context.Context) error {
		errM := mt.Stop(c, nil)
		errG := g.Stop(c, nil)
		switch {
		case errM != nil && errG != nil:
			return fmt.Errorf("translation stop: motor=%v, gantry=%v", errM, errG)
		case errM != nil:
			return fmt.Errorf("translation_motor stop: %w", errM)
		case errG != nil:
			return fmt.Errorf("gantry stop: %w", errG)
		}
		return nil
	},
	logger,
)
```

Update the startup Info log to include the new fields:

```go
logger.Infof("hockey-player started: t=[%.2f,%.2f]mm axis=%d default_rpm=%.1f default_speed=%.1f default_wrap=%v rot_tol=%.2f° trans_tol=%.2fmm",
	conf.MinTranslationMM, conf.MaxTranslationMM, conf.TranslationAxisIndex,
	conf.DefaultRPMRotation, conf.DefaultSpeedMMPerSec, conf.DefaultWrap,
	conf.RotationTolDeg(), conf.TranslationTolMM())
```

In `do_command_test.go` `newTestInstance`, add a second injected motor:

```go
mt := &inject.Motor{}
// ... append to return signature: returns (s, g, m, mt) ...
s.translationMotor = mt
// default fakes:
mt.StopFunc = func(ctx context.Context, extra map[string]interface{}) error { return nil }
mt.SetPowerFunc = func(ctx context.Context, powerPct float64, extra map[string]interface{}) error { return nil }
mt.PositionFunc = func(ctx context.Context, extra map[string]interface{}) (float64, error) { return 0, nil }
mt.IsMovingFunc = func(ctx context.Context) (bool, error) { return false, nil }
mt.PropertiesFunc = func(ctx context.Context, extra map[string]interface{}) (motor.Properties, error) {
	return motor.Properties{PositionReporting: true}, nil
}
// Translation stop fan-out matching module.go:
s.translationAxis = newAxisController("translation", func(c context.Context) error {
	if err := mt.Stop(c, nil); err != nil {
		_ = g.Stop(c, nil)
		return err
	}
	return g.Stop(c, nil)
}, logger)
```

Update every caller of `newTestInstance` in `do_command_test.go` to the new signature.

- [ ] **Step 4: Run tests**

```
go test ./... -run TestNewHockeyPlayer_MissingTranslationMotor
go test ./... -run TestDoMotion
```

Expected: the new test passes; existing DoMotion tests still pass.

- [ ] **Step 5: Checkpoint**

```
make test
```

---

### Task 3: Make `axisController.dispatch` return a `<-chan error`

**Files:**
- Modify: `axis_controller.go`
- Modify: `do_command.go` (callers)
- Modify: `do_command_test.go` (any direct dispatch references in tests, if present)

This task is purely a plumbing change so a later task can make `DoCommand` blocking. Behavior from an outside caller's perspective should be unchanged if they don't read the channel — the goroutine still runs and errors still get logged. The new thing is that callers *can* read the channel to wait.

- [ ] **Step 1: Write a failing test for the error-channel behavior**

In `do_command_test.go` (or a new `axis_controller_test.go` — check whether one already exists):

```go
func TestAxisController_DispatchReturnsErrorChan_Success(t *testing.T) {
	logger := logging.NewTestLogger(t)
	a := newAxisController("test", func(c context.Context) error { return nil }, logger)
	t.Cleanup(a.close)

	ch := a.dispatch(context.Background(), func(ctx context.Context) error {
		return nil
	})

	select {
	case err := <-ch:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("dispatch channel never fired")
	}
}

func TestAxisController_DispatchReturnsErrorChan_Failure(t *testing.T) {
	logger := logging.NewTestLogger(t)
	a := newAxisController("test", func(c context.Context) error { return nil }, logger)
	t.Cleanup(a.close)

	want := errors.New("move blew up")
	ch := a.dispatch(context.Background(), func(ctx context.Context) error {
		return want
	})

	select {
	case err := <-ch:
		require.ErrorIs(t, err, want)
	case <-time.After(time.Second):
		t.Fatal("dispatch channel never fired")
	}
}

func TestAxisController_DispatchReturnsErrorChan_PreemptedIsNil(t *testing.T) {
	logger := logging.NewTestLogger(t)
	stopped := make(chan struct{}, 1)
	a := newAxisController("test", func(c context.Context) error {
		select {
		case stopped <- struct{}{}:
		default:
		}
		return nil
	}, logger)
	t.Cleanup(a.close)

	blocked := make(chan struct{})
	ch1 := a.dispatch(context.Background(), func(ctx context.Context) error {
		<-ctx.Done()
		close(blocked)
		return ctx.Err()
	})

	// Second dispatch preempts the first.
	ch2 := a.dispatch(context.Background(), func(ctx context.Context) error { return nil })

	select {
	case err := <-ch1:
		// Preempted dispatch should report nil (context cancelled, not a real failure).
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("preempted dispatch channel never fired")
	}
	<-ch2
	<-blocked
	<-stopped
}
```

- [ ] **Step 2: Run tests to verify they fail**

```
go test ./... -run TestAxisController_Dispatch
```

Expected: compile error — `dispatch` currently returns nothing.

- [ ] **Step 3: Update `dispatch` to return `<-chan error`**

Replace the body of `dispatch` in `axis_controller.go`:

```go
func (a *axisController) dispatch(moduleCtx context.Context, moveFn func(ctx context.Context) error) <-chan error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.cancel != nil {
		a.cancel()
		if err := a.stopFn(context.Background()); err != nil {
			a.logger.Warnf("axis %s: stop during preempt failed: %v", a.name, err)
		}
		<-a.done
	}

	ctx, cancel := context.WithCancel(moduleCtx)
	done := make(chan struct{})
	errCh := make(chan error, 1)
	a.cancel = cancel
	a.done = done

	go func() {
		defer close(done)
		err := moveFn(ctx)
		switch {
		case err == nil:
			errCh <- nil
		case ctx.Err() != nil:
			// preempt or module shutdown — not a real failure
			errCh <- nil
		default:
			a.logger.Errorf("axis %s failed: %v", a.name, err)
			errCh <- err
		}
	}()

	return errCh
}
```

- [ ] **Step 4: Update callers in `do_command.go` to ignore the channel for now**

Any existing `s.rotationAxis.dispatch(...)` or `s.translationAxis.dispatch(...)` calls should be trivially updated to `_ = s.rotationAxis.dispatch(...)` so the package compiles. Blocking comes in Task 9.

- [ ] **Step 5: Run tests**

```
go test ./... -run TestAxisController_Dispatch
go test ./...
```

Expected: new tests PASS; existing tests still pass.

- [ ] **Step 6: Checkpoint**

```
make test
```

---

### Task 4: Switch rotation rpm-mode from `GoFor` to `GoTo(target_rev)`

**Files:**
- Modify: `do_command.go`
- Modify: `do_command_test.go`

- [ ] **Step 1: Write a failing test**

Replace or supplement any existing `GoFor`-checking test with:

```go
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

	// Blocking is added in Task 9; this test tolerates fire-and-forget for now.
	require.Eventually(t, func() bool { return callCount == 1 }, time.Second, 10*time.Millisecond)

	assert.InDelta(t, 45.0, gotRPM, 1e-9)
	// Current 90°, target 180°, wrap=false default, short path is +90°. Absolute target:
	// current_rev + 90/360 = 2.25 + 0.25 = 2.5.
	assert.InDelta(t, 2.5, gotTargetRev, 1e-9)
}
```

- [ ] **Step 2: Run the test to verify it fails**

```
go test ./... -run TestDoMotion_RPMRotation_UsesGoToWithAbsoluteTarget
```

Expected: FAIL — current code still calls `GoFor`, so the `t.Fatalf` in `GoForFunc` fires.

- [ ] **Step 3: Change the rotation rpm branch in `do_command.go`**

Find the block in `doMotion` that starts with `if rOK {` and currently calls `motorRef.GoFor`. Replace it with a call to `GoTo(rpm, target_rev)`:

```go
if rOK {
	posRev, err := s.rotationMotor.Position(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("reading rotation motor position: %w", err)
	}
	currentDeg := normalizeAngle(posRev)
	delta := computeDelta(currentDeg, rVal, wrap)
	if math.Abs(delta) < 0.01 {
		s.logger.Debugf("rotation already at target (current=%.2f°, target=%.2f°)", currentDeg, rVal)
		resp["r_dispatched"] = false
	} else {
		targetRev := posRev + delta/360.0
		motorRef := s.rotationMotor
		_ = s.rotationAxis.dispatch(s.cancelCtx, func(c context.Context) error {
			return motorRef.GoTo(c, rpm, targetRev, nil)
		})
		resp["r_dispatched"] = true
	}
}
```

- [ ] **Step 4: Run tests**

```
go test ./... -run TestDoMotion
```

Expected: the new test passes. Any older test that was specifically asserting `GoForFunc` invocation must be updated in the same step to assert `GoToFunc` instead (grep `GoForFunc` in the test file and switch those cases).

- [ ] **Step 5: Checkpoint**

```
make test
```

---

### Task 5: Shared `powerMoveTo` helper

**Files:**
- Create: `power_move.go`
- Create: `power_move_test.go`

This is the reusable "SetPower → poll → Stop within tolerance" loop. No viam types in its signature — only plain function types — so it's trivially unit-testable and reusable across both axes.

- [ ] **Step 1: Write failing tests**

`power_move_test.go`:

```go
package hockeyplayer

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAxis models a simple 1-D actuator: SetPower drives position at a rate
// proportional to power; Position returns the current value; Stop zeroes power.
// Not thread-safe for concurrent SetPower / Stop calls — callers use it serially.
type fakeAxis struct {
	pos   float64
	power float64
	rate  float64 // units-per-second per unit power
	stopped int32
}

func (f *fakeAxis) Position(ctx context.Context) (float64, error) { return f.pos, nil }
func (f *fakeAxis) SetPower(ctx context.Context, p float64) error { f.power = p; return nil }
func (f *fakeAxis) Stop(ctx context.Context) error                { atomic.StoreInt32(&f.stopped, 1); f.power = 0; return nil }

// step advances the fake by dt seconds.
func (f *fakeAxis) step(dt float64) { f.pos += f.power * f.rate * dt }

func TestPowerMoveTo_ForwardReachesTarget(t *testing.T) {
	f := &fakeAxis{pos: 0, rate: 100} // at power=1, 100 units per second
	poll := 10 * time.Millisecond

	// advance `f` in a separate goroutine so the move progresses.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		ticker := time.NewTicker(poll)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				f.step(poll.Seconds())
			}
		}
	}()

	err := powerMoveTo(ctx, powerMove{
		readPos:  f.Position,
		setPower: f.SetPower,
		stop:     f.Stop,
		target:   5.0,
		power:    0.5,
		tol:      0.1,
		poll:     poll,
	})
	require.NoError(t, err)
	assert.InDelta(t, 5.0, f.pos, 0.5) // coast tolerance
	assert.Equal(t, int32(1), atomic.LoadInt32(&f.stopped))
}

func TestPowerMoveTo_ReverseReachesTarget(t *testing.T) {
	f := &fakeAxis{pos: 10, rate: 100}
	poll := 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		ticker := time.NewTicker(poll)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				f.step(poll.Seconds())
			}
		}
	}()

	err := powerMoveTo(ctx, powerMove{
		readPos:  f.Position,
		setPower: f.SetPower,
		stop:     f.Stop,
		target:   0.0,
		power:    0.5,
		tol:      0.1,
		poll:     poll,
	})
	require.NoError(t, err)
	assert.InDelta(t, 0.0, f.pos, 0.5)
}

func TestPowerMoveTo_AlreadyAtTarget(t *testing.T) {
	f := &fakeAxis{pos: 5, rate: 100}
	err := powerMoveTo(context.Background(), powerMove{
		readPos:  f.Position,
		setPower: f.SetPower,
		stop:     f.Stop,
		target:   5.0,
		power:    0.5,
		tol:      0.1,
		poll:     10 * time.Millisecond,
	})
	require.NoError(t, err)
	// SetPower should NOT have been called with a non-zero value, since we were already there.
	assert.Equal(t, 0.0, f.power)
}

func TestPowerMoveTo_ContextCancelTriggersStop(t *testing.T) {
	f := &fakeAxis{pos: 0, rate: 100}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()

	err := powerMoveTo(ctx, powerMove{
		readPos:  f.Position,
		setPower: f.SetPower,
		stop:     f.Stop,
		target:   1000.0, // unreachable before cancel
		power:    0.5,
		tol:      0.1,
		poll:     10 * time.Millisecond,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, int32(1), atomic.LoadInt32(&f.stopped))
}

func TestPowerMoveTo_SetPowerError(t *testing.T) {
	want := errors.New("set_power boom")
	err := powerMoveTo(context.Background(), powerMove{
		readPos:  func(ctx context.Context) (float64, error) { return 0, nil },
		setPower: func(ctx context.Context, p float64) error { return want },
		stop:     func(ctx context.Context) error { return nil },
		target:   5.0,
		power:    0.5,
		tol:      0.1,
		poll:     10 * time.Millisecond,
	})
	require.ErrorIs(t, err, want)
}
```

- [ ] **Step 2: Run tests to verify they fail**

```
go test ./... -run TestPowerMoveTo
```

Expected: compile error — `powerMoveTo` and `powerMove` don't exist yet.

- [ ] **Step 3: Implement `powerMoveTo`**

`power_move.go`:

```go
package hockeyplayer

import (
	"context"
	"fmt"
	"math"
	"time"
)

// powerMove configures a single SetPower-based move to a scalar target.
// The caller supplies axis-specific accessors so the same loop works for
// rotation (motor position read, motor SetPower/Stop) and translation
// (gantry position read, translation_motor SetPower/Stop).
type powerMove struct {
	readPos  func(ctx context.Context) (float64, error)
	setPower func(ctx context.Context, power float64) error
	stop     func(ctx context.Context) error

	target float64       // in the same units the reader returns
	power  float64       // magnitude in (0, 1]; direction is chosen from the sign of (target - current)
	tol    float64       // arrival tolerance in the same units
	poll   time.Duration // how often to sample position
}

// powerMoveTo drives the axis toward target using SetPower at the given
// magnitude, polling position at `poll` and stopping once within `tol` of
// target. It returns nil on arrival, ctx.Err()-wrapped on cancellation, and
// whatever the underlying setPower/stop/readPos returned on RPC failure.
// In all exit paths it calls stop() on a best-effort basis.
func powerMoveTo(ctx context.Context, m powerMove) error {
	cur, err := m.readPos(ctx)
	if err != nil {
		return fmt.Errorf("power move: reading initial position: %w", err)
	}
	delta := m.target - cur
	if math.Abs(delta) <= m.tol {
		return nil
	}
	direction := 1.0
	if delta < 0 {
		direction = -1.0
	}

	if err := m.setPower(ctx, direction*m.power); err != nil {
		return fmt.Errorf("power move: SetPower: %w", err)
	}

	ticker := time.NewTicker(m.poll)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// best-effort stop using a fresh background context so a cancelled
			// parent doesn't short-circuit the stop RPC
			_ = m.stop(context.Background())
			return ctx.Err()
		case <-ticker.C:
			cur, err := m.readPos(ctx)
			if err != nil {
				_ = m.stop(context.Background())
				return fmt.Errorf("power move: reading position: %w", err)
			}
			if math.Abs(m.target-cur) <= m.tol {
				if err := m.stop(ctx); err != nil {
					return fmt.Errorf("power move: Stop: %w", err)
				}
				return nil
			}
		}
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

```
go test ./... -run TestPowerMoveTo -v
```

Expected: all PASS.

- [ ] **Step 5: Checkpoint**

```
make test
```

---

### Task 6: Accept `rpm`/`power` mode in DoCommand; reject both; validate ranges

**Files:**
- Modify: `do_command.go`
- Modify: `do_command_test.go`

- [ ] **Step 1: Write failing tests**

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

```
go test ./... -run TestDoMotion_Rejects
```

Expected: FAIL — current code has no knowledge of `power`.

- [ ] **Step 3: Parse rpm/power in `doMotion`**

Near the top of `doMotion` in `do_command.go`, introduce a single `driveMode` value that later per-axis logic inspects:

```go
type driveMode int

const (
	modeRPM   driveMode = iota // default
	modePower
)

type drive struct {
	mode  driveMode
	rpm   float64 // populated when mode == modeRPM
	power float64 // populated when mode == modePower (magnitude in (0, 1])
	// For translation rpm we still need speed_mm_per_sec, which is separate
	// from rpm. It defaults from config and is read regardless of mode.
	speedMMPerSec float64
}
```

And a parser:

```go
func (s *hockeyPlayerHockeyPlayer) parseDrive(cmd map[string]interface{}) (drive, error) {
	rpmVal, rpmOK, err := optFloat(cmd, "rpm")
	if err != nil {
		return drive{}, err
	}
	pwrVal, pwrOK, err := optFloat(cmd, "power")
	if err != nil {
		return drive{}, err
	}
	if rpmOK && pwrOK {
		return drive{}, fmt.Errorf("cannot specify both 'rpm' and 'power'")
	}

	// speed_mm_per_sec is its own knob used in rpm-mode translation.
	speedVal, speedOK, err := optFloat(cmd, "speed_mm_per_sec")
	if err != nil {
		return drive{}, err
	}
	if speedOK && speedVal <= 0 {
		return drive{}, fmt.Errorf("'speed_mm_per_sec' must be > 0, got %v", speedVal)
	}
	speed := s.cfg.DefaultSpeedMMPerSec
	if speedOK {
		speed = speedVal
	}

	if pwrOK {
		if pwrVal <= 0 || pwrVal > 1 {
			return drive{}, fmt.Errorf("'power' must be in (0, 1], got %v", pwrVal)
		}
		return drive{mode: modePower, power: pwrVal, speedMMPerSec: speed}, nil
	}
	rpm := s.cfg.DefaultRPMRotation
	if rpmOK {
		if rpmVal <= 0 {
			return drive{}, fmt.Errorf("'rpm' must be > 0, got %v", rpmVal)
		}
		rpm = rpmVal
	}
	return drive{mode: modeRPM, rpm: rpm, speedMMPerSec: speed}, nil
}
```

Replace the existing `rpm_rotation`/`speed_mm_per_sec` parsing in `doMotion` with a single `drv, err := s.parseDrive(cmd)` call early, then branch on `drv.mode` in the per-axis blocks. (The rotation rpm branch uses `drv.rpm`; the translation rpm branch uses `drv.speedMMPerSec`.)

**Back-compat note:** the v1 spec used `rpm_rotation` (not `rpm`) for the speed knob. We're switching to `rpm` per the v2 spec's DoCommand shape. If you want to keep `rpm_rotation` as an alias, accept either in the parser — but the v2 spec uses only `rpm`, so do not add an alias unless the user asks.

- [ ] **Step 4: Run tests**

```
go test ./... -run TestDoMotion_Rejects
go test ./... -run TestDoMotion
```

Expected: the new rejects pass; previous rpm tests still pass after adjusting them to `rpm` (search for `"rpm_rotation"` in the test file and rename to `"rpm"` where applicable).

- [ ] **Step 5: Checkpoint**

```
make test
```

---

### Task 7: Power-mode rotation dispatch

**Files:**
- Modify: `do_command.go`
- Modify: `do_command_test.go`

- [ ] **Step 1: Write a failing test**

```go
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
```

- [ ] **Step 2: Run to verify failure**

```
go test ./... -run TestDoMotion_PowerRotation_UsesSetPowerPollStop
```

Expected: FAIL — `doMotion` has no power-mode branch yet.

- [ ] **Step 3: Wire power-mode rotation**

In `doMotion`, inside the rotation-handling block, branch on `drv.mode`. After computing `currentDeg`, `delta`, and the already-there early-return:

```go
if drv.mode == modeRPM {
	targetRev := posRev + delta/360.0
	motorRef := s.rotationMotor
	rpm := drv.rpm
	_ = s.rotationAxis.dispatch(s.cancelCtx, func(c context.Context) error {
		return motorRef.GoTo(c, rpm, targetRev, nil)
	})
	resp["r_dispatched"] = true
} else {
	// power mode
	motorRef := s.rotationMotor
	pwr := drv.power
	tol := s.cfg.RotationTolDeg()
	poll := time.Duration(defaultPowerPollInterval) * time.Millisecond
	currentAccumDeg := posRev * 360.0
	targetAccumDeg := currentAccumDeg + delta
	_ = s.rotationAxis.dispatch(s.cancelCtx, func(c context.Context) error {
		return powerMoveTo(c, powerMove{
			readPos: func(ctx context.Context) (float64, error) {
				p, err := motorRef.Position(ctx, nil)
				if err != nil {
					return 0, err
				}
				return p * 360.0, nil
			},
			setPower: func(ctx context.Context, p float64) error { return motorRef.SetPower(ctx, p, nil) },
			stop:     func(ctx context.Context) error { return motorRef.Stop(ctx, nil) },
			target:   targetAccumDeg,
			power:    pwr,
			tol:      tol,
			poll:     poll,
		})
	})
	resp["r_dispatched"] = true
}
```

Add `"time"` to the imports if not already present.

- [ ] **Step 4: Run tests**

```
go test ./... -run TestDoMotion_PowerRotation_UsesSetPowerPollStop
```

Expected: PASS.

- [ ] **Step 5: Checkpoint**

```
make test
```

---

### Task 8: Power-mode translation dispatch

**Files:**
- Modify: `do_command.go`
- Modify: `do_command_test.go`

- [ ] **Step 1: Write a failing test**

```go
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
```

- [ ] **Step 2: Run to verify failure**

```
go test ./... -run TestDoMotion_PowerTranslation_SetPowerOnTranslationMotor
```

Expected: FAIL.

- [ ] **Step 3: Wire power-mode translation**

In `doMotion`, inside the translation-handling block, branch on `drv.mode`:

```go
if drv.mode == modeRPM {
	currentPositions, err := s.gantry.Position(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("reading gantry position: %w", err)
	}
	targetMM := tToMM(tVal, s.cfg.MinTranslationMM, s.cfg.MaxTranslationMM)
	positions, err := buildPositions(currentPositions, s.cfg.TranslationAxisIndex, targetMM)
	if err != nil {
		return nil, err
	}
	speeds := make([]float64, len(positions))
	for i := range speeds {
		speeds[i] = drv.speedMMPerSec
	}
	gantryRef := s.gantry
	_ = s.translationAxis.dispatch(s.cancelCtx, func(c context.Context) error {
		return gantryRef.MoveToPosition(c, positions, speeds, nil)
	})
	resp["t_dispatched"] = true
} else {
	// power mode
	axisIdx := s.cfg.TranslationAxisIndex
	gantryRef := s.gantry
	translationMotorRef := s.translationMotor
	targetMM := tToMM(tVal, s.cfg.MinTranslationMM, s.cfg.MaxTranslationMM)
	pwr := drv.power
	tol := s.cfg.TranslationTolMM()
	poll := time.Duration(defaultPowerPollInterval) * time.Millisecond
	_ = s.translationAxis.dispatch(s.cancelCtx, func(c context.Context) error {
		return powerMoveTo(c, powerMove{
			readPos: func(ctx context.Context) (float64, error) {
				pos, err := gantryRef.Position(ctx, nil)
				if err != nil {
					return 0, err
				}
				if axisIdx >= len(pos) {
					return 0, fmt.Errorf("gantry returned %d axes but translation_axis_index is %d", len(pos), axisIdx)
				}
				return pos[axisIdx], nil
			},
			setPower: func(ctx context.Context, p float64) error { return translationMotorRef.SetPower(ctx, p, nil) },
			stop:     func(ctx context.Context) error { return translationMotorRef.Stop(ctx, nil) },
			target:   targetMM,
			power:    pwr,
			tol:      tol,
			poll:     poll,
		})
	})
	resp["t_dispatched"] = true
}
```

- [ ] **Step 4: Run tests**

```
go test ./... -run TestDoMotion_PowerTranslation
```

Expected: PASS.

- [ ] **Step 5: Checkpoint**

```
make test
```

---

### Task 9: Make `DoCommand` blocking; build `{status, t_final, r_final}` response

**Files:**
- Modify: `do_command.go`
- Modify: `do_command_test.go`

- [ ] **Step 1: Write failing tests**

```go
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
```

- [ ] **Step 2: Run to verify failure**

```
go test ./... -run TestDoMotion_Blocking
```

Expected: FAIL — `doMotion` currently returns `{"status": "dispatched", ...}` immediately.

- [ ] **Step 3: Change `doMotion` to wait and build final response**

At the top of `doMotion` after validation, replace `resp := map[string]interface{}{"status": "dispatched"}` with local bookkeeping:

```go
type axisJob struct {
	name string                                       // "rotation" or "translation"
	errCh <-chan error
}

jobs := make([]axisJob, 0, 2)
```

Whenever the code currently does `_ = s.rotationAxis.dispatch(...)`, change it to:

```go
ch := s.rotationAxis.dispatch(s.cancelCtx, func(c context.Context) error { /* ... */ })
jobs = append(jobs, axisJob{name: "rotation", errCh: ch})
```

And likewise for translation. Drop the old `"r_dispatched"` / `"t_dispatched"` bookkeeping — replace with final-value reads below.

After all dispatches, wait:

```go
var firstErr error
remaining := len(jobs)
for remaining > 0 {
	for i := range jobs {
		if jobs[i].errCh == nil {
			continue
		}
		select {
		case err := <-jobs[i].errCh:
			jobs[i].errCh = nil
			remaining--
			if err != nil && firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", jobs[i].name, err)
				// Preempt the other axis.
				if jobs[i].name == "rotation" {
					s.translationAxis.close()
				} else {
					s.rotationAxis.close()
				}
			}
		default:
		}
	}
	if remaining > 0 {
		// Tiny sleep to avoid busy-spinning; scale is fine for < 10Hz dispatch rate.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
}
if firstErr != nil {
	return nil, firstErr
}
```

*(Alternative: use `reflect.Select` over a slice of channels — cleaner but heavier. The polling loop above is fine for 2 channels.)*

Then read final positions for the axes we commanded:

```go
resp := map[string]interface{}{"status": "done"}

if tOK {
	gPos, err := s.gantry.Position(ctx, nil)
	if err == nil && s.cfg.TranslationAxisIndex < len(gPos) {
		resp["t_final"] = mmToT(gPos[s.cfg.TranslationAxisIndex], s.cfg.MinTranslationMM, s.cfg.MaxTranslationMM)
	}
}
if rOK {
	mPos, err := s.rotationMotor.Position(ctx, nil)
	if err == nil {
		resp["r_final"] = normalizeAngle(mPos)
	}
}
return resp, nil
```

Note on preemption via `close()`: that method, as written in `axis_controller.go`, cancels and stops then zeroes `cancel`/`done`. After an error-triggered preempt, the *losing* axis's err channel will still fire (the goroutine exits). The select loop handles that because it keeps reading until `remaining == 0`. The fact that `close()` sets `cancel = nil` is harmless — the next `dispatch()` will re-initialize.

- [ ] **Step 4: Run tests**

```
go test ./... -run TestDoMotion_Blocking
go test ./... -run TestDoMotion
```

Expected: new blocking tests PASS; older DoMotion tests that asserted `"dispatched"` need updating (change assertions to `"done"` and check `t_final`/`r_final` instead of `t_dispatched`/`r_dispatched`). Update them in this step.

- [ ] **Step 5: Final full-suite checkpoint**

```
make test
```

Expected: ENTIRE suite passes. If any test still expects v1 fire-and-forget semantics, it's stale — update it to the v2 blocking shape.

---

## Self-Review

**Spec coverage:**
- §1 architecture — three resources, axisController, dispatcher waits → Tasks 2, 3, 9.
- §2 config — new fields + validate + defaults → Task 1.
- §3 DoCommand shape and validation → Tasks 4, 6.
- §3 blocking + final response → Task 9.
- §3 position query — **unchanged from v1**, no task needed (existing `doGetPosition` still correct; verify no regressions via `make test`).
- §4 rotation math — `computeDelta`, `normalizeAngle` unchanged from v1 → no task.
- §4 rotation rpm-mode (GoTo) → Task 4.
- §4 rotation power-mode (accumulated degrees) → Task 7.
- §5 translation math — unchanged → no task.
- §5 translation rpm-mode → already in code, lightly updated in Task 6/8.
- §5 translation power-mode → Tasks 5, 8.
- §6 concurrency — `axisController` persists; `dispatch` returns err chan → Task 3; wait+preempt in Task 9.
- §6 translation stop fan-out → Task 2.
- §7 error handling — wired through Tasks 1, 6, 9.
- §8 startup/shutdown — Task 2 handles translation_motor in Close implicitly via `translationAxis.close` (which already calls `stopFn`, which now stops both gantry and translation_motor).
- §9 testing — each task carries its own tests; combine for coverage.
- §10 non-goals — nothing to implement.

**Placeholder scan:** none ("TBD", "TODO", "handle errors", "implement later" — grep turned up zero).

**Type consistency:** `drive` / `driveMode` / `powerMove` defined consistently. `dispatch` now `<-chan error` everywhere. Tolerance accessors `RotationTolDeg()` / `TranslationTolMM()` used both in startup log (Task 2) and in power-mode dispatches (Tasks 7, 8).

---

## Execution Handoff

Plan saved to `docs/superpowers/plans/2026-04-24-hockey-player-v2.md`.

**Two execution options:**

**1. Subagent-Driven (recommended)** — one fresh subagent per task, review between tasks. Clean context each run, easy to course-correct mid-plan.

**2. Inline Execution** — keep this conversation, batch through tasks with checkpoints for review.

Which approach?
