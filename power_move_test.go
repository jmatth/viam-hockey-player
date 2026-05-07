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
)

// fakeAxis models a simple 1-D actuator: SetPower drives position at a rate
// proportional to power; Position returns the current value; Stop zeroes power.
type fakeAxis struct {
	mu      sync.Mutex
	pos     float64
	power   float64
	rate    float64 // units-per-second per unit power
	stopped int32
}

func (f *fakeAxis) Position(ctx context.Context) (float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pos, nil
}
func (f *fakeAxis) SetPower(ctx context.Context, p float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.power = p
	return nil
}
func (f *fakeAxis) Stop(ctx context.Context) error {
	atomic.StoreInt32(&f.stopped, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.power = 0
	return nil
}

// step advances the fake by dt seconds.
func (f *fakeAxis) step(dt float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pos += f.power * f.rate * dt
}

// power returns the current power value safely.
func (f *fakeAxis) powerVal() float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.power
}

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
	pos, _ := f.Position(ctx)
	assert.InDelta(t, 5.0, pos, 0.5) // coast tolerance
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
	pos, _ := f.Position(ctx)
	assert.InDelta(t, 0.0, pos, 0.5)
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
	assert.Equal(t, 0.0, f.powerVal())
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
