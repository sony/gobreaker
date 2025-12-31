package gobreaker

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var (
	errFailure  = errors.New("fail")
	errExcluded = errors.New("excluded")
)

type StateChange struct {
	name string
	from State
	to   State
}

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
	_, err := cb.Execute(func() (bool, error) { return false, errFailure })
	if errors.Is(err, errFailure) {
		return nil
	}
	return err
}

func exclude(cb *CircuitBreaker[bool]) error {
	_, err := cb.Execute(func() (bool, error) { return false, errExcluded })
	if errors.Is(err, errExcluded) {
		return nil
	}
	return err
}

func causePanic(cb *CircuitBreaker[bool]) error {
	_, err := cb.Execute(func() (bool, error) { panic("oops") })
	return err
}

func newCustom(stateChange *StateChange) *CircuitBreaker[bool] {
	var customSt Settings
	customSt.Name = "cb"
	customSt.MaxRequests = 3
	customSt.Interval = time.Duration(30) * time.Second
	customSt.Timeout = time.Duration(90) * time.Second
	customSt.ReadyToTrip = func(counts Counts) bool {
		numReqs := counts.Requests - counts.TotalExclusions
		failureRatio := float64(counts.TotalFailures) / float64(numReqs)

		return numReqs >= 3 && failureRatio >= 0.6
	}
	if stateChange != nil {
		customSt.OnStateChange = func(name string, from State, to State) {
			*stateChange = StateChange{name, from, to}
		}
	}
	customSt.Exclude = func(err error) bool {
		return errors.Is(err, errExcluded)
	}

	return NewCircuitBreaker[bool](customSt)
}

func newRollingWindow(stateChange *StateChange) *CircuitBreaker[bool] {
	var rollingWindowSt Settings
	rollingWindowSt.Name = "rw"
	rollingWindowSt.MaxRequests = 3
	rollingWindowSt.Interval = time.Duration(30) * time.Second
	rollingWindowSt.BucketPeriod = time.Duration(3) * time.Second
	rollingWindowSt.Timeout = time.Duration(90) * time.Second
	rollingWindowSt.ReadyToTrip = func(counts Counts) bool {
		numReqs := counts.Requests - counts.TotalExclusions
		failureRatio := float64(counts.TotalFailures) / float64(numReqs)

		return numReqs >= 3 && failureRatio >= 0.6
	}
	if stateChange != nil {
		rollingWindowSt.OnStateChange = func(name string, from State, to State) {
			*stateChange = StateChange{name, from, to}
		}
	}
	rollingWindowSt.Exclude = func(err error) bool {
		return errors.Is(err, errExcluded)
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
	assert.Nil(t, defaultCB.onStateChange)
	assert.Equal(t, StateClosed, defaultCB.state)
	assert.Equal(t, Counts{}, defaultCB.Counts())
	assert.True(t, defaultCB.expiry.IsZero())

	var stateChange StateChange
	customCB := newCustom(&stateChange)
	assert.Equal(t, "cb", customCB.name)
	assert.Equal(t, uint32(3), customCB.maxRequests)
	assert.Equal(t, time.Duration(30)*time.Second, customCB.interval)
	assert.Equal(t, time.Duration(90)*time.Second, customCB.timeout)
	assert.NotNil(t, customCB.readyToTrip)
	assert.NotNil(t, customCB.onStateChange)
	assert.Equal(t, StateClosed, customCB.state)
	assert.Equal(t, Counts{}, customCB.Counts())
	assert.False(t, customCB.expiry.IsZero())

	rollingWindowCB := newRollingWindow(nil)
	assert.Equal(t, "rw", rollingWindowCB.name)
	assert.Equal(t, uint32(3), rollingWindowCB.maxRequests)
	assert.Equal(t, time.Duration(30)*time.Second, rollingWindowCB.interval)
	assert.Equal(t, 10, len(rollingWindowCB.counts.buckets))
	assert.Equal(t, time.Duration(90)*time.Second, rollingWindowCB.timeout)
	assert.NotNil(t, rollingWindowCB.readyToTrip)
	assert.Nil(t, rollingWindowCB.onStateChange)
	assert.Equal(t, StateClosed, rollingWindowCB.state)
	assert.Equal(t, Counts{}, rollingWindowCB.Counts())
	assert.True(t, rollingWindowCB.expiry.IsZero())

	negativeDurationCB := newNegativeDurationCB()
	assert.Equal(t, "ncb", negativeDurationCB.name)
	assert.Equal(t, uint32(1), negativeDurationCB.maxRequests)
	assert.Equal(t, time.Duration(0)*time.Second, negativeDurationCB.interval)
	assert.Equal(t, time.Duration(60)*time.Second, negativeDurationCB.timeout)
	assert.NotNil(t, negativeDurationCB.readyToTrip)
	assert.Nil(t, negativeDurationCB.onStateChange)
	assert.Equal(t, StateClosed, negativeDurationCB.state)
	assert.Equal(t, Counts{}, negativeDurationCB.Counts())
	assert.True(t, negativeDurationCB.expiry.IsZero())
}

func TestDefaultCircuitBreaker(t *testing.T) {
	defaultCB := NewCircuitBreaker[bool](Settings{})
	assert.Equal(t, "", defaultCB.Name())

	for i := 0; i < 5; i++ {
		assert.Nil(t, fail(defaultCB))
	}
	assert.Equal(t, StateClosed, defaultCB.State())
	assert.Equal(t, Counts{Requests: 5, TotalFailures: 5, ConsecutiveFailures: 5}, defaultCB.Counts())

	assert.Nil(t, succeed(defaultCB))
	assert.Equal(t, StateClosed, defaultCB.State())
	assert.Equal(t, Counts{Requests: 6, TotalSuccesses: 1, TotalFailures: 5, ConsecutiveSuccesses: 1}, defaultCB.Counts())

	assert.Nil(t, fail(defaultCB))
	assert.Equal(t, StateClosed, defaultCB.State())
	assert.Equal(t, Counts{Requests: 7, TotalSuccesses: 1, TotalFailures: 6, ConsecutiveFailures: 1}, defaultCB.Counts())

	// StateClosed to StateOpen
	for i := 0; i < 5; i++ {
		assert.Nil(t, fail(defaultCB)) // 6 consecutive failures
	}
	assert.Equal(t, StateOpen, defaultCB.State())
	assert.Equal(t, Counts{}, defaultCB.Counts())
	assert.False(t, defaultCB.expiry.IsZero())

	assert.Error(t, succeed(defaultCB))
	assert.Error(t, fail(defaultCB))
	assert.Equal(t, Counts{}, defaultCB.Counts())

	pseudoSleep(defaultCB, time.Duration(59)*time.Second)
	assert.Equal(t, StateOpen, defaultCB.State())

	// StateOpen to StateHalfOpen
	pseudoSleep(defaultCB, time.Duration(2)*time.Second) // over Timeout
	assert.Equal(t, StateHalfOpen, defaultCB.State())
	assert.True(t, defaultCB.expiry.IsZero())

	// StateHalfOpen to StateOpen
	assert.Nil(t, fail(defaultCB))
	assert.Equal(t, StateOpen, defaultCB.State())
	assert.Equal(t, Counts{}, defaultCB.Counts())
	assert.False(t, defaultCB.expiry.IsZero())

	// StateOpen to StateHalfOpen
	pseudoSleep(defaultCB, time.Duration(60)*time.Second)
	assert.Equal(t, StateHalfOpen, defaultCB.State())
	assert.True(t, defaultCB.expiry.IsZero())

	// StateHalfOpen to StateClosed
	assert.Nil(t, succeed(defaultCB))
	assert.Equal(t, StateClosed, defaultCB.State())
	assert.Equal(t, Counts{}, defaultCB.Counts())
	assert.True(t, defaultCB.expiry.IsZero())
}

func TestCustomCircuitBreaker(t *testing.T) {
	var stateChange StateChange
	customCB := newCustom(&stateChange)
	assert.Equal(t, "cb", customCB.Name())

	for i := 0; i < 5; i++ {
		assert.Nil(t, succeed(customCB))
		assert.Nil(t, fail(customCB))
		assert.Nil(t, exclude(customCB))
	}
	assert.Equal(t, StateClosed, customCB.State())
	assert.Equal(t, Counts{Requests: 15, TotalSuccesses: 5, TotalFailures: 5, ConsecutiveFailures: 1, TotalExclusions: 5}, customCB.Counts())

	pseudoSleep(customCB, time.Duration(29)*time.Second)
	assert.Nil(t, succeed(customCB))
	assert.Equal(t, StateClosed, customCB.State())
	assert.Equal(t, Counts{Requests: 16, TotalSuccesses: 6, TotalFailures: 5, ConsecutiveSuccesses: 1, TotalExclusions: 5}, customCB.Counts())

	pseudoSleep(customCB, time.Duration(1)*time.Second) // over Interval
	assert.Nil(t, fail(customCB))
	assert.Equal(t, StateClosed, customCB.State())
	assert.Equal(t, Counts{Requests: 1, TotalFailures: 1, ConsecutiveFailures: 1}, customCB.Counts())

	// StateClosed to StateOpen
	assert.Nil(t, succeed(customCB))
	assert.Nil(t, fail(customCB)) // failure ratio: 2/3 >= 0.6
	assert.Equal(t, StateOpen, customCB.State())
	assert.Equal(t, Counts{}, customCB.Counts())
	assert.False(t, customCB.expiry.IsZero())
	assert.Equal(t, StateChange{"cb", StateClosed, StateOpen}, stateChange)

	// StateOpen to StateHalfOpen
	pseudoSleep(customCB, time.Duration(90)*time.Second)
	assert.Equal(t, StateHalfOpen, customCB.State())
	assert.True(t, customCB.expiry.IsZero())
	assert.Equal(t, StateChange{"cb", StateOpen, StateHalfOpen}, stateChange)

	// Excluded requests are neutral in Half-Open state.
	// They do not affect breaker counts or state transitions.
	// The condition to stay within MaxRequests should be evaluated as:
	//   (cb.counts.Requests - cb.counts.TotalExclusions) < cb.maxRequests
	// This ensures excluded requests don’t cause premature errors.
	assert.Nil(t, exclude(customCB))
	assert.Nil(t, exclude(customCB))
	assert.Nil(t, exclude(customCB))
	assert.Nil(t, exclude(customCB))
	assert.Equal(t, StateHalfOpen, customCB.State())

	// Transition: Half-Open → Open
	// In Half-Open, the first real failure immediately re-opens the breaker.
	assert.Nil(t, fail(customCB))
	assert.Equal(t, StateOpen, customCB.State())
	assert.Equal(t, Counts{}, customCB.Counts())
	assert.False(t, customCB.expiry.IsZero())
	assert.Equal(t, StateChange{"cb", StateHalfOpen, StateOpen}, stateChange)

	// Transition: Open → Half-Open (after timeout expires).
	pseudoSleep(customCB, time.Duration(90)*time.Second)
	assert.Equal(t, StateHalfOpen, customCB.State())

	// Successes increment counters but do not
	// close the breaker until the MaxRequests threshold is satisfied.
	assert.Nil(t, succeed(customCB))
	assert.Nil(t, succeed(customCB))
	assert.Equal(t, StateHalfOpen, customCB.State())
	assert.Equal(t, Counts{Requests: 2, TotalSuccesses: 2, ConsecutiveSuccesses: 2}, customCB.Counts())

	// StateHalfOpen to StateClosed
	ch := succeedLater(customCB, time.Duration(100)*time.Millisecond) // 3 consecutive successes
	time.Sleep(time.Duration(50) * time.Millisecond)
	assert.Equal(t, Counts{Requests: 3, TotalSuccesses: 2, ConsecutiveSuccesses: 2}, customCB.Counts())
	assert.Error(t, succeed(customCB)) // over MaxRequests
	assert.Nil(t, <-ch)
	assert.Equal(t, StateClosed, customCB.State())
	assert.Equal(t, Counts{}, customCB.Counts())
	assert.False(t, customCB.expiry.IsZero())
	assert.Equal(t, StateChange{"cb", StateHalfOpen, StateClosed}, stateChange)
}

func TestRollingWindowCircuitBreaker(t *testing.T) {
	var stateChange StateChange
	rollingCB := newRollingWindow(&stateChange)
	assert.Equal(t, "rw", rollingCB.Name())

	for i := 0; i < 5; i++ {
		assert.Nil(t, succeed(rollingCB))
		assert.Nil(t, fail(rollingCB))
		assert.Nil(t, exclude(rollingCB))
	}
	assert.Equal(t, StateClosed, rollingCB.State())
	assert.Equal(t, Counts{Requests: 15, TotalSuccesses: 5, TotalFailures: 5, ConsecutiveFailures: 1, TotalExclusions: 5}, rollingCB.Counts())
	assert.Equal(t, 10, len(rollingCB.counts.buckets))
	assert.Equal(t, Counts{Requests: 15, TotalSuccesses: 5, TotalFailures: 5, ConsecutiveFailures: 1, TotalExclusions: 5}, rollingCB.counts.bucketAt(0))

	pseudoSleep(rollingCB, time.Duration(3)*time.Second)
	assert.Nil(t, succeed(rollingCB))
	assert.Equal(t, StateClosed, rollingCB.State())
	assert.Equal(t, Counts{Requests: 16, TotalSuccesses: 6, TotalFailures: 5, ConsecutiveSuccesses: 1, TotalExclusions: 5}, rollingCB.Counts())
	assert.Equal(t, 10, len(rollingCB.counts.buckets))
	// With circular buffer, previous bucket is at (current-1+len) % len
	assert.Equal(t, Counts{Requests: 15, TotalSuccesses: 5, TotalFailures: 5, ConsecutiveFailures: 1, TotalExclusions: 5}, rollingCB.counts.bucketAt(-1))
	assert.Equal(t, Counts{Requests: 1, TotalSuccesses: 1, ConsecutiveSuccesses: 1}, rollingCB.counts.bucketAt(0))

	pseudoSleep(rollingCB, time.Duration(2)*time.Second)
	assert.Nil(t, succeed(rollingCB))
	assert.Nil(t, exclude(rollingCB))
	assert.Equal(t, StateClosed, rollingCB.State())
	assert.Equal(t, 10, len(rollingCB.counts.buckets))
	assert.Equal(t, Counts{Requests: 18, TotalSuccesses: 7, TotalFailures: 5, ConsecutiveSuccesses: 2, TotalExclusions: 6}, rollingCB.Counts())
	// Previous bucket index
	assert.Equal(t, Counts{Requests: 15, TotalSuccesses: 5, TotalFailures: 5, ConsecutiveFailures: 1, TotalExclusions: 5}, rollingCB.counts.bucketAt(-1))
	assert.Equal(t, Counts{Requests: 3, TotalSuccesses: 2, ConsecutiveSuccesses: 2, TotalExclusions: 1}, rollingCB.counts.bucketAt(0))

	pseudoSleep(rollingCB, time.Duration(2)*time.Second)
	// Capture age before undetermined request
	beforeAge := rollingCB.counts.age
	// Run an undetermined request
	assert.Nil(t, exclude(rollingCB))
	// Age should increase
	assert.Greater(t, rollingCB.counts.age, beforeAge)
	// Next success should increment counts normally
	assert.Nil(t, succeed(rollingCB))
	assert.Equal(t, StateClosed, rollingCB.State())
	assert.Equal(t, Counts{Requests: 20, TotalSuccesses: 8, TotalFailures: 5, ConsecutiveSuccesses: 3, TotalExclusions: 7}, rollingCB.Counts())
	assert.Equal(t, 10, len(rollingCB.counts.buckets))
	// Calculate indices for buckets relative to current
	assert.Equal(t, Counts{Requests: 15, TotalSuccesses: 5, TotalFailures: 5, ConsecutiveFailures: 1, TotalExclusions: 5}, rollingCB.counts.bucketAt(-2))
	assert.Equal(t, Counts{Requests: 3, TotalSuccesses: 2, ConsecutiveSuccesses: 2, TotalExclusions: 1}, rollingCB.counts.bucketAt(-1))
	assert.Equal(t, Counts{Requests: 2, TotalSuccesses: 1, ConsecutiveSuccesses: 1, TotalExclusions: 1}, rollingCB.counts.bucketAt(0))

	pseudoSleep(rollingCB, time.Duration(2)*time.Second)
	assert.Nil(t, fail(rollingCB))
	assert.Equal(t, StateClosed, rollingCB.State())
	assert.Equal(t, Counts{Requests: 21, TotalSuccesses: 8, TotalFailures: 6, ConsecutiveFailures: 1, TotalExclusions: 7}, rollingCB.Counts())
	assert.Equal(t, 10, len(rollingCB.counts.buckets))
	// Calculate indices for buckets relative to current
	assert.Equal(t, Counts{Requests: 15, TotalSuccesses: 5, TotalFailures: 5, ConsecutiveFailures: 1, TotalExclusions: 5}, rollingCB.counts.bucketAt(-3))
	assert.Equal(t, Counts{Requests: 3, TotalSuccesses: 2, ConsecutiveSuccesses: 2, TotalExclusions: 1}, rollingCB.counts.bucketAt(-2))
	assert.Equal(t, Counts{Requests: 2, TotalSuccesses: 1, ConsecutiveSuccesses: 1, TotalExclusions: 1}, rollingCB.counts.bucketAt(-1))
	assert.Equal(t, Counts{Requests: 1, TotalFailures: 1, ConsecutiveFailures: 1}, rollingCB.counts.bucketAt(0))

	// fill all the buckets
	for i := 0; i < 6; i++ {
		pseudoSleep(rollingCB, time.Duration(3)*time.Second)
		assert.Nil(t, succeed(rollingCB))
		assert.Nil(t, fail(rollingCB))
		assert.Equal(t, Counts{Requests: uint32(23 + 2*i), TotalSuccesses: uint32(9 + i), TotalFailures: uint32(7 + i), ConsecutiveFailures: 1, TotalExclusions: 7}, rollingCB.Counts())
	}

	assert.Equal(t, 10, len(rollingCB.counts.buckets))

	// first bucket should be discarded
	pseudoSleep(rollingCB, time.Duration(3)*time.Second)
	assert.Nil(t, fail(rollingCB))
	assert.Equal(t, 10, len(rollingCB.counts.buckets))
	assert.Equal(t, Counts{Requests: 19, TotalSuccesses: 9, TotalFailures: 8, ConsecutiveFailures: 2, TotalExclusions: 2}, rollingCB.Counts())

	for i := 0; i < 5; i++ {
		assert.Nil(t, fail(rollingCB))
		assert.Equal(t, Counts{Requests: uint32(20 + i), TotalSuccesses: 9, TotalFailures: uint32(9 + i), ConsecutiveFailures: uint32(3 + i), TotalExclusions: 2}, rollingCB.Counts())
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
	assert.Equal(t, Counts{Requests: 2, TotalSuccesses: 2, ConsecutiveSuccesses: 2}, rollingCB.Counts())

	// StateHalfOpen to StateClosed
	ch := succeedLater(rollingCB, time.Duration(100)*time.Millisecond) // 3 consecutive successes
	time.Sleep(time.Duration(50) * time.Millisecond)
	assert.Equal(t, Counts{Requests: 3, TotalSuccesses: 2, ConsecutiveSuccesses: 2}, rollingCB.Counts())
	assert.Error(t, succeed(rollingCB)) // over MaxRequests
	assert.Nil(t, <-ch)
	assert.Equal(t, StateClosed, rollingCB.State())
	assert.Equal(t, Counts{}, rollingCB.Counts())
	assert.True(t, rollingCB.expiry.IsZero())
	assert.Equal(t, StateChange{"rw", StateHalfOpen, StateClosed}, stateChange)
}

func TestPanicInRequest(t *testing.T) {
	defaultCB := NewCircuitBreaker[bool](Settings{})
	assert.Panics(t, func() { _ = causePanic(defaultCB) })
	assert.Equal(t, Counts{Requests: 1, TotalFailures: 1, ConsecutiveFailures: 1}, defaultCB.Counts())
}

func TestGeneration(t *testing.T) {
	customCB := newCustom(nil)
	pseudoSleep(customCB, time.Duration(29)*time.Second)
	assert.Nil(t, succeed(customCB))
	ch := succeedLater(customCB, time.Duration(1500)*time.Millisecond)
	time.Sleep(time.Duration(500) * time.Millisecond)
	assert.Equal(t, Counts{Requests: 2, TotalSuccesses: 1, ConsecutiveSuccesses: 1}, customCB.Counts())

	time.Sleep(time.Duration(500) * time.Millisecond) // over Interval
	assert.Equal(t, StateClosed, customCB.State())
	assert.Equal(t, Counts{}, customCB.Counts())

	// the request from the previous generation has no effect on customCB.windowCounts.Counts
	assert.Nil(t, <-ch)
	assert.Equal(t, Counts{}, customCB.Counts())
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
	assert.Equal(t, Counts{Requests: 5, TotalSuccesses: 5, ConsecutiveSuccesses: 5}, cb.Counts())

	cb.counts.clear()

	cb.outcomeEvaluator = outcomeEvaluatorFunc(Settings{
		IsSuccessful: func(err error) bool { return err == nil },
	})

	for i := 0; i < 6; i++ {
		assert.Nil(t, fail(cb))
	}
	assert.Equal(t, StateOpen, cb.State())

}

func TestCircuitBreakerInParallel(t *testing.T) {
	runtime.GOMAXPROCS(runtime.NumCPU())

	customCB := newCustom(nil)
	ch := make(chan error)

	var successCount, excludeCount atomic.Uint32

	const numReqs = 10000
	routine := func() {
		for i := 0; i < numReqs; i++ {
			var err error
			switch i % 2 {
			case 0:
				err = succeed(customCB)
				successCount.Add(1)
			case 1:
				err = exclude(customCB)
				excludeCount.Add(1)
			}

			ch <- err
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

	assert.Equal(t, Counts{Requests: total, TotalSuccesses: successCount.Load(), ConsecutiveSuccesses: successCount.Load(), TotalExclusions: excludeCount.Load()}, customCB.Counts())
}

func TestExcludeAndIsSuccessfulCombination(t *testing.T) {
	cb := NewCircuitBreaker[bool](Settings{
		Exclude: func(err error) bool {
			return errors.Is(err, errExcluded)
		},
		IsSuccessful: func(err error) bool {
			return err == nil
		},
	})

	// Case 1: excluded error -> should be Undetermined (not counted)
	for i := 0; i < 3; i++ {
		assert.Nil(t, exclude(cb))
	}
	assert.Equal(t, Counts{Requests: 3, TotalExclusions: 3}, cb.Counts(), "excluded errors must not be counted as success or failure")

	// Case 2: nil error -> IsSuccessful says "success"
	for i := 0; i < 3; i++ {
		assert.Nil(t, succeed(cb))
	}
	assert.Equal(t, Counts{Requests: 6, TotalSuccesses: 3, ConsecutiveSuccesses: 3, TotalExclusions: 3}, cb.Counts(), "nil errors should be counted as success per IsSuccessful")

	// Case 3: not nil error -> IsSuccessful says failure
	for i := 0; i < 2; i++ {
		assert.Nil(t, fail(cb))
	}
	assert.Equal(t, Counts{Requests: 8, TotalSuccesses: 3, TotalFailures: 2, ConsecutiveFailures: 2, TotalExclusions: 3}, cb.Counts(), "errors should be counted as failures per IsSuccessful")
}

func TestRollingWindowCircuitBreakerInParallel(t *testing.T) {
	rollingCB := newRollingWindow(nil)
	const numGoroutines = 12
	const numRequests = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < numRequests; j++ {
				switch j % 3 {
				case 0:
					assert.Nil(t, succeed(rollingCB)) // success
				case 1:
					assert.Nil(t, fail(rollingCB)) // failure
				case 2:
					assert.Nil(t, exclude(rollingCB)) // exclude
				}
			}
		}()
	}

	wg.Wait()
}
