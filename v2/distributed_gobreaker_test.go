package gobreaker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

var customDCB *DistributedCircuitBreaker[any]

type storeAdapter struct {
	client *redis.Client
}

func (r *storeAdapter) GetData(ctx context.Context, key string) ([]byte, error) {
	return r.client.Get(ctx, key).Bytes()
}

func (r *storeAdapter) SetData(ctx context.Context, key string, value []byte) error {
	return r.client.Set(ctx, key, value, 0).Err()
}

func setupTestWithMiniredis(ctx context.Context) (*DistributedCircuitBreaker[any], *miniredis.Miniredis, *redis.Client) {
	mr, err := miniredis.Run()
	if err != nil {
		panic(err)
	}

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	store := &storeAdapter{client: client}

	dcb, err := NewDistributedCircuitBreaker[any](ctx, store, Settings{
		Name:        "TestBreaker",
		MaxRequests: 3,
		Interval:    time.Second,
		Timeout:     time.Second * 2,
		ReadyToTrip: func(counts Counts) bool {
			return counts.ConsecutiveFailures > 5
		},
	})
	if err != nil {
		panic(err)
	}

	return dcb, mr, client
}

func pseudoSleepStorage(ctx context.Context, dcb *DistributedCircuitBreaker[any], period time.Duration) {
	state, err := dcb.getSharedState(ctx)
	if err != nil {
		panic(err)
	}

	state.Expiry = state.Expiry.Add(-period)
	// Reset counts if the interval has passed
	if time.Now().After(state.Expiry) {
		state.Counts = Counts{}
	}
	if err := dcb.setSharedState(ctx, state); err != nil {
		panic(err)
	}
}

func successRequest(ctx context.Context, dcb *DistributedCircuitBreaker[any]) error {
	_, err := dcb.Execute(ctx, func() (interface{}, error) { return nil, nil })
	return err
}

func failRequest(ctx context.Context, dcb *DistributedCircuitBreaker[any]) error {
	_, err := dcb.Execute(ctx, func() (interface{}, error) { return nil, errors.New("fail") })
	if err != nil && err.Error() == "fail" {
		return nil
	}
	return err
}

func assertState(ctx context.Context, t *testing.T, dcb *DistributedCircuitBreaker[any], expected State) {
	state, err := dcb.State(ctx)
	assert.Equal(t, expected, state)
	assert.NoError(t, err)
}

func TestDistributedCircuitBreakerInitialization(t *testing.T) {
	ctx := context.Background()
	dcb, mr, _ := setupTestWithMiniredis(ctx)
	defer mr.Close()

	assert.Equal(t, "TestBreaker", dcb.Name())
	assert.Equal(t, uint32(3), dcb.maxRequests)
	assert.Equal(t, time.Second, dcb.interval)
	assert.Equal(t, time.Second*2, dcb.timeout)
	assert.NotNil(t, dcb.readyToTrip)

	assertState(ctx, t, dcb, StateClosed)
}

func TestDistributedCircuitBreakerStateTransitions(t *testing.T) {
	ctx := context.Background()
	dcb, mr, _ := setupTestWithMiniredis(ctx)
	defer mr.Close()

	// Check if initial state is closed
	assertState(ctx, t, dcb, StateClosed)

	// StateClosed to StateOpen
	for i := 0; i < 6; i++ {
		assert.NoError(t, failRequest(ctx, dcb))
	}

	assertState(ctx, t, dcb, StateOpen)

	// Ensure requests fail when circuit is open
	err := failRequest(ctx, dcb)
	assert.Error(t, err)
	assert.Equal(t, ErrOpenState, err)

	// Wait for timeout to transition to half-open
	pseudoSleepStorage(ctx, dcb, dcb.timeout)
	assertState(ctx, t, dcb, StateHalfOpen)

	// StateHalfOpen to StateClosed
	for i := 0; i < int(dcb.maxRequests); i++ {
		assert.NoError(t, successRequest(ctx, dcb))
	}
	assertState(ctx, t, dcb, StateClosed)

	// StateClosed to StateOpen (again)
	for i := 0; i < 6; i++ {
		assert.NoError(t, failRequest(ctx, dcb))
	}
	assertState(ctx, t, dcb, StateOpen)
}

func TestDistributedCircuitBreakerExecution(t *testing.T) {
	ctx := context.Background()
	dcb, mr, _ := setupTestWithMiniredis(ctx)
	defer mr.Close()

	// Test successful execution
	result, err := dcb.Execute(ctx, func() (interface{}, error) {
		return "success", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "success", result)

	// Test failed execution
	_, err = dcb.Execute(ctx, func() (interface{}, error) {
		return nil, errors.New("test error")
	})
	assert.Error(t, err)
	assert.Equal(t, "test error", err.Error())
}

func TestDistributedCircuitBreakerCounts(t *testing.T) {
	ctx := context.Background()
	dcb, mr, _ := setupTestWithMiniredis(ctx)
	defer mr.Close()

	for i := 0; i < 5; i++ {
		assert.Nil(t, successRequest(ctx, dcb))
	}

	state, _ := dcb.getSharedState(ctx)
	assert.Equal(t, Counts{5, 5, 0, 5, 0}, state.Counts)

	assert.Nil(t, failRequest(ctx, dcb))
	state, _ = dcb.getSharedState(ctx)
	assert.Equal(t, Counts{6, 5, 1, 0, 1}, state.Counts)
}

func TestDistributedCircuitBreakerFallback(t *testing.T) {
	ctx := context.Background()
	dcb, mr, _ := setupTestWithMiniredis(ctx)
	defer mr.Close()

	// Test when Storage is unavailable
	mr.Close() // Simulate Storage being unavailable

	dcb.store = nil

	assertState(ctx, t, dcb, StateClosed)

	// Ensure operations still work without Storage
	assert.Nil(t, successRequest(ctx, dcb))
	assert.Nil(t, failRequest(ctx, dcb))
}

func TestCustomDistributedCircuitBreaker(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		panic(err)
	}
	defer mr.Close()

	ctx := context.Background()
	
	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	store := &storeAdapter{client: client}

	customDCB, err = NewDistributedCircuitBreaker[any](ctx, store, Settings{
		Name:        "CustomBreaker",
		MaxRequests: 3,
		Interval:    time.Second * 30,
		Timeout:     time.Second * 90,
		ReadyToTrip: func(counts Counts) bool {
			numReqs := counts.Requests
			failureRatio := float64(counts.TotalFailures) / float64(numReqs)
			return numReqs >= 3 && failureRatio >= 0.6
		},
	})
	assert.NoError(t, err)

	t.Run("Initialization", func(t *testing.T) {
		assert.Equal(t, "CustomBreaker", customDCB.Name())
		assertState(ctx, t, customDCB, StateClosed)
	})

	t.Run("Counts and State Transitions", func(t *testing.T) {
		// Perform 5 successful and 5 failed requests
		for i := 0; i < 5; i++ {
			assert.NoError(t, successRequest(ctx, customDCB))
			assert.NoError(t, failRequest(ctx, customDCB))
		}

		state, err := customDCB.getSharedState(ctx)
		assert.NoError(t, err)
		assert.Equal(t, StateClosed, state.State)
		assert.Equal(t, Counts{10, 5, 5, 0, 1}, state.Counts)

		// Perform one more successful request
		assert.NoError(t, successRequest(ctx, customDCB))
		state, err = customDCB.getSharedState(ctx)
		assert.NoError(t, err)
		assert.Equal(t, Counts{11, 6, 5, 1, 0}, state.Counts)

		// Simulate time passing to reset counts
		pseudoSleepStorage(ctx, customDCB, time.Second*30)

		// Perform requests to trigger StateOpen
		assert.NoError(t, successRequest(ctx, customDCB))
		assert.NoError(t, failRequest(ctx, customDCB))
		assert.NoError(t, failRequest(ctx, customDCB))

		// Check if the circuit breaker is now open
		assertState(ctx, t, customDCB, StateOpen)

		state, err = customDCB.getSharedState(ctx)
		assert.NoError(t, err)
		assert.Equal(t, Counts{0, 0, 0, 0, 0}, state.Counts)
	})

	t.Run("Timeout and Half-Open State", func(t *testing.T) {
		// Simulate timeout to transition to half-open state
		pseudoSleepStorage(ctx, customDCB, time.Second*90)
		assertState(ctx, t, customDCB, StateHalfOpen)

		// Successful requests in half-open state should close the circuit
		for i := 0; i < 3; i++ {
			assert.NoError(t, successRequest(ctx, customDCB))
		}
		assertState(ctx, t, customDCB, StateClosed)
	})
}

func TestCustomDistributedCircuitBreakerStateTransitions(t *testing.T) {
	// Setup
	var stateChange StateChange
	customSt := Settings{
		Name:        "cb",
		MaxRequests: 3,
		Interval:    5 * time.Second,
		Timeout:     5 * time.Second,
		ReadyToTrip: func(counts Counts) bool {
			return counts.ConsecutiveFailures >= 2
		},
		OnStateChange: func(name string, from State, to State) {
			stateChange = StateChange{name, from, to}
		},
	}

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("Failed to start miniredis: %v", err)
	}
	defer mr.Close()

	ctx := context.Background()
	
	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	store := &storeAdapter{client: client}

	dcb, err := NewDistributedCircuitBreaker[any](ctx, store, customSt)
	assert.NoError(t, err)

	// Test case
	t.Run("Circuit Breaker State Transitions", func(t *testing.T) {
		// Initial state should be Closed
		assertState(ctx, t, dcb, StateClosed)

		// Cause two consecutive failures to trip the circuit
		for i := 0; i < 2; i++ {
			err := failRequest(ctx, dcb)
			assert.NoError(t, err, "Fail request should not return an error")
		}

		// Circuit should now be Open
		assertState(ctx, t, dcb, StateOpen)
		assert.Equal(t, StateChange{"cb", StateClosed, StateOpen}, stateChange)

		// Requests should fail immediately when circuit is Open
		err := successRequest(ctx, dcb)
		assert.Error(t, err)
		assert.Equal(t, ErrOpenState, err)

		// Simulate timeout to transition to Half-Open
		pseudoSleepStorage(ctx, dcb, 6*time.Second)
		assertState(ctx, t, dcb, StateHalfOpen)
		assert.Equal(t, StateChange{"cb", StateOpen, StateHalfOpen}, stateChange)

		// Successful requests in Half-Open state should close the circuit
		for i := 0; i < int(dcb.maxRequests); i++ {
			err := successRequest(ctx, dcb)
			assert.NoError(t, err)
		}

		// Circuit should now be Closed
		assertState(ctx, t, dcb, StateClosed)
		assert.Equal(t, StateChange{"cb", StateHalfOpen, StateClosed}, stateChange)
	})
}
