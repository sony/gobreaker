package gobreaker

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// MockStore implements SharedDataStore interface for testing
type MockStore struct {
	data map[string][]byte
	mu   sync.RWMutex
}

func NewMockStore() *MockStore {
	return &MockStore{
		data: make(map[string][]byte),
	}
}

func (m *MockStore) Lock(name string) error {
	// Mock implementation - no actual locking needed for tests
	return nil
}

func (m *MockStore) Unlock(name string) error {
	// Mock implementation - no actual unlocking needed for tests
	return nil
}

func (m *MockStore) GetData(name string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	data, exists := m.data[name]
	if !exists {
		return nil, nil
	}
	return data, nil
}

func (m *MockStore) SetData(name string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.data[name] = data
	return nil
}

func (m *MockStore) Close() {
	// Mock implementation - no cleanup needed
}

func setUpDCB() *DistributedCircuitBreaker[any] {
	mockStore := NewMockStore()
	dcb, err := NewDistributedCircuitBreaker[any](mockStore, Settings{
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
	return dcb
}

func dcbPseudoSleep(dcb *DistributedCircuitBreaker[any], period time.Duration) {
	state, err := dcb.getSharedState()
	if err != nil {
		panic(err)
	}

	state.Expiry = state.Expiry.Add(-period)
	// Reset counts if the interval has passed
	if time.Now().After(state.Expiry) {
		state.Counts.clear()
		for i := range state.Buckets {
			state.Buckets[i].clear()
		}
	}

	err = dcb.setSharedState(state)
	if err != nil {
		panic(err)
	}
}

func successRequest(dcb *DistributedCircuitBreaker[any]) error {
	_, err := dcb.Execute(func() (interface{}, error) { return nil, nil })
	return err
}

func failRequest(dcb *DistributedCircuitBreaker[any]) error {
	_, err := dcb.Execute(func() (interface{}, error) { return nil, errors.New("fail") })
	if err != nil && err.Error() == "fail" {
		return nil
	}
	return err
}

func assertState(t *testing.T, dcb *DistributedCircuitBreaker[any], expected State) {
	state, err := dcb.State()
	assert.Equal(t, expected, state)
	assert.NoError(t, err)
}

func TestDistributedCircuitBreakerInitialization(t *testing.T) {
	dcb := setUpDCB()

	assert.Equal(t, "TestBreaker", dcb.Name())
	assert.Equal(t, uint32(3), dcb.maxRequests)
	assert.Equal(t, time.Second, dcb.interval)
	assert.Equal(t, time.Second*2, dcb.timeout)
	assert.NotNil(t, dcb.readyToTrip)

	assertState(t, dcb, StateClosed)
}

func TestDistributedCircuitBreakerStateTransitions(t *testing.T) {
	dcb := setUpDCB()

	// Check if initial state is closed
	assertState(t, dcb, StateClosed)

	// StateClosed to StateOpen
	for i := 0; i < 6; i++ {
		assert.NoError(t, failRequest(dcb))
	}
	assertState(t, dcb, StateOpen)

	// Ensure requests fail when the circuit is open
	err := failRequest(dcb)
	assert.Equal(t, ErrOpenState, err)

	// Wait for timeout so that the state will move to half-open
	dcbPseudoSleep(dcb, dcb.timeout+time.Second)
	assertState(t, dcb, StateHalfOpen)

	// StateHalfOpen to StateClosed
	for i := 0; i < int(dcb.maxRequests); i++ {
		assert.NoError(t, successRequest(dcb))
	}
	assertState(t, dcb, StateClosed)

	// StateClosed to StateOpen (again)
	for i := 0; i < 6; i++ {
		assert.NoError(t, failRequest(dcb))
	}
	assertState(t, dcb, StateOpen)
}

func TestDistributedCircuitBreakerExecution(t *testing.T) {
	dcb := setUpDCB()

	// Test successful execution
	result, err := dcb.Execute(func() (interface{}, error) {
		return "success", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "success", result)

	// Test failed execution
	_, err = dcb.Execute(func() (interface{}, error) {
		return nil, errors.New("test error")
	})
	assert.Error(t, err)
	assert.Equal(t, "test error", err.Error())
}

func TestDistributedCircuitBreakerCounts(t *testing.T) {
	dcb := setUpDCB()

	for i := 0; i < 5; i++ {
		assert.Nil(t, successRequest(dcb))
	}

	state, err := dcb.getSharedState()
	assert.Equal(t, Counts{Requests: 5, TotalSuccesses: 5, ConsecutiveSuccesses: 5}, state.Counts)
	assert.NoError(t, err)

	assert.Nil(t, failRequest(dcb))
	state, err = dcb.getSharedState()
	assert.Equal(t, Counts{Requests: 6, TotalSuccesses: 5, TotalFailures: 1, ConsecutiveFailures: 1}, state.Counts)
	assert.NoError(t, err)
}

func TestCustomDistributedCircuitBreaker(t *testing.T) {
	mockStore := NewMockStore()
	customDCB, err := NewDistributedCircuitBreaker[any](mockStore, Settings{
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
		assertState(t, customDCB, StateClosed)
	})

	t.Run("Counts and State Transitions", func(t *testing.T) {
		// Perform 5 successful and 5 failed requests
		for i := 0; i < 5; i++ {
			assert.NoError(t, successRequest(customDCB))
			assert.NoError(t, failRequest(customDCB))
		}

		state, err := customDCB.getSharedState()
		assert.NoError(t, err)
		assert.Equal(t, StateClosed, state.State)
		assert.Equal(t, Counts{Requests: 10, TotalSuccesses: 5, TotalFailures: 5, ConsecutiveFailures: 1}, state.Counts)

		// Perform one more successful request
		assert.NoError(t, successRequest(customDCB))
		state, err = customDCB.getSharedState()
		assert.NoError(t, err)
		assert.Equal(t, Counts{Requests: 11, TotalSuccesses: 6, TotalFailures: 5, ConsecutiveSuccesses: 1}, state.Counts)

		// Simulate time passing to reset counts
		dcbPseudoSleep(customDCB, time.Second*31)

		// Perform requests to trigger StateOpen
		assert.NoError(t, successRequest(customDCB))
		assert.NoError(t, failRequest(customDCB))
		assert.NoError(t, failRequest(customDCB))

		// Check if the circuit breaker is now open
		assertState(t, customDCB, StateOpen)

		state, err = customDCB.getSharedState()
		assert.NoError(t, err)
		assert.Equal(t, Counts{}, state.Counts)
	})

	t.Run("Timeout and Half-Open State", func(t *testing.T) {
		// Simulate timeout to transition to half-open state
		dcbPseudoSleep(customDCB, time.Second*91)
		assertState(t, customDCB, StateHalfOpen)

		// Successful requests in half-open state should close the circuit
		for i := 0; i < 3; i++ {
			assert.NoError(t, successRequest(customDCB))
		}
		assertState(t, customDCB, StateClosed)
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

	mockStore := NewMockStore()
	dcb, err := NewDistributedCircuitBreaker[any](mockStore, customSt)
	assert.NoError(t, err)

	// Test case
	t.Run("Circuit Breaker State Transitions", func(t *testing.T) {
		// Initial state should be Closed
		assertState(t, dcb, StateClosed)

		// Cause two consecutive failures to trip the circuit
		for i := 0; i < 2; i++ {
			err := failRequest(dcb)
			assert.NoError(t, err, "Fail request should not return an error")
		}

		// Circuit should now be Open
		assertState(t, dcb, StateOpen)
		assert.Equal(t, StateChange{"cb", StateClosed, StateOpen}, stateChange)

		// Requests should fail immediately when circuit is Open
		err := successRequest(dcb)
		assert.Error(t, err)
		assert.Equal(t, ErrOpenState, err)

		// Simulate timeout to transition to Half-Open
		dcbPseudoSleep(dcb, 6*time.Second)
		assertState(t, dcb, StateHalfOpen)
		assert.Equal(t, StateChange{"cb", StateOpen, StateHalfOpen}, stateChange)

		// Successful requests in Half-Open state should close the circuit
		for i := 0; i < int(dcb.maxRequests); i++ {
			err := successRequest(dcb)
			assert.NoError(t, err)
		}

		// Circuit should now be Closed
		assertState(t, dcb, StateClosed)
		assert.Equal(t, StateChange{"cb", StateHalfOpen, StateClosed}, stateChange)
	})
}

func TestDistributedCircuitBreakerTimeSynchronization(t *testing.T) {
	// Test that different instances with different start times use synchronized bucket indexing
	mockStore := NewMockStore()

	// Create first instance
	dcb1, err := NewDistributedCircuitBreaker[any](mockStore, Settings{
		Name:         "TimeSyncTest",
		MaxRequests:  3,
		Interval:     10 * time.Second,
		BucketPeriod: 2 * time.Second, // 5 buckets
		Timeout:      5 * time.Second,
		ReadyToTrip: func(counts Counts) bool {
			return counts.ConsecutiveFailures >= 2
		},
	})
	assert.NoError(t, err)

	// Get the shared start time from first instance
	sharedState1, err := dcb1.getSharedState()
	assert.NoError(t, err)
	originalStart := sharedState1.Start

	// Simulate some time passing and make a request
	time.Sleep(100 * time.Millisecond)
	_, err = dcb1.Execute(func() (interface{}, error) {
		return "success", nil
	})
	assert.NoError(t, err)

	// Create second instance (simulating different start time)
	dcb2, err := NewDistributedCircuitBreaker[any](mockStore, Settings{
		Name:         "TimeSyncTest",
		MaxRequests:  3,
		Interval:     10 * time.Second,
		BucketPeriod: 2 * time.Second,
		Timeout:      5 * time.Second,
		ReadyToTrip: func(counts Counts) bool {
			return counts.ConsecutiveFailures >= 2
		},
	})
	assert.NoError(t, err)

	// Verify that both instances have the same start time
	sharedState2, err := dcb2.getSharedState()
	assert.NoError(t, err)
	assert.Equal(t, originalStart, sharedState2.Start)

	// Make a request from the second instance
	_, err = dcb2.Execute(func() (interface{}, error) {
		return "success", nil
	})
	assert.NoError(t, err)

	// Verify that both instances have the same counts and age
	state1, err := dcb1.getSharedState()
	assert.NoError(t, err)
	state2, err := dcb2.getSharedState()
	assert.NoError(t, err)

	// Both instances should have the same age and counts
	assert.Equal(t, state1.Age, state2.Age, "Both instances should have the same age")
	assert.Equal(t, state1.Counts, state2.Counts, "Both instances should have the same counts")
	assert.Equal(t, state1.Start, state2.Start, "Both instances should have the same start time")

	// Verify that the age calculation is consistent
	// The age should be based on the shared start time, not individual instance start times
	now := time.Now()
	expectedAge := uint64(now.Sub(originalStart) / (2 * time.Second))

	// Allow for small time differences due to test execution
	var minAge uint64
	if expectedAge > 0 {
		minAge = expectedAge - 1
	}
	assert.True(t, state1.Age >= minAge && state1.Age <= expectedAge+1,
		"Age should be calculated from shared start time, got %d, expected around %d", state1.Age, expectedAge)
}

func TestDistributedCircuitBreakerBucketIndexingConsistency(t *testing.T) {
	// Test that bucket indexing is consistent across instances with different local start times
	mockStore := NewMockStore()

	// Create first instance
	dcb1, err := NewDistributedCircuitBreaker[any](mockStore, Settings{
		Name:         "BucketTest",
		MaxRequests:  3,
		Interval:     6 * time.Second,
		BucketPeriod: 2 * time.Second, // 3 buckets
		Timeout:      5 * time.Second,
		ReadyToTrip: func(counts Counts) bool {
			return counts.ConsecutiveFailures >= 2
		},
	})
	assert.NoError(t, err)

	// Make a request to establish the shared state
	_, err = dcb1.Execute(func() (interface{}, error) {
		return "success", nil
	})
	assert.NoError(t, err)

	// Get the shared state
	sharedState, err := dcb1.getSharedState()
	assert.NoError(t, err)
	sharedStart := sharedState.Start

	// Wait a bit to ensure we're in a different bucket
	time.Sleep(2 * time.Second)

	// Create second instance with different local start time
	dcb2, err := NewDistributedCircuitBreaker[any](mockStore, Settings{
		Name:         "BucketTest",
		MaxRequests:  3,
		Interval:     6 * time.Second,
		BucketPeriod: 2 * time.Second,
		Timeout:      5 * time.Second,
		ReadyToTrip: func(counts Counts) bool {
			return counts.ConsecutiveFailures >= 2
		},
	})
	assert.NoError(t, err)

	// Make requests from both instances
	_, err = dcb1.Execute(func() (interface{}, error) {
		return "success", nil
	})
	assert.NoError(t, err)

	_, err = dcb2.Execute(func() (interface{}, error) {
		return "success", nil
	})
	assert.NoError(t, err)

	// Verify that both instances have consistent bucket indexing
	state1, err := dcb1.getSharedState()
	assert.NoError(t, err)
	state2, err := dcb2.getSharedState()
	assert.NoError(t, err)

	// Both should have the same age and start time
	assert.Equal(t, state1.Age, state2.Age, "Bucket ages should be consistent")
	assert.Equal(t, state1.Start, state2.Start, "Start times should be synchronized")
	assert.Equal(t, state1.Counts, state2.Counts, "Counts should be consistent")

	// Verify that the age is calculated from the shared start time
	now := time.Now()
	expectedAge := uint64(now.Sub(sharedStart) / (2 * time.Second))
	assert.True(t, state1.Age >= expectedAge-1 && state1.Age <= expectedAge+1,
		"Age should be calculated from shared start time")
}
