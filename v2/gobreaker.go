// Package gobreaker implements the Circuit Breaker pattern.
// See https://msdn.microsoft.com/en-us/library/dn589784.aspx.
package gobreaker

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// State is a type that represents a state of CircuitBreaker.
type State int

// These constants are states of CircuitBreaker.
const (
	StateClosed State = iota
	StateHalfOpen
	StateOpen
)

var (
	// ErrTooManyRequests is returned when the CB state is half open and the requests count is over the cb maxRequests
	ErrTooManyRequests = errors.New("too many requests")
	// ErrOpenState is returned when the CB state is open
	ErrOpenState = errors.New("circuit breaker is open")
	// ErrRecovering is returned when the CB is closed but still recovering
	// and the request is not in the share of requests it lets through at the current step.
	// It wraps ErrTooManyRequests.
	ErrRecovering = fmt.Errorf("circuit breaker is recovering: %w", ErrTooManyRequests)
)

// String implements stringer interface.
func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateHalfOpen:
		return "half-open"
	case StateOpen:
		return "open"
	default:
		return fmt.Sprintf("unknown state: %d", s)
	}
}

// Settings configures CircuitBreaker:
//
// Name is the name of the CircuitBreaker.
//
// MaxRequests is the maximum number of requests allowed to pass through
// when the CircuitBreaker is half-open.
// If MaxRequests is 0, the CircuitBreaker allows only 1 request.
//
// Interval is the cyclic period of the closed state
// for the CircuitBreaker to clear the internal Counts.
// If Interval is less than or equal to 0, the CircuitBreaker does not clear internal Counts during the closed state.
//
// BucketPeriod defines the time duration for each bucket in the rolling window strategy.
// The internal Counts will be updated and reset gradually for each bucket.
// Interval will be automatically adjusted to be a multiple of BucketPeriod.
// If BucketPeriod is less than or equal to 0, the CircuitBreaker will use a fixed window strategy instead.
//
// Timeout is the period of the open state,
// after which the state of the CircuitBreaker becomes half-open.
// If Timeout is less than or equal to 0, the timeout value of the CircuitBreaker is set to 60 seconds.
//
// ReadyToTrip is called with a copy of Counts whenever a request fails in the closed state.
// If ReadyToTrip returns true, the CircuitBreaker will be placed into the open state.
// If ReadyToTrip is nil, default ReadyToTrip is used.
// Default ReadyToTrip returns true when the number of consecutive failures is more than 5.
//
// OnStateChange is called whenever the state of the CircuitBreaker changes.
//
// IsSuccessful is called with the error returned from a request.
// If IsSuccessful returns true, the error is counted as a success.
// Otherwise the error is counted as a failure.
// If IsSuccessful is nil, default IsSuccessful is used, which returns false for all non-nil errors.
//
// IsExcluded determines whether a request error should be ignored
// for the purposes of updating the circuit breaker metrics.
// If IsExcluded returns true for a given error,
// the request is neither counted as a success nor as a failure.
// This can be used, for example, to ignore context cancellations or
// other errors that should not affect the circuit breaker state.
// If IsExcluded is nil, no requests are excluded.
//
// Recovery enables gradual recovery: after the half-open state, the CircuitBreaker
// lets requests back in steps instead of all at once. See Recovery.
// If Recovery is nil, the CircuitBreaker lets all requests through as soon as it closes.
type Settings struct {
	Name          string
	MaxRequests   uint32
	Interval      time.Duration
	BucketPeriod  time.Duration
	Timeout       time.Duration
	ReadyToTrip   func(counts Counts) bool
	OnStateChange func(name string, from State, to State)
	IsSuccessful  func(err error) bool
	IsExcluded    func(err error) bool
	Recovery      *Recovery
}

// CircuitBreaker is a state machine to prevent sending requests that are likely to fail.
type CircuitBreaker[T any] struct {
	name          string
	maxRequests   uint32
	interval      time.Duration
	bucketPeriod  time.Duration
	timeout       time.Duration
	readyToTrip   func(counts Counts) bool
	isSuccessful  func(err error) bool
	isExcluded    func(err error) bool
	onStateChange func(name string, from State, to State)
	recovery      *recovery

	mutex      sync.Mutex
	state      State
	generation uint64
	counts     *rollingCounts
	start      time.Time
	expiry     time.Time
}

// NewCircuitBreaker returns a new CircuitBreaker configured with the given Settings.
func NewCircuitBreaker[T any](st Settings) *CircuitBreaker[T] {
	cb := new(CircuitBreaker[T])

	cb.name = st.Name
	cb.onStateChange = st.OnStateChange

	if st.MaxRequests == 0 {
		cb.maxRequests = 1
	} else {
		cb.maxRequests = st.MaxRequests
	}

	var numBuckets int64
	if st.Interval <= 0 {
		cb.interval = defaultInterval
		cb.bucketPeriod = cb.interval
		numBuckets = 1
	} else if st.BucketPeriod <= 0 {
		cb.interval = st.Interval
		cb.bucketPeriod = cb.interval
		numBuckets = 1
	} else {
		cb.interval = (st.Interval + st.BucketPeriod - 1) / st.BucketPeriod * st.BucketPeriod
		cb.bucketPeriod = st.BucketPeriod
		numBuckets = int64(cb.interval / cb.bucketPeriod)
	}

	cb.counts = newRollingCounts(numBuckets)

	if st.Timeout <= 0 {
		cb.timeout = defaultTimeout
	} else {
		cb.timeout = st.Timeout
	}

	if st.ReadyToTrip == nil {
		cb.readyToTrip = defaultReadyToTrip
	} else {
		cb.readyToTrip = st.ReadyToTrip
	}

	if st.IsSuccessful == nil {
		cb.isSuccessful = defaultIsSuccessful
	} else {
		cb.isSuccessful = st.IsSuccessful
	}

	if st.IsExcluded == nil {
		cb.isExcluded = defaultIsExcluded
	} else {
		cb.isExcluded = st.IsExcluded
	}

	cb.recovery = newRecovery(st.Recovery)

	cb.toNewGeneration(time.Now())

	return cb
}

const defaultInterval = time.Duration(0) * time.Second
const defaultTimeout = time.Duration(60) * time.Second

func defaultReadyToTrip(counts Counts) bool {
	return counts.ConsecutiveFailures > 5
}

func defaultIsSuccessful(err error) bool {
	return err == nil
}

func defaultIsExcluded(_ error) bool {
	return false
}

// Name returns the name of the CircuitBreaker.
func (cb *CircuitBreaker[T]) Name() string {
	return cb.name
}

// State returns the current state of the CircuitBreaker.
func (cb *CircuitBreaker[T]) State() State {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	now := time.Now()
	state, _, _ := cb.currentState(now)
	return state
}

// Counts returns internal counters
func (cb *CircuitBreaker[T]) Counts() Counts {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	return cb.counts.Counts
}

// Execute runs the given request if the CircuitBreaker accepts it.
// Execute returns an error instantly if the CircuitBreaker rejects the request.
// Otherwise, Execute returns the result of the request.
// If a panic occurs in the request, the CircuitBreaker handles it as an error
// and causes the same panic again.
func (cb *CircuitBreaker[T]) Execute(req func() (T, error)) (T, error) {
	adm, err := cb.beforeRequest()
	if err != nil {
		var defaultValue T
		return defaultValue, err
	}

	defer func() {
		e := recover()
		if e != nil {
			cb.afterRequest(adm, fmt.Errorf("%v", e))
			panic(e)
		}
	}()

	result, err := req()
	cb.afterRequest(adm, err)
	return result, err
}

// admission identifies a request the CircuitBreaker let through.
type admission struct {
	generation uint64
	age        uint64
	step       int // recovery step the request was let through at, or notRecovering
}

func (cb *CircuitBreaker[T]) beforeRequest() (admission, error) {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	now := time.Now()
	state, generation, age := cb.currentState(now)
	adm := admission{generation: generation, age: age, step: notRecovering}

	if state == StateOpen {
		return adm, ErrOpenState
	} else if state == StateHalfOpen && cb.counts.validRequests() >= cb.maxRequests {
		return adm, ErrTooManyRequests
	} else if state == StateClosed && cb.recovery != nil {
		step, ok := cb.recovery.admit(now)
		if !ok {
			return adm, ErrRecovering
		}
		adm.step = step
	}

	cb.counts.onRequest()
	return adm, nil
}

func (cb *CircuitBreaker[T]) afterRequest(adm admission, err error) {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	now := time.Now()
	state, generation, _ := cb.currentState(now)
	if generation != adm.generation {
		return
	}

	if cb.isExcluded(err) {
		cb.onExclusion(adm.age)
		return
	}

	if cb.isSuccessful(err) {
		cb.onSuccess(state, adm, now)
	} else {
		cb.onFailure(state, adm, now)
	}
}

func (cb *CircuitBreaker[T]) onSuccess(state State, adm admission, now time.Time) {
	switch state {
	case StateClosed:
		cb.counts.onSuccess(adm.age)
		if cb.recovery != nil {
			cb.recovery.onSuccess(adm.step)
		}
	case StateHalfOpen:
		cb.counts.onSuccess(adm.age)
		if cb.counts.ConsecutiveSuccesses >= cb.maxRequests {
			cb.setState(StateClosed, now)
		}
	}
}

func (cb *CircuitBreaker[T]) onFailure(state State, adm admission, now time.Time) {
	switch state {
	case StateClosed:
		cb.counts.onFailure(adm.age)
		readyToTrip := cb.readyToTrip
		if cb.recovery != nil && cb.recovery.onFailure(adm.step) {
			readyToTrip = cb.recovery.readyToTrip
		}
		if readyToTrip(cb.counts.Counts) {
			cb.setState(StateOpen, now)
		}
	case StateHalfOpen:
		cb.setState(StateOpen, now)
	}
}

func (cb *CircuitBreaker[T]) onExclusion(age uint64) {
	cb.counts.onExclusion(age)
}

func (cb *CircuitBreaker[T]) currentState(now time.Time) (State, uint64, uint64) {
	switch cb.state {
	case StateClosed:
		if cb.recovery != nil {
			// Recovery may end here; then the clearing it put off comes before the next request.
			cb.recovery.update(now)
		}
		if !cb.expiry.IsZero() && cb.expiry.Before(now) && !cb.recovering() {
			cb.toNewGeneration(now)
		} else if len(cb.counts.buckets) >= 2 {
			cb.counts.grow(cb.age(now))
		}
	case StateOpen:
		if cb.expiry.Before(now) {
			cb.setState(StateHalfOpen, now)
		}
	}
	return cb.state, cb.generation, cb.counts.age
}

// recovering reports whether the CircuitBreaker is closed and recovering.
func (cb *CircuitBreaker[T]) recovering() bool {
	return cb.recovery != nil && cb.recovery.state.Active
}

func (cb *CircuitBreaker[T]) age(now time.Time) uint64 {
	if cb.bucketPeriod == 0 {
		return 0
	}

	elapsed := now.Sub(cb.start)
	age := int64(elapsed / cb.bucketPeriod)
	if age < 0 {
		return 0
	}
	return uint64(age)
}

func (cb *CircuitBreaker[T]) setState(state State, now time.Time) {
	if cb.state == state {
		return
	}

	prev := cb.state
	cb.state = state

	if cb.recovery != nil {
		cb.recovery.onStateChange(state, now)
	}

	cb.toNewGeneration(now)

	if cb.onStateChange != nil {
		cb.onStateChange(cb.name, prev, state)
	}
}

func (cb *CircuitBreaker[T]) toNewGeneration(now time.Time) {
	cb.generation++
	cb.start = now
	cb.counts.clear()

	var zero time.Time
	switch cb.state {
	case StateClosed:
		if cb.interval == 0 || len(cb.counts.buckets) >= 2 {
			cb.expiry = zero
		} else {
			cb.expiry = now.Add(cb.interval)
		}
	case StateOpen:
		cb.expiry = now.Add(cb.timeout)
	default: // StateHalfOpen
		cb.expiry = zero
	}
}
