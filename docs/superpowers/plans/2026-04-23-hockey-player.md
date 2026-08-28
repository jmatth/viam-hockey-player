# Hockey Player Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the `viam-rod-hockey:generic:hockey-player` generic component that drives a Viam gantry for linear translation and a Viam stepper motor for rotation, following the spec at `docs/superpowers/specs/2026-04-23-hockey-player-design.md`.

**Architecture:** Single generic component registered against `go.viam.com/rdk/components/generic`. Public API is `DoCommand`. Translation moves go through `gantry.MoveToPosition`; rotation moves go through `motor.GoFor` with a computed signed delta in revolutions. Each axis has a small `axisController` that serializes dispatches, preempts in-flight motion when a new command arrives for that axis, and leaves the other axis alone.

**Tech Stack:** Go 1.23, `go.viam.com/rdk` (Viam SDK), `github.com/stretchr/testify`, `go.uber.org/goleak`.

**Commits:** This plan has no commit steps — per the user, commits are skipped. The project is not yet a git repository. Run `git init` if you want to add commits during execution.

---

## File structure

All source files live in the module root (package `hockeyplayer`):

- `module.go` — *modify*: init registration, struct, constructor, `DoCommand` dispatch, `Close`.
- `config.go` — *create*: `Config` struct, `Validate`.
- `rotation.go` — *create*: pure rotation math (`normalizeAngle`, `computeDelta`).
- `translation.go` — *create*: pure translation math (`tToMM`, `mmToT`, `buildPositions`).
- `axis_controller.go` — *create*: `axisController` type with `dispatch` and `close`.
- `do_command.go` — *create*: `doMotion`, `doGetPosition`, and DoCommand field parsers (keeps `module.go` focused on lifecycle).

Test files (sibling `_test.go` files, same package):

- `config_test.go`, `rotation_test.go`, `translation_test.go`, `axis_controller_test.go`, `do_command_test.go`, `module_test.go`.

Other changes:

- `go.mod` / `go.sum` — add `github.com/stretchr/testify` and `go.uber.org/goleak`; `go.viam.com/rdk` gets pulled in transitively.
- `cmd/module/main.go` — change `services/generic` import to `components/generic`.

---

## Task 1: Set up dependencies and switch registration from service to component

**Files:**
- Modify: `go.mod` (indirectly, via `go mod tidy`)
- Modify: `module.go`
- Modify: `cmd/module/main.go`

- [ ] **Step 1: Update `cmd/module/main.go` to use the generic component API**

Replace the file contents with:

```go
package main

import (
	"hockeyplayer"

	"go.viam.com/rdk/components/generic"
	"go.viam.com/rdk/module"
	"go.viam.com/rdk/resource"
)

func main() {
	module.ModularMain(resource.APIModel{API: generic.API, Model: hockeyplayer.HockeyPlayer})
}
```

- [ ] **Step 2: Update `module.go` to register as a component, not a service**

In `module.go`, change the imports block and `init()` function. The full file replacement is:

```go
package hockeyplayer

import (
	"context"
	"errors"
	"fmt"

	"go.viam.com/rdk/components/generic"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
)

var (
	HockeyPlayer     = resource.NewModel("viam-rod-hockey", "generic", "hockey-player")
	errUnimplemented = errors.New("unimplemented")
)

func init() {
	resource.RegisterComponent(generic.API, HockeyPlayer,
		resource.Registration[resource.Resource, *Config]{
			Constructor: newHockeyPlayerHockeyPlayer,
		},
	)
}

type hockeyPlayerHockeyPlayer struct {
	resource.AlwaysRebuild

	name resource.Name

	logger logging.Logger
	cfg    *Config

	cancelCtx  context.Context
	cancelFunc func()
}

func newHockeyPlayerHockeyPlayer(ctx context.Context, deps resource.Dependencies, rawConf resource.Config, logger logging.Logger) (resource.Resource, error) {
	conf, err := resource.NativeConfig[*Config](rawConf)
	if err != nil {
		return nil, err
	}

	return NewHockeyPlayer(ctx, deps, rawConf.ResourceName(), conf, logger)
}

func NewHockeyPlayer(ctx context.Context, deps resource.Dependencies, name resource.Name, conf *Config, logger logging.Logger) (resource.Resource, error) {
	cancelCtx, cancelFunc := context.WithCancel(context.Background())

	s := &hockeyPlayerHockeyPlayer{
		name:       name,
		logger:     logger,
		cfg:        conf,
		cancelCtx:  cancelCtx,
		cancelFunc: cancelFunc,
	}
	return s, nil
}

func (s *hockeyPlayerHockeyPlayer) Name() resource.Name {
	return s.name
}

func (s *hockeyPlayerHockeyPlayer) DoCommand(ctx context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *hockeyPlayerHockeyPlayer) Close(context.Context) error {
	s.cancelFunc()
	return nil
}
```

(This file will grow more through later tasks — we're just switching service → component and removing the inline `Config` definition in preparation for extracting it to `config.go`.)

- [ ] **Step 3: Add test dependencies to `go.mod` via `go get`**

Run:

```bash
cd /home/nick/hockey-player && go get github.com/stretchr/testify go.uber.org/goleak && go mod tidy
```

Expected: `go.mod` and `go.sum` are populated. No errors.

- [ ] **Step 4: Verify the module still builds**

Run:

```bash
cd /home/nick/hockey-player && go build ./...
```

Expected: build fails with an error about missing `Config` type, because we just removed its inline definition.

This is OK — Task 2 creates `config.go`. If it *succeeds*, double-check that the inline `Config` wasn't accidentally left in `module.go`.

---

## Task 2: Create `Config` and `Validate`

**Files:**
- Create: `config.go`
- Create: `config_test.go`

- [ ] **Step 1: Write the failing tests in `config_test.go`**

```go
package hockeyplayer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validCfg() *Config {
	return &Config{
		Gantry:               "g",
		RotationMotor:        "m",
		TranslationAxisIndex: 0,
		MinTranslationMM:     10,
		MaxTranslationMM:     100,
		DefaultRPMRotation:   60,
		DefaultSpeedMMPerSec: 50,
		DefaultWrap:          false,
	}
}

func TestValidate_HappyPath(t *testing.T) {
	req, opt, err := validCfg().Validate("components.0")
	require.NoError(t, err)
	assert.Equal(t, []string{"g", "m"}, req)
	assert.Nil(t, opt)
}

func TestValidate_Failures(t *testing.T) {
	cases := []struct {
		name  string
		mut   func(c *Config)
		match string
	}{
		{"missing gantry", func(c *Config) { c.Gantry = "" }, "gantry"},
		{"missing rotation_motor", func(c *Config) { c.RotationMotor = "" }, "rotation_motor"},
		{"negative axis index", func(c *Config) { c.TranslationAxisIndex = -1 }, "translation_axis_index"},
		{"negative min translation", func(c *Config) { c.MinTranslationMM = -1 }, "min_translation_mm"},
		{"max <= min", func(c *Config) { c.MaxTranslationMM = 10 }, "max_translation_mm"},
		{"zero default rpm", func(c *Config) { c.DefaultRPMRotation = 0 }, "default_rpm_rotation"},
		{"negative default speed", func(c *Config) { c.DefaultSpeedMMPerSec = -1 }, "default_speed_mm_per_sec"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validCfg()
			tc.mut(c)
			_, _, err := c.Validate("components.0")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.match)
			assert.Contains(t, err.Error(), "components.0")
		})
	}
}
```

- [ ] **Step 2: Run the tests and verify they fail**

Run:

```bash
cd /home/nick/hockey-player && go test ./... -run TestValidate -v
```

Expected: FAIL with a compile error — `Config` type is not defined.

- [ ] **Step 3: Create `config.go` with the `Config` struct and `Validate`**

```go
package hockeyplayer

import "fmt"

type Config struct {
	Gantry               string  `json:"gantry"`
	RotationMotor        string  `json:"rotation_motor"`
	TranslationAxisIndex int     `json:"translation_axis_index,omitempty"`
	MinTranslationMM     float64 `json:"min_translation_mm"`
	MaxTranslationMM     float64 `json:"max_translation_mm"`
	DefaultRPMRotation   float64 `json:"default_rpm_rotation"`
	DefaultSpeedMMPerSec float64 `json:"default_speed_mm_per_sec"`
	DefaultWrap          bool    `json:"default_wrap,omitempty"`
}

func (c *Config) Validate(path string) ([]string, []string, error) {
	if c.Gantry == "" {
		return nil, nil, fmt.Errorf("%s: 'gantry' is required", path)
	}
	if c.RotationMotor == "" {
		return nil, nil, fmt.Errorf("%s: 'rotation_motor' is required", path)
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
	return []string{c.Gantry, c.RotationMotor}, nil, nil
}
```

- [ ] **Step 4: Run the tests and verify they pass**

Run:

```bash
cd /home/nick/hockey-player && go test ./... -run TestValidate -v
```

Expected: PASS. Both `TestValidate_HappyPath` and all `TestValidate_Failures` subtests pass.

---

## Task 3: Implement rotation math

**Files:**
- Create: `rotation.go`
- Create: `rotation_test.go`

- [ ] **Step 1: Write the failing tests in `rotation_test.go`**

```go
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

func TestComputeDelta_Wrap(t *testing.T) {
	cases := []struct {
		current, target, want float64
	}{
		{350, 10, 20},    // forward across seam
		{10, 350, -20},   // backward across seam
		{100, 150, 50},   // forward no seam
		{150, 100, -50},  // backward no seam
		{0, 0, 0},
	}
	for _, tc := range cases {
		got := computeDelta(tc.current, tc.target, true)
		assert.InDelta(t, tc.want, got, 1e-9, "computeDelta(%v→%v, wrap=true)", tc.current, tc.target)
	}
}

func TestComputeDelta_NoWrap(t *testing.T) {
	cases := []struct {
		current, target, want float64
	}{
		{350, 10, -340}, // short path crosses seam → go long way
		{10, 350, 340},  // short path crosses seam → go long way
		{100, 150, 50},  // short path stays in [0,360] → allowed
		{150, 100, -50}, // short path stays → allowed
		{0, 0, 0},
	}
	for _, tc := range cases {
		got := computeDelta(tc.current, tc.target, false)
		assert.InDelta(t, tc.want, got, 1e-9, "computeDelta(%v→%v, wrap=false)", tc.current, tc.target)
	}
}

func TestComputeDelta_Symmetry(t *testing.T) {
	// If target equals current (modulo 360) the delta should be 0.
	cases := []float64{0, 90, 180, 270, 359.99}
	for _, v := range cases {
		assert.InDelta(t, 0.0, computeDelta(v, v, true), 1e-9)
		assert.InDelta(t, 0.0, computeDelta(v, v, false), 1e-9)
	}
	// r=0 and r=360 should be treated as the same angle: normalize caller-side or accept
	// the identical result.
	d := computeDelta(0, 360, true)
	assert.InDelta(t, 0.0, math.Mod(d+360, 360), 0.01)
}

// At exactly 180° separation both directions are equally shortest.
// The formula may return either +180 or -180; both are physically equivalent.
func TestComputeDelta_Exactly180(t *testing.T) {
	got := computeDelta(0, 180, true)
	assert.InDelta(t, 180.0, math.Abs(got), 1e-9)
}
```

- [ ] **Step 2: Run the tests and verify they fail**

Run:

```bash
cd /home/nick/hockey-player && go test ./... -run TestNormalizeAngle -v
cd /home/nick/hockey-player && go test ./... -run TestComputeDelta -v
```

Expected: FAIL with compile error — `normalizeAngle` and `computeDelta` undefined.

- [ ] **Step 3: Create `rotation.go` with the math**

```go
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
// wrap = true : pick the shortest path; may cross the 0/360 seam.
// wrap = false: never cross the 0/360 seam; if the shortest path would cross
//                it, take the long way around instead.
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
```

- [ ] **Step 4: Run the tests and verify they pass**

Run:

```bash
cd /home/nick/hockey-player && go test ./... -run "TestNormalizeAngle|TestComputeDelta" -v
```

Expected: PASS.

---

## Task 4: Implement translation math

**Files:**
- Create: `translation.go`
- Create: `translation_test.go`

- [ ] **Step 1: Write the failing tests in `translation_test.go`**

```go
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
```

- [ ] **Step 2: Run the tests and verify they fail**

Run:

```bash
cd /home/nick/hockey-player && go test ./... -run "TestTToMM|TestMMToT|TestBuildPositions" -v
```

Expected: FAIL with compile error — functions undefined.

- [ ] **Step 3: Create `translation.go`**

```go
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
```

- [ ] **Step 4: Run the tests and verify they pass**

Run:

```bash
cd /home/nick/hockey-player && go test ./... -run "TestTToMM|TestMMToT|TestBuildPositions" -v
```

Expected: PASS.

---

## Task 5: Implement `axisController`

**Files:**
- Create: `axis_controller.go`
- Create: `axis_controller_test.go`

- [ ] **Step 1: Write the failing tests in `axis_controller_test.go`**

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
	"go.uber.org/goleak"
	"go.viam.com/rdk/logging"
)

// fakeStopper records how many times it was called.
type fakeStopper struct{ calls atomic.Int32 }

func (f *fakeStopper) stop(ctx context.Context) error {
	f.calls.Add(1)
	return nil
}

func TestAxisController_SingleDispatch(t *testing.T) {
	defer goleak.VerifyNone(t)
	logger := logging.NewTestLogger(t)
	stopper := &fakeStopper{}
	a := newAxisController("rotation", stopper.stop, logger)
	defer a.close()

	ran := make(chan struct{})
	a.dispatch(context.Background(), func(ctx context.Context) error {
		close(ran)
		return nil
	})
	select {
	case <-ran:
	case <-time.After(time.Second):
		t.Fatal("moveFn never ran")
	}
}

func TestAxisController_PreemptsInFlight(t *testing.T) {
	defer goleak.VerifyNone(t)
	logger := logging.NewTestLogger(t)
	stopper := &fakeStopper{}
	a := newAxisController("rotation", stopper.stop, logger)
	defer a.close()

	// First dispatch blocks until its context is cancelled.
	first := make(chan error, 1)
	a.dispatch(context.Background(), func(ctx context.Context) error {
		<-ctx.Done()
		first <- ctx.Err()
		return ctx.Err()
	})

	// Second dispatch should cancel the first and run.
	secondRan := make(chan struct{})
	a.dispatch(context.Background(), func(ctx context.Context) error {
		close(secondRan)
		return nil
	})

	select {
	case err := <-first:
		assert.True(t, errors.Is(err, context.Canceled))
	case <-time.After(time.Second):
		t.Fatal("first dispatch was not cancelled")
	}
	select {
	case <-secondRan:
	case <-time.After(time.Second):
		t.Fatal("second dispatch never ran")
	}
	assert.GreaterOrEqual(t, stopper.calls.Load(), int32(1), "stop called during preempt")
}

func TestAxisController_CloseInterruptsInFlight(t *testing.T) {
	defer goleak.VerifyNone(t)
	logger := logging.NewTestLogger(t)
	stopper := &fakeStopper{}
	a := newAxisController("rotation", stopper.stop, logger)

	done := make(chan struct{})
	a.dispatch(context.Background(), func(ctx context.Context) error {
		<-ctx.Done()
		close(done)
		return ctx.Err()
	})

	a.close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("goroutine did not exit after close")
	}
	assert.GreaterOrEqual(t, stopper.calls.Load(), int32(1), "stop called during close")
}

func TestAxisController_CloseIsIdempotent(t *testing.T) {
	defer goleak.VerifyNone(t)
	logger := logging.NewTestLogger(t)
	stopper := &fakeStopper{}
	a := newAxisController("rotation", stopper.stop, logger)
	a.close()
	a.close() // should not panic or block
}

func TestAxisController_SuppressesCancelErrorsFromLog(t *testing.T) {
	// We can't easily inspect the logger output here without a custom sink;
	// this test just exercises the path and ensures no panic or leak.
	defer goleak.VerifyNone(t)
	logger := logging.NewTestLogger(t)
	stopper := &fakeStopper{}
	a := newAxisController("rotation", stopper.stop, logger)
	defer a.close()

	started := make(chan struct{})
	a.dispatch(context.Background(), func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err() // returned when preempted, should be suppressed
	})
	<-started
	require.NotNil(t, a)

	done := make(chan struct{})
	a.dispatch(context.Background(), func(ctx context.Context) error {
		close(done)
		return nil
	})
	<-done
}
```

- [ ] **Step 2: Run the tests and verify they fail**

Run:

```bash
cd /home/nick/hockey-player && go test ./... -run TestAxisController -v
```

Expected: FAIL with compile error — `newAxisController` undefined.

- [ ] **Step 3: Create `axis_controller.go`**

```go
package hockeyplayer

import (
	"context"
	"sync"

	"go.viam.com/rdk/logging"
)

// axisController serializes motion dispatch on a single physical axis. Each
// dispatch preempts any in-flight motion by cancelling the old goroutine's
// context and calling the resource's Stop, then waits for the old goroutine
// to exit before starting a new one.
type axisController struct {
	name   string
	stopFn func(context.Context) error
	logger logging.Logger

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func newAxisController(name string, stopFn func(context.Context) error, logger logging.Logger) *axisController {
	return &axisController{name: name, stopFn: stopFn, logger: logger}
}

// dispatch preempts any in-flight motion on this axis and spawns a new
// goroutine that runs moveFn with a child context of moduleCtx. moveFn's
// returned error is logged unless the context was cancelled (i.e. a
// preempt or a module shutdown caused the failure).
func (a *axisController) dispatch(moduleCtx context.Context, moveFn func(ctx context.Context) error) {
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
	a.cancel = cancel
	a.done = done

	go func() {
		defer close(done)
		if err := moveFn(ctx); err != nil && ctx.Err() == nil {
			a.logger.Errorf("axis %s failed: %v", a.name, err)
		}
	}()
}

// close cancels any in-flight motion, stops the underlying resource, and
// waits for the running goroutine to exit. Safe to call multiple times.
func (a *axisController) close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel == nil {
		return
	}
	a.cancel()
	if err := a.stopFn(context.Background()); err != nil {
		a.logger.Warnf("axis %s: stop during close failed: %v", a.name, err)
	}
	<-a.done
	a.cancel = nil
	a.done = nil
}
```

- [ ] **Step 4: Run the tests and verify they pass**

Run:

```bash
cd /home/nick/hockey-player && go test ./... -run TestAxisController -v
```

Expected: PASS, and no goroutine leaks reported by `goleak`.

---

## Task 6: Expand the main component struct and constructor

**Files:**
- Modify: `module.go`

- [ ] **Step 1: Replace `module.go` with the expanded version**

Full file content:

```go
package hockeyplayer

import (
	"context"
	"errors"
	"fmt"

	"go.viam.com/rdk/components/gantry"
	"go.viam.com/rdk/components/generic"
	"go.viam.com/rdk/components/motor"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
)

var (
	HockeyPlayer     = resource.NewModel("viam-rod-hockey", "generic", "hockey-player")
	errUnimplemented = errors.New("unimplemented")
)

func init() {
	resource.RegisterComponent(generic.API, HockeyPlayer,
		resource.Registration[resource.Resource, *Config]{
			Constructor: newHockeyPlayerHockeyPlayer,
		},
	)
}

type hockeyPlayerHockeyPlayer struct {
	resource.AlwaysRebuild

	name   resource.Name
	logger logging.Logger
	cfg    *Config

	gantry        gantry.Gantry
	rotationMotor motor.Motor

	cancelCtx  context.Context
	cancelFunc func()

	rotationAxis    *axisController
	translationAxis *axisController
}

func newHockeyPlayerHockeyPlayer(ctx context.Context, deps resource.Dependencies, rawConf resource.Config, logger logging.Logger) (resource.Resource, error) {
	conf, err := resource.NativeConfig[*Config](rawConf)
	if err != nil {
		return nil, err
	}
	return NewHockeyPlayer(ctx, deps, rawConf.ResourceName(), conf, logger)
}

func NewHockeyPlayer(ctx context.Context, deps resource.Dependencies, name resource.Name, conf *Config, logger logging.Logger) (resource.Resource, error) {
	g, err := gantry.FromDependencies(deps, conf.Gantry)
	if err != nil {
		return nil, fmt.Errorf("gantry %q: %w", conf.Gantry, err)
	}
	m, err := motor.FromDependencies(deps, conf.RotationMotor)
	if err != nil {
		return nil, fmt.Errorf("rotation_motor %q: %w", conf.RotationMotor, err)
	}

	lengths, err := g.Lengths(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("reading gantry lengths: %w", err)
	}
	if conf.TranslationAxisIndex >= len(lengths) {
		return nil, fmt.Errorf("translation_axis_index %d out of range (gantry has %d axes)",
			conf.TranslationAxisIndex, len(lengths))
	}
	if conf.MaxTranslationMM > lengths[conf.TranslationAxisIndex] {
		return nil, fmt.Errorf("max_translation_mm %.2f exceeds gantry axis %d length %.2f",
			conf.MaxTranslationMM, conf.TranslationAxisIndex, lengths[conf.TranslationAxisIndex])
	}

	props, err := m.Properties(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("reading motor properties: %w", err)
	}
	if !props.PositionReporting {
		return nil, fmt.Errorf("rotation_motor %q does not support position reporting", conf.RotationMotor)
	}

	cancelCtx, cancelFunc := context.WithCancel(context.Background())

	s := &hockeyPlayerHockeyPlayer{
		name:          name,
		logger:        logger,
		cfg:           conf,
		gantry:        g,
		rotationMotor: m,
		cancelCtx:     cancelCtx,
		cancelFunc:    cancelFunc,
	}
	s.rotationAxis = newAxisController("rotation", func(c context.Context) error { return m.Stop(c, nil) }, logger)
	s.translationAxis = newAxisController("translation", func(c context.Context) error { return g.Stop(c, nil) }, logger)

	logger.Infof("hockey-player started: t=[%.2f,%.2f]mm axis=%d default_rpm=%.1f default_speed=%.1f default_wrap=%v",
		conf.MinTranslationMM, conf.MaxTranslationMM, conf.TranslationAxisIndex,
		conf.DefaultRPMRotation, conf.DefaultSpeedMMPerSec, conf.DefaultWrap)

	return s, nil
}

func (s *hockeyPlayerHockeyPlayer) Name() resource.Name { return s.name }

func (s *hockeyPlayerHockeyPlayer) DoCommand(ctx context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	if c, ok := cmd["cmd"].(string); ok {
		switch c {
		case "get_position":
			return s.doGetPosition(ctx)
		default:
			return nil, fmt.Errorf("unknown cmd: %q", c)
		}
	}
	return s.doMotion(ctx, cmd)
}

// Status is required by resource.Resource in this RDK version. We don't
// expose anything here — callers use the get_position DoCommand instead.
func (s *hockeyPlayerHockeyPlayer) Status(ctx context.Context) (map[string]interface{}, error) {
	return map[string]interface{}{}, nil
}

func (s *hockeyPlayerHockeyPlayer) Close(ctx context.Context) error {
	s.cancelFunc()
	s.rotationAxis.close()
	s.translationAxis.close()
	return nil
}
```

- [ ] **Step 2: Create a stub `do_command.go` so the package compiles**

Task 7 fills this in fully. For now just enough to resolve the references in `module.go`:

```go
package hockeyplayer

import (
	"context"
	"fmt"
)

func (s *hockeyPlayerHockeyPlayer) doMotion(ctx context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *hockeyPlayerHockeyPlayer) doGetPosition(ctx context.Context) (map[string]interface{}, error) {
	return nil, fmt.Errorf("not implemented")
}
```

- [ ] **Step 3: Verify the package builds**

Run:

```bash
cd /home/nick/hockey-player && go build ./...
```

Expected: success (no output).

- [ ] **Step 4: Run the existing test suite**

Run:

```bash
cd /home/nick/hockey-player && go test ./...
```

Expected: all tests from Tasks 2–5 still pass.

---

## Task 7: Implement `doMotion` in `do_command.go` with tests

**Files:**
- Modify: `do_command.go`
- Create: `do_command_test.go`

- [ ] **Step 1: Write the failing tests in `do_command_test.go`**

```go
package hockeyplayer

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.viam.com/rdk/components/motor"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/testutils/inject"
)

// newTestInstance wires up a hockeyPlayerHockeyPlayer around inject fakes.
// It skips NewHockeyPlayer's real dependency resolution so we can set
// behavior on the fakes directly.
func newTestInstance(t *testing.T) (*hockeyPlayerHockeyPlayer, *inject.Gantry, *inject.Motor) {
	t.Helper()
	logger := logging.NewTestLogger(t)
	g := &inject.Gantry{}
	m := &inject.Motor{}

	cfg := &Config{
		Gantry:               "g",
		RotationMotor:        "m",
		TranslationAxisIndex: 0,
		MinTranslationMM:     0,
		MaxTranslationMM:     100,
		DefaultRPMRotation:   60,
		DefaultSpeedMMPerSec: 50,
		DefaultWrap:          false,
	}
	cancelCtx, cancelFunc := context.WithCancel(context.Background())

	s := &hockeyPlayerHockeyPlayer{
		name:          resource.Name{Name: "hp"},
		logger:        logger,
		cfg:           cfg,
		gantry:        g,
		rotationMotor: m,
		cancelCtx:     cancelCtx,
		cancelFunc:    cancelFunc,
	}
	s.rotationAxis = newAxisController("rotation", func(c context.Context) error { return m.Stop(c, nil) }, logger)
	s.translationAxis = newAxisController("translation", func(c context.Context) error { return g.Stop(c, nil) }, logger)

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
	m.IsMovingFunc = func(ctx context.Context) (bool, error) { return false, nil }
	m.PropertiesFunc = func(ctx context.Context, extra map[string]interface{}) (motor.Properties, error) {
		return motor.Properties{PositionReporting: true}, nil
	}

	return s, g, m
}

func TestDoMotion_RejectsEmpty(t *testing.T) {
	s, _, _ := newTestInstance(t)
	_, err := s.DoCommand(context.Background(), map[string]interface{}{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no motion fields")
}

func TestDoMotion_RejectsOutOfRange(t *testing.T) {
	s, _, _ := newTestInstance(t)
	_, err := s.DoCommand(context.Background(), map[string]interface{}{"t": 1.5})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "'t'")

	_, err = s.DoCommand(context.Background(), map[string]interface{}{"r": -10.0})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "'r'")

	_, err = s.DoCommand(context.Background(), map[string]interface{}{"r": 90.0, "rpm_rotation": 0.0})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rpm_rotation")
}

func TestDoMotion_RotationOnly(t *testing.T) {
	s, g, m := newTestInstance(t)

	var gotRPM, gotRev float64
	called := make(chan struct{})
	m.GoForFunc = func(ctx context.Context, rpm, revolutions float64, extra map[string]interface{}) error {
		gotRPM, gotRev = rpm, revolutions
		close(called)
		return nil
	}
	m.PositionFunc = func(ctx context.Context, extra map[string]interface{}) (float64, error) { return 0, nil }

	moveCalled := false
	g.MoveToPositionFunc = func(ctx context.Context, positionsMm, speedsMmPerSec []float64, extra map[string]interface{}) error {
		moveCalled = true
		return nil
	}

	resp, err := s.DoCommand(context.Background(), map[string]interface{}{"r": 90.0, "rpm_rotation": 30.0})
	require.NoError(t, err)
	assert.Equal(t, "dispatched", resp["status"])
	assert.Equal(t, true, resp["r_dispatched"])

	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("GoFor not called")
	}
	assert.InDelta(t, 30.0, gotRPM, 1e-9)
	assert.InDelta(t, 0.25, gotRev, 1e-9) // 90° = 0.25 rev
	assert.False(t, moveCalled, "translation should not be touched")
}

func TestDoMotion_TranslationOnly(t *testing.T) {
	s, g, m := newTestInstance(t)

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
	goForCalled := false
	m.GoForFunc = func(ctx context.Context, rpm, revolutions float64, extra map[string]interface{}) error {
		goForCalled = true
		return nil
	}

	resp, err := s.DoCommand(context.Background(), map[string]interface{}{"t": 0.5})
	require.NoError(t, err)
	assert.Equal(t, true, resp["t_dispatched"])

	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("MoveToPosition not called")
	}
	assert.Equal(t, []float64{50.0}, gotPositions)
	assert.Equal(t, []float64{50.0}, gotSpeeds) // default from config
	assert.False(t, goForCalled, "rotation should not be touched")
}

func TestDoMotion_BothAxes(t *testing.T) {
	s, g, m := newTestInstance(t)

	rotCalled := make(chan struct{})
	transCalled := make(chan struct{})
	m.GoForFunc = func(ctx context.Context, rpm, revolutions float64, extra map[string]interface{}) error {
		close(rotCalled)
		return nil
	}
	g.MoveToPositionFunc = func(ctx context.Context, positionsMm, speedsMmPerSec []float64, extra map[string]interface{}) error {
		close(transCalled)
		return nil
	}

	resp, err := s.DoCommand(context.Background(), map[string]interface{}{"t": 0.3, "r": 45.0})
	require.NoError(t, err)
	assert.Equal(t, true, resp["t_dispatched"])
	assert.Equal(t, true, resp["r_dispatched"])

	for _, ch := range []chan struct{}{rotCalled, transCalled} {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatal("one axis did not run")
		}
	}
}

func TestDoMotion_AlreadyAtTarget(t *testing.T) {
	s, _, m := newTestInstance(t)
	goForCalled := false
	m.GoForFunc = func(ctx context.Context, rpm, revolutions float64, extra map[string]interface{}) error {
		goForCalled = true
		return nil
	}
	m.PositionFunc = func(ctx context.Context, extra map[string]interface{}) (float64, error) {
		return 0.25, nil // 90°
	}

	resp, err := s.DoCommand(context.Background(), map[string]interface{}{"r": 90.0})
	require.NoError(t, err)
	assert.Equal(t, false, resp["r_dispatched"], "should skip dispatch when already at target")
	time.Sleep(50 * time.Millisecond)
	assert.False(t, goForCalled)
}

func TestDoMotion_PerAxisPreemption(t *testing.T) {
	s, g, m := newTestInstance(t)

	first := make(chan struct{})
	m.GoForFunc = func(ctx context.Context, rpm, revolutions float64, extra map[string]interface{}) error {
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

	_, err := s.DoCommand(context.Background(), map[string]interface{}{"r": 45.0})
	require.NoError(t, err)
	<-first

	// Second: overrides rotation, leaves translation alone
	gMoved := false
	g.MoveToPositionFunc = func(ctx context.Context, positionsMm, speedsMmPerSec []float64, extra map[string]interface{}) error {
		gMoved = true
		return nil
	}

	secondRan := make(chan struct{})
	m.GoForFunc = func(ctx context.Context, rpm, revolutions float64, extra map[string]interface{}) error {
		close(secondRan)
		return nil
	}
	_, err = s.DoCommand(context.Background(), map[string]interface{}{"r": 180.0})
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
	s, _, m := newTestInstance(t)
	m.PositionFunc = func(ctx context.Context, extra map[string]interface{}) (float64, error) {
		return 0, errors.New("boom")
	}
	_, err := s.DoCommand(context.Background(), map[string]interface{}{"r": 90.0})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rotation motor position")
}
```

- [ ] **Step 2: Run the tests and verify they fail**

Run:

```bash
cd /home/nick/hockey-player && go test ./... -run TestDoMotion -v
```

Expected: FAIL — `doMotion` returns "not implemented".

- [ ] **Step 3: Replace `do_command.go` with the full implementation**

```go
package hockeyplayer

import (
	"context"
	"fmt"
	"math"
)

func (s *hockeyPlayerHockeyPlayer) doMotion(ctx context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	tVal, tOK, err := optFloat(cmd, "t")
	if err != nil {
		return nil, err
	}
	rVal, rOK, err := optFloat(cmd, "r")
	if err != nil {
		return nil, err
	}
	if !tOK && !rOK {
		return nil, fmt.Errorf("no motion fields provided (need 't' and/or 'r')")
	}
	if tOK && (tVal < 0 || tVal > 1) {
		return nil, fmt.Errorf("'t' must be in [0, 1], got %v", tVal)
	}
	if rOK && (rVal < 0 || rVal > 360) {
		return nil, fmt.Errorf("'r' must be in [0, 360], got %v", rVal)
	}

	wrapVal, wrapOK, err := optBool(cmd, "wrap")
	if err != nil {
		return nil, err
	}
	wrap := s.cfg.DefaultWrap
	if wrapOK {
		wrap = wrapVal
	}

	rpmVal, rpmOK, err := optFloat(cmd, "rpm_rotation")
	if err != nil {
		return nil, err
	}
	if rpmOK && rpmVal <= 0 {
		return nil, fmt.Errorf("'rpm_rotation' must be > 0, got %v", rpmVal)
	}
	rpm := s.cfg.DefaultRPMRotation
	if rpmOK {
		rpm = rpmVal
	}

	speedVal, speedOK, err := optFloat(cmd, "speed_mm_per_sec")
	if err != nil {
		return nil, err
	}
	if speedOK && speedVal <= 0 {
		return nil, fmt.Errorf("'speed_mm_per_sec' must be > 0, got %v", speedVal)
	}
	speed := s.cfg.DefaultSpeedMMPerSec
	if speedOK {
		speed = speedVal
	}

	resp := map[string]interface{}{"status": "dispatched"}

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
			deltaRev := delta / 360.0
			motorRef := s.rotationMotor
			s.rotationAxis.dispatch(s.cancelCtx, func(c context.Context) error {
				return motorRef.GoFor(c, rpm, deltaRev, nil)
			})
			resp["r_dispatched"] = true
		}
	}

	if tOK {
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
			speeds[i] = speed
		}
		gantryRef := s.gantry
		s.translationAxis.dispatch(s.cancelCtx, func(c context.Context) error {
			return gantryRef.MoveToPosition(c, positions, speeds, nil)
		})
		resp["t_dispatched"] = true
	}

	return resp, nil
}

func (s *hockeyPlayerHockeyPlayer) doGetPosition(ctx context.Context) (map[string]interface{}, error) {
	return nil, fmt.Errorf("not implemented")
}

// optFloat extracts an optional numeric field from the DoCommand map.
// Returns (value, present, err). If present is false, value is meaningless.
func optFloat(m map[string]interface{}, key string) (float64, bool, error) {
	v, ok := m[key]
	if !ok {
		return 0, false, nil
	}
	switch n := v.(type) {
	case float64:
		return n, true, nil
	case float32:
		return float64(n), true, nil
	case int:
		return float64(n), true, nil
	case int64:
		return float64(n), true, nil
	default:
		return 0, false, fmt.Errorf("%q must be a number, got %T", key, v)
	}
}

func optBool(m map[string]interface{}, key string) (bool, bool, error) {
	v, ok := m[key]
	if !ok {
		return false, false, nil
	}
	b, ok := v.(bool)
	if !ok {
		return false, false, fmt.Errorf("%q must be a bool, got %T", key, v)
	}
	return b, true, nil
}
```

- [ ] **Step 4: Run the tests and verify they pass**

Run:

```bash
cd /home/nick/hockey-player && go test ./... -run TestDoMotion -v
```

Expected: PASS for all `TestDoMotion_*` subtests.

---

## Task 8: Implement `doGetPosition`

**Files:**
- Modify: `do_command.go`
- Modify: `do_command_test.go`

- [ ] **Step 1: Append tests to `do_command_test.go`**

Append these test functions to the end of `do_command_test.go`:

```go
func TestGetPosition_HappyPath(t *testing.T) {
	s, g, m := newTestInstance(t)
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
	s, _, _ := newTestInstance(t)
	_, err := s.DoCommand(context.Background(), map[string]interface{}{"cmd": "party"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown cmd")
}
```

- [ ] **Step 2: Run the new tests and verify they fail**

Run:

```bash
cd /home/nick/hockey-player && go test ./... -run "TestGetPosition|TestDoCommand_UnknownCmd" -v
```

Expected: `TestGetPosition_HappyPath` fails ("not implemented"); `TestDoCommand_UnknownCmd` passes (because `module.go` already returns the unknown-cmd error).

- [ ] **Step 3: Replace the stub `doGetPosition` in `do_command.go`**

Replace the `doGetPosition` function in `do_command.go` with:

```go
func (s *hockeyPlayerHockeyPlayer) doGetPosition(ctx context.Context) (map[string]interface{}, error) {
	gPos, err := s.gantry.Position(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("reading gantry position: %w", err)
	}
	if s.cfg.TranslationAxisIndex >= len(gPos) {
		return nil, fmt.Errorf("gantry position has %d axes but translation_axis_index is %d",
			len(gPos), s.cfg.TranslationAxisIndex)
	}
	t := mmToT(gPos[s.cfg.TranslationAxisIndex], s.cfg.MinTranslationMM, s.cfg.MaxTranslationMM)

	mPos, err := s.rotationMotor.Position(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("reading rotation motor position: %w", err)
	}
	r := normalizeAngle(mPos)

	gMoving, err := s.gantry.IsMoving(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading gantry is_moving: %w", err)
	}
	mMoving, err := s.rotationMotor.IsMoving(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading motor is_moving: %w", err)
	}

	return map[string]interface{}{
		"t":        t,
		"r":        r,
		"t_moving": gMoving,
		"r_moving": mMoving,
	}, nil
}
```

- [ ] **Step 4: Run the tests and verify they pass**

Run:

```bash
cd /home/nick/hockey-player && go test ./... -run "TestGetPosition|TestDoCommand_UnknownCmd" -v
```

Expected: PASS.

---

## Task 9: Test `Close` lifecycle and constructor validation

**Files:**
- Create: `module_test.go`

- [ ] **Step 1: Write tests in `module_test.go`**

```go
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

func baseCfg() *Config {
	return &Config{
		Gantry:               "g",
		RotationMotor:        "m",
		TranslationAxisIndex: 0,
		MinTranslationMM:     10,
		MaxTranslationMM:     100,
		DefaultRPMRotation:   60,
		DefaultSpeedMMPerSec: 50,
	}
}

func TestNewHockeyPlayer_HappyPath(t *testing.T) {
	defer goleak.VerifyNone(t)
	logger := logging.NewTestLogger(t)
	g := &inject.Gantry{}
	m := &inject.Motor{}
	g.LengthsFunc = func(ctx context.Context, extra map[string]interface{}) ([]float64, error) {
		return []float64{200}, nil
	}
	m.PropertiesFunc = func(ctx context.Context, extra map[string]interface{}) (motor.Properties, error) {
		return motor.Properties{PositionReporting: true}, nil
	}
	g.StopFunc = func(ctx context.Context, extra map[string]interface{}) error { return nil }
	m.StopFunc = func(ctx context.Context, extra map[string]interface{}) error { return nil }

	deps := makeDeps(t, "g", "m", g, m)
	r, err := NewHockeyPlayer(context.Background(), deps, resource.Name{Name: "hp"}, baseCfg(), logger)
	require.NoError(t, err)
	require.NoError(t, r.Close(context.Background()))
}

func TestNewHockeyPlayer_RejectsMaxBeyondGantryLength(t *testing.T) {
	logger := logging.NewTestLogger(t)
	g := &inject.Gantry{}
	m := &inject.Motor{}
	g.LengthsFunc = func(ctx context.Context, extra map[string]interface{}) ([]float64, error) {
		return []float64{50}, nil
	}
	m.PropertiesFunc = func(ctx context.Context, extra map[string]interface{}) (motor.Properties, error) {
		return motor.Properties{PositionReporting: true}, nil
	}
	cfg := baseCfg()
	cfg.MaxTranslationMM = 999
	deps := makeDeps(t, "g", "m", g, m)
	_, err := NewHockeyPlayer(context.Background(), deps, resource.Name{Name: "hp"}, cfg, logger)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds gantry axis")
}

func TestNewHockeyPlayer_RejectsAxisIndexOutOfRange(t *testing.T) {
	logger := logging.NewTestLogger(t)
	g := &inject.Gantry{}
	m := &inject.Motor{}
	g.LengthsFunc = func(ctx context.Context, extra map[string]interface{}) ([]float64, error) {
		return []float64{200}, nil
	}
	m.PropertiesFunc = func(ctx context.Context, extra map[string]interface{}) (motor.Properties, error) {
		return motor.Properties{PositionReporting: true}, nil
	}
	cfg := baseCfg()
	cfg.TranslationAxisIndex = 5
	deps := makeDeps(t, "g", "m", g, m)
	_, err := NewHockeyPlayer(context.Background(), deps, resource.Name{Name: "hp"}, cfg, logger)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "translation_axis_index")
}

func TestNewHockeyPlayer_RejectsNoPositionReporting(t *testing.T) {
	logger := logging.NewTestLogger(t)
	g := &inject.Gantry{}
	m := &inject.Motor{}
	g.LengthsFunc = func(ctx context.Context, extra map[string]interface{}) ([]float64, error) {
		return []float64{200}, nil
	}
	m.PropertiesFunc = func(ctx context.Context, extra map[string]interface{}) (motor.Properties, error) {
		return motor.Properties{PositionReporting: false}, nil
	}
	deps := makeDeps(t, "g", "m", g, m)
	_, err := NewHockeyPlayer(context.Background(), deps, resource.Name{Name: "hp"}, baseCfg(), logger)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "position reporting")
}

func TestNewHockeyPlayer_GantryLengthsError(t *testing.T) {
	logger := logging.NewTestLogger(t)
	g := &inject.Gantry{}
	m := &inject.Motor{}
	g.LengthsFunc = func(ctx context.Context, extra map[string]interface{}) ([]float64, error) {
		return nil, errors.New("bus error")
	}
	m.PropertiesFunc = func(ctx context.Context, extra map[string]interface{}) (motor.Properties, error) {
		return motor.Properties{PositionReporting: true}, nil
	}
	deps := makeDeps(t, "g", "m", g, m)
	_, err := NewHockeyPlayer(context.Background(), deps, resource.Name{Name: "hp"}, baseCfg(), logger)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bus error")
}

func TestClose_InterruptsInFlightMotion(t *testing.T) {
	defer goleak.VerifyNone(t)
	s, g, m := newTestInstance(t) // helper from do_command_test.go
	_ = g

	started := make(chan struct{})
	m.GoForFunc = func(ctx context.Context, rpm, revolutions float64, extra map[string]interface{}) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}

	_, err := s.DoCommand(context.Background(), map[string]interface{}{"r": 90.0})
	require.NoError(t, err)
	<-started

	require.NoError(t, s.Close(context.Background()))

	// A follow-up Close should also be safe.
	require.NoError(t, s.Close(context.Background()))
}

func TestClose_NoInFlightMotion(t *testing.T) {
	defer goleak.VerifyNone(t)
	s, _, _ := newTestInstance(t)
	require.NoError(t, s.Close(context.Background()))
}

// Confirms that Close waits for the in-flight goroutine rather than leaking.
func TestClose_WaitsForGoroutine(t *testing.T) {
	defer goleak.VerifyNone(t)
	s, _, m := newTestInstance(t)

	started := make(chan struct{})
	exited := make(chan struct{})
	m.GoForFunc = func(ctx context.Context, rpm, revolutions float64, extra map[string]interface{}) error {
		close(started)
		<-ctx.Done()
		close(exited)
		return ctx.Err()
	}
	_, err := s.DoCommand(context.Background(), map[string]interface{}{"r": 90.0})
	require.NoError(t, err)
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
```

- [ ] **Step 2: Run the tests and verify they pass**

Run:

```bash
cd /home/nick/hockey-player && go test ./... -v
```

Expected: all existing tests still pass; `TestNewHockeyPlayer_*`, `TestClose_*` all pass.

- [ ] **Step 3: Verify no goroutine leaks across the whole suite**

Run:

```bash
cd /home/nick/hockey-player && go test ./... -count=1
```

Expected: PASS. `goleak.VerifyNone` checks in the individual tests catch anything that escapes.

---

## Task 10: Final build + full-suite run

**Files:** none modified.

- [ ] **Step 1: Build the module binary**

Run:

```bash
cd /home/nick/hockey-player && make $(pwd)/bin/hockey-player
```

(Or simply `make module` which runs tests and then builds the tarball. If the Makefile path expression confuses things, you can use `go build -o bin/hockey-player ./cmd/module` directly.)

Expected: `bin/hockey-player` produced with no errors.

- [ ] **Step 2: Run the full test suite one more time**

Run:

```bash
cd /home/nick/hockey-player && go test ./... -race -count=1 -v
```

Expected: PASS. The `-race` flag catches any concurrency mistakes in `axisController` or `DoCommand`.

- [ ] **Step 3: Run `go vet` and `gofmt` hygiene checks**

Run:

```bash
cd /home/nick/hockey-player && go vet ./... && gofmt -d .
```

Expected: no `vet` complaints; no `gofmt` diffs.

---

## Out of scope for this plan (per the spec)

- Persistence of home across restarts.
- Limit-switch / automatic homing on startup.
- A "last error" DoCommand.
- Integration into Viam's geometry / kinematics pipeline.
- Moving more than one gantry axis at a time.
- Raw `SetPower` / PWM exposure.

These are explicitly non-goals; do not add them.
