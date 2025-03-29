package gobreaker

import (
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var defaultCB *CircuitBreaker[bool]
var customCB *CircuitBreaker[bool]

var rollingWindowCB *CircuitBreaker[bool]

type StateChange struct {
	name string
	from State
	to   State
}

var stateChange StateChange

func pseudoSleep(cb *CircuitBreaker[bool], period time.Duration) {
	if !cb.expiry.IsZero() {
		cb.expiry = cb.expiry.Add(-period)
	}
}

func succeed(cb *CircuitBreaker[bool]) error {
	_, err := cb.Execute(func() (bool, error) { return true, nil })
	return err
}

func succeedLater(cb *CircuitBreaker[bool], delay time.Duration) <-chan error {
	ch := make(chan error)
	go func() {
		_, err := cb.Execute(func() (bool, error) {
			time.Sleep(delay)
			return true, nil
		})
		ch <- err
	}()
	return ch
}

func succeed2Step(cb *TwoStepCircuitBreaker[bool]) error {
	done, err := cb.Allow()
	if err != nil {
		return err
	}

	done(true)
	return nil
}

func fail(cb *CircuitBreaker[bool]) error {
	msg := "fail"
	_, err := cb.Execute(func() (bool, error) { return false, errors.New(msg) })
	if err.Error() == msg {
		return nil
	}
	return err
}

func fail2Step(cb *TwoStepCircuitBreaker[bool]) error {
	done, err := cb.Allow()
	if err != nil {
		return err
	}

	done(false)
	return nil
}

func causePanic(cb *CircuitBreaker[bool]) error {
	_, err := cb.Execute(func() (bool, error) { panic("oops"); return false, nil })
	return err
}

func newCustom() *CircuitBreaker[bool] {
	var customSt Settings
	customSt.Name = "cb"
	customSt.MaxRequests = 3
	customSt.Interval = time.Duration(30) * time.Second
	customSt.Timeout = time.Duration(90) * time.Second
	customSt.ReadyToTrip = func(counts Counts) bool {
		numReqs := counts.Requests
		failureRatio := float64(counts.TotalFailures) / float64(numReqs)

		return numReqs >= 3 && failureRatio >= 0.6
	}
	customSt.OnStateChange = func(name string, from State, to State) {
		stateChange = StateChange{name, from, to}
	}

	return NewCircuitBreaker[bool](customSt)
}

func newRollingWindow() *CircuitBreaker[bool] {
	var rollingWindowSt Settings
	rollingWindowSt.Name = "rw"
	rollingWindowSt.MaxRequests = 3
	rollingWindowSt.Interval = time.Duration(30) * time.Second
	rollingWindowSt.BucketPeriod = time.Duration(3) * time.Second
	rollingWindowSt.Timeout = time.Duration(90) * time.Second
	rollingWindowSt.ReadyToTrip = func(counts Counts) bool {
		numReqs := counts.Requests
		failureRatio := float64(counts.TotalFailures) / float64(numReqs)

		return numReqs >= 3 && failureRatio >= 0.6
	}
	rollingWindowSt.OnStateChange = func(name string, from State, to State) {
		stateChange = StateChange{name, from, to}
	}

	return NewCircuitBreaker[bool](rollingWindowSt)
}

func newNegativeDurationCB() *CircuitBreaker[bool] {
	var negativeSt Settings
	negativeSt.Name = "ncb"
	negativeSt.Interval = time.Duration(-30) * time.Second
	negativeSt.Timeout = time.Duration(-90) * time.Second

	return NewCircuitBreaker[bool](negativeSt)
}

func init() {
	defaultCB = NewCircuitBreaker[bool](Settings{})
	customCB = newCustom()
	rollingWindowCB = newRollingWindow()
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
	defaultCB := NewCircuitBreaker[bool](Settings{})
	assert.Equal(t, "", defaultCB.name)
	assert.Equal(t, uint32(1), defaultCB.maxRequests)
	assert.Equal(t, time.Duration(0), defaultCB.interval)
	assert.Equal(t, time.Duration(60)*time.Second, defaultCB.timeout)
	assert.NotNil(t, defaultCB.readyToTrip)
	assert.Nil(t, defaultCB.onStateChange)
	assert.Equal(t, StateClosed, defaultCB.state)
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, defaultCB.windowCounts.ToCounts())
	assert.True(t, defaultCB.expiry.IsZero())

	customCB := newCustom()
	assert.Equal(t, "cb", customCB.name)
	assert.Equal(t, uint32(3), customCB.maxRequests)
	assert.Equal(t, time.Duration(30)*time.Second, customCB.interval)
	assert.Equal(t, time.Duration(90)*time.Second, customCB.timeout)
	assert.NotNil(t, customCB.readyToTrip)
	assert.NotNil(t, customCB.onStateChange)
	assert.Equal(t, StateClosed, customCB.state)
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, customCB.windowCounts.ToCounts())
	assert.False(t, customCB.expiry.IsZero())

	rollingWindowCB := newRollingWindow()
	assert.Equal(t, "rw", rollingWindowCB.name)
	assert.Equal(t, uint32(3), rollingWindowCB.maxRequests)
	assert.Equal(t, time.Duration(3)*time.Second, rollingWindowCB.interval)
	assert.Equal(t, int64(10), rollingWindowCB.windowCounts.numBuckets)
	assert.Equal(t, time.Duration(90)*time.Second, rollingWindowCB.timeout)
	assert.NotNil(t, rollingWindowCB.readyToTrip)
	assert.NotNil(t, rollingWindowCB.onStateChange)
	assert.Equal(t, StateClosed, rollingWindowCB.state)
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, rollingWindowCB.windowCounts.ToCounts())
	assert.False(t, rollingWindowCB.expiry.IsZero())

	negativeDurationCB := newNegativeDurationCB()
	assert.Equal(t, "ncb", negativeDurationCB.name)
	assert.Equal(t, uint32(1), negativeDurationCB.maxRequests)
	assert.Equal(t, time.Duration(0)*time.Second, negativeDurationCB.interval)
	assert.Equal(t, time.Duration(60)*time.Second, negativeDurationCB.timeout)
	assert.NotNil(t, negativeDurationCB.readyToTrip)
	assert.Nil(t, negativeDurationCB.onStateChange)
	assert.Equal(t, StateClosed, negativeDurationCB.state)
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, negativeDurationCB.windowCounts.ToCounts())
	assert.True(t, negativeDurationCB.expiry.IsZero())
}

func TestDefaultCircuitBreaker(t *testing.T) {
	assert.Equal(t, "", defaultCB.Name())

	for i := 0; i < 5; i++ {
		assert.Nil(t, fail(defaultCB))
	}
	assert.Equal(t, StateClosed, defaultCB.State())
	assert.Equal(t, Counts{5, 0, 5, 0, 5}, defaultCB.windowCounts.ToCounts())

	assert.Nil(t, succeed(defaultCB))
	assert.Equal(t, StateClosed, defaultCB.State())
	assert.Equal(t, Counts{6, 1, 5, 1, 0}, defaultCB.windowCounts.ToCounts())

	assert.Nil(t, fail(defaultCB))
	assert.Equal(t, StateClosed, defaultCB.State())
	assert.Equal(t, Counts{7, 1, 6, 0, 1}, defaultCB.windowCounts.ToCounts())

	// StateClosed to StateOpen
	for i := 0; i < 5; i++ {
		assert.Nil(t, fail(defaultCB)) // 6 consecutive failures
	}
	assert.Equal(t, StateOpen, defaultCB.State())
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, defaultCB.windowCounts.ToCounts())
	assert.False(t, defaultCB.expiry.IsZero())

	assert.Error(t, succeed(defaultCB))
	assert.Error(t, fail(defaultCB))
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, defaultCB.windowCounts.ToCounts())

	pseudoSleep(defaultCB, time.Duration(59)*time.Second)
	assert.Equal(t, StateOpen, defaultCB.State())

	// StateOpen to StateHalfOpen
	pseudoSleep(defaultCB, time.Duration(1)*time.Second) // over Timeout
	assert.Equal(t, StateHalfOpen, defaultCB.State())
	assert.True(t, defaultCB.expiry.IsZero())

	// StateHalfOpen to StateOpen
	assert.Nil(t, fail(defaultCB))
	assert.Equal(t, StateOpen, defaultCB.State())
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, defaultCB.windowCounts.ToCounts())
	assert.False(t, defaultCB.expiry.IsZero())

	// StateOpen to StateHalfOpen
	pseudoSleep(defaultCB, time.Duration(60)*time.Second)
	assert.Equal(t, StateHalfOpen, defaultCB.State())
	assert.True(t, defaultCB.expiry.IsZero())

	// StateHalfOpen to StateClosed
	assert.Nil(t, succeed(defaultCB))
	assert.Equal(t, StateClosed, defaultCB.State())
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, defaultCB.windowCounts.ToCounts())
	assert.True(t, defaultCB.expiry.IsZero())
}

func TestCustomCircuitBreaker(t *testing.T) {
	assert.Equal(t, "cb", customCB.Name())

	for i := 0; i < 5; i++ {
		assert.Nil(t, succeed(customCB))
		assert.Nil(t, fail(customCB))
	}
	assert.Equal(t, StateClosed, customCB.State())
	assert.Equal(t, Counts{10, 5, 5, 0, 1}, customCB.windowCounts.ToCounts())

	pseudoSleep(customCB, time.Duration(29)*time.Second)
	assert.Nil(t, succeed(customCB))
	assert.Equal(t, StateClosed, customCB.State())
	assert.Equal(t, Counts{11, 6, 5, 1, 0}, customCB.windowCounts.ToCounts())

	pseudoSleep(customCB, time.Duration(1)*time.Second) // over Interval
	assert.Nil(t, fail(customCB))
	assert.Equal(t, StateClosed, customCB.State())
	assert.Equal(t, Counts{1, 0, 1, 0, 1}, customCB.windowCounts.ToCounts())

	// StateClosed to StateOpen
	assert.Nil(t, succeed(customCB))
	assert.Nil(t, fail(customCB)) // failure ratio: 2/3 >= 0.6
	assert.Equal(t, StateOpen, customCB.State())
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, customCB.windowCounts.ToCounts())
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
	assert.Equal(t, Counts{2, 2, 0, 2, 0}, customCB.windowCounts.ToCounts())

	// StateHalfOpen to StateClosed
	ch := succeedLater(customCB, time.Duration(100)*time.Millisecond) // 3 consecutive successes
	time.Sleep(time.Duration(50) * time.Millisecond)
	assert.Equal(t, Counts{3, 2, 0, 2, 0}, customCB.windowCounts.ToCounts())
	assert.Error(t, succeed(customCB)) // over MaxRequests
	assert.Nil(t, <-ch)
	assert.Equal(t, StateClosed, customCB.State())
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, customCB.windowCounts.ToCounts())
	assert.False(t, customCB.expiry.IsZero())
	assert.Equal(t, StateChange{"cb", StateHalfOpen, StateClosed}, stateChange)
}

func TestRollingWindowCircuitBreaker(t *testing.T) {
	assert.Equal(t, "rw", rollingWindowCB.Name())

	for i := 0; i < 5; i++ {
		assert.Nil(t, succeed(rollingWindowCB))
		assert.Nil(t, fail(rollingWindowCB))
	}
	assert.Equal(t, StateClosed, rollingWindowCB.State())
	assert.Equal(t, Counts{10, 5, 5, 0, 1}, rollingWindowCB.windowCounts.ToCounts())
	assert.Equal(t, 1, rollingWindowCB.windowCounts.bucketCounts.Len())
	assert.Equal(t, Counts{10, 5, 5, 0, 1}, rollingWindowCB.windowCounts.bucketCounts.Front().Value)

	pseudoSleep(rollingWindowCB, time.Duration(3)*time.Second)
	assert.Nil(t, succeed(rollingWindowCB))
	assert.Equal(t, StateClosed, rollingWindowCB.State())
	assert.Equal(t, Counts{11, 6, 5, 1, 0}, rollingWindowCB.windowCounts.ToCounts())
	assert.Equal(t, 2, rollingWindowCB.windowCounts.bucketCounts.Len())
	assert.Equal(t, Counts{10, 5, 5, 0, 1}, rollingWindowCB.windowCounts.bucketCounts.Front().Value)
	assert.Equal(t, Counts{1, 1, 0, 1, 0}, rollingWindowCB.windowCounts.bucketCounts.Front().Next().Value)

	pseudoSleep(rollingWindowCB, time.Duration(2)*time.Second)
	assert.Nil(t, succeed(rollingWindowCB))
	assert.Equal(t, StateClosed, rollingWindowCB.State())
	assert.Equal(t, 2, rollingWindowCB.windowCounts.bucketCounts.Len())
	assert.Equal(t, Counts{12, 7, 5, 2, 0}, rollingWindowCB.windowCounts.ToCounts())
	assert.Equal(t, Counts{10, 5, 5, 0, 1}, rollingWindowCB.windowCounts.bucketCounts.Front().Value)
	assert.Equal(t, Counts{2, 2, 0, 2, 0}, rollingWindowCB.windowCounts.bucketCounts.Front().Next().Value)

	pseudoSleep(rollingWindowCB, time.Duration(2)*time.Second)
	assert.Nil(t, succeed(rollingWindowCB))
	assert.Equal(t, StateClosed, rollingWindowCB.State())
	assert.Equal(t, Counts{13, 8, 5, 3, 0}, rollingWindowCB.windowCounts.ToCounts())
	assert.Equal(t, 3, rollingWindowCB.windowCounts.bucketCounts.Len())
	assert.Equal(t, Counts{10, 5, 5, 0, 1}, rollingWindowCB.windowCounts.bucketCounts.Front().Value)
	assert.Equal(t, Counts{2, 2, 0, 2, 0}, rollingWindowCB.windowCounts.bucketCounts.Front().Next().Value)
	assert.Equal(t, Counts{1, 1, 0, 1, 0}, rollingWindowCB.windowCounts.bucketCounts.Front().Next().Next().Value)

	pseudoSleep(rollingWindowCB, time.Duration(2)*time.Second)
	assert.Nil(t, fail(rollingWindowCB))
	assert.Equal(t, StateClosed, rollingWindowCB.State())
	assert.Equal(t, Counts{14, 8, 6, 0, 1}, rollingWindowCB.windowCounts.ToCounts())
	assert.Equal(t, 4, rollingWindowCB.windowCounts.bucketCounts.Len())
	assert.Equal(t, Counts{10, 5, 5, 0, 1}, rollingWindowCB.windowCounts.bucketCounts.Front().Value)
	assert.Equal(t, Counts{2, 2, 0, 2, 0}, rollingWindowCB.windowCounts.bucketCounts.Front().Next().Value)
	assert.Equal(t, Counts{1, 1, 0, 1, 0}, rollingWindowCB.windowCounts.bucketCounts.Front().Next().Next().Value)
	assert.Equal(t, Counts{1, 0, 1, 0, 1}, rollingWindowCB.windowCounts.bucketCounts.Back().Value)

	// fill all the buckets
	for i := 0; i < 6; i++ {
		pseudoSleep(rollingWindowCB, time.Duration(3)*time.Second)
		assert.Nil(t, succeed(rollingWindowCB))
		assert.Nil(t, fail(rollingWindowCB))
		assert.Equal(t, Counts{uint32(16 + 2*i), uint32(9 + i), uint32(7 + i), 0, 1}, rollingWindowCB.windowCounts.ToCounts())
	}

	assert.Equal(t, 10, rollingWindowCB.windowCounts.bucketCounts.Len())

	// first bucket should be discarded
	pseudoSleep(rollingWindowCB, time.Duration(3)*time.Second)
	assert.Nil(t, fail(rollingWindowCB))
	assert.Equal(t, 10, rollingWindowCB.windowCounts.bucketCounts.Len())
	assert.Equal(t, Counts{17, 9, 8, 0, 2}, rollingWindowCB.windowCounts.ToCounts())

	for i := 0; i < 5; i++ {
		assert.Nil(t, fail(rollingWindowCB))
		assert.Equal(t, Counts{uint32(18 + i), 9, uint32(9 + i), 0, uint32(3 + i)}, rollingWindowCB.windowCounts.ToCounts())
	}

	assert.Equal(t, StateClosed, rollingWindowCB.State())

	assert.Nil(t, fail(rollingWindowCB)) //failureRate = 14/23 > 0.6
	assert.Equal(t, StateOpen, rollingWindowCB.State())
	assert.False(t, rollingWindowCB.expiry.IsZero())
	assert.Equal(t, StateChange{"rw", StateClosed, StateOpen}, stateChange)

	// StateOpen to StateHalfOpen
	pseudoSleep(rollingWindowCB, time.Duration(90)*time.Second)
	assert.Equal(t, StateHalfOpen, rollingWindowCB.State())
	assert.True(t, defaultCB.expiry.IsZero())
	assert.Equal(t, StateChange{"rw", StateOpen, StateHalfOpen}, stateChange)

	assert.Nil(t, succeed(rollingWindowCB))
	assert.Nil(t, succeed(rollingWindowCB))
	assert.Equal(t, StateHalfOpen, rollingWindowCB.State())
	assert.Equal(t, Counts{2, 2, 0, 2, 0}, rollingWindowCB.windowCounts.ToCounts())

	// StateHalfOpen to StateClosed
	ch := succeedLater(rollingWindowCB, time.Duration(100)*time.Millisecond) // 3 consecutive successes
	time.Sleep(time.Duration(50) * time.Millisecond)
	assert.Equal(t, Counts{3, 2, 0, 2, 0}, rollingWindowCB.windowCounts.ToCounts())
	assert.Error(t, succeed(rollingWindowCB)) // over MaxRequests
	assert.Nil(t, <-ch)
	assert.Equal(t, StateClosed, rollingWindowCB.State())
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, rollingWindowCB.windowCounts.ToCounts())
	assert.False(t, rollingWindowCB.expiry.IsZero())
	assert.Equal(t, StateChange{"rw", StateHalfOpen, StateClosed}, stateChange)
}

func TestTwoStepCircuitBreaker(t *testing.T) {
	tscb := NewTwoStepCircuitBreaker[bool](Settings{Name: "tscb"})
	assert.Equal(t, "tscb", tscb.Name())

	for i := 0; i < 5; i++ {
		assert.Nil(t, fail2Step(tscb))
	}

	assert.Equal(t, StateClosed, tscb.State())
	assert.Equal(t, Counts{5, 0, 5, 0, 5}, tscb.cb.windowCounts.ToCounts())

	assert.Nil(t, succeed2Step(tscb))
	assert.Equal(t, StateClosed, tscb.State())
	assert.Equal(t, Counts{6, 1, 5, 1, 0}, tscb.cb.windowCounts.ToCounts())

	assert.Nil(t, fail2Step(tscb))
	assert.Equal(t, StateClosed, tscb.State())
	assert.Equal(t, Counts{7, 1, 6, 0, 1}, tscb.cb.windowCounts.ToCounts())

	// StateClosed to StateOpen
	for i := 0; i < 5; i++ {
		assert.Nil(t, fail2Step(tscb)) // 6 consecutive failures
	}
	assert.Equal(t, StateOpen, tscb.State())
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, tscb.cb.windowCounts.ToCounts())
	assert.False(t, tscb.cb.expiry.IsZero())

	assert.Error(t, succeed2Step(tscb))
	assert.Error(t, fail2Step(tscb))
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, tscb.cb.windowCounts.ToCounts())

	pseudoSleep(tscb.cb, time.Duration(59)*time.Second)
	assert.Equal(t, StateOpen, tscb.State())

	// StateOpen to StateHalfOpen
	pseudoSleep(tscb.cb, time.Duration(1)*time.Second) // over Timeout
	assert.Equal(t, StateHalfOpen, tscb.State())
	assert.True(t, tscb.cb.expiry.IsZero())

	// StateHalfOpen to StateOpen
	assert.Nil(t, fail2Step(tscb))
	assert.Equal(t, StateOpen, tscb.State())
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, tscb.cb.windowCounts.ToCounts())
	assert.False(t, tscb.cb.expiry.IsZero())

	// StateOpen to StateHalfOpen
	pseudoSleep(tscb.cb, time.Duration(60)*time.Second)
	assert.Equal(t, StateHalfOpen, tscb.State())
	assert.True(t, tscb.cb.expiry.IsZero())

	// StateHalfOpen to StateClosed
	assert.Nil(t, succeed2Step(tscb))
	assert.Equal(t, StateClosed, tscb.State())
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, tscb.cb.windowCounts.ToCounts())
	assert.True(t, tscb.cb.expiry.IsZero())
}

func TestPanicInRequest(t *testing.T) {
	assert.Panics(t, func() { _ = causePanic(defaultCB) })
	assert.Equal(t, Counts{1, 0, 1, 0, 1}, defaultCB.windowCounts.ToCounts())
}

func TestGeneration(t *testing.T) {
	pseudoSleep(customCB, time.Duration(29)*time.Second)
	assert.Nil(t, succeed(customCB))
	ch := succeedLater(customCB, time.Duration(1500)*time.Millisecond)
	time.Sleep(time.Duration(500) * time.Millisecond)
	assert.Equal(t, Counts{2, 1, 0, 1, 0}, customCB.windowCounts.ToCounts())

	time.Sleep(time.Duration(500) * time.Millisecond) // over Interval
	assert.Equal(t, StateClosed, customCB.State())
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, customCB.windowCounts.ToCounts())

	// the request from the previous generation has no effect on customCB.windowCounts.ToCounts()
	assert.Nil(t, <-ch)
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, customCB.windowCounts.ToCounts())
}

func TestCustomIsSuccessful(t *testing.T) {
	isSuccessful := func(error) bool {
		return true
	}
	cb := NewCircuitBreaker[bool](Settings{IsSuccessful: isSuccessful})

	for i := 0; i < 5; i++ {
		assert.Nil(t, fail(cb))
	}
	assert.Equal(t, StateClosed, cb.State())
	assert.Equal(t, Counts{5, 5, 0, 5, 0}, cb.windowCounts.ToCounts())

	cb.windowCounts.clear()

	cb.isSuccessful = func(err error) bool {
		return err == nil
	}
	for i := 0; i < 6; i++ {
		assert.Nil(t, fail(cb))
	}
	assert.Equal(t, StateOpen, cb.State())

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
	assert.Equal(t, Counts{total, total, 0, total, 0}, customCB.windowCounts.ToCounts())
}

func TestRollingWindowCircuitBreakerInParallel(t *testing.T) {
	runtime.GOMAXPROCS(runtime.NumCPU())

	ch := make(chan error)

	const numReqs = 10000
	routine := func() {
		for i := 0; i < numReqs; i++ {
			ch <- succeed(rollingWindowCB)
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
	assert.Equal(t, Counts{total, total, 0, total, 0}, rollingWindowCB.windowCounts.ToCounts())
}
