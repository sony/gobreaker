package gobreaker

import (
	"context"
	"fmt"
	"time"
)

// SharedState represents the shared state of DistributedCircuitBreaker.
type SharedState struct {
	State      State     `json:"state"`
	Generation uint64    `json:"generation"`
	Counts     Counts    `json:"counts"`
	Expiry     time.Time `json:"expiry"`
}

type SharedStateStore interface {
	GetState(ctx context.Context) (SharedState, error)
	SetState(ctx context.Context, state SharedState) error
}

// DistributedCircuitBreaker extends CircuitBreaker with distributed state storage
type DistributedCircuitBreaker[T any] struct {
	*CircuitBreaker[T]
	store SharedStateStore
}

// NewDistributedCircuitBreaker returns a new DistributedCircuitBreaker configured with the given StorageSettings
func NewDistributedCircuitBreaker[T any](store SharedStateStore, settings Settings) *DistributedCircuitBreaker[T] {
	cb := NewCircuitBreaker[T](settings)
	return &DistributedCircuitBreaker[T]{
		CircuitBreaker: cb,
		store:          store,
	}
}

func (dcb *DistributedCircuitBreaker[T]) State(ctx context.Context) State {
	if dcb.store == nil {
		return dcb.CircuitBreaker.State()
	}

	state, err := dcb.store.GetState(ctx)
	if err != nil {
		// Fallback to in-memory state if Storage fails
		return dcb.CircuitBreaker.State()
	}

	now := time.Now()
	currentState, _ := dcb.currentState(state, now)

	// Update the state in Storage if it has changed
	if currentState != state.State {
		state.State = currentState
		if err := dcb.store.SetState(ctx, state); err != nil {
			// Log the error, but continue with the current state
			fmt.Printf("Failed to update state in storage: %v\n", err)
		}
	}

	return state.State
}

// Execute runs the given request if the DistributedCircuitBreaker accepts it
func (dcb *DistributedCircuitBreaker[T]) Execute(ctx context.Context, req func() (T, error)) (T, error) {
	if dcb.store == nil {
		return dcb.CircuitBreaker.Execute(req)
	}
	generation, err := dcb.beforeRequest(ctx)
	if err != nil {
		var zero T
		return zero, err
	}

	defer func() {
		e := recover()
		if e != nil {
			dcb.afterRequest(ctx, generation, false)
			panic(e)
		}
	}()

	result, err := req()
	dcb.afterRequest(ctx, generation, dcb.isSuccessful(err))

	return result, err
}

func (dcb *DistributedCircuitBreaker[T]) beforeRequest(ctx context.Context) (uint64, error) {
	state, err := dcb.store.GetState(ctx)
	if err != nil {
		return 0, err
	}
	now := time.Now()
	currentState, generation := dcb.currentState(state, now)

	if currentState != state.State {
		dcb.setState(&state, currentState, now)
		err = dcb.store.SetState(ctx, state)
		if err != nil {
			return 0, err
		}
	}

	if currentState == StateOpen {
		return generation, ErrOpenState
	} else if currentState == StateHalfOpen && state.Counts.Requests >= dcb.maxRequests {
		return generation, ErrTooManyRequests
	}

	state.Counts.onRequest()
	err = dcb.store.SetState(ctx, state)
	if err != nil {
		return 0, err
	}

	return generation, nil
}

func (dcb *DistributedCircuitBreaker[T]) afterRequest(ctx context.Context, before uint64, success bool) {
	state, err := dcb.store.GetState(ctx)
	if err != nil {
		return
	}
	now := time.Now()
	currentState, generation := dcb.currentState(state, now)
	if generation != before {
		return
	}

	if success {
		dcb.onSuccess(&state, currentState, now)
	} else {
		dcb.onFailure(&state, currentState, now)
	}

	dcb.store.SetState(ctx, state)
}

func (dcb *DistributedCircuitBreaker[T]) onSuccess(state *SharedState, currentState State, now time.Time) {
	if state.State == StateOpen {
		state.State = currentState
	}

	switch currentState {
	case StateClosed:
		state.Counts.onSuccess()
	case StateHalfOpen:
		state.Counts.onSuccess()
		if state.Counts.ConsecutiveSuccesses >= dcb.maxRequests {
			dcb.setState(state, StateClosed, now)
		}
	}
}

func (dcb *DistributedCircuitBreaker[T]) onFailure(state *SharedState, currentState State, now time.Time) {
	switch currentState {
	case StateClosed:
		state.Counts.onFailure()
		if dcb.readyToTrip(state.Counts) {
			dcb.setState(state, StateOpen, now)
		}
	case StateHalfOpen:
		dcb.setState(state, StateOpen, now)
	}
}

func (dcb *DistributedCircuitBreaker[T]) currentState(state SharedState, now time.Time) (State, uint64) {
	switch state.State {
	case StateClosed:
		if !state.Expiry.IsZero() && state.Expiry.Before(now) {
			dcb.toNewGeneration(&state, now)
		}
	case StateOpen:
		if state.Expiry.Before(now) {
			dcb.setState(&state, StateHalfOpen, now)
		}
	}
	return state.State, state.Generation
}

func (dcb *DistributedCircuitBreaker[T]) setState(state *SharedState, newState State, now time.Time) {
	if state.State == newState {
		return
	}

	prev := state.State
	state.State = newState

	dcb.toNewGeneration(state, now)

	if dcb.onStateChange != nil {
		dcb.onStateChange(dcb.name, prev, newState)
	}
}

func (dcb *DistributedCircuitBreaker[T]) toNewGeneration(state *SharedState, now time.Time) {
	state.Generation++
	state.Counts.clear()

	var zero time.Time
	switch state.State {
	case StateClosed:
		if dcb.interval == 0 {
			state.Expiry = zero
		} else {
			state.Expiry = now.Add(dcb.interval)
		}
	case StateOpen:
		state.Expiry = now.Add(dcb.timeout)
	default: // StateHalfOpen
		state.Expiry = zero
	}
}
