package entities

import (
	"github.com/github.com/jorelcb/golang-circuit-breaker/pkg/circuit_breaker/domain/model/entities/errors"
	"time"
)

const OpenStateName = "Open"

type OpenState struct {
	breaker *CircuitBreaker
}

func (s *OpenState) Name() string {
	return OpenStateName
}

func (s *OpenState) beforeRequest(generation uint64) (uint64, error) {
	return generation, errors.OpenStateErr
}

func (s *OpenState) onSuccess(now time.Time) {
	// TODO: review this
	return
}

func (s *OpenState) onFailure(now time.Time) {
	// TODO: review this
	return
}

func (s *OpenState) currentState(now time.Time) (State, uint64) {
	if s.breaker.expiry.Before(now) {
		s.breaker.setState(s.breaker.half, now)
	}
	return s.breaker.current, s.breaker.generation
}

func (s *OpenState) toNewGeneration(now time.Time) {
	s.breaker.expiry = now.Add(s.breaker.timeout)
}
