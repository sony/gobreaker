package gobreaker

import (
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var defaultCB *CircuitBreaker
var customCB *CircuitBreaker

type StateChange struct {
	name string
	from State
	to   State
}

var stateChange StateChange

func pseudoSleep(cb *CircuitBreaker, period time.Duration) {
	if !cb.expiry.IsZero() {
		cb.expiry = cb.expiry.Add(-period)
	}
}

func succeed(cb *CircuitBreaker) error {
	_, err := cb.Execute(func() (interface{}, error) { return nil, nil })
	return err
}

func succeedLater(cb *CircuitBreaker, delay time.Duration) <-chan error {
	ch := make(chan error)
	go func() {
		_, err := cb.Execute(func() (interface{}, error) {
			time.Sleep(delay)
			return nil, nil
		})
		ch <- err
	}()
	return ch
}

func fail(cb *CircuitBreaker) error {
	msg := "fail"
	_, err := cb.Execute(func() (interface{}, error) { return nil, fmt.Errorf(msg) })
	if err.Error() == msg {
		return nil
	}
	return err
}

func causePanic(cb *CircuitBreaker) error {
	_, err := cb.Execute(func() (interface{}, error) { panic("oops"); return nil, nil })
	return err
}

func TestStateConstants(t *testing.T) {
	assert.Equal(t, State(0), StateClosed)
	assert.Equal(t, State(1), StateHalfOpen)
	assert.Equal(t, State(2), StateOpen)

	assert.Equal(t, StateClosed.String(), "closed")
	assert.Equal(t, StateHalfOpen.String(), "half-open")
	assert.Equal(t, StateOpen.String(), "open")
	assert.Equal(t, State(100).String(), "unknown state: 100")
}

func TestNewCircuitBreaker(t *testing.T) {
	var defaultSt Settings
	defaultCB = NewCircuitBreaker(defaultSt)
	assert.Equal(t, "", defaultCB.name)
	assert.Equal(t, uint32(1), defaultCB.maxRequests)
	assert.Equal(t, time.Duration(0), defaultCB.interval)
	assert.Equal(t, time.Duration(60)*time.Second, defaultCB.timeout)
	assert.NotNil(t, defaultCB.readyToTrip)
	assert.Nil(t, defaultCB.onStateChange)
	assert.Equal(t, StateClosed, defaultCB.state)
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, defaultCB.counts)
	assert.True(t, defaultCB.expiry.IsZero())

	var customSt Settings
	customSt.Name = "cb"
	customSt.MaxRequests = 3
	customSt.Interval = time.Duration(30) * time.Second
	customSt.Timeout = time.Duration(90) * time.Second
	customSt.ReadyToTrip = func(counts Counts) bool {
		numReqs := counts.Requests
		failureRatio := float64(counts.TotalFailures) / float64(numReqs)

		counts.clear() // no effect on customCB.counts

		return numReqs >= 3 && failureRatio >= 0.6
	}
	customSt.OnStateChange = func(name string, from State, to State) {
		stateChange = StateChange{name, from, to}
	}
	customCB = NewCircuitBreaker(customSt)
	assert.Equal(t, "cb", customCB.name)
	assert.Equal(t, uint32(3), customCB.maxRequests)
	assert.Equal(t, time.Duration(30)*time.Second, customCB.interval)
	assert.Equal(t, time.Duration(90)*time.Second, customCB.timeout)
	assert.NotNil(t, customCB.readyToTrip)
	assert.NotNil(t, customCB.onStateChange)
	assert.Equal(t, StateClosed, customCB.state)
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, customCB.counts)
	assert.False(t, customCB.expiry.IsZero())
}

func TestDefaultCircuitBreaker(t *testing.T) {
	for i := 0; i < 5; i++ {
		assert.Nil(t, fail(defaultCB))
	}
	assert.Equal(t, StateClosed, defaultCB.State())
	assert.Equal(t, Counts{5, 0, 5, 0, 5}, defaultCB.counts)

	assert.Nil(t, succeed(defaultCB))
	assert.Equal(t, StateClosed, defaultCB.State())
	assert.Equal(t, Counts{6, 1, 5, 1, 0}, defaultCB.counts)

	assert.Nil(t, fail(defaultCB))
	assert.Equal(t, StateClosed, defaultCB.State())
	assert.Equal(t, Counts{7, 1, 6, 0, 1}, defaultCB.counts)

	// StateClosed to StateOpen
	for i := 0; i < 5; i++ {
		assert.Nil(t, fail(defaultCB)) // 6 consecutive failures
	}
	assert.Equal(t, StateOpen, defaultCB.State())
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, defaultCB.counts)
	assert.False(t, defaultCB.expiry.IsZero())

	assert.Error(t, succeed(defaultCB))
	assert.Error(t, fail(defaultCB))
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, defaultCB.counts)

	pseudoSleep(defaultCB, time.Duration(59)*time.Second)
	assert.Equal(t, StateOpen, defaultCB.State())

	// StateOpen to StateHalfOpen
	pseudoSleep(defaultCB, time.Duration(1)*time.Second) // over Timeout
	assert.Equal(t, StateHalfOpen, defaultCB.State())
	assert.True(t, defaultCB.expiry.IsZero())

	// StateHalfOpen to StateOpen
	assert.Nil(t, fail(defaultCB))
	assert.Equal(t, StateOpen, defaultCB.State())
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, defaultCB.counts)
	assert.False(t, defaultCB.expiry.IsZero())

	// StateOpen to StateHalfOpen
	pseudoSleep(defaultCB, time.Duration(60)*time.Second)
	assert.Equal(t, StateHalfOpen, defaultCB.State())
	assert.True(t, defaultCB.expiry.IsZero())

	// StateHalfOpen to StateClosed
	assert.Nil(t, succeed(defaultCB))
	assert.Equal(t, StateClosed, defaultCB.State())
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, defaultCB.counts)
	assert.True(t, defaultCB.expiry.IsZero())
}

func TestCustomCircuitBreaker(t *testing.T) {
	for i := 0; i < 5; i++ {
		assert.Nil(t, succeed(customCB))
		assert.Nil(t, fail(customCB))
	}
	assert.Equal(t, StateClosed, customCB.State())
	assert.Equal(t, Counts{10, 5, 5, 0, 1}, customCB.counts)

	pseudoSleep(customCB, time.Duration(29)*time.Second)
	assert.Nil(t, succeed(customCB))
	assert.Equal(t, StateClosed, customCB.State())
	assert.Equal(t, Counts{11, 6, 5, 1, 0}, customCB.counts)

	pseudoSleep(customCB, time.Duration(1)*time.Second) // over Interval
	assert.Nil(t, fail(customCB))
	assert.Equal(t, StateClosed, customCB.State())
	assert.Equal(t, Counts{1, 0, 1, 0, 1}, customCB.counts)

	// StateClosed to StateOpen
	assert.Nil(t, succeed(customCB))
	assert.Nil(t, fail(customCB)) // failure ratio: 2/3 >= 0.6
	assert.Equal(t, StateOpen, customCB.State())
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, customCB.counts)
	assert.False(t, customCB.expiry.IsZero())
	assert.Equal(t, StateChange{"cb", StateClosed, StateOpen}, stateChange)

	// StateOpen to StateHalfOpen
	pseudoSleep(customCB, time.Duration(90)*time.Second)
	assert.Equal(t, StateHalfOpen, customCB.State())
	assert.True(t, defaultCB.expiry.IsZero())
	assert.Equal(t, StateChange{"cb", StateOpen, StateHalfOpen}, stateChange)

	assert.Nil(t, succeed(customCB))
	assert.Nil(t, succeed(customCB))
	assert.Equal(t, StateHalfOpen, customCB.State())
	assert.Equal(t, Counts{2, 2, 0, 2, 0}, customCB.counts)

	// StateHalfOpen to StateClosed
	ch := succeedLater(customCB, time.Duration(100)*time.Millisecond) // 3 consecutive successes
	time.Sleep(time.Duration(50) * time.Millisecond)
	assert.Equal(t, Counts{3, 2, 0, 2, 0}, customCB.counts)
	assert.Error(t, succeed(customCB)) // over MaxRequests
	assert.Nil(t, <-ch)
	assert.Equal(t, StateClosed, customCB.State())
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, customCB.counts)
	assert.False(t, customCB.expiry.IsZero())
	assert.Equal(t, StateChange{"cb", StateHalfOpen, StateClosed}, stateChange)
}

func TestPanicInRequest(t *testing.T) {
	assert.Panics(t, func() { causePanic(defaultCB) })
	assert.Equal(t, Counts{1, 0, 1, 0, 1}, defaultCB.counts)
}

func TestGeneration(t *testing.T) {
	pseudoSleep(customCB, time.Duration(29)*time.Second)
	assert.Nil(t, succeed(customCB))
	ch := succeedLater(customCB, time.Duration(1500)*time.Millisecond)
	time.Sleep(time.Duration(500) * time.Millisecond)
	assert.Equal(t, Counts{2, 1, 0, 1, 0}, customCB.counts)

	time.Sleep(time.Duration(500) * time.Millisecond) // over Interval
	assert.Equal(t, StateClosed, customCB.State())
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, customCB.counts)

	// the request from the previous generation has no effect on customCB.counts
	assert.Nil(t, <-ch)
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, customCB.counts)
}

func TestCircuitBreakerInParallel(t *testing.T) {
	runtime.GOMAXPROCS(runtime.NumCPU())

	ch := make(chan error)

	const numReqs = 10000
	routine := func() {
		for i := 0; i < numReqs; i++ {
			ch <- succeed(customCB)
		}
	}

	const numRoutines = 10
	for i := 0; i < numRoutines; i++ {
		go routine()
	}

	total := uint32(numReqs * numRoutines)
	for i := uint32(0); i < total; i++ {
		err := <-ch
		assert.Nil(t, err)
	}
	assert.Equal(t, Counts{total, total, 0, total, 0}, customCB.counts)
}

func TestErrorStateOpen(t *testing.T) {
	err := defaultCB.errorStateOpen()
	assert.Equal(t, "circuit breaker is open", err.Error())

	err = customCB.errorStateOpen()
	assert.Equal(t, "circuit breaker 'cb' is open", err.Error())
}
