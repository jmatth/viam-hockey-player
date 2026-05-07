package hockeyplayer

import (
	"context"
	"fmt"
	"math"
	"time"
)

// defaultPowerPollInterval is how often powerMoveTo samples position while a
// SetPower-based move is in flight. Used by do_command.go in both axis
// power-mode dispatches.
const defaultPowerPollInterval = 20 * time.Millisecond

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
