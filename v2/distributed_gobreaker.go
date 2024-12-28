package gobreaker

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var (
	// ErrNoSharedStore is returned when there is no shared store.
	ErrNoSharedStore = errors.New("no shared store")
	// ErrNoSharedState is returned when there is no shared state.
	ErrNoSharedState = errors.New("no shared state")
)

// SharedState represents the shared state of DistributedCircuitBreaker.
type SharedState struct {
	State      State     `json:"state"`
	Generation uint64    `json:"generation"`
	Counts     Counts    `json:"counts"`
	Expiry     time.Time `json:"expiry"`
}

// SharedDataStore stores the shared state of DistributedCircuitBreaker.
type SharedDataStore interface {
	GetData(ctx context.Context, name string) ([]byte, error)
	SetData(ctx context.Context, name string, data []byte) error
}

// DistributedCircuitBreaker extends CircuitBreaker with SharedDataStore.
type DistributedCircuitBreaker[T any] struct {
	*CircuitBreaker[T]
	store SharedDataStore
}

// NewDistributedCircuitBreaker returns a new DistributedCircuitBreaker.
func NewDistributedCircuitBreaker[T any](ctx context.Context, store SharedDataStore, settings Settings) (*DistributedCircuitBreaker[T], error) {
	if store == nil {
		return nil, ErrNoSharedStore
	}

	dcb := &DistributedCircuitBreaker[T]{
		CircuitBreaker: NewCircuitBreaker[T](settings),
		store:          store,
	}
	state := SharedState{
		State:      dcb.state,
		Generation: dcb.generation,
		Counts:     dcb.counts,
		Expiry:     dcb.expiry,
	}
	err := dcb.setSharedState(ctx, state)
	return dcb, err
}

func (dcb *DistributedCircuitBreaker[T]) sharedStateKey() string {
	return "gobreaker:" + dcb.name
}

func (dcb *DistributedCircuitBreaker[T]) getSharedState(ctx context.Context) (SharedState, error) {
	var state SharedState
	if dcb.store == nil {
		return state, ErrNoSharedStore
	}

	data, err := dcb.store.GetData(ctx, dcb.sharedStateKey())
	if len(data) == 0 {
		return state, ErrNoSharedState
	} else if err != nil {
		return state, err
	}

	err = json.Unmarshal(data, &state)
	return state, err
}

func (dcb *DistributedCircuitBreaker[T]) setSharedState(ctx context.Context, state SharedState) error {
	if dcb.store == nil {
		return ErrNoSharedStore
	}

	data, err := json.Marshal(state)
	if err != nil {
		return err
	}

	return dcb.store.SetData(ctx, dcb.sharedStateKey(), data)
}

// State returns the State of DistributedCircuitBreaker.
func (dcb *DistributedCircuitBreaker[T]) State(ctx context.Context) (State, error) {
	state, err := dcb.getSharedState(ctx)
	if err != nil {
		return state.State, err
	}

	now := time.Now()
	currentState, _ := dcb.currentState(state, now)

	// update the state if it has changed
	if currentState != state.State {
		state.State = currentState
		if err := dcb.setSharedState(ctx, state); err != nil {
			return state.State, err
		}
	}

	return state.State, nil
}

// Execute runs the given request if the DistributedCircuitBreaker accepts it.
func (dcb *DistributedCircuitBreaker[T]) Execute(ctx context.Context, req func() (T, error)) (t T, err error) {
	generation, err := dcb.beforeRequest(ctx)
	if err != nil {
		var defaultValue T
		return defaultValue, err
	}

	defer func() {
		e := recover()
		if e != nil {
			ae := dcb.afterRequest(ctx, generation, false)
			if err == nil {
				err = ae
			}
			panic(e)
		}
	}()

	result, err := req()
	ae := dcb.afterRequest(ctx, generation, dcb.isSuccessful(err))
	if err == nil {
		err = ae
	}
	return result, err
}

func (dcb *DistributedCircuitBreaker[T]) beforeRequest(ctx context.Context) (uint64, error) {
	state, err := dcb.getSharedState(ctx)
	if err != nil {
		return 0, err
	}
	now := time.Now()
	currentState, generation := dcb.currentState(state, now)

	if currentState != state.State {
		dcb.setState(&state, currentState, now)
		err = dcb.setSharedState(ctx, state)
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
	err = dcb.setSharedState(ctx, state)
	if err != nil {
		return 0, err
	}

	return generation, nil
}

func (dcb *DistributedCircuitBreaker[T]) afterRequest(ctx context.Context, before uint64, success bool) error {
	state, err := dcb.getSharedState(ctx)
	if err != nil {
		return err
	}

	now := time.Now()
	currentState, generation := dcb.currentState(state, now)
	if generation != before {
		return nil
	}

	if success {
		dcb.onSuccess(&state, currentState, now)
	} else {
		dcb.onFailure(&state, currentState, now)
	}
	return dcb.setSharedState(ctx, state)
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
