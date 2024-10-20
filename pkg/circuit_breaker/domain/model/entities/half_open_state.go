package entities

import (
	"github.com/github.com/jorelcb/golang-circuit-breaker/pkg/circuit_breaker/domain/model/entities/errors"
	"time"
)

const HalfOpenStateName = "HalfOpen"

type HalfOpenState struct {
	breaker *CircuitBreaker
}

func (s *HalfOpenState) Name() string {
	return HalfOpenStateName
}

func (s *HalfOpenState) beforeRequest(generation uint64) (uint64, error) {
	if s.breaker.counts.Requests >= s.breaker.maxRequests {
		return generation, errors.TooManyRequestsError
	}
	s.breaker.counts.OnRequest()
	return generation, nil
}

func (s *HalfOpenState) onSuccess(now time.Time) {
	s.breaker.counts.OnSuccess()
	if s.breaker.counts.ConsecutiveSuccesses >= s.breaker.maxRequests {
		s.breaker.setState(s.breaker.closed, now)
	}
}

func (s *HalfOpenState) onFailure(now time.Time) {
	s.breaker.setState(s.breaker.open, now)
}

func (s *HalfOpenState) currentState(now time.Time) (State, uint64) {
	return s.breaker.current, s.breaker.generation
}

func (s *HalfOpenState) toNewGeneration(now time.Time) {
	var zero time.Time
	s.breaker.expiry = zero
}
