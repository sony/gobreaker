package entities

import "time"

const ClosedStateName = "Closed"

type ClosedState struct {
	breaker *CircuitBreaker
}

func (s *ClosedState) Name() string {
	return ClosedStateName
}

func (s *ClosedState) beforeRequest(generation uint64) (uint64, error) {
	s.breaker.counts.OnRequest()
	return generation, nil
}

func (s *ClosedState) onSuccess(now time.Time) {
	s.breaker.counts.OnSuccess()
}

func (s *ClosedState) onFailure(now time.Time) {
	s.breaker.counts.OnFailure()
	if s.breaker.readyToTrip(s.breaker.counts) {
		s.breaker.setState(s.breaker.open, now)
	}
}

func (s *ClosedState) currentState(now time.Time) (State, uint64) {
	if !s.breaker.expiry.IsZero() && s.breaker.expiry.Before(now) {
		s.breaker.toNewGeneration(now)
	}
	return s.breaker.current, s.breaker.generation
}

func (s *ClosedState) toNewGeneration(now time.Time) {
	var zero time.Time
	if s.breaker.interval == 0 {
		s.breaker.expiry = zero
	} else {
		s.breaker.expiry = now.Add(s.breaker.interval)
	}
}
