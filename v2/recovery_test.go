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

// failLetThrough sends failing requests until n of them are let through.
func failLetThrough(t *testing.T, cb *CircuitBreaker[bool], n int) {
	t.Helper()
	for passed := 0; passed < n; {
		err := fail(cb)
		if err == nil {
			passed++
			continue
		}
		require.ErrorIs(t, err, ErrRecovering)
	}
}

// nextStep makes a request at the current step succeed and waits the step out;
// recovery moves to the next step with the next request.
func nextStep(t *testing.T, cb *CircuitBreaker[bool]) {
	t.Helper()
	for letThrough(t, cb, 1) == 0 {
	}
	pseudoSleep(cb, testRecovery.StepDuration)
}

func TestNewRecovery(t *testing.T) {
	assert.Nil(t, newRecovery(nil))
	assert.Nil(t, NewCircuitBreaker[bool](Settings{}).recovery)

	r := newRecovery(&Recovery{})
	assert.Equal(t, []float64{0.05, 0.1, 0.25, 0.5, 0.75}, r.shares)
	assert.Equal(t, 5*time.Second, r.stepDuration)
	assert.False(t, r.state.Active)
	assert.Equal(t, notRecovering, r.state.Failed)

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

	// The full load lasts one more step before recovery ends.
	assert.Equal(t, 1000, letThrough(t, cb, 1000))
	assert.Equal(t, len(testRecovery.Steps), cb.recovery.state.Step)
	assert.True(t, cb.recovery.state.Active)

	pseudoSleep(cb, time.Second)
	assert.Equal(t, 1000, letThrough(t, cb, 1000))
	assert.False(t, cb.recovery.state.Active)
	assert.Equal(t, 0, cb.recovery.state.Resume)
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

func TestRecoveryResumesBelowFailedStep(t *testing.T) {
	cb := NewCircuitBreaker[bool](recoverySettings())
	tripAndClose(t, cb)
	assert.Equal(t, 0, cb.recovery.state.Step)

	// The service handles 10% and 25% of the load and fails at 50%: the first failure trips.
	nextStep(t, cb)
	nextStep(t, cb)
	assert.InDelta(t, 50, letThrough(t, cb, 100), 2)
	assert.Equal(t, 2, cb.recovery.state.Step)
	failLetThrough(t, cb, 1)
	assert.Equal(t, StateOpen, cb.State())
	assert.Equal(t, 1, cb.recovery.state.Resume)

	// A failure in the half-open state does not change what was learned.
	pseudoSleep(cb, cb.timeout+time.Millisecond)
	assert.Equal(t, StateHalfOpen, cb.State())
	assert.NoError(t, fail(cb))
	assert.Equal(t, StateOpen, cb.State())
	assert.Equal(t, 1, cb.recovery.state.Resume)

	// The next recovery starts at 25%, the highest step the service handled.
	reopenAndClose(t, cb)
	assert.Equal(t, 1, cb.recovery.state.Step)
	assert.InDelta(t, 250, letThrough(t, cb, 1000), 2)

	// It fails at 25% this time: the next recovery starts one step lower.
	failLetThrough(t, cb, 1)
	assert.Equal(t, StateOpen, cb.State())
	assert.Equal(t, 0, cb.recovery.state.Resume)

	reopenAndClose(t, cb)
	assert.Equal(t, 0, cb.recovery.state.Step)
	assert.InDelta(t, 100, letThrough(t, cb, 1000), 2)

	// A failure at the first step keeps the first step.
	failLetThrough(t, cb, 1)
	assert.Equal(t, StateOpen, cb.State())
	assert.Equal(t, 0, cb.recovery.state.Resume)
}

func TestRecoveryForgetsFailedStepAtFullLoad(t *testing.T) {
	cb := NewCircuitBreaker[bool](recoverySettings())
	tripAndClose(t, cb)
	nextStep(t, cb)
	nextStep(t, cb)
	nextStep(t, cb)
	failLetThrough(t, cb, 1)
	assert.Equal(t, StateOpen, cb.State())
	assert.Equal(t, 2, cb.recovery.state.Resume)

	reopenAndClose(t, cb)
	assert.Equal(t, 2, cb.recovery.state.Step)

	// From the third step, two steps later comes the full load, still part of recovery.
	nextStep(t, cb)
	nextStep(t, cb)
	assert.Equal(t, 100, letThrough(t, cb, 100))
	assert.Equal(t, len(testRecovery.Steps), cb.recovery.state.Step)
	assert.True(t, cb.recovery.state.Active)

	// A trip at the full load during recovery: the next recovery starts from the last partial step.
	assert.NoError(t, fail(cb))
	assert.Equal(t, StateOpen, cb.State())
	assert.Equal(t, 3, cb.recovery.state.Resume)

	reopenAndClose(t, cb)
	assert.Equal(t, 3, cb.recovery.state.Step)
	assert.InDelta(t, 750, letThrough(t, cb, 1000), 2)

	// Recovery that holds the full load for one step ends and forgets the failed step.
	nextStep(t, cb)
	nextStep(t, cb)
	assert.Equal(t, 100, letThrough(t, cb, 100))
	assert.False(t, cb.recovery.state.Active)
	assert.Equal(t, 0, cb.recovery.state.Resume)

	// After recovery Settings.ReadyToTrip decides again (more than 5 consecutive failures),
	// and a trip at the full load starts the next recovery from the first step.
	for i := 0; i < 5; i++ {
		assert.NoError(t, fail(cb))
	}
	assert.Equal(t, StateClosed, cb.State())
	assert.NoError(t, fail(cb))
	assert.Equal(t, StateOpen, cb.State())
	assert.Equal(t, 0, cb.recovery.state.Resume)
	reopenAndClose(t, cb)
	assert.Equal(t, 0, cb.recovery.state.Step)
}

func TestRecoveryLearnsStepOfAdmission(t *testing.T) {
	tscb := NewTwoStepCircuitBreaker[bool](recoverySettings())
	cb := tscb.cb
	tripAndClose(t, cb)

	// A slow request is let through at the third step...
	nextStep(t, cb)
	nextStep(t, cb)
	done, err := tscb.Allow()
	require.NoError(t, err)
	assert.Equal(t, 2, cb.recovery.state.Step)

	// ...and fails two steps later, at the full load.
	nextStep(t, cb)
	nextStep(t, cb)
	assert.Equal(t, 100, letThrough(t, cb, 100))
	assert.Equal(t, len(testRecovery.Steps), cb.recovery.state.Step)
	done(errFailed)
	assert.Equal(t, StateOpen, cb.State())

	// The service failed with the load of the third step, so the next recovery starts from the second.
	assert.Equal(t, 1, cb.recovery.state.Resume)
}

func TestRecoveryLearnsLowestFailedStep(t *testing.T) {
	st := recoverySettings()
	st.Recovery.ReadyToTrip = func(counts Counts) bool { return counts.TotalFailures >= 3 }
	tscb := NewTwoStepCircuitBreaker[bool](st)
	cb := tscb.cb
	tripAndClose(t, cb)

	// A slow request is let through at 50%.
	nextStep(t, cb)
	nextStep(t, cb)
	slow, err := tscb.Allow()
	require.NoError(t, err)
	assert.Equal(t, 2, cb.recovery.state.Step)
	nextStep(t, cb)

	// A failure at 75% is tolerated, then the slow request fails, then a failure at the full load trips.
	failLetThrough(t, cb, 1)
	assert.Equal(t, 3, cb.recovery.state.Step)
	slow(errFailed)
	nextStep(t, cb)
	failLetThrough(t, cb, 1)
	assert.Equal(t, len(testRecovery.Steps), cb.recovery.state.Step)
	assert.Equal(t, StateOpen, cb.State())

	// Neither the first failure nor the last decides but the lowest step that failed:
	// the next recovery starts at 25%, one step below 50%.
	assert.Equal(t, 1, cb.recovery.state.Resume)
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

func TestRecoveryTripsOnFirstFailure(t *testing.T) {
	cb := NewCircuitBreaker[bool](recoverySettings())
	tripAndClose(t, cb)

	// Settings.ReadyToTrip needs more than 5 consecutive failures; during recovery one is enough.
	assert.InDelta(t, 100, letThrough(t, cb, 1000), 2)
	failLetThrough(t, cb, 1)
	assert.Equal(t, StateOpen, cb.State())
}

func TestRecoveryKeepsCountsAcrossInterval(t *testing.T) {
	st := recoverySettings()
	st.Interval = time.Second // a fixed window
	tscb := NewTwoStepCircuitBreaker[bool](st)
	cb := tscb.cb
	tripAndClose(t, cb)

	// A request let through during recovery fails after the window has passed:
	// the failure is not dropped with the window, and it trips the CircuitBreaker.
	done, err := tscb.Allow()
	require.NoError(t, err)
	pseudoSleep(cb, 2*time.Second)
	assert.Equal(t, StateClosed, cb.State())
	done(errFailed)
	assert.Equal(t, StateOpen, cb.State())

	// After recovery the window clears Counts at Interval again, as without Recovery.
	// The clearing put off during recovery comes first, so the first request after recovery counts.
	reopenAndClose(t, cb)
	for i := 0; i <= len(testRecovery.Steps); i++ {
		nextStep(t, cb)
	}
	require.NoError(t, fail(cb))
	require.False(t, cb.recovery.state.Active)
	assert.Equal(t, Counts{Requests: 1, TotalFailures: 1, ConsecutiveFailures: 1}, cb.Counts())
	pseudoSleep(cb, time.Second+time.Millisecond)
	assert.Equal(t, StateClosed, cb.State())
	assert.Equal(t, Counts{}, cb.Counts())
}

func TestRecoveryReadyToTrip(t *testing.T) {
	st := recoverySettings()
	var seen []Counts
	st.Recovery.ReadyToTrip = func(counts Counts) bool {
		seen = append(seen, counts)
		return counts.Requests >= 20 && counts.TotalFailures*2 > counts.Requests
	}
	cb := NewCircuitBreaker[bool](st)

	// Outside recovery Settings.ReadyToTrip decides.
	tripAndClose(t, cb)
	assert.Empty(t, seen)

	// During recovery Recovery.ReadyToTrip decides, and it tolerates a few failures.
	// 1 of 20 requests let through succeeds: Counts hold only them, not the rejected ones.
	assert.NoError(t, succeed(cb))
	failLetThrough(t, cb, 18)
	assert.Equal(t, StateClosed, cb.State())
	assert.Len(t, seen, 18)
	failLetThrough(t, cb, 1)
	assert.Equal(t, StateOpen, cb.State())
	assert.Equal(t, Counts{Requests: 20, TotalSuccesses: 1, TotalFailures: 19, ConsecutiveFailures: 19}, seen[len(seen)-1])
	assert.Equal(t, 0, cb.recovery.state.Resume)

	// After recovery Settings.ReadyToTrip decides again.
	reopenAndClose(t, cb)
	seen = nil
	for i := 0; i <= len(testRecovery.Steps); i++ {
		nextStep(t, cb)
	}
	assert.Equal(t, 100, letThrough(t, cb, 100))
	assert.False(t, cb.recovery.state.Active)
	assert.NoError(t, fail(cb))
	assert.Empty(t, seen)
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

func TestRecoveryStepOutOfRange(t *testing.T) {
	// An instance with more Steps shared a step this one does not have.
	r := newRecovery(&testRecovery)
	now := time.Now()
	r.state = RecoveryState{Active: true, Step: 9, StepStart: now, Failed: notRecovering}

	step, ok := r.admit(now)
	assert.Equal(t, len(testRecovery.Steps), step)
	assert.True(t, ok)
	r.onSuccess(step)
	r.update(now.Add(testRecovery.StepDuration))
	assert.False(t, r.state.Active)

	r.state = RecoveryState{Resume: -3, Failed: notRecovering}
	r.onStateChange(StateClosed, now)
	assert.Equal(t, 0, r.state.Step)
}

func TestDistributedRecovery(t *testing.T) {
	store := NewMockStore()
	recovery := testRecovery
	st := Settings{
		Name:        "TestRecovery",
		MaxRequests: 1,
		Timeout:     2 * time.Second,
		Recovery:    &recovery,
	}
	dcb1, err := NewDistributedCircuitBreaker[any](store, st)
	require.NoError(t, err)
	dcb2, err := NewDistributedCircuitBreaker[any](store, st)
	require.NoError(t, err)

	shared, err := dcb1.getSharedState()
	require.NoError(t, err)
	require.NotNil(t, shared.Recovery)
	assert.False(t, shared.Recovery.Active)

	for i := 0; i < 6; i++ {
		assert.NoError(t, failRequest(dcb1))
	}
	assertState(t, dcb1, StateOpen)

	dcbPseudoSleep(dcb1, st.Timeout+time.Millisecond)
	assertState(t, dcb1, StateHalfOpen)
	assert.NoError(t, successRequest(dcb1))
	assertState(t, dcb1, StateClosed)

	// The other instance follows the recovery started by the first one.
	passed := 0
	for i := 0; i < 1000; i++ {
		err := successRequest(dcb2)
		if err == nil {
			passed++
			continue
		}
		assert.ErrorIs(t, err, ErrRecovering)
	}
	assert.InDelta(t, 100, passed, 2)

	shared, err = dcb1.getSharedState()
	require.NoError(t, err)
	require.NotNil(t, shared.Recovery)
	assert.True(t, shared.Recovery.Active)
	assert.True(t, shared.Recovery.StepSucceeded)
	assert.Equal(t, uint64(1000), shared.Recovery.Seq)

	// Without Recovery, the shared state has none.
	plain, err := NewDistributedCircuitBreaker[any](NewMockStore(), Settings{Name: "plain"})
	require.NoError(t, err)
	shared, err = plain.getSharedState()
	require.NoError(t, err)
	assert.Nil(t, shared.Recovery)
	data, err := plain.store.GetData(plain.sharedStateKey())
	require.NoError(t, err)
	assert.NotContains(t, string(data), `"recovery"`, "other instances read the same JSON as before")
}

// dcbStepSleep moves the start of the shared recovery step back by period.
func dcbStepSleep(t *testing.T, dcb *DistributedCircuitBreaker[any], period time.Duration) {
	t.Helper()
	shared, err := dcb.getSharedState()
	require.NoError(t, err)
	require.NotNil(t, shared.Recovery)
	shared.Recovery.StepStart = shared.Recovery.StepStart.Add(-period)
	require.NoError(t, dcb.setSharedState(shared))
}

// dcbLetThrough sends n successful requests and returns how many the breaker let through.
func dcbLetThrough(t *testing.T, dcb *DistributedCircuitBreaker[any], n int) int {
	t.Helper()
	passed := 0
	for i := 0; i < n; i++ {
		err := successRequest(dcb)
		if err == nil {
			passed++
			continue
		}
		require.ErrorIs(t, err, ErrRecovering)
	}
	return passed
}

func TestDistributedRecoveryLearnsStep(t *testing.T) {
	store := NewMockStore()
	recovery := testRecovery
	st := Settings{
		Name:        "TestRecoveryLearns",
		MaxRequests: 1,
		Timeout:     2 * time.Second,
		Recovery:    &recovery,
	}
	dcb1, err := NewDistributedCircuitBreaker[any](store, st)
	require.NoError(t, err)
	dcb2, err := NewDistributedCircuitBreaker[any](store, st)
	require.NoError(t, err)

	for i := 0; i < 6; i++ {
		require.NoError(t, failRequest(dcb1))
	}
	dcbPseudoSleep(dcb1, st.Timeout+time.Millisecond)
	assertState(t, dcb1, StateHalfOpen)
	require.NoError(t, successRequest(dcb1))
	assertState(t, dcb1, StateClosed)

	// The first instance takes recovery up to 50%, where a request fails and trips the breaker.
	for step := 0; step < 2; step++ {
		for dcbLetThrough(t, dcb1, 1) == 0 {
		}
		dcbStepSleep(t, dcb1, time.Second)
	}
	for {
		err := failRequest(dcb1)
		if err == nil {
			break
		}
		require.ErrorIs(t, err, ErrRecovering)
	}
	assertState(t, dcb2, StateOpen)

	// The second instance closes the breaker again, and recovery resumes at 25%, below the failed step.
	dcbPseudoSleep(dcb2, st.Timeout+time.Millisecond)
	assertState(t, dcb2, StateHalfOpen)
	require.NoError(t, successRequest(dcb2))
	assertState(t, dcb2, StateClosed)
	assert.InDelta(t, 250, dcbLetThrough(t, dcb2, 1000), 2)
	shared, err := dcb2.getSharedState()
	require.NoError(t, err)
	assert.Equal(t, 1, shared.Recovery.Step)
}
