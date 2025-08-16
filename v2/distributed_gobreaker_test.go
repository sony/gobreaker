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

var mockStore *MockStore

func setUpDCB() *DistributedCircuitBreaker[any] {
	mockStore = NewMockStore()

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

func tearDownDCB(dcb *DistributedCircuitBreaker[any]) {
	if dcb != nil {
		// For mock store, no cleanup needed
		// For Redis store, this would call store.Close()
	}
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
	defer tearDownDCB(dcb)

	assert.Equal(t, "TestBreaker", dcb.Name())
	assert.Equal(t, uint32(3), dcb.maxRequests)
	assert.Equal(t, time.Second, dcb.interval)
	assert.Equal(t, time.Second*2, dcb.timeout)
	assert.NotNil(t, dcb.readyToTrip)

	assertState(t, dcb, StateClosed)
}

func TestDistributedCircuitBreakerStateTransitions(t *testing.T) {
	dcb := setUpDCB()
	defer tearDownDCB(dcb)

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
	dcbPseudoSleep(dcb, dcb.timeout)
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
	defer tearDownDCB(dcb)

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
	defer tearDownDCB(dcb)

	for i := 0; i < 5; i++ {
		assert.Nil(t, successRequest(dcb))
	}

	state, err := dcb.getSharedState()
	assert.Equal(t, Counts{5, 5, 0, 5, 0}, state.Counts)
	assert.NoError(t, err)

	assert.Nil(t, failRequest(dcb))
	state, err = dcb.getSharedState()
	assert.Equal(t, Counts{6, 5, 1, 0, 1}, state.Counts)
	assert.NoError(t, err)
}

var customDCB *DistributedCircuitBreaker[any]

func TestCustomDistributedCircuitBreaker(t *testing.T) {
	mockStore = NewMockStore()

	var err error
	customDCB, err = NewDistributedCircuitBreaker[any](mockStore, Settings{
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
		assert.Equal(t, Counts{10, 5, 5, 0, 1}, state.Counts)

		// Perform one more successful request
		assert.NoError(t, successRequest(customDCB))
		state, err = customDCB.getSharedState()
		assert.NoError(t, err)
		assert.Equal(t, Counts{11, 6, 5, 1, 0}, state.Counts)

		// Simulate time passing to reset counts
		dcbPseudoSleep(customDCB, time.Second*30)

		// Perform requests to trigger StateOpen
		assert.NoError(t, successRequest(customDCB))
		assert.NoError(t, failRequest(customDCB))
		assert.NoError(t, failRequest(customDCB))

		// Check if the circuit breaker is now open
		assertState(t, customDCB, StateOpen)

		state, err = customDCB.getSharedState()
		assert.NoError(t, err)
		assert.Equal(t, Counts{0, 0, 0, 0, 0}, state.Counts)
	})

	t.Run("Timeout and Half-Open State", func(t *testing.T) {
		// Simulate timeout to transition to half-open state
		dcbPseudoSleep(customDCB, time.Second*90)
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

	mockStore = NewMockStore()

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
