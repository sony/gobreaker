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

	_, err := dcb.getSharedState(ctx)
	if err == ErrNoSharedState {
		state := SharedState{
			State:      dcb.state,
			Generation: dcb.generation,
			Counts:     dcb.counts,
			Expiry:     dcb.expiry,
		}
		err = dcb.setSharedState(ctx, state)
	}
	if err != nil {
		return nil, err
	}

	return dcb, nil
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

func (dcb *DistributedCircuitBreaker[T]) inject(shared SharedState) {
	dcb.mutex.Lock()
	defer dcb.mutex.Unlock()

	dcb.state = shared.State
	dcb.generation = shared.Generation
	dcb.counts = shared.Counts
	dcb.expiry = shared.Expiry
}

func (dcb *DistributedCircuitBreaker[T]) extract() SharedState {
	dcb.mutex.Lock()
	defer dcb.mutex.Unlock()

	return SharedState{
		State:      dcb.state,
		Generation: dcb.generation,
		Counts:     dcb.counts,
		Expiry:     dcb.expiry,
	}
}

// State returns the State of DistributedCircuitBreaker.
func (dcb *DistributedCircuitBreaker[T]) State(ctx context.Context) (State, error) {
	shared, err := dcb.getSharedState(ctx)
	if err != nil {
		return shared.State, err
	}

	dcb.inject(shared)
	state := dcb.CircuitBreaker.State()
	shared = dcb.extract()

	err = dcb.setSharedState(ctx, shared)
	return state, err
}

// Execute runs the given request if the DistributedCircuitBreaker accepts it.
func (dcb *DistributedCircuitBreaker[T]) Execute(ctx context.Context, req func() (T, error)) (T, error) {
	shared, err := dcb.getSharedState(ctx)
	if err != nil {
		var defaultValue T
		return defaultValue, err
	}

	dcb.inject(shared)
	t, e := dcb.CircuitBreaker.Execute(req)
	shared = dcb.extract()

	err = dcb.setSharedState(ctx, shared)
	if err != nil {
		var defaultValue T
		return defaultValue, err
	}
	
	return t, e
}
