package entities

import (
	"sync"
	"time"
)

// CircuitBreaker is a current machine to prevent sending requests that are likely to fail.
type CircuitBreaker struct {
	name string

	maxRequests   uint32
	interval      time.Duration
	timeout       time.Duration
	readyToTrip   func(counts RequestsCounts) bool
	isSuccessful  func(err error) bool
	onStateChange func(name string, from State, to State)

	mutex      sync.Mutex
	current    State
	closed     State
	open       State
	half       State
	generation uint64
	counts     RequestsCounts
	expiry     time.Time
}

// NewCircuitBreaker returns a new CircuitBreaker configured with the given Settings.
func NewCircuitBreaker(options ...Option) *CircuitBreaker {

	conf := newConfig(options)

	cb := &CircuitBreaker{}

	cb.closed = &ClosedState{breaker: cb}
	cb.open = &OpenState{breaker: cb}
	cb.half = &HalfOpenState{breaker: cb}

	cb.setState(cb.closed, time.Now())

	cb.name = conf.name

	cb.maxRequests = conf.maxRequests
	cb.interval = conf.interval
	cb.timeout = conf.timeout

	cb.readyToTrip = conf.readyToTrip
	cb.isSuccessful = conf.isSuccessful
	cb.onStateChange = conf.onStateChange

	cb.toNewGeneration(time.Now())
	return cb
}

// Name returns the name of the CircuitBreaker.
func (cb *CircuitBreaker) Name() string {
	return cb.name
}

// State returns the current state of the CircuitBreaker.
func (cb *CircuitBreaker) State() State {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	now := time.Now()
	state, _ := cb.currentState(now)
	return state
}

// RequestsCounts returns internal counters
func (cb *CircuitBreaker) RequestsCounts() RequestsCounts {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	return cb.counts
}

// Execute runs the given request if the CircuitBreaker accepts it.
// Execute returns an error instantly if the CircuitBreaker rejects the request.
// Otherwise, Execute returns the result of the request.
// If a panic occurs in the request, the CircuitBreaker handles it as an error
// and causes the same panic again.
func (cb *CircuitBreaker) Execute(req func() (interface{}, error)) (interface{}, error) {
	generation, err := cb.beforeRequest()
	if err != nil {
		return nil, err
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

func (cb *CircuitBreaker) beforeRequest() (uint64, error) {
	var generation uint64
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	now := time.Now()
	cb.current, generation = cb.currentState(now)
	return cb.current.beforeRequest(generation)
}

func (cb *CircuitBreaker) afterRequest(before uint64, success bool) {
	var generation uint64
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	now := time.Now()
	cb.current, generation = cb.currentState(now)
	if generation != before {
		return
	}

	if success {
		cb.onSuccess(now)
	} else {
		cb.onFailure(now)
	}
}

func (cb *CircuitBreaker) onSuccess(now time.Time) {
	cb.current.onSuccess(now)
}

func (cb *CircuitBreaker) onFailure(now time.Time) {
	cb.current.onFailure(now)
}

func (cb *CircuitBreaker) currentState(now time.Time) (State, uint64) {
	cb.current.currentState(now)
	return cb.current, cb.generation
}

func (cb *CircuitBreaker) setState(state State, now time.Time) {
	if cb.current != nil && cb.current.Name() == state.Name() {
		return
	}

	prev := cb.current
	cb.current = state

	cb.toNewGeneration(now)

	if cb.onStateChange != nil {
		cb.onStateChange(cb.name, prev, state)
	}
}

func (cb *CircuitBreaker) toNewGeneration(now time.Time) {
	cb.generation++
	cb.counts.Clear()
	cb.current.toNewGeneration(now)
}
