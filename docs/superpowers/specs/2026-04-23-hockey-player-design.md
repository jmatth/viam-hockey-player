# Hockey Player — Design Spec

**Module:** `nfranczak:generic:hockey-player`
**Resource type:** generic **component** (changed from the scaffolded generic service)
**Date:** 2026-04-23

## Purpose

Drive a physical hockey player figure whose linear translation is controlled by a Viam gantry and whose rotation is controlled by a Viam stepper motor. Callers send normalized coordinates — `t` in `[0, 1]` for translation and `r` in `[0, 360]` for rotation — and the module translates those into the gantry and motor calls needed to arrive at the commanded pose. Missing fields leave an axis untouched. Translation and rotation in a single command run concurrently.

## 1. Architecture overview

The module registers the `nfranczak:generic:hockey-player` model against the generic **component** API and depends on two Viam resources:

- A **`gantry`** resource for linear translation along one axis.
- A **`motor`** resource (stepper) for rotation.

Both names appear in the module's config and are returned as required dependencies from `Validate(path)`.

The module exposes no custom gRPC surface. Its entire public interface is the generic component's `DoCommand`. Internally, each axis is driven by a small `axisController` that owns:

- A mutex guarding in-flight motion state.
- A cancel function for the currently-running goroutine, if any.
- A `done` channel so a preempting caller can wait for the old goroutine to exit cleanly.
- A reference to the appropriate `Stop(ctx)` function (`motor.Stop` or `gantry.Stop`).

When a motion `DoCommand` arrives, the dispatcher looks at each field and, for each axis whose field is set, tells that axis's controller to stop any in-flight motion, wait for the old goroutine to exit, then spawn a new goroutine that calls `motor.GoFor(...)` or `gantry.MoveToPosition(...)`. Fields that are absent leave the corresponding axis alone entirely. `DoCommand` returns immediately after dispatch; motion errors surface in the logs.

## 2. Configuration schema

```jsonc
{
  "gantry": "my_gantry",                  // required: gantry resource name
  "rotation_motor": "my_stepper",         // required: motor resource name

  "translation_axis_index": 0,            // optional, default 0
  "min_translation_mm": 50.0,             // required: t=0 maps here
  "max_translation_mm": 450.0,            // required: t=1 maps here

  "default_rpm_rotation": 60.0,           // required: rpm used when r is sent without rpm_rotation
  "default_speed_mm_per_sec": 100.0,      // required: used when t is sent without speed_mm_per_sec

  "default_wrap": false                   // optional, default false
}
```

### `Validate(path)`

Returns `([gantry_name, rotation_motor_name], nil, err)`. Checks that do not require talking to the dependencies:

- `gantry` and `rotation_motor` are non-empty strings.
- `min_translation_mm >= 0` and `max_translation_mm > min_translation_mm`.
- `translation_axis_index >= 0`.
- `default_rpm_rotation > 0` and `default_speed_mm_per_sec > 0`.

### Construction-time checks (in `NewHockeyPlayer`)

These require the resolved dependencies, so they run in the constructor, not `Validate`. Failures return an error and the module refuses to start:

- `translation_axis_index < len(gantry.GetLengths(ctx))`.
- `max_translation_mm <= gantry.GetLengths(ctx)[translation_axis_index]`.
- `motor.GetProperties(ctx).PositionReporting == true`.

Startup logs an `Info` line with the configured ranges and defaults so misconfigurations are obvious in the machine log.

## 3. DoCommand API

Two command shapes, distinguished by the presence of a `"cmd"` key. Any other `"cmd"` value is an error.

### Motion command (no `"cmd"` key)

```jsonc
{
  "t": 0.5,                  // optional, [0, 1]
  "r": 90.0,                 // optional, [0, 360]
  "wrap": true,              // optional, defaults to config's default_wrap
  "rpm_rotation": 30.0,      // optional, defaults to config's default_rpm_rotation
  "speed_mm_per_sec": 120.0  // optional, defaults to config's default_speed_mm_per_sec
}
```

Synchronous validation, applied before any dispatch. Any failure returns an error and dispatches nothing:

- At least one of `t`, `r` must be present (`"no motion fields provided"` otherwise).
- If `t` is present, it must be a number in `[0, 1]`.
- If `r` is present, it must be a number in `[0, 360]`. `0` and `360` are treated as the same angle.
- `rpm_rotation`, `speed_mm_per_sec`: if provided, must be `> 0`.

Response (returned after dispatch, *not* after motion completes):

```jsonc
{
  "status": "dispatched",
  "t_dispatched": true,
  "r_dispatched": true
}
```

### Position query

```jsonc
{ "cmd": "get_position" }
```

Response:

```jsonc
{
  "t": 0.374,
  "r": 214.3,
  "t_moving": true,
  "r_moving": false
}
```

Implementation: read `gantry.Position(ctx)`, `motor.Position(ctx)`, `gantry.IsMoving(ctx)`, `motor.IsMoving(ctx)`; normalize translation by inverting the subrange mapping; normalize rotation via the double-mod formula in §4.

Note: `gantry.IsMoving()` reports motion for the gantry as a whole, not the specific `translation_axis_index`. For the normal 1-axis gantry this is exact; for a multi-axis gantry, `t_moving` being `true` means "some axis of the gantry is moving", which is a superset of "our translation axis is moving". Acceptable for v1.

## 4. Rotation math (wrap semantics)

The stepper's position is in revolutions, monotonically accumulating from zero. Convert to the current angle in `[0, 360)`:

```
current_deg = ((motor.Position() * 360) mod 360 + 360) mod 360
```

The double-mod handles negative accumulated revolutions (the player has been rotated backward past the power-on zero).

Given target `r` and `current_deg`, compute the signed short-path delta (range `[-180, 180]`) and its long-way-around counterpart (opposite sign, `delta_short + delta_long = ±360`):

```
delta_short = ((r - current_deg + 540) mod 360) - 180
delta_long  = delta_short > 0 ? delta_short - 360 : delta_short + 360
```

Pick the delta based on `wrap`:

- `wrap = true`: `delta = delta_short`. Shortest path, free to cross the 0/360 seam.
- `wrap = false`: compute `end = current_deg + delta_short`. If `end < 0` or `end > 360`, the short path crosses the seam and we must take the long way: `delta = delta_long`. Otherwise `delta = delta_short`.

Convert and dispatch:

```
delta_rev = delta / 360.0
motor.GoFor(ctx, rpm, delta_rev, nil)
```

Edge cases:

- `|delta| < 0.01°` → treat as "already there", skip the call, log a debug message.
- `r == 0` and `r == 360` are the same angle.

## 5. Concurrency model (per-axis preemption)

One `axisController` per axis:

```go
type axisController struct {
    mu     sync.Mutex
    cancel context.CancelFunc       // cancels the in-flight goroutine's context
    done   chan struct{}            // closed when the in-flight goroutine exits
    stop   func(context.Context) error  // motor.Stop or gantry.Stop
    logger logging.Logger
    name   string                   // "rotation" or "translation", for log lines
}
```

### Dispatch flow

On a motion `DoCommand`:

1. Parse and validate the command (§3).
2. For each axis whose field is set, compute the move:
   - **Rotation:** read `motor.Position(ctx)`, run the §4 math to get `(rpm, delta_rev)`. `rpm` is the per-command value if supplied, otherwise `default_rpm_rotation`.
   - **Translation:** read `gantry.Position(ctx)` for all axes, replace the target axis with `min_translation_mm + t * (max_translation_mm - min_translation_mm)`, keep all other axes at their current values, produce `(positions_mm[], speeds_mm_per_sec[])`. The speed vector gets the per-command `speed_mm_per_sec` (or default) at the target axis index and the same value at other axes (they won't move, so the value there is harmless).
3. Hand each axis its move via `axisController.dispatch(moveFn)`.

### `axisController.dispatch`

```
lock mu
defer unlock mu
if cancel != nil:
    cancel()                       // cancel old goroutine's context
    stop(context.Background())     // interrupt motor.GoFor / gantry.MoveToPosition
    <-done                         // wait for old goroutine to exit
ctx, cancel = context.WithCancel(moduleCtx)
done = make(chan struct{})
go func():
    defer close(done)
    err := moveFn(ctx)             // wraps motor.GoFor or gantry.MoveToPosition
    if err != nil && ctx.Err() == nil:
        logger.Errorf("axis %s failed: %v", name, err)
```

The mutex is held for the entire dispatch — through the cancel, the `Stop()` call, and the wait for the old goroutine to exit — so that two concurrent `DoCommand`s on the same axis are serialized in arrival order. The wait window is short: `Stop()` causes the underlying RPC to return promptly.

### Shutdown

`Close(ctx)` on the module cancels both controllers' contexts, calls `motor.Stop` and `gantry.Stop` with a fresh background context, and waits for both `done` channels so no goroutine leaks.

## 6. Error handling

### Synchronous errors (returned from `DoCommand`)

- Malformed payload (non-numeric `t`, `r`, etc.).
- Out-of-range values per §3.
- Empty motion command (no `t` and no `r`).
- Unknown `"cmd"` value.
- Reading current position for dispatch math fails (e.g. `motor.Position()` errors). Nothing is dispatched for either axis if this happens for either axis.

### Async errors (logs only)

- `motor.GoFor` or `gantry.MoveToPosition` returns an error after dispatch (stall, RPC failure, etc.). Logged via `s.logger.Errorf("rotation move failed: %v", err)`; axis stops, no retry.
- When an axis is preempted, the old goroutine's error is suppressed if `ctx.Err() != nil` — we caused the cancellation; it isn't a real failure.

## 7. Startup and shutdown

**`NewHockeyPlayer`:**

1. Resolve `gantry` and `rotation_motor` from `deps`. Missing → error.
2. Run the construction-time checks listed in §2.
3. Initialize the two `axisController` instances (nil cancel, nil done, correct stop fn and log name).
4. Log an `Info` line stating the configured ranges and defaults.
5. No motion on startup. The module trusts that the user has parked the rig at the intended home before starting the server.

**`Close`:**

1. Cancel module context.
2. `motor.Stop(context.Background(), nil)` and `gantry.Stop(context.Background(), nil)`.
3. Wait for both axis controllers' `done` channels.

## 8. Testing approach

**Unit tests, no hardware:**

- **Rotation math (`rotation_math_test.go`).** Table-driven: rows of `(current_deg, target_deg, wrap) → expected_delta`. Covers short path both directions, long path both directions (`wrap = false`), seam-crossing behavior, `r == current`, `r == 0` vs `r == 360`, negative accumulated motor revolutions.
- **Translation mapping.** `(t, min, max) → expected_mm` and the inverse used by `get_position`. Range-check validation errors.
- **`Validate()`** table-driven: happy path + each individual failure mode.

**Component tests with fake Viam resources** (use `go.viam.com/rdk/testutils/inject` or hand-rolled fakes):

- Motion command with `t` only → only `gantry.MoveToPosition` is called, `positions_mm` has the target value at `translation_axis_index` and other axes at their previously-reported values.
- Motion command with `r` only → only `motor.GoFor` is called with the expected signed delta (in revolutions) and rpm.
- Motion command with both → both underlying calls happen; goroutines run concurrently.
- Per-axis preemption: fake motor's `GoFor` blocks on a channel; send a second motion command for `r`; verify `motor.Stop` was called, old goroutine exits, new `GoFor` is invoked with the new delta. The translation axis is untouched by a rotation-only preempt.
- `get_position` returns normalized `(t, r, t_moving, r_moving)` computed from fake `Position()` / `IsMoving()` values.
- `Close` during an in-flight motion: `Stop` is called on both resources; no goroutine leaks (verified via `goleak.VerifyNone`).

**Out of scope:**

- End-to-end tests against real hardware — covered by running the module on the actual rig.

## 9. Explicit non-goals (v1)

Named here so they don't accidentally creep in:

- No persistence of home across restarts.
- No limit-switch homing.
- No "last error" query DoCommand.
- No geometry / kinematics integration.
- Multi-axis gantries are *supported* (other axes' positions are preserved on every `MoveToPosition` call) but the module only ever actively *moves* one axis.
- No raw `SetPower` / PWM exposure; motion is always positional via `GoFor` / `MoveToPosition`.
