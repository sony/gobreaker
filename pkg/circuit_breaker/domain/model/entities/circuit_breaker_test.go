package entities

import (
	"fmt"
	"github.com/stretchr/testify/assert"
	"runtime"
	"testing"
	"time"
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

//func succeed2Step(cb *TwoStepCircuitBreaker) error {
//	done, err := cb.Allow()
//	if err != nil {
//		return err
//	}
//
//	done(true)
//	return nil
//}

func fail(cb *CircuitBreaker) error {
	msg := "fail"
	_, err := cb.Execute(func() (interface{}, error) { return nil, fmt.Errorf(msg) })
	if err.Error() == msg {
		return nil
	}
	return err
}

//func fail2Step(cb *TwoStepCircuitBreaker) error {
//	done, err := cb.Allow()
//	if err != nil {
//		return err
//	}
//
//	done(false)
//	return nil
//}

func causePanic(cb *CircuitBreaker) error {
	_, err := cb.Execute(func() (interface{}, error) { panic("oops"); return nil, nil })
	return err
}

func newCustom() *CircuitBreaker {
	var customConfig = config{
		name:        "cb",
		maxRequests: 3,
		interval:    time.Duration(30) * time.Second,
		timeout:     time.Duration(90) * time.Second,
		readyToTrip: func(counts RequestsCounts) bool {
			numReqs := counts.Requests
			failureRatio := float64(counts.TotalFailures) / float64(numReqs)

			counts.Clear() // no effect on customCB.counts

			return numReqs >= 3 && failureRatio >= 0.6
		},
		onStateChange: func(name string, from State, to State) {
			stateChange = StateChange{name, from, to}
		},
	}

	return NewCircuitBreaker(
		WithName(customConfig.name),
		WithMaxRequests(customConfig.maxRequests),
		WithInterval(customConfig.interval),
		WithTimeout(customConfig.timeout),
		WithReadyToTrip(customConfig.readyToTrip),
		WithOnStateChange(customConfig.onStateChange),
	)
}

func newNegativeDurationCB() *CircuitBreaker {
	var negativeConfig config
	negativeConfig.name = "ncb"
	negativeConfig.interval = time.Duration(-30) * time.Second
	negativeConfig.timeout = time.Duration(-90) * time.Second

	return NewCircuitBreaker(
		WithName(negativeConfig.name),
		WithInterval(negativeConfig.interval),
		WithTimeout(negativeConfig.timeout),
	)
}

func init() {
	defaultCB = NewCircuitBreaker()
	customCB = newCustom()
	//negativeDurationCB = newNegativeDurationCB()

}

func TestStateConstants(t *testing.T) {
	closed := &ClosedState{breaker: defaultCB}
	open := &OpenState{breaker: defaultCB}
	half := &HalfOpenState{breaker: defaultCB}

	assert.Equal(t, closed.Name(), ClosedStateName)
	assert.Equal(t, half.Name(), HalfOpenStateName)
	assert.Equal(t, open.Name(), OpenStateName)
}

func TestNewDefaultCircuitBreaker(t *testing.T) {
	defaultCB := NewCircuitBreaker()
	assert.Equal(t, "", defaultCB.name)
	assert.Equal(t, uint32(1), defaultCB.maxRequests)
	assert.Equal(t, time.Duration(0), defaultCB.interval)
	assert.Equal(t, time.Duration(60)*time.Second, defaultCB.timeout)
	assert.NotNil(t, defaultCB.readyToTrip)
	assert.Nil(t, defaultCB.onStateChange)
	assert.Equal(t, ClosedStateName, defaultCB.current.Name())
	assert.Equal(t, RequestsCounts{0, 0, 0, 0, 0}, defaultCB.counts)
	assert.True(t, defaultCB.expiry.IsZero())
}

func TestNewCustomCircuitBreaker(t *testing.T) {
	customCB := newCustom()
	assert.Equal(t, "cb", customCB.name)
	assert.Equal(t, uint32(3), customCB.maxRequests)
	assert.Equal(t, time.Duration(30)*time.Second, customCB.interval)
	assert.Equal(t, time.Duration(90)*time.Second, customCB.timeout)
	assert.NotNil(t, customCB.readyToTrip)
	assert.NotNil(t, customCB.onStateChange)
	assert.Equal(t, ClosedStateName, customCB.current.Name())
	assert.Equal(t, RequestsCounts{0, 0, 0, 0, 0}, customCB.counts)
	assert.False(t, customCB.expiry.IsZero())
}

func TestNewNegativeCircuitBreaker(t *testing.T) {
	negativeDurationCB := newNegativeDurationCB()
	assert.Equal(t, "ncb", negativeDurationCB.name)
	assert.Equal(t, uint32(1), negativeDurationCB.maxRequests)
	assert.Equal(t, time.Duration(0)*time.Second, negativeDurationCB.interval)
	assert.Equal(t, time.Duration(60)*time.Second, negativeDurationCB.timeout)
	assert.NotNil(t, negativeDurationCB.readyToTrip)
	assert.Nil(t, negativeDurationCB.onStateChange)
	assert.Equal(t, ClosedStateName, negativeDurationCB.current.Name())
	assert.Equal(t, RequestsCounts{0, 0, 0, 0, 0}, negativeDurationCB.counts)
	assert.True(t, negativeDurationCB.expiry.IsZero())
}

func TestDefaultCircuitBreaker(t *testing.T) {
	defaultCB := NewCircuitBreaker()
	assert.Equal(t, "", defaultCB.Name())

	for i := 0; i < 5; i++ {
		assert.Nil(t, fail(defaultCB))
	}
	assert.Equal(t, ClosedStateName, defaultCB.State().Name())

	assert.Equal(t, RequestsCounts{5, 0, 5, 0, 5}, defaultCB.counts)

	assert.Nil(t, succeed(defaultCB))
	assert.Equal(t, ClosedStateName, defaultCB.State().Name())
	assert.Equal(t, RequestsCounts{6, 1, 5, 1, 0}, defaultCB.counts)

	assert.Nil(t, fail(defaultCB))
	assert.Equal(t, ClosedStateName, defaultCB.State().Name())
	assert.Equal(t, RequestsCounts{7, 1, 6, 0, 1}, defaultCB.counts)

	// StateClosed to StateOpen
	for i := 0; i < 5; i++ {
		assert.Nil(t, fail(defaultCB)) // 6 consecutive failures
	}
	assert.Equal(t, OpenStateName, defaultCB.State().Name())
	assert.Equal(t, RequestsCounts{0, 0, 0, 0, 0}, defaultCB.counts)
	assert.False(t, defaultCB.expiry.IsZero())

	assert.Error(t, succeed(defaultCB))
	assert.Error(t, fail(defaultCB))
	assert.Equal(t, RequestsCounts{0, 0, 0, 0, 0}, defaultCB.counts)

	pseudoSleep(defaultCB, time.Duration(59)*time.Second)
	assert.Equal(t, OpenStateName, defaultCB.State().Name())

	//// StateOpen to StateHalfOpen
	pseudoSleep(defaultCB, time.Duration(1)*time.Second) // over Timeout
	assert.Equal(t, HalfOpenStateName, defaultCB.State().Name())
	assert.True(t, defaultCB.expiry.IsZero())

	//// StateHalfOpen to StateOpen
	assert.Nil(t, fail(defaultCB))
	assert.Equal(t, OpenStateName, defaultCB.State().Name())
	assert.Equal(t, RequestsCounts{0, 0, 0, 0, 0}, defaultCB.counts)
	assert.False(t, defaultCB.expiry.IsZero())

	//// StateOpen to StateHalfOpen
	pseudoSleep(defaultCB, time.Duration(60)*time.Second)
	assert.Equal(t, HalfOpenStateName, defaultCB.State().Name())
	assert.True(t, defaultCB.expiry.IsZero())

	// StateHalfOpen to StateClosed
	assert.Nil(t, succeed(defaultCB))
	assert.Equal(t, ClosedStateName, defaultCB.State().Name())
	assert.Equal(t, RequestsCounts{0, 0, 0, 0, 0}, defaultCB.counts)
	assert.True(t, defaultCB.expiry.IsZero())
}

func TestCustomCircuitBreaker(t *testing.T) {
	customCB = newCustom()

	closed := &ClosedState{breaker: customCB}
	open := &OpenState{breaker: customCB}
	half := &HalfOpenState{breaker: customCB}

	assert.Equal(t, "cb", customCB.Name())

	for i := 0; i < 5; i++ {
		assert.Nil(t, succeed(customCB))
		assert.Nil(t, fail(customCB))
	}
	assert.Equal(t, ClosedStateName, customCB.State().Name())
	assert.Equal(t, RequestsCounts{10, 5, 5, 0, 1}, customCB.counts)

	pseudoSleep(customCB, time.Duration(29)*time.Second)
	assert.Nil(t, succeed(customCB))
	assert.Equal(t, ClosedStateName, customCB.State().Name())
	assert.Equal(t, RequestsCounts{11, 6, 5, 1, 0}, customCB.counts)

	pseudoSleep(customCB, time.Duration(1)*time.Second) // over Interval
	assert.Nil(t, fail(customCB))
	assert.Equal(t, ClosedStateName, customCB.State().Name())
	assert.Equal(t, RequestsCounts{1, 0, 1, 0, 1}, customCB.counts)

	// StateClosed to StateOpen
	assert.Nil(t, succeed(customCB))
	assert.Nil(t, fail(customCB)) // failure ratio: 2/3 >= 0.6
	assert.Equal(t, OpenStateName, customCB.State().Name())
	assert.Equal(t, RequestsCounts{0, 0, 0, 0, 0}, customCB.counts)
	assert.False(t, customCB.expiry.IsZero())
	assert.Equal(t, StateChange{"cb", closed, open}, stateChange)

	// StateOpen to StateHalfOpen
	//println("StateOpen to StateHalfOpen -  pre sleep", customCB.current.Name())
	pseudoSleep(customCB, time.Duration(90)*time.Second)
	//println("StateOpen to StateHalfOpen -  post sleep", customCB.current.Name())
	assert.Equal(t, HalfOpenStateName, customCB.State().Name())
	//println("StateOpen to StateHalfOpen -  post equal", customCB.current.Name())
	assert.True(t, customCB.expiry.IsZero())
	assert.Equal(t, StateChange{"cb", open, half}, stateChange)

	assert.Nil(t, succeed(customCB))
	assert.Nil(t, succeed(customCB))
	assert.Equal(t, HalfOpenStateName, customCB.State().Name())
	assert.Equal(t, RequestsCounts{2, 2, 0, 2, 0}, customCB.counts)

	// StateHalfOpen to StateClosed
	ch := succeedLater(customCB, time.Duration(100)*time.Millisecond) // 3 consecutive successes
	time.Sleep(time.Duration(50) * time.Millisecond)

	// TODO Check this race condition
	// assert.Equal(t, RequestsCounts{3, 2, 0, 2, 0}, customCB.counts)

	assert.Error(t, succeed(customCB)) // over MaxRequests
	assert.Nil(t, <-ch)
	assert.Equal(t, ClosedStateName, customCB.State().Name())
	assert.Equal(t, RequestsCounts{0, 0, 0, 0, 0}, customCB.counts)
	assert.False(t, customCB.expiry.IsZero())
	assert.Equal(t, StateChange{"cb", half, closed}, stateChange)
}

//func TestTwoStepCircuitBreaker(t *testing.T) {
//	tscb := NewTwoStepCircuitBreaker(Settings{Name: "tscb"})
//	assert.Equal(t, "tscb", tscb.Name())
//
//	for i := 0; i < 5; i++ {
//		assert.Nil(t, fail2Step(tscb))
//	}
//
//	assert.Equal(t, StateClosed, tscb.State())
//	assert.Equal(t, Counts{5, 0, 5, 0, 5}, tscb.cb.counts)
//
//	assert.Nil(t, succeed2Step(tscb))
//	assert.Equal(t, StateClosed, tscb.State())
//	assert.Equal(t, Counts{6, 1, 5, 1, 0}, tscb.cb.counts)
//
//	assert.Nil(t, fail2Step(tscb))
//	assert.Equal(t, StateClosed, tscb.State())
//	assert.Equal(t, Counts{7, 1, 6, 0, 1}, tscb.cb.counts)
//
//	// StateClosed to StateOpen
//	for i := 0; i < 5; i++ {
//		assert.Nil(t, fail2Step(tscb)) // 6 consecutive failures
//	}
//	assert.Equal(t, StateOpen, tscb.State())
//	assert.Equal(t, Counts{0, 0, 0, 0, 0}, tscb.cb.counts)
//	assert.False(t, tscb.cb.expiry.IsZero())
//
//	assert.Error(t, succeed2Step(tscb))
//	assert.Error(t, fail2Step(tscb))
//	assert.Equal(t, Counts{0, 0, 0, 0, 0}, tscb.cb.counts)
//
//	pseudoSleep(tscb.cb, time.Duration(59)*time.Second)
//	assert.Equal(t, StateOpen, tscb.State())
//
//	// StateOpen to StateHalfOpen
//	pseudoSleep(tscb.cb, time.Duration(1)*time.Second) // over Timeout
//	assert.Equal(t, StateHalfOpen, tscb.State())
//	assert.True(t, tscb.cb.expiry.IsZero())
//
//	// StateHalfOpen to StateOpen
//	assert.Nil(t, fail2Step(tscb))
//	assert.Equal(t, StateOpen, tscb.State())
//	assert.Equal(t, Counts{0, 0, 0, 0, 0}, tscb.cb.counts)
//	assert.False(t, tscb.cb.expiry.IsZero())
//
//	// StateOpen to StateHalfOpen
//	pseudoSleep(tscb.cb, time.Duration(60)*time.Second)
//	assert.Equal(t, StateHalfOpen, tscb.State())
//	assert.True(t, tscb.cb.expiry.IsZero())
//
//	// StateHalfOpen to StateClosed
//	assert.Nil(t, succeed2Step(tscb))
//	assert.Equal(t, StateClosed, tscb.State())
//	assert.Equal(t, Counts{0, 0, 0, 0, 0}, tscb.cb.counts)
//	assert.True(t, tscb.cb.expiry.IsZero())
//}

func TestPanicInRequest(t *testing.T) {
	defaultCB := NewCircuitBreaker()
	assert.Panics(t, func() { causePanic(defaultCB) })
	assert.Equal(t, RequestsCounts{1, 0, 1, 0, 1}, defaultCB.counts)
}

func TestGeneration(t *testing.T) {
	customCB = newCustom()

	pseudoSleep(customCB, time.Duration(29)*time.Second)
	assert.Nil(t, succeed(customCB))
	ch := succeedLater(customCB, time.Duration(1500)*time.Millisecond)
	time.Sleep(time.Duration(500) * time.Millisecond)

	// TODO Check this race condition
	// assert.Equal(t, RequestsCounts{2, 1, 0, 1, 0}, customCB.counts)

	time.Sleep(time.Duration(500) * time.Millisecond) // over Interval
	assert.Equal(t, ClosedStateName, customCB.State().Name())
	assert.Equal(t, RequestsCounts{0, 0, 0, 0, 0}, customCB.counts)

	// the request from the previous generation has no effect on customCB.counts
	assert.Nil(t, <-ch)
	assert.Equal(t, RequestsCounts{0, 0, 0, 0, 0}, customCB.counts)
}

func TestCustomIsSuccessful(t *testing.T) {
	isSuccessful := func(error) bool {
		return true
	}
	cb := NewCircuitBreaker(WithIsSuccessful(isSuccessful))

	for i := 0; i < 5; i++ {
		assert.Nil(t, fail(cb))
	}
	assert.Equal(t, ClosedStateName, cb.State().Name())
	assert.Equal(t, RequestsCounts{5, 5, 0, 5, 0}, cb.counts)

	cb.counts.Clear()

	cb.isSuccessful = func(err error) bool {
		return err == nil
	}
	for i := 0; i < 6; i++ {
		assert.Nil(t, fail(cb))
	}
	assert.Equal(t, OpenStateName, cb.State().Name())

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
	assert.Equal(t, RequestsCounts{total, total, 0, total, 0}, customCB.counts)
}
