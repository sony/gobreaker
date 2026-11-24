package gobreaker

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testRecovery = Recovery{
	Steps:        []float64{0.1, 0.25, 0.5, 0.75},
	StepDuration: time.Second,
}

func recoverySettings() Settings {
	recovery := testRecovery
	return Settings{
		Name:        "recovery",
		MaxRequests: 1,
		Timeout:     10 * time.Second,
		Recovery:    &recovery,
	}
}

// tripAndClose trips a closed CircuitBreaker at full load and closes it again,
// which starts recovery.
func tripAndClose(t *testing.T, cb *CircuitBreaker[bool]) {
	t.Helper()
	for i := 0; i < 6; i++ {
		require.NoError(t, fail(cb))
	}
	require.Equal(t, StateOpen, cb.State())
	reopenAndClose(t, cb)
}

// reopenAndClose waits out the open state and closes the CircuitBreaker from half-open.
func reopenAndClose(t *testing.T, cb *CircuitBreaker[bool]) {
	t.Helper()
	require.Equal(t, StateOpen, cb.State())
	pseudoSleep(cb, cb.timeout+time.Millisecond)
	require.Equal(t, StateHalfOpen, cb.State())
	require.NoError(t, succeed(cb))
	require.Equal(t, StateClosed, cb.State())
	require.True(t, cb.recovery.state.Active)
}

// letThrough sends n successful requests and returns how many the CircuitBreaker let through.
func letThrough(t *testing.T, cb *CircuitBreaker[bool], n int) int {
	t.Helper()
	passed := 0
	for i := 0; i < n; i++ {
		err := succeed(cb)
		if err == nil {
			passed++
			continue
		}
		require.ErrorIs(t, err, ErrRecovering)
	}
	return passed
}

func TestNewRecovery(t *testing.T) {
	assert.Nil(t, newRecovery(nil))
	assert.Nil(t, NewCircuitBreaker[bool](Settings{}).recovery)

	r := newRecovery(&Recovery{})
	assert.Equal(t, []float64{0.05, 0.1, 0.25, 0.5, 0.75}, r.shares)
	assert.Equal(t, 5*time.Second, r.stepDuration)
	assert.False(t, r.state.Active)

	r = newRecovery(&Recovery{
		Steps:        []float64{0.5, -1, 0, 0.1, 1, 2, 0.5, math.NaN()},
		StepDuration: -time.Second,
	})
	assert.Equal(t, []float64{0.1, 0.5}, r.shares)
	assert.Equal(t, 5*time.Second, r.stepDuration)

	r = newRecovery(&Recovery{Steps: []float64{0, 1}, StepDuration: time.Second})
	assert.Equal(t, []float64{0.05, 0.1, 0.25, 0.5, 0.75}, r.shares)
	assert.Equal(t, time.Second, r.stepDuration)

	steps := []float64{0.2, 0.4}
	r = newRecovery(&Recovery{Steps: steps})
	steps[0] = 0.9
	assert.Equal(t, []float64{0.2, 0.4}, r.shares, "the CircuitBreaker keeps its own copy of Steps")
}

func TestErrRecovering(t *testing.T) {
	assert.ErrorIs(t, ErrRecovering, ErrTooManyRequests)
	assert.NotErrorIs(t, ErrTooManyRequests, ErrRecovering)
}

func TestRecoveryLetsFirstRequestThroughAtTinyShare(t *testing.T) {
	// 1e-30 of 2^64 is less than 1; the step still lets its first request through.
	r := newRecovery(&Recovery{Steps: []float64{1e-30}})
	now := time.Now()
	r.onStateChange(StateClosed, now)

	_, ok := r.admit(now)
	assert.True(t, ok)
	_, ok = r.admit(now)
	assert.False(t, ok)
}

func TestRecoveryStepsUpToFullLoad(t *testing.T) {
	var changes []StateChange
	st := recoverySettings()
	st.OnStateChange = func(name string, from State, to State) {
		changes = append(changes, StateChange{name, from, to})
	}
	cb := NewCircuitBreaker[bool](st)

	// A new CircuitBreaker lets all requests through.
	assert.Equal(t, 100, letThrough(t, cb, 100))
	assert.False(t, cb.recovery.state.Active)

	tripAndClose(t, cb)
	assert.Equal(t, []StateChange{
		{"recovery", StateClosed, StateOpen},
		{"recovery", StateOpen, StateHalfOpen},
		{"recovery", StateHalfOpen, StateClosed},
	}, changes)

	for step, share := range testRecovery.Steps {
		before := cb.Counts().Requests
		passed := letThrough(t, cb, 1000)
		assert.Equal(t, step, cb.recovery.state.Step)
		assert.InDelta(t, share*1000, passed, 2, "step %d", step)
		assert.EqualValues(t, passed, cb.Counts().Requests-before, "rejected requests are not counted")
		assert.Equal(t, StateClosed, cb.State())
		pseudoSleep(cb, time.Second)
	}

	assert.Equal(t, 1000, letThrough(t, cb, 1000))
	assert.False(t, cb.recovery.state.Active)
	assert.Len(t, changes, 3, "recovery does not change the state")
}

func TestRecoveryWaitsForSuccess(t *testing.T) {
	tscb := NewTwoStepCircuitBreaker[bool](recoverySettings())
	cb := tscb.cb
	tripAndClose(t, cb)

	// No requests come for longer than the whole recovery: it stays at the first step.
	pseudoSleep(cb, time.Minute)
	assert.Equal(t, StateClosed, cb.State())
	assert.Equal(t, 0, cb.recovery.state.Step)

	// The first request at a step goes through; while it has not succeeded, the step goes on.
	done, err := tscb.Allow()
	require.NoError(t, err)
	pseudoSleep(cb, time.Minute)
	assert.Equal(t, StateClosed, cb.State())
	assert.Equal(t, 0, cb.recovery.state.Step)

	// Its success moves recovery one step further, not to the step the elapsed time would give.
	done(nil)
	assert.Equal(t, StateClosed, cb.State())
	assert.Equal(t, 1, cb.recovery.state.Step)
	assert.InDelta(t, 250, letThrough(t, cb, 1000), 2)
}

func TestRecoveryCountsOnlySuccessAtCurrentStep(t *testing.T) {
	tscb := NewTwoStepCircuitBreaker[bool](recoverySettings())
	cb := tscb.cb
	tripAndClose(t, cb)

	// A slow request is let through at the first step; another one succeeds there and the step ends.
	done, err := tscb.Allow()
	require.NoError(t, err)
	for letThrough(t, cb, 1) == 0 {
	}
	pseudoSleep(cb, testRecovery.StepDuration)
	assert.Equal(t, StateClosed, cb.State())
	assert.Equal(t, 1, cb.recovery.state.Step)

	// The slow request succeeds at the second step. It says nothing about the load of the second step,
	// so that step goes on until a request let through at it succeeds.
	done(nil)
	pseudoSleep(cb, time.Minute)
	assert.Equal(t, StateClosed, cb.State())
	assert.Equal(t, 1, cb.recovery.state.Step)
}

func TestRecoveryLetsAlternatingCallersThroughEqually(t *testing.T) {
	cb := NewCircuitBreaker[bool](recoverySettings())
	tripAndClose(t, cb)

	for _, step := range []int{0, 1, 2, 3} {
		share := testRecovery.Steps[step]
		var a, b int
		for i := 0; i < 1000; i++ {
			if succeed(cb) == nil {
				a++
			}
			if succeed(cb) == nil {
				b++
			}
		}
		assert.Equal(t, step, cb.recovery.state.Step)
		assert.InDelta(t, share*1000, a, 0.02*1000, "caller A at step %d", step)
		assert.InDelta(t, share*1000, b, 0.02*1000, "caller B at step %d", step)
		pseudoSleep(cb, time.Second)
	}
}

func TestTwoStepRecovery(t *testing.T) {
	tscb := NewTwoStepCircuitBreaker[bool](recoverySettings())
	tripAndClose(t, tscb.cb)

	passed := 0
	for i := 0; i < 1000; i++ {
		done, err := tscb.Allow()
		if err != nil {
			assert.True(t, errors.Is(err, ErrRecovering))
			continue
		}
		passed++
		done(nil)
	}
	assert.InDelta(t, 100, passed, 2)
}
