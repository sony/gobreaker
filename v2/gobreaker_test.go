package gobreaker

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var defaultCB *CircuitBreaker[bool]
var customCB *CircuitBreaker[bool]
var rollingCB *CircuitBreaker[bool]

type StateChange struct {
	name string
	from State
	to   State
}

var stateChange StateChange

type ctxKey string

func pseudoSleep(cb *CircuitBreaker[bool], period time.Duration) {
	cb.start = cb.start.Add(-period)
	if !cb.expiry.IsZero() {
		cb.expiry = cb.expiry.Add(-period)
	}
}

func succeed(cb *CircuitBreaker[bool]) error {
	_, err := cb.Execute(func() (bool, error) { return true, nil })
	return err
}

func succeedLater(cb *CircuitBreaker[bool], delay time.Duration) <-chan error {
	ch := make(chan error)
	go func() {
		_, err := cb.Execute(func() (bool, error) {
			time.Sleep(delay)
			return true, nil
		})
		ch <- err
	}()
	return ch
}

func fail(cb *CircuitBreaker[bool]) error {
	msg := "fail"
	_, err := cb.Execute(func() (bool, error) { return false, errors.New(msg) })
	if err.Error() == msg {
		return nil
	}
	return err
}

func causePanic(cb *CircuitBreaker[bool]) error {
	_, err := cb.Execute(func() (bool, error) { panic("oops") })
	return err
}

func newCustom() *CircuitBreaker[bool] {
	var customSt Settings
	customSt.Name = "cb"
	customSt.MaxRequests = 3
	customSt.Interval = time.Duration(30) * time.Second
	customSt.Timeout = time.Duration(90) * time.Second
	customSt.ReadyToTrip = func(counts Counts) bool {
		numReqs := counts.Requests
		failureRatio := float64(counts.TotalFailures) / float64(numReqs)

		return numReqs >= 3 && failureRatio >= 0.6
	}
	customSt.OnStateChange = func(name string, from State, to State) {
		stateChange = StateChange{name, from, to}
	}

	return NewCircuitBreaker[bool](customSt)
}

func newRollingWindow() *CircuitBreaker[bool] {
	var rollingWindowSt Settings
	rollingWindowSt.Name = "rw"
	rollingWindowSt.MaxRequests = 3
	rollingWindowSt.Interval = time.Duration(30) * time.Second
	rollingWindowSt.BucketPeriod = time.Duration(3) * time.Second
	rollingWindowSt.Timeout = time.Duration(90) * time.Second
	rollingWindowSt.ReadyToTrip = func(counts Counts) bool {
		numReqs := counts.Requests
		failureRatio := float64(counts.TotalFailures) / float64(numReqs)

		return numReqs >= 3 && failureRatio >= 0.6
	}
	rollingWindowSt.OnStateChange = func(name string, from State, to State) {
		stateChange = StateChange{name, from, to}
	}

	return NewCircuitBreaker[bool](rollingWindowSt)
}

func newNegativeDurationCB() *CircuitBreaker[bool] {
	var negativeSt Settings
	negativeSt.Name = "ncb"
	negativeSt.Interval = time.Duration(-30) * time.Second
	negativeSt.Timeout = time.Duration(-90) * time.Second

	return NewCircuitBreaker[bool](negativeSt)
}

func init() {
	defaultCB = NewCircuitBreaker[bool](Settings{})
	customCB = newCustom()
	rollingCB = newRollingWindow()
}

func TestStateConstants(t *testing.T) {
	assert.Equal(t, State(0), StateClosed)
	assert.Equal(t, State(1), StateHalfOpen)
	assert.Equal(t, State(2), StateOpen)

	assert.Equal(t, StateClosed.String(), "closed")
	assert.Equal(t, StateHalfOpen.String(), "half-open")
	assert.Equal(t, StateOpen.String(), "open")
	assert.Equal(t, State(100).String(), "unknown state: 100")
}

func TestNewCircuitBreaker(t *testing.T) {
	defaultCB := NewCircuitBreaker[bool](Settings{})
	assert.Equal(t, "", defaultCB.name)
	assert.Equal(t, uint32(1), defaultCB.maxRequests)
	assert.Equal(t, time.Duration(0), defaultCB.interval)
	assert.Equal(t, time.Duration(60)*time.Second, defaultCB.timeout)
	assert.NotNil(t, defaultCB.readyToTrip)
	assert.Nil(t, defaultCB.onStateChangeCtx)
	assert.Equal(t, StateClosed, defaultCB.state)
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, defaultCB.Counts())
	assert.True(t, defaultCB.expiry.IsZero())

	customCB := newCustom()
	assert.Equal(t, "cb", customCB.name)
	assert.Equal(t, uint32(3), customCB.maxRequests)
	assert.Equal(t, time.Duration(30)*time.Second, customCB.interval)
	assert.Equal(t, time.Duration(90)*time.Second, customCB.timeout)
	assert.NotNil(t, customCB.readyToTrip)
	assert.NotNil(t, customCB.onStateChangeCtx)
	assert.Equal(t, StateClosed, customCB.state)
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, customCB.Counts())
	assert.False(t, customCB.expiry.IsZero())

	rollingWindowCB := newRollingWindow()
	assert.Equal(t, "rw", rollingWindowCB.name)
	assert.Equal(t, uint32(3), rollingWindowCB.maxRequests)
	assert.Equal(t, time.Duration(30)*time.Second, rollingWindowCB.interval)
	assert.Equal(t, 10, len(rollingWindowCB.counts.buckets))
	assert.Equal(t, time.Duration(90)*time.Second, rollingWindowCB.timeout)
	assert.NotNil(t, rollingWindowCB.readyToTrip)
	assert.NotNil(t, rollingWindowCB.onStateChangeCtx)
	assert.Equal(t, StateClosed, rollingWindowCB.state)
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, rollingWindowCB.Counts())
	assert.True(t, rollingWindowCB.expiry.IsZero())

	negativeDurationCB := newNegativeDurationCB()
	assert.Equal(t, "ncb", negativeDurationCB.name)
	assert.Equal(t, uint32(1), negativeDurationCB.maxRequests)
	assert.Equal(t, time.Duration(0)*time.Second, negativeDurationCB.interval)
	assert.Equal(t, time.Duration(60)*time.Second, negativeDurationCB.timeout)
	assert.NotNil(t, negativeDurationCB.readyToTrip)
	assert.Nil(t, negativeDurationCB.onStateChangeCtx)
	assert.Equal(t, StateClosed, negativeDurationCB.state)
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, negativeDurationCB.Counts())
	assert.True(t, negativeDurationCB.expiry.IsZero())
}

func TestDefaultCircuitBreaker(t *testing.T) {
	assert.Equal(t, "", defaultCB.Name())

	for i := 0; i < 5; i++ {
		assert.Nil(t, fail(defaultCB))
	}
	assert.Equal(t, StateClosed, defaultCB.State())
	assert.Equal(t, Counts{5, 0, 5, 0, 5}, defaultCB.Counts())

	assert.Nil(t, succeed(defaultCB))
	assert.Equal(t, StateClosed, defaultCB.State())
	assert.Equal(t, Counts{6, 1, 5, 1, 0}, defaultCB.Counts())

	assert.Nil(t, fail(defaultCB))
	assert.Equal(t, StateClosed, defaultCB.State())
	assert.Equal(t, Counts{7, 1, 6, 0, 1}, defaultCB.Counts())

	// StateClosed to StateOpen
	for i := 0; i < 5; i++ {
		assert.Nil(t, fail(defaultCB)) // 6 consecutive failures
	}
	assert.Equal(t, StateOpen, defaultCB.State())
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, defaultCB.Counts())
	assert.False(t, defaultCB.expiry.IsZero())

	assert.Error(t, succeed(defaultCB))
	assert.Error(t, fail(defaultCB))
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, defaultCB.Counts())

	pseudoSleep(defaultCB, time.Duration(59)*time.Second)
	assert.Equal(t, StateOpen, defaultCB.State())

	// StateOpen to StateHalfOpen
	pseudoSleep(defaultCB, time.Duration(1)*time.Second) // over Timeout
	assert.Equal(t, StateHalfOpen, defaultCB.State())
	assert.True(t, defaultCB.expiry.IsZero())

	// StateHalfOpen to StateOpen
	assert.Nil(t, fail(defaultCB))
	assert.Equal(t, StateOpen, defaultCB.State())
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, defaultCB.Counts())
	assert.False(t, defaultCB.expiry.IsZero())

	// StateOpen to StateHalfOpen
	pseudoSleep(defaultCB, time.Duration(60)*time.Second)
	assert.Equal(t, StateHalfOpen, defaultCB.State())
	assert.True(t, defaultCB.expiry.IsZero())

	// StateHalfOpen to StateClosed
	assert.Nil(t, succeed(defaultCB))
	assert.Equal(t, StateClosed, defaultCB.State())
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, defaultCB.Counts())
	assert.True(t, defaultCB.expiry.IsZero())
}

func TestCustomCircuitBreaker(t *testing.T) {
	assert.Equal(t, "cb", customCB.Name())

	for i := 0; i < 5; i++ {
		assert.Nil(t, succeed(customCB))
		assert.Nil(t, fail(customCB))
	}
	assert.Equal(t, StateClosed, customCB.State())
	assert.Equal(t, Counts{10, 5, 5, 0, 1}, customCB.Counts())

	pseudoSleep(customCB, time.Duration(29)*time.Second)
	assert.Nil(t, succeed(customCB))
	assert.Equal(t, StateClosed, customCB.State())
	assert.Equal(t, Counts{11, 6, 5, 1, 0}, customCB.Counts())

	pseudoSleep(customCB, time.Duration(1)*time.Second) // over Interval
	assert.Nil(t, fail(customCB))
	assert.Equal(t, StateClosed, customCB.State())
	assert.Equal(t, Counts{1, 0, 1, 0, 1}, customCB.Counts())

	// StateClosed to StateOpen
	assert.Nil(t, succeed(customCB))
	assert.Nil(t, fail(customCB)) // failure ratio: 2/3 >= 0.6
	assert.Equal(t, StateOpen, customCB.State())
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, customCB.Counts())
	assert.False(t, customCB.expiry.IsZero())
	assert.Equal(t, StateChange{"cb", StateClosed, StateOpen}, stateChange)

	// StateOpen to StateHalfOpen
	pseudoSleep(customCB, time.Duration(90)*time.Second)
	assert.Equal(t, StateHalfOpen, customCB.State())
	assert.True(t, defaultCB.expiry.IsZero())
	assert.Equal(t, StateChange{"cb", StateOpen, StateHalfOpen}, stateChange)

	assert.Nil(t, succeed(customCB))
	assert.Nil(t, succeed(customCB))
	assert.Equal(t, StateHalfOpen, customCB.State())
	assert.Equal(t, Counts{2, 2, 0, 2, 0}, customCB.Counts())

	// StateHalfOpen to StateClosed
	ch := succeedLater(customCB, time.Duration(100)*time.Millisecond) // 3 consecutive successes
	time.Sleep(time.Duration(50) * time.Millisecond)
	assert.Equal(t, Counts{3, 2, 0, 2, 0}, customCB.Counts())
	assert.Error(t, succeed(customCB)) // over MaxRequests
	assert.Nil(t, <-ch)
	assert.Equal(t, StateClosed, customCB.State())
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, customCB.Counts())
	assert.False(t, customCB.expiry.IsZero())
	assert.Equal(t, StateChange{"cb", StateHalfOpen, StateClosed}, stateChange)
}

func TestRollingWindowCircuitBreaker(t *testing.T) {
	assert.Equal(t, "rw", rollingCB.Name())

	for i := 0; i < 5; i++ {
		assert.Nil(t, succeed(rollingCB))
		assert.Nil(t, fail(rollingCB))
	}
	assert.Equal(t, StateClosed, rollingCB.State())
	assert.Equal(t, Counts{10, 5, 5, 0, 1}, rollingCB.Counts())
	assert.Equal(t, 10, len(rollingCB.counts.buckets))
	assert.Equal(t, Counts{10, 5, 5, 0, 1}, rollingCB.counts.bucketAt(0))

	pseudoSleep(rollingCB, time.Duration(3)*time.Second)
	assert.Nil(t, succeed(rollingCB))
	assert.Equal(t, StateClosed, rollingCB.State())
	assert.Equal(t, Counts{11, 6, 5, 1, 0}, rollingCB.Counts())
	assert.Equal(t, 10, len(rollingCB.counts.buckets))
	// With circular buffer, previous bucket is at (current-1+len) % len
	assert.Equal(t, Counts{10, 5, 5, 0, 1}, rollingCB.counts.bucketAt(-1))
	assert.Equal(t, Counts{1, 1, 0, 1, 0}, rollingCB.counts.bucketAt(0))

	pseudoSleep(rollingCB, time.Duration(2)*time.Second)
	assert.Nil(t, succeed(rollingCB))
	assert.Equal(t, StateClosed, rollingCB.State())
	assert.Equal(t, 10, len(rollingCB.counts.buckets))
	assert.Equal(t, Counts{12, 7, 5, 2, 0}, rollingCB.Counts())
	// Previous bucket index
	assert.Equal(t, Counts{10, 5, 5, 0, 1}, rollingCB.counts.bucketAt(-1))
	assert.Equal(t, Counts{2, 2, 0, 2, 0}, rollingCB.counts.bucketAt(0))

	pseudoSleep(rollingCB, time.Duration(2)*time.Second)
	assert.Nil(t, succeed(rollingCB))
	assert.Equal(t, StateClosed, rollingCB.State())
	assert.Equal(t, Counts{13, 8, 5, 3, 0}, rollingCB.Counts())
	assert.Equal(t, 10, len(rollingCB.counts.buckets))
	// Calculate indices for buckets relative to current
	assert.Equal(t, Counts{10, 5, 5, 0, 1}, rollingCB.counts.bucketAt(-2))
	assert.Equal(t, Counts{2, 2, 0, 2, 0}, rollingCB.counts.bucketAt(-1))
	assert.Equal(t, Counts{1, 1, 0, 1, 0}, rollingCB.counts.bucketAt(0))

	pseudoSleep(rollingCB, time.Duration(2)*time.Second)
	assert.Nil(t, fail(rollingCB))
	assert.Equal(t, StateClosed, rollingCB.State())
	assert.Equal(t, Counts{14, 8, 6, 0, 1}, rollingCB.Counts())
	assert.Equal(t, 10, len(rollingCB.counts.buckets))
	// Calculate indices for buckets relative to current
	assert.Equal(t, Counts{10, 5, 5, 0, 1}, rollingCB.counts.bucketAt(-3))
	assert.Equal(t, Counts{2, 2, 0, 2, 0}, rollingCB.counts.bucketAt(-2))
	assert.Equal(t, Counts{1, 1, 0, 1, 0}, rollingCB.counts.bucketAt(-1))
	assert.Equal(t, Counts{1, 0, 1, 0, 1}, rollingCB.counts.bucketAt(0))

	// fill all the buckets
	for i := 0; i < 6; i++ {
		pseudoSleep(rollingCB, time.Duration(3)*time.Second)
		assert.Nil(t, succeed(rollingCB))
		assert.Nil(t, fail(rollingCB))
		assert.Equal(t, Counts{uint32(16 + 2*i), uint32(9 + i), uint32(7 + i), 0, 1}, rollingCB.Counts())
	}

	assert.Equal(t, 10, len(rollingCB.counts.buckets))

	// first bucket should be discarded
	pseudoSleep(rollingCB, time.Duration(3)*time.Second)
	assert.Nil(t, fail(rollingCB))
	assert.Equal(t, 10, len(rollingCB.counts.buckets))
	assert.Equal(t, Counts{17, 9, 8, 0, 2}, rollingCB.Counts())

	for i := 0; i < 5; i++ {
		assert.Nil(t, fail(rollingCB))
		assert.Equal(t, Counts{uint32(18 + i), 9, uint32(9 + i), 0, uint32(3 + i)}, rollingCB.Counts())
	}

	assert.Equal(t, StateClosed, rollingCB.State())

	assert.Nil(t, fail(rollingCB)) //failureRate = 14/23 > 0.6
	assert.Equal(t, StateOpen, rollingCB.State())
	assert.False(t, rollingCB.expiry.IsZero())
	assert.Equal(t, StateChange{"rw", StateClosed, StateOpen}, stateChange)

	// StateOpen to StateHalfOpen
	pseudoSleep(rollingCB, time.Duration(90)*time.Second)
	assert.Equal(t, StateHalfOpen, rollingCB.State())
	assert.True(t, rollingCB.expiry.IsZero())
	assert.Equal(t, StateChange{"rw", StateOpen, StateHalfOpen}, stateChange)

	assert.Nil(t, succeed(rollingCB))
	assert.Nil(t, succeed(rollingCB))
	assert.Equal(t, StateHalfOpen, rollingCB.State())
	assert.Equal(t, Counts{2, 2, 0, 2, 0}, rollingCB.Counts())

	// StateHalfOpen to StateClosed
	ch := succeedLater(rollingCB, time.Duration(100)*time.Millisecond) // 3 consecutive successes
	time.Sleep(time.Duration(50) * time.Millisecond)
	assert.Equal(t, Counts{3, 2, 0, 2, 0}, rollingCB.Counts())
	assert.Error(t, succeed(rollingCB)) // over MaxRequests
	assert.Nil(t, <-ch)
	assert.Equal(t, StateClosed, rollingCB.State())
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, rollingCB.Counts())
	assert.True(t, rollingCB.expiry.IsZero())
	assert.Equal(t, StateChange{"rw", StateHalfOpen, StateClosed}, stateChange)
}

func TestPanicInRequest(t *testing.T) {
	assert.Panics(t, func() { _ = causePanic(defaultCB) })
	assert.Equal(t, Counts{1, 0, 1, 0, 1}, defaultCB.Counts())
}

func TestGeneration(t *testing.T) {
	pseudoSleep(customCB, time.Duration(29)*time.Second)
	assert.Nil(t, succeed(customCB))
	ch := succeedLater(customCB, time.Duration(1500)*time.Millisecond)
	time.Sleep(time.Duration(500) * time.Millisecond)
	assert.Equal(t, Counts{2, 1, 0, 1, 0}, customCB.Counts())

	time.Sleep(time.Duration(500) * time.Millisecond) // over Interval
	assert.Equal(t, StateClosed, customCB.State())
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, customCB.Counts())

	// the request from the previous generation has no effect on customCB.windowCounts.Counts
	assert.Nil(t, <-ch)
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, customCB.Counts())
}

func TestCustomIsSuccessful(t *testing.T) {
	isSuccessful := func(error) bool {
		return true
	}
	cb := NewCircuitBreaker[bool](Settings{IsSuccessful: isSuccessful})

	for i := 0; i < 5; i++ {
		assert.Nil(t, fail(cb))
	}
	assert.Equal(t, StateClosed, cb.State())
	assert.Equal(t, Counts{5, 5, 0, 5, 0}, cb.Counts())

	cb.counts.clear()

	cb.isSuccessful = func(err error) bool {
		return err == nil
	}
	for i := 0; i < 6; i++ {
		assert.Nil(t, fail(cb))
	}
	assert.Equal(t, StateOpen, cb.State())

}

func TestCircuitBreakerInParallel(t *testing.T) {
	runtime.GOMAXPROCS(runtime.NumCPU())

	ch := make(chan error)

	const numReqs = 10000
	routine := func() {
		for i := 0; i < numReqs; i++ {
			ch <- succeed(customCB)
		}
	}

	const numRoutines = 10
	for i := 0; i < numRoutines; i++ {
		go routine()
	}

	total := uint32(numReqs * numRoutines)
	for i := uint32(0); i < total; i++ {
		err := <-ch
		assert.Nil(t, err)
	}
	assert.Equal(t, Counts{total, total, 0, total, 0}, customCB.Counts())
}

func TestRollingWindowCircuitBreakerInParallel(t *testing.T) {
	const numGoroutines = 10
	const numRequests = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < numRequests; j++ {
				if j%2 == 0 {
					assert.Nil(t, succeed(rollingCB))
				} else {
					assert.Nil(t, fail(rollingCB))
				}
			}
		}()
	}

	wg.Wait()
}

func TestOnStateChangeCtx_ExecuteCtx(t *testing.T) {
	var got struct {
		name string
		from State
		to   State
		val  any
	}

	st := Settings{
		Name:        "ctxcb1",
		MaxRequests: 3,
		Timeout:     2 * time.Second,
		ReadyToTrip: func(c Counts) bool { return c.ConsecutiveFailures >= 1 },
		OnStateChangeCtx: func(ctx context.Context, name string, from State, to State) {
			got.name = name
			got.from = from
			got.to = to
			got.val = ctx.Value(ctxKey("id"))
		},
	}
	cb := NewCircuitBreaker[bool](st)

	ctx := context.WithValue(context.Background(), ctxKey("id"), "exec1")
	_, err := cb.ExecuteCtx(ctx, func() (bool, error) { return false, assert.AnError })
	assert.Error(t, err)

	assert.Equal(t, "ctxcb1", got.name)
	assert.Equal(t, StateClosed, got.from)
	assert.Equal(t, StateOpen, got.to)
	assert.Equal(t, "exec1", got.val)
}

func TestOnStateChangeCtx_StateCtx_TimeoutTransition(t *testing.T) {
	var got struct {
		val      any
		from, to State
	}

	st := Settings{
		Name:        "ctxcb2",
		Timeout:     time.Second,
		ReadyToTrip: func(c Counts) bool { return c.ConsecutiveFailures >= 1 },
		OnStateChangeCtx: func(ctx context.Context, name string, from State, to State) {
			if name == "ctxcb2" {
				got.from = from
				got.to = to
				got.val = ctx.Value(ctxKey("poll"))
			}
		},
	}
	cb := NewCircuitBreaker[bool](st)
	// Trip to open
	_, _ = cb.ExecuteCtx(context.Background(), func() (bool, error) { return false, assert.AnError })
	assert.Equal(t, StateOpen, cb.State())

	// Move time and call StateCtx to trigger HalfOpen with provided ctx
	pseudoSleep(cb, st.Timeout+time.Millisecond)
	pollCtx := context.WithValue(context.Background(), ctxKey("poll"), "state-call")
	state := cb.StateCtx(pollCtx)
	assert.Equal(t, StateHalfOpen, state)
	assert.Equal(t, StateOpen, got.from)
	assert.Equal(t, StateHalfOpen, got.to)
	assert.Equal(t, "state-call", got.val)
}

func TestTwoStep_AllowCtx_ContextPropagation(t *testing.T) {
	var got struct {
		from, to State
		val      any
	}
	st := Settings{
		Name:        "twostep",
		MaxRequests: 2,
		ReadyToTrip: func(c Counts) bool { return c.ConsecutiveFailures >= 1 },
		OnStateChangeCtx: func(ctx context.Context, name string, from State, to State) {
			if name == "twostep" {
				got.from = from
				got.to = to
				got.val = ctx.Value(ctxKey("step"))
			}
		},
	}
	tscb := NewTwoStepCircuitBreaker[bool](st)

	ctx := context.WithValue(context.Background(), ctxKey("step"), "allow-ctx")
	done, err := tscb.AllowCtx(ctx)
	assert.NoError(t, err)
	done(false) // cause failure to trip to open

	assert.Equal(t, StateClosed, got.from)
	assert.Equal(t, StateOpen, got.to)
	assert.Equal(t, "allow-ctx", got.val)
}

func TestNoCallbacks_NoPanic(t *testing.T) {
	// Ensure no callbacks set does not panic on transitions.
	cb := NewCircuitBreaker[bool](Settings{ReadyToTrip: func(c Counts) bool { return c.ConsecutiveFailures >= 1 }})
	// Trip to open and then to half-open
	_, _ = cb.Execute(func() (bool, error) { return false, assert.AnError })
	assert.Equal(t, StateOpen, cb.State())
	pseudoSleep(cb, time.Second*2)
	_ = cb.State() // should transition as needed without panic
}
