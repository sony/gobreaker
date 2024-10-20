package entities

import (
	"time"
)

// State is a interface that represents a current state of CircuitBreaker.
type State interface {
	Name() string
	beforeRequest(generation uint64) (uint64, error)
	onSuccess(now time.Time)
	onFailure(now time.Time)
	currentState(now time.Time) (State, uint64)
	toNewGeneration(now time.Time)
}
