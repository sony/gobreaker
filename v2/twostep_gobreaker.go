package gobreaker

// TwoStepCircuitBreaker is like CircuitBreaker but instead of surrounding a function
// with the breaker functionality, it only checks whether a request can proceed and
// expects the caller to report the outcome in a separate step using a callback.
type TwoStepCircuitBreaker[T any] struct {
	cb *CircuitBreaker[T]
}

// NewTwoStepCircuitBreaker returns a new TwoStepCircuitBreaker configured with the given Settings.
func NewTwoStepCircuitBreaker[T any](st Settings) *TwoStepCircuitBreaker[T] {
	return &TwoStepCircuitBreaker[T]{
		cb: NewCircuitBreaker[T](st),
	}
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

// Allow checks if a new request can proceed.
// If the circuit breaker allow the new request, it returns a callback
// used to register the error object that the request will return in a separate step.
// If the circuit breaker does not allow the request, it returns an error.
//
// The returned callback reports whether its result was actually recorded by the
// CircuitBreaker: it returns false if the CircuitBreaker had already moved on to a new
// generation by the time the callback was invoked -- for example because the Open-state
// timeout expired and it transitioned to Half-Open, or because a concurrent request tripped
// it back to Open in the meantime. In that case the result is discarded rather than silently
// applied to a state the caller no longer observed. Most callers can ignore the return value;
// it exists for callers that need to detect and react to a dropped result (e.g. retrying the
// probe, or alerting that a Half-Open trial never got counted).
func (tscb *TwoStepCircuitBreaker[T]) Allow() (done func(err error) bool, err error) {
	generation, age, err := tscb.cb.beforeRequest()
	if err != nil {
		return nil, err
	}

	return func(err error) bool {
		return tscb.cb.afterRequest(generation, age, err)
	}, nil
}
