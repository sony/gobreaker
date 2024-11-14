package gobreaker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type CacheClient interface {
	GetState(ctx context.Context, key string) ([]byte, error)
	SetState(ctx context.Context, key string, value interface{}, expiration time.Duration) error
}

// DistributedCircuitBreaker extends CircuitBreaker with distributed state storage
type DistributedCircuitBreaker[T any] struct {
	*CircuitBreaker[T]
	cacheClient CacheClient
}

// StorageSettings extends Settings
type StorageSettings struct {
	Settings
}

// NewDistributedCircuitBreaker returns a new DistributedCircuitBreaker configured with the given StorageSettings
func NewDistributedCircuitBreaker[T any](storageClient CacheClient, settings StorageSettings) *DistributedCircuitBreaker[T] {
	cb := NewCircuitBreaker[T](settings.Settings)
	return &DistributedCircuitBreaker[T]{
		CircuitBreaker: cb,
		cacheClient:    storageClient,
	}
}

// StoredState represents the CircuitBreaker state stored in Distributed Storage
type StoredState struct {
	State      State     `json:"state"`
	Generation uint64    `json:"generation"`
	Counts     Counts    `json:"counts"`
	Expiry     time.Time `json:"expiry"`
}

func (rcb *DistributedCircuitBreaker[T]) State(ctx context.Context) State {
	if rcb.cacheClient == nil {
		return rcb.CircuitBreaker.State()
	}

	state, err := rcb.getStoredState(ctx)
	if err != nil {
		// Fallback to in-memory state if Storage fails
		return rcb.CircuitBreaker.State()
	}

	now := time.Now()
	currentState, _ := rcb.currentState(state, now)

	// Update the state in Storage if it has changed
	if currentState != state.State {
		state.State = currentState
		if err := rcb.setStoredState(ctx, state); err != nil {
			// Log the error, but continue with the current state
			fmt.Printf("Failed to update state in storage: %v\n", err)
		}
	}

	return state.State
}

// Execute runs the given request if the DistributedCircuitBreaker accepts it
func (rcb *DistributedCircuitBreaker[T]) Execute(ctx context.Context, req func() (T, error)) (T, error) {
	if rcb.cacheClient == nil {
		return rcb.CircuitBreaker.Execute(req)
	}
	generation, err := rcb.beforeRequest(ctx)
	if err != nil {
		var zero T
		return zero, err
	}

	defer func() {
		e := recover()
		if e != nil {
			rcb.afterRequest(ctx, generation, false)
			panic(e)
		}
	}()

	result, err := req()
	rcb.afterRequest(ctx, generation, rcb.isSuccessful(err))

	return result, err
}

func (rcb *DistributedCircuitBreaker[T]) beforeRequest(ctx context.Context) (uint64, error) {
	state, err := rcb.getStoredState(ctx)
	if err != nil {
		return 0, err
	}
	now := time.Now()
	currentState, generation := rcb.currentState(state, now)

	if currentState != state.State {
		rcb.setState(&state, currentState, now)
		err = rcb.setStoredState(ctx, state)
		if err != nil {
			return 0, err
		}
	}

	if currentState == StateOpen {
		return generation, ErrOpenState
	} else if currentState == StateHalfOpen && state.Counts.Requests >= rcb.maxRequests {
		return generation, ErrTooManyRequests
	}

	state.Counts.onRequest()
	err = rcb.setStoredState(ctx, state)
	if err != nil {
		return 0, err
	}

	return generation, nil
}

func (rcb *DistributedCircuitBreaker[T]) afterRequest(ctx context.Context, before uint64, success bool) {
	state, err := rcb.getStoredState(ctx)
	if err != nil {
		return
	}
	now := time.Now()
	currentState, generation := rcb.currentState(state, now)
	if generation != before {
		return
	}

	if success {
		rcb.onSuccess(&state, currentState, now)
	} else {
		rcb.onFailure(&state, currentState, now)
	}

	rcb.setStoredState(ctx, state)
}

func (rcb *DistributedCircuitBreaker[T]) onSuccess(state *StoredState, currentState State, now time.Time) {
	if state.State == StateOpen {
		state.State = currentState
	}

	switch currentState {
	case StateClosed:
		state.Counts.onSuccess()
	case StateHalfOpen:
		state.Counts.onSuccess()
		if state.Counts.ConsecutiveSuccesses >= rcb.maxRequests {
			rcb.setState(state, StateClosed, now)
		}
	}
}

func (rcb *DistributedCircuitBreaker[T]) onFailure(state *StoredState, currentState State, now time.Time) {
	switch currentState {
	case StateClosed:
		state.Counts.onFailure()
		if rcb.readyToTrip(state.Counts) {
			rcb.setState(state, StateOpen, now)
		}
	case StateHalfOpen:
		rcb.setState(state, StateOpen, now)
	}
}

func (rcb *DistributedCircuitBreaker[T]) currentState(state StoredState, now time.Time) (State, uint64) {
	switch state.State {
	case StateClosed:
		if !state.Expiry.IsZero() && state.Expiry.Before(now) {
			rcb.toNewGeneration(&state, now)
		}
	case StateOpen:
		if state.Expiry.Before(now) {
			rcb.setState(&state, StateHalfOpen, now)
		}
	}
	return state.State, state.Generation
}

func (rcb *DistributedCircuitBreaker[T]) setState(state *StoredState, newState State, now time.Time) {
	if state.State == newState {
		return
	}

	prev := state.State
	state.State = newState

	rcb.toNewGeneration(state, now)

	if rcb.onStateChange != nil {
		rcb.onStateChange(rcb.name, prev, newState)
	}
}

func (rcb *DistributedCircuitBreaker[T]) toNewGeneration(state *StoredState, now time.Time) {

	state.Generation++
	state.Counts.clear()

	var zero time.Time
	switch state.State {
	case StateClosed:
		if rcb.interval == 0 {
			state.Expiry = zero
		} else {
			state.Expiry = now.Add(rcb.interval)
		}
	case StateOpen:
		state.Expiry = now.Add(rcb.timeout)
	default: // StateHalfOpen
		state.Expiry = zero
	}
}

func (rcb *DistributedCircuitBreaker[T]) getStorageKey() string {
	return "cb:" + rcb.name
}

func (rcb *DistributedCircuitBreaker[T]) getStoredState(ctx context.Context) (StoredState, error) {
	var state StoredState
	data, err := rcb.cacheClient.GetState(ctx, rcb.getStorageKey())
	if len(data) == 0 {
		// Key doesn't exist, return default state
		return StoredState{State: StateClosed}, nil
	} else if err != nil {
		return state, err
	}

	err = json.Unmarshal(data, &state)
	return state, err
}

func (rcb *DistributedCircuitBreaker[T]) setStoredState(ctx context.Context, state StoredState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}

	return rcb.cacheClient.SetState(ctx, rcb.getStorageKey(), data, 0)
}
