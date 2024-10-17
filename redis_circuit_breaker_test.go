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

var defaultRCB *RedisCircuitBreaker
var customRCB *RedisCircuitBreaker

func setupTestWithMiniredis() (*RedisCircuitBreaker, *miniredis.Miniredis, *redis.Client) {
	mr, err := miniredis.Run()
	if err != nil {
		panic(err)
	}

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	return NewRedisCircuitBreaker(client, RedisSettings{
		Settings: Settings{
			Name:        "TestBreaker",
			MaxRequests: 3,
			Interval:    time.Second,
			Timeout:     time.Second * 2,
			ReadyToTrip: func(counts Counts) bool {
				return counts.ConsecutiveFailures > 5
			},
		},
	}), mr, client
}

func pseudoSleepRedis(rcb *RedisCircuitBreaker, period time.Duration) {
	ctx := context.Background()
	state, _ := rcb.getRedisState(ctx)

	state.Expiry = state.Expiry.Add(-period)
	// Reset counts if the interval has passed
	if time.Now().After(state.Expiry) {
		state.Counts = Counts{}
	}
	rcb.setRedisState(ctx, state)

}

func successRequest(rcb *RedisCircuitBreaker) error {
	_, err := rcb.Execute(func() (interface{}, error) { return nil, nil })
	return err
}

func failRequest(rcb *RedisCircuitBreaker) error {
	_, err := rcb.Execute(func() (interface{}, error) { return nil, errors.New("fail") })
	if err != nil && err.Error() == "fail" {
		return nil
	}
	return err
}

func TestRedisCircuitBreakerInitialization(t *testing.T) {
	rcb, mr, _ := setupTestWithMiniredis()
	defer mr.Close()

	assert.Equal(t, "TestBreaker", rcb.Name())
	assert.Equal(t, uint32(3), rcb.maxRequests)
	assert.Equal(t, time.Second, rcb.interval)
	assert.Equal(t, time.Second*2, rcb.timeout)
	assert.NotNil(t, rcb.readyToTrip)

	state := rcb.State()
	assert.Equal(t, StateClosed, state)
}

func TestRedisCircuitBreakerStateTransitions(t *testing.T) {
	rcb, mr, _ := setupTestWithMiniredis()
	defer mr.Close()

	// Check if initial state is closed
	assert.Equal(t, StateClosed, rcb.State())

	// StateClosed to StateOpen
	for i := 0; i < 6; i++ {
		assert.NoError(t, failRequest(rcb))
	}

	assert.Equal(t, StateOpen, rcb.State())

	// Ensure requests fail when circuit is open
	err := failRequest(rcb)
	assert.Error(t, err)
	assert.Equal(t, ErrOpenState, err)

	// Wait for timeout to transition to half-open
	pseudoSleepRedis(rcb, rcb.timeout)
	assert.Equal(t, StateHalfOpen, rcb.State())

	// StateHalfOpen to StateClosed
	for i := 0; i < int(rcb.maxRequests); i++ {
		assert.NoError(t, successRequest(rcb))
	}
	assert.Equal(t, StateClosed, rcb.State())

	// StateClosed to StateOpen (again)
	for i := 0; i < 6; i++ {
		assert.NoError(t, failRequest(rcb))
	}
	assert.Equal(t, StateOpen, rcb.State())
}

func TestRedisCircuitBreakerExecution(t *testing.T) {
	rcb, mr, _ := setupTestWithMiniredis()
	defer mr.Close()

	// Test successful execution
	result, err := rcb.Execute(func() (interface{}, error) {
		return "success", nil
	})
	assert.NoError(t, err)
	assert.Equal(t, "success", result)

	// Test failed execution
	_, err = rcb.Execute(func() (interface{}, error) {
		return nil, errors.New("test error")
	})
	assert.Error(t, err)
	assert.Equal(t, "test error", err.Error())
}

func TestRedisCircuitBreakerCounts(t *testing.T) {
	rcb, mr, _ := setupTestWithMiniredis()
	defer mr.Close()

	for i := 0; i < 5; i++ {
		assert.Nil(t, successRequest(rcb))
	}

	ctx := context.Background()
	state, _ := rcb.getRedisState(ctx)
	assert.Equal(t, Counts{5, 5, 0, 5, 0}, state.Counts)

	assert.Nil(t, failRequest(rcb))
	state, _ = rcb.getRedisState(ctx)
	assert.Equal(t, Counts{6, 5, 1, 0, 1}, state.Counts)
}

func TestRedisCircuitBreakerFallback(t *testing.T) {
	rcb, mr, _ := setupTestWithMiniredis()
	defer mr.Close()

	// Test when Redis is unavailable
	mr.Close() // Simulate Redis being unavailable

	rcb.redisClient = nil

	state := rcb.State()
	assert.Equal(t, StateClosed, state, "Should fallback to in-memory state when Redis is unavailable")

	// Ensure operations still work without Redis
	assert.Nil(t, successRequest(rcb))
	assert.Nil(t, failRequest(rcb))
}

func TestCustomRedisCircuitBreaker(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		panic(err)
	}
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	customRCB = NewRedisCircuitBreaker(client, RedisSettings{
		Settings: Settings{
			Name:        "CustomBreaker",
			MaxRequests: 3,
			Interval:    time.Second * 30,
			Timeout:     time.Second * 90,
			ReadyToTrip: func(counts Counts) bool {
				numReqs := counts.Requests
				failureRatio := float64(counts.TotalFailures) / float64(numReqs)
				return numReqs >= 3 && failureRatio >= 0.6
			},
		},
	})

	t.Run("Initialization", func(t *testing.T) {
		assert.Equal(t, "CustomBreaker", customRCB.Name())
		assert.Equal(t, StateClosed, customRCB.State())
	})

	t.Run("Counts and State Transitions", func(t *testing.T) {
		ctx := context.Background()

		// Perform 5 successful and 5 failed requests
		for i := 0; i < 5; i++ {
			assert.NoError(t, successRequest(customRCB))
			assert.NoError(t, failRequest(customRCB))
		}

		state, err := customRCB.getRedisState(ctx)
		assert.NoError(t, err)
		assert.Equal(t, StateClosed, state.State)
		assert.Equal(t, Counts{10, 5, 5, 0, 1}, state.Counts)

		// Perform one more successful request
		assert.NoError(t, successRequest(customRCB))
		state, err = customRCB.getRedisState(ctx)
		assert.NoError(t, err)
		assert.Equal(t, Counts{11, 6, 5, 1, 0}, state.Counts)

		// Simulate time passing to reset counts
		pseudoSleepRedis(customRCB, time.Second*30)

		// Perform requests to trigger StateOpen
		assert.NoError(t, successRequest(customRCB))
		assert.NoError(t, failRequest(customRCB))
		assert.NoError(t, failRequest(customRCB))

		// Check if the circuit breaker is now open
		assert.Equal(t, StateOpen, customRCB.State())

		state, err = customRCB.getRedisState(ctx)
		assert.NoError(t, err)
		assert.Equal(t, Counts{0, 0, 0, 0, 0}, state.Counts)
	})

	t.Run("Timeout and Half-Open State", func(t *testing.T) {
		// Simulate timeout to transition to half-open state
		pseudoSleepRedis(customRCB, time.Second*90)
		assert.Equal(t, StateHalfOpen, customRCB.State())

		// Successful requests in half-open state should close the circuit
		for i := 0; i < 3; i++ {
			assert.NoError(t, successRequest(customRCB))
		}
		assert.Equal(t, StateClosed, customRCB.State())
	})
}
