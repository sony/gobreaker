package gobreaker

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func succeed2Step(cb *TwoStepCircuitBreaker[bool]) error {
	done, err := cb.Allow()
	if err != nil {
		return err
	}

	done(true)
	return nil
}

func fail2Step(cb *TwoStepCircuitBreaker[bool]) error {
	done, err := cb.Allow()
	if err != nil {
		return err
	}

	done(false)
	return nil
}

func TestTwoStepCircuitBreaker(t *testing.T) {
	tscb := NewTwoStepCircuitBreaker[bool](Settings{Name: "tscb"})
	assert.Equal(t, "tscb", tscb.Name())

	for i := 0; i < 5; i++ {
		assert.Nil(t, fail2Step(tscb))
	}

	assert.Equal(t, StateClosed, tscb.State())
	assert.Equal(t, Counts{5, 0, 5, 0, 5}, tscb.cb.Counts())

	assert.Nil(t, succeed2Step(tscb))
	assert.Equal(t, StateClosed, tscb.State())
	assert.Equal(t, Counts{6, 1, 5, 1, 0}, tscb.cb.Counts())

	assert.Nil(t, fail2Step(tscb))
	assert.Equal(t, StateClosed, tscb.State())
	assert.Equal(t, Counts{7, 1, 6, 0, 1}, tscb.cb.Counts())

	// StateClosed to StateOpen
	for i := 0; i < 5; i++ {
		assert.Nil(t, fail2Step(tscb)) // 6 consecutive failures
	}
	assert.Equal(t, StateOpen, tscb.State())
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, tscb.cb.Counts())
	assert.False(t, tscb.cb.expiry.IsZero())

	assert.Error(t, succeed2Step(tscb))
	assert.Error(t, fail2Step(tscb))
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, tscb.cb.Counts())

	pseudoSleep(tscb.cb, time.Duration(59)*time.Second)
	assert.Equal(t, StateOpen, tscb.State())

	// StateOpen to StateHalfOpen
	pseudoSleep(tscb.cb, time.Duration(1)*time.Second) // over Timeout
	assert.Equal(t, StateHalfOpen, tscb.State())
	assert.True(t, tscb.cb.expiry.IsZero())

	// StateHalfOpen to StateOpen
	assert.Nil(t, fail2Step(tscb))
	assert.Equal(t, StateOpen, tscb.State())
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, tscb.cb.Counts())
	assert.False(t, tscb.cb.expiry.IsZero())

	// StateOpen to StateHalfOpen
	pseudoSleep(tscb.cb, time.Duration(60)*time.Second)
	assert.Equal(t, StateHalfOpen, tscb.State())
	assert.True(t, tscb.cb.expiry.IsZero())

	// StateHalfOpen to StateClosed
	assert.Nil(t, succeed2Step(tscb))
	assert.Equal(t, StateClosed, tscb.State())
	assert.Equal(t, Counts{0, 0, 0, 0, 0}, tscb.cb.Counts())
	assert.True(t, tscb.cb.expiry.IsZero())
}
