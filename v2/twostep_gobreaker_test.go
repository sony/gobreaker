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

	done(errors.New("failed"))
	return nil
}

func exclude2step(cb *TwoStepCircuitBreaker[bool]) error {
	done, err := cb.Allow()
	if err != nil {
		return err
	}

	done(errors.New("excluded"))
	return nil
}

func exclude2StepWithDoneDelay(cb *TwoStepCircuitBreaker[bool], delay time.Duration) error {
	done, err := cb.Allow()
	if err != nil {
		return err
	}

	go func() {
		time.Sleep(delay)
		defer done(errors.New("excluded"))
	}()

	return nil
}

func TestTwoStepCircuitBreaker(t *testing.T) {
	tscb := NewTwoStepCircuitBreaker[bool](Settings{Name: "tscb", MaxRequests: 2})
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

	pseudoSleep(tscb.cb, time.Duration(59)*time.Second)
	assert.Equal(t, StateOpen, tscb.State())

	// StateOpen to StateHalfOpen
	pseudoSleep(tscb.cb, time.Duration(2)*time.Second) // over Timeout
	assert.Equal(t, StateHalfOpen, tscb.State())
	assert.True(t, tscb.cb.expiry.IsZero())

	// In half-open state, when max requests are in progress, others get ErrTooManyRequests
	// But if in-progress requests complete with Excluded, circuit breaker can accept requests again
	assert.Nil(t, exclude2StepWithDoneDelay(tscb, 100*time.Millisecond))
	assert.Nil(t, exclude2StepWithDoneDelay(tscb, 100*time.Millisecond))
	// Verify new requests are rejected while at capacity
	assert.Equal(t, ErrTooManyRequests, fail2Step(tscb))
	assert.Equal(t, ErrTooManyRequests, succeed2Step(tscb))
	// Wait for excluded requests to complete
	time.Sleep(time.Second)
	// Now circuit breaker should accept requests again
	assert.Nil(t, succeed2Step(tscb))

	// StateHalfOpen to StateOpen
	assert.Nil(t, fail2Step(tscb))
	assert.Equal(t, StateOpen, tscb.State())
	assert.Equal(t, Counts{}, tscb.cb.Counts())
	assert.False(t, tscb.cb.expiry.IsZero())

	// StateOpen to StateHalfOpen
	pseudoSleep(tscb.cb, time.Duration(61)*time.Second)
	assert.Equal(t, StateHalfOpen, tscb.State())
	assert.True(t, tscb.cb.expiry.IsZero())

	// StateHalfOpen to StateClosed
	assert.Nil(t, succeed2Step(tscb))
	assert.Nil(t, succeed2Step(tscb))
	assert.Equal(t, StateClosed, tscb.State())
	assert.Equal(t, Counts{}, tscb.cb.Counts())
	assert.True(t, tscb.cb.expiry.IsZero())
}
