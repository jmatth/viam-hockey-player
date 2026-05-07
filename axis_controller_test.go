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

// goleakOpts ignores persistent init-time goroutines from third-party libs
// (desertbit/timer, go.opencensus.io) that we don't spawn or control.
var goleakOpts = []goleak.Option{
	goleak.IgnoreTopFunction("github.com/desertbit/timer.timerRoutine"),
	goleak.IgnoreTopFunction("go.opencensus.io/stats/view.(*worker).start"),
}

func TestAxisController_SingleDispatch(t *testing.T) {
	defer goleak.VerifyNone(t, goleakOpts...)
	logger := logging.NewTestLogger(t)
	stopper := &fakeStopper{}
	a := newAxisController("rotation", stopper.stop, logger)
	defer a.close()

	ran := make(chan struct{})
	_ = a.dispatch(context.Background(), func(ctx context.Context) error {
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
	defer goleak.VerifyNone(t, goleakOpts...)
	logger := logging.NewTestLogger(t)
	stopper := &fakeStopper{}
	a := newAxisController("rotation", stopper.stop, logger)
	defer a.close()

	first := make(chan error, 1)
	_ = a.dispatch(context.Background(), func(ctx context.Context) error {
		<-ctx.Done()
		first <- ctx.Err()
		return ctx.Err()
	})

	secondRan := make(chan struct{})
	_ = a.dispatch(context.Background(), func(ctx context.Context) error {
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
	defer goleak.VerifyNone(t, goleakOpts...)
	logger := logging.NewTestLogger(t)
	stopper := &fakeStopper{}
	a := newAxisController("rotation", stopper.stop, logger)

	done := make(chan struct{})
	_ = a.dispatch(context.Background(), func(ctx context.Context) error {
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
	defer goleak.VerifyNone(t, goleakOpts...)
	logger := logging.NewTestLogger(t)
	stopper := &fakeStopper{}
	a := newAxisController("rotation", stopper.stop, logger)
	a.close()
	a.close() // should not panic or block
}

func TestAxisController_SuppressesCancelErrorsFromLog(t *testing.T) {
	defer goleak.VerifyNone(t, goleakOpts...)
	logger := logging.NewTestLogger(t)
	stopper := &fakeStopper{}
	a := newAxisController("rotation", stopper.stop, logger)
	defer a.close()

	started := make(chan struct{})
	_ = a.dispatch(context.Background(), func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	<-started
	require.NotNil(t, a)

	done := make(chan struct{})
	_ = a.dispatch(context.Background(), func(ctx context.Context) error {
		close(done)
		return nil
	})
	<-done
}

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
