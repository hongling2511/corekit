package app_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hongling2511/corekit/app"
)

// events records what components did, in order.
type events struct {
	mu   sync.Mutex
	list []string
}

func (e *events) add(s string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.list = append(e.list, s)
}

func (e *events) get() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.list...)
}

// fake is a component that runs until its context is cancelled or Stop is
// called. Hooks change its behaviour.
type fake struct {
	name    string
	ev      *events
	started chan struct{} // closed when Start is entered
	stopped chan struct{} // closed by Stop
	once    sync.Once

	start func(ctx context.Context) error // runs inside Start before blocking; a non-nil error is returned
	early bool                            // Start returns nil at once
	final error                           // returned by Start once it is told to stop
	stop  func(ctx context.Context) error // replaces Stop's behaviour
}

func newFake(name string, ev *events) *fake {
	return &fake{name: name, ev: ev, started: make(chan struct{}), stopped: make(chan struct{})}
}

func (f *fake) Name() string { return f.name }

func (f *fake) Start(ctx context.Context) error {
	close(f.started)
	if f.early {
		return nil
	}
	if f.start != nil {
		if err := f.start(ctx); err != nil {
			return err
		}
	}
	select {
	case <-ctx.Done():
	case <-f.stopped:
	}
	f.ev.add("exit " + f.name)
	return f.final
}

func (f *fake) Stop(ctx context.Context) error {
	f.ev.add("stop " + f.name)
	if f.stop != nil {
		return f.stop(ctx)
	}
	f.once.Do(func() { close(f.stopped) })
	return nil
}

// runAsync runs the components and returns a channel with Run's result.
func runAsync(ctx context.Context, timeout time.Duration, cs ...app.Component) <-chan error {
	done := make(chan error, 1)
	go func() { done <- app.RunComponents(ctx, timeout, cs...) }()
	return done
}

func waitStarted(t *testing.T, fs ...*fake) {
	t.Helper()
	for _, f := range fs {
		select {
		case <-f.started:
		case <-time.After(5 * time.Second):
			t.Fatalf("component %s did not start", f.name)
		}
	}
}

func result(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("RunComponents did not return")
		return nil
	}
}

func TestComponentsStartInParallelAndStopInReverseOrder(t *testing.T) {
	ev := &events{}
	a, b, c := newFake("a", ev), newFake("b", ev), newFake("c", ev)
	// Each component's Start waits for the others to have started, so this
	// only completes if the starts run in parallel.
	all := []*fake{a, b, c}
	for _, f := range all {
		f.start = func(ctx context.Context) error {
			for _, other := range all {
				select {
				case <-other.started:
				case <-ctx.Done():
				}
			}
			return nil
		}
	}
	ctx, cancel := context.WithCancel(context.Background())

	done := runAsync(ctx, 5*time.Second, a, b, c)
	waitStarted(t, a, b, c)
	cancel()

	require.NoError(t, result(t, done))
	assert.Equal(t, []string{"stop c", "exit c", "stop b", "exit b", "stop a", "exit a"}, ev.get())
}

func TestComponentFailureStopsTheOthers(t *testing.T) {
	ev := &events{}
	a, b, c := newFake("a", ev), newFake("b", ev), newFake("c", ev)
	boom := errors.New("boom")
	fail := make(chan struct{})
	b.start = func(context.Context) error { <-fail; return boom }

	done := runAsync(context.Background(), 5*time.Second, a, b, c)
	waitStarted(t, a, b, c)
	close(fail)

	err := result(t, done)
	require.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), "component b")
	assert.Equal(t, []string{"stop c", "exit c", "stop a", "exit a"}, ev.get())
}

func TestComponentReturningEarlyWithoutErrorIsAFailure(t *testing.T) {
	ev := &events{}
	a, b := newFake("a", ev), newFake("b", ev)
	b.early = true

	err := result(t, runAsync(context.Background(), 5*time.Second, a, b))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "component b: exited unexpectedly")
	assert.Equal(t, []string{"stop a", "exit a"}, ev.get())
}

func TestStopTimeoutAbandonsAStuckComponent(t *testing.T) {
	ev := &events{}
	a, b := newFake("a", ev), newFake("b", ev)
	b.stop = func(context.Context) error { select {} } // ignores its deadline
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := runAsync(ctx, 100*time.Millisecond, a, b)
	waitStarted(t, a, b)
	began := time.Now()
	cancel()

	err := result(t, done)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stop timed out after 100ms; still running: [b a]")
	assert.Less(t, time.Since(began), 2*time.Second)
	assert.Contains(t, ev.get(), "stop b")
	assert.NotContains(t, ev.get(), "stop a", "a is not stopped once the timeout has passed")
}

func TestStopErrorIsReturnedAndTheRestStillStop(t *testing.T) {
	ev := &events{}
	a, b := newFake("a", ev), newFake("b", ev)
	bad := errors.New("bad stop")
	b.stop = func(context.Context) error { return bad }
	ctx, cancel := context.WithCancel(context.Background())

	done := runAsync(ctx, 5*time.Second, a, b)
	waitStarted(t, a, b)
	cancel()

	err := result(t, done)
	require.ErrorIs(t, err, bad)
	assert.Contains(t, err.Error(), "stop component b")
	assert.Equal(t, []string{"stop b", "exit b", "stop a", "exit a"}, ev.get())
}

func TestSignalStopsTheComponents(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			ev := &events{}
			a, b := newFake("a", ev), newFake("b", ev)

			done := runAsync(context.Background(), 5*time.Second, a, b)
			waitStarted(t, a, b)
			// Start has been entered, so the runner's signal handler is registered.
			require.NoError(t, syscall.Kill(os.Getpid(), sig))

			require.NoError(t, result(t, done))
			assert.Equal(t, []string{"stop b", "exit b", "stop a", "exit a"}, ev.get())
		})
	}
}

func TestFailureIsKeptWhenTheStopThenTimesOut(t *testing.T) {
	ev := &events{}
	a, b := newFake("a", ev), newFake("b", ev)
	boom := errors.New("boom")
	fail := make(chan struct{})
	b.start = func(context.Context) error { <-fail; return boom }
	a.stop = func(context.Context) error { select {} } // ignores its deadline

	done := runAsync(context.Background(), 100*time.Millisecond, a, b)
	waitStarted(t, a, b)
	close(fail)

	err := result(t, done)
	require.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), "stop timed out after 100ms; still running: [a]")
}

func TestErrorFromStartDuringShutdownIsReturned(t *testing.T) {
	for _, trigger := range []string{"signal", "cancel"} {
		t.Run(trigger, func(t *testing.T) {
			ev := &events{}
			a, b := newFake("a", ev), newFake("b", ev)
			flush := errors.New("final flush failed")
			b.final = flush
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			done := runAsync(ctx, 5*time.Second, a, b)
			waitStarted(t, a, b)
			if trigger == "signal" {
				require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGTERM))
			} else {
				cancel()
			}

			err := result(t, done)
			require.ErrorIs(t, err, flush)
			assert.Contains(t, err.Error(), "component b")
			assert.Equal(t, []string{"stop b", "exit b", "stop a", "exit a"}, ev.get(), "a still stops")
		})
	}
}

func TestContextCanceledFromStartDuringShutdownIsClean(t *testing.T) {
	ev := &events{}
	a := newFake("a", ev)
	a.final = context.Canceled
	ctx, cancel := context.WithCancel(context.Background())

	done := runAsync(ctx, 5*time.Second, a)
	waitStarted(t, a)
	cancel()

	require.NoError(t, result(t, done))
}

// sliceErr is an error type whose values cannot be compared with ==.
type sliceErr []string

func (e sliceErr) Error() string { return "custom cause" }

func TestUncomparableCancelCauseWithStopTimeout(t *testing.T) {
	ev := &events{}
	a := newFake("a", ev)
	a.stop = func(context.Context) error { select {} } // ignores its deadline
	ctx, cancel := context.WithCancelCause(context.Background())

	done := runAsync(ctx, 100*time.Millisecond, a)
	waitStarted(t, a)
	cancel(sliceErr{"x"})

	err := result(t, done)
	require.Error(t, err)
	assert.Equal(t, "stop timed out after 100ms; still running: [a]", err.Error(), "the caller's cause is not a component failure")
}
