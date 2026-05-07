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
// goroutine that runs moveFn with a child context of moduleCtx. It returns a
// buffered channel of size 1 that receives nil on success or context
// cancellation (preempt/shutdown), and the real error otherwise. The goroutine
// always sends exactly one value so callers can safely ignore the channel.
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
