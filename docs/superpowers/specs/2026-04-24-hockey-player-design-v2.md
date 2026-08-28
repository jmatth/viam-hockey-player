# Hockey Player — Design Spec (v2)

**Module:** `viam-rod-hockey:generic:hockey-player`
**Resource type:** generic **component**
**Date:** 2026-04-24
**Supersedes:** `2026-04-23-hockey-player-design.md`

## What changed from v1

1. Config gains `translation_motor` (the motor the gantry drives). Needed so we can `SetPower` on the translation axis directly, since the gantry abstraction has no `SetPower`.
2. Motion commands accept either `rpm` or `power` to select **how** to drive. Rpm mode uses the motor/gantry firmware's positional APIs; power mode does `SetPower` + poll + `Stop`.
3. `DoCommand` is now **blocking** — it returns when every axis in the command has arrived (or errored). v1 was fire-and-forget.
4. Rotation rpm-mode uses `motor.GoTo(rpm, target_rev)` instead of `motor.GoFor(rpm, delta_rev)`. Absolute target is cleaner.
5. Two optional arrival-tolerance knobs in config for power mode.

## Purpose

Drive a physical hockey player whose linear translation is controlled by a Viam gantry and whose rotation is controlled by a Viam stepper motor. Callers send a target `(t, r)` — `t` in `[0, 1]` for translation, `r` in `[0, 360]` for rotation — along with one speed specifier (`rpm` *or* `power`). The module dispatches the appropriate underlying API call per axis and blocks until all commanded axes have arrived.

Missing fields leave an axis untouched. Translation and rotation in a single command run concurrently.

## 1. Architecture overview

The module registers `viam-rod-hockey:generic:hockey-player` against the generic **component** API and depends on three Viam resources:

- **`gantry`** — used for positional translation moves (`MoveToPosition`) and for position reads (`Position`) in both modes.
- **`translation_motor`** — the motor the gantry drives. Used only in power-mode translation for `SetPower` / `Stop`. Position feedback still comes from the gantry so the `t ↔ mm` mapping stays consistent.
- **`rotation_motor`** — used for rpm-mode (`GoTo`) and power-mode (`SetPower` / `Position` / `Stop`) rotation.

The module exposes no custom gRPC surface. Its entire public interface is the generic component's `DoCommand`. Internally, each axis is driven by an `axisController` that owns:

- A mutex guarding in-flight motion state.
- A cancel function for the in-flight goroutine, if any.
- A `done` channel so a preempting caller can wait for the old goroutine to exit cleanly.
- A `stop` function for the resources it may have commanded (`rotation_motor.Stop` for rotation; `translation_motor.Stop` for translation — power mode drives the motor, rpm mode drives the gantry, so for simplicity the translation controller stops both when preempting).

When a motion `DoCommand` arrives, the dispatcher computes the per-axis move plan (target + mode), hands each active axis off to its controller, and then **waits** for all active axis goroutines to finish before returning.

## 2. Configuration schema

```jsonc
{
  "gantry": "my_gantry",                        // required
  "rotation_motor": "my_stepper",               // required
  "translation_motor": "my_translation_motor",  // required

  "translation_axis_index": 0,                  // optional, default 0
  "min_translation_mm": 50.0,                   // required: t=0 maps here
  "max_translation_mm": 450.0,                  // required: t=1 maps here

  "default_rpm_rotation": 60.0,                 // required, > 0
  "default_speed_mm_per_sec": 100.0,            // required, > 0

  "default_wrap": false,                        // optional, default false

  "rotation_arrival_tol_deg": 0.5,              // optional, default 0.5
  "translation_arrival_tol_mm": 2.0             // optional, default 2.0
}
```

### `Validate(path)`

Returns `([gantry, rotation_motor, translation_motor], nil, err)`. Non-dependency checks:

- `gantry`, `rotation_motor`, `translation_motor` are non-empty strings.
- `min_translation_mm >= 0`, `max_translation_mm > min_translation_mm`.
- `translation_axis_index >= 0`.
- `default_rpm_rotation > 0`, `default_speed_mm_per_sec > 0`.
- `rotation_arrival_tol_deg > 0`, `translation_arrival_tol_mm > 0` if present.

### Construction-time checks (`NewHockeyPlayer`)

Run after dependencies resolve:

- `translation_axis_index < len(gantry.GetLengths(ctx))`.
- `max_translation_mm <= gantry.GetLengths(ctx)[translation_axis_index]`.
- `rotation_motor.GetProperties(ctx).PositionReporting == true`.
- `translation_motor.GetProperties(ctx).PositionReporting == true` *(not strictly required — we read position from the gantry — but we log a warning if it's false so the config is clearer)*.

Startup logs an `Info` line with configured ranges, defaults, and tolerances.

## 3. DoCommand API

Two command shapes, distinguished by the presence of a `"cmd"` key.

### Motion command (no `"cmd"` key)

```jsonc
{
  "t": 0.5,          // optional, [0, 1]
  "r": 90.0,         // optional, [0, 360]
  "wrap": true,      // optional, rotation only, defaults to config's default_wrap

  // speed — specify AT MOST ONE. If neither is set, use defaults (rpm mode with
  // default_rpm_rotation / default_speed_mm_per_sec).
  "rpm": 30.0,       // optional, > 0 → rpm mode
  "power": 0.5       // optional, (0, 1] → power mode (magnitude; sign chosen from direction)
}
```

Synchronous validation, applied before any dispatch. Any failure returns an error and dispatches nothing:

- At least one of `t`, `r` must be present.
- `t`, if present, in `[0, 1]`; `r`, if present, in `[0, 360]`. `0` and `360` are the same angle.
- At most one of `rpm`, `power` may be present.
- `rpm > 0` if present; `power` in `(0, 1]` if present.

**Blocking behavior.** `DoCommand` returns only after all active axes have either arrived or errored. If both `t` and `r` are set, the two axes run in parallel goroutines; the call returns when the slower one finishes. If one axis errors, the other is stopped and the first error is returned.

Response (returned after motion completes):

```jsonc
{
  "status": "done",
  "t_final": 0.498,       // present if t was commanded
  "r_final": 89.6         // present if r was commanded
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
  "t_moving": bool,
  "r_moving": bool
}
```

Implementation: `gantry.Position`, `rotation_motor.Position`, `gantry.IsMoving`, `rotation_motor.IsMoving`; normalize translation by inverting the subrange mapping; normalize rotation via the double-mod formula in §4.

Note: `gantry.IsMoving()` reports motion for the gantry as a whole, not the specific `translation_axis_index`. For a 1-axis gantry this is exact.

## 4. Rotation math

The stepper's position is in revolutions, monotonically accumulating from zero. Convert to the current angle in `[0, 360)`:

```
current_deg = ((motor.Position() * 360) mod 360 + 360) mod 360
```

Given target `r` in `[0, 360]`, compute the signed short-path delta in `[-180, 180]` and its long-way counterpart:

```
delta_short = ((r - current_deg + 540) mod 360) - 180
delta_long  = delta_short > 0 ? delta_short - 360 : delta_short + 360
```

Pick the delta:

- `wrap = true`: `delta = delta_short` (shortest path; free to cross the 0/360 seam).
- `wrap = false`: compute `end = current_deg + delta_short`. If `end < 0` or `end > 360`, take the long way (`delta = delta_long`); else `delta = delta_short`.

### Rpm mode (rotation)

Convert delta into an **absolute** target revolution value:

```
current_rev = motor.Position()         // accumulated revolutions
delta_rev   = delta / 360.0
target_rev  = current_rev + delta_rev
motor.GoTo(ctx, rpm, target_rev, nil)  // blocks until arrival
```

### Power mode (rotation)

Work in **accumulated degrees** so wrap doesn't muddy the termination check:

```
cur_deg_accum = motor.Position() * 360
delta         = short/long path, chosen per wrap (as above, from normalize(cur_deg_accum) vs r)
target_accum  = cur_deg_accum + delta
direction     = sign(delta)
motor.SetPower(ctx, direction * power, nil)
loop:
  sleep poll_interval
  cur_deg_accum = motor.Position() * 360
  if |target_accum - cur_deg_accum| <= rotation_arrival_tol_deg: break
motor.Stop(ctx, nil)
```

Because the motor's position accumulates monotonically (negative or positive), comparing against `target_accum` directly means we're always measuring along the commanded path and don't need to reason about the 0/360 seam mid-move.

Edge cases:

- `|delta| < 0.01°` at dispatch time → already there; skip the call; log debug.
- `r == 0` and `r == 360` are the same angle.

## 5. Translation math

Linear mapping between `t` and millimeters:

```
target_mm = min_translation_mm + t * (max_translation_mm - min_translation_mm)
t_from_mm = (mm - min_translation_mm) / (max_translation_mm - min_translation_mm)
```

### Rpm mode (translation)

```
cur_positions = gantry.Position(ctx)
positions     = copy of cur_positions with index translation_axis_index replaced by target_mm
speeds        = []float64 of len(positions) filled with speed_mm_per_sec
gantry.MoveToPosition(ctx, positions, speeds, nil)  // blocks until arrival
```

Other axes keep their current values so a multi-axis gantry isn't disturbed.

### Power mode (translation)

```
cur_mm    = gantry.Position(ctx)[translation_axis_index]
direction = sign(target_mm - cur_mm)
translation_motor.SetPower(ctx, direction * power, nil)
loop:
  sleep poll_interval
  cur_mm = gantry.Position(ctx)[translation_axis_index]
  if |target_mm - cur_mm| <= translation_arrival_tol_mm: break
translation_motor.Stop(ctx, nil)
```

`poll_interval` is a module constant — 20 ms is a fine starting point; we can tune after hardware testing.

## 6. Concurrency model

One `axisController` per axis:

```go
type axisController struct {
    mu     sync.Mutex
    cancel context.CancelFunc
    done   chan struct{}
    stop   func(context.Context) error  // stops whichever resources this axis might drive
    logger logging.Logger
    name   string                       // "rotation" or "translation"
}
```

### Dispatch flow

On a motion `DoCommand`:

1. Parse and validate (§3). Any failure → return error, no dispatch.
2. For each axis whose field is set:
   - Read current position from the appropriate resource.
   - Compute the move plan (target + mode).
3. Hand each axis plan to its controller via `axisController.dispatch(moveFn)` — this returns a `<-chan error` that fires once the move finishes or errors.
4. Wait on all returned error channels. If any returns non-nil and non-canceled, preempt the others and return that error. Otherwise read final positions and build the response.

### `axisController.dispatch`

```
lock mu
if cancel != nil:
    cancel()                      // cancel old goroutine's context
    stop(context.Background())    // interrupt in-flight RPC / poll loop
    <-done                        // wait for old goroutine to exit
ctx, cancel = context.WithCancel(moduleCtx)
done = make(chan struct{})
errCh := make(chan error, 1)
go:
    defer close(done)
    err := moveFn(ctx)
    if err != nil && ctx.Err() == nil:
        logger.Errorf("%s move failed: %v", name, err)
        errCh <- err
    else:
        errCh <- nil
unlock mu
return errCh
```

The mutex is held through the cancel/stop/wait sequence so two concurrent motion `DoCommand`s for the same axis are serialized in arrival order.

### Translation stop fan-out

Because translation rpm-mode drives the gantry and power-mode drives the translation_motor, the translation controller's `stop` function stops **both**: `translation_motor.Stop` then `gantry.Stop`, errors logged and otherwise ignored. This keeps preemption simple.

### Shutdown

`Close(ctx)` cancels the module context, calls `Stop` on all three resources with a fresh background context, and waits for both `done` channels.

## 7. Error handling

### Synchronous errors (returned from `DoCommand`)

- Malformed payload (non-numeric fields, wrong types).
- Out-of-range values per §3 (`t ∉ [0, 1]`, `r ∉ [0, 360]`, `rpm <= 0`, `power ∉ (0, 1]`).
- Both `rpm` and `power` set.
- Empty motion command (no `t` and no `r`).
- Unknown `"cmd"` value.
- Reading current position for dispatch math fails. Nothing is dispatched if this fails for either axis.

### Motion errors (returned from `DoCommand` since it blocks)

- Underlying RPC returns an error (`GoTo`, `MoveToPosition`, `SetPower`, `Stop`, `Position`).
- Module context canceled mid-move (shutdown).
- Any non-nil, non-context-canceled error from one axis causes the other to be preempted; the first error is returned.

### Async errors (logs only)

- `Stop` calls during preemption/shutdown that fail: logged, not returned.

## 8. Startup and shutdown

**`NewHockeyPlayer`:**

1. Resolve `gantry`, `rotation_motor`, `translation_motor`. Missing → error.
2. Run construction-time checks from §2.
3. Initialize two `axisController`s with correct `stop` functions and log names.
4. Log `Info` with ranges, defaults, tolerances.
5. No motion on startup. Trust that the rig was parked before the module started.

**`Close`:**

1. Cancel module context.
2. `rotation_motor.Stop(bg)`, `translation_motor.Stop(bg)`, `gantry.Stop(bg)`.
3. Wait for both axis controllers' `done` channels.

## 9. Testing approach

**Unit tests, no hardware:**

- **Rotation math** (`rotation_math_test.go`). Table-driven over `(current_deg, target_deg, wrap) → expected_delta`. Short/long path both directions, seam crossing, `r == current`, `r == 0` vs `r == 360`, negative accumulated revolutions.
- **Translation mapping.** `(t, min, max) → mm` and inverse. Range-check errors.
- **`Validate()`** table-driven: happy path + each failure mode, including both new motor/tolerance fields.

**Component tests with fake Viam resources** (`go.viam.com/rdk/testutils/inject` or hand-rolled):

- Rpm-mode motion with `t` only → `gantry.MoveToPosition` called with `speed_mm_per_sec`; returns after the fake's `MoveToPosition` returns.
- Rpm-mode motion with `r` only → `motor.GoTo` called with computed `target_rev` and `rpm`.
- Power-mode motion with `r` → `rotation_motor.SetPower(sign*power)` then polled `Position()` then `Stop`. Verify direction sign and tolerance arrival.
- Power-mode motion with `t` → `translation_motor.SetPower(sign*power)`, polled `gantry.Position`, `translation_motor.Stop`. Gantry is *not* commanded.
- Motion with both `t` and `r` → both underlying calls happen concurrently; `DoCommand` returns after both fakes finish; response contains `t_final` and `r_final`.
- Reject command with both `rpm` and `power`.
- Per-axis preemption: fake's in-flight move blocks on a channel; second motion command for same axis → verify `Stop` called on the right resource, old goroutine exits, new move dispatched.
- Cross-axis independence: rotation-only preempt doesn't touch translation.
- `get_position` returns normalized `(t, r, t_moving, r_moving)`.
- `Close` during in-flight motion stops all three resources; `goleak.VerifyNone`.

**Out of scope:**

- End-to-end tests against real hardware — covered by running the module on the rig.

## 10. Explicit non-goals (v1 / v2)

- No persistence of home across restarts.
- No limit-switch homing.
- No "last error" query DoCommand.
- No geometry / kinematics integration.
- Power-mode overshoot correction (we accept whatever the motor coasts past the tolerance).
- Ramped approach in power mode (no two-phase slowdown).
- Multi-axis gantries are *supported* in rpm mode (other axes preserved) but the module only actively moves one axis.
- No direct exposure of `SetRPM` (indefinite velocity) — every motion command has a target.
