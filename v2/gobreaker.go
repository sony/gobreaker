// Package gobreaker implements the Circuit Breaker pattern.
// See https://msdn.microsoft.com/en-us/library/dn589784.aspx.
package gobreaker

import (
	"container/list"
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

// Counts holds the numbers of requests and their successes/failures.
// CircuitBreaker clears the internal Counts either
// on the change of the state or at the closed-state intervals.
// Counts ignores the results of the requests sent before clearing.
type Counts struct {
	Requests             uint32
	TotalSuccesses       uint32
	TotalFailures        uint32
	ConsecutiveSuccesses uint32
	ConsecutiveFailures  uint32
}

type WindowCounts struct {
	Counts
	mutex sync.Mutex
	// counts       Counts
	numBuckets       int64
	bucketCounts     *list.List
	bucketGeneration int
}

func NewWindowCounts(numBuckets int64) *WindowCounts {
	return &WindowCounts{
		numBuckets:   numBuckets,
		bucketCounts: list.New(),
	}
}

func (w *WindowCounts) ToCounts() Counts {
	return w.Counts
	// return Counts{
	// 	Requests:             w.Requests,
	// 	TotalSuccesses:       w.TotalSuccesses,
	// 	TotalFailures:        w.TotalFailures,
	// 	ConsecutiveSuccesses: w.ConsecutiveSuccesses,
	// 	ConsecutiveFailures:  w.ConsecutiveFailures,
	// }
}

func (w *WindowCounts) FromCounts(counts Counts, bucketCounts []Counts) {
	w.Counts = counts
	// w.Counts.Requests = counts.Requests
	// w.Counts.TotalSuccesses = counts.TotalSuccesses
	// w.Counts.TotalFailures = counts.TotalFailures
	// w.Counts.ConsecutiveSuccesses = counts.ConsecutiveSuccesses
	// w.Counts.ConsecutiveFailures = counts.ConsecutiveFailures

	w.bucketCounts.Init()
	for _, buckCounts := range bucketCounts {
		w.bucketCounts.PushBack(buckCounts)
	}
}

func (w *WindowCounts) onRequest() {
	currentBucket := w.bucketCounts.Back()
	currentBucketValue, _ := currentBucket.Value.(Counts)

	currentBucketValue.Requests++
	w.Requests++
	currentBucket.Value = currentBucketValue
}

func (w *WindowCounts) onSuccess() {
	currentBucket := w.bucketCounts.Back()
	currentBucketValue, _ := currentBucket.Value.(Counts)

	currentBucketValue.TotalSuccesses++
	w.TotalSuccesses++

	currentBucketValue.ConsecutiveSuccesses++
	w.ConsecutiveSuccesses++

	currentBucketValue.ConsecutiveFailures = 0
	w.ConsecutiveFailures = 0

	currentBucket.Value = currentBucketValue
}

func (w *WindowCounts) onFailure() {
	currentBucket := w.bucketCounts.Back()
	currentBucketValue, _ := currentBucket.Value.(Counts)

	currentBucketValue.TotalFailures++
	w.TotalFailures++

	currentBucketValue.ConsecutiveFailures++
	w.ConsecutiveFailures++

	currentBucketValue.ConsecutiveSuccesses = 0
	w.ConsecutiveSuccesses = 0

	currentBucket.Value = currentBucketValue
}

func (w *WindowCounts) clear() {
	w.Requests = 0
	w.TotalSuccesses = 0
	w.TotalFailures = 0
	w.ConsecutiveSuccesses = 0
	w.ConsecutiveFailures = 0

	w.bucketGeneration = 0

	w.bucketCounts.Init()
	w.bucketCounts.PushBack(Counts{})
}

func (w *WindowCounts) rotate() {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	w.bucketCounts.PushBack(Counts{})

	if w.bucketCounts.Len() <= int(w.numBuckets) {
		return
	}

	oldBucket := w.bucketCounts.Front()

	if oldBucket != nil {
		oldBucketCount, _ := oldBucket.Value.(Counts)

		if w.ConsecutiveSuccesses == w.TotalSuccesses {
			w.ConsecutiveSuccesses -= oldBucketCount.ConsecutiveSuccesses
		}

		if w.ConsecutiveFailures == w.TotalFailures {
			w.ConsecutiveFailures -= oldBucketCount.ConsecutiveFailures
		}

		w.Requests -= oldBucketCount.Requests
		w.TotalSuccesses -= oldBucketCount.TotalSuccesses
		w.TotalFailures -= oldBucketCount.TotalFailures
		w.bucketCounts.Remove(oldBucket)
	}
}

func (c *Counts) clear() {
	c.Requests = 0
	c.TotalSuccesses = 0
	c.TotalFailures = 0
	c.ConsecutiveSuccesses = 0
	c.ConsecutiveFailures = 0
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
// If Interval is less than or equal to 0, the CircuitBreaker doesn't clear internal Counts during the closed state.
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
type Settings struct {
	Name          string
	MaxRequests   uint32
	Interval      time.Duration
	BucketPeriod  time.Duration
	Timeout       time.Duration
	ReadyToTrip   func(counts Counts) bool
	OnStateChange func(name string, from State, to State)
	IsSuccessful  func(err error) bool
}

// CircuitBreaker is a state machine to prevent sending requests that are likely to fail.
type CircuitBreaker[T any] struct {
	name        string
	maxRequests uint32
	interval    time.Duration
	// numBuckets    int64
	timeout       time.Duration
	readyToTrip   func(counts Counts) bool
	isSuccessful  func(err error) bool
	onStateChange func(name string, from State, to State)

	mutex      sync.Mutex
	state      State
	generation uint64
	// counts       Counts
	// bucketCounts *list.List
	windowCounts *WindowCounts
	expiry       time.Time
}

// TwoStepCircuitBreaker is like CircuitBreaker but instead of surrounding a function
// with the breaker functionality, it only checks whether a request can proceed and
// expects the caller to report the outcome in a separate step using a callback.
type TwoStepCircuitBreaker[T any] struct {
	cb *CircuitBreaker[T]
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

	var numBuckets int64 = 1
	if st.Interval <= 0 {
		cb.interval = defaultInterval
	} else {
		if st.BucketPeriod <= 0 || st.BucketPeriod == st.Interval {
			cb.interval = st.Interval
		} else {
			cb.interval = st.BucketPeriod
			numBuckets = st.Interval.Milliseconds() / st.BucketPeriod.Milliseconds()
		}
	}

	cb.windowCounts = NewWindowCounts(numBuckets)

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

	cb.toNewGeneration(time.Now())

	return cb
}

// NewTwoStepCircuitBreaker returns a new TwoStepCircuitBreaker configured with the given Settings.
func NewTwoStepCircuitBreaker[T any](st Settings) *TwoStepCircuitBreaker[T] {
	return &TwoStepCircuitBreaker[T]{
		cb: NewCircuitBreaker[T](st),
	}
}

const defaultInterval = time.Duration(0) * time.Second
const defaultTimeout = time.Duration(60) * time.Second

func defaultReadyToTrip(counts Counts) bool {
	return counts.ConsecutiveFailures > 5
}

func defaultIsSuccessful(err error) bool {
	return err == nil
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
	state, _ := cb.currentState(now)
	return state
}

// Counts returns internal counters
func (cb *CircuitBreaker[T]) Counts() Counts {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	return cb.windowCounts.ToCounts()
}

// Execute runs the given request if the CircuitBreaker accepts it.
// Execute returns an error instantly if the CircuitBreaker rejects the request.
// Otherwise, Execute returns the result of the request.
// If a panic occurs in the request, the CircuitBreaker handles it as an error
// and causes the same panic again.
func (cb *CircuitBreaker[T]) Execute(req func() (T, error)) (T, error) {
	generation, err := cb.beforeRequest()
	if err != nil {
		var defaultValue T
		return defaultValue, err
	}

	defer func() {
		e := recover()
		if e != nil {
			cb.afterRequest(generation, false)
			panic(e)
		}
	}()

	result, err := req()
	cb.afterRequest(generation, cb.isSuccessful(err))
	return result, err
}

// Name returns the name of the TwoStepCircuitBreaker.
func (tscb *TwoStepCircuitBreaker[T]) Name() string {
	return tscb.cb.Name()
}

// State returns the current state of the TwoStepCircuitBreaker.
func (tscb *TwoStepCircuitBreaker[T]) State() State {
	return tscb.cb.State()
}

// Counts returns internal counters
func (tscb *TwoStepCircuitBreaker[T]) Counts() Counts {
	return tscb.cb.Counts()
}

// Allow checks if a new request can proceed. It returns a callback that should be used to
// register the success or failure in a separate step. If the circuit breaker doesn't allow
// requests, it returns an error.
func (tscb *TwoStepCircuitBreaker[T]) Allow() (done func(success bool), err error) {
	generation, err := tscb.cb.beforeRequest()
	if err != nil {
		return nil, err
	}

	return func(success bool) {
		tscb.cb.afterRequest(generation, success)
	}, nil
}

func (cb *CircuitBreaker[T]) beforeRequest() (uint64, error) {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	now := time.Now()
	state, generation := cb.currentState(now)

	if state == StateOpen {
		return generation, ErrOpenState
	} else if state == StateHalfOpen && cb.windowCounts.Requests >= cb.maxRequests {
		return generation, ErrTooManyRequests
	}

	cb.windowCounts.onRequest()
	return generation, nil
}

func (cb *CircuitBreaker[T]) afterRequest(before uint64, success bool) {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	now := time.Now()
	state, generation := cb.currentState(now)
	if generation != before {
		return
	}

	if success {
		cb.onSuccess(state, now)
	} else {
		cb.onFailure(state, now)
	}
}

func (cb *CircuitBreaker[T]) onSuccess(state State, now time.Time) {
	switch state {
	case StateClosed:
		cb.windowCounts.onSuccess()
	case StateHalfOpen:
		cb.windowCounts.onSuccess()
		if cb.windowCounts.ConsecutiveSuccesses >= cb.maxRequests {
			cb.setState(StateClosed, now)
		}
	}
}

func (cb *CircuitBreaker[T]) onFailure(state State, now time.Time) {
	switch state {
	case StateClosed:
		cb.windowCounts.onFailure()
		if cb.readyToTrip(cb.windowCounts.ToCounts()) {
			cb.setState(StateOpen, now)
		}
	case StateHalfOpen:
		cb.setState(StateOpen, now)
	}
}

func (cb *CircuitBreaker[T]) currentState(now time.Time) (State, uint64) {
	switch cb.state {
	case StateClosed:
		if !cb.expiry.IsZero() && cb.expiry.Before(now) {
			cb.toNewBucket(cb.expiry)
		}
	case StateOpen:
		if cb.expiry.Before(now) {
			cb.setState(StateHalfOpen, now)
		}
	}
	return cb.state, cb.generation
}

func (cb *CircuitBreaker[T]) setState(state State, now time.Time) {
	if cb.state == state {
		return
	}

	prev := cb.state
	cb.state = state

	cb.toNewGeneration(now)

	if cb.onStateChange != nil {
		cb.onStateChange(cb.name, prev, state)
	}
}

func (cb *CircuitBreaker[T]) updateExpiry(now time.Time) {
	var zero time.Time
	switch cb.state {
	case StateClosed:
		if cb.interval == 0 {
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

func (cb *CircuitBreaker[T]) toNewGeneration(now time.Time) {
	cb.generation++

	cb.windowCounts.clear()
	cb.updateExpiry(now)
}

func (cb *CircuitBreaker[T]) toNewBucket(lastExpiry time.Time) {
	cb.windowCounts.bucketGeneration++
	if cb.windowCounts.bucketGeneration == int(cb.windowCounts.numBuckets) {
		cb.generation++
	}
	cb.windowCounts.rotate()
	cb.updateExpiry(lastExpiry)
}
