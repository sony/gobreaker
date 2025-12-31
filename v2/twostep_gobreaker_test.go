package gobreaker

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func succeed2Step(cb *TwoStepCircuitBreaker[bool]) error {
	done, err := cb.Allow()
	if err != nil {
		return err
	}

	done(nil)
	return nil
}

func fail2Step(cb *TwoStepCircuitBreaker[bool]) error {
	done, err := cb.Allow()
	if err != nil {
		return err
	}

	done(errFailed)
	return nil
}

func exclude2step(cb *TwoStepCircuitBreaker[bool]) error {
	done, err := cb.Allow()
	if err != nil {
		return err
	}

	done(errExcluded)
	return nil
}

func exclude2StepWithDelay(cb *TwoStepCircuitBreaker[bool]) (chan struct{}, error) {
	done, err := cb.Allow()
	if err != nil {
		return nil, err
	}

	finished := make(chan struct{})
	go func() {
		time.Sleep(100 * time.Millisecond)
		done(errExcluded)
		close(finished)
	}()

	return finished, nil
}

func TestTwoStepCircuitBreaker(t *testing.T) {
	tscb := NewTwoStepCircuitBreaker[bool](
		Settings{
			Name:        "tscb",
			MaxRequests: 2,
			IsExcluded: func(err error) bool {
				return errors.Is(err, errExcluded)
			},
		},
	)
	assert.Equal(t, "tscb", tscb.Name())

	for i := 0; i < 5; i++ {
		assert.Nil(t, fail2Step(tscb))
	}

	assert.Equal(t, StateClosed, tscb.State())
	assert.Equal(t, Counts{Requests: 5, TotalFailures: 5, ConsecutiveFailures: 5}, tscb.cb.Counts())

	assert.Nil(t, succeed2Step(tscb))
	assert.Equal(t, StateClosed, tscb.State())
	assert.Equal(t, Counts{Requests: 6, TotalSuccesses: 1, TotalFailures: 5, ConsecutiveSuccesses: 1}, tscb.cb.Counts())

	assert.Nil(t, fail2Step(tscb))
	assert.Equal(t, StateClosed, tscb.State())
	assert.Equal(t, Counts{Requests: 7, TotalSuccesses: 1, TotalFailures: 6, ConsecutiveFailures: 1}, tscb.cb.Counts())

	// StateClosed to StateOpen
	for i := 0; i < 5; i++ {
		assert.Nil(t, fail2Step(tscb)) // 6 consecutive failures
	}
	assert.Equal(t, StateOpen, tscb.State())
	assert.Equal(t, Counts{}, tscb.cb.Counts())
	assert.False(t, tscb.cb.expiry.IsZero())

	assert.Error(t, succeed2Step(tscb))
	assert.Error(t, fail2Step(tscb))
	assert.Error(t, exclude2step(tscb))
	assert.Equal(t, Counts{}, tscb.cb.Counts())

	pseudoSleep(tscb.cb, tscb.cb.timeout-time.Nanosecond)
	assert.Equal(t, StateOpen, tscb.State())

	// StateOpen to StateHalfOpen
	pseudoSleep(tscb.cb, time.Microsecond) // over timeout
	assert.Equal(t, StateHalfOpen, tscb.State())
	assert.True(t, tscb.cb.expiry.IsZero())

	// in half-open state, when max number of requests are in progress,
	// others get rejected because of too many requests
	// but if in-progress requests complete with excluded, circuit breaker can accept requests again
	ch1, err := exclude2StepWithDelay(tscb)
	assert.Nil(t, err)
	ch2, err := exclude2StepWithDelay(tscb)
	assert.Nil(t, err)
	// rejected because of too many requests
	assert.Equal(t, ErrTooManyRequests, succeed2Step(tscb))
	assert.Equal(t, ErrTooManyRequests, fail2Step(tscb))
	// wait for excluded requests to complete
	<-ch1
	<-ch2
	// now circuit breaker should accept requests again
	assert.Nil(t, succeed2Step(tscb))

	// StateHalfOpen to StateOpen
	assert.Nil(t, fail2Step(tscb))
	assert.Equal(t, StateOpen, tscb.State())
	assert.Equal(t, Counts{}, tscb.cb.Counts())
	assert.False(t, tscb.cb.expiry.IsZero())

	// StateOpen to StateHalfOpen
	pseudoSleep(tscb.cb, tscb.cb.timeout+time.Nanosecond)
	assert.Equal(t, StateHalfOpen, tscb.State())
	assert.True(t, tscb.cb.expiry.IsZero())

	// StateHalfOpen to StateClosed
	assert.Nil(t, succeed2Step(tscb))
	assert.Nil(t, succeed2Step(tscb))
	assert.Equal(t, StateClosed, tscb.State())
	assert.Equal(t, Counts{}, tscb.cb.Counts())
	assert.True(t, tscb.cb.expiry.IsZero())
}
